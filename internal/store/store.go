package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/arrorLabArts/heatcheck/internal/securedata"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound             = errors.New("resource not found")
	ErrConflict             = errors.New("resource conflict")
	ErrForbidden            = errors.New("operation forbidden")
	ErrInvalid              = errors.New("invalid operation")
	ErrPolicy               = errors.New("required policy acceptance is missing")
	ErrToken                = errors.New("refresh token is invalid")
	ErrRateLimit            = errors.New("rate limit exceeded")
	ErrSubscriptionRequired = errors.New("an active subscription is required")
	ErrUsageLimit           = errors.New("submission usage limit reached")
)

type UsageLimitError struct {
	Period  string
	Limit   int
	ResetAt time.Time
}

func (e *UsageLimitError) Error() string {
	return fmt.Sprintf("%s submission limit of %d reached", e.Period, e.Limit)
}

func (e *UsageLimitError) Unwrap() error {
	return ErrUsageLimit
}

type Store struct {
	pool   *pgxpool.Pool
	cipher *securedata.Cipher
}

type Option func(*Store)

func WithCipher(cipher *securedata.Cipher) Option {
	return func(store *Store) {
		store.cipher = cipher
	}
}

func New(pool *pgxpool.Pool, options ...Option) *Store {
	result := &Store{pool: pool}
	for _, option := range options {
		option(result)
	}
	return result
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23503", "23514", "22P02":
			return ErrInvalid
		}
	}
	return fmt.Errorf("database operation: %w", err)
}

type scanner interface {
	Scan(dest ...any) error
}
