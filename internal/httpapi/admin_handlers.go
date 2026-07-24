package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/store"
	"github.com/go-chi/chi/v5"
)

func (a *API) listReports(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && status != "open" && status != "reviewing" &&
		status != "resolved" && status != "dismissed" {
		validationError(w, map[string]string{"status": "is not supported"})
		return
	}
	limit, offset := pagination(r)
	reports, err := a.store.ListReports(r.Context(), status, limit, offset)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": reports,
		"pagination": map[string]int{
			"limit": limit, "offset": offset,
		},
	})
}

type dismissReportRequest struct {
	Note string `json:"note"`
}

func (a *API) dismissReport(w http.ResponseWriter, r *http.Request) {
	var request dismissReportRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.Note = strings.TrimSpace(request.Note)
	if len(request.Note) < 10 || len(request.Note) > 2000 {
		validationError(w, map[string]string{"note": "must contain 10-2000 characters"})
		return
	}
	user, _ := userFromContext(r.Context())
	report, err := a.store.DismissReport(
		r.Context(),
		chi.URLParam(r, "reportID"),
		user.ID,
		request.Note,
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": report})
}

type moderationActionRequest struct {
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	Action     string          `json:"action"`
	Reason     string          `json:"reason"`
	Notes      string          `json:"notes"`
	ReportID   *string         `json:"report_id"`
	Metadata   json.RawMessage `json:"metadata"`
}

func (a *API) createModerationAction(w http.ResponseWriter, r *http.Request) {
	var request moderationActionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.TargetType = strings.TrimSpace(request.TargetType)
	request.TargetID = strings.TrimSpace(request.TargetID)
	request.Action = strings.TrimSpace(request.Action)
	request.Reason = strings.TrimSpace(request.Reason)
	request.Notes = strings.TrimSpace(request.Notes)
	validCombination := map[string]bool{
		"submission:approve": true, "submission:restore": true,
		"submission:reject": true, "submission:remove": true,
		"user:suspend": true, "user:unsuspend": true, "user:warn": true,
		"challenge:close": true, "challenge:archive": true,
	}
	validation := map[string]string{}
	if !validCombination[request.TargetType+":"+request.Action] {
		validation["action"] = "is not valid for the specified target type"
	}
	if request.TargetID == "" {
		validation["target_id"] = "is required"
	}
	if len(request.Reason) < 3 || len(request.Reason) > 200 {
		validation["reason"] = "must contain 3-200 characters"
	}
	if len(request.Notes) > 2000 {
		validation["notes"] = "must contain at most 2000 characters"
	}
	if len(request.Metadata) > 0 && !json.Valid(request.Metadata) {
		validation["metadata"] = "must be valid JSON"
	}
	if len(validation) > 0 {
		validationError(w, validation)
		return
	}
	user, _ := userFromContext(r.Context())
	action, err := a.store.CreateModerationAction(r.Context(), store.CreateModerationActionParams{
		ModeratorID: user.ID,
		TargetType:  request.TargetType,
		TargetID:    request.TargetID,
		Action:      request.Action,
		Reason:      request.Reason,
		Notes:       request.Notes,
		ReportID:    request.ReportID,
		Metadata:    request.Metadata,
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": action})
}

func (a *API) listModerationSubmissions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending" && status != "approved" &&
		status != "rejected" && status != "removed" {
		validationError(w, map[string]string{"status": "is not supported"})
		return
	}
	limit, offset := pagination(r)
	submissions, err := a.store.ListModerationSubmissions(r.Context(), status, limit, offset)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	for index := range submissions {
		if err := a.attachClipURL(r, &submissions[index]); err != nil {
			a.logger.Error("presign moderation clip", "error", err, "submission_id", submissions[index].ID)
			writeError(w, http.StatusBadGateway, "storage_error", "Clips are temporarily unavailable.", nil)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": submissions,
		"pagination": map[string]int{
			"limit": limit, "offset": offset,
		},
	})
}

type verificationRequest struct {
	Status  string          `json:"status"`
	Details json.RawMessage `json:"details"`
}

func (a *API) updateVerification(w http.ResponseWriter, r *http.Request) {
	var request verificationRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	if request.Status != "pending" && request.Status != "passed" &&
		request.Status != "failed" && request.Status != "manual_review" {
		validationError(w, map[string]string{"status": "is not supported"})
		return
	}
	if len(request.Details) == 0 {
		request.Details = json.RawMessage(`{}`)
	}
	if !json.Valid(request.Details) {
		validationError(w, map[string]string{"details": "must be valid JSON"})
		return
	}
	user, _ := userFromContext(r.Context())
	submission, err := a.store.UpdateVerification(
		r.Context(),
		chi.URLParam(r, "submissionID"),
		request.Status,
		request.Details,
		user.ID,
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if err := a.attachClipURL(r, &submission); err != nil {
		a.logger.Warn("presign verified clip", "error", err, "submission_id", submission.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": submission})
}

func (a *API) listAppeals(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending" && status != "upheld" && status != "reversed" {
		validationError(w, map[string]string{"status": "is not supported"})
		return
	}
	limit, offset := pagination(r)
	appeals, err := a.store.ListAppeals(r.Context(), status, limit, offset)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": appeals,
		"pagination": map[string]int{
			"limit": limit, "offset": offset,
		},
	})
}

type reviewAppealRequest struct {
	Status         string `json:"status"`
	ResolutionNote string `json:"resolution_note"`
}

func (a *API) reviewAppeal(w http.ResponseWriter, r *http.Request) {
	var request reviewAppealRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.ResolutionNote = strings.TrimSpace(request.ResolutionNote)
	if request.Status != "upheld" && request.Status != "reversed" {
		validationError(w, map[string]string{"status": "must be upheld or reversed"})
		return
	}
	if len(request.ResolutionNote) < 10 || len(request.ResolutionNote) > 2000 {
		validationError(w, map[string]string{"resolution_note": "must contain 10-2000 characters"})
		return
	}
	user, _ := userFromContext(r.Context())
	appeal, err := a.store.ReviewAppeal(
		r.Context(),
		chi.URLParam(r, "appealID"),
		user.ID,
		request.Status,
		request.ResolutionNote,
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": appeal})
}

func (a *API) listCopyrightNotices(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	validStatuses := map[string]bool{
		"": true, "received": true, "reviewing": true, "actioned": true,
		"rejected": true, "countered": true, "restored": true, "closed": true,
	}
	if !validStatuses[status] {
		validationError(w, map[string]string{"status": "is not supported"})
		return
	}
	limit, offset := pagination(r)
	notices, err := a.store.ListCopyrightNotices(r.Context(), status, limit, offset)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": notices,
		"pagination": map[string]int{
			"limit": limit, "offset": offset,
		},
	})
}

type reviewCopyrightNoticeRequest struct {
	Status           string     `json:"status"`
	ResolutionNote   string     `json:"resolution_note"`
	CounterNoticeDue *time.Time `json:"counter_notice_due"`
}

func (a *API) reviewCopyrightNotice(w http.ResponseWriter, r *http.Request) {
	var request reviewCopyrightNoticeRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.ResolutionNote = strings.TrimSpace(request.ResolutionNote)
	validStatuses := map[string]bool{
		"reviewing": true, "actioned": true, "rejected": true,
		"restored": true, "closed": true,
	}
	validation := map[string]string{}
	if !validStatuses[request.Status] {
		validation["status"] = "is not a supported review outcome"
	}
	if len(request.ResolutionNote) < 10 || len(request.ResolutionNote) > 2000 {
		validation["resolution_note"] = "must contain 10-2000 characters"
	}
	if len(validation) > 0 {
		validationError(w, validation)
		return
	}
	user, _ := userFromContext(r.Context())
	notice, err := a.store.ReviewCopyrightNotice(r.Context(), store.ReviewCopyrightNoticeParams{
		NoticeID:         chi.URLParam(r, "noticeID"),
		ActorID:          user.ID,
		Status:           request.Status,
		ResolutionNote:   request.ResolutionNote,
		CounterNoticeDue: request.CounterNoticeDue,
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": notice})
}

func (a *API) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	events, err := a.store.ListAuditEvents(r.Context(), limit, offset)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": events,
		"pagination": map[string]int{
			"limit": limit, "offset": offset,
		},
	})
}

