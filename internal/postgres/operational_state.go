package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

var (
	ErrOrderNotFound           = errors.Join(errors.New("postgres order not found"), app.ErrStoreNotFound)
	ErrWebhookEventNotFound    = errors.Join(errors.New("postgres webhook event not found"), app.ErrStoreNotFound)
	ErrWebhookEventExists      = errors.Join(errors.New("postgres webhook event exists"), app.ErrStoreConflict)
	ErrReportNotFound          = errors.Join(errors.New("postgres report not found"), app.ErrStoreNotFound)
	ErrJobNotFound             = errors.Join(errors.New("postgres job not found"), app.ErrStoreNotFound)
	ErrMailNotFound            = errors.Join(errors.New("postgres mail not found"), app.ErrStoreNotFound)
	ErrWorkerHeartbeatNotFound = errors.Join(errors.New("postgres worker heartbeat not found"), app.ErrStoreNotFound)
)

type JobRecord struct {
	ID        string
	Kind      string
	TargetID  string
	Status    string
	Attempts  int
	LastError string
	RunAfter  time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

type MailRecord struct {
	ID        string
	To        string
	Subject   string
	Body      string
	Status    string
	Attempts  int
	LastError string
	RunAfter  time.Time
	CreatedAt time.Time
	SentAt    *time.Time
}

type WorkerHeartbeat struct {
	WorkerID   string
	LastSeenAt time.Time
	UpdatedAt  time.Time
}

type OrderStore struct {
	pool *pgxpool.Pool
}

func NewOrderStore(pool *pgxpool.Pool) *OrderStore {
	return &OrderStore{pool: pool}
}

func (s *OrderStore) CreateOrder(ctx context.Context, order app.Order) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO orders (
	id,
	user_id,
	provider,
	plan_id,
	period,
	amount_cents,
	currency,
	status,
	checkout_url,
	address,
	chain,
	tx_id,
	created_at,
	expires_at,
	paid_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
)
`, order.ID, order.UserID, order.Provider, order.PlanID, order.Period, order.AmountCents, order.Currency, order.Status, order.CheckoutURL, order.Address, order.Chain, order.TxID, order.CreatedAt, order.ExpiresAt, order.PaidAt); err != nil {
		return fmt.Errorf("create order: %w", err)
	}
	return nil
}

func (s *OrderStore) OrderByID(ctx context.Context, id string) (app.Order, error) {
	order, err := scanOrder(s.pool.QueryRow(ctx, `
SELECT id, user_id, provider, plan_id, period, amount_cents, currency, status, checkout_url, address, chain, tx_id, created_at, expires_at, paid_at
FROM orders
WHERE id = $1
`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Order{}, ErrOrderNotFound
		}
		return app.Order{}, err
	}
	return order, nil
}

func (s *OrderStore) ListOrdersByUser(ctx context.Context, userID string) ([]app.Order, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, user_id, provider, plan_id, period, amount_cents, currency, status, checkout_url, address, chain, tx_id, created_at, expires_at, paid_at
FROM orders
WHERE user_id = $1
ORDER BY created_at DESC, id DESC
`, userID)
	if err != nil {
		return nil, fmt.Errorf("query orders by user: %w", err)
	}
	defer rows.Close()
	orders := []app.Order{}
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read orders: %w", err)
	}
	return orders, nil
}

func (s *OrderStore) ListOrders(ctx context.Context) ([]app.Order, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, user_id, provider, plan_id, period, amount_cents, currency, status, checkout_url, address, chain, tx_id, created_at, expires_at, paid_at
FROM orders
ORDER BY created_at DESC, id DESC
`)
	if err != nil {
		return nil, fmt.Errorf("query orders: %w", err)
	}
	defer rows.Close()
	return scanOrders(rows)
}

func (s *OrderStore) UpdateOrder(ctx context.Context, order app.Order) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE orders
SET
	provider = $2,
	plan_id = $3,
	period = $4,
	amount_cents = $5,
	currency = $6,
	status = $7,
	checkout_url = $8,
	address = $9,
	chain = $10,
	tx_id = $11,
	expires_at = $12,
	paid_at = $13
WHERE id = $1
`, order.ID, order.Provider, order.PlanID, order.Period, order.AmountCents, order.Currency, order.Status, order.CheckoutURL, order.Address, order.Chain, order.TxID, order.ExpiresAt, order.PaidAt)
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrOrderNotFound
	}
	return nil
}

