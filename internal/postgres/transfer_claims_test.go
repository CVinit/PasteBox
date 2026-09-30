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

// seedPublishedTransfer creates a published transfer with a share and the given
// claim quota, so the claim tests exercise the real row lock and counter.
func seedPublishedTransfer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string, quota int) (app.Transfer, app.Share) {
	t.Helper()
	now := time.Now().UTC()
	id := fmt.Sprintf("claim_%s_%d", suffix, now.UnixNano())
	user := app.User{
		ID: id, Email: id + "@example.com", DisplayName: "Claim", Language: "en",
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
		ScanStatus: "clean", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	if err := NewPasteStore(pool).CreatePaste(ctx, paste); err != nil {
		t.Fatalf("create paste: %v", err)
	}
	store := NewTransferStore(pool)
	transfer := app.Transfer{
		ID: id + "_transfer", UserID: user.ID, PasteID: paste.ID, Status: app.TransferStatusDraft,
		IdempotencyKey: "claim-key-" + suffix, ClaimQuota: quota,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateTransfer(ctx, transfer); err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	share := app.Share{
		ID: id + "_share", PasteID: paste.ID, UserID: user.ID,
		Token: id + "_token", TokenHash: id + "_token_hash",
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	published, err := store.PublishTransfer(ctx, transfer.ID, share, now, true)
	if err != nil {
		t.Fatalf("publish transfer: %v", err)
	}
	return published, share
}

func newClaimTestPools(t *testing.T, ctx context.Context, dsn string) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
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

// TestTransferStoreClaimQuotaIsAtomicAcrossConnections is the real-database
// proof for the claim quota: many connections claim at once and exactly the
// granted number of slots are spent, no matter how the requests interleave.
func TestTransferStoreClaimQuotaIsAtomicAcrossConnections(t *testing.T) {
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

	const quota = 3
	const attempts = 12
	published, share := seedPublishedTransfer(t, ctx, pool, "quota", quota)
	stores := []*TransferStore{NewTransferStore(pool), NewTransferStore(second)}

	now := time.Now().UTC()
	errs := make([]error, attempts)
	created := make([]bool, attempts)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			claim := app.TransferClaim{
				ID:          fmt.Sprintf("%s_claim_%d", published.ID, i),
				TransferID:  published.ID,
				ShareID:     share.ID,
				Kind:        app.TransferClaimKindFile,
				OperationID: fmt.Sprintf("op-%d", i),
				Token:       fmt.Sprintf("token-%d", i),
				TokenHash:   fmt.Sprintf("hash-%s-%d", published.ID, i),
				Status:      app.TransferClaimStatusActive,
				ExpiresAt:   now.Add(app.TransferClaimSessionTTL),
				CreatedAt:   now,
			}
			_, granted, err := stores[i%len(stores)].AllocateTransferClaim(ctx, claim)
			created[i], errs[i] = granted, err
		}(i)
	}
	close(start)
	wg.Wait()

	granted := 0
	for i := range errs {
		switch {
		case errs[i] == nil && created[i]:
			granted++
		case errors.Is(errs[i], app.ErrTransferClaimQuotaExhausted):
		default:
			t.Fatalf("claim %d: unexpected result created=%v err=%v", i, created[i], errs[i])
		}
	}
	if granted != quota {
		t.Fatalf("expected exactly %d granted claims, got %d", quota, granted)
	}

	var claimed int
	if err := second.QueryRow(ctx, `SELECT claimed_count FROM transfers WHERE id = $1`, published.ID).Scan(&claimed); err != nil {
		t.Fatalf("read claimed count: %v", err)
	}
	if claimed != quota {
		t.Fatalf("expected claimed_count %d, got %d", quota, claimed)
	}
	var rows int
	if err := second.QueryRow(ctx, `SELECT count(*) FROM transfer_claims WHERE transfer_id = $1`, published.ID).Scan(&rows); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if rows != quota {
		t.Fatalf("expected %d claim rows, got %d", quota, rows)
	}

	// The share lookup is what decides the claim gate applies, so it must find
	// the transfer from a different connection.
	byShare, err := NewTransferStore(second).TransferByShareID(ctx, share.ID)
	if err != nil || byShare.ID != published.ID {
		t.Fatalf("expected the share lookup to find %s, got %#v err=%v", published.ID, byShare, err)
	}
}

// TestTransferStoreClaimRetryIsIdempotentAcrossConnections covers the retried
// claim: the same operation id arriving on two connections must yield one claim
// and one spent slot.
func TestTransferStoreClaimRetryIsIdempotentAcrossConnections(t *testing.T) {
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

	published, share := seedPublishedTransfer(t, ctx, pool, "retry", 1)
	stores := []*TransferStore{NewTransferStore(pool), NewTransferStore(second)}

	now := time.Now().UTC()
	claim := app.TransferClaim{
		ID: published.ID + "_claim", TransferID: published.ID, ShareID: share.ID,
		Kind: app.TransferClaimKindFile, OperationID: "op-retry",
		Token: "retry-token", TokenHash: "retry-" + published.ID,
		Status: app.TransferClaimStatusActive, ExpiresAt: now.Add(app.TransferClaimSessionTTL), CreatedAt: now,
	}
	results := make([]app.TransferClaim, 2)
	created := make([]bool, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], created[i], errs[i] = stores[i].AllocateTransferClaim(ctx, claim)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("claim retry %d failed: %v", i, errs[i])
		}
	}
	if results[0].ID != results[1].ID {
		t.Fatalf("expected both retries to return one claim, got %s and %s", results[0].ID, results[1].ID)
	}
	if created[0] == created[1] {
		t.Fatalf("expected exactly one allocation to create the claim, got %v and %v", created[0], created[1])
	}

	var claimed int
	if err := second.QueryRow(ctx, `SELECT claimed_count FROM transfers WHERE id = $1`, published.ID).Scan(&claimed); err != nil {
		t.Fatalf("read claimed count: %v", err)
	}
	if claimed != 1 {
		t.Fatalf("expected one spent slot for the retried claim, got %d", claimed)
	}
	var rows int
	if err := second.QueryRow(ctx, `SELECT count(*) FROM transfer_claims WHERE transfer_id = $1`, published.ID).Scan(&rows); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected one claim row for the retried claim, got %d", rows)
	}
}