type publishPolicyRequest struct {
	Kind               string    `json:"kind"`
	Version            string    `json:"version"`
	Title              string    `json:"title"`
	Content            string    `json:"content"`
	RequiresAcceptance bool      `json:"requires_acceptance"`
	EffectiveAt        time.Time `json:"effective_at"`
}

func (a *API) publishPolicy(w http.ResponseWriter, r *http.Request) {
	var request publishPolicyRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.Kind = strings.TrimSpace(request.Kind)
	request.Version = strings.TrimSpace(request.Version)
	request.Title = strings.TrimSpace(request.Title)
	request.Content = strings.TrimSpace(request.Content)
	validation := map[string]string{}
	if !slugPattern.MatchString(strings.ReplaceAll(request.Kind, "_", "-")) ||
		len(request.Kind) > 80 {
		validation["kind"] = "must be a lowercase identifier"
	}
	if request.Version == "" || len(request.Version) > 80 {
		validation["version"] = "is required and must contain at most 80 characters"
	}
	if len(request.Title) < 3 || len(request.Title) > 200 {
		validation["title"] = "must contain 3-200 characters"
	}
	if len(request.Content) < 100 || len(request.Content) > 100_000 {
		validation["content"] = "must contain 100-100000 characters"
	}
	if request.EffectiveAt.IsZero() {
		validation["effective_at"] = "is required"
	} else if request.EffectiveAt.After(time.Now().UTC().Add(5 * time.Minute)) {
		validation["effective_at"] = "cannot be scheduled in the future"
	}
	if len(validation) > 0 {
		validationError(w, validation)
		return
	}
	user, _ := userFromContext(r.Context())
	policy, err := a.store.PublishPolicy(r.Context(), store.PublishPolicyParams{
		Kind:               request.Kind,
		Version:            request.Version,
		Title:              request.Title,
		Content:            request.Content,
		RequiresAcceptance: request.RequiresAcceptance,
		EffectiveAt:        request.EffectiveAt,
		ActorID:            user.ID,
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": policy})
}