type WebhookEventStore struct {
	pool *pgxpool.Pool
}

func NewWebhookEventStore(pool *pgxpool.Pool) *WebhookEventStore {
	return &WebhookEventStore{pool: pool}
}

func (s *WebhookEventStore) CreateWebhookEvent(ctx context.Context, event app.WebhookEvent) error {
	metadata, err := json.Marshal(normalizeMetadata(event.Metadata))
	if err != nil {
		return fmt.Errorf("encode webhook metadata: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO webhook_events (
	id,
	provider,
	event_type,
	target_id,
	idempotency_key,
	processed,
	metadata,
	received_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8
)
`, event.ID, event.Provider, event.EventType, event.TargetID, event.IdempotencyKey, event.Processed, string(metadata), event.ReceivedAt); err != nil {
		if isUniqueViolation(err, "webhook_events_idempotency_key_key") {
			return ErrWebhookEventExists
		}
		return fmt.Errorf("create webhook event: %w", err)
	}
	return nil
}

func (s *WebhookEventStore) WebhookEventByID(ctx context.Context, id string) (app.WebhookEvent, error) {
	return s.queryWebhookEvent(ctx, `
SELECT id, provider, event_type, target_id, idempotency_key, processed, metadata, received_at
FROM webhook_events
WHERE id = $1
`, id)
}

func (s *WebhookEventStore) WebhookEventByIdempotencyKey(ctx context.Context, idempotencyKey string) (app.WebhookEvent, error) {
	return s.queryWebhookEvent(ctx, `
SELECT id, provider, event_type, target_id, idempotency_key, processed, metadata, received_at
FROM webhook_events
WHERE idempotency_key = $1
`, idempotencyKey)
}

func (s *WebhookEventStore) UpdateWebhookEventProcessed(ctx context.Context, id string, processed bool) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE webhook_events
SET processed = $2
WHERE id = $1
`, id, processed)
	if err != nil {
		return fmt.Errorf("update webhook event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrWebhookEventNotFound
	}
	return nil
}

func (s *WebhookEventStore) ListWebhookEvents(ctx context.Context) ([]app.WebhookEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, provider, event_type, target_id, idempotency_key, processed, metadata, received_at
FROM webhook_events
ORDER BY received_at DESC, id DESC
`)
	if err != nil {
		return nil, fmt.Errorf("query webhook events: %w", err)
	}
	defer rows.Close()
	events := []app.WebhookEvent{}
	for rows.Next() {
		event, err := scanWebhookEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read webhook events: %w", err)
	}
	return events, nil
}

func (s *WebhookEventStore) queryWebhookEvent(ctx context.Context, sql string, args ...any) (app.WebhookEvent, error) {
	event, err := scanWebhookEvent(s.pool.QueryRow(ctx, sql, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.WebhookEvent{}, ErrWebhookEventNotFound
		}
		return app.WebhookEvent{}, err
	}
	return event, nil
}

type ReportStore struct {
	pool *pgxpool.Pool
}

func NewReportStore(pool *pgxpool.Pool) *ReportStore {
	return &ReportStore{pool: pool}
}

func (s *ReportStore) CreateReport(ctx context.Context, report app.Report) error {
	userID := nullableString(report.UserID)
	if _, err := s.pool.Exec(ctx, `
INSERT INTO reports (id, user_id, target, reason, status, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
`, report.ID, userID, report.Target, report.Reason, report.Status, report.CreatedAt); err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	return nil
}

func (s *ReportStore) ReportByID(ctx context.Context, id string) (app.Report, error) {
	report, err := scanReport(s.pool.QueryRow(ctx, `
SELECT id, user_id, target, reason, status, created_at
FROM reports
WHERE id = $1
`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Report{}, ErrReportNotFound
		}
		return app.Report{}, err
	}
	return report, nil
}

func (s *ReportStore) UpdateReportStatus(ctx context.Context, id string, status string) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE reports
SET status = $2
WHERE id = $1
`, id, status)
	if err != nil {
		return fmt.Errorf("update report: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrReportNotFound
	}
	return nil
}

func (s *ReportStore) ListReports(ctx context.Context) ([]app.Report, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, user_id, target, reason, status, created_at
FROM reports
ORDER BY created_at DESC, id DESC
`)
	if err != nil {
		return nil, fmt.Errorf("query reports: %w", err)
	}
	defer rows.Close()
	reports := []app.Report{}
	for rows.Next() {
		report, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read reports: %w", err)
	}
	return reports, nil
}

type JobStore struct {
	pool *pgxpool.Pool
}

func NewJobStore(pool *pgxpool.Pool) *JobStore {
	return &JobStore{pool: pool}
}

func (s *JobStore) CreateJob(ctx context.Context, job JobRecord) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO jobs (id, kind, target_id, status, attempts, last_error, run_after, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
`, job.ID, job.Kind, job.TargetID, job.Status, job.Attempts, job.LastError, job.RunAfter, job.CreatedAt, job.UpdatedAt); err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	return nil
}

func (s *JobStore) CreateQueueItem(ctx context.Context, item app.QueueItem) error {
	job := jobRecordFromQueueItem(item)
	if job.Status == "" {
		job.Status = "failed"
	}
	return s.CreateJob(ctx, job)
}

func (s *JobStore) JobByID(ctx context.Context, id string) (JobRecord, error) {
	job, err := scanJob(s.pool.QueryRow(ctx, `
SELECT id, kind, target_id, status, attempts, last_error, run_after, created_at, updated_at
FROM jobs
WHERE id = $1
`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return JobRecord{}, ErrJobNotFound
		}
		return JobRecord{}, err
	}
	return job, nil
}

func (s *JobStore) ListRunnableJobs(ctx context.Context, limit int, now time.Time) ([]JobRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, kind, target_id, status, attempts, last_error, run_after, created_at, updated_at
FROM jobs
WHERE status = 'pending' AND run_after <= $1
ORDER BY run_after ASC, created_at ASC, id ASC
LIMIT $2
`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query runnable jobs: %w", err)
	}
	defer rows.Close()
	jobs := []JobRecord{}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read jobs: %w", err)
	}
	return jobs, nil
}

func (s *JobStore) ListQueueItemsByKind(ctx context.Context, kind string) ([]app.QueueItem, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, kind, target_id, status, attempts, last_error, run_after, created_at, updated_at
FROM jobs
WHERE kind = $1
ORDER BY updated_at DESC, created_at DESC, id DESC
`, kind)
	if err != nil {
		return nil, fmt.Errorf("query queue items by kind: %w", err)
	}
	defer rows.Close()
	items := []app.QueueItem{}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, queueItemFromJobRecord(job))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read queue items: %w", err)
	}
	return items, nil
}

