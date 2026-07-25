package store

import (
	"context"
	"errors"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ReplacePasswordHash(
	ctx context.Context,
	userID string,
	currentHash string,
	replacementHash string,
) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		SET password_hash = $3, updated_at = now()
		WHERE id = $1 AND password_hash = $2 AND deleted_at IS NULL
	`, userID, currentHash, replacementHash)
	return mapError(err)
}

func (s *Store) StartEmailVerification(
	ctx context.Context,
	userID string,
	tokenHash []byte,
	expiresAt time.Time,
	emailPayload any,
) error {
	encoded, err := s.encodeEmailPayload(emailPayload)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)

	var verifiedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT email_verified_at
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, userID).Scan(&verifiedAt); err != nil {
		return mapError(err)
	}
	if verifiedAt != nil {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE email_verification_tokens
		SET consumed_at = now()
		WHERE user_id = $1 AND consumed_at IS NULL
	`, userID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO email_verification_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, tokenHash, expiresAt); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO jobs (kind, entity_id, payload, max_attempts)
		VALUES ('email.verification', $1, $2, 8)
	`, userID, encoded); err != nil {
		return mapError(err)
	}
	return mapError(tx.Commit(ctx))
}

func (s *Store) VerifyEmail(ctx context.Context, tokenHash []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)

	var tokenID, userID string
	err = tx.QueryRow(ctx, `
		SELECT id, user_id
		FROM email_verification_tokens
		WHERE token_hash = $1
		  AND consumed_at IS NULL
		  AND expires_at > now()
		FOR UPDATE
	`, tokenHash).Scan(&tokenID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrToken
	}
	if err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET email_verified_at = COALESCE(email_verified_at, now()), updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
	`, userID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE email_verification_tokens
		SET consumed_at = now()
		WHERE user_id = $1 AND consumed_at IS NULL
	`, userID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_id, action, entity_type, entity_id)
		VALUES (
			$1::uuid,
			'user.email_verified',
			'user',
			($1::uuid)::text
		)
	`, userID); err != nil {
		return mapError(err)
	}
	return mapError(tx.Commit(ctx))
}

func (s *Store) StartPasswordReset(
	ctx context.Context,
	email string,
	tokenHash []byte,
	expiresAt time.Time,
	emailPayload any,
) error {
	encoded, err := s.encodeEmailPayload(emailPayload)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)
	var userID string
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM users
		WHERE email = $1 AND status <> 'deleted' AND deleted_at IS NULL
		FOR SHARE
	`, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE password_reset_tokens
		SET consumed_at = now()
		WHERE user_id = $1 AND consumed_at IS NULL
	`, userID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, tokenHash, expiresAt); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO jobs (kind, entity_id, payload, max_attempts)
		VALUES ('email.password_reset', $1, $2, 8)
	`, userID, encoded); err != nil {
		return mapError(err)
	}
	return mapError(tx.Commit(ctx))
}

func (s *Store) ResetPassword(
	ctx context.Context,
	tokenHash []byte,
	passwordHash string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)
	var userID string
	err = tx.QueryRow(ctx, `
		SELECT user_id
		FROM password_reset_tokens
		WHERE token_hash = $1
		  AND consumed_at IS NULL
		  AND expires_at > now()
		FOR UPDATE
	`, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrToken
	}
	if err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET password_hash = $2, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
	`, userID, passwordHash); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE password_reset_tokens
		SET consumed_at = now()
		WHERE user_id = $1 AND consumed_at IS NULL
	`, userID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = COALESCE(revoked_at, now())
		WHERE user_id = $1
	`, userID); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_id, action, entity_type, entity_id)
		VALUES (
			$1::uuid,
			'user.password_reset',
			'user',
			($1::uuid)::text
		)
	`, userID); err != nil {
		return mapError(err)
	}
	return mapError(tx.Commit(ctx))
}

func (s *Store) ListSessions(ctx context.Context, userID string) ([]domain.Session, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (family_id)
			family_id,
			user_agent,
			COALESCE(host(ip_address), ''),
			min(created_at) OVER (PARTITION BY family_id),
			max(last_used_at) OVER (PARTITION BY family_id),
			expires_at
		FROM refresh_tokens
		WHERE user_id = $1
		  AND revoked_at IS NULL
		  AND expires_at > now()
		ORDER BY family_id, created_at DESC
	`, userID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var sessions []domain.Session
	for rows.Next() {
		var session domain.Session
		if err := rows.Scan(
			&session.ID,
			&session.UserAgent,
			&session.IPAddress,
			&session.CreatedAt,
			&session.LastUsedAt,
			&session.ExpiresAt,
		); err != nil {
			return nil, mapError(err)
		}
		sessions = append(sessions, session)
	}
	return sessions, mapError(rows.Err())
}

func (s *Store) RevokeSession(ctx context.Context, userID, familyID string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = COALESCE(revoked_at, now())
		WHERE user_id = $1 AND family_id = $2 AND revoked_at IS NULL
	`, userID, familyID)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RevokeAllSessions(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = COALESCE(revoked_at, now())
		WHERE user_id = $1
	`, userID)
	return mapError(err)
}
