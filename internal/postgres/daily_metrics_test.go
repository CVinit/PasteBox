package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDailyMetricStorePersistsAndAccumulatesByUTCDay(t *testing.T) {
	databaseURL := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL to run PostgreSQL daily metrics integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := ApplyMigrations(ctx, databaseURL); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	userID := "usr_daily_metrics_test"
	_, _ = pool.Exec(ctx, `DELETE FROM daily_metrics WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM daily_metrics WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, userID)
	})
	_, err = pool.Exec(ctx, `
INSERT INTO users (
	id,
	email,
	display_name,
	language,
	password_hash,
	role,
	email_verified,
	plan_id,
	created_at,
	updated_at
) VALUES (
	$1,
	'daily-metrics-test@example.com',
	'Daily Metrics Test',
	'en',
	'test-password-hash',
	'user',
	true,
	'free',
	now(),
	now()
)
`, userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	store := NewDailyMetricStore(pool)
	day := time.Date(2026, 5, 25, 23, 59, 59, 0, time.FixedZone("UTC+8", 8*60*60))
	if got, err := store.DailyMetric(ctx, userID, "upload", day); err != nil || got != 0 {
		t.Fatalf("expected missing metric to read as zero, got %d err=%v", got, err)
	}
	if err := store.RecordDailyMetric(ctx, userID, "upload", day, 5); err != nil {
		t.Fatalf("record first metric: %v", err)
	}
	if err := store.RecordDailyMetric(ctx, userID, "upload", day.Add(30*time.Minute), 7); err != nil {
		t.Fatalf("record second metric: %v", err)
	}
	if err := store.RecordDailyMetric(ctx, userID, "upload", day, 0); err != nil {
		t.Fatalf("zero metric should be ignored: %v", err)
	}
	if got, err := store.DailyMetric(ctx, userID, "upload", day); err != nil || got != 12 {
		t.Fatalf("expected accumulated metric to be 12, got %d err=%v", got, err)
	}

	nextUTCDate := day.Add(24 * time.Hour)
	if got, err := store.DailyMetric(ctx, userID, "upload", nextUTCDate); err != nil || got != 0 {
		t.Fatalf("expected a different UTC day to read as zero, got %d err=%v", got, err)
	}
}
