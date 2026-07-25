package httpapi

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/arrorLabArts/heatcheck/internal/mailer"
	"github.com/arrorLabArts/heatcheck/internal/store"
	"github.com/go-chi/chi/v5"
)

type createReportRequest struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Reason     string `json:"reason"`
	Details    string `json:"details"`
}

func (a *API) createReport(w http.ResponseWriter, r *http.Request) {
	var request createReportRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.TargetType = strings.TrimSpace(request.TargetType)
	request.TargetID = strings.TrimSpace(request.TargetID)
	request.Reason = strings.TrimSpace(request.Reason)
	request.Details = strings.TrimSpace(request.Details)
	targetTypes := map[string]bool{"submission": true, "user": true, "challenge": true}
	reasons := map[string]bool{
		"harassment": true, "hate": true, "sexual_content": true,
		"violence": true, "self_harm": true, "privacy": true,
		"spam": true, "cheating": true, "copyright": true,
		"underage": true, "child_safety": true, "other": true,
	}
	validation := map[string]string{}
	if !targetTypes[request.TargetType] {
		validation["target_type"] = "must be submission, user, or challenge"
	}
	if request.TargetID == "" {
		validation["target_id"] = "is required"
	}
	if !reasons[request.Reason] {
		validation["reason"] = "is not a supported report reason"
	}
	if len(request.Details) > 2000 {
		validation["details"] = "must contain at most 2000 characters"
	}
	if request.Reason == "other" && len(request.Details) < 10 {
		validation["details"] = "must explain reports with reason other"
	}
	if len(validation) > 0 {
		validationError(w, validation)
		return
	}
	priority := "normal"
	switch request.Reason {
	case "child_safety", "self_harm":
		priority = "urgent"
	case "violence", "privacy", "underage":
		priority = "high"
	}
	user, _ := userFromContext(r.Context())
	var alert any
	if priority == "urgent" {
		textBody := fmt.Sprintf(
			"Urgent HeatCheck safety report\n\nReporter: %s\nTarget: %s/%s\nReason: %s\nDetails: %s",
			user.ID,
			request.TargetType,
			request.TargetID,
			request.Reason,
			request.Details,
		)
		alert = mailer.Message{
			To:       a.safetyAlertEmail,
			Subject:  "Urgent HeatCheck safety report",
			TextBody: textBody,
			HTMLBody: "<pre>" + html.EscapeString(textBody) + "</pre>",
		}
	}
	report, err := a.store.CreateReport(r.Context(), store.CreateReportParams{
		ReporterID: user.ID,
		TargetType: request.TargetType,
		TargetID:   request.TargetID,
		Reason:     request.Reason,
		Details:    request.Details,
		Priority:   priority,
		AlertEmail: alert,
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": report})
}

func (a *API) blockUser(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	if err := a.store.BlockUser(r.Context(), user.ID, chi.URLParam(r, "userID")); err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func (a *API) unblockUser(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	if err := a.store.UnblockUser(r.Context(), user.ID, chi.URLParam(r, "userID")); err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

type copyrightNoticeRequest struct {
	ClaimantName    string  `json:"claimant_name"`
	ClaimantEmail   string  `json:"claimant_email"`
	ClaimantAddress string  `json:"claimant_address"`
	Relationship    string  `json:"relationship"`
	CopyrightedWork string  `json:"copyrighted_work"`
	InfringingURL   string  `json:"infringing_url"`
	SubmissionID    *string `json:"submission_id"`
	GoodFaith       bool    `json:"good_faith"`
	Accuracy        bool    `json:"accuracy"`
	Signature       string  `json:"signature"`
}

func (a *API) createCopyrightNotice(w http.ResponseWriter, r *http.Request) {
	var request copyrightNoticeRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.ClaimantName = strings.TrimSpace(request.ClaimantName)
	request.ClaimantEmail = strings.ToLower(strings.TrimSpace(request.ClaimantEmail))
	request.ClaimantAddress = strings.TrimSpace(request.ClaimantAddress)
	request.Relationship = strings.TrimSpace(request.Relationship)
	request.CopyrightedWork = strings.TrimSpace(request.CopyrightedWork)
	request.InfringingURL = strings.TrimSpace(request.InfringingURL)
	request.Signature = strings.TrimSpace(request.Signature)
	validation := map[string]string{}
	requiredLengths := map[string]struct {
		value string
		max   int
	}{
		"claimant_name":    {request.ClaimantName, 200},
		"claimant_address": {request.ClaimantAddress, 1000},
		"relationship":     {request.Relationship, 500},
		"copyrighted_work": {request.CopyrightedWork, 4000},
		"signature":        {request.Signature, 200},
	}
	for field, requirement := range requiredLengths {
		if requirement.value == "" || len(requirement.value) > requirement.max {
			validation[field] = "is required and exceeds the allowed length"
		}
	}
	if !validEmail(request.ClaimantEmail) {
		validation["claimant_email"] = "must be a valid email address"
	}
	if !validHTTPURL(request.InfringingURL) {
		validation["infringing_url"] = "must be a valid http or https URL"
	}
	if !request.GoodFaith {
		validation["good_faith"] = "must be affirmed"
	}
	if !request.Accuracy {
		validation["accuracy"] = "must be affirmed"
	}
	if len(validation) > 0 {
		validationError(w, validation)
		return
	}
	notice, err := a.store.CreateCopyrightNotice(r.Context(), store.CreateCopyrightNoticeParams{
		ClaimantName:    request.ClaimantName,
		ClaimantEmail:   request.ClaimantEmail,
		ClaimantAddress: request.ClaimantAddress,
		Relationship:    request.Relationship,
		CopyrightedWork: request.CopyrightedWork,
		InfringingURL:   request.InfringingURL,
		SubmissionID:    request.SubmissionID,
		GoodFaith:       request.GoodFaith,
		Accuracy:        request.Accuracy,
		Signature:       request.Signature,
		Notifications: []store.EmailNotification{
			{
				Kind: "email.copyright_receipt",
				Payload: mailer.Message{
					To:       request.ClaimantEmail,
					Subject:  "HeatCheck copyright notice received",
					TextBody: "HeatCheck received your copyright notice. Our legal moderation queue will review it and contact you if more information is required.",
					HTMLBody: "<p>HeatCheck received your copyright notice.</p><p>Our legal moderation queue will review it and contact you if more information is required.</p>",
				},
			},
			{
				Kind: "email.legal_alert",
				Payload: mailer.Message{
					To:      a.legalAlertEmail,
					Subject: "New HeatCheck copyright notice",
					TextBody: fmt.Sprintf(
						"Claimant: %s <%s>\nInfringing URL: %s\nCopyrighted work: %s",
						request.ClaimantName,
						request.ClaimantEmail,
						request.InfringingURL,
						request.CopyrightedWork,
					),
					HTMLBody: "<p><strong>Claimant:</strong> " + html.EscapeString(request.ClaimantName) +
						" &lt;" + html.EscapeString(request.ClaimantEmail) + "&gt;</p><p><strong>Infringing URL:</strong> " +
						html.EscapeString(request.InfringingURL) + "</p><p><strong>Copyrighted work:</strong> " +
						html.EscapeString(request.CopyrightedWork) + "</p>",
				},
			},
		},
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"data": map[string]any{
			"id":         notice.ID,
			"status":     notice.Status,
			"created_at": notice.CreatedAt,
		},
	})
}

type counterNoticeRequest struct {
	FullName         string `json:"full_name"`
	Address          string `json:"address"`
	Phone            string `json:"phone"`
	Email            string `json:"email"`
	GoodFaith        bool   `json:"good_faith"`
	ConsentToProcess bool   `json:"consent_to_process"`
	Signature        string `json:"signature"`
}

func (a *API) createCounterNotice(w http.ResponseWriter, r *http.Request) {
	var request counterNoticeRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.FullName = strings.TrimSpace(request.FullName)
	request.Address = strings.TrimSpace(request.Address)
	request.Phone = strings.TrimSpace(request.Phone)
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	request.Signature = strings.TrimSpace(request.Signature)
	validation := map[string]string{}
	if request.FullName == "" || len(request.FullName) > 200 {
		validation["full_name"] = "is required and must contain at most 200 characters"
	}
	if request.Address == "" || len(request.Address) > 1000 {
		validation["address"] = "is required and must contain at most 1000 characters"
	}
	if request.Phone == "" || len(request.Phone) > 50 {
		validation["phone"] = "is required and must contain at most 50 characters"
	}
	if !validEmail(request.Email) {
		validation["email"] = "must be a valid email address"
	}
	if request.Signature == "" || len(request.Signature) > 200 {
		validation["signature"] = "is required and must contain at most 200 characters"
	}
	if !request.GoodFaith {
		validation["good_faith"] = "must be affirmed"
	}
	if !request.ConsentToProcess {
		validation["consent_to_process"] = "must be affirmed"
	}
	if len(validation) > 0 {
		validationError(w, validation)
		return
	}
	user, _ := userFromContext(r.Context())
	noticeID := chi.URLParam(r, "noticeID")
	counterLink := a.publicAppURL + "/copyright/notices/" + url.PathEscape(noticeID)
	legalBody := fmt.Sprintf(
		"HeatCheck copyright counter-notice received\n\nNotice: %s\nUploader: %s\nName: %s\nEmail: %s\nAddress: %s\nPhone: %s\nReview: %s",
		noticeID,
		user.ID,
		request.FullName,
		request.Email,
		request.Address,
		request.Phone,
		counterLink,
	)
	counter, err := a.store.CreateCounterNotice(r.Context(), store.CreateCounterNoticeParams{
		NoticeID:         noticeID,
		UserID:           user.ID,
		FullName:         request.FullName,
		Address:          request.Address,
		Phone:            request.Phone,
		Email:            request.Email,
		GoodFaith:        request.GoodFaith,
		ConsentToProcess: request.ConsentToProcess,
		Signature:        request.Signature,
		Notifications: []store.EmailNotification{
			{
				Kind: "email.copyright_counter_receipt",
				Payload: mailer.Message{
					To:       request.Email,
					Subject:  "HeatCheck counter-notice received",
					TextBody: "HeatCheck received your copyright counter-notice. The legal moderation team will review it and contact you if further information is required.",
					HTMLBody: "<p>HeatCheck received your copyright counter-notice.</p><p>The legal moderation team will review it and contact you if further information is required.</p>",
				},
			},
			{
				Kind: "email.legal_alert",
				Payload: mailer.Message{
					To:       a.legalAlertEmail,
					Subject:  "HeatCheck copyright counter-notice received",
					TextBody: legalBody,
					HTMLBody: "<pre>" + html.EscapeString(legalBody) + "</pre>",
				},
			},
		},
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"data": map[string]any{
			"id":         counter.ID,
			"notice_id":  counter.NoticeID,
			"status":     counter.Status,
			"created_at": counter.CreatedAt,
		},
	})
}

type createAppealRequest struct {
	Reason string `json:"reason"`
}

func (a *API) createAppeal(w http.ResponseWriter, r *http.Request) {
	var request createAppealRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if len(request.Reason) < 20 || len(request.Reason) > 2000 {
		validationError(w, map[string]string{"reason": "must contain 20-2000 characters"})
		return
	}
	user, _ := userFromContext(r.Context())
	appeal, err := a.store.CreateAppeal(
		r.Context(),
		user.ID,
		chi.URLParam(r, "actionID"),
		request.Reason,
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": appeal})
}

func validHTTPURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil &&
		(parsed.Scheme == "http" || parsed.Scheme == "https") &&
		parsed.Host != ""
}
