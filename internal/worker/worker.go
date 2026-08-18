package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"pastebox/internal/app"
	"pastebox/internal/postgres"
)

type Service interface {
	RunCleanup(actorID string) (map[string]int, error)
	RunBillingReconciliation(actorID string) (map[string]int, error)
	RunAttachmentScan(scanner app.Scanner, attachmentID string) error
}

type JobStore interface {
	ClaimRunnableJobs(ctx context.Context, workerID string, limit int, now time.Time, leaseExpiresAt time.Time) ([]postgres.JobRecord, error)
	UpdateJob(ctx context.Context, job postgres.JobRecord) error
}

type MailStore interface {
	ClaimRunnableMail(ctx context.Context, workerID string, limit int, now time.Time, leaseExpiresAt time.Time) ([]postgres.MailRecord, error)
	UpdateMail(ctx context.Context, mail postgres.MailRecord) error
}

type MailSender interface {
	Send(ctx context.Context, to string, subject string, body string) error
}

type HeartbeatStore interface {
	RecordWorkerHeartbeat(ctx context.Context, workerID string, seenAt time.Time) error
}

type Config struct {
	BatchSize     int
	MaxAttempts   int
	PollInterval  time.Duration
	LeaseDuration time.Duration
	Now           func() time.Time
	Logger        *slog.Logger
	Scanner       app.Scanner
	WorkerID      string
	Heartbeats    HeartbeatStore
}

type Summary struct {
	Seen        int
	Completed   int
	Retried     int
	Failed      int
	MailSeen    int
	MailSent    int
	MailRetried int
	MailFailed  int
}

type Runner struct {
	jobs       JobStore
	mails      MailStore
	mailSender MailSender
	service    Service
	cfg        Config
}

func NewRunner(jobs JobStore, service Service, cfg Config) *Runner {
	return NewRunnerWithMail(jobs, nil, nil, service, cfg)
}

func NewRunnerWithMail(jobs JobStore, mails MailStore, mailSender MailSender, service Service, cfg Config) *Runner {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 25
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 15 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Runner{jobs: jobs, mails: mails, mailSender: mailSender, service: service, cfg: cfg}
}

