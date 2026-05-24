package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

func TestAuditLogStorePersistsMetadataAndListsNewestFirst(t *testing.T) {
	databaseURL := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL to run PostgreSQL audit log integration test")
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

	ids := []string{"aud_store_test_old", "aud_store_test_new"}
	for _, id := range ids {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE id = $1`, id)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, id := range ids {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_logs WHERE id = $1`, id)
		}
	})

	store := NewAuditLogStore(pool)
	oldAt := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	newAt := oldAt.Add(time.Hour)
	if err := store.RecordAuditLog(ctx, app.AuditLog{
		ID:        ids[0],
		ActorID:   "usr_audit_admin",
		Action:    "admin.old",
		Target:    "target-old",
		Metadata:  map[string]any{"status": "open"},
		CreatedAt: oldAt,
	}); err != nil {
		t.Fatalf("record old audit log: %v", err)
	}
	if err := store.RecordAuditLog(ctx, app.AuditLog{
		ID:        ids[1],
		ActorID:   "usr_audit_admin",
		Action:    "admin.new",
		Target:    "target-new",
		Metadata:  map[string]any{"status": "resolved", "attempts": float64(2)},
		CreatedAt: newAt,
	}); err != nil {
		t.Fatalf("record new audit log: %v", err)
	}

	logs, err := store.AuditLogs(ctx, 100)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	newIndex := auditLogIndex(logs, ids[1])
	oldIndex := auditLogIndex(logs, ids[0])
	if newIndex == -1 || oldIndex == -1 || newIndex > oldIndex {
		t.Fatalf("expected newest-first order, got %#v", logs)
	}
	if logs[newIndex].Metadata["status"] != "resolved" || logs[newIndex].Metadata["attempts"] != float64(2) {
		t.Fatalf("expected decoded metadata, got %#v", logs[newIndex].Metadata)
	}
}

func auditLogIndex(logs []app.AuditLog, id string) int {
	for index, log := range logs {
		if log.ID == id {
			return index
		}
	}
	return -1
}