func (s *JobStore) ListQueueItemsByStatus(ctx context.Context, status string, limit int) ([]app.QueueItem, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, kind, target_id, status, attempts, last_error, run_after, created_at, updated_at
FROM jobs
WHERE status = $1
ORDER BY updated_at DESC, created_at DESC, id DESC
LIMIT $2
`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("query queue items by status: %w", err)
	}
	defer rows.Close()
	items := []app.QueueItem{}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, queueItemFromJobRecord(job))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read queue items by status: %w", err)
	}
	return items, nil
}

func (s *JobStore) DeleteQueueItemsByKindTarget(ctx context.Context, kind string, targetID string) error {
	if _, err := s.pool.Exec(ctx, `
DELETE FROM jobs
WHERE kind = $1 AND target_id = $2
`, kind, targetID); err != nil {
		return fmt.Errorf("delete queue items by kind target: %w", err)
	}
	return nil
}

func (s *JobStore) UpdateJob(ctx context.Context, job JobRecord) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE jobs
SET
	status = $2,
	attempts = $3,
	last_error = $4,
	run_after = $5,
	updated_at = $6
WHERE id = $1
`, job.ID, job.Status, job.Attempts, job.LastError, job.RunAfter, job.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrJobNotFound
	}
	return nil
}

type MailStore struct {
	pool *pgxpool.Pool
}

