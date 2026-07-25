package store

import (
	"context"
	"time"
)

func (s *Store) HasHealthyWorker(ctx context.Context, staleAfter time.Duration) (bool, error) {
	var healthy bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM worker_heartbeats
			WHERE updated_at > now() - $1::interval
		)
	`, staleAfter.String()).Scan(&healthy)
	return healthy, mapError(err)
}
