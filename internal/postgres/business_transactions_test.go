package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

func TestBusinessTransactionsSerializeRedemptionAndRollbackBilling(t *testing.T) {
	databaseURL := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL to run PostgreSQL business transaction integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := ApplyMigrations(ctx, databaseURL); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	cleanupBusinessTransactionRows(ctx, pool)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanupBusinessTransactionRows(cleanupCtx, pool)
	}()

	now := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
	userStore := NewUserStore(pool)
	redemptionUser := app.User{
		ID: "usr_business_redemption", Email: "business-redemption@example.com", DisplayName: "Redemption",
		Language: "en", PasswordHash: "hash", Role: "user", EmailVerified: true, PlanID: "free",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := userStore.CreateUser(ctx, redemptionUser); err != nil {
		t.Fatalf("create redemption user: %v", err)
	}
	batch := app.RedemptionBatch{
		ID: "rb_business_transaction", PlanID: "plus", DurationDays: 30, Quantity: 1,
		MaxTotalRedemptions: 1, MaxRedemptionsPerUser: 1, CreatedAt: now, UpdatedAt: now,
	}
	code := app.RedemptionCode{CodeHash: "business_redemption_code_hash", BatchID: batch.ID, CreatedAt: now}
	if err := NewRedemptionStore(pool).CreateRedemptionBatch(ctx, batch, []app.RedemptionCode{code}); err != nil {
		t.Fatalf("create redemption batch: %v", err)
	}

	transactions := NewBusinessTransactionStore(pool)
	start := make(chan struct{})
	results := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, results[index] = transactions.RedeemCode(context.Background(), app.RedemptionTransactionInput{
				UserID: redemptionUser.ID, CodeHash: code.CodeHash,
				RecordID: "red_business_" + string(rune('a'+index)), AuditID: "aud_business_redemption_" + string(rune('a'+index)),
				RedeemedAt: now.Add(time.Minute),
			})
		}(i)
	}
	close(start)
	wg.Wait()
	successes := 0
	usedErrors := 0
	for _, err := range results {
		if err == nil {
			successes++
			continue
		}
		var appErr *app.Error
		if errors.As(err, &appErr) && appErr.Code == "redemption_code_used" {
			usedErrors++
		}
	}
	if successes != 1 || usedErrors != 1 {
		t.Fatalf("expected one redemption and one used-code error, got %#v", results)
	}
	assertRedemptionTransactionState(t, ctx, pool, redemptionUser.ID, batch.ID, code.CodeHash)

	billingUser := app.User{
		ID: "usr_business_billing", Email: "business-billing@example.com", DisplayName: "Billing",
		Language: "en", PasswordHash: "hash", Role: "user", EmailVerified: true, PlanID: "free",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := userStore.CreateUser(ctx, billingUser); err != nil {
		t.Fatalf("create billing user: %v", err)
	}
	order := app.Order{
		ID: "ord_business_transaction", UserID: billingUser.ID, Provider: "stripe", PlanID: "plus", Period: "monthly",
		AmountCents: 900, Currency: "USD", Status: "pending", CreatedAt: now,
	}
	if err := NewOrderStore(pool).CreateOrder(ctx, order); err != nil {
		t.Fatalf("create billing order: %v", err)
	}
	if err := NewMailStore(pool).CreateMail(ctx, MailRecord{
		ID: "mail_business_existing", To: "existing@example.com", Subject: "existing", Body: "existing",
		Status: "queued", CreatedAt: now,
	}); err != nil {
		t.Fatalf("create conflicting mail: %v", err)
	}
	failedInput := app.BillingTransactionInput{
		ActorID: "webhook:stripe", OrderID: order.ID, TxID: "tx-business", DesiredStatus: "paid",
		EventID: "wh_business_failed", EventProvider: "stripe", EventType: "payment.succeeded",
		IdempotencyKey: "billing-business-failed", AuditID: "aud_business_billing_failed",
		MailID: "mail_business_existing", OccurredAt: now.Add(time.Minute),
	}
	if _, err := transactions.ApplyBilling(ctx, failedInput); err == nil {
		t.Fatal("expected duplicate mail to roll back billing transaction")
	}
	assertBillingTransactionRolledBack(t, ctx, pool, billingUser.ID, order.ID, failedInput)

	successInput := failedInput
	successInput.EventID = "wh_business_success"
	successInput.IdempotencyKey = "billing-business-success"
	successInput.AuditID = "aud_business_billing_success"
	successInput.MailID = "mail_business_success"
	result, err := transactions.ApplyBilling(ctx, successInput)
	if err != nil {
		t.Fatalf("apply successful billing transaction: %v", err)
	}
	if result.Order == nil || result.Order.Status != "paid" || result.User == nil || result.User.PlanID != "plus" || result.Event == nil || result.Audit == nil || result.Mail == nil {
		t.Fatalf("unexpected billing transaction result: %#v", result)
	}
	assertBillingTransactionCommitted(t, ctx, pool, billingUser.ID, order.ID, successInput)
}