func NewMailStore(pool *pgxpool.Pool) *MailStore {
	return &MailStore{pool: pool}
}

func (s *MailStore) CreateMail(ctx context.Context, mail MailRecord) error {
	mail = mailRecordWithDefaults(mail)
	if _, err := s.pool.Exec(ctx, `
INSERT INTO mails (id, recipient, subject, body, status, attempts, last_error, run_after, created_at, sent_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
`, mail.ID, mail.To, mail.Subject, mail.Body, mail.Status, mail.Attempts, mail.LastError, mail.RunAfter, mail.CreatedAt, mail.SentAt); err != nil {
		return fmt.Errorf("create mail: %w", err)
	}
	return nil
}

func (s *MailStore) QueueMail(ctx context.Context, mail app.Mail) error {
	return s.CreateMail(ctx, MailRecord{
		ID:        mail.ID,
		To:        mail.To,
		Subject:   mail.Subject,
		Body:      mail.Body,
		Status:    "queued",
		CreatedAt: mail.CreatedAt,
	})
}

func (s *MailStore) MailByID(ctx context.Context, id string) (MailRecord, error) {
	mail, err := scanMail(s.pool.QueryRow(ctx, `
SELECT id, recipient, subject, body, status, attempts, last_error, run_after, created_at, sent_at
FROM mails
WHERE id = $1
`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MailRecord{}, ErrMailNotFound
		}
		return MailRecord{}, err
	}
	return mail, nil
}

func (s *MailStore) ListQueuedMail(ctx context.Context, limit int) ([]MailRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, recipient, subject, body, status, attempts, last_error, run_after, created_at, sent_at
FROM mails
WHERE status = 'queued'
ORDER BY created_at ASC, id ASC
LIMIT $1
`, limit)
	if err != nil {
		return nil, fmt.Errorf("query queued mail: %w", err)
	}
	defer rows.Close()
	mails := []MailRecord{}
	for rows.Next() {
		mail, err := scanMail(rows)
		if err != nil {
			return nil, err
		}
		mails = append(mails, mail)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read mails: %w", err)
	}
	return mails, nil
}

func (s *MailStore) MailQueueItems(ctx context.Context, status string, limit int) ([]app.MailQueueItem, error) {
	records, err := s.listMailByStatus(ctx, status, limit)
	if err != nil {
		return nil, err
	}
	items := make([]app.MailQueueItem, 0, len(records))
	for _, record := range records {
		items = append(items, mailQueueItemFromRecord(record))
	}
	return items, nil
}

func (s *MailStore) listMailByStatus(ctx context.Context, status string, limit int) ([]MailRecord, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "queued"
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, recipient, subject, body, status, attempts, last_error, run_after, created_at, sent_at
FROM mails
WHERE status = $2
ORDER BY run_after ASC, created_at ASC, id ASC
LIMIT $1
`, limit, status)
	if err != nil {
		return nil, fmt.Errorf("query mail queue items: %w", err)
	}
	defer rows.Close()
	mails := []MailRecord{}
	for rows.Next() {
		mail, err := scanMail(rows)
		if err != nil {
			return nil, err
		}
		mails = append(mails, mail)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read mail queue items: %w", err)
	}
	return mails, nil
}

