package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/jackc/pgx/v5"
)

type ExportMedia struct {
	SubmissionID string
	ObjectKey    string
	ContentType  string
}

type CleanupObject struct {
	Kind      string
	ID        string
	ObjectKey string
}

func (s *Store) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	var hash string
	err := s.pool.QueryRow(ctx, `
		SELECT password_hash
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, userID).Scan(&hash)
	return hash, mapError(err)
}

func (s *Store) CreateAccountExport(
	ctx context.Context,
	userID string,
) (domain.AccountExport, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AccountExport{}, mapError(err)
	}
	defer tx.Rollback(ctx)
	var export domain.AccountExport
	err = tx.QueryRow(ctx, `
		INSERT INTO account_exports (user_id)
		VALUES ($1)
		ON CONFLICT (user_id)
			WHERE status IN ('pending', 'processing', 'ready')
		DO UPDATE SET user_id = account_exports.user_id
		RETURNING id, status, COALESCE(object_key, ''), expires_at, error, created_at, completed_at
	`, userID).Scan(
		&export.ID,
		&export.Status,
		&export.ObjectKey,
		&export.ExpiresAt,
		&export.Error,
		&export.CreatedAt,
		&export.CompletedAt,
	)
	if err != nil {
		return domain.AccountExport{}, mapError(err)
	}
	if export.Status == "pending" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO jobs (kind, entity_id, dedupe_key, max_attempts)
			VALUES (
				'account.export',
				$1::uuid,
				'account.export:' || ($1::uuid)::text,
				5
			)
			ON CONFLICT (dedupe_key)
				WHERE dedupe_key IS NOT NULL AND status IN ('queued', 'running')
			DO NOTHING
		`, export.ID); err != nil {
			return domain.AccountExport{}, mapError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AccountExport{}, mapError(err)
	}
	return export, nil
}

func (s *Store) GetAccountExport(
	ctx context.Context,
	userID string,
	exportID string,
) (domain.AccountExport, error) {
	var export domain.AccountExport
	err := s.pool.QueryRow(ctx, `
		SELECT id, status, COALESCE(object_key, ''), expires_at, error, created_at, completed_at
		FROM account_exports
		WHERE id = $1 AND user_id = $2
	`, exportID, userID).Scan(
		&export.ID,
		&export.Status,
		&export.ObjectKey,
		&export.ExpiresAt,
		&export.Error,
		&export.CreatedAt,
		&export.CompletedAt,
	)
	return export, mapError(err)
}

func (s *Store) StartAccountExport(
	ctx context.Context,
	exportID string,
) (string, error) {
	var userID string
	err := s.pool.QueryRow(ctx, `
		UPDATE account_exports
		SET status = 'processing', error = ''
		WHERE id = $1 AND status IN ('pending', 'processing')
		RETURNING user_id
	`, exportID).Scan(&userID)
	return userID, mapError(err)
}

