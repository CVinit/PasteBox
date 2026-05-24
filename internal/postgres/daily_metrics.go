package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DailyMetricStore struct {
	pool *pgxpool.Pool
}

func NewDailyMetricStore(pool *pgxpool.Pool) *DailyMetricStore {
	return &DailyMetricStore{pool: pool}
}

func (s *DailyMetricStore) DailyMetric(ctx context.Context, userID string, kind string, day time.Time) (int64, error) {
	var bytes int64
	err := s.pool.QueryRow(ctx, `
SELECT bytes
FROM daily_metrics
WHERE user_id = $1 AND metric_kind = $2 AND metric_day = $3
`, userID, kind, metricDay(day)).Scan(&bytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("read daily metric: %w", err)
	}
	return bytes, nil
}

func (s *DailyMetricStore) RecordDailyMetric(ctx context.Context, userID string, kind string, day time.Time, bytes int64) error {
	if bytes <= 0 {
		return nil
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO daily_metrics (user_id, metric_kind, metric_day, bytes)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, metric_kind, metric_day)
DO UPDATE SET bytes = daily_metrics.bytes + EXCLUDED.bytes
`, userID, kind, metricDay(day), bytes); err != nil {
		return fmt.Errorf("record daily metric: %w", err)
	}
	return nil
}

func metricDay(day time.Time) time.Time {
	year, month, date := day.UTC().Date()
	return time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
}