func (s *MailStore) ListRunnableMail(ctx context.Context, limit int, now time.Time) ([]MailRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, recipient, subject, body, status, attempts, last_error, run_after, created_at, sent_at
FROM mails
WHERE status = 'queued' AND run_after <= $2
ORDER BY run_after ASC, created_at ASC, id ASC
LIMIT $1
`, limit, now)
	if err != nil {
		return nil, fmt.Errorf("query runnable mail: %w", err)
	}
	defer rows.Close()
	mails := []MailRecord{}
	for rows.Next() {
		mail, err := scanMail(rows)
		if err != nil {
			return nil, err
		}
		mails = append(mails, mail)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read runnable mail: %w", err)
	}
	return mails, nil
}

func (s *MailStore) QueuedMails(ctx context.Context, limit int) ([]app.Mail, error) {
	records, err := s.ListQueuedMail(ctx, limit)
	if err != nil {
		return nil, err
	}
	mails := make([]app.Mail, 0, len(records))
	for _, record := range records {
		mails = append(mails, mailFromRecord(record))
	}
	return mails, nil
}

func (s *MailStore) UpdateMail(ctx context.Context, mail MailRecord) error {
	mail = mailRecordWithDefaults(mail)
	tag, err := s.pool.Exec(ctx, `
UPDATE mails
SET
	status = $2,
	attempts = $3,
	last_error = $4,
	run_after = $5,
	sent_at = $6
WHERE id = $1
`, mail.ID, mail.Status, mail.Attempts, mail.LastError, mail.RunAfter, mail.SentAt)
	if err != nil {
		return fmt.Errorf("update mail: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrMailNotFound
	}
	return nil
}

type WorkerHeartbeatStore struct {
	pool *pgxpool.Pool
}

func NewWorkerHeartbeatStore(pool *pgxpool.Pool) *WorkerHeartbeatStore {
	return &WorkerHeartbeatStore{pool: pool}
}

func (s *WorkerHeartbeatStore) RecordWorkerHeartbeat(ctx context.Context, workerID string, seenAt time.Time) error {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return fmt.Errorf("worker heartbeat id is required")
	}
	if seenAt.IsZero() {
		seenAt = time.Now().UTC()
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO worker_heartbeats (worker_id, last_seen_at, updated_at)
VALUES ($1, $2, $2)
ON CONFLICT (worker_id) DO UPDATE SET
	last_seen_at = EXCLUDED.last_seen_at,
	updated_at = EXCLUDED.updated_at
`, workerID, seenAt.UTC()); err != nil {
		return fmt.Errorf("record worker heartbeat: %w", err)
	}
	return nil
}

func (s *WorkerHeartbeatStore) LastWorkerHeartbeat(ctx context.Context) (WorkerHeartbeat, error) {
	var heartbeat WorkerHeartbeat
	if err := s.pool.QueryRow(ctx, `
SELECT worker_id, last_seen_at, updated_at
FROM worker_heartbeats
ORDER BY last_seen_at DESC, worker_id ASC
LIMIT 1
`).Scan(&heartbeat.WorkerID, &heartbeat.LastSeenAt, &heartbeat.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkerHeartbeat{}, ErrWorkerHeartbeatNotFound
		}
		return WorkerHeartbeat{}, fmt.Errorf("read worker heartbeat: %w", err)
	}
	return heartbeat, nil
}

func scanOrders(rows rowsScanner) ([]app.Order, error) {
	orders := []app.Order{}
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read orders: %w", err)
	}
	return orders, nil
}

func scanOrder(row rowScanner) (app.Order, error) {
	var order app.Order
	var expiresAt pgtype.Timestamptz
	var paidAt pgtype.Timestamptz
	if err := row.Scan(
		&order.ID,
		&order.UserID,
		&order.Provider,
		&order.PlanID,
		&order.Period,
		&order.AmountCents,
		&order.Currency,
		&order.Status,
		&order.CheckoutURL,
		&order.Address,
		&order.Chain,
		&order.TxID,
		&order.CreatedAt,
		&expiresAt,
		&paidAt,
	); err != nil {
		return app.Order{}, fmt.Errorf("scan order: %w", err)
	}
	order.ExpiresAt = optionalTime(expiresAt)
	order.PaidAt = optionalTime(paidAt)
	return order, nil
}

