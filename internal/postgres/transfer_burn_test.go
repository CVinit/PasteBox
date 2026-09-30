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

type burnSeed struct {
	transfer   app.Transfer
	share      app.Share
	paste      app.Paste
	attachment app.Attachment
}

// seedBurnTransfer creates a published transfer with a share, one active
// attachment and an explicit burn switch, so the destroy tests exercise the
// real rows a destroyed send has to leave behind.
func seedBurnTransfer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string, quota int, burn bool, expiresAt time.Time) burnSeed {
	t.Helper()
	now := time.Now().UTC()
	id := fmt.Sprintf("burn_%s_%d", suffix, now.UnixNano())
	user := app.User{
		ID: id, Email: id + "@example.com", DisplayName: "Burn", Language: "en",
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
		ID: id + "_paste", UserID: user.ID, Title: "note", Text: "content", Status: "active",
		ScanStatus: "clean", ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now,
	}
	if err := NewPasteStore(pool).CreatePaste(ctx, paste); err != nil {
		t.Fatalf("create paste: %v", err)
	}
	attachment := app.Attachment{
		ID: id + "_attachment", UserID: user.ID, PasteID: paste.ID, FileName: "one.txt",
		ContentType: "text/plain", Size: 3, SHA256: id + "_digest", ObjectKey: id + "_object",
		Status: "active", ScanStatus: "clean", CreatedAt: now,
	}
	if err := NewAttachmentStore(pool).CreateAttachment(ctx, attachment); err != nil {
		t.Fatalf("create attachment: %v", err)
	}

	store := NewTransferStore(pool)
	transfer := app.Transfer{
		ID: id + "_transfer", UserID: user.ID, PasteID: paste.ID, Status: app.TransferStatusDraft,
		IdempotencyKey: "burn-key-" + suffix, ClaimQuota: quota, BurnAfterReading: burn,
		ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateTransfer(ctx, transfer); err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	share := app.Share{
		ID: id + "_share", PasteID: paste.ID, UserID: user.ID,
		Token: id + "_token", TokenHash: id + "_token_hash",
		ExpiresAt: expiresAt, CreatedAt: now,
	}
	published, err := store.PublishTransfer(ctx, transfer.ID, share, now, true)
	if err != nil {
		t.Fatalf("publish transfer: %v", err)
	}
	return burnSeed{transfer: published, share: share, paste: paste, attachment: attachment}
}

func seedBurnClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed burnSeed, suffix string, status string, expiresAt time.Time) app.TransferClaim {
	t.Helper()
	claim := app.TransferClaim{
		ID:          seed.transfer.ID + "_claim_" + suffix,
		TransferID:  seed.transfer.ID,
		ShareID:     seed.share.ID,
		Kind:        app.TransferClaimKindFile,
		OperationID: "op-" + suffix,
		Token:       seed.transfer.ID + "_claim_token_" + suffix,
		TokenHash:   seed.transfer.ID + "_claim_hash_" + suffix,
		Status:      status,
		ExpiresAt:   expiresAt,
		CreatedAt:   time.Now().UTC(),
	}
	allocated, _, err := NewTransferStore(pool).AllocateTransferClaim(ctx, claim)
	if err != nil {
		t.Fatalf("allocate claim: %v", err)
	}
	if status == app.TransferClaimStatusCompleted {
		if _, err := NewTransferStore(pool).CompleteTransferClaim(ctx, allocated.ID, time.Now().UTC()); err != nil {
			t.Fatalf("complete claim: %v", err)
		}
		allocated.Status = app.TransferClaimStatusCompleted
	}
	return allocated
}

