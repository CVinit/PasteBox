package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

type RuntimeConfigStore struct {
	pool *pgxpool.Pool
}

func NewRuntimeConfigStore(pool *pgxpool.Pool) *RuntimeConfigStore {
	return &RuntimeConfigStore{pool: pool}
}

func (s *RuntimeConfigStore) RuntimeConfig(ctx context.Context) (app.RuntimeConfig, bool, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `
SELECT config
FROM system_configs
WHERE id = $1
`, "default").Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.RuntimeConfig{}, false, nil
		}
		return app.RuntimeConfig{}, false, fmt.Errorf("read runtime config: %w", err)
	}
	var cfg app.RuntimeConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return app.RuntimeConfig{}, false, fmt.Errorf("decode runtime config: %w", err)
	}
	return cfg, true, nil
}

func (s *RuntimeConfigStore) SaveRuntimeConfig(ctx context.Context, cfg app.RuntimeConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode runtime config: %w", err)
	}
	updatedAt := cfg.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO system_configs (id, config, created_at, updated_at)
VALUES ($1, $2, $3, $3)
ON CONFLICT (id) DO UPDATE SET
	config = EXCLUDED.config,
	updated_at = EXCLUDED.updated_at
`, "default", string(raw), updatedAt); err != nil {
		return fmt.Errorf("save runtime config: %w", err)
	}
	return nil
}

type RedemptionStore struct {
	pool *pgxpool.Pool
}

func NewRedemptionStore(pool *pgxpool.Pool) *RedemptionStore {
	return &RedemptionStore{pool: pool}
}

func (s *RedemptionStore) CreateRedemptionBatch(ctx context.Context, batch app.RedemptionBatch, codes []app.RedemptionCode) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin redemption batch create: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if err := insertRedemptionBatch(ctx, tx, batch); err != nil {
		return err
	}
	for _, code := range codes {
		if _, err := tx.Exec(ctx, `
INSERT INTO redemption_codes (code_hash, batch_id, redeemed_by, redeemed_at, created_at)
VALUES ($1, $2, $3, $4, $5)
`, code.CodeHash, code.BatchID, nullableString(code.RedeemedBy), code.RedeemedAt, code.CreatedAt); err != nil {
			return fmt.Errorf("insert redemption code: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit redemption batch create: %w", err)
	}
	return nil
}

func (s *RedemptionStore) UpdateRedemptionBatch(ctx context.Context, batch app.RedemptionBatch) error {
	allowedEmails, err := json.Marshal(nonNilStrings(batch.AllowedEmails))
	if err != nil {
		return fmt.Errorf("encode allowed emails: %w", err)
	}
	allowedDomains, err := json.Marshal(nonNilStrings(batch.AllowedDomains))
	if err != nil {
		return fmt.Errorf("encode allowed domains: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE redemption_batches
SET
	plan_id = $2,
	duration_days = $3,
	quantity = $4,
	expires_at = $5,
	max_total_redemptions = $6,
	max_redemptions_per_user = $7,
	allowed_emails = $8,
	allowed_domains = $9,
	note = $10,
	disabled = $11,
	redeemed_count = $12,
	created_at = $13,
	updated_at = $14
WHERE id = $1
`, batch.ID, batch.PlanID, batch.DurationDays, batch.Quantity, batch.ExpiresAt, batch.MaxTotalRedemptions, batch.MaxRedemptionsPerUser, string(allowedEmails), string(allowedDomains), batch.Note, batch.Disabled, batch.RedeemedCount, batch.CreatedAt, batch.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update redemption batch: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return app.ErrStoreNotFound
	}
	return nil
}

func (s *RedemptionStore) UpdateRedemptionCode(ctx context.Context, code app.RedemptionCode) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE redemption_codes
SET
	batch_id = $2,
	redeemed_by = $3,
	redeemed_at = $4,
	created_at = $5