func (s *Store) BuildAccountExport(
	ctx context.Context,
	userID string,
) (json.RawMessage, []ExportMedia, error) {
	var data json.RawMessage
	err := s.pool.QueryRow(ctx, `
		SELECT jsonb_pretty(jsonb_build_object(
			'exported_at', now(),
			'user', (
				SELECT to_jsonb(u) - ARRAY['password_hash', 'deleted_at']::text[]
				FROM users u WHERE u.id = $1
			),
			'policy_acceptances', COALESCE((
				SELECT jsonb_agg(to_jsonb(p) ORDER BY p.accepted_at)
				FROM policy_acceptances p WHERE p.user_id = $1
			), '[]'::jsonb),
			'submissions', COALESCE((
				SELECT jsonb_agg(to_jsonb(s) ORDER BY s.created_at)
				FROM submissions s WHERE s.user_id = $1
			), '[]'::jsonb),
			'votes', COALESCE((
				SELECT jsonb_agg(to_jsonb(v) ORDER BY v.created_at)
				FROM votes v WHERE v.user_id = $1
			), '[]'::jsonb),
			'blocks', COALESCE((
				SELECT jsonb_agg(to_jsonb(b) ORDER BY b.created_at)
				FROM user_blocks b WHERE b.blocker_id = $1
			), '[]'::jsonb),
			'reports', COALESCE((
				SELECT jsonb_agg(to_jsonb(r) ORDER BY r.created_at)
				FROM reports r WHERE r.reporter_id = $1
			), '[]'::jsonb),
			'appeals', COALESCE((
				SELECT jsonb_agg(to_jsonb(a) ORDER BY a.created_at)
				FROM appeals a WHERE a.user_id = $1
			), '[]'::jsonb),
			'subscriptions', COALESCE((
				SELECT jsonb_agg(to_jsonb(s) ORDER BY s.created_at)
				FROM subscriptions s WHERE s.user_id = $1
			), '[]'::jsonb),
			'entitlements', COALESCE((
				SELECT jsonb_agg(to_jsonb(e) ORDER BY e.updated_at)
				FROM entitlements e WHERE e.user_id = $1
			), '[]'::jsonb),
			'submission_usage', COALESCE((
				SELECT jsonb_agg(to_jsonb(u) ORDER BY u.created_at)
				FROM submission_usage_reservations u WHERE u.user_id = $1
			), '[]'::jsonb),
			'billing_events', COALESCE((
				SELECT jsonb_agg(to_jsonb(b) ORDER BY b.processed_at)
				FROM billing_events b WHERE b.user_id = $1
			), '[]'::jsonb),
			'copyright_counter_notices', COALESCE((
				SELECT jsonb_agg(to_jsonb(c) ORDER BY c.created_at)
				FROM copyright_counter_notices c WHERE c.user_id = $1
			), '[]'::jsonb),
			'audit_events', COALESCE((
				SELECT jsonb_agg(to_jsonb(e) ORDER BY e.created_at)
				FROM audit_events e WHERE e.actor_id = $1
			), '[]'::jsonb)
		))::text
	`, userID).Scan(&data)
	if err != nil {
		return nil, nil, mapError(err)
	}
	var exportDocument map[string]any
	if err := json.Unmarshal(data, &exportDocument); err != nil {
		return nil, nil, err
	}
	if notices, ok := exportDocument["copyright_counter_notices"].([]any); ok {
		for _, rawNotice := range notices {
			notice, ok := rawNotice.(map[string]any)
			if !ok {
				continue
			}
			for _, field := range []string{"full_name", "address", "phone", "email", "signature"} {
				value, ok := notice[field].(string)
				if !ok {
					continue
				}
				revealed, err := s.cipher.Reveal(value)
				if err != nil {
					return nil, nil, err
				}
				notice[field] = revealed
			}
		}
	}
	data, err = json.MarshalIndent(exportDocument, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, m.object_key, m.content_type
		FROM submissions s
		JOIN media_uploads m ON m.id = s.media_upload_id
		WHERE s.user_id = $1
		ORDER BY s.created_at
	`, userID)
	if err != nil {
		return nil, nil, mapError(err)
	}
	defer rows.Close()
	var media []ExportMedia
	for rows.Next() {
		var item ExportMedia
		if err := rows.Scan(&item.SubmissionID, &item.ObjectKey, &item.ContentType); err != nil {
			return nil, nil, mapError(err)
		}
		media = append(media, item)
	}
	return data, media, mapError(rows.Err())
}

func (s *Store) CompleteAccountExport(
	ctx context.Context,
	exportID string,
	objectKey string,
	expiresAt time.Time,
) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE account_exports
		SET status = 'ready',
		    object_key = $2,
		    expires_at = $3,
		    completed_at = now(),
		    error = ''
		WHERE id = $1 AND status = 'processing'
	`, exportID, objectKey, expiresAt)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func (s *Store) FailAccountExport(ctx context.Context, exportID, message string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE account_exports
		SET status = 'failed', error = left($2, 1000), completed_at = now()
		WHERE id = $1 AND status IN ('pending', 'processing')
	`, exportID, message)
	return mapError(err)
}

func (s *Store) RequestAccountDeletion(
	ctx context.Context,
	userID string,
	executeAfter time.Time,
) (time.Time, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return time.Time{}, mapError(err)
	}
	defer tx.Rollback(ctx)
	var currentStatus string
	if err := tx.QueryRow(ctx, `
		SELECT status FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
	`, userID).Scan(&currentStatus); err != nil {
		return time.Time{}, mapError(err)
	}
	if currentStatus != "active" && currentStatus != "deletion_pending" {
		return time.Time{}, ErrForbidden
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO account_deletion_requests (user_id, execute_after)
		VALUES ($1, $2)
		ON CONFLICT (user_id)
		DO UPDATE SET
			execute_after = EXCLUDED.execute_after,
			cancelled_at = NULL,
			completed_at = NULL
	`, userID, executeAfter); err != nil {
		return time.Time{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET status = 'deletion_pending', updated_at = now() WHERE id = $1
	`, userID); err != nil {
		return time.Time{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO jobs (
			kind, entity_id, dedupe_key, available_at, max_attempts
		)
		VALUES (
			'account.delete',
			$1::uuid,
			'account.delete:' || ($1::uuid)::text,
			$2,
			20
		)
		ON CONFLICT (dedupe_key)
			WHERE dedupe_key IS NOT NULL AND status IN ('queued', 'running')
		DO UPDATE SET available_at = EXCLUDED.available_at, updated_at = now()
	`, userID, executeAfter); err != nil {
		return time.Time{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_id, action, entity_type, entity_id, metadata)
		VALUES (
			$1::uuid,
			'user.deletion_requested',
			'user',
			($1::uuid)::text,
			jsonb_build_object('execute_after', $2::timestamptz)
		)
	`, userID, executeAfter); err != nil {
		return time.Time{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, mapError(err)
	}
	return executeAfter, nil
}

func (s *Store) CancelAccountDeletion(ctx context.Context, userID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)

	var jobStatus string
	err = tx.QueryRow(ctx, `
		SELECT status
		FROM jobs
		WHERE dedupe_key = 'account.delete:' || ($1::uuid)::text
		  AND status IN ('queued', 'running')
		FOR UPDATE
	`, userID).Scan(&jobStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return mapError(err)
	}
	if jobStatus == "running" {
		return ErrForbidden
	}

	command, err := tx.Exec(ctx, `
		UPDATE account_deletion_requests
		SET cancelled_at = now()
		WHERE user_id = $1 AND cancelled_at IS NULL AND completed_at IS NULL
	`, userID)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET status = 'active', updated_at = now()
		WHERE id = $1 AND status = 'deletion_pending'
	`, userID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE jobs
		SET status = 'dead',
		    last_error = 'account deletion cancelled',
		    completed_at = now(),
		    updated_at = now()
		WHERE dedupe_key = 'account.delete:' || ($1::uuid)::text
		  AND status = 'queued'
	`, userID); err != nil {
		return mapError(err)
	}
	return mapError(tx.Commit(ctx))
}

func (s *Store) AccountDeletionMedia(ctx context.Context, userID string) ([]string, error) {
	var due bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM account_deletion_requests
			WHERE user_id = $1
			  AND execute_after <= now()
			  AND cancelled_at IS NULL
			  AND completed_at IS NULL
		)
	`).Scan(&due)
	if err != nil {
		return nil, mapError(err)
	}
	if !due {
		return nil, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `
		SELECT object_key
		FROM media_uploads
		WHERE user_id = $1
		UNION ALL
		SELECT processed_object_key
		FROM media_uploads
		WHERE user_id = $1 AND processed_object_key IS NOT NULL
		UNION ALL
		SELECT thumbnail_object_key
		FROM media_uploads
		WHERE user_id = $1 AND thumbnail_object_key IS NOT NULL
		UNION ALL
		SELECT object_key
		FROM account_exports
		WHERE user_id = $1 AND object_key IS NOT NULL
	`, userID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, mapError(err)
		}
		keys = append(keys, key)
	}
	return keys, mapError(rows.Err())
}

