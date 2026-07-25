package store

import (
	"context"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/domain"
)

type CreateMediaUploadParams struct {
	UserID       string
	ObjectKey    string
	ContentType  string
	ExpectedSize int64
	ExpiresAt    time.Time
}

func scanMediaUpload(row scanner) (domain.MediaUpload, error) {
	var upload domain.MediaUpload
	err := row.Scan(
		&upload.ID,
		&upload.UserID,
		&upload.ObjectKey,
		&upload.ContentType,
		&upload.ExpectedSize,
		&upload.ActualSize,
		&upload.DurationSeconds,
		&upload.Width,
		&upload.Height,
		&upload.VideoCodec,
		&upload.ScannedAt,
		&upload.Status,
		&upload.ExpiresAt,
		&upload.CreatedAt,
	)
	return upload, mapError(err)
}

func (s *Store) CreateMediaUpload(
	ctx context.Context,
	params CreateMediaUploadParams,
) (domain.MediaUpload, error) {
	return scanMediaUpload(s.pool.QueryRow(ctx, `
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
}

func (s *Store) GetMediaUpload(
	ctx context.Context,
	id string,
	userID string,
) (domain.MediaUpload, error) {
	return scanMediaUpload(s.pool.QueryRow(ctx, `
		SELECT
			id, user_id, object_key, content_type, expected_size,
			actual_size, duration_seconds, width, height, COALESCE(video_codec, ''), scanned_at,
			status, expires_at, created_at
		FROM media_uploads
		WHERE id = $1 AND user_id = $2
	`, id, userID))
}

func (s *Store) CompleteMediaUpload(
	ctx context.Context,
	id string,
	userID string,
	actualSize int64,
) (domain.MediaUpload, error) {
	return scanMediaUpload(s.pool.QueryRow(ctx, `
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
}

func (s *Store) UpdateMediaInspection(
	ctx context.Context,
	id string,
	durationSeconds float64,
	width int,
	height int,
	videoCodec string,
	scannedAt time.Time,
	retainedUntil time.Time,
	processedObjectKey string,
	thumbnailObjectKey string,
) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE media_uploads
		SET duration_seconds = $2,
		    width = $3,
		    height = $4,
		    video_codec = $5,
		    scanned_at = $6,
		    retained_until = $7,
		    processed_object_key = $8,
		    thumbnail_object_key = $9,
		    updated_at = now()
		WHERE id = $1
	`,
		id,
		durationSeconds,
		width,
		height,
		videoCodec,
		scannedAt,
		retainedUntil,
		processedObjectKey,
		thumbnailObjectKey,
	)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RejectMediaUpload(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE media_uploads
		SET status = 'rejected', updated_at = now()
		WHERE id = $1
	`, id)
	return mapError(err)
}