func burnTestPool(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestTransferStoreDestroyCommitsTheWholeTerminalState is the real-database
// proof that destroying a send is one step: the transfer stops authorizing
// access, its share is revoked, its content is marked for deletion and the
// cleanup job is queued — or none of it happened.
func TestTransferStoreDestroyCommitsTheWholeTerminalState(t *testing.T) {
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

	now := time.Now().UTC()
	seed := seedBurnTransfer(t, ctx, pool, "atomic", 1, true, now.Add(time.Hour))
	store := NewTransferStore(pool)

	destroyed, changed, err := store.DestroyTransfer(ctx, seed.transfer.ID, seed.transfer.ID+"_job", now, app.TransferDestroyReasonClaimsEnded)
	if err != nil {
		t.Fatalf("destroy transfer: %v", err)
	}
	if !changed {
		t.Fatalf("expected the first destroy to change the send")
	}
	if destroyed.Status != app.TransferStatusDestroyed || destroyed.DestroyedAt == nil {
		t.Fatalf("expected a destroyed transfer, got %#v", destroyed)
	}
	if destroyed.DestroyReason != app.TransferDestroyReasonClaimsEnded {
		t.Fatalf("expected the destroy reason to be stored, got %q", destroyed.DestroyReason)
	}

	var revokedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT revoked_at FROM shares WHERE id = $1`, seed.share.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("load share: %v", err)
	}
	if revokedAt == nil {
		t.Fatalf("expected the share to be revoked with the send")
	}
	var pasteStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM pastes WHERE id = $1`, seed.paste.ID).Scan(&pasteStatus); err != nil {
		t.Fatalf("load paste: %v", err)
	}
	if pasteStatus != "pending_delete" {
		t.Fatalf("expected the content to be marked for deletion, got %q", pasteStatus)
	}
	var attachmentStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM attachments WHERE id = $1`, seed.attachment.ID).Scan(&attachmentStatus); err != nil {
		t.Fatalf("load attachment: %v", err)
	}
	if attachmentStatus != "pending_delete" {
		t.Fatalf("expected the attachment to be marked for deletion, got %q", attachmentStatus)
	}
	var jobCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind = 'cleanup' AND target_id = $1`, seed.paste.ID).Scan(&jobCount); err != nil {
		t.Fatalf("count cleanup jobs: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("expected exactly one queued cleanup job, got %d", jobCount)
	}

	// A retry is a no-op: no second cleanup job, and the recorded terminal
	// state is the one that was committed first.
	retry, changed, err := store.DestroyTransfer(ctx, seed.transfer.ID, seed.transfer.ID+"_job_retry", now.Add(time.Minute), app.TransferDestroyReasonExpired)
	if err != nil {
		t.Fatalf("destroy transfer retry: %v", err)
	}
	if changed {
		t.Fatalf("expected a destroy retry to change nothing")
	}
	if retry.DestroyReason != app.TransferDestroyReasonClaimsEnded || !retry.DestroyedAt.Equal(*destroyed.DestroyedAt) {
		t.Fatalf("expected the first terminal state to stand, got %#v", retry)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind = 'cleanup' AND target_id = $1`, seed.paste.ID).Scan(&jobCount); err != nil {
		t.Fatalf("count cleanup jobs after retry: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("expected the retry to queue no second cleanup job, got %d", jobCount)
	}

	// Only a published send can be destroyed; a draft is a programming error.
	draft := app.Transfer{
		ID: seed.transfer.ID + "_draft", UserID: seed.transfer.UserID, PasteID: seed.paste.ID,
		Status: app.TransferStatusDraft, ClaimQuota: 1, ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateTransfer(ctx, draft); err != nil {
		t.Fatalf("create draft transfer: %v", err)
	}
	if _, _, err := store.DestroyTransfer(ctx, draft.ID, draft.ID+"_job", now, app.TransferDestroyReasonExpired); !errors.Is(err, app.ErrStoreConflict) {
		t.Fatalf("expected a draft to be refused, got %v", err)
	}
}

// TestTransferStoreBurnSweepQueryTracksClaimsAndExpiry proves the sweep finds
// exactly the sends that have to go: fully claimed with no live session, or past
// their lifetime. A send with a slot left is not touched.
func TestTransferStoreBurnSweepQueryTracksClaimsAndExpiry(t *testing.T) {
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
	store := NewTransferStore(pool)

	now := time.Now().UTC()
	live := seedBurnTransfer(t, ctx, pool, "sweep_live", 2, true, now.Add(time.Hour))
	seedBurnClaim(t, ctx, pool, live, "live", app.TransferClaimStatusActive, now.Add(30*time.Minute))

	ended := seedBurnTransfer(t, ctx, pool, "sweep_ended", 1, true, now.Add(time.Hour))
	seedBurnClaim(t, ctx, pool, ended, "done", app.TransferClaimStatusCompleted, now.Add(30*time.Minute))

	abandoned := seedBurnTransfer(t, ctx, pool, "sweep_abandoned", 1, true, now.Add(time.Hour))
	seedBurnClaim(t, ctx, pool, abandoned, "stale", app.TransferClaimStatusActive, now.Add(-time.Minute))

	expiring := seedBurnTransfer(t, ctx, pool, "sweep_expiring", 3, true, now.Add(-time.Minute))

	ordinary := seedBurnTransfer(t, ctx, pool, "sweep_ordinary", 1, false, now.Add(time.Hour))
	seedBurnClaim(t, ctx, pool, ordinary, "done", app.TransferClaimStatusCompleted, now.Add(30*time.Minute))

	due, err := store.ListBurnableTransfers(ctx, now, 100)
	if err != nil {
		t.Fatalf("list burnable transfers: %v", err)
	}
	dueIDs := map[string]bool{}
	for _, transfer := range due {
		dueIDs[transfer.ID] = true
	}
	for _, expected := range []string{ended.transfer.ID, abandoned.transfer.ID, expiring.transfer.ID} {
		if !dueIDs[expected] {
			t.Fatalf("expected %s to be due, got %#v", expected, dueIDs)
		}
	}
	for _, unexpected := range []string{live.transfer.ID, ordinary.transfer.ID} {
		if dueIDs[unexpected] {
			t.Fatalf("expected %s not to be due, got %#v", unexpected, dueIDs)
		}
	}

	// The live-claim count is what the service uses to decide whether the last
	// session has ended, and it must ignore completed and expired sessions.
	cases := []struct {
		name     string
		seed     burnSeed
		expected int
	}{
		{name: "live session", seed: live, expected: 1},
		{name: "completed session", seed: ended, expected: 0},
		{name: "expired session", seed: abandoned, expected: 0},
	}
	for _, tc := range cases {
		got, err := store.CountLiveTransferClaims(ctx, tc.seed.transfer.ID, now)
		if err != nil {
			t.Fatalf("count live claims for %s: %v", tc.name, err)
		}
		if got != tc.expected {
			t.Fatalf("expected %d live claims for %s, got %d", tc.expected, tc.name, got)
		}
	}

	// A second store instance sees the same terminal state, so a destroy
	// survives a restart.
	second := burnTestPool(t, ctx, dsn)
	if _, changed, err := NewTransferStore(second).DestroyTransfer(ctx, ended.transfer.ID, ended.transfer.ID+"_job", now, app.TransferDestroyReasonClaimsEnded); err != nil || !changed {
		t.Fatalf("expected the destroy to go through a second connection, got changed=%v err=%v", changed, err)
	}
	reloaded, err := NewTransferStore(second).TransferByID(ctx, ended.transfer.ID)
	if err != nil {
		t.Fatalf("reload destroyed transfer: %v", err)
	}
	if reloaded.Status != app.TransferStatusDestroyed || reloaded.DestroyedAt == nil {
		t.Fatalf("expected the terminal state to be visible from another connection, got %#v", reloaded)
	}
	if due, err := store.ListBurnableTransfers(ctx, now, 100); err != nil {
		t.Fatalf("list burnable transfers after destroy: %v", err)
	} else {
		for _, transfer := range due {
			if transfer.ID == ended.transfer.ID {
				t.Fatalf("expected a destroyed send to leave the sweep set")
			}
		}
	}
}

// TestTransferStoreDestroyRacesClaimsSafely proves the destroy and the claim
// serialize on the transfer row: a claim either lands while the send is still
// published, or is refused once the destroy committed, and the send can only
// ever be destroyed once with one queued cleanup.
func TestTransferStoreDestroyRacesClaimsSafely(t *testing.T) {
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
	const quota = 5
	const attempts = 8
	seed := seedBurnTransfer(t, ctx, pool, "race", quota, true, now.Add(time.Hour))

	claimStore := NewTransferStore(second)
	destroyStore := NewTransferStore(pool)
	created := make([]bool, attempts)
	errs := make([]error, attempts)
	destroyed := make(chan bool, 1)
	destroyErr := make(chan error, 1)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			claim := app.TransferClaim{
				ID:          fmt.Sprintf("%s_race_%d", seed.transfer.ID, i),
				TransferID:  seed.transfer.ID,
				ShareID:     seed.share.ID,
				Kind:        app.TransferClaimKindFile,
				OperationID: fmt.Sprintf("race-op-%d", i),
				Token:       fmt.Sprintf("race-token-%d", i),
				TokenHash:   fmt.Sprintf("race-hash-%d", i),
				Status:      app.TransferClaimStatusActive,
				ExpiresAt:   now.Add(30 * time.Minute),
				CreatedAt:   now,
			}
			_, wasCreated, err := claimStore.AllocateTransferClaim(ctx, claim)
			created[i], errs[i] = wasCreated, err
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_, changed, err := destroyStore.DestroyTransfer(ctx, seed.transfer.ID, seed.transfer.ID+"_race_job", now, app.TransferDestroyReasonClaimsEnded)
		destroyed <- changed
		destroyErr <- err
	}()
	close(start)
	wg.Wait()

	if err := <-destroyErr; err != nil {
		t.Fatalf("destroy transfer: %v", err)
	}
	if !<-destroyed {
		t.Fatalf("expected the destroy to change the send")
	}
	spent := 0
	for i := range created {
		if created[i] {
			spent++
			continue
		}
		if !errors.Is(errs[i], app.ErrStoreConflict) {
			t.Fatalf("expected a refused claim to be a store conflict, got %v", errs[i])
		}
	}
	reloaded, err := destroyStore.TransferByID(ctx, seed.transfer.ID)
	if err != nil {
		t.Fatalf("reload transfer: %v", err)
	}
	if reloaded.Status != app.TransferStatusDestroyed {
		t.Fatalf("expected the send to end destroyed, got %#v", reloaded)
	}
	if reloaded.ClaimedCount != spent {
		t.Fatalf("expected the spent slots (%d) to match the created claims (%d)", reloaded.ClaimedCount, spent)
	}
	if reloaded.ClaimedCount > quota {
		t.Fatalf("expected the quota (%d) to hold, got %d spent", quota, reloaded.ClaimedCount)
	}
	// One destroy, one cleanup: the race must not queue a second release.
	var jobCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind = 'cleanup' AND target_id = $1`, seed.paste.ID).Scan(&jobCount); err != nil {
		t.Fatalf("count cleanup jobs: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("expected exactly one queued cleanup job, got %d", jobCount)
	}
	// Nothing can claim a destroyed send afterwards.
	if _, _, err := claimStore.AllocateTransferClaim(ctx, app.TransferClaim{
		ID: seed.transfer.ID + "_race_late", TransferID: seed.transfer.ID, ShareID: seed.share.ID,
		Kind: app.TransferClaimKindFile, OperationID: "race-op-late",
		Token: "race-token-late", TokenHash: "race-hash-late",
		Status: app.TransferClaimStatusActive, ExpiresAt: now.Add(30 * time.Minute), CreatedAt: now,
	}); !errors.Is(err, app.ErrStoreConflict) {
		t.Fatalf("expected a claim after the destroy to be refused, got %v", err)
	}
}
