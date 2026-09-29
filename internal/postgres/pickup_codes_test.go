package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

func pickupTestPools(t *testing.T, ctx context.Context) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL integration database required")
	}
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
	return pool, second
}

func TestSharePickupCodeIsUniqueAcrossConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, second := pickupTestPools(t, ctx)

	now := time.Now().UTC()
	id := fmt.Sprintf("pickup_%d", now.UnixNano())
	user := app.User{
		ID: id, Email: id + "@example.com", DisplayName: "Pickup", Language: "en",
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
		ID: id + "_paste", UserID: user.ID, Title: "pickup", Status: "active",
		ScanStatus: "clean", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	if err := NewPasteStore(pool).CreatePaste(ctx, paste); err != nil {
		t.Fatalf("create paste: %v", err)
	}

	code := "AB2345"
	stored := app.Share{
		ID: id + "_share_a", PasteID: paste.ID, UserID: user.ID,
		TokenHash: id + "_hash_a", Token: id + "_token_a", PickupCode: code,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	if err := NewShareStore(pool).CreateShare(ctx, stored); err != nil {
		t.Fatalf("create share: %v", err)
	}

	// A second connection cannot take the same code, so two API instances can
	// never hand one code to two different shares.
	duplicate := stored
	duplicate.ID = id + "_share_b"
	duplicate.TokenHash = id + "_hash_b"
	duplicate.Token = id + "_token_b"
	if err := NewShareStore(second).CreateShare(ctx, duplicate); !errors.Is(err, app.ErrSharePickupCodeExists) {
		t.Fatalf("expected a duplicate pickup code to conflict, got %v", err)
	}

	// The code resolves the share that owns it, and only that share.
	resolved, err := NewShareStore(second).ShareByPickupCode(ctx, code)
	if err != nil {
		t.Fatalf("resolve pickup code: %v", err)
	}
	if resolved.ID != stored.ID || resolved.PickupCode != code {
		t.Fatalf("unexpected resolved share: %#v", resolved)
	}
	if _, err := NewShareStore(second).ShareByPickupCode(ctx, "234567"); !errors.Is(err, ErrShareNotFound) {
		t.Fatalf("expected an unknown code to be not found, got %v", err)
	}
}

func TestPickupAttemptBudgetIsSharedAcrossConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, second := pickupTestPools(t, ctx)

	key := fmt.Sprintf("pickup:ip:198.51.100.%d", time.Now().UnixNano()%250)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM pickup_code_attempts WHERE attempt_key = $1`, key); err != nil {
			t.Errorf("cleanup pickup attempts: %v", err)
		}
	})

	first := NewPickupAttemptStore(pool)
	other := NewPickupAttemptStore(second)
	now := time.Now().UTC()
	window := time.Minute
	limit := 3

	// Failures spread over two API instances still share one budget.
	for attempt := 1; attempt <= limit; attempt++ {
		store := first
		if attempt%2 == 0 {
			store = other
		}
		state, err := store.PickupAttemptCount(ctx, key, window, now)
		if err != nil {
			t.Fatalf("read attempt %d: %v", attempt, err)
		}
		if state.Count != attempt-1 {
			t.Fatalf("expected %d failures before attempt %d, got %d", attempt-1, attempt, state.Count)
		}
		recorded, err := store.RecordPickupFailure(ctx, key, window, now)
		if err != nil {
			t.Fatalf("record attempt %d: %v", attempt, err)
		}
		if recorded.Count != attempt {
			t.Fatalf("expected %d recorded failures, got %d", attempt, recorded.Count)
		}
	}

	// The other connection sees the spent budget, so it cannot hand out extra
	// guesses.
	state, err := other.PickupAttemptCount(ctx, key, window, now)
	if err != nil {
		t.Fatalf("read shared budget: %v", err)
	}
	if state.Count < limit {
		t.Fatalf("expected the shared budget to report %d failures, got %d", limit, state.Count)
	}

	// A new window gives the key a fresh budget instead of locking it forever.
	state, err = first.PickupAttemptCount(ctx, key, window, now.Add(window+time.Second))
	if err != nil {
		t.Fatalf("read after window: %v", err)
	}
	if state.Count != 0 {
		t.Fatalf("expected an expired window to read as zero, got %d", state.Count)
	}
	recorded, err := first.RecordPickupFailure(ctx, key, window, now.Add(window+time.Second))
	if err != nil {
		t.Fatalf("record after window: %v", err)
	}
	if recorded.Count != 1 {
		t.Fatalf("expected the new window to start at one failure, got %d", recorded.Count)
	}
}

func TestPublishTransferRejectsADuplicatePickupCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, second := pickupTestPools(t, ctx)

	now := time.Now().UTC()
	id := fmt.Sprintf("pickup_publish_%d", now.UnixNano())
	user := app.User{
		ID: id, Email: id + "@example.com", DisplayName: "Pickup Publish", Language: "en",
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
		ID: id + "_paste", UserID: user.ID, Title: "pickup", Status: "active",
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

	// The code that is already taken belongs to a different share.
	taken := app.Share{
		ID: id + "_share_taken", PasteID: paste.ID, UserID: user.ID,
		TokenHash: id + "_hash_taken", Token: id + "_token_taken", PickupCode: "CD2345",
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	if err := NewShareStore(pool).CreateShare(ctx, taken); err != nil {
		t.Fatalf("create taken share: %v", err)
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
		AttachmentID: attachment.ID, Size: attachment.Size, Status: app.TransferItemUploaded,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateTransferItem(ctx, item); err != nil {
		t.Fatalf("create transfer item: %v", err)
	}

	colliding := app.Share{
		ID: id + "_share_published", PasteID: paste.ID, UserID: user.ID,
		TokenHash: id + "_hash_published", Token: id + "_token_published", PickupCode: "CD2345",
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	if _, err := store.PublishTransfer(ctx, transfer.ID, colliding, now); !errors.Is(err, app.ErrSharePickupCodeExists) {
		t.Fatalf("expected publishing to report a pickup code conflict, got %v", err)
	}

	// The conflict rolls the publish back, so the transfer stays draft and can
	// be retried with a fresh code.
	reloaded, err := store.TransferByID(ctx, transfer.ID)
	if err != nil {
		t.Fatalf("reload transfer: %v", err)
	}
	if reloaded.Status != app.TransferStatusDraft || reloaded.ShareID != "" {
		t.Fatalf("expected the failed publish to leave the transfer unpublished, got %#v", reloaded)
	}

	fresh := colliding
	fresh.ID = id + "_share_retry"
	fresh.TokenHash = id + "_hash_retry"
	fresh.Token = id + "_token_retry"
	fresh.PickupCode = "EF2345"
	published, err := NewTransferStore(second).PublishTransfer(ctx, transfer.ID, fresh, now)
	if err != nil {
		t.Fatalf("publish with a fresh code: %v", err)
	}
	if published.Status != app.TransferStatusPublished || published.ShareID != fresh.ID {
		t.Fatalf("unexpected published transfer: %#v", published)
	}
	resolved, err := NewShareStore(second).ShareByPickupCode(ctx, fresh.PickupCode)
	if err != nil {
		t.Fatalf("resolve published code: %v", err)
	}
	if resolved.ID != fresh.ID {
		t.Fatalf("expected the published code to resolve share %q, got %q", fresh.ID, resolved.ID)
	}
}