func (s *Store) CompleteAccountDeletion(ctx context.Context, userID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)
	var executeAfter time.Time
	err = tx.QueryRow(ctx, `
		SELECT execute_after
		FROM account_deletion_requests
		WHERE user_id = $1
		  AND execute_after <= now()
		  AND cancelled_at IS NULL
		  AND completed_at IS NULL
		FOR UPDATE
	`, userID).Scan(&executeAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalid
	}
	if err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE copyright_notices
		SET submission_id = NULL
		WHERE submission_id IN (SELECT id FROM submissions WHERE user_id = $1)
	`, userID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM submissions WHERE user_id = $1`, userID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_uploads WHERE user_id = $1`, userID); err != nil {
		return mapError(err)
	}
	for _, statement := range []string{
		`DELETE FROM refresh_tokens WHERE user_id = $1`,
		`DELETE FROM email_verification_tokens WHERE user_id = $1`,
		`DELETE FROM password_reset_tokens WHERE user_id = $1`,
		`DELETE FROM votes WHERE user_id = $1`,
		`DELETE FROM user_blocks WHERE blocker_id = $1 OR blocked_user_id = $1`,
		`DELETE FROM policy_acceptances WHERE user_id = $1`,
		`DELETE FROM account_exports WHERE user_id = $1`,
		`DELETE FROM subscriptions WHERE user_id = $1`,
		`DELETE FROM entitlements WHERE user_id = $1`,
		`UPDATE billing_events SET user_id = NULL WHERE user_id = $1`,
	} {
		if _, err := tx.Exec(ctx, statement, userID); err != nil {
			return mapError(err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE copyright_counter_notices
		SET full_name = 'Deleted user',
		    address = '',
		    phone = '',
		    email = 'deleted@example.invalid',
		    signature = 'Deleted'
		WHERE user_id = $1
	`, userID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET email = ('deleted+' || id::text || '@example.invalid')::citext,
		    password_hash = 'deleted',
		    handle = ('deleted_' || replace(id::text, '-', ''))::citext,
		    display_name = 'Deleted user',
		    date_of_birth = date '1900-01-01',
		    status = 'deleted',
		    email_verified_at = NULL,
		    deleted_at = now(),
		    updated_at = now()
		WHERE id = $1 AND status = 'deletion_pending'
	`, userID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE account_deletion_requests SET completed_at = now() WHERE user_id = $1
	`, userID); err != nil {
		return mapError(err)
	}
	return mapError(tx.Commit(ctx))
}

