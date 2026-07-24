package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/jackc/pgx/v5"
)

type CreateUserParams struct {
	Email        string
	PasswordHash string
	Handle       string
	DisplayName  string
	DateOfBirth  time.Time
	Role         string
	Acceptances  []domain.PolicyAcceptance
	IPAddress    string
}

func (s *Store) CreateUser(ctx context.Context, params CreateUserParams) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT kind, version
		FROM policies
		WHERE is_current AND requires_acceptance
		FOR SHARE
	`)
	if err != nil {
		return domain.User{}, mapError(err)
	}
	required := map[string]string{}
	for rows.Next() {
		var kind, version string
		if err := rows.Scan(&kind, &version); err != nil {
			rows.Close()
			return domain.User{}, mapError(err)
		}
		required[kind] = version
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return domain.User{}, mapError(err)
	}

	accepted := make(map[string]string, len(params.Acceptances))
	for _, acceptance := range params.Acceptances {
		accepted[acceptance.Kind] = acceptance.Version
	}
	for kind, version := range required {
		if accepted[kind] != version {
			return domain.User{}, ErrPolicy
		}
	}

	var user domain.User
	err = tx.QueryRow(ctx, `
		INSERT INTO users (
			email, password_hash, handle, display_name, date_of_birth, role
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING
			id, email, handle, display_name, date_of_birth::text,
			role, status, created_at
	`,
		strings.ToLower(params.Email),
		params.PasswordHash,
		strings.ToLower(params.Handle),
		params.DisplayName,
		params.DateOfBirth,
		params.Role,
	).Scan(
		&user.ID,
		&user.Email,
		&user.Handle,
		&user.DisplayName,
		&user.DateOfBirth,
		&user.Role,
		&user.Status,
		&user.CreatedAt,
	)
	if err != nil {
		return domain.User{}, mapError(err)
	}

	for _, acceptance := range params.Acceptances {
		if _, err := tx.Exec(ctx, `
			INSERT INTO policy_acceptances (user_id, kind, version, ip_address)
			VALUES ($1, $2, $3, NULLIF($4, '')::inet)
			ON CONFLICT DO NOTHING
		`, user.ID, acceptance.Kind, acceptance.Version, params.IPAddress); err != nil {
			return domain.User{}, mapError(err)
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_id, action, entity_type, entity_id)
		VALUES ($1, 'user.registered', 'user', $2)
	`, user.ID, user.ID); err != nil {
		return domain.User{}, mapError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, mapError(err)
	}
	return user, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (domain.UserWithPassword, error) {
	var user domain.UserWithPassword
	err := s.pool.QueryRow(ctx, `
		SELECT
			id, email, password_hash, handle, display_name, date_of_birth::text,
			role, status, created_at
		FROM users
		WHERE email = $1 AND deleted_at IS NULL
	`, strings.ToLower(email)).Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.Handle,
		&user.DisplayName,
		&user.DateOfBirth,
		&user.Role,
		&user.Status,
		&user.CreatedAt,
	)
	return user, mapError(err)
}

func (s *Store) GetUserByID(ctx context.Context, id string) (domain.User, error) {
	var user domain.User
	err := s.pool.QueryRow(ctx, `
		SELECT
			id, email, handle, display_name, date_of_birth::text,
			role, status, created_at
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(
		&user.ID,
		&user.Email,
		&user.Handle,
		&user.DisplayName,
		&user.DateOfBirth,
		&user.Role,
		&user.Status,
		&user.CreatedAt,
	)
	return user, mapError(err)
}

func (s *Store) GetPublicUser(ctx context.Context, id string) (domain.User, error) {
	var user domain.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, handle, display_name, created_at
		FROM users
		WHERE id = $1 AND status <> 'deleted'
	`, id).Scan(
		&user.ID,
		&user.Handle,
		&user.DisplayName,
		&user.CreatedAt,
	)
	return user, mapError(err)
}

func (s *Store) CreateRefreshToken(
	ctx context.Context,
	userID string,
	hash []byte,
	expiresAt time.Time,
	userAgent string,
	ipAddress string,
) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (
			user_id, token_hash, expires_at, user_agent, ip_address
		)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::inet)
	`, userID, hash, expiresAt, userAgent, ipAddress)
	return mapError(err)
}

func (s *Store) RotateRefreshToken(
	ctx context.Context,
	oldHash []byte,
	newHash []byte,
	newExpiresAt time.Time,
	userAgent string,
	ipAddress string,
) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	var oldID, userID string
	err = tx.QueryRow(ctx, `
		SELECT id, user_id
		FROM refresh_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
		FOR UPDATE
	`, oldHash).Scan(&oldID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrToken
	}
	if err != nil {
		return domain.User{}, mapError(err)
	}

	var user domain.User
	err = tx.QueryRow(ctx, `
		SELECT
			id, email, handle, display_name, date_of_birth::text,
			role, status, created_at
		FROM users
		WHERE id = $1 AND status <> 'deleted' AND deleted_at IS NULL
	`, userID).Scan(
		&user.ID,
		&user.Email,
		&user.Handle,
		&user.DisplayName,
		&user.DateOfBirth,
		&user.Role,
		&user.Status,
		&user.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrToken
	}
	if err != nil {
		return domain.User{}, mapError(err)
	}

	var newID string
	err = tx.QueryRow(ctx, `
		INSERT INTO refresh_tokens (
			user_id, token_hash, expires_at, user_agent, ip_address
		)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::inet)
		RETURNING id
	`, userID, newHash, newExpiresAt, userAgent, ipAddress).Scan(&newID)
	if err != nil {
		return domain.User{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = now(), replaced_by = $2
		WHERE id = $1
	`, oldID, newID); err != nil {
		return domain.User{}, mapError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, mapError(err)
	}
	return user, nil
}

func (s *Store) RevokeRefreshToken(ctx context.Context, hash []byte) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL
	`, hash)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return ErrToken
	}
	return nil
}
