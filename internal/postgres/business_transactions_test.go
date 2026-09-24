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

func TestPasswordResetTransactionCommitsAllAuthState(t *testing.T) {
	databaseURL := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL to run PostgreSQL password reset transaction integration test")
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

	const (
		userID         = "usr_password_reset_transaction"
		email          = "password-reset-transaction@example.com"
		session        = "sess_password_reset_transaction"
		tokenHashValue = "password_reset_transaction_hash"
		mailID         = "mail_password_reset_transaction"
	)
	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM mails WHERE id = $1`, mailID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM auth_tokens WHERE hash = $1`, tokenHashValue)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM sessions WHERE id = $1`, session)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, userID)
	}
	cleanup()
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Microsecond)
	user := app.User{ID: userID, Email: email, DisplayName: "Reset User", Language: "en", PasswordHash: "old-hash", Role: "user", EmailVerified: true, PlanID: "free", CreatedAt: now, UpdatedAt: now}
	if err := NewUserStore(pool).CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := NewSessionStore(pool).CreateSession(ctx, app.Session{ID: session, UserID: userID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := NewAuthTokenStore(pool).CreateAuthToken(ctx, "password_reset", app.AuthToken{Hash: tokenHashValue, UserID: userID, Email: email, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("create reset token: %v", err)
	}

	result, err := NewBusinessTransactionStore(pool).FinishPasswordReset(ctx, app.PasswordResetTransactionInput{
		TokenHash: tokenHashValue, PasswordHash: "new-hash", UsedAt: now.Add(time.Minute), MailID: mailID, MailCreatedAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("finish password reset transaction: %v", err)
	}
	if result.User.PasswordHash != "new-hash" || result.Mail.ID != mailID {
		t.Fatalf("unexpected transaction result: %#v", result)
	}
	updated, err := NewUserStore(pool).UserByID(ctx, userID)
	if err != nil || updated.PasswordHash != "new-hash" {
		t.Fatalf("expected password update, user=%#v err=%v", updated, err)
	}
	var revokedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT revoked_at FROM sessions WHERE id = $1`, session).Scan(&revokedAt); err != nil {
		t.Fatalf("read revoked session: %v", err)
	}
	if revokedAt == nil {
		t.Fatal("expected session revocation")
	}
	mail, err := NewMailStore(pool).MailByID(ctx, mailID)
	if err != nil || mail.Status != "queued" {
		t.Fatalf("expected queued password change mail, mail=%#v err=%v", mail, err)
	}
	if _, err := NewBusinessTransactionStore(pool).FinishPasswordReset(ctx, app.PasswordResetTransactionInput{
		TokenHash: tokenHashValue, PasswordHash: "another-hash", UsedAt: now.Add(2 * time.Minute), MailID: "mail_password_reset_transaction_second", MailCreatedAt: now.Add(2 * time.Minute),
	}); !errors.Is(err, app.ErrStoreNotFound) {
		t.Fatalf("expected reset token to be single-use, got %v", err)
	}
}

