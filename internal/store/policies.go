package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
)

func (s *Store) ListCurrentPolicies(ctx context.Context) ([]domain.Policy, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind, version, title, content, requires_acceptance, effective_at
		FROM policies
		WHERE is_current
		ORDER BY kind
	`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var policies []domain.Policy
	for rows.Next() {
		var policy domain.Policy
		if err := rows.Scan(
			&policy.Kind,
			&policy.Version,
			&policy.Title,
			&policy.Content,
			&policy.RequiresAcceptance,
			&policy.EffectiveAt,
		); err != nil {
			return nil, mapError(err)
		}
		policies = append(policies, policy)
	}
	return policies, mapError(rows.Err())
}

func (s *Store) GetCurrentPolicy(ctx context.Context, kind string) (domain.Policy, error) {
	var policy domain.Policy
	err := s.pool.QueryRow(ctx, `
		SELECT kind, version, title, content, requires_acceptance, effective_at
		FROM policies
		WHERE kind = $1 AND is_current
	`, kind).Scan(
		&policy.Kind,
		&policy.Version,
		&policy.Title,
		&policy.Content,
		&policy.RequiresAcceptance,
		&policy.EffectiveAt,
	)
	return policy, mapError(err)
}

func (s *Store) MissingRequiredPolicies(
	ctx context.Context,
	userID string,
) ([]domain.PolicyAcceptance, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.kind, p.version
		FROM policies p
		WHERE p.is_current
		  AND p.requires_acceptance
		  AND NOT EXISTS (
			SELECT 1
			FROM policy_acceptances a
			WHERE a.user_id = $1
			  AND a.kind = p.kind
			  AND a.version = p.version
		  )
		ORDER BY p.kind
	`, userID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var missing []domain.PolicyAcceptance
	for rows.Next() {
		var acceptance domain.PolicyAcceptance
		if err := rows.Scan(&acceptance.Kind, &acceptance.Version); err != nil {
			return nil, mapError(err)
		}
		missing = append(missing, acceptance)
	}
	return missing, mapError(rows.Err())
}

func (s *Store) AcceptPolicies(
	ctx context.Context,
	userID string,
	acceptances []domain.PolicyAcceptance,
	ipAddress string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)

	for _, acceptance := range acceptances {
		command, err := tx.Exec(ctx, `
			INSERT INTO policy_acceptances (
				user_id, kind, version, ip_address
			)
			SELECT $1, kind, version, NULLIF($4, '')::inet
			FROM policies
			WHERE kind = $2
			  AND version = $3
			  AND is_current
			  AND requires_acceptance
			ON CONFLICT DO NOTHING
		`, userID, acceptance.Kind, acceptance.Version, ipAddress)
		if err != nil {
			return mapError(err)
		}
		if command.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM policy_acceptances
					WHERE user_id = $1 AND kind = $2 AND version = $3
				)
			`, userID, acceptance.Kind, acceptance.Version).Scan(&exists); err != nil {
				return mapError(err)
			}
			if !exists {
				return ErrPolicy
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return mapError(err)
	}
	return nil
}

type PublishPolicyParams struct {
	Kind               string
	Version            string
	Title              string
	Content            string
	RequiresAcceptance bool
	EffectiveAt        time.Time
	ActorID            string
}

func (s *Store) PublishPolicy(ctx context.Context, params PublishPolicyParams) (domain.Policy, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Policy{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE policies SET is_current = false WHERE kind = $1 AND is_current`,
		params.Kind,
	); err != nil {
		return domain.Policy{}, mapError(err)
	}

	var policy domain.Policy
	err = tx.QueryRow(ctx, `
		INSERT INTO policies (
			kind, version, title, content, requires_acceptance, effective_at, is_current
		)
		VALUES ($1, $2, $3, $4, $5, $6, true)
		RETURNING kind, version, title, content, requires_acceptance, effective_at
	`,
		params.Kind,
		params.Version,
		params.Title,
		params.Content,
		params.RequiresAcceptance,
		params.EffectiveAt,
	).Scan(
		&policy.Kind,
		&policy.Version,
		&policy.Title,
		&policy.Content,
		&policy.RequiresAcceptance,
		&policy.EffectiveAt,
	)
	if err != nil {
		return domain.Policy{}, mapError(err)
	}

	metadata, _ := json.Marshal(map[string]string{"version": params.Version})
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_id, action, entity_type, entity_id, metadata)
		VALUES ($1, 'policy.published', 'policy', $2, $3)
	`, params.ActorID, params.Kind, metadata); err != nil {
		return domain.Policy{}, fmt.Errorf("audit policy publication: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Policy{}, mapError(err)
	}
	return policy, nil
}
