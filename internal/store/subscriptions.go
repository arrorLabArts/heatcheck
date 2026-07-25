package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/jackc/pgx/v5"
)

type BillingEventParams struct {
	ExternalEventID string
	EventType       string
	UserID          string
	Environment     string
	EventTimestamp  time.Time
	PayloadSHA256   string
	Metadata        map[string]any
}

type CreatePaidMediaUploadParams struct {
	CreateMediaUploadParams
	EntitlementID    string
	DailyLimit       int
	MonthlyLimit     int
	GlobalDailyLimit int
	Now              time.Time
}

func (s *Store) ApplyRevenueCatEvent(
	ctx context.Context,
	event BillingEventParams,
	subscriptions ...domain.Subscription,
) error {
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)

	command, err := tx.Exec(ctx, `
		INSERT INTO billing_events (
			provider, external_event_id, event_type, user_id, environment,
			event_timestamp, payload_sha256, metadata
		)
		VALUES ('revenuecat', $1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (provider, external_event_id) DO NOTHING
	`,
		event.ExternalEventID,
		event.EventType,
		event.UserID,
		event.Environment,
		event.EventTimestamp,
		event.PayloadSHA256,
		metadata,
	)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return nil
	}
	for _, subscription := range subscriptions {
		if err := upsertSubscription(ctx, tx, subscription); err != nil {
			return err
		}
	}
	return mapError(tx.Commit(ctx))
}

func (s *Store) SyncSubscription(
	ctx context.Context,
	subscription domain.Subscription,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)
	if err := upsertSubscription(ctx, tx, subscription); err != nil {
		return err
	}
	return mapError(tx.Commit(ctx))
}

func upsertSubscription(
	ctx context.Context,
	tx pgx.Tx,
	subscription domain.Subscription,
) error {
	if subscription.SourceUpdatedAt.IsZero() {
		subscription.SourceUpdatedAt = time.Now().UTC()
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO subscriptions (
			user_id, provider, entitlement_id, product_id, store, environment,
			status, active, will_renew, current_period_start,
			current_period_end, management_url, source_updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13
		)
		ON CONFLICT (provider, user_id, entitlement_id)
		DO UPDATE SET
			product_id = EXCLUDED.product_id,
			store = EXCLUDED.store,
			environment = EXCLUDED.environment,
			status = EXCLUDED.status,
			active = EXCLUDED.active,
			will_renew = EXCLUDED.will_renew,
			current_period_start = EXCLUDED.current_period_start,
			current_period_end = EXCLUDED.current_period_end,
			management_url = EXCLUDED.management_url,
			source_updated_at = EXCLUDED.source_updated_at,
			updated_at = now()
		WHERE EXCLUDED.source_updated_at >= subscriptions.source_updated_at
	`,
		subscription.UserID,
		subscription.Provider,
		subscription.EntitlementID,
		subscription.ProductID,
		subscription.Store,
		subscription.Environment,
		subscription.Status,
		subscription.Active,
		subscription.WillRenew,
		subscription.CurrentPeriodStart,
		subscription.CurrentPeriodEnd,
		subscription.ManagementURL,
		subscription.SourceUpdatedAt,
	)
	if err != nil {
		return mapError(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO entitlements (
			user_id, entitlement_id, provider, product_id, active,
			valid_from, valid_until, source_updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id, entitlement_id)
		DO UPDATE SET
			provider = EXCLUDED.provider,
			product_id = EXCLUDED.product_id,
			active = EXCLUDED.active,
			valid_from = EXCLUDED.valid_from,
			valid_until = EXCLUDED.valid_until,
			source_updated_at = EXCLUDED.source_updated_at,
			updated_at = now()
		WHERE EXCLUDED.source_updated_at >= entitlements.source_updated_at
	`,
		subscription.UserID,
		subscription.EntitlementID,
		subscription.Provider,
		subscription.ProductID,
		subscription.Active,
		subscription.CurrentPeriodStart,
		subscription.CurrentPeriodEnd,
		subscription.SourceUpdatedAt,
	)
	return mapError(err)
}

