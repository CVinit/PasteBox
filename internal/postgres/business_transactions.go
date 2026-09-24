package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

type BusinessTransactionStore struct {
	pool *pgxpool.Pool
}

func NewBusinessTransactionStore(pool *pgxpool.Pool) *BusinessTransactionStore {
	return &BusinessTransactionStore{pool: pool}
}

func (s *BusinessTransactionStore) RegisterUser(ctx context.Context, user app.User, mail app.Mail) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin user registration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertUserRecord(ctx, tx, user); err != nil {
		return err
	}
	if err := insertMailRecord(ctx, tx, MailRecord{
		ID: mail.ID, To: mail.To, Subject: mail.Subject, Body: mail.Body,
		Status: "queued", CreatedAt: mail.CreatedAt,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit user registration transaction: %w", err)
	}
	return nil
}

func (s *BusinessTransactionStore) RegisterUserWithEmailVerification(ctx context.Context, user app.User, mail app.Mail, tokenHash string, email string, usedAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin verified user registration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tokenEmail string
	if err := tx.QueryRow(ctx, `
UPDATE auth_tokens
SET used_at = $2
WHERE kind = 'registration_email_verification'
  AND hash = $1
  AND email = $3
  AND used_at IS NULL
  AND expires_at > $2
RETURNING email
`, tokenHash, usedAt, email).Scan(&tokenEmail); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
		}
		return fmt.Errorf("consume registration email token: %w", err)
	}
	if err := insertUserRecord(ctx, tx, user); err != nil {
		return err
	}
	if err := insertMailRecord(ctx, tx, MailRecord{
		ID: mail.ID, To: mail.To, Subject: mail.Subject, Body: mail.Body,
		Status: "queued", CreatedAt: mail.CreatedAt,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit verified user registration transaction: %w", err)
	}
	return nil
}

