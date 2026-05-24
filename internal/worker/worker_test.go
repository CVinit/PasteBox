package worker

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"pastebox/internal/postgres"
)

func TestRunnerCompletesCleanupJob(t *testing.T) {
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	jobs := &fakeJobStore{runnable: []postgres.JobRecord{{
		ID:        "job_cleanup",
		Kind:      "cleanup",
		Status:    "pending",
		RunAfter:  now.Add(-time.Minute),
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now.Add(-time.Minute),
	}}}
	service := &fakeCleanupService{}

	runner := NewRunner(jobs, service, Config{Now: func() time.Time { return now }, Logger: slog.Default()})
	summary, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if summary.Seen != 1 || summary.Completed != 1 || summary.Retried != 0 || summary.Failed != 0 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if service.cleanupCalls != 1 {
		t.Fatalf("expected cleanup call, got %d", service.cleanupCalls)
	}
	updated := jobs.updated[0]
	if updated.Status != "completed" || updated.Attempts != 1 || updated.LastError != "" {
		t.Fatalf("expected completed job update, got %#v", updated)
	}
}

func TestRunnerRetriesFailedJobWithBackoff(t *testing.T) {
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	jobs := &fakeJobStore{runnable: []postgres.JobRecord{{
		ID:        "job_cleanup_retry",
		Kind:      "cleanup",
		Status:    "pending",
		Attempts:  1,
		RunAfter:  now.Add(-time.Minute),
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now.Add(-time.Minute),
	}}}
	service := &fakeCleanupService{err: errors.New("object cleanup unavailable")}

	runner := NewRunner(jobs, service, Config{Now: func() time.Time { return now }, Logger: slog.Default()})
	summary, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if summary.Seen != 1 || summary.Completed != 0 || summary.Retried != 1 || summary.Failed != 0 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	updated := jobs.updated[0]
	if updated.Status != "pending" || updated.Attempts != 2 || updated.LastError != "object cleanup unavailable" {
		t.Fatalf("expected retry job update, got %#v", updated)
	}
	if !updated.RunAfter.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("expected second-attempt backoff, got %s", updated.RunAfter)
	}
}

func TestRunnerMarksUnsupportedJobFailedAfterMaxAttempts(t *testing.T) {
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	jobs := &fakeJobStore{runnable: []postgres.JobRecord{{
		ID:        "job_unknown",
		Kind:      "unknown",
		Status:    "pending",
		Attempts:  4,
		RunAfter:  now.Add(-time.Minute),
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now.Add(-time.Minute),
	}}}

	runner := NewRunner(jobs, &fakeCleanupService{}, Config{Now: func() time.Time { return now }, MaxAttempts: 5, Logger: slog.Default()})
	summary, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if summary.Seen != 1 || summary.Completed != 0 || summary.Retried != 0 || summary.Failed != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	updated := jobs.updated[0]
	if updated.Status != "failed" || updated.Attempts != 5 || updated.LastError != `unsupported job kind "unknown"` {
		t.Fatalf("expected failed job update, got %#v", updated)
	}
}

type fakeCleanupService struct {
	cleanupCalls int
	err          error
}

func (s *fakeCleanupService) RunCleanup(_ string) (map[string]int, error) {
	s.cleanupCalls++
	if s.err != nil {
		return nil, s.err
	}
	return map[string]int{"expired": 1}, nil
}

type fakeJobStore struct {
	runnable []postgres.JobRecord
	updated  []postgres.JobRecord
}

func (s *fakeJobStore) ListRunnableJobs(_ context.Context, _ int, _ time.Time) ([]postgres.JobRecord, error) {
	return append([]postgres.JobRecord(nil), s.runnable...), nil
}

func (s *fakeJobStore) UpdateJob(_ context.Context, job postgres.JobRecord) error {
	s.updated = append(s.updated, job)
	return nil
}
