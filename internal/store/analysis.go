package store

import (
	"context"
	"encoding/json"

	"github.com/arrorLabArts/heatcheck/internal/domain"
)

type SubmissionAnalysisInput struct {
	SubmissionID         string
	UserID               string
	Caption              string
	MediaUploadID        string
	MediaObjectKey       string
	MediaSize            int64
	ChallengeTitle       string
	ChallengeDescription string
	ChallengeRules       json.RawMessage
}

func (s *Store) GetSubmissionAnalysisInput(
	ctx context.Context,
	submissionID string,
) (SubmissionAnalysisInput, error) {
	var input SubmissionAnalysisInput
	err := s.pool.QueryRow(ctx, `
		SELECT
			s.id,
			s.user_id,
			s.caption,
			m.id,
			m.object_key,
			m.actual_size,
			c.title,
			c.description,
			c.rules
		FROM submissions s
		JOIN media_uploads m ON m.id = s.media_upload_id
		JOIN challenges c ON c.id = s.challenge_id
		WHERE s.id = $1
	`, submissionID).Scan(
		&input.SubmissionID,
		&input.UserID,
		&input.Caption,
		&input.MediaUploadID,
		&input.MediaObjectKey,
		&input.MediaSize,
		&input.ChallengeTitle,
		&input.ChallengeDescription,
		&input.ChallengeRules,
	)
	return input, mapError(err)
}

func (s *Store) CompleteAutomatedAnalysis(
	ctx context.Context,
	submissionID string,
	verificationStatus string,
	moderationStatus string,
	details any,
) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE submissions
		SET verification_status = $2::text,
		    moderation_status = $3::text,
		    verification_details = $4,
		    published_at = CASE
		        WHEN $2::text = 'passed' AND $3::text = 'approved' THEN COALESCE(published_at, now())
		        ELSE NULL
		    END,
		    updated_at = now()
		WHERE id = $1 AND verification_status IN ('pending', 'manual_review')
	`, submissionID, verificationStatus, moderationStatus, encoded)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (action, entity_type, entity_id, metadata)
		VALUES (
			'submission.automated_analysis_completed',
			'submission',
			$1,
			jsonb_build_object(
				'verification_status', $2::text,
				'moderation_status', $3::text
			)
		)
	`, submissionID, verificationStatus, moderationStatus); err != nil {
		return mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return mapError(err)
	}
	return nil
}

func (s *Store) MarkAnalysisForManualReview(
	ctx context.Context,
	submissionID string,
	reason string,
) error {
	return s.CompleteAutomatedAnalysis(ctx, submissionID, "manual_review", "pending", map[string]any{
		"source": "worker",
		"reason": reason,
	})
}

func (s *Store) GetPublicUserStats(
	ctx context.Context,
	userID string,
) (domain.PublicUserStats, error) {
	var stats domain.PublicUserStats
	err := s.pool.QueryRow(ctx, `
		WITH approved AS (
			SELECT
				s.id,
				s.challenge_id,
				s.style_score,
				c.starts_at::date AS challenge_date,
				rank() OVER (
					PARTITION BY s.challenge_id
					ORDER BY s.style_score DESC, s.created_at
				) AS rank
			FROM submissions s
			JOIN challenges c ON c.id = s.challenge_id
			WHERE s.verification_status = 'passed'
			  AND s.moderation_status = 'approved'
		),
		user_days AS (
			SELECT DISTINCT challenge_date
			FROM approved
			JOIN submissions s ON s.id = approved.id
			WHERE s.user_id = $1
		),
		grouped AS (
			SELECT
				challenge_date,
				challenge_date - (row_number() OVER (ORDER BY challenge_date))::integer AS streak_group
			FROM user_days
		),
		streaks AS (
			SELECT min(challenge_date) AS first_day, max(challenge_date) AS last_day, count(*)::integer AS length
			FROM grouped
			GROUP BY streak_group
		)
		SELECT
			(SELECT count(*)::integer FROM approved JOIN submissions s ON s.id = approved.id WHERE s.user_id = $1),
			(SELECT count(*)::integer FROM approved JOIN submissions s ON s.id = approved.id WHERE s.user_id = $1 AND approved.rank = 1),
			COALESCE((
				SELECT length FROM streaks
				WHERE last_day >= current_date - 1
				ORDER BY last_day DESC LIMIT 1
			), 0),
			COALESCE((SELECT max(length) FROM streaks), 0),
			COALESCE((
				SELECT avg(approved.style_score)::double precision
				FROM approved JOIN submissions s ON s.id = approved.id
				WHERE s.user_id = $1
			), 0)
	`, userID).Scan(
		&stats.SubmissionCount,
		&stats.ChallengeWins,
		&stats.CurrentStreak,
		&stats.BestStreak,
		&stats.AverageScore,
	)
	return stats, mapError(err)
}