func (s *BusinessTransactionStore) RegisterOAuthUser(ctx context.Context, input app.OAuthRegistrationTransactionInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin oauth registration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertUserRecord(ctx, tx, input.User); err != nil {
		return err
	}
	if err := insertOAuthIdentityRecord(ctx, tx, input.Identity); err != nil {
		return err
	}
	for _, audit := range input.Audits {
		if err := insertAuditLog(ctx, tx, audit); err != nil {
			return err
		}
	}
	if err := insertMailRecord(ctx, tx, MailRecord{
		ID: input.Mail.ID, To: input.Mail.To, Subject: input.Mail.Subject, Body: input.Mail.Body,
		Status: "queued", CreatedAt: input.Mail.CreatedAt,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit oauth registration transaction: %w", err)
	}
	return nil
}

func (s *BusinessTransactionStore) FinishPasswordReset(ctx context.Context, input app.PasswordResetTransactionInput) (app.PasswordResetTransactionResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return app.PasswordResetTransactionResult{}, fmt.Errorf("begin password reset transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var token app.AuthToken
	var userID pgtype.Text
	var usedAt pgtype.Timestamptz
	err = tx.QueryRow(ctx, `
UPDATE auth_tokens
SET used_at = $2
WHERE kind = 'password_reset'
  AND hash = $1
  AND used_at IS NULL
  AND expires_at > $2
RETURNING hash, user_id, email, expires_at, used_at
`, input.TokenHash, input.UsedAt).Scan(&token.Hash, &userID, &token.Email, &token.ExpiresAt, &usedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.PasswordResetTransactionResult{}, app.ErrStoreNotFound
		}
		return app.PasswordResetTransactionResult{}, fmt.Errorf("consume password reset token: %w", err)
	}
	token.UserID = userID.String
	token.UsedAt = optionalTime(usedAt)
	if token.UserID == "" {
		return app.PasswordResetTransactionResult{}, fmt.Errorf("password reset token has no user")
	}

	user, err := scanUser(tx.QueryRow(ctx, `
SELECT
	id, email, display_name, language, password_hash, role, email_verified,
	plan_id, plan_expires_at, frozen, created_at, updated_at,
	delete_requested_at, delete_scheduled_at, deleted_at
FROM users
WHERE id = $1
FOR UPDATE
`, token.UserID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.PasswordResetTransactionResult{}, app.ErrStoreNotFound
		}
		return app.PasswordResetTransactionResult{}, fmt.Errorf("load password reset user: %w", err)
	}
	if user.DeletedAt != nil || user.Frozen {
		return app.PasswordResetTransactionResult{}, app.E(http.StatusForbidden, "account_unavailable", "account is unavailable")
	}
	user.PasswordHash = input.PasswordHash
	user.UpdatedAt = input.UsedAt
	if err := updateUserRecord(ctx, tx, user); err != nil {
		return app.PasswordResetTransactionResult{}, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE sessions
SET revoked_at = $2
WHERE user_id = $1 AND revoked_at IS NULL
`, user.ID, input.UsedAt); err != nil {
		return app.PasswordResetTransactionResult{}, fmt.Errorf("revoke password reset sessions: %w", err)
	}
	mail := app.Mail{
		ID:        input.MailID,
		To:        user.Email,
		Subject:   "PasteBox password changed",
		Body:      "Your password was changed.",
		CreatedAt: input.MailCreatedAt,
	}
	if err := insertMailRecord(ctx, tx, MailRecord{
		ID:        mail.ID,
		To:        mail.To,
		Subject:   mail.Subject,
		Body:      mail.Body,
		Status:    "queued",
		CreatedAt: mail.CreatedAt,
	}); err != nil {
		return app.PasswordResetTransactionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return app.PasswordResetTransactionResult{}, fmt.Errorf("commit password reset transaction: %w", err)
	}
	return app.PasswordResetTransactionResult{User: user, Mail: mail}, nil
}

func (s *BusinessTransactionStore) CreatePasteWithDailyMetric(ctx context.Context, paste app.Paste, day time.Time, bytes int64) error {
	return s.withPasteDailyMetricTransaction(ctx, paste, day, bytes, false)
}

func (s *BusinessTransactionStore) UpdatePasteWithDailyMetric(ctx context.Context, paste app.Paste, day time.Time, bytes int64) error {
	return s.withPasteDailyMetricTransaction(ctx, paste, day, bytes, true)
}

func (s *BusinessTransactionStore) withPasteDailyMetricTransaction(ctx context.Context, paste app.Paste, day time.Time, bytes int64, update bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin paste quota transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if update {
		if err := updatePasteRecord(ctx, tx, paste); err != nil {
			return err
		}
	} else if err := createPasteRecord(ctx, tx, paste); err != nil {
		return err
	}
	if err := recordDailyMetric(ctx, tx, paste.UserID, "upload", day, bytes); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit paste quota transaction: %w", err)
	}
	return nil
}

func (s *BusinessTransactionStore) RedeemCode(ctx context.Context, input app.RedemptionTransactionInput) (app.RedemptionTransactionResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return app.RedemptionTransactionResult{}, fmt.Errorf("begin redemption transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	code, err := scanRedemptionCode(tx.QueryRow(ctx, `
SELECT code_hash, batch_id, redeemed_by, redeemed_at, created_at
FROM redemption_codes
WHERE code_hash = $1
FOR UPDATE
`, strings.TrimSpace(input.CodeHash)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.RedemptionTransactionResult{}, app.E(http.StatusNotFound, "redemption_code_invalid", "redemption code is invalid")
		}
		return app.RedemptionTransactionResult{}, err
	}
	batch, err := scanRedemptionBatch(tx.QueryRow(ctx, `
SELECT
	id, plan_id, duration_days, quantity, expires_at, max_total_redemptions,
	max_redemptions_per_user, allowed_emails, allowed_domains, note, disabled,
	redeemed_count, created_at, updated_at
FROM redemption_batches
WHERE id = $1
FOR UPDATE
`, code.BatchID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.RedemptionTransactionResult{}, app.E(http.StatusNotFound, "redemption_code_invalid", "redemption code is invalid")
		}
		return app.RedemptionTransactionResult{}, err
	}
	user, err := scanUser(tx.QueryRow(ctx, `
SELECT
	id, email, display_name, language, password_hash, role, email_verified,
	plan_id, plan_expires_at, frozen, created_at, updated_at,
	delete_requested_at, delete_scheduled_at, deleted_at
FROM users
WHERE id = $1
FOR UPDATE
`, input.UserID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.RedemptionTransactionResult{}, app.E(http.StatusUnauthorized, "user_not_found", "user not found")
		}
		return app.RedemptionTransactionResult{}, err
	}
	var userRedemptions int
	if err := tx.QueryRow(ctx, `
SELECT count(*)
FROM redemption_records
WHERE batch_id = $1 AND user_id = $2
`, batch.ID, user.ID).Scan(&userRedemptions); err != nil {
		return app.RedemptionTransactionResult{}, fmt.Errorf("count user redemptions: %w", err)
	}
	if input.RedeemedAt.IsZero() {
		input.RedeemedAt = time.Now().UTC()
	}
	result, err := app.BuildRedemptionTransaction(input, user, batch, code, userRedemptions)
	if err != nil {
		return app.RedemptionTransactionResult{}, err
	}
	if err := updateRedemptionCodeRecord(ctx, tx, result.Code, true); err != nil {
		return app.RedemptionTransactionResult{}, err
	}
	if err := updateRedemptionBatchRecord(ctx, tx, result.Batch); err != nil {
		return app.RedemptionTransactionResult{}, err
	}
	if err := insertRedemptionRecord(ctx, tx, result.Record); err != nil {
		return app.RedemptionTransactionResult{}, err
	}
	if err := updateUserRecord(ctx, tx, result.User); err != nil {
		return app.RedemptionTransactionResult{}, err
	}
	if err := insertAuditLog(ctx, tx, result.Audit); err != nil {
		return app.RedemptionTransactionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return app.RedemptionTransactionResult{}, fmt.Errorf("commit redemption transaction: %w", err)
	}
	return result, nil
}

func (s *BusinessTransactionStore) ApplyBilling(ctx context.Context, input app.BillingTransactionInput) (app.BillingTransactionResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return app.BillingTransactionResult{}, fmt.Errorf("begin billing transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if key := strings.TrimSpace(input.IdempotencyKey); key != "" {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
			return app.BillingTransactionResult{}, fmt.Errorf("lock billing idempotency key: %w", err)
		}
		existing, err := scanWebhookEvent(tx.QueryRow(ctx, `
SELECT id, provider, event_type, target_id, idempotency_key, processed, metadata, received_at
FROM webhook_events
WHERE idempotency_key = $1
`, key))
		if err == nil {
			result := app.BillingTransactionResult{Event: &existing, ExistingEvent: true}
			if existing.TargetID != "" {
				order, orderErr := scanOrder(tx.QueryRow(ctx, `
SELECT id, user_id, provider, plan_id, period, amount_cents, currency, status, checkout_url, address, chain, tx_id, created_at, expires_at, paid_at
FROM orders
WHERE id = $1
`, existing.TargetID))
				if orderErr != nil && !errors.Is(orderErr, pgx.ErrNoRows) {
					return app.BillingTransactionResult{}, orderErr
				}
				if orderErr == nil {
					result.Order = &order
				}
			}
			if err := tx.Commit(ctx); err != nil {
				return app.BillingTransactionResult{}, fmt.Errorf("commit duplicate billing transaction: %w", err)
			}
			return result, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return app.BillingTransactionResult{}, err
		}
	}

	var order *app.Order
	if orderID := strings.TrimSpace(input.OrderID); orderID != "" {
		loaded, err := scanOrder(tx.QueryRow(ctx, `
SELECT id, user_id, provider, plan_id, period, amount_cents, currency, status, checkout_url, address, chain, tx_id, created_at, expires_at, paid_at
FROM orders
WHERE id = $1
FOR UPDATE
`, orderID))
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return app.BillingTransactionResult{}, err
			}
		} else {
			order = &loaded
		}
	}

	var user *app.User
	desiredStatus := strings.ToLower(strings.TrimSpace(input.DesiredStatus))
	needsUser := order != nil && ((desiredStatus == "paid" && order.Status != "paid") || (input.RevokePlan && order.Status == "paid"))
	if needsUser {
		loaded, err := scanUser(tx.QueryRow(ctx, `
SELECT
	id, email, display_name, language, password_hash, role, email_verified,
	plan_id, plan_expires_at, frozen, created_at, updated_at,
	delete_requested_at, delete_scheduled_at, deleted_at
FROM users
WHERE id = $1
FOR UPDATE
`, order.UserID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return app.BillingTransactionResult{}, app.E(http.StatusNotFound, "user_not_found", "user not found")
			}
			return app.BillingTransactionResult{}, err
		}
		user = &loaded
	}
	if input.OccurredAt.IsZero() {
		input.OccurredAt = time.Now().UTC()
	}
	result, err := app.BuildBillingTransaction(input, order, user)
	if err != nil {
		return app.BillingTransactionResult{}, err
	}
	if result.User != nil {
		if err := updateUserRecord(ctx, tx, *result.User); err != nil {
			return app.BillingTransactionResult{}, err
		}
	}
	if result.Audit != nil && result.Order != nil {
		if err := updateOrderRecord(ctx, tx, *result.Order); err != nil {
			return app.BillingTransactionResult{}, err
		}
	}
	if result.Audit != nil {
		if err := insertAuditLog(ctx, tx, *result.Audit); err != nil {
			return app.BillingTransactionResult{}, err
		}
	}
	if result.Event != nil {
		if err := insertWebhookEvent(ctx, tx, *result.Event); err != nil {
			return app.BillingTransactionResult{}, err
		}
	}
	if result.Mail != nil {
		if err := insertMailRecord(ctx, tx, MailRecord{
			ID: result.Mail.ID, To: result.Mail.To, Subject: result.Mail.Subject, Body: result.Mail.Body,
			Status: "queued", CreatedAt: result.Mail.CreatedAt,
		}); err != nil {
			return app.BillingTransactionResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return app.BillingTransactionResult{}, fmt.Errorf("commit billing transaction: %w", err)
	}
	return result, nil
}

func (s *BusinessTransactionStore) SaveOAuthAccount(ctx context.Context, user app.User, identity *app.OAuthIdentity, audits []app.AuditLog) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE users SET display_name = $2, email_verified = $3, updated_at = $4 WHERE id = $1 AND deleted_at IS NULL AND frozen = false`, user.ID, user.DisplayName, user.EmailVerified, user.UpdatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return app.E(http.StatusForbidden, "account_unavailable", "account is unavailable")
	}
	if identity != nil {
		if err := insertOAuthIdentityRecord(ctx, tx, *identity); err != nil {
			return err
		}
	}
	for _, audit := range audits {
		if err := insertAuditLog(ctx, tx, audit); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