WHERE code_hash = $1
`, code.CodeHash, code.BatchID, nullableString(code.RedeemedBy), code.RedeemedAt, code.CreatedAt)
	if err != nil {
		return fmt.Errorf("update redemption code: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return app.ErrStoreNotFound
	}
	return nil
}

func (s *RedemptionStore) CreateRedemptionRecord(ctx context.Context, record app.RedemptionRecord) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO redemption_records (id, code_hash, batch_id, user_id, plan_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
`, record.ID, record.CodeHash, record.BatchID, record.UserID, record.PlanID, record.CreatedAt); err != nil {
		return fmt.Errorf("create redemption record: %w", err)
	}
	return nil
}

func (s *RedemptionStore) ListRedemptionBatches(ctx context.Context) ([]app.RedemptionBatch, error) {
	rows, err := s.pool.Query(ctx, `
SELECT
	id,
	plan_id,
	duration_days,
	quantity,
	expires_at,
	max_total_redemptions,
	max_redemptions_per_user,
	allowed_emails,
	allowed_domains,
	note,
	disabled,
	redeemed_count,
	created_at,
	updated_at
FROM redemption_batches
ORDER BY created_at DESC, id DESC
`)
	if err != nil {
		return nil, fmt.Errorf("query redemption batches: %w", err)
	}
	defer rows.Close()

	batches := []app.RedemptionBatch{}
	for rows.Next() {
		batch, err := scanRedemptionBatch(rows)
		if err != nil {
			return nil, err
		}
		batches = append(batches, batch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read redemption batches: %w", err)
	}
	return batches, nil
}

func (s *RedemptionStore) ListRedemptionCodes(ctx context.Context) ([]app.RedemptionCode, error) {
	rows, err := s.pool.Query(ctx, `
SELECT code_hash, batch_id, redeemed_by, redeemed_at, created_at
FROM redemption_codes
ORDER BY created_at ASC, code_hash ASC
`)
	if err != nil {
		return nil, fmt.Errorf("query redemption codes: %w", err)
	}
	defer rows.Close()

	codes := []app.RedemptionCode{}
	for rows.Next() {
		code, err := scanRedemptionCode(rows)
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read redemption codes: %w", err)
	}
	return codes, nil
}

func (s *RedemptionStore) ListRedemptionRecords(ctx context.Context) ([]app.RedemptionRecord, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, code_hash, batch_id, user_id, plan_id, created_at
FROM redemption_records
ORDER BY created_at DESC, id DESC
`)
	if err != nil {
		return nil, fmt.Errorf("query redemption records: %w", err)
	}
	defer rows.Close()

	records := []app.RedemptionRecord{}
	for rows.Next() {
		var record app.RedemptionRecord
		if err := rows.Scan(&record.ID, &record.CodeHash, &record.BatchID, &record.UserID, &record.PlanID, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan redemption record: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read redemption records: %w", err)
	}
	return records, nil
}

type AlertEventStore struct {
	pool *pgxpool.Pool
}

func NewAlertEventStore(pool *pgxpool.Pool) *AlertEventStore {
	return &AlertEventStore{pool: pool}
}

func (s *AlertEventStore) CreateAlertEvent(ctx context.Context, event app.AlertEvent) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO alert_events (id, fingerprint, level, message, status, last_error, sent_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
`, event.ID, event.Fingerprint, event.Level, event.Message, event.Status, event.LastError, event.SentAt, event.CreatedAt, event.UpdatedAt); err != nil {
		return fmt.Errorf("create alert event: %w", err)
	}
	return nil
}

func (s *AlertEventStore) UpdateAlertEvent(ctx context.Context, event app.AlertEvent) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE alert_events
SET
	fingerprint = $2,
	level = $3,
	message = $4,
	status = $5,
	last_error = $6,
	sent_at = $7,
	created_at = $8,
	updated_at = $9
WHERE id = $1
`, event.ID, event.Fingerprint, event.Level, event.Message, event.Status, event.LastError, event.SentAt, event.CreatedAt, event.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update alert event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return app.ErrStoreNotFound
	}
	return nil
}

func (s *AlertEventStore) ListAlertEvents(ctx context.Context, limit int) ([]app.AlertEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, fingerprint, level, message, status, last_error, sent_at, created_at, updated_at
FROM alert_events
ORDER BY updated_at DESC, id DESC
LIMIT $1
`, limit)
	if err != nil {
		return nil, fmt.Errorf("query alert events: %w", err)
	}
	defer rows.Close()

	events := []app.AlertEvent{}
	for rows.Next() {
		event, err := scanAlertEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read alert events: %w", err)
	}
	return events, nil
}

