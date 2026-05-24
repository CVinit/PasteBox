package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

func TestOperationalStateStoresRoundTripBillingSupportJobsAndMail(t *testing.T) {
	databaseURL := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL to run PostgreSQL operational state integration test")
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

	userID := "usr_operational_state_test"
	orderID := "ord_operational_state_test"
	webhookID := "wh_operational_state_test"
	duplicateWebhookID := "wh_operational_state_duplicate"
	reportID := "rpt_operational_state_test"
	jobID := "job_operational_state_test"
	mailID := "mail_operational_state_test"
	idempotencyKey := "operational-state-idempotency-key"
	cleanupOperationalStateTestRows(ctx, t, pool, userID, orderID, webhookID, duplicateWebhookID, reportID, jobID, mailID, idempotencyKey)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanupOperationalStateTestRows(cleanupCtx, t, pool, userID, orderID, webhookID, duplicateWebhookID, reportID, jobID, mailID, idempotencyKey)
	})

	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	if err := NewUserStore(pool).CreateUser(ctx, app.User{
		ID:            userID,
		Email:         "operational-state-test@example.com",
		DisplayName:   "Operational State Test",
		Language:      "en",
		PasswordHash:  "argon2-test-hash",
		Role:          "user",
		EmailVerified: true,
		PlanID:        "free",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	orderStore := NewOrderStore(pool)
	expiresAt := now.Add(30 * time.Minute)
	order := app.Order{
		ID:          orderID,
		UserID:      userID,
		Provider:    "stripe",
		PlanID:      "plus",
		Period:      "monthly",
		AmountCents: 900,
		Currency:    "USD",
		Status:      "pending",
		CheckoutURL: "https://checkout.example/order",
		Address:     "",
		Chain:       "",
		CreatedAt:   now,
		ExpiresAt:   &expiresAt,
	}
	if err := orderStore.CreateOrder(ctx, order); err != nil {
		t.Fatalf("create order: %v", err)
	}
	loadedOrder, err := orderStore.OrderByID(ctx, orderID)
	if err != nil {
		t.Fatalf("read order: %v", err)
	}
	if loadedOrder.ID != orderID || loadedOrder.Provider != "stripe" || loadedOrder.ExpiresAt == nil {
		t.Fatalf("unexpected order: %#v", loadedOrder)
	}
	paidAt := now.Add(time.Hour)
	order.Status = "paid"
	order.TxID = "tx-operational"
	order.PaidAt = &paidAt
	if err := orderStore.UpdateOrder(ctx, order); err != nil {
		t.Fatalf("update order: %v", err)
	}
	orders, err := orderStore.ListOrdersByUser(ctx, userID)
	if err != nil {
		t.Fatalf("list orders: %v", err)
	}
	if len(orders) != 1 || orders[0].Status != "paid" || orders[0].TxID != "tx-operational" || orders[0].PaidAt == nil {
		t.Fatalf("unexpected orders: %#v", orders)
	}
	if _, err := orderStore.OrderByID(ctx, "ord_operational_state_missing"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("expected missing order error, got %v", err)
	}

	webhookStore := NewWebhookEventStore(pool)
	event := app.WebhookEvent{
		ID:             webhookID,
		Provider:       "stripe",
		EventType:      "checkout.session.completed",
		TargetID:       orderID,
		IdempotencyKey: idempotencyKey,
		Processed:      false,
		Metadata:       map[string]any{"txId": "tx-operational", "attempt": float64(1)},
		ReceivedAt:     now.Add(time.Minute),
	}
	if err := webhookStore.CreateWebhookEvent(ctx, event); err != nil {
		t.Fatalf("create webhook event: %v", err)
	}
	loadedEvent, err := webhookStore.WebhookEventByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		t.Fatalf("read webhook event by key: %v", err)
	}
	if loadedEvent.ID != webhookID || loadedEvent.Metadata["txId"] != "tx-operational" || loadedEvent.Metadata["attempt"] != float64(1) {
		t.Fatalf("unexpected webhook event: %#v", loadedEvent)
	}
	duplicateEvent := event
	duplicateEvent.ID = duplicateWebhookID
	if err := webhookStore.CreateWebhookEvent(ctx, duplicateEvent); !errors.Is(err, ErrWebhookEventExists) {
		t.Fatalf("expected duplicate webhook event error, got %v", err)
	}
	if err := webhookStore.UpdateWebhookEventProcessed(ctx, webhookID, true); err != nil {
		t.Fatalf("update webhook event processed: %v", err)
	}
	processedEvent, err := webhookStore.WebhookEventByID(ctx, webhookID)
	if err != nil {
		t.Fatalf("read processed webhook event: %v", err)
	}
	if !processedEvent.Processed {
		t.Fatalf("expected processed webhook event, got %#v", processedEvent)
	}
	if _, err := webhookStore.WebhookEventByID(ctx, "wh_operational_state_missing"); !errors.Is(err, ErrWebhookEventNotFound) {
		t.Fatalf("expected missing webhook event error, got %v", err)
	}

	reportStore := NewReportStore(pool)
	report := app.Report{ID: reportID, UserID: userID, Target: "share:abc", Reason: "abuse", Status: "open", CreatedAt: now}
	if err := reportStore.CreateReport(ctx, report); err != nil {
		t.Fatalf("create report: %v", err)
	}
	loadedReport, err := reportStore.ReportByID(ctx, reportID)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if loadedReport.UserID != userID || loadedReport.Status != "open" {
		t.Fatalf("unexpected report: %#v", loadedReport)
	}
	if err := reportStore.UpdateReportStatus(ctx, reportID, "resolved"); err != nil {
		t.Fatalf("update report status: %v", err)
	}
	resolvedReport, err := reportStore.ReportByID(ctx, reportID)
	if err != nil {
		t.Fatalf("read resolved report: %v", err)
	}
	if resolvedReport.Status != "resolved" {
		t.Fatalf("expected resolved report, got %#v", resolvedReport)
	}
	if _, err := reportStore.ReportByID(ctx, "rpt_operational_state_missing"); !errors.Is(err, ErrReportNotFound) {
		t.Fatalf("expected missing report error, got %v", err)
	}

	jobStore := NewJobStore(pool)
	job := JobRecord{
		ID:        jobID,
		Kind:      "scan",
		TargetID:  "att-operational",
		Status:    "pending",
		Attempts:  0,
		LastError: "",
		RunAfter:  now.Add(-time.Minute),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := jobStore.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	runnable, err := jobStore.ListRunnableJobs(ctx, 10, now)
	if err != nil {
		t.Fatalf("list runnable jobs: %v", err)
	}
	if len(runnable) != 1 || runnable[0].ID != jobID {
		t.Fatalf("expected runnable job, got %#v", runnable)
	}
	job.Status = "failed"
	job.Attempts = 1
	job.LastError = "scanner unavailable"
	job.UpdatedAt = now.Add(time.Minute)
	if err := jobStore.UpdateJob(ctx, job); err != nil {
		t.Fatalf("update job: %v", err)
	}
	failedJob, err := jobStore.JobByID(ctx, jobID)
	if err != nil {
		t.Fatalf("read failed job: %v", err)
	}
	if failedJob.Status != "failed" || failedJob.Attempts != 1 || failedJob.LastError != "scanner unavailable" {
		t.Fatalf("unexpected failed job: %#v", failedJob)
	}
	if _, err := jobStore.JobByID(ctx, "job_operational_state_missing"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected missing job error, got %v", err)
	}

	mailStore := NewMailStore(pool)
	mail := MailRecord{
		ID:        mailID,
		To:        "user@example.com",
		Subject:   "Verify",
		Body:      "Token",
		Status:    "queued",
		Attempts:  0,
		LastError: "",
		CreatedAt: now,
	}
	if err := mailStore.CreateMail(ctx, mail); err != nil {
		t.Fatalf("create mail: %v", err)
	}
	queuedMail, err := mailStore.ListQueuedMail(ctx, 10)
	if err != nil {
		t.Fatalf("list queued mail: %v", err)
	}
	createdMail, ok := mailRecordByID(queuedMail, mailID)
	if !ok {
		t.Fatalf("expected queued mail, got %#v", queuedMail)
	}
	if !createdMail.RunAfter.Equal(now) {
		t.Fatalf("expected queued mail to be runnable at creation time, got %#v", createdMail)
	}
	runnableMail, err := mailStore.ListRunnableMail(ctx, 10, now)
	if err != nil {
		t.Fatalf("list runnable mail: %v", err)
	}
	if _, ok := mailRecordByID(runnableMail, mailID); !ok {
		t.Fatalf("expected runnable mail, got %#v", runnableMail)
	}
	sentAt := now.Add(2 * time.Minute)
	mail.Status = "sent"
	mail.Attempts = 1
	mail.RunAfter = sentAt
	mail.SentAt = &sentAt
	if err := mailStore.UpdateMail(ctx, mail); err != nil {
		t.Fatalf("update mail: %v", err)
	}
	sentMail, err := mailStore.MailByID(ctx, mailID)
	if err != nil {
		t.Fatalf("read sent mail: %v", err)
	}
	if sentMail.Status != "sent" || sentMail.SentAt == nil || !sentMail.SentAt.Equal(sentAt) || !sentMail.RunAfter.Equal(sentAt) {
		t.Fatalf("unexpected sent mail: %#v", sentMail)
	}
	if _, err := mailStore.MailByID(ctx, "mail_operational_state_missing"); !errors.Is(err, ErrMailNotFound) {
		t.Fatalf("expected missing mail error, got %v", err)
	}
}

func cleanupOperationalStateTestRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, userID string, orderID string, webhookID string, duplicateWebhookID string, reportID string, jobID string, mailID string, idempotencyKey string) {
	t.Helper()
	_, _ = pool.Exec(ctx, `DELETE FROM webhook_events WHERE id IN ($1, $2) OR idempotency_key = $3`, webhookID, duplicateWebhookID, idempotencyKey)
	_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE id = $1`, orderID)
	_, _ = pool.Exec(ctx, `DELETE FROM reports WHERE id = $1`, reportID)
	_, _ = pool.Exec(ctx, `DELETE FROM jobs WHERE id = $1`, jobID)
	_, _ = pool.Exec(ctx, `DELETE FROM mails WHERE id = $1`, mailID)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
}

func mailRecordByID(records []MailRecord, id string) (MailRecord, bool) {
	for _, record := range records {
		if record.ID == id {
			return record, true
		}
	}
	return MailRecord{}, false
}