func scanWebhookEvent(row rowScanner) (app.WebhookEvent, error) {
	var event app.WebhookEvent
	var metadataBytes []byte
	if err := row.Scan(
		&event.ID,
		&event.Provider,
		&event.EventType,
		&event.TargetID,
		&event.IdempotencyKey,
		&event.Processed,
		&metadataBytes,
		&event.ReceivedAt,
	); err != nil {
		return app.WebhookEvent{}, fmt.Errorf("scan webhook event: %w", err)
	}
	metadata := map[string]any{}
	if len(metadataBytes) > 0 {
		if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
			return app.WebhookEvent{}, fmt.Errorf("decode webhook metadata: %w", err)
		}
	}
	event.Metadata = metadata
	return event, nil
}

func scanReport(row rowScanner) (app.Report, error) {
	var report app.Report
	var userID pgtype.Text
	if err := row.Scan(&report.ID, &userID, &report.Target, &report.Reason, &report.Status, &report.CreatedAt); err != nil {
		return app.Report{}, fmt.Errorf("scan report: %w", err)
	}
	if userID.Valid {
		report.UserID = userID.String
	}
	return report, nil
}

func scanJob(row rowScanner) (JobRecord, error) {
	var job JobRecord
	if err := row.Scan(&job.ID, &job.Kind, &job.TargetID, &job.Status, &job.Attempts, &job.LastError, &job.RunAfter, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return JobRecord{}, fmt.Errorf("scan job: %w", err)
	}
	return job, nil
}

func scanMail(row rowScanner) (MailRecord, error) {
	var mail MailRecord
	var sentAt pgtype.Timestamptz
	if err := row.Scan(&mail.ID, &mail.To, &mail.Subject, &mail.Body, &mail.Status, &mail.Attempts, &mail.LastError, &mail.RunAfter, &mail.CreatedAt, &sentAt); err != nil {
		return MailRecord{}, fmt.Errorf("scan mail: %w", err)
	}
	mail.SentAt = optionalTime(sentAt)
	return mail, nil
}

func mailRecordWithDefaults(mail MailRecord) MailRecord {
	if mail.Status == "" {
		mail.Status = "queued"
	}
	if mail.CreatedAt.IsZero() {
		mail.CreatedAt = time.Now().UTC()
	}
	if mail.RunAfter.IsZero() {
		mail.RunAfter = mail.CreatedAt
	}
	return mail
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func jobRecordFromQueueItem(item app.QueueItem) JobRecord {
	runAfter := item.UpdatedAt
	if runAfter.IsZero() {
		runAfter = item.CreatedAt
	}
	return JobRecord{
		ID:        item.ID,
		Kind:      item.Kind,
		TargetID:  item.TargetID,
		Status:    item.Status,
		Attempts:  item.Attempts,
		LastError: item.Error,
		RunAfter:  runAfter,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
}

func queueItemFromJobRecord(job JobRecord) app.QueueItem {
	return app.QueueItem{
		ID:        job.ID,
		Kind:      job.Kind,
		TargetID:  job.TargetID,
		Status:    job.Status,
		Error:     job.LastError,
		Attempts:  job.Attempts,
		RunAfter:  job.RunAfter,
		CreatedAt: job.CreatedAt,
		UpdatedAt: job.UpdatedAt,
	}
}

func mailFromRecord(record MailRecord) app.Mail {
	return app.Mail{
		ID:        record.ID,
		To:        record.To,
		Subject:   record.Subject,
		Body:      record.Body,
		CreatedAt: record.CreatedAt,
	}
}

func mailQueueItemFromRecord(record MailRecord) app.MailQueueItem {
	return app.MailQueueItem{
		ID:        record.ID,
		To:        record.To,
		Subject:   record.Subject,
		Status:    record.Status,
		Attempts:  record.Attempts,
		LastError: record.LastError,
		RunAfter:  record.RunAfter,
		CreatedAt: record.CreatedAt,
		SentAt:    record.SentAt,
	}
}
