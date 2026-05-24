package app

import (
	"context"
	"sync"
	"time"
)

type DailyMetricStore interface {
	DailyMetric(ctx context.Context, userID string, kind string, day time.Time) (int64, error)
	RecordDailyMetric(ctx context.Context, userID string, kind string, day time.Time, bytes int64) error
}

type memoryDailyMetricStore struct {
	mu     sync.Mutex
	values map[string]int64
}

func newMemoryDailyMetricStore() *memoryDailyMetricStore {
	return &memoryDailyMetricStore{values: map[string]int64{}}
}

func (s *memoryDailyMetricStore) DailyMetric(_ context.Context, userID string, kind string, day time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[dailyMetricKey(userID, kind, day)], nil
}

func (s *memoryDailyMetricStore) RecordDailyMetric(_ context.Context, userID string, kind string, day time.Time, bytes int64) error {
	if bytes <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[dailyMetricKey(userID, kind, day)] += bytes
	return nil
}

func dailyMetricKey(userID string, kind string, day time.Time) string {
	return kind + "/" + userID + "/" + day.UTC().Format("2006-01-02")
}