type execQuerier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func insertRedemptionBatch(ctx context.Context, tx execQuerier, batch app.RedemptionBatch) error {
	allowedEmails, err := json.Marshal(nonNilStrings(batch.AllowedEmails))
	if err != nil {
		return fmt.Errorf("encode allowed emails: %w", err)
	}
	allowedDomains, err := json.Marshal(nonNilStrings(batch.AllowedDomains))
	if err != nil {
		return fmt.Errorf("encode allowed domains: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO redemption_batches (
	id,
	plan_id,
	duration_days,
	quantity,
	expires_at,
	max_total_redemptions,
	max_redemptions_per_user,
	allowed_emails,
	allowed_domains,
	note,
	disabled,
	redeemed_count,
	created_at,
	updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
`, batch.ID, batch.PlanID, batch.DurationDays, batch.Quantity, batch.ExpiresAt, batch.MaxTotalRedemptions, batch.MaxRedemptionsPerUser, string(allowedEmails), string(allowedDomains), batch.Note, batch.Disabled, batch.RedeemedCount, batch.CreatedAt, batch.UpdatedAt); err != nil {
		return fmt.Errorf("insert redemption batch: %w", err)
	}
	return nil
}

type runtimeRow interface {
	Scan(dest ...any) error
}

func scanRedemptionBatch(row runtimeRow) (app.RedemptionBatch, error) {
	var batch app.RedemptionBatch
	var expiresAt pgtype.Timestamptz
	var allowedEmails []byte
	var allowedDomains []byte
	if err := row.Scan(
		&batch.ID,
		&batch.PlanID,
		&batch.DurationDays,
		&batch.Quantity,
		&expiresAt,
		&batch.MaxTotalRedemptions,
		&batch.MaxRedemptionsPerUser,
		&allowedEmails,
		&allowedDomains,
		&batch.Note,
		&batch.Disabled,
		&batch.RedeemedCount,
		&batch.CreatedAt,
		&batch.UpdatedAt,
	); err != nil {
		return app.RedemptionBatch{}, fmt.Errorf("scan redemption batch: %w", err)
	}
	batch.ExpiresAt = optionalTime(expiresAt)
	batch.AllowedEmails = []string{}
	if len(allowedEmails) > 0 {
		if err := json.Unmarshal(allowedEmails, &batch.AllowedEmails); err != nil {
			return app.RedemptionBatch{}, fmt.Errorf("decode allowed emails: %w", err)
		}
	}
	batch.AllowedDomains = []string{}
	if len(allowedDomains) > 0 {
		if err := json.Unmarshal(allowedDomains, &batch.AllowedDomains); err != nil {
			return app.RedemptionBatch{}, fmt.Errorf("decode allowed domains: %w", err)
		}
	}
	return batch, nil
}

func scanRedemptionCode(row runtimeRow) (app.RedemptionCode, error) {
	var code app.RedemptionCode
	var redeemedBy pgtype.Text
	var redeemedAt pgtype.Timestamptz
	if err := row.Scan(&code.CodeHash, &code.BatchID, &redeemedBy, &redeemedAt, &code.CreatedAt); err != nil {
		return app.RedemptionCode{}, fmt.Errorf("scan redemption code: %w", err)
	}
	if redeemedBy.Valid {
		code.RedeemedBy = redeemedBy.String
	}
	code.RedeemedAt = optionalTime(redeemedAt)
	return code, nil
}

func scanAlertEvent(row runtimeRow) (app.AlertEvent, error) {
	var event app.AlertEvent
	var sentAt pgtype.Timestamptz
	if err := row.Scan(&event.ID, &event.Fingerprint, &event.Level, &event.Message, &event.Status, &event.LastError, &sentAt, &event.CreatedAt, &event.UpdatedAt); err != nil {
		return app.AlertEvent{}, fmt.Errorf("scan alert event: %w", err)
	}
	event.SentAt = optionalTime(sentAt)
	return event, nil
}
