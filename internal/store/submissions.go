package store

import (
	"context"
	"errors"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
	"github.com/jackc/pgx/v5"
)

const submissionColumns = `
	s.id, s.challenge_id, s.user_id, u.handle, s.media_upload_id,
	s.caption, s.verification_status, s.verification_details,
	s.moderation_status, s.style_score, s.vote_count, s.published_at,
	s.created_at, s.updated_at, m.object_key
`

func scanSubmission(row scanner) (domain.Submission, error) {
	var submission domain.Submission
	err := row.Scan(
		&submission.ID,
		&submission.ChallengeID,
		&submission.UserID,
		&submission.UserHandle,
		&submission.MediaUploadID,
		&submission.Caption,
		&submission.VerificationStatus,
		&submission.VerificationDetails,
		&submission.ModerationStatus,
		&submission.StyleScore,
		&submission.VoteCount,
		&submission.PublishedAt,
		&submission.CreatedAt,
		&submission.UpdatedAt,
		&submission.MediaObjectKey,
	)
	return submission, mapError(err)
}

type CreateSubmissionParams struct {
	ChallengeID   string
	UserID        string
	MediaUploadID string
	Caption       string
	Now           time.Time
}

func (s *Store) CreateSubmission(
	ctx context.Context,
	params CreateSubmissionParams,
) (domain.Submission, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Submission{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	var challengeOpen bool
	err = tx.QueryRow(ctx, `
		SELECT status = 'published' AND starts_at <= $2 AND ends_at > $2
		FROM challenges
		WHERE id = $1
		FOR SHARE
	`, params.ChallengeID, params.Now).Scan(&challengeOpen)
	if err != nil {
		return domain.Submission{}, mapError(err)
	}
	if !challengeOpen {
		return domain.Submission{}, ErrInvalid
	}

	var uploadStatus string
	err = tx.QueryRow(ctx, `
		SELECT status
		FROM media_uploads
		WHERE id = $1 AND user_id = $2
		FOR UPDATE
	`, params.MediaUploadID, params.UserID).Scan(&uploadStatus)
	if err != nil {
		return domain.Submission{}, mapError(err)
	}
	if uploadStatus != "uploaded" {
		return domain.Submission{}, ErrInvalid
	}

	var submissionID string
	err = tx.QueryRow(ctx, `
		INSERT INTO submissions (
			challenge_id, user_id, media_upload_id, caption
		)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`,
		params.ChallengeID,
		params.UserID,
		params.MediaUploadID,
		params.Caption,
	).Scan(&submissionID)
	if err != nil {
		return domain.Submission{}, mapError(err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE media_uploads
		SET status = 'consumed', updated_at = now()
		WHERE id = $1
	`, params.MediaUploadID); err != nil {
		return domain.Submission{}, mapError(err)
	}

	submission, err := scanSubmission(tx.QueryRow(ctx, `
		SELECT `+submissionColumns+`
		FROM submissions s
		JOIN users u ON u.id = s.user_id
		JOIN media_uploads m ON m.id = s.media_upload_id
		WHERE s.id = $1
	`, submissionID))
	if err != nil {
		return domain.Submission{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Submission{}, mapError(err)
	}
	return submission, nil
}

func (s *Store) GetSubmission(ctx context.Context, id string) (domain.Submission, error) {
	return scanSubmission(s.pool.QueryRow(ctx, `
		SELECT `+submissionColumns+`
		FROM submissions s
		JOIN users u ON u.id = s.user_id
		JOIN media_uploads m ON m.id = s.media_upload_id
		WHERE s.id = $1
	`, id))
}

func (s *Store) ListChallengeSubmissions(
	ctx context.Context,
	challengeID string,
	viewerID string,
	includeNonPublic bool,
	limit int,
	offset int,
) ([]domain.Submission, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+submissionColumns+`
		FROM submissions s
		JOIN users u ON u.id = s.user_id
		JOIN media_uploads m ON m.id = s.media_upload_id
		WHERE s.challenge_id = $1
		  AND ($3 OR s.moderation_status = 'approved')
		  AND (
			NULLIF($2, '')::uuid IS NULL
			OR NOT EXISTS (
				SELECT 1 FROM user_blocks b
				WHERE (b.blocker_id = NULLIF($2, '')::uuid AND b.blocked_user_id = s.user_id)
				   OR (b.blocker_id = s.user_id AND b.blocked_user_id = NULLIF($2, '')::uuid)
			)
		  )
		ORDER BY s.style_score DESC, s.created_at ASC
		LIMIT $4 OFFSET $5
	`, challengeID, viewerID, includeNonPublic, limit, offset)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var submissions []domain.Submission
	for rows.Next() {
		submission, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		submissions = append(submissions, submission)
	}
	return submissions, mapError(rows.Err())
}

func (s *Store) Vote(
	ctx context.Context,
	submissionID string,
	userID string,
	score int,
) (domain.Submission, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Submission{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	var ownerID, moderationStatus string
	err = tx.QueryRow(ctx, `
		SELECT user_id, moderation_status
		FROM submissions
		WHERE id = $1
		FOR UPDATE
	`, submissionID).Scan(&ownerID, &moderationStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Submission{}, ErrNotFound
	}
	if err != nil {
		return domain.Submission{}, mapError(err)
	}
	if moderationStatus != "approved" || ownerID == userID {
		return domain.Submission{}, ErrForbidden
	}
	var blocked bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_blocks
			WHERE (blocker_id = $1 AND blocked_user_id = $2)
			   OR (blocker_id = $2 AND blocked_user_id = $1)
		)
	`, userID, ownerID).Scan(&blocked); err != nil {
		return domain.Submission{}, mapError(err)
	}
	if blocked {
		return domain.Submission{}, ErrForbidden
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO votes (submission_id, user_id, score)
		VALUES ($1, $2, $3)
		ON CONFLICT (submission_id, user_id)
		DO UPDATE SET score = EXCLUDED.score, updated_at = now()
	`, submissionID, userID, score); err != nil {
		return domain.Submission{}, mapError(err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE submissions s
		SET
			style_score = aggregate.average_score,
			vote_count = aggregate.vote_count,
			updated_at = now()
		FROM (
			SELECT
				COALESCE(avg(score), 0)::numeric(4,2) AS average_score,
				count(*)::integer AS vote_count
			FROM votes
			WHERE submission_id = $1
		) aggregate
		WHERE s.id = $1
	`, submissionID); err != nil {
		return domain.Submission{}, mapError(err)
	}

	submission, err := scanSubmission(tx.QueryRow(ctx, `
		SELECT `+submissionColumns+`
		FROM submissions s
		JOIN users u ON u.id = s.user_id
		JOIN media_uploads m ON m.id = s.media_upload_id
		WHERE s.id = $1
	`, submissionID))
	if err != nil {
		return domain.Submission{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Submission{}, mapError(err)
	}
	return submission, nil
}

func (s *Store) ListModerationSubmissions(
	ctx context.Context,
	status string,
	limit int,
	offset int,
) ([]domain.Submission, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+submissionColumns+`
		FROM submissions s
		JOIN users u ON u.id = s.user_id
		JOIN media_uploads m ON m.id = s.media_upload_id
		WHERE $1 = '' OR s.moderation_status = $1
		ORDER BY s.created_at
		LIMIT $2 OFFSET $3
	`, status, limit, offset)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var submissions []domain.Submission
	for rows.Next() {
		submission, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		submissions = append(submissions, submission)
	}
	return submissions, mapError(rows.Err())
}

func (s *Store) UpdateVerification(
	ctx context.Context,
	submissionID string,
	status string,
	details []byte,
	actorID string,
) (domain.Submission, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Submission{}, mapError(err)
	}
	defer tx.Rollback(ctx)

	command, err := tx.Exec(ctx, `
		UPDATE submissions
		SET verification_status = $2,
		    verification_details = $3,
		    updated_at = now()
		WHERE id = $1
	`, submissionID, status, details)
	if err != nil {
		return domain.Submission{}, mapError(err)
	}
	if command.RowsAffected() == 0 {
		return domain.Submission{}, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			actor_id, action, entity_type, entity_id, metadata
		)
		VALUES (
			$1, 'submission.verification_updated', 'submission', $2,
			jsonb_build_object('status', $3)
		)
	`, actorID, submissionID, status); err != nil {
		return domain.Submission{}, mapError(err)
	}

	submission, err := scanSubmission(tx.QueryRow(ctx, `
		SELECT `+submissionColumns+`
		FROM submissions s
		JOIN users u ON u.id = s.user_id
		JOIN media_uploads m ON m.id = s.media_upload_id
		WHERE s.id = $1
	`, submissionID))
	if err != nil {
		return domain.Submission{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Submission{}, mapError(err)
	}
	return submission, nil
}