func (r *Runner) Run(ctx context.Context) error {
	for {
		summary, err := r.RunOnce(ctx)
		if err != nil {
			return err
		}
		if summary.Seen > 0 || summary.MailSeen > 0 {
			r.cfg.Logger.Info(
				"worker batch processed",
				"seen", summary.Seen,
				"completed", summary.Completed,
				"retried", summary.Retried,
				"failed", summary.Failed,
				"mailSeen", summary.MailSeen,
				"mailSent", summary.MailSent,
				"mailRetried", summary.MailRetried,
				"mailFailed", summary.MailFailed,
			)
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
	if err := r.recordHeartbeat(ctx, now); err != nil {
		return Summary{}, err
	}
	workerID := r.workerID()
	jobs, err := r.jobs.ClaimRunnableJobs(ctx, workerID, r.cfg.BatchSize, now, now.Add(r.cfg.LeaseDuration))
	if err != nil {
		return Summary{}, fmt.Errorf("claim runnable jobs: %w", err)
	}
	r.cfg.Logger.Debug("worker jobs polled", "count", len(jobs), "batch_size", r.cfg.BatchSize)

	summary := Summary{Seen: len(jobs)}
	for _, job := range jobs {
		r.cfg.Logger.Debug("worker job started", "kind", job.Kind, "attempts", job.Attempts)
		if err := r.handleJob(ctx, job); err != nil {
			if updateErr := r.markJobFailedOrRetry(ctx, job, err); updateErr != nil {
				return summary, updateErr
			}
			r.cfg.Logger.Debug("worker job retry scheduled", "kind", job.Kind, "attempts", job.Attempts+1)
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
		r.cfg.Logger.Debug("worker job completed", "kind", job.Kind, "attempts", job.Attempts+1)
		summary.Completed++
	}
	if r.mails == nil || r.mailSender == nil {
		if summary.Seen > 0 {
			if err := r.recordHeartbeat(ctx, r.cfg.Now().UTC()); err != nil {
				return summary, err
			}
		}
		return summary, nil
	}
	mailClaimedAt := r.cfg.Now().UTC()
	mails, err := r.mails.ClaimRunnableMail(ctx, workerID, r.cfg.BatchSize, mailClaimedAt, mailClaimedAt.Add(r.cfg.LeaseDuration))
	if err != nil {
		return summary, fmt.Errorf("claim runnable mail: %w", err)
	}
	r.cfg.Logger.Debug("worker mail polled", "count", len(mails), "batch_size", r.cfg.BatchSize)
	summary.MailSeen = len(mails)
	for _, mail := range mails {
		r.cfg.Logger.Debug("worker mail started", "attempts", mail.Attempts)
		if err := r.mailSender.Send(ctx, mail.To, mail.Subject, mail.Body); err != nil {
			if updateErr := r.markMailFailedOrRetry(ctx, mail, err); updateErr != nil {
				return summary, updateErr
			}
			r.cfg.Logger.Debug("worker mail retry scheduled", "attempts", mail.Attempts+1)
			if mail.Attempts+1 >= r.cfg.MaxAttempts {
				summary.MailFailed++
			} else {
				summary.MailRetried++
			}
			continue
		}
		if err := r.markMailSent(ctx, mail); err != nil {
			return summary, err
		}
		r.cfg.Logger.Debug("worker mail sent", "attempts", mail.Attempts+1)
		summary.MailSent++
	}
	if summary.Seen > 0 || summary.MailSeen > 0 {
		if err := r.recordHeartbeat(ctx, r.cfg.Now().UTC()); err != nil {
			return summary, err
		}
	}
	return summary, nil
}

func (r *Runner) recordHeartbeat(ctx context.Context, now time.Time) error {
	if r.cfg.Heartbeats == nil {
		return nil
	}
	workerID := r.workerID()
	if err := r.cfg.Heartbeats.RecordWorkerHeartbeat(ctx, workerID, now.UTC()); err != nil {
		return fmt.Errorf("record worker heartbeat: %w", err)
	}
	return nil
}

func (r *Runner) workerID() string {
	workerID := strings.TrimSpace(r.cfg.WorkerID)
	if workerID == "" {
		return "worker"
	}
	return workerID
}

func (r *Runner) handleJob(ctx context.Context, job postgres.JobRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch job.Kind {
	case "billing_reconcile":
		_, err := r.service.RunBillingReconciliation("")
		return err
	case "cleanup":
		_, err := r.service.RunCleanup("")
		return err
	case "scan":
		if r.cfg.Scanner == nil {
			return errors.New("scanner is not configured")
		}
		return r.service.RunAttachmentScan(r.cfg.Scanner, job.TargetID)
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

func (r *Runner) markMailSent(ctx context.Context, mail postgres.MailRecord) error {
	now := r.cfg.Now().UTC()
	mail.Status = "sent"
	mail.Attempts++
	mail.LastError = ""
	mail.RunAfter = now
	mail.SentAt = &now
	if err := r.mails.UpdateMail(ctx, mail); err != nil {
		return fmt.Errorf("mark mail sent %s: %w", mail.ID, err)
	}
	return nil
}

func (r *Runner) markMailFailedOrRetry(ctx context.Context, mail postgres.MailRecord, cause error) error {
	now := r.cfg.Now().UTC()
	mail.Attempts++
	mail.LastError = errorString(cause)
	mail.SentAt = nil
	if mail.Attempts >= r.cfg.MaxAttempts || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		mail.Status = "failed"
		mail.RunAfter = now
	} else {
		mail.Status = "queued"
		mail.RunAfter = now.Add(retryBackoff(mail.Attempts))
	}
	if err := r.mails.UpdateMail(ctx, mail); err != nil {
		return fmt.Errorf("mark mail failed %s: %w", mail.ID, err)
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
