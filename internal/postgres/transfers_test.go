package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

func TestTransferStorePersistsAndPublishesOnce(t *testing.T) {
	dsn := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL integration database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := ApplyMigrations(ctx, dsn); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	second, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect second postgres pool: %v", err)
	}
	t.Cleanup(second.Close)

	now := time.Now().UTC()
	id := fmt.Sprintf("transfer_%d", now.UnixNano())
	user := app.User{
		ID: id, Email: id + "@example.com", DisplayName: "Transfer", Language: "en",
		PasswordHash: "hash", Role: "user", PlanID: "free", EmailVerified: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := NewUserStore(pool).CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, user.ID); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})

	paste := app.Paste{
		ID: id + "_paste", UserID: user.ID, Title: "report.txt", Status: "active",
		ScanStatus: "clean", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	if err := NewPasteStore(pool).CreatePaste(ctx, paste); err != nil {
		t.Fatalf("create paste: %v", err)
	}
	attachment := app.Attachment{
		ID: id + "_attachment", UserID: user.ID, PasteID: paste.ID, FileName: "report.txt",
		ContentType: "text/plain", Size: 5, SHA256: id + "_digest", ObjectKey: id + "_object",
		Status: "active", ScanStatus: "clean", CreatedAt: now,
	}
	if err := NewAttachmentStore(pool).CreateAttachment(ctx, attachment); err != nil {
		t.Fatalf("create attachment: %v", err)
	}

	store := NewTransferStore(pool)
	transfer := app.Transfer{
		ID: id + "_transfer", UserID: user.ID, PasteID: paste.ID, Status: app.TransferStatusDraft,
		IdempotencyKey: "key-1", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateTransfer(ctx, transfer); err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	item := app.TransferItem{
		TransferID: transfer.ID, ItemID: "itm-1", FileName: "report.txt", ContentType: "text/plain",
		Status: app.TransferItemPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateTransferItem(ctx, item); err != nil {
		t.Fatalf("create transfer item: %v", err)
	}

	duplicate := transfer
	duplicate.ID = id + "_transfer_duplicate"
	if err := store.CreateTransfer(ctx, duplicate); !errors.Is(err, app.ErrStoreConflict) {
		t.Fatalf("expected a reused idempotency key to conflict, got %v", err)
	}

	// A second store instance sees the same rows, so state survives a restart.
	reopened := NewTransferStore(second)
	loaded, err := reopened.TransferByID(ctx, transfer.ID)
	if err != nil {
		t.Fatalf("reload transfer: %v", err)
	}
	if loaded.PasteID != paste.ID || loaded.Status != app.TransferStatusDraft || loaded.IdempotencyKey != "key-1" {
		t.Fatalf("unexpected reloaded transfer: %#v", loaded)
	}
	items, err := reopened.ListTransferItems(ctx, transfer.ID)
	if err != nil {
		t.Fatalf("reload transfer items: %v", err)
	}
	if len(items) != 1 || items[0].Status != app.TransferItemPending {
		t.Fatalf("unexpected reloaded items: %#v", items)
	}
	byKey, err := reopened.TransferByIdempotencyKey(ctx, user.ID, "key-1")
	if err != nil || byKey.ID != transfer.ID {
		t.Fatalf("expected the idempotency lookup to find the transfer, got %#v err=%v", byKey, err)
	}

	// A transfer with unfinished items cannot be published.
	unfinished := app.Share{
		ID: id + "_share_unfinished", PasteID: paste.ID, UserID: user.ID,
		TokenHash: id + "_hash_unfinished", Token: id + "_token_unfinished",
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	if _, err := store.PublishTransfer(ctx, transfer.ID, unfinished, now); !errors.Is(err, app.ErrStoreConflict) {
		t.Fatalf("expected an unfinished transfer to be rejected, got %v", err)
	}

	item.Status = app.TransferItemUploaded
	item.AttachmentID = attachment.ID
	item.Size = attachment.Size
	item.UpdatedAt = now
	if err := store.UpdateTransferItem(ctx, item); err != nil {
		t.Fatalf("mark item uploaded: %v", err)
	}

	// Two concurrent publish attempts must produce exactly one share.
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]app.Transfer, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			share := app.Share{
				ID:        fmt.Sprintf("%s_share_%d", id, i),
				PasteID:   paste.ID,
				UserID:    user.ID,
				TokenHash: fmt.Sprintf("%s_hash_%d", id, i),
				Token:     fmt.Sprintf("%s_token_%d", id, i),
				ExpiresAt: now.Add(time.Hour),
				CreatedAt: now,
			}
			results[i], errs[i] = store.PublishTransfer(ctx, transfer.ID, share, time.Now().UTC())
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("publish %d failed: %v", i, err)
		}
	}
	if results[0].Status != app.TransferStatusPublished || results[0].ShareID == "" || results[0].PublishedAt == nil {
		t.Fatalf("unexpected published transfer: %#v", results[0])
	}
	if results[1].ShareID != results[0].ShareID {
		t.Fatalf("expected both publishes to agree on one share, got %s and %s", results[0].ShareID, results[1].ShareID)
	}

	var shareCount int
	if err := second.QueryRow(ctx, `SELECT count(*) FROM shares WHERE paste_id = $1`, paste.ID).Scan(&shareCount); err != nil {
		t.Fatalf("count shares: %v", err)
	}
	if shareCount != 1 {
		t.Fatalf("expected exactly one share row for the transfer, got %d", shareCount)
	}

	var itemCount int
	if err := second.QueryRow(ctx, `SELECT count(*) FROM transfer_items WHERE transfer_id = $1`, transfer.ID).Scan(&itemCount); err != nil {
		t.Fatalf("count transfer items: %v", err)
	}
	if itemCount != 1 {
		t.Fatalf("expected exactly one transfer item, got %d", itemCount)
	}
}
