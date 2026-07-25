package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/arrorLabArts/heatcheck/internal/store"
)

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status == http.StatusNoContent {
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, errorEnvelope{Error: apiError{
		Code:    code,
		Message: message,
		Details: details,
	}})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func handleStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "The requested resource was not found.", nil)
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "The resource already exists or conflicts with its current state.", nil)
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "This operation is not allowed.", nil)
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, "invalid_operation", "The operation is not valid for the resource's current state.", nil)
	case errors.Is(err, store.ErrPolicy):
		writeError(w, http.StatusPreconditionRequired, "policy_acceptance_required", "Current required policies must be accepted.", nil)
	case errors.Is(err, store.ErrSubscriptionRequired):
		writeError(w, http.StatusPaymentRequired, "subscription_required", "An active Pro subscription is required to submit clips.", nil)
	case errors.Is(err, store.ErrUsageLimit):
		var limitError *store.UsageLimitError
		if errors.As(err, &limitError) {
			writeError(w, http.StatusTooManyRequests, "submission_limit_reached", "The submission allowance has been reached.", map[string]any{
				"period":    limitError.Period,
				"limit":     limitError.Limit,
				"resets_at": limitError.ResetAt,
			})
			return
		}
		writeError(w, http.StatusTooManyRequests, "submission_limit_reached", "The submission allowance has been reached.", nil)
	case errors.Is(err, store.ErrToken):
		writeError(w, http.StatusUnauthorized, "invalid_refresh_token", "The refresh token is invalid or expired.", nil)
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
	}
}

func badJSON(w http.ResponseWriter, err error) {
	writeError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.", err.Error())
}

func validationError(w http.ResponseWriter, details any) {
	writeError(w, http.StatusUnprocessableEntity, "validation_failed", "The request failed validation.", details)
}
