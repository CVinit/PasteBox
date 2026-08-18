package app

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// BusinessTransactionStore owns cross-repository mutations that must commit as
// one database transaction in production.
type BusinessTransactionStore interface {
	RedeemCode(ctx context.Context, input RedemptionTransactionInput) (RedemptionTransactionResult, error)
	ApplyBilling(ctx context.Context, input BillingTransactionInput) (BillingTransactionResult, error)
}

type PasteDailyMetricTransactionStore interface {
	CreatePasteWithDailyMetric(ctx context.Context, paste Paste, day time.Time, bytes int64) error
	UpdatePasteWithDailyMetric(ctx context.Context, paste Paste, day time.Time, bytes int64) error
}

type RedemptionTransactionInput struct {
	UserID     string
	CodeHash   string
	RecordID   string
	AuditID    string
	RedeemedAt time.Time
}

type RedemptionTransactionResult struct {
	User   User
	Batch  RedemptionBatch
	Code   RedemptionCode
	Record RedemptionRecord
	Audit  AuditLog
}

type BillingTransactionInput struct {
	ActorID        string
	OrderID        string
	TxID           string
	DesiredStatus  string
	RevokePlan     bool
	EventID        string
	EventProvider  string
	EventType      string
	IdempotencyKey string
	Metadata       map[string]any
	AuditID        string
	MailID         string
	OccurredAt     time.Time
}

type BillingTransactionResult struct {
	User          *User
	Order         *Order
	Event         *WebhookEvent
	Audit         *AuditLog
	Mail          *Mail
	ExistingEvent bool
}

func BuildRedemptionTransaction(
	input RedemptionTransactionInput,
	user User,
	batch RedemptionBatch,
	code RedemptionCode,
	userRedemptions int,
) (RedemptionTransactionResult, error) {
	now := input.RedeemedAt.UTC()
	if err := ValidateRedemption(user, batch, code, userRedemptions, now); err != nil {
		return RedemptionTransactionResult{}, err
	}

	code = cloneRedemptionCode(code, true)
	batch = cloneRedemptionBatch(batch)
	code.RedeemedBy = user.ID
	code.RedeemedAt = &now
	batch.RedeemedCount++
	batch.UpdatedAt = now
	record := RedemptionRecord{
		ID:        input.RecordID,
		CodeHash:  code.CodeHash,
		BatchID:   batch.ID,
		UserID:    user.ID,
		PlanID:    batch.PlanID,
		CreatedAt: now,
	}
	user.PlanID = batch.PlanID
	expires := now.Add(time.Duration(batch.DurationDays) * 24 * time.Hour)
	if user.PlanExpiresAt != nil && user.PlanExpiresAt.After(now) {
		expires = user.PlanExpiresAt.Add(time.Duration(batch.DurationDays) * 24 * time.Hour)
	}
	user.PlanExpiresAt = &expires
	user.UpdatedAt = now
	audit := AuditLog{
		ID:        input.AuditID,
		ActorID:   user.ID,
		Action:    "billing.redemption_redeemed",
		Target:    batch.ID,
		Metadata:  map[string]any{"planId": batch.PlanID, "durationDays": batch.DurationDays},
		CreatedAt: now,
	}
	return RedemptionTransactionResult{User: user, Batch: batch, Code: code, Record: record, Audit: audit}, nil
}

// ValidateRedemption is shared by the in-memory path and the PostgreSQL
// transaction after it has locked the code, batch, and user rows.
func ValidateRedemption(user User, batch RedemptionBatch, code RedemptionCode, userRedemptions int, now time.Time) error {
	if user.DeletedAt != nil {
		return E(http.StatusUnauthorized, "user_not_found", "user not found")
	}
	if user.Frozen {
		return E(http.StatusForbidden, "account_frozen", "account is frozen")
	}
	if batch.Disabled {
		return E(http.StatusForbidden, "redemption_batch_disabled", "redemption batch is disabled")
	}
	if batch.ExpiresAt != nil && !batch.ExpiresAt.After(now) {
		return E(http.StatusGone, "redemption_batch_expired", "redemption batch is expired")
	}
	if code.RedeemedAt != nil || code.RedeemedBy != "" {
		return E(http.StatusConflict, "redemption_code_used", "redemption code has already been used")
	}
	if batch.RedeemedCount >= batch.MaxTotalRedemptions {
		return E(http.StatusConflict, "redemption_batch_limit", "redemption batch redemption limit reached")
	}
	if len(batch.AllowedEmails) > 0 && !contains(batch.AllowedEmails, normalizeEmail(user.Email)) {
		return E(http.StatusForbidden, "redemption_email_not_allowed", "redemption code is not valid for this email")
	}
	if len(batch.AllowedDomains) > 0 && !contains(batch.AllowedDomains, emailDomain(user.Email)) {
		return E(http.StatusForbidden, "redemption_domain_not_allowed", "redemption code is not valid for this email domain")
	}
	if userRedemptions >= batch.MaxRedemptionsPerUser {
		return E(http.StatusConflict, "redemption_user_limit", "user redemption limit reached")
	}
	return nil
}

