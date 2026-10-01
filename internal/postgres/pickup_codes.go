package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

// pickupAttemptPruneBatch bounds how many expired counter rows one failure may
// remove, so housekeeping stays off the hot path.
const pickupAttemptPruneBatch = 50

// PickupAttemptStore counts failed pickup-code guesses in PostgreSQL so every
// API instance shares one budget. Counting in memory only would let a guesser
// multiply their attempts by spreading them across processes.
type PickupAttemptStore struct {
	pool *pgxpool.Pool
}

func NewPickupAttemptStore(pool *pgxpool.Pool) *PickupAttemptStore {
	return &PickupAttemptStore{pool: pool}
}

func (s *PickupAttemptStore) WithPickupAttemptLock(ctx context.Context, key string, window time.Duration, fn func(context.Context) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin pickup attempt: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		return fmt.Errorf("lock pickup attempt: %w", err)
	}
	lockCtx := context.WithValue(ctx, scopedTransactionKey{}, scopedTransaction{pool: s.pool, tx: tx})
	lookupErr := fn(lockCtx)
	// A not-found result is precisely what spends a failure. Commit it; a SQL
	// failure leaves the transaction aborted and Commit will return an error.
	if err := tx.Commit(ctx); err != nil {
		return errors.Join(lookupErr, fmt.Errorf("commit pickup attempt: %w", err))
	}
	s.pruneExpiredAttempts(ctx, time.Now().UTC().Add(-2*window))
	return lookupErr
}

// PickupAttemptCount reads the failures recorded for a key in the current
// window. An unknown key, or one whose window has passed, reads as zero.
func (s *PickupAttemptStore) PickupAttemptCount(ctx context.Context, key string, window time.Duration, now time.Time) (app.PickupAttemptWindow, error) {
	if window <= 0 {
		return app.PickupAttemptWindow{}, fmt.Errorf("pickup attempt window must be positive")
	}
	var windowStart time.Time
	var count int
	err := contentDB(ctx, s.pool).QueryRow(ctx, `
SELECT window_start, attempt_count
FROM pickup_code_attempts
WHERE attempt_key = $1
  AND window_start + make_interval(secs => $2) > $3
`, key, window.Seconds(), now).Scan(&windowStart, &count)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.PickupAttemptWindow{Start: now}, nil
		}
		return app.PickupAttemptWindow{}, fmt.Errorf("read pickup attempts: %w", err)
	}
	return app.PickupAttemptWindow{Start: windowStart, Count: count}, nil
}

// RecordPickupFailure counts one failed guess, restarting the window in place
// once it has passed.
func (s *PickupAttemptStore) RecordPickupFailure(ctx context.Context, key string, window time.Duration, now time.Time) (app.PickupAttemptWindow, error) {
	if window <= 0 {
		return app.PickupAttemptWindow{}, fmt.Errorf("pickup attempt window must be positive")
	}
	var windowStart time.Time
	var count int
	if err := contentDB(ctx, s.pool).QueryRow(ctx, `
INSERT INTO pickup_code_attempts (attempt_key, window_start, attempt_count)
VALUES ($1, $2, 1)
ON CONFLICT (attempt_key) DO UPDATE SET
    attempt_count = CASE
        WHEN pickup_code_attempts.window_start + make_interval(secs => $3) > $2
        THEN pickup_code_attempts.attempt_count + 1
        ELSE 1
    END,
    window_start = CASE
        WHEN pickup_code_attempts.window_start + make_interval(secs => $3) > $2
        THEN pickup_code_attempts.window_start
        ELSE $2
    END
RETURNING window_start, attempt_count
`, key, now, window.Seconds()).Scan(&windowStart, &count); err != nil {
		return app.PickupAttemptWindow{}, fmt.Errorf("record pickup failure: %w", err)
	}

	if contentDB(ctx, s.pool) == s.pool {
		s.pruneExpiredAttempts(ctx, now.Add(-2*window))
	}

	return app.PickupAttemptWindow{Start: windowStart, Count: count}, nil
}

// pruneExpiredAttempts keeps the counter table from growing with every client
// that ever guessed. A failure here is housekeeping only: the failure above is
// already committed, so it is not worth failing the request.
func (s *PickupAttemptStore) pruneExpiredAttempts(ctx context.Context, before time.Time) {
	_, _ = s.pool.Exec(ctx, `
DELETE FROM pickup_code_attempts
WHERE attempt_key IN (
    SELECT attempt_key
    FROM pickup_code_attempts
    WHERE window_start < $1
    LIMIT $2
)
`, before, pickupAttemptPruneBatch)
}
