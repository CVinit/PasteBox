package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

func TestAccountStatusBoundsHistoricalRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, _ := pickupTestPools(t, ctx)
	user := seedStatusUser(t, ctx, pool, "pages")
	now := time.Now().UTC()
	for i := range 123 {
		id := fmt.Sprintf("%s_%03d", user.ID, i)
		paste := app.Paste{ID: id, UserID: user.ID, Status: "active", ScanStatus: "clean", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
		if err := NewPasteStore(pool).CreatePaste(ctx, paste); err != nil {
			t.Fatal(err)
		}
		transfer := app.Transfer{ID: id, UserID: user.ID, PasteID: id, Status: app.TransferStatusDraft, ClaimQuota: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: paste.ExpiresAt}
		if err := NewTransferStore(pool).CreateTransfer(ctx, transfer); err != nil {
			t.Fatal(err)
		}
	}
	status, err := NewAccountStatusStore(pool).AccountStatus(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Transfers) > 50 || len(status.Pastes) > 51 {
		t.Fatalf("unbounded status: transfers=%d pastes=%d", len(status.Transfers), len(status.Pastes))
	}
	seen := map[string]bool{}
	for {
		for _, row := range status.Transfers {
			if seen[row.Transfer.ID] {
				t.Fatalf("duplicate page row: %s", row.Transfer.ID)
			}
			seen[row.Transfer.ID] = true
		}
		if status.NextTransferCursor == "" {
			break
		}
		status, err = NewAccountStatusStore(pool).AccountStatus(ctx, user.ID, app.AccountStatusOptions{BeforeTransferID: status.NextTransferCursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(status.Transfers) > app.AccountStatusPageSize {
			t.Fatal("history page exceeded bound")
		}
	}
	if len(seen) != 123 {
		t.Fatalf("history lost records: %d", len(seen))
	}
	oldID := user.ID + "_000"
	status, err = NewAccountStatusStore(pool).AccountStatus(ctx, user.ID, app.AccountStatusOptions{ActivePasteID: oldID})
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Pastes) != app.AccountStatusPageSize+1 {
		t.Fatal("old editor marker was dropped")
	}
	// Editing the oldest record brings its marker into the recent batch even
	// if it was not the watched editor. A different account never gets it.
	if _, err := pool.Exec(ctx, "UPDATE pastes SET updated_at=$2 WHERE id=$1", oldID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	status, err = NewAccountStatusStore(pool).AccountStatus(ctx, user.ID)
	if err != nil || status.Pastes[0].ID != oldID {
		t.Fatalf("old edit missing: %v", err)
	}
	stranger := seedStatusUser(t, ctx, pool, "foreign")
	foreign, err := NewAccountStatusStore(pool).AccountStatus(ctx, stranger.ID, app.AccountStatusOptions{ActivePasteID: oldID, BeforeTransferID: oldID})
	if err != nil || len(foreign.Transfers) != 0 || len(foreign.Pastes) != 0 {
		t.Fatalf("foreign markers leaked: %#v %v", foreign, err)
	}

}

// A compact status read must not fall back to one claim query for each row.
type noStatusClaimQueries struct {
	*TransferStore
	t *testing.T
}

func (s noStatusClaimQueries) CountLiveTransferClaims(context.Context, string, time.Time) (int, error) {
	s.t.Error("account snapshot issued a per-transfer claim query")
	return 0, nil
}

func TestAccountStatusUsesBatchedLiveClaims(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, _ := pickupTestPools(t, ctx)
	now := time.Now().UTC()
	seed := seedBurnTransfer(t, ctx, pool, "batch-claims", 1, true, now.Add(time.Hour))
	if _, err := pool.Exec(ctx, "UPDATE transfers SET claimed_count=1 WHERE id=$1", seed.transfer.ID); err != nil {
		t.Fatal(err)
	}
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword = "", ""
	svc := newPostgresBackedService(t, ctx, pool, cfg, func(stores *app.Stores) { stores.Content.Transfers = noStatusClaimQueries{NewTransferStore(pool), t} })
	status, err := svc.AccountStatusWithContext(ctx, seed.transfer.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Transfers) != 1 || status.Transfers[0].State != app.TransferStatusStateDestroyed {
		t.Fatalf("wrong terminal state: %#v", status)
	}
}
