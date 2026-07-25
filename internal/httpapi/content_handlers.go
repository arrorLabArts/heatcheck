package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/arrorLabArts/heatcheck/internal/sharecard"
	"github.com/arrorLabArts/heatcheck/internal/store"
	"github.com/go-chi/chi/v5"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func (a *API) listChallenges(w http.ResponseWriter, r *http.Request) {
	user, authenticated := userFromContext(r.Context())
	includePrivate := authenticated &&
		(user.Role == domain.RoleModerator || user.Role == domain.RoleAdmin) &&
		r.URL.Query().Get("include_private") == "true"
	limit, offset := pagination(r)
	challenges, err := a.store.ListChallenges(r.Context(), includePrivate, limit, offset)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": challenges,
		"pagination": map[string]int{
			"limit": limit, "offset": offset,
		},
	})
}

func (a *API) dailyChallenge(w http.ResponseWriter, r *http.Request) {
	challenge, err := a.store.GetDailyChallenge(r.Context(), time.Now().UTC())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": challenge})
}

func (a *API) getChallenge(w http.ResponseWriter, r *http.Request) {
	challenge, err := a.store.GetChallenge(r.Context(), chi.URLParam(r, "challengeID"))
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if challenge.Visibility != "public" || challenge.Status == "draft" || challenge.Status == "archived" {
		user, ok := userFromContext(r.Context())
		if !ok || (user.Role != domain.RoleModerator && user.Role != domain.RoleAdmin) {
			writeError(w, http.StatusNotFound, "not_found", "The requested resource was not found.", nil)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": challenge})
}

type createChallengeRequest struct {
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Rules       json.RawMessage `json:"rules"`
	Status      string          `json:"status"`
	Visibility  string          `json:"visibility"`
	StartsAt    time.Time       `json:"starts_at"`
	EndsAt      time.Time       `json:"ends_at"`
}

func (a *API) createChallenge(w http.ResponseWriter, r *http.Request) {
	var request createChallengeRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.Slug = strings.ToLower(strings.TrimSpace(request.Slug))
	request.Title = strings.TrimSpace(request.Title)
	request.Description = strings.TrimSpace(request.Description)
	if request.Status == "" {
		request.Status = "draft"
	}
	if request.Visibility == "" {
		request.Visibility = "public"
	}

	validation := map[string]string{}
	if !slugPattern.MatchString(request.Slug) || len(request.Slug) > 80 {
		validation["slug"] = "must be a lowercase hyphenated slug of at most 80 characters"
	}
	if len(request.Title) < 3 || len(request.Title) > 120 {
		validation["title"] = "must contain 3-120 characters"
	}
	if len(request.Description) < 10 || len(request.Description) > 4000 {
		validation["description"] = "must contain 10-4000 characters"
	}
	if !json.Valid(request.Rules) || string(request.Rules) == "null" {
		validation["rules"] = "must be valid JSON"
	}
	if request.Status != "draft" && request.Status != "published" {
		validation["status"] = "must be draft or published"
	}
	if request.Visibility != "public" && request.Visibility != "unlisted" {
		validation["visibility"] = "must be public or unlisted"
	}
	if request.StartsAt.IsZero() {
		validation["starts_at"] = "is required"
	}
	if !request.EndsAt.After(request.StartsAt) {
		validation["ends_at"] = "must be after starts_at"
	}
	if len(validation) > 0 {
		validationError(w, validation)
		return
	}

	user, _ := userFromContext(r.Context())
	challenge, err := a.store.CreateChallenge(r.Context(), store.CreateChallengeParams{
		Slug:        request.Slug,
		Title:       request.Title,
		Description: request.Description,
		Rules:       request.Rules,
		Status:      request.Status,
		Visibility:  request.Visibility,
		StartsAt:    request.StartsAt,
		EndsAt:      request.EndsAt,
		CreatedBy:   user.ID,
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": challenge})
}

type createUploadRequest struct {
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

func (a *API) createUpload(w http.ResponseWriter, r *http.Request) {
	var request createUploadRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	allowedTypes := map[string]bool{
		"video/mp4":       true,
		"video/quicktime": true,
		"video/webm":      true,
	}
	validation := map[string]string{}
	if !allowedTypes[request.ContentType] {
		validation["content_type"] = "must be video/mp4, video/quicktime, or video/webm"
	}
	if request.SizeBytes <= 0 || request.SizeBytes > a.maxUploadBytes {
		validation["size_bytes"] = fmt.Sprintf("must be between 1 and %d", a.maxUploadBytes)
	}
	if len(validation) > 0 {
		validationError(w, validation)
		return
	}

	user, _ := userFromContext(r.Context())
	now := time.Now().UTC()
	objectKey, err := a.media.NewObjectKey(user.ID, request.ContentType, now)
	if err != nil {
		a.logger.Error("create media object key", "error", err)
		writeError(w, http.StatusInternalServerError, "storage_error", "The upload could not be initialized.", nil)
		return
	}
	upload, err := a.store.CreatePaidMediaUpload(r.Context(), store.CreatePaidMediaUploadParams{
		CreateMediaUploadParams: store.CreateMediaUploadParams{
			UserID:       user.ID,
			ObjectKey:    objectKey,
			ContentType:  request.ContentType,
			ExpectedSize: request.SizeBytes,
			ExpiresAt:    now.Add(a.uploadURLTTL),
		},
		EntitlementID:    a.billing.EntitlementID(),
		DailyLimit:       a.proDailyLimit,
		MonthlyLimit:     a.proMonthlyLimit,
		GlobalDailyLimit: a.globalDailyLimit,
		Now:              now,
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	url, headers, err := a.media.PresignedUploadURL(
		r.Context(),
		objectKey,
		request.ContentType,
		a.uploadURLTTL,
	)
	if err != nil {
		a.logger.Error("presign upload", "error", err, "upload_id", upload.ID)
		if releaseErr := a.store.ReleaseUploadReservation(
			r.Context(),
			upload.ID,
			user.ID,
		); releaseErr != nil {
			a.logger.Error(
				"release failed upload reservation",
				"error", releaseErr,
				"upload_id", upload.ID,
			)
		}
		writeError(w, http.StatusBadGateway, "storage_error", "The upload could not be initialized.", nil)
		return
	}
	upload.ObjectKey = ""
	writeJSON(w, http.StatusCreated, map[string]any{
		"data": map[string]any{
			"upload": upload,
			"method": http.MethodPut,
			"url":    url,
			"headers": map[string]string{
				"Content-Type": headers.Get("Content-Type"),
			},
		},
	})
}

func (a *API) completeUpload(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	upload, err := a.store.GetMediaUpload(
		r.Context(),
		chi.URLParam(r, "uploadID"),
		user.ID,
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if upload.Status != "pending" || time.Now().UTC().After(upload.ExpiresAt) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_upload", "The upload is not pending or has expired.", nil)
		return
	}
	info, err := a.media.Stat(r.Context(), upload.ObjectKey)
	if err != nil {
		a.logger.Warn("uploaded object unavailable", "error", err, "upload_id", upload.ID)
		writeError(w, http.StatusUnprocessableEntity, "upload_incomplete", "The uploaded object was not found.", nil)
		return
	}
	if info.Size != upload.ExpectedSize {
		validationError(w, map[string]string{"size_bytes": "uploaded object size does not match the declared size"})
		return
	}
	if info.ContentType != "" && info.ContentType != upload.ContentType {
		validationError(w, map[string]string{"content_type": "uploaded object content type does not match"})
		return
	}
	upload, err = a.store.CompletePaidMediaUpload(
		r.Context(),
		upload.ID,
		user.ID,
		info.Size,
		time.Now().UTC().Add(a.reservationTTL),
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	upload.ObjectKey = ""
	writeJSON(w, http.StatusOK, map[string]any{"data": upload})
}

type createSubmissionRequest struct {
	MediaUploadID string `json:"media_upload_id"`
	Caption       string `json:"caption"`
}

func (a *API) createSubmission(w http.ResponseWriter, r *http.Request) {
	var request createSubmissionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	request.Caption = strings.TrimSpace(request.Caption)
	validation := map[string]string{}
	if request.MediaUploadID == "" {
		validation["media_upload_id"] = "is required"
	}
	if len(request.Caption) > 280 {
		validation["caption"] = "must contain at most 280 characters"
	}
	if len(validation) > 0 {
		validationError(w, validation)
		return
	}
	user, _ := userFromContext(r.Context())
	submission, err := a.store.CreateSubmission(r.Context(), store.CreateSubmissionParams{
		ChallengeID:   chi.URLParam(r, "challengeID"),
		UserID:        user.ID,
		MediaUploadID: request.MediaUploadID,
		Caption:       request.Caption,
		Now:           time.Now().UTC(),
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if err := a.attachClipURL(r, &submission); err != nil {
		a.logger.Error("presign submitted clip", "error", err, "submission_id", submission.ID)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": submission})
}

func (a *API) getSubmission(w http.ResponseWriter, r *http.Request) {
	submission, err := a.store.GetSubmission(r.Context(), chi.URLParam(r, "submissionID"))
	if err != nil {
		handleStoreError(w, err)
		return
	}
	user, authenticated := userFromContext(r.Context())
	canViewPrivate := authenticated &&
		(user.ID == submission.UserID ||
			user.Role == domain.RoleModerator ||
			user.Role == domain.RoleAdmin)
	if (submission.ModerationStatus != "approved" || submission.VerificationStatus != "passed") && !canViewPrivate {
		writeError(w, http.StatusNotFound, "not_found", "The requested resource was not found.", nil)
		return
	}
	if err := a.attachClipURL(r, &submission); err != nil {
		a.logger.Error("presign clip", "error", err, "submission_id", submission.ID)
		writeError(w, http.StatusBadGateway, "storage_error", "The clip is temporarily unavailable.", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": submission})
}

func (a *API) listSubmissions(w http.ResponseWriter, r *http.Request) {
	user, authenticated := userFromContext(r.Context())
	viewerID := ""
	if authenticated {
		viewerID = user.ID
	}
	includeNonPublic := authenticated &&
		(user.Role == domain.RoleModerator || user.Role == domain.RoleAdmin) &&
		r.URL.Query().Get("include_private") == "true"
	limit, offset := pagination(r)
	submissions, err := a.store.ListChallengeSubmissions(
		r.Context(),
		chi.URLParam(r, "challengeID"),
		viewerID,
		includeNonPublic,
		limit,
		offset,
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	for index := range submissions {
		if err := a.attachClipURL(r, &submissions[index]); err != nil {
			a.logger.Error("presign clip", "error", err, "submission_id", submissions[index].ID)
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

type voteRequest struct {
	Score int `json:"score"`
}

func (a *API) vote(w http.ResponseWriter, r *http.Request) {
	var request voteRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	if request.Score < 1 || request.Score > 5 {
		validationError(w, map[string]string{"score": "must be between 1 and 5"})
		return
	}
	user, _ := userFromContext(r.Context())
	submission, err := a.store.Vote(
		r.Context(),
		chi.URLParam(r, "submissionID"),
		user.ID,
		request.Score,
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if err := a.attachClipURL(r, &submission); err != nil {
		a.logger.Warn("presign clip after vote", "error", err, "submission_id", submission.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": submission})
}

func (a *API) getPublicUser(w http.ResponseWriter, r *http.Request) {
	user, err := a.store.GetPublicUser(r.Context(), chi.URLParam(r, "userID"))
	if err != nil {
		handleStoreError(w, err)
		return
	}
	stats, err := a.store.GetPublicUserStats(r.Context(), user.ID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"user":  user,
			"stats": stats,
		},
	})
}

func (a *API) getLeaderboard(w http.ResponseWriter, r *http.Request) {
	user, authenticated := userFromContext(r.Context())
	viewerID := ""
	if authenticated {
		viewerID = user.ID
	}
	limit, offset := pagination(r)
	submissions, err := a.store.ListChallengeSubmissions(
		r.Context(),
		chi.URLParam(r, "challengeID"),
		viewerID,
		false,
		limit,
		offset,
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	entries := make([]domain.LeaderboardEntry, 0, len(submissions))
	for index := range submissions {
		if err := a.attachClipURL(r, &submissions[index]); err != nil {
			a.logger.Error("presign leaderboard clip", "error", err, "submission_id", submissions[index].ID)
			writeError(w, http.StatusBadGateway, "storage_error", "Clips are temporarily unavailable.", nil)
			return
		}
		entries = append(entries, domain.LeaderboardEntry{
			Rank:       offset + index + 1,
			Submission: submissions[index],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": entries,
		"pagination": map[string]int{
			"limit": limit, "offset": offset,
		},
	})
}

func (a *API) getShareCard(w http.ResponseWriter, r *http.Request) {
	data, err := a.store.GetShareData(r.Context(), chi.URLParam(r, "submissionID"))
	if err != nil {
		handleStoreError(w, err)
		return
	}
	var card bytes.Buffer
	if err := sharecard.Render(&card, sharecard.Data{
		ChallengeTitle: data.ChallengeTitle,
		Handle:         data.Submission.UserHandle,
		DisplayName:    data.DisplayName,
		Score:          data.Submission.StyleScore,
		VoteCount:      data.Submission.VoteCount,
		Rank:           data.Rank,
		CurrentStreak:  data.CurrentStreak,
	}); err != nil {
		a.logger.Error("render share card", "error", err, "submission_id", data.Submission.ID)
		writeError(w, http.StatusInternalServerError, "internal_error", "The share card could not be rendered.", nil)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Length", strconv.Itoa(card.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(card.Bytes())
}

func (a *API) attachClipURL(r *http.Request, submission *domain.Submission) error {
	submission.MediaUploadID = ""
	if submission.MediaThumbnailKey == "" {
		return nil
	}
	url, err := a.media.PresignedDownloadURL(
		r.Context(),
		submission.MediaObjectKey,
		10*time.Minute,
	)
	if err != nil {
		return err
	}
	submission.ClipURL = url
	if submission.MediaThumbnailKey != "" {
		thumbnailURL, err := a.media.PresignedDownloadURL(
			r.Context(),
			submission.MediaThumbnailKey,
			10*time.Minute,
		)
		if err != nil {
			return err
		}
		submission.ThumbnailURL = thumbnailURL
	}
	return nil
}

func pagination(r *http.Request) (limit, offset int) {
	limit = 20
	offset = 0
	if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed > 0 {
		limit = min(parsed, 100)
	}
	if parsed, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && parsed >= 0 {
		offset = parsed
	}
	return limit, offset
}
