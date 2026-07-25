package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
)

func (s *Store) EnqueueJob(
	ctx context.Context,
	kind string,
	entityID string,
	dedupeKey string,
	payload any,
	maxAttempts int,
) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO jobs (kind, entity_id, dedupe_key, payload, max_attempts)
		VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, ''), $4, $5)
		ON CONFLICT (dedupe_key)
			WHERE dedupe_key IS NOT NULL AND status IN ('queued', 'running')
		DO UPDATE SET updated_at = jobs.updated_at
		RETURNING id
	`, kind, entityID, dedupeKey, encoded, maxAttempts).Scan(&id)
	return id, mapError(err)
}

func (s *Store) EnqueueEmailNotification(
	ctx context.Context,
	kind string,
	entityID string,
	dedupeKey string,
	payload any,
	maxAttempts int,
) (string, error) {
	encoded, err := s.encodeEmailPayload(payload)
	if err != nil {
		return "", err
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO jobs (
			kind, entity_id, dedupe_key, payload, max_attempts
		)
		VALUES (
			$1,
			NULLIF($2, '')::uuid,
			NULLIF($3, ''),
			$4,
			$5
		)
		ON CONFLICT (dedupe_key)
			WHERE dedupe_key IS NOT NULL AND status IN ('queued', 'running')
		DO UPDATE SET updated_at = jobs.updated_at
		RETURNING id
	`, kind, entityID, dedupeKey, encoded, maxAttempts).Scan(&id)
	return id, mapError(err)
}

func (s *Store) ClaimJob(
	ctx context.Context,
	workerID string,
	staleAfter time.Duration,
) (domain.Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Job{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE submissions s
		SET verification_status = 'manual_review',
		    moderation_status = 'pending',
		    verification_details = jsonb_build_object(
		        'source', 'worker',
		        'reason', 'Automated analysis worker repeatedly stopped before completing.'
		    ),
		    updated_at = now()
		FROM jobs j
		WHERE j.entity_id = s.id
		  AND j.kind = 'submission.analyze'
		  AND j.status = 'running'
		  AND j.locked_at < now() - $1::interval
		  AND j.attempts >= j.max_attempts
		  AND s.verification_status = 'pending'
	`, staleAfter.String()); err != nil {
		return domain.Job{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE jobs
		SET status = CASE
		        WHEN attempts >= max_attempts THEN 'dead'
		        ELSE 'queued'
		    END,
		    locked_at = NULL,
		    locked_by = NULL,
		    available_at = now(),
		    last_error = 'worker lock expired',
		    completed_at = CASE
		        WHEN attempts >= max_attempts THEN now()
		        ELSE NULL
		    END,
		    updated_at = now()
		WHERE status = 'running' AND locked_at < now() - $1::interval
	`, staleAfter.String()); err != nil {
		return domain.Job{}, mapError(err)
	}

	var job domain.Job
	err = tx.QueryRow(ctx, `
		WITH next_job AS (
			SELECT id
			FROM jobs
			WHERE status = 'queued'
			  AND attempts < max_attempts
			  AND available_at <= now()
			ORDER BY available_at, created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE jobs j
		SET status = 'running',
		    attempts = attempts + 1,
		    locked_at = now(),
		    locked_by = $1,
		    updated_at = now()
		FROM next_job
		WHERE j.id = next_job.id
		RETURNING j.id, j.kind, j.entity_id, j.payload, j.attempts, j.max_attempts
	`, workerID).Scan(
		&job.ID,
		&job.Kind,
		&job.EntityID,
		&job.Payload,
		&job.Attempts,
		&job.MaxAttempts,
	)
	if err != nil {
		return domain.Job{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Job{}, mapError(err)
	}
	return job, nil
}

func (s *Store) CompleteJob(ctx context.Context, jobID, workerID string, result any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE jobs
		SET status = 'succeeded',
		    result = $3,
		    payload = CASE WHEN kind LIKE 'email.%' THEN '{}'::jsonb ELSE payload END,
		    locked_at = NULL,
		    locked_by = NULL,
		    completed_at = now(),
		    updated_at = now()
		WHERE id = $1 AND status = 'running' AND locked_by = $2
	`, jobID, workerID, encoded)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func (s *Store) FailJob(ctx context.Context, job domain.Job, workerID string, jobErr error) error {
	status := "queued"
	if job.Attempts >= job.MaxAttempts {
		status = "dead"
	}
	backoff := time.Duration(1<<min(job.Attempts, 8)) * time.Second
	command, err := s.pool.Exec(ctx, `
		UPDATE jobs
		SET status = $3,
		    available_at = now() + $4::interval,
		    locked_at = NULL,
		    locked_by = NULL,
		    last_error = left($5, 4000),
		    completed_at = CASE WHEN $3 = 'dead' THEN now() ELSE NULL END,
		    updated_at = now()
		WHERE id = $1 AND status = 'running' AND locked_by = $2
	`, job.ID, workerID, status, backoff.String(), jobErr.Error())
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func (s *Store) HeartbeatWorker(
	ctx context.Context,
	workerID string,
	version string,
	currentJobID string,
) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO worker_heartbeats (worker_id, version, current_job_id)
		VALUES ($1, $2, NULLIF($3, '')::uuid)
		ON CONFLICT (worker_id)
		DO UPDATE SET
			version = EXCLUDED.version,
			current_job_id = EXCLUDED.current_job_id,
			updated_at = now()
	`, workerID, version, currentJobID)
	return mapError(err)
}

func (s *Store) encodeEmailPayload(payload any) ([]byte, error) {
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	protected, err := s.cipher.Protect(string(plain))
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"encrypted": protected})
}

func (s *Store) DecodeEmailPayload(payload json.RawMessage, target any) error {
	var envelope struct {
		Encrypted string `json:"encrypted"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return err
	}
	plain, err := s.cipher.Reveal(envelope.Encrypted)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(plain), target)
}
