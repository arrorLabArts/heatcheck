package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
)

type CreateChallengeParams struct {
	Slug        string
	Title       string
	Description string
	Rules       json.RawMessage
	Status      string
	Visibility  string
	StartsAt    time.Time
	EndsAt      time.Time
	CreatedBy   string
}

func scanChallenge(row scanner) (domain.Challenge, error) {
	var challenge domain.Challenge
	err := row.Scan(
		&challenge.ID,
		&challenge.Slug,
		&challenge.Title,
		&challenge.Description,
		&challenge.Rules,
		&challenge.Status,
		&challenge.Visibility,
		&challenge.StartsAt,
		&challenge.EndsAt,
		&challenge.CreatedBy,
		&challenge.CreatedAt,
		&challenge.UpdatedAt,
	)
	return challenge, mapError(err)
}

func (s *Store) CreateChallenge(ctx context.Context, params CreateChallengeParams) (domain.Challenge, error) {
	challenge, err := scanChallenge(s.pool.QueryRow(ctx, `
		INSERT INTO challenges (
			slug, title, description, rules, status, visibility,
			starts_at, ends_at, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING
			id, slug, title, description, rules, status, visibility,
			starts_at, ends_at, COALESCE(created_by::text, ''),
			created_at, updated_at
	`,
		params.Slug,
		params.Title,
		params.Description,
		params.Rules,
		params.Status,
		params.Visibility,
		params.StartsAt,
		params.EndsAt,
		params.CreatedBy,
	))
	return challenge, err
}

func (s *Store) GetChallenge(ctx context.Context, id string) (domain.Challenge, error) {
	return scanChallenge(s.pool.QueryRow(ctx, `
		SELECT
			id, slug, title, description, rules, status, visibility,
			starts_at, ends_at, COALESCE(created_by::text, ''),
			created_at, updated_at
		FROM challenges
		WHERE id = $1
	`, id))
}

func (s *Store) GetDailyChallenge(ctx context.Context, now time.Time) (domain.Challenge, error) {
	return scanChallenge(s.pool.QueryRow(ctx, `
		SELECT
			id, slug, title, description, rules, status, visibility,
			starts_at, ends_at, COALESCE(created_by::text, ''),
			created_at, updated_at
		FROM challenges
		WHERE status = 'published'
		  AND visibility = 'public'
		  AND starts_at <= $1
		  AND ends_at > $1
		ORDER BY starts_at DESC
		LIMIT 1
	`, now))
}

func (s *Store) ListChallenges(
	ctx context.Context,
	includePrivate bool,
	limit int,
	offset int,
) ([]domain.Challenge, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			id, slug, title, description, rules, status, visibility,
			starts_at, ends_at, COALESCE(created_by::text, ''),
			created_at, updated_at
		FROM challenges
		WHERE $1 OR (visibility = 'public' AND status IN ('published', 'closed'))
		ORDER BY starts_at DESC
		LIMIT $2 OFFSET $3
	`, includePrivate, limit, offset)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var challenges []domain.Challenge
	for rows.Next() {
		challenge, err := scanChallenge(rows)
		if err != nil {
			return nil, err
		}
		challenges = append(challenges, challenge)
	}
	return challenges, mapError(rows.Err())
}
