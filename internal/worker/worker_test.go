package worker

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"pastebox/internal/app"
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

func TestRunnerCompletesScanJob(t *testing.T) {
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	jobs := &fakeJobStore{runnable: []postgres.JobRecord{{
		ID:        "job_scan",
		Kind:      "scan",
		TargetID:  "att_scan",
		Status:    "pending",
		RunAfter:  now.Add(-time.Minute),
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now.Add(-time.Minute),
	}}}
	service := &fakeCleanupService{}

	runner := NewRunner(jobs, service, Config{Now: func() time.Time { return now }, Logger: slog.Default(), Scanner: fakeScanner{}})
	summary, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if summary.Seen != 1 || summary.Completed != 1 || summary.Retried != 0 || summary.Failed != 0 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if service.scanCalls != 1 || service.scannedAttachmentID != "att_scan" {
		t.Fatalf("expected scan call for attachment, calls=%d id=%q", service.scanCalls, service.scannedAttachmentID)
	}
	updated := jobs.updated[0]
	if updated.Status != "completed" || updated.Attempts != 1 || updated.LastError != "" {
		t.Fatalf("expected completed scan job update, got %#v", updated)
	}
}

func TestRunnerRetriesScanWhenScannerIsMissing(t *testing.T) {
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	jobs := &fakeJobStore{runnable: []postgres.JobRecord{{
		ID:        "job_scan_missing",
		Kind:      "scan",
		TargetID:  "att_scan",
		Status:    "pending",
		RunAfter:  now.Add(-time.Minute),
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now.Add(-time.Minute),
	}}}

	runner := NewRunner(jobs, &fakeCleanupService{}, Config{Now: func() time.Time { return now }, Logger: slog.Default()})
	summary, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if summary.Seen != 1 || summary.Completed != 0 || summary.Retried != 1 || summary.Failed != 0 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	updated := jobs.updated[0]
	if updated.Status != "pending" || updated.Attempts != 1 || updated.LastError != "scanner is not configured" {
		t.Fatalf("expected retry scan job update, got %#v", updated)
	}
}

func TestRunnerSendsRunnableMail(t *testing.T) {
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	mails := &fakeMailStore{runnable: []postgres.MailRecord{{
		ID:        "mail_verify",
		To:        "user@example.com",
		Subject:   "Verify",
		Body:      "Token",
		Status:    "queued",
		RunAfter:  now.Add(-time.Minute),
		CreatedAt: now.Add(-time.Minute),
	}}}
	sender := &fakeMailSender{}

	runner := NewRunnerWithMail(&fakeJobStore{}, mails, sender, &fakeCleanupService{}, Config{Now: func() time.Time { return now }, Logger: slog.Default()})
	summary, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if summary.MailSeen != 1 || summary.MailSent != 1 || summary.MailRetried != 0 || summary.MailFailed != 0 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if len(sender.sent) != 1 || sender.sent[0].to != "user@example.com" || sender.sent[0].subject != "Verify" {
		t.Fatalf("expected mail delivery, got %#v", sender.sent)
	}
	updated := mails.updated[0]
	if updated.Status != "sent" || updated.Attempts != 1 || updated.LastError != "" || updated.SentAt == nil || !updated.SentAt.Equal(now) {
		t.Fatalf("expected sent mail update, got %#v", updated)
	}
}

func TestRunnerRetriesFailedMailWithBackoff(t *testing.T) {
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	mails := &fakeMailStore{runnable: []postgres.MailRecord{{
		ID:        "mail_retry",
		To:        "user@example.com",
		Subject:   "Verify",
		Body:      "Token",
		Status:    "queued",
		Attempts:  1,
		RunAfter:  now.Add(-time.Minute),
		CreatedAt: now.Add(-time.Minute),
	}}}
	sender := &fakeMailSender{err: errors.New("smtp unavailable")}

	runner := NewRunnerWithMail(&fakeJobStore{}, mails, sender, &fakeCleanupService{}, Config{Now: func() time.Time { return now }, Logger: slog.Default()})
	summary, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if summary.MailSeen != 1 || summary.MailSent != 0 || summary.MailRetried != 1 || summary.MailFailed != 0 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	updated := mails.updated[0]
	if updated.Status != "queued" || updated.Attempts != 2 || updated.LastError != "smtp unavailable" {
		t.Fatalf("expected retry mail update, got %#v", updated)
	}
	if !updated.RunAfter.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("expected second-attempt mail backoff, got %s", updated.RunAfter)
	}
}

func TestRunnerMarksMailFailedAfterMaxAttempts(t *testing.T) {
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	mails := &fakeMailStore{runnable: []postgres.MailRecord{{
		ID:        "mail_failed",
		To:        "user@example.com",
		Subject:   "Verify",
		Body:      "Token",
		Status:    "queued",
		Attempts:  4,
		RunAfter:  now.Add(-time.Minute),
		CreatedAt: now.Add(-time.Minute),
	}}}
	sender := &fakeMailSender{err: errors.New("smtp unavailable")}

	runner := NewRunnerWithMail(&fakeJobStore{}, mails, sender, &fakeCleanupService{}, Config{Now: func() time.Time { return now }, MaxAttempts: 5, Logger: slog.Default()})
	summary, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if summary.MailSeen != 1 || summary.MailSent != 0 || summary.MailRetried != 0 || summary.MailFailed != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	updated := mails.updated[0]
	if updated.Status != "failed" || updated.Attempts != 5 || updated.LastError != "smtp unavailable" || !updated.RunAfter.Equal(now) {
		t.Fatalf("expected failed mail update, got %#v", updated)
	}
}

type fakeCleanupService struct {
	cleanupCalls        int
	scanCalls           int
	scannedAttachmentID string
	err                 error
	scanErr             error
}

func (s *fakeCleanupService) RunCleanup(_ string) (map[string]int, error) {
	s.cleanupCalls++
	if s.err != nil {
		return nil, s.err
	}
	return map[string]int{"expired": 1}, nil
}

func (s *fakeCleanupService) RunAttachmentScan(_ app.Scanner, attachmentID string) error {
	s.scanCalls++
	s.scannedAttachmentID = attachmentID
	return s.scanErr
}

type fakeScanner struct{}

func (fakeScanner) Scan(_ context.Context, _ string, _ string, _ []byte) (app.ScanResult, error) {
	return app.ScanResult{Status: "clean"}, nil
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

type fakeMailStore struct {
	runnable []postgres.MailRecord
	updated  []postgres.MailRecord
}

func (s *fakeMailStore) ListRunnableMail(_ context.Context, _ int, _ time.Time) ([]postgres.MailRecord, error) {
	return append([]postgres.MailRecord(nil), s.runnable...), nil
}

func (s *fakeMailStore) UpdateMail(_ context.Context, mail postgres.MailRecord) error {
	s.updated = append(s.updated, mail)
	return nil
}

type fakeMailSender struct {
	err  error
	sent []sentMail
}

type sentMail struct {
	to      string
	subject string
	body    string
}

func (s *fakeMailSender) Send(_ context.Context, to string, subject string, body string) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, sentMail{to: to, subject: subject, body: body})
	return nil
}