func (s *Store) ListCleanupObjects(ctx context.Context, limit int) ([]CleanupObject, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT 'upload', id, object_key
		FROM media_uploads
		WHERE (
			(status = 'pending' AND expires_at < now())
			OR (status IN ('expired', 'rejected') AND updated_at < now() - interval '7 days')
		)
		UNION ALL
		SELECT 'upload', id, processed_object_key
		FROM media_uploads
		WHERE processed_object_key IS NOT NULL AND (
			(status = 'pending' AND expires_at < now())
			OR (status IN ('expired', 'rejected') AND updated_at < now() - interval '7 days')
		)
		UNION ALL
		SELECT 'upload', id, thumbnail_object_key
		FROM media_uploads
		WHERE thumbnail_object_key IS NOT NULL AND (
			(status = 'pending' AND expires_at < now())
			OR (status IN ('expired', 'rejected') AND updated_at < now() - interval '7 days')
		)
		UNION ALL
		SELECT 'export', id, object_key
		FROM account_exports
		WHERE status = 'ready' AND expires_at < now() AND object_key IS NOT NULL
		UNION ALL
		SELECT 'original', media.id, media.object_key
		FROM media_uploads media
		WHERE media.status = 'consumed'
		  AND media.retained_until < now()
		  AND media.processed_object_key IS NOT NULL
		  AND media.object_key <> media.processed_object_key
		  AND NOT EXISTS (
		      SELECT 1
		      FROM account_exports export
		      WHERE export.user_id = media.user_id
		        AND export.status IN ('pending', 'processing')
		  )
		  AND NOT EXISTS (
		      SELECT 1
		      FROM submissions submission
		      JOIN copyright_notices notice
		        ON notice.submission_id = submission.id
		      WHERE submission.media_upload_id = media.id
		        AND notice.status IN ('received', 'reviewing', 'actioned', 'countered')
		  )
		  AND NOT EXISTS (
		      SELECT 1
		      FROM submissions submission
		      JOIN reports report
		        ON report.target_type = 'submission'
		       AND report.target_id = submission.id
		      WHERE submission.media_upload_id = media.id
		        AND report.status IN ('open', 'reviewing')
		  )
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var objects []CleanupObject
	for rows.Next() {
		var object CleanupObject
		if err := rows.Scan(&object.Kind, &object.ID, &object.ObjectKey); err != nil {
			return nil, mapError(err)
		}
		objects = append(objects, object)
	}
	return objects, mapError(rows.Err())
}

func (s *Store) CompleteCleanupObject(ctx context.Context, object CleanupObject) error {
	switch object.Kind {
	case "upload":
		_, err := s.pool.Exec(ctx, `
			UPDATE media_uploads
			SET status = 'expired', updated_at = now()
			WHERE id = $1 AND status IN ('pending', 'expired', 'rejected')
		`, object.ID)
		return mapError(err)
	case "export":
		_, err := s.pool.Exec(ctx, `
			UPDATE account_exports
			SET status = 'expired', object_key = NULL
			WHERE id = $1 AND status = 'ready'
		`, object.ID)
		return mapError(err)
	case "original":
		command, err := s.pool.Exec(ctx, `
			UPDATE media_uploads
			SET object_key = processed_object_key,
			    content_type = 'video/mp4',
			    updated_at = now()
			WHERE id = $1
			  AND status = 'consumed'
			  AND processed_object_key IS NOT NULL
			  AND object_key <> processed_object_key
		`, object.ID)
		if err != nil {
			return mapError(err)
		}
		if command.RowsAffected() == 0 {
			return ErrConflict
		}
		return nil
	default:
		return ErrInvalid
	}
}

func (s *Store) CleanupDatabase(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM rate_limits WHERE expires_at < now();
		DELETE FROM email_verification_tokens WHERE expires_at < now() - interval '7 days';
		DELETE FROM password_reset_tokens WHERE expires_at < now() - interval '7 days';
		DELETE FROM refresh_tokens WHERE expires_at < now() - interval '30 days';
		DELETE FROM submission_usage_reservations
		WHERE (
			status = 'released'
			OR (status = 'reserved' AND expires_at < now())
		)
		  AND updated_at < now() - interval '30 days';
		DELETE FROM jobs
		WHERE status IN ('succeeded', 'dead')
		  AND completed_at < now() - interval '30 days';
	`)
	return mapError(err)
}

func (s *Store) GetDeletionRequest(ctx context.Context, userID string) (time.Time, error) {
	var executeAfter time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT execute_after
		FROM account_deletion_requests
		WHERE user_id = $1 AND cancelled_at IS NULL AND completed_at IS NULL
	`, userID).Scan(&executeAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	return executeAfter, mapError(err)
}
