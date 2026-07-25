package store

import (
	"context"

	"github.com/arrorLabArts/heatcheck/internal/domain"
)

type ShareData struct {
	Submission     domain.Submission
	ChallengeTitle string
	DisplayName    string
	Rank           int
	CurrentStreak  int
}

func (s *Store) GetShareData(ctx context.Context, submissionID string) (ShareData, error) {
	var data ShareData
	err := s.pool.QueryRow(ctx, `
		SELECT
			`+submissionColumns+`,
			c.title,
			u.display_name,
			1 + (
				SELECT count(*)::integer
				FROM submissions other
				WHERE other.challenge_id = s.challenge_id
				  AND other.verification_status = 'passed'
				  AND other.moderation_status = 'approved'
				  AND (
					other.style_score > s.style_score
					OR (
						other.style_score = s.style_score
						AND other.created_at < s.created_at
					)
				  )
			)
		FROM submissions s
		JOIN users u ON u.id = s.user_id
		JOIN media_uploads m ON m.id = s.media_upload_id
		JOIN challenges c ON c.id = s.challenge_id
		WHERE s.id = $1
		  AND s.verification_status = 'passed'
		  AND s.moderation_status = 'approved'
	`, submissionID).Scan(
		&data.Submission.ID,
		&data.Submission.ChallengeID,
		&data.Submission.UserID,
		&data.Submission.UserHandle,
		&data.Submission.MediaUploadID,
		&data.Submission.Caption,
		&data.Submission.VerificationStatus,
		&data.Submission.VerificationDetails,
		&data.Submission.ModerationStatus,
		&data.Submission.StyleScore,
		&data.Submission.VoteCount,
		&data.Submission.PublishedAt,
		&data.Submission.CreatedAt,
		&data.Submission.UpdatedAt,
		&data.Submission.MediaObjectKey,
		&data.Submission.MediaThumbnailKey,
		&data.ChallengeTitle,
		&data.DisplayName,
		&data.Rank,
	)
	if err != nil {
		return ShareData{}, mapError(err)
	}
	stats, err := s.GetPublicUserStats(ctx, data.Submission.UserID)
	if err != nil {
		return ShareData{}, err
	}
	data.CurrentStreak = stats.CurrentStreak
	return data, nil
}
