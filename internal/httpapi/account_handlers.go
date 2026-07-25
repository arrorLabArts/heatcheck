package httpapi

import (
	"net/http"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/auth"
	"github.com/go-chi/chi/v5"
)

func (a *API) createAccountExport(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	if user.Status != "active" {
		writeError(w, http.StatusForbidden, "account_restricted", "The account is currently restricted.", nil)
		return
	}
	export, err := a.store.CreateAccountExport(r.Context(), user.ID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"data": export})
}

func (a *API) getAccountExport(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	export, err := a.store.GetAccountExport(
		r.Context(),
		user.ID,
		chi.URLParam(r, "exportID"),
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if export.Status == "ready" && export.ExpiresAt != nil && time.Now().UTC().Before(*export.ExpiresAt) {
		signedTTL := a.uploadURLTTL
		if remaining := time.Until(*export.ExpiresAt); remaining < signedTTL {
			signedTTL = remaining
		}
		export.DownloadURL, err = a.media.PresignedDownloadURL(
			r.Context(),
			export.ObjectKey,
			signedTTL,
		)
		if err != nil {
			a.logger.Error("presign account export", "error", err, "export_id", export.ID)
			writeError(w, http.StatusBadGateway, "storage_error", "The export is temporarily unavailable.", nil)
			return
		}
	}
	export.ObjectKey = ""
	writeJSON(w, http.StatusOK, map[string]any{"data": export})
}

type accountDeletionRequest struct {
	Password string `json:"password"`
}

func (a *API) requestAccountDeletion(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	if user.Status != "active" {
		writeError(w, http.StatusForbidden, "account_restricted", "The account is currently restricted.", nil)
		return
	}
	var request accountDeletionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		badJSON(w, err)
		return
	}
	passwordHash, err := a.store.GetPasswordHash(r.Context(), user.ID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !auth.CheckPassword(passwordHash, request.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "The password is incorrect.", nil)
		return
	}
	executeAfter, err := a.store.RequestAccountDeletion(
		r.Context(),
		user.ID,
		time.Now().UTC().Add(a.accountDeleteWait),
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"data": map[string]any{
			"status":        "deletion_pending",
			"execute_after": executeAfter,
		},
	})
}

func (a *API) getAccountDeletion(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	executeAfter, err := a.store.GetDeletionRequest(r.Context(), user.ID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"status":        "deletion_pending",
			"execute_after": executeAfter,
		},
	})
}

func (a *API) cancelAccountDeletion(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	if err := a.store.CancelAccountDeletion(r.Context(), user.ID); err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}
