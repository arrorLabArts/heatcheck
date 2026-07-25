package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/billing"
	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/arrorLabArts/heatcheck/internal/store"
)

func (a *API) getSubscription(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	overview, err := a.store.GetSubscriptionOverview(
		r.Context(),
		user.ID,
		a.billing.EntitlementID(),
		a.proDailyLimit,
		a.proMonthlyLimit,
		time.Now().UTC(),
	)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": overview})
}

func (a *API) syncSubscription(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	subscription, err := a.billing.FetchSubscription(r.Context(), user.ID)
	if err != nil {
		a.logger.Error("sync RevenueCat subscription", "error", err, "user_id", user.ID)
		writeError(
			w,
			http.StatusBadGateway,
			"billing_provider_unavailable",
			"Subscription status could not be synchronized.",
			nil,
		)
		return
	}
	if err := a.store.SyncSubscription(r.Context(), subscription); err != nil {
		handleStoreError(w, err)
		return
	}
	a.getSubscription(w, r)
}

func (a *API) revenueCatWebhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_webhook", "The webhook body is invalid.", nil)
		return
	}
	if err := a.billing.VerifyWebhook(
		body,
		r.Header.Get("Authorization"),
		r.Header.Get("X-RevenueCat-Webhook-Signature"),
		time.Now().UTC(),
	); err != nil {
		a.logger.Warn("reject RevenueCat webhook", "error", err)
		writeError(w, http.StatusUnauthorized, "invalid_webhook_signature", "Webhook authentication failed.", nil)
		return
	}
	webhook, err := a.billing.ParseWebhook(body)
	if err != nil {
		a.logger.Warn("reject RevenueCat webhook payload", "error", err)
		writeError(w, http.StatusBadRequest, "invalid_webhook", "The webhook payload is invalid.", nil)
		return
	}
	if webhook.Event.Type == "TEST" {
		writeJSON(w, http.StatusOK, map[string]any{"received": true})
		return
	}
	environment := billing.WebhookEnvironment(webhook.Event.Environment)
	if environment == "sandbox" && !a.billing.AllowSandbox() {
		a.logger.Info(
			"ignore RevenueCat sandbox webhook",
			"event_id", webhook.Event.ID,
			"event_type", webhook.Event.Type,
		)
		writeJSON(w, http.StatusOK, map[string]any{
			"received": true,
			"ignored":  true,
		})
		return
	}
	candidateUserIDs := webhook.Event.AffectedAppUserIDs()
	if len(candidateUserIDs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"received": true,
			"ignored":  true,
		})
		return
	}

	knownUserIDs := make([]string, 0, len(candidateUserIDs))
	for _, candidateUserID := range candidateUserIDs {
		if _, err := a.store.GetUserByID(r.Context(), candidateUserID); err != nil {
			if !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrInvalid) {
				handleStoreError(w, err)
				return
			}
			continue
		}
		knownUserIDs = append(knownUserIDs, candidateUserID)
	}
	if len(knownUserIDs) == 0 {
		a.logger.Warn(
			"ignore RevenueCat webhook for unknown app users",
			"event_id", webhook.Event.ID,
			"candidate_count", len(candidateUserIDs),
		)
		writeJSON(w, http.StatusOK, map[string]any{
			"received": true,
			"ignored":  true,
		})
		return
	}

	subscriptions := make([]domain.Subscription, 0, len(knownUserIDs))
	for _, userID := range knownUserIDs {
		subscription, err := a.billing.FetchSubscription(r.Context(), userID)
		if err != nil {
			a.logger.Error(
				"reconcile RevenueCat webhook",
				"error", err,
				"event_id", webhook.Event.ID,
				"user_id", userID,
			)
			writeError(
				w,
				http.StatusServiceUnavailable,
				"billing_provider_unavailable",
				"Subscription state could not be reconciled.",
				nil,
			)
			return
		}
		subscriptions = append(subscriptions, subscription)
	}

	eventUserID := knownUserIDs[0]
	if strings.TrimSpace(webhook.Event.AppUserID) != "" {
		for _, userID := range knownUserIDs {
			if userID == webhook.Event.AppUserID {
				eventUserID = userID
				break
			}
		}
	}
	metadata := map[string]any{
		"app_id":                  webhook.Event.AppID,
		"product_id":              webhook.Event.ProductID,
		"store":                   webhook.Event.Store,
		"transaction_id":          webhook.Event.TransactionID,
		"original_transaction_id": webhook.Event.OriginalTransactionID,
		"entitlement_ids":         webhook.Event.EntitlementIDs,
		"currency":                webhook.Event.Currency,
		"price":                   webhook.Event.Price,
		"transferred_from":        webhook.Event.TransferredFrom,
		"transferred_to":          webhook.Event.TransferredTo,
	}
	if webhook.Event.ExpirationAtMS != nil {
		metadata["expiration_at"] = time.UnixMilli(*webhook.Event.ExpirationAtMS).UTC()
	}
	if err := a.store.ApplyRevenueCatEvent(
		r.Context(),
		store.BillingEventParams{
			ExternalEventID: webhook.Event.ID,
			EventType:       webhook.Event.Type,
			UserID:          eventUserID,
			Environment:     environment,
			EventTimestamp: billing.WebhookTimestamp(
				webhook.Event.EventTimestampMS,
				time.Now().UTC(),
			),
			PayloadSHA256: billing.PayloadHash(body),
			Metadata:      metadata,
		},
		subscriptions...,
	); err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": true})
}
