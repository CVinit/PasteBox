package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

func TestRuntimeControlStoresPersistConfigRedemptionsAndAlerts(t *testing.T) {
	databaseURL := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL to run PostgreSQL runtime control integration test")
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

	cleanupRuntimeControlStoreRows(t, pool)

	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	runtimeStore := NewRuntimeConfigStore(pool)
	cfg := app.RuntimeConfig{
		ID:       "default",
		LogLevel: app.RuntimeLogLevelDebug,
		GuestUploads: app.GuestUploadConfig{
			Enabled:            true,
			RequireTurnstile:   true,
			RetentionSeconds:   3600,
			ActivePasteLimit:   2,
			ActiveStorageBytes: 1024,
			SingleTextBytes:    512,
			SingleFileBytes:    512,
			SinglePasteBytes:   1024,
			DailyUploadBytes:   2048,
		},
		Alerts: app.AlertConfig{
			Enabled:                true,
			TelegramEnabled:        true,
			CooldownSeconds:        60,
			CPUPercentThreshold:    90,
			MemoryPercentThreshold: 90,
			DiskPercentThreshold:   90,
		},
		UpdatedAt: now,
	}
	if err := runtimeStore.SaveRuntimeConfig(ctx, cfg); err != nil {
		t.Fatalf("save runtime config: %v", err)
	}
	loaded, ok, err := runtimeStore.RuntimeConfig(ctx)
	if err != nil {
		t.Fatalf("read runtime config: %v", err)
	}
	if !ok || loaded.LogLevel != app.RuntimeLogLevelDebug || !loaded.GuestUploads.Enabled || !loaded.Alerts.TelegramEnabled {
		t.Fatalf("unexpected loaded runtime config: ok=%v cfg=%#v", ok, loaded)
	}

	user := app.User{
		ID:            "usr_runtime_store_test",
		Email:         "runtime-store@example.com",
		DisplayName:   "Runtime Store",
		Language:      "en",
		PasswordHash:  "hash",
		Role:          "user",
		EmailVerified: true,
		PlanID:        "free",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := NewUserStore(pool).CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	redemptions := NewRedemptionStore(pool)
	batch := app.RedemptionBatch{
		ID:                    "rb_runtime_store_test",
		PlanID:                "plus",
		DurationDays:          30,
		Quantity:              1,
		MaxTotalRedemptions:   1,
		MaxRedemptionsPerUser: 1,
		AllowedEmails:         []string{"runtime-store@example.com"},
		AllowedDomains:        []string{"example.com"},
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	code := app.RedemptionCode{CodeHash: "redemption_hash_runtime_store_test", BatchID: batch.ID, CreatedAt: now}
	if err := redemptions.CreateRedemptionBatch(ctx, batch, []app.RedemptionCode{code}); err != nil {
		t.Fatalf("create redemption batch: %v", err)
	}
	redeemedAt := now.Add(time.Minute)
	code.RedeemedBy = user.ID
	code.RedeemedAt = &redeemedAt
	if err := redemptions.UpdateRedemptionCode(ctx, code); err != nil {
		t.Fatalf("update redemption code: %v", err)
	}
	batch.RedeemedCount = 1
	batch.UpdatedAt = redeemedAt
	if err := redemptions.UpdateRedemptionBatch(ctx, batch); err != nil {
		t.Fatalf("update redemption batch: %v", err)
	}
	record := app.RedemptionRecord{ID: "red_runtime_store_test", CodeHash: code.CodeHash, BatchID: batch.ID, UserID: user.ID, PlanID: batch.PlanID, CreatedAt: redeemedAt}
	if err := redemptions.CreateRedemptionRecord(ctx, record); err != nil {
		t.Fatalf("create redemption record: %v", err)
	}
	batches, err := redemptions.ListRedemptionBatches(ctx)
	if err != nil {
		t.Fatalf("list redemption batches: %v", err)
	}
	if len(batches) == 0 || batches[0].AllowedDomains[0] != "example.com" {
		t.Fatalf("unexpected redemption batches: %#v", batches)
	}
	codes, err := redemptions.ListRedemptionCodes(ctx)
	if err != nil {
		t.Fatalf("list redemption codes: %v", err)
	}
	if len(codes) == 0 || codes[0].RedeemedBy != user.ID || codes[0].RedeemedAt == nil {
		t.Fatalf("unexpected redemption codes: %#v", codes)
	}
	records, err := redemptions.ListRedemptionRecords(ctx)
	if err != nil {
		t.Fatalf("list redemption records: %v", err)
	}
	if len(records) == 0 || records[0].UserID != user.ID {
		t.Fatalf("unexpected redemption records: %#v", records)
	}

	alerts := NewAlertEventStore(pool)
	event := app.AlertEvent{
		ID:          "alrt_runtime_store_test",
		Fingerprint: "cpu_high",
		Level:       "warning",
		Message:     "CPU high",
		Status:      "pending",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := alerts.CreateAlertEvent(ctx, event); err != nil {
		t.Fatalf("create alert event: %v", err)
	}
	event.Status = "sent"
	sentAt := now.Add(time.Minute)
	event.SentAt = &sentAt
	event.UpdatedAt = sentAt
	if err := alerts.UpdateAlertEvent(ctx, event); err != nil {
		t.Fatalf("update alert event: %v", err)
	}
	events, err := alerts.ListAlertEvents(ctx, 10)
	if err != nil {
		t.Fatalf("list alert events: %v", err)
	}
	if len(events) == 0 || events[0].Status != "sent" || events[0].SentAt == nil {
		t.Fatalf("unexpected alert events: %#v", events)
	}
}

func cleanupRuntimeControlStoreRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	statements := []string{
		`DELETE FROM redemption_records WHERE id = 'red_runtime_store_test'`,
		`DELETE FROM redemption_codes WHERE code_hash = 'redemption_hash_runtime_store_test'`,
		`DELETE FROM redemption_batches WHERE id = 'rb_runtime_store_test'`,
		`DELETE FROM users WHERE id = 'usr_runtime_store_test'`,
		`DELETE FROM alert_events WHERE id = 'alrt_runtime_store_test'`,
		`DELETE FROM system_configs WHERE id = 'default'`,
	}
	for _, stmt := range statements {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("cleanup runtime control store row with %q: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, stmt := range statements {
			_, _ = pool.Exec(cleanupCtx, stmt)
		}
	})
}
