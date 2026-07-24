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
			actual_size, status, expires_at, created_at
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
			actual_size, status, expires_at, created_at
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
			actual_size, status, expires_at, created_at
	`, id, userID, actualSize))
}