// BuildBillingTransaction applies billing rules to detached values. Callers
// publish the result to caches only after persistence commits.
func BuildBillingTransaction(input BillingTransactionInput, order *Order, user *User) (BillingTransactionResult, error) {
	now := input.OccurredAt.UTC()
	status := strings.ToLower(strings.TrimSpace(input.DesiredStatus))
	result := BillingTransactionResult{}
	if order != nil {
		clonedOrder := *order
		result.Order = &clonedOrder
	}

	if input.EventID != "" && input.IdempotencyKey != "" {
		provider := normalizeProvider(input.EventProvider)
		if provider == "" && result.Order != nil {
			provider = result.Order.Provider
		}
		provider = defaultString(provider, "local")
		eventType := strings.TrimSpace(input.EventType)
		if eventType == "" && status == "paid" {
			eventType = "payment.succeeded"
		}
		event := WebhookEvent{
			ID:             input.EventID,
			Provider:       provider,
			EventType:      eventType,
			TargetID:       strings.TrimSpace(input.OrderID),
			IdempotencyKey: strings.TrimSpace(input.IdempotencyKey),
			Processed:      true,
			Metadata:       cloneMetadata(input.Metadata),
			ReceivedAt:     now,
		}
		if txID := strings.TrimSpace(input.TxID); txID != "" {
			event.Metadata["txId"] = txID
		}
		result.Event = &event
	}

	if status == "" {
		return result, nil
	}
	if result.Order == nil {
		if status == "paid" {
			return BillingTransactionResult{}, E(http.StatusNotFound, "order_not_found", "order not found")
		}
		return result, nil
	}

	previousStatus := result.Order.Status
	if status == "paid" {
		if previousStatus == "paid" {
			if result.Event != nil && result.Order.TxID != "" {
				result.Event.Metadata["txId"] = result.Order.TxID
			}
			return result, nil
		}
		if user == nil {
			return BillingTransactionResult{}, E(http.StatusNotFound, "user_not_found", "user not found")
		}
		clonedUser := *user
		result.User = &clonedUser
		result.Order.Status = "paid"
		result.Order.TxID = strings.TrimSpace(input.TxID)
		result.Order.PaidAt = &now
		result.User.PlanID = result.Order.PlanID
		days := 30
		if result.Order.Period == "yearly" {
			days = 365
		}
		expires := now.Add(time.Duration(days) * 24 * time.Hour)
		result.User.PlanExpiresAt = &expires
		result.User.UpdatedAt = now
		auditMetadata := cloneMetadata(input.Metadata)
		auditMetadata["planId"] = result.Order.PlanID
		auditMetadata["provider"] = result.Order.Provider
		if result.Order.TxID != "" {
			auditMetadata["txId"] = result.Order.TxID
		}
		result.Audit = &AuditLog{ID: input.AuditID, ActorID: input.ActorID, Action: "billing.order_paid", Target: result.Order.ID, Metadata: auditMetadata, CreatedAt: now}
		result.Mail = &Mail{ID: input.MailID, To: result.User.Email, Subject: "PasteBox payment received", Body: "Your membership is active.", CreatedAt: now}
		return result, nil
	}

	if previousStatus == status || (previousStatus == "paid" && !input.RevokePlan) {
		return result, nil
	}
	planRevoked := false
	if input.RevokePlan && previousStatus == "paid" {
		if user == nil {
			return BillingTransactionResult{}, E(http.StatusNotFound, "user_not_found", "user not found")
		}
		if user.PlanID == result.Order.PlanID {
			clonedUser := *user
			clonedUser.PlanID = "free"
			clonedUser.PlanExpiresAt = nil
			clonedUser.UpdatedAt = now
			result.User = &clonedUser
			planRevoked = true
		}
	}
	result.Order.Status = status
	auditMetadata := cloneMetadata(input.Metadata)
	auditMetadata["planId"] = result.Order.PlanID
	auditMetadata["provider"] = result.Order.Provider
	auditMetadata["previousStatus"] = previousStatus
	auditMetadata["planRevoked"] = planRevoked
	result.Audit = &AuditLog{ID: input.AuditID, ActorID: input.ActorID, Action: "billing.order_" + status, Target: result.Order.ID, Metadata: auditMetadata, CreatedAt: now}
	return result, nil
}
