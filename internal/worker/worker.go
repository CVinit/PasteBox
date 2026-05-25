package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	ListRunnableJobs(ctx context.Context, limit int, now time.Time) ([]postgres.JobRecord, error)
	UpdateJob(ctx context.Context, job postgres.JobRecord) error
}

type MailStore interface {
	ListRunnableMail(ctx context.Context, limit int, now time.Time) ([]postgres.MailRecord, error)
	UpdateMail(ctx context.Context, mail postgres.MailRecord) error
}

type MailSender interface {
	Send(ctx context.Context, to string, subject string, body string) error
}

type Config struct {
	BatchSize    int
	MaxAttempts  int
	PollInterval time.Duration
	Now          func() time.Time
	Logger       *slog.Logger
	Scanner      app.Scanner
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
	if r.mails == nil || r.mailSender == nil {
		return summary, nil
	}
	mails, err := r.mails.ListRunnableMail(ctx, r.cfg.BatchSize, now)
	if err != nil {
		return summary, fmt.Errorf("list runnable mail: %w", err)
	}
	summary.MailSeen = len(mails)
	for _, mail := range mails {
		if err := r.mailSender.Send(ctx, mail.To, mail.Subject, mail.Body); err != nil {
			if updateErr := r.markMailFailedOrRetry(ctx, mail, err); updateErr != nil {
				return summary, updateErr
			}
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
		summary.MailSent++
	}
	return summary, nil
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