func (s *Store) GetSubscriptionOverview(
	ctx context.Context,
	userID string,
	entitlementID string,
	dailyLimit int,
	monthlyLimit int,
	now time.Time,
) (domain.SubscriptionOverview, error) {
	now = now.UTC()
	subscription := domain.Subscription{
		UserID:        userID,
		Tier:          "none",
		Provider:      "revenuecat",
		EntitlementID: entitlementID,
		Environment:   "unknown",
		Status:        "inactive",
	}
	err := s.pool.QueryRow(ctx, `
		SELECT
			user_id, provider, entitlement_id, product_id, store, environment,
			status, active, will_renew, current_period_start,
			current_period_end, management_url, source_updated_at, updated_at
		FROM subscriptions
		WHERE user_id = $1 AND entitlement_id = $2
		ORDER BY updated_at DESC
		LIMIT 1
	`, userID, entitlementID).Scan(
		&subscription.UserID,
		&subscription.Provider,
		&subscription.EntitlementID,
		&subscription.ProductID,
		&subscription.Store,
		&subscription.Environment,
		&subscription.Status,
		&subscription.Active,
		&subscription.WillRenew,
		&subscription.CurrentPeriodStart,
		&subscription.CurrentPeriodEnd,
		&subscription.ManagementURL,
		&subscription.SourceUpdatedAt,
		&subscription.UpdatedAt,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.SubscriptionOverview{}, mapError(err)
	}
	if err == nil {
		if subscription.ProductID != "" || subscription.Status != "inactive" {
			subscription.Tier = "pro"
		}
		if subscription.CurrentPeriodEnd != nil &&
			!subscription.CurrentPeriodEnd.After(now) {
			subscription.Active = false
			subscription.WillRenew = false
			subscription.Status = "expired"
		}
	}

	dayStart, dayEnd, monthStart, monthEnd := usageWindows(now)
	var dailyUsed, monthlyUsed int
	if err := s.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (
				WHERE (
					status = 'consumed'
					AND consumed_at >= $2
					AND consumed_at < $3
				)
				OR (status = 'reserved' AND expires_at > $6)
			)::integer,
			count(*) FILTER (
				WHERE (
					status = 'consumed'
					AND consumed_at >= $4
					AND consumed_at < $5
				)
				OR (status = 'reserved' AND expires_at > $6)
			)::integer
		FROM submission_usage_reservations
		WHERE user_id = $1
		  AND status IN ('reserved', 'consumed')
	`,
		userID,
		dayStart,
		dayEnd,
		monthStart,
		monthEnd,
		now,
	).Scan(&dailyUsed, &monthlyUsed); err != nil {
		return domain.SubscriptionOverview{}, mapError(err)
	}
	return domain.SubscriptionOverview{
		AppUserID:    userID,
		Subscription: subscription,
		Usage: domain.SubscriptionUsage{
			DailyLimit:       dailyLimit,
			DailyUsed:        dailyUsed,
			DailyRemaining:   max(0, dailyLimit-dailyUsed),
			DailyResetsAt:    dayEnd,
			MonthlyLimit:     monthlyLimit,
			MonthlyUsed:      monthlyUsed,
			MonthlyRemaining: max(0, monthlyLimit-monthlyUsed),
			MonthlyResetAt:   monthEnd,
		},
	}, nil
}

func (s *Store) CreatePaidMediaUpload(
	ctx context.Context,
	params CreatePaidMediaUploadParams,
) (domain.MediaUpload, error) {
	params.Now = params.Now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MediaUpload{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	var accountActive bool
	err = tx.QueryRow(ctx, `
		SELECT status = 'active'
		FROM users
		WHERE id = $1
		FOR UPDATE
	`, params.UserID).Scan(&accountActive)
	if err != nil {
		return domain.MediaUpload{}, mapError(err)
	}
	if !accountActive {
		return domain.MediaUpload{}, ErrForbidden
	}

	var entitled bool
	err = tx.QueryRow(ctx, `
		SELECT active AND (valid_until IS NULL OR valid_until > $3)
		FROM entitlements
		WHERE user_id = $1 AND entitlement_id = $2
		FOR UPDATE
	`, params.UserID, params.EntitlementID, params.Now).Scan(&entitled)
	if errors.Is(err, pgx.ErrNoRows) || !entitled {
		return domain.MediaUpload{}, ErrSubscriptionRequired
	}
	if err != nil {
		return domain.MediaUpload{}, mapError(err)
	}

	dayStart, dayEnd, monthStart, monthEnd := usageWindows(params.Now)
	// Serialize the platform-wide capacity check with its reservation insert.
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(484395367125)
	`); err != nil {
		return domain.MediaUpload{}, mapError(err)
	}
	var dailyUsed, monthlyUsed, globalDailyUsed int
	if err := tx.QueryRow(ctx, `
		SELECT
			count(*) FILTER (
				WHERE (
					status = 'consumed'
					AND consumed_at >= $2
					AND consumed_at < $3
				)
				OR (status = 'reserved' AND expires_at > $6)
			)::integer,
			count(*) FILTER (
				WHERE (
					status = 'consumed'
					AND consumed_at >= $4
					AND consumed_at < $5
				)
				OR (status = 'reserved' AND expires_at > $6)
			)::integer
		FROM submission_usage_reservations
		WHERE user_id = $1
		  AND status IN ('reserved', 'consumed')
	`,
		params.UserID,
		dayStart,
		dayEnd,
		monthStart,
		monthEnd,
		params.Now,
	).Scan(&dailyUsed, &monthlyUsed); err != nil {
		return domain.MediaUpload{}, mapError(err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)::integer
		FROM submission_usage_reservations
		WHERE (
			status = 'consumed'
			AND consumed_at >= $1
			AND consumed_at < $2
		)
		OR (status = 'reserved' AND expires_at > $3)
	`, dayStart, dayEnd, params.Now).Scan(&globalDailyUsed); err != nil {
		return domain.MediaUpload{}, mapError(err)
	}
	if globalDailyUsed >= params.GlobalDailyLimit {
		return domain.MediaUpload{}, &UsageLimitError{
			Period: "global_daily", Limit: params.GlobalDailyLimit, ResetAt: dayEnd,
		}
	}
	if dailyUsed >= params.DailyLimit {
		return domain.MediaUpload{}, &UsageLimitError{
			Period: "daily", Limit: params.DailyLimit, ResetAt: dayEnd,
		}
	}
	if monthlyUsed >= params.MonthlyLimit {
		return domain.MediaUpload{}, &UsageLimitError{
			Period: "monthly", Limit: params.MonthlyLimit, ResetAt: monthEnd,
		}
	}

	upload, err := scanMediaUpload(tx.QueryRow(ctx, `
		INSERT INTO media_uploads (
			user_id, object_key, content_type, expected_size, expires_at
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING
			id, user_id, object_key, content_type, expected_size,
			actual_size, duration_seconds, width, height, COALESCE(video_codec, ''), scanned_at,
			status, expires_at, created_at
	`,
		params.UserID,
		params.ObjectKey,
		params.ContentType,
		params.ExpectedSize,
		params.ExpiresAt,
	))
	if err != nil {
		return domain.MediaUpload{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO submission_usage_reservations (
			user_id, entitlement_id, media_upload_id, reserved_at, expires_at
		)
		VALUES ($1, $2, $3, $4, $5)
	`,
		params.UserID,
		params.EntitlementID,
		upload.ID,
		params.Now,
		params.ExpiresAt,
	); err != nil {
		return domain.MediaUpload{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MediaUpload{}, mapError(err)
	}
	return upload, nil
}

func (s *Store) CompletePaidMediaUpload(
	ctx context.Context,
	id string,
	userID string,
	actualSize int64,
	reservationExpiresAt time.Time,
) (domain.MediaUpload, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MediaUpload{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	upload, err := scanMediaUpload(tx.QueryRow(ctx, `
		UPDATE media_uploads
		SET status = 'uploaded', actual_size = $3, updated_at = now()
		WHERE id = $1
		  AND user_id = $2
		  AND status = 'pending'
		  AND expires_at > now()
		  AND expected_size = $3
		RETURNING
			id, user_id, object_key, content_type, expected_size,
			actual_size, duration_seconds, width, height, COALESCE(video_codec, ''), scanned_at,
			status, expires_at, created_at
	`, id, userID, actualSize))
	if err != nil {
		return domain.MediaUpload{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE submission_usage_reservations
		SET expires_at = $3, updated_at = now()
		WHERE media_upload_id = $1
		  AND user_id = $2
		  AND status = 'reserved'
	`, id, userID, reservationExpiresAt)
	if err != nil {
		return domain.MediaUpload{}, mapError(err)
	}
	if command.RowsAffected() == 0 {
		return domain.MediaUpload{}, ErrInvalid
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MediaUpload{}, mapError(err)
	}
	return upload, nil
}

func (s *Store) ReleaseUploadReservation(
	ctx context.Context,
	uploadID string,
	userID string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE submission_usage_reservations
		SET status = 'released', released_at = now(), updated_at = now()
		WHERE media_upload_id = $1
		  AND user_id = $2
		  AND status = 'reserved'
	`, uploadID, userID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE media_uploads
		SET status = 'expired', updated_at = now()
		WHERE id = $1 AND user_id = $2 AND status = 'pending'
	`, uploadID, userID); err != nil {
		return mapError(err)
	}
	return mapError(tx.Commit(ctx))
}

func usageWindows(now time.Time) (time.Time, time.Time, time.Time, time.Time) {
	now = now.UTC()
	dayStart := time.Date(
		now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC,
	)
	monthStart := time.Date(
		now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC,
	)
	return dayStart, dayStart.AddDate(0, 0, 1),
		monthStart, monthStart.AddDate(0, 1, 0)
}
