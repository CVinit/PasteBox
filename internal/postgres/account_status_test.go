package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

// seedStatusUser creates the account a status test reads, so the same rows are
// visible to every connection and to every service instance built on the pool.
func seedStatusUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) app.User {
	t.Helper()
	now := time.Now().UTC()
	id := fmt.Sprintf("status_%s_%d", suffix, now.UnixNano())
	user := app.User{
		ID: id, Email: id + "@example.com", DisplayName: "Status", Language: "en",
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
	return user
}

// TestAccountStatusStoreReportsCommittedSendState pins the snapshot query to
// the committed rows: the send, its share, its declared file count and the
// state of its backing record, with no attachment rows and no content.
func TestAccountStatusStoreReportsCommittedSendState(t *testing.T) {
	dsn := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL integration database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := ApplyMigrations(ctx, dsn); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	pool, second := newClaimTestPools(t, ctx, dsn)

	now := time.Now().UTC()
	seed := seedBurnTransfer(t, ctx, pool, "status-read", 2, false, now.Add(time.Hour))
	item := app.TransferItem{
		TransferID: seed.transfer.ID, ItemID: "itm-1", FileName: "one.txt", ContentType: "text/plain",
		Size: 3, AttachmentID: seed.attachment.ID, Status: app.TransferItemUploaded, CreatedAt: now, UpdatedAt: now,
	}
	if err := NewTransferStore(pool).CreateTransferItem(ctx, item); err != nil {
		t.Fatalf("create transfer item: %v", err)
	}

	status, err := NewAccountStatusStore(pool).AccountStatus(ctx, seed.transfer.UserID)
	if err != nil {
		t.Fatalf("account status: %v", err)
	}
	if len(status.Transfers) != 1 {
		t.Fatalf("expected one send, got %#v", status.Transfers)
	}
	record := status.Transfers[0]
	if record.Transfer.ID != seed.transfer.ID || record.Transfer.Status != app.TransferStatusPublished {
		t.Fatalf("unexpected send row %#v", record.Transfer)
	}
	if record.Transfer.ClaimQuota != 2 || record.Transfer.ClaimedCount != 0 {
		t.Fatalf("expected the stored quota, got %#v", record.Transfer)
	}
	if record.Title != seed.paste.Title || record.CleanupStatus != "active" || record.ItemCount != 1 {
		t.Fatalf("expected the record title, cleanup state and file count, got %#v", record)
	}
	if record.ShareToken != seed.share.Token || record.PickupCode != seed.share.PickupCode {
		t.Fatalf("expected the sender's link and pickup code, got %#v", record)
	}
	if len(status.Pastes) != 1 || status.Pastes[0].ID != seed.paste.ID || status.Pastes[0].Status != "active" {
		t.Fatalf("expected one record marker, got %#v", status.Pastes)
	}

	// A second connection reads the same committed state, so no instance can
	// claim a different answer.
	other, err := NewAccountStatusStore(second).AccountStatus(ctx, seed.transfer.UserID)
	if err != nil {
		t.Fatalf("account status on a second connection: %v", err)
	}
	if len(other.Transfers) != 1 || other.Transfers[0].Transfer.ID != record.Transfer.ID || other.Transfers[0].ItemCount != 1 {
		t.Fatalf("expected both connections to agree, got %#v and %#v", record, other.Transfers)
	}

	// The snapshot is scoped to the account that owns the rows.
	stranger := seedStatusUser(t, ctx, pool, "status-read-stranger")
	elsewhere, err := NewAccountStatusStore(pool).AccountStatus(ctx, stranger.ID)
	if err != nil {
		t.Fatalf("account status of an unrelated account: %v", err)
	}
	if len(elsewhere.Transfers) != 0 || len(elsewhere.Pastes) != 0 {
		t.Fatalf("expected an unrelated account to see nothing, got %#v", elsewhere)
	}
}

// TestAccountStatusIsConsistentAcrossInstances is the cross-instance proof: two
// service instances with their own caches and their own object stores report
// the same send state, because every status read goes back to the database and
// no in-memory broadcast is treated as the truth.
func TestAccountStatusIsConsistentAcrossInstances(t *testing.T) {
	dsn := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL integration database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := ApplyMigrations(ctx, dsn); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	pool := burnTestPool(t, ctx, dsn)

	cfg := config.FromEnv()
	cfg.PublicURL = "https://pastebox.example.test"
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	user := seedStatusUser(t, ctx, pool, "status-instances")

	// The sender publishes a text send through the first instance.
	first := newPostgresBackedService(t, ctx, pool, cfg)
	created, err := first.CreateTransferWithContext(ctx, user.ID, app.TransferInput{
		IdempotencyKey:   "status-instances-key",
		ExpiresInSeconds: 3600,
		ClaimQuota:       2,
		Title:            "note",
		Text:             "body",
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	published, err := first.PublishTransferWithContext(ctx, user.ID, created.ID)
	if err != nil {
		t.Fatalf("publish transfer: %v", err)
	}
	if published.Share == nil || published.Share.Token == "" {
		t.Fatalf("expected a published share, got %#v", published)
	}

	// The second instance is a separate process image: its own caches were
	// loaded before the send existed.
	second := newPostgresBackedService(t, ctx, pool, cfg)
	snapshot, err := second.AccountStatusWithContext(ctx, user.ID)
	if err != nil {
		t.Fatalf("account status on the second instance: %v", err)
	}
	if len(snapshot.Transfers) != 1 {
		t.Fatalf("expected the second instance to see the send, got %#v", snapshot.Transfers)
	}
	if snapshot.Transfers[0].State != app.TransferStatusStateClaimable || snapshot.Transfers[0].ClaimsRemaining != 2 {
		t.Fatalf("unexpected cross-instance snapshot %#v", snapshot.Transfers[0])
	}
	if snapshot.Transfers[0].ShareURL != "https://pastebox.example.test/s/"+published.Share.Token {
		t.Fatalf("expected the configured share address, got %q", snapshot.Transfers[0].ShareURL)
	}

	// A recipient claims through the first instance; the second one reports the
	// count on its next read.
	claim, err := first.ClaimTransferWithContext(ctx, published.Share.Token, "", "", "op-status-instances")
	if err != nil {
		t.Fatalf("claim transfer: %v", err)
	}
	snapshot, err = second.AccountStatusWithContext(ctx, user.ID)
	if err != nil {
		t.Fatalf("account status after the claim: %v", err)
	}
	if snapshot.Transfers[0].ClaimedCount != 1 || snapshot.Transfers[0].ClaimsRemaining != 1 {
		t.Fatalf("expected the claim to reach the second instance, got %#v", snapshot.Transfers[0])
	}

	// The credential-scoped status of the same send agrees, and reports the
	// live claim without spending anything.
	status, err := second.ShareStatusWithContext(ctx, published.Share.Token, claim.ClaimToken, true)
	if err != nil {
		t.Fatalf("share status on the second instance: %v", err)
	}
	if !status.Claimed || status.ClaimID != claim.Claim.ID || status.ClaimedCount != 1 {
		t.Fatalf("unexpected credential-scoped status %#v", status)
	}

	// A burn send ends with its last session; the terminal state, its reason
	// and the cleanup boundary are what both instances report, and what the
	// database actually stores.
	burning, err := first.CreateTransferWithContext(ctx, user.ID, app.TransferInput{
		IdempotencyKey:   "status-instances-burn",
		ExpiresInSeconds: 3600,
		ClaimQuota:       1,
		BurnAfterReading: true,
		Title:            "burning",
		Text:             "body",
	})
	if err != nil {
		t.Fatalf("create burning transfer: %v", err)
	}
	publishedBurn, err := first.PublishTransferWithContext(ctx, user.ID, burning.ID)
	if err != nil {
		t.Fatalf("publish burning transfer: %v", err)
	}
	burnClaim, err := first.ClaimTransferWithContext(ctx, publishedBurn.Share.Token, "", "", "op-status-instances-burn")
	if err != nil {
		t.Fatalf("claim burning transfer: %v", err)
	}
	if _, err := first.CompleteTransferClaimWithContext(ctx, publishedBurn.Share.Token, burnClaim.Claim.ID, burnClaim.ClaimToken, ""); err != nil {
		t.Fatalf("complete burning claim: %v", err)
	}

	snapshot, err = second.AccountStatusWithContext(ctx, user.ID)
	if err != nil {
		t.Fatalf("account status after destruction: %v", err)
	}
	var destroyed app.TransferRecordView
	for _, record := range snapshot.Transfers {
		if record.TransferID == burning.ID {
			destroyed = record
		}
	}
	if destroyed.State != app.TransferStatusStateDestroyed {
		t.Fatalf("expected the destroyed send in the snapshot, got %#v", destroyed)
	}
	if destroyed.DestroyReason != app.TransferDestroyReasonClaimsEnded || destroyed.DestroyedAt == nil {
		t.Fatalf("expected the recorded reason and time, got %#v", destroyed)
	}
	if destroyed.CleanupStatus != "pending_delete" {
		t.Fatalf("expected the cleanup boundary, got %q", destroyed.CleanupStatus)
	}

	var storedStatus string
	var storedReason string
	if err := pool.QueryRow(ctx, `SELECT status, destroy_reason FROM transfers WHERE id = $1`, burning.ID).Scan(&storedStatus, &storedReason); err != nil {
		t.Fatalf("read stored transfer: %v", err)
	}
	if storedStatus != app.TransferStatusDestroyed || storedReason != app.TransferDestroyReasonClaimsEnded {
		t.Fatalf("expected the database to hold the terminal state, got %q/%q", storedStatus, storedReason)
	}
	var pasteStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM pastes WHERE id = $1`, burning.PasteID).Scan(&pasteStatus); err != nil {
		t.Fatalf("read stored record: %v", err)
	}
	if pasteStatus != "pending_delete" {
		t.Fatalf("expected the backing record to be marked for cleanup, got %q", pasteStatus)
	}
}
