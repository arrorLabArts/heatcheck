package store

import (
	"context"
	"time"
)

func (s *Store) AllowRateLimit(
	ctx context.Context,
	key string,
	maximum int,
	window time.Duration,
	now time.Time,
) (time.Time, error) {
	windowStart := now.Truncate(window)
	expiresAt := windowStart.Add(window)
	var count int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO rate_limits (key, window_started_at, count, expires_at)
		VALUES ($1, $2, 1, $3)
		ON CONFLICT (key, window_started_at)
		DO UPDATE SET count = rate_limits.count + 1
		WHERE rate_limits.count < $4
		RETURNING count
	`, key, windowStart, expiresAt, maximum).Scan(&count)
	if err != nil {
		mapped := mapError(err)
		if mapped == ErrNotFound {
			return expiresAt, ErrRateLimit
		}
		return expiresAt, mapped
	}
	return expiresAt, nil
}