func assertRedemptionTransactionState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID string, batchID string, codeHash string) {
	t.Helper()
	user, err := NewUserStore(pool).UserByID(ctx, userID)
	if err != nil || user.PlanID != "plus" || user.PlanExpiresAt == nil {
		t.Fatalf("unexpected redeemed user: user=%#v err=%v", user, err)
	}
	var redeemedCount int
	if err := pool.QueryRow(ctx, `SELECT redeemed_count FROM redemption_batches WHERE id = $1`, batchID).Scan(&redeemedCount); err != nil || redeemedCount != 1 {
		t.Fatalf("unexpected redeemed count: count=%d err=%v", redeemedCount, err)
	}
	var records, audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM redemption_records WHERE code_hash = $1`, codeHash).Scan(&records); err != nil {
		t.Fatalf("count redemption records: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'billing.redemption_redeemed' AND target = $1`, batchID).Scan(&audits); err != nil {
		t.Fatalf("count redemption audits: %v", err)
	}
	if records != 1 || audits != 1 {
		t.Fatalf("redemption transaction split: records=%d audits=%d", records, audits)
	}
}

func assertBillingTransactionRolledBack(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID string, orderID string, input app.BillingTransactionInput) {
	t.Helper()
	user, _ := NewUserStore(pool).UserByID(ctx, userID)
	order, _ := NewOrderStore(pool).OrderByID(ctx, orderID)
	var events, audits int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE idempotency_key = $1`, input.IdempotencyKey).Scan(&events)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE id = $1`, input.AuditID).Scan(&audits)
	if user.PlanID != "free" || user.PlanExpiresAt != nil || order.Status != "pending" || events != 0 || audits != 0 {
		t.Fatalf("billing transaction did not roll back: user=%#v order=%#v events=%d audits=%d", user, order, events, audits)
	}
}

func assertBillingTransactionCommitted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID string, orderID string, input app.BillingTransactionInput) {
	t.Helper()
	user, _ := NewUserStore(pool).UserByID(ctx, userID)
	order, _ := NewOrderStore(pool).OrderByID(ctx, orderID)
	var events, audits, mails int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE idempotency_key = $1`, input.IdempotencyKey).Scan(&events)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE id = $1`, input.AuditID).Scan(&audits)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM mails WHERE id = $1`, input.MailID).Scan(&mails)
	if user.PlanID != "plus" || user.PlanExpiresAt == nil || order.Status != "paid" || events != 1 || audits != 1 || mails != 1 {
		t.Fatalf("billing transaction split: user=%#v order=%#v events=%d audits=%d mails=%d", user, order, events, audits, mails)
	}
}

func cleanupBusinessTransactionRows(ctx context.Context, pool *pgxpool.Pool) {
	_, _ = pool.Exec(ctx, `DELETE FROM redemption_records WHERE batch_id = 'rb_business_transaction'`)
	_, _ = pool.Exec(ctx, `DELETE FROM redemption_codes WHERE batch_id = 'rb_business_transaction'`)
	_, _ = pool.Exec(ctx, `DELETE FROM redemption_batches WHERE id = 'rb_business_transaction'`)
	_, _ = pool.Exec(ctx, `DELETE FROM webhook_events WHERE idempotency_key IN ('billing-business-failed', 'billing-business-success')`)
	_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE id LIKE 'aud_business_%'`)
	_, _ = pool.Exec(ctx, `DELETE FROM mails WHERE id IN ('mail_business_existing', 'mail_business_success')`)
	_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE id = 'ord_business_transaction'`)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ('usr_business_redemption', 'usr_business_billing')`)
}
