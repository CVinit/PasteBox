package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"pastebox/internal/postgres"
)

type CleanupService interface {
	RunCleanup(actorID string) (map[string]int, error)
}

type JobStore interface {
	ListRunnableJobs(ctx context.Context, limit int, now time.Time) ([]postgres.JobRecord, error)
	UpdateJob(ctx context.Context, job postgres.JobRecord) error
}

type Config struct {
	BatchSize    int
	MaxAttempts  int
	PollInterval time.Duration
	Now          func() time.Time
	Logger       *slog.Logger
}

type Summary struct {
	Seen      int
	Completed int
	Retried   int
	Failed    int
}

type Runner struct {
	jobs    JobStore
	service CleanupService
	cfg     Config
}

func NewRunner(jobs JobStore, service CleanupService, cfg Config) *Runner {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 25
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Runner{jobs: jobs, service: service, cfg: cfg}
}

func (r *Runner) Run(ctx context.Context) error {
	for {
		summary, err := r.RunOnce(ctx)
		if err != nil {
			return err
		}
		if summary.Seen > 0 {
			r.cfg.Logger.Info("worker batch processed", "seen", summary.Seen, "completed", summary.Completed, "retried", summary.Retried, "failed", summary.Failed)
		}

		timer := time.NewTimer(r.cfg.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (r *Runner) RunOnce(ctx context.Context) (Summary, error) {
	now := r.cfg.Now().UTC()
	jobs, err := r.jobs.ListRunnableJobs(ctx, r.cfg.BatchSize, now)
	if err != nil {
		return Summary{}, fmt.Errorf("list runnable jobs: %w", err)
	}

	summary := Summary{Seen: len(jobs)}
	for _, job := range jobs {
		if err := r.handleJob(ctx, job); err != nil {
			if updateErr := r.markJobFailedOrRetry(ctx, job, err); updateErr != nil {
				return summary, updateErr
			}
			if job.Attempts+1 >= r.cfg.MaxAttempts {
				summary.Failed++
			} else {
				summary.Retried++
			}
			continue
		}
		if err := r.markJobCompleted(ctx, job); err != nil {
			return summary, err
		}
		summary.Completed++
	}
	return summary, nil
}

func (r *Runner) handleJob(ctx context.Context, job postgres.JobRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch job.Kind {
	case "cleanup":
		_, err := r.service.RunCleanup("")
		return err
	default:
		return fmt.Errorf("unsupported job kind %q", job.Kind)
	}
}

func (r *Runner) markJobCompleted(ctx context.Context, job postgres.JobRecord) error {
	now := r.cfg.Now().UTC()
	job.Status = "completed"
	job.Attempts++
	job.LastError = ""
	job.RunAfter = now
	job.UpdatedAt = now
	if err := r.jobs.UpdateJob(ctx, job); err != nil {
		return fmt.Errorf("mark job completed %s: %w", job.ID, err)
	}
	return nil
}

func (r *Runner) markJobFailedOrRetry(ctx context.Context, job postgres.JobRecord, cause error) error {
	now := r.cfg.Now().UTC()
	job.Attempts++
	job.LastError = errorString(cause)
	job.UpdatedAt = now
	if job.Attempts >= r.cfg.MaxAttempts || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		job.Status = "failed"
		job.RunAfter = now
	} else {
		job.Status = "pending"
		job.RunAfter = now.Add(retryBackoff(job.Attempts))
	}
	if err := r.jobs.UpdateJob(ctx, job); err != nil {
		return fmt.Errorf("mark job failed %s: %w", job.ID, err)
	}
	return nil
}

func retryBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := time.Minute
	for i := 1; i < attempts && delay < 30*time.Minute; i++ {
		delay *= 2
	}
	if delay > 30*time.Minute {
		return 30 * time.Minute
	}
	return delay
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	const maxLen = 500
	if len(message) > maxLen {
		return message[:maxLen]
	}
	return message
}