func TestAuthRegistrationTransactionsRollbackOnMailFailure(t *testing.T) {
	databaseURL := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL to run PostgreSQL auth registration transaction integration test")
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

	const (
		registerUserID    = "usr_auth_tx_register"
		oauthUserID       = "usr_auth_tx_oauth"
		registerMailID    = "mail_auth_tx_register"
		registerTokenHash = "token_auth_tx_register"
		oauthMailID       = "mail_auth_tx_oauth"
		oauthSubject      = "auth-tx-oauth-subject"
	)
	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM mails WHERE id IN ($1, $2)`, registerMailID, oauthMailID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM auth_tokens WHERE hash = $1`, registerTokenHash)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_logs WHERE id IN ('aud_auth_tx_linked', 'aud_auth_tx_login')`)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM oauth_identities WHERE provider = 'google' AND subject = $1`, oauthSubject)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id IN ($1, $2)`, registerUserID, oauthUserID)
	}
	cleanup()
	defer cleanup()

	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	if err := NewMailStore(pool).CreateMail(ctx, MailRecord{
		ID: registerMailID, To: "existing@example.com", Subject: "existing", Body: "existing", Status: "queued", CreatedAt: now,
	}); err != nil {
		t.Fatalf("create registration mail conflict: %v", err)
	}
	transactions := NewBusinessTransactionStore(pool)
	registerUser := app.User{
		ID: registerUserID, Email: "auth-tx-register@example.com", DisplayName: "Register", Language: "en",
		PasswordHash: "hash", Role: "user", EmailVerified: true, PlanID: "free", CreatedAt: now, UpdatedAt: now,
	}
	if err := transactions.RegisterUser(ctx, registerUser, app.Mail{
		ID: registerMailID, To: registerUser.Email, Subject: "Welcome", Body: "Welcome", CreatedAt: now,
	}); err == nil {
		t.Fatal("expected registration transaction mail conflict")
	}
	if _, err := NewUserStore(pool).UserByID(ctx, registerUserID); !errors.Is(err, app.ErrStoreNotFound) {
		t.Fatalf("registration user survived rollback: %v", err)
	}
	if err := NewAuthTokenStore(pool).CreateAuthToken(ctx, "registration_email_verification", app.AuthToken{
		Hash: registerTokenHash, Email: registerUser.Email, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create registration token: %v", err)
	}
	verifiedUser := registerUser
	verifiedUser.ID = "usr_auth_tx_verified"
	verifiedUser.Email = "auth-tx-verified@example.com"
	if err := transactions.RegisterUserWithEmailVerification(ctx, verifiedUser, app.Mail{
		ID: registerMailID, To: verifiedUser.Email, Subject: "Welcome", Body: "Welcome", CreatedAt: now,
	}, registerTokenHash, verifiedUser.Email, now.Add(time.Minute)); err == nil {
		t.Fatal("expected verified registration transaction mail conflict")
	}
	var tokenUsedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT used_at FROM auth_tokens WHERE hash = $1`, registerTokenHash).Scan(&tokenUsedAt); err != nil {
		t.Fatalf("read registration token after rollback: %v", err)
	}
	if tokenUsedAt != nil {
		t.Fatalf("registration token was consumed despite rollback: %v", tokenUsedAt)
	}
	if _, err := NewUserStore(pool).UserByID(ctx, verifiedUser.ID); !errors.Is(err, app.ErrStoreNotFound) {
		t.Fatalf("verified registration user survived rollback: %v", err)
	}

	if err := NewMailStore(pool).CreateMail(ctx, MailRecord{
		ID: oauthMailID, To: "existing-oauth@example.com", Subject: "existing", Body: "existing", Status: "queued", CreatedAt: now,
	}); err != nil {
		t.Fatalf("create oauth mail conflict: %v", err)
	}
	oauthUser := app.User{
		ID: oauthUserID, Email: "auth-tx-oauth@example.com", DisplayName: "OAuth", Language: "en",
		PasswordHash: "hash", Role: "user", EmailVerified: true, PlanID: "free", CreatedAt: now, UpdatedAt: now,
	}
	if err := transactions.RegisterOAuthUser(ctx, app.OAuthRegistrationTransactionInput{
		User:     oauthUser,
		Identity: app.OAuthIdentity{UserID: oauthUserID, Provider: "google", Subject: oauthSubject, CreatedAt: now, UpdatedAt: now},
		Audits: []app.AuditLog{
			{ID: "aud_auth_tx_linked", ActorID: oauthUserID, Action: "auth.oauth_linked", Target: oauthUserID, Metadata: map[string]any{"provider": "google"}, CreatedAt: now},
			{ID: "aud_auth_tx_login", ActorID: oauthUserID, Action: "auth.google_oauth", Target: oauthUserID, Metadata: map[string]any{"provider": "google"}, CreatedAt: now},
		},
		Mail: app.Mail{ID: oauthMailID, To: oauthUser.Email, Subject: "Welcome", Body: "Welcome", CreatedAt: now},
	}); err == nil {
		t.Fatal("expected oauth registration transaction mail conflict")
	}
	if _, err := NewUserStore(pool).UserByID(ctx, oauthUserID); !errors.Is(err, app.ErrStoreNotFound) {
		t.Fatalf("oauth user survived rollback: %v", err)
	}
	var identities, audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM oauth_identities WHERE provider = 'google' AND subject = $1`, oauthSubject).Scan(&identities); err != nil {
		t.Fatalf("count oauth identities: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE id IN ('aud_auth_tx_linked', 'aud_auth_tx_login')`).Scan(&audits); err != nil {
		t.Fatalf("count oauth audits: %v", err)
	}
	if identities != 0 || audits != 0 {
		t.Fatalf("oauth transaction partially committed: identities=%d audits=%d", identities, audits)
	}
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
