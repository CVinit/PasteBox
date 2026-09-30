package postgres

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

// isBurnTerminalError reports the service's terminal answer for a destroyed
// send, so the test asserts the code the API hands out rather than a status.
func isBurnTerminalError(err error) bool {
	var appErr *app.Error
	return errors.As(err, &appErr) && appErr.Code == "transfer_destroyed"
}

type cleanBurnScanner struct{}

func (cleanBurnScanner) Scan(context.Context, string, string, []byte) (app.ScanResult, error) {
	return app.ScanResult{Status: "clean"}, nil
}

// publishServiceBurnTransfer creates, fills and publishes a burning send through
// the service, so the test drives the same calls the API handlers do.
func publishServiceBurnTransfer(t *testing.T, ctx context.Context, service *app.Service, userID string, key string, quota int, expiresInSeconds int64) (app.TransferView, string) {
	t.Helper()
	created, err := service.CreateTransferWithContext(ctx, userID, app.TransferInput{
		IdempotencyKey:   key,
		ExpiresInSeconds: expiresInSeconds,
		ClaimQuota:       quota,
		BurnAfterReading: true,
		Items: []app.TransferItemInput{
			{ItemID: "itm-1", FileName: "one.txt", ContentType: "text/plain", Size: 3},
		},
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	preflight, err := service.PreflightTransferItemUploadWithContext(ctx, userID, created.ID, "itm-1")
	if err != nil {
		t.Fatalf("preflight upload: %v", err)
	}
	upload, err := app.PrepareAttachmentUploadWithLimit("one.txt", "text/plain", bytes.NewReader([]byte("one")), preflight.MaxBytes)
	if err != nil {
		t.Fatalf("prepare upload: %v", err)
	}
	defer upload.Close()
	_, attachment, err := service.AddPreparedTransferItemWithContext(ctx, preflight, upload)
	if err != nil {
		t.Fatalf("upload item: %v", err)
	}
	if err := service.RunAttachmentScanWithContext(ctx, cleanBurnScanner{}, attachment.ID); err != nil {
		t.Fatalf("scan item: %v", err)
	}
	published, err := service.PublishTransferWithContext(ctx, userID, created.ID)
	if err != nil {
		t.Fatalf("publish transfer: %v", err)
	}
	if published.Share == nil || published.Share.Token == "" {
		t.Fatalf("expected a published share, got %#v", published)
	}
	return published, attachment.ID
}

// TestPostgresBackedServiceBurnsItsOwnContent drives burn-after-reading through
// the service on top of the real stores. That is the wiring production uses, and
// it is where the atomic destroy, the store-backed live-claim count, the sweep
// query and the existing cleanup path have to agree with each other.
func TestPostgresBackedServiceBurnsItsOwnContent(t *testing.T) {
	dsn := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL integration database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := ApplyMigrations(ctx, dsn); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	email := "burn-service@example.com"
	cleanupServiceIntegrationRows(ctx, t, pool, email, "")
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		cleanupServiceIntegrationRows(cleanupCtx, t, pool, email, "")
	})

	cfg := config.FromEnv()
	cfg.PublicURL = "https://pastebox.example.test"
	cfg.DevAuthTokens = true
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	service := newPostgresBackedService(t, ctx, pool, cfg)

	start, err := service.StartRegistrationEmailVerification(ctx, email)
	if err != nil {
		t.Fatalf("start registration verification: %v", err)
	}
	auth, err := service.Register(ctx, app.RegisterInput{
		Email:                 email,
		Password:              "password123",
		DisplayName:           "Burn Service",
		EmailVerificationCode: start["devToken"],
	})
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	userID := auth.User.ID

	published, attachmentID := publishServiceBurnTransfer(t, ctx, service, userID, "burn-service-1", 1, 3600)
	if !published.BurnAfterReading {
		t.Fatalf("expected the burn switch to be stored, got %#v", published)
	}
	token := published.Share.Token

	// Opening the page spends nothing and destroys nothing.
	if _, _, access, err := service.AccessShareWithTransferContext(ctx, token, "", "", ""); err != nil {
		t.Fatalf("open share: %v", err)
	} else if access == nil || access.ClaimsRemaining != 1 || access.Claimed {
		t.Fatalf("expected an untouched claim state, got %#v", access)
	}

	claim, err := service.ClaimTransferWithContext(ctx, token, "", "", "op-1")
	if err != nil {
		t.Fatalf("claim transfer: %v", err)
	}
	download, err := service.OpenSharedAttachmentWithClaimOrAccessGrantContext(ctx, token, claim.ClaimToken, false, attachmentID, "")
	if err != nil {
		t.Fatalf("open download: %v", err)
	}
	content, err := io.ReadAll(download.Body)
	_ = download.Body.Close()
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if string(content) != "one" {
		t.Fatalf("expected the stored bytes, got %q", content)
	}

	// The last session ending destroys the send through the atomic store path.
	if _, err := service.CompleteTransferClaimWithContext(ctx, token, claim.Claim.ID, claim.ClaimToken, ""); err != nil {
		t.Fatalf("complete claim: %v", err)
	}
	view, err := service.GetTransferWithContext(ctx, userID, published.ID)
	if err != nil {
		t.Fatalf("get transfer: %v", err)
	}
	if view.Status != app.TransferStatusDestroyed || view.DestroyedAt == nil {
		t.Fatalf("expected a destroyed send, got %#v", view)
	}
	if view.DestroyReason != app.TransferDestroyReasonClaimsEnded || view.CleanupStatus != "pending_delete" {
		t.Fatalf("expected the claims reason and a queued cleanup, got %#v", view)
	}
	var jobCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind = 'cleanup' AND target_id = $1`, published.PasteID).Scan(&jobCount); err != nil {
		t.Fatalf("count cleanup jobs: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("expected exactly one queued cleanup job, got %d", jobCount)
	}
	// A destroyed send is refused through the service, not only in the store.
	if _, _, _, err := service.AccessShareWithTransferContext(ctx, token, "", "", ""); !isBurnTerminalError(err) {
		t.Fatalf("expected the destroyed page to be refused, got %v", err)
	}

	// The physical release runs through the existing worker cleanup path.
	if _, err := service.RunCleanupWithContext(ctx, ""); err != nil {
		t.Fatalf("run cleanup: %v", err)
	}
	view, err = service.GetTransferWithContext(ctx, userID, published.ID)
	if err != nil {
		t.Fatalf("get transfer after cleanup: %v", err)
	}
	if view.CleanupStatus != "deleted" {
		t.Fatalf("expected the content to be fully cleaned up, got %q", view.CleanupStatus)
	}
	var attachmentStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM attachments WHERE id = $1`, attachmentID).Scan(&attachmentStatus); err != nil {
		t.Fatalf("load attachment: %v", err)
	}
	if attachmentStatus != "deleted" {
		t.Fatalf("expected the attachment row to be released, got %q", attachmentStatus)
	}

	// A lifetime that ran out with slots left is destroyed by the sweep alone.
	expiring, _ := publishServiceBurnTransfer(t, ctx, service, userID, "burn-service-2", 3, 1)
	deadline := time.Now().Add(15 * time.Second)
	destroyed := 0
	for time.Now().Before(deadline) {
		count, err := service.RunTransferBurnSweepWithContext(ctx)
		if err != nil {
			t.Fatalf("burn sweep: %v", err)
		}
		destroyed += count
		if destroyed > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if destroyed == 0 {
		t.Fatalf("expected the sweep to destroy the expired send")
	}
	view, err = service.GetTransferWithContext(ctx, userID, expiring.ID)
	if err != nil {
		t.Fatalf("get expired transfer: %v", err)
	}
	if view.Status != app.TransferStatusDestroyed || view.DestroyReason != app.TransferDestroyReasonExpired {
		t.Fatalf("expected the lifetime to end the send, got %#v", view)
	}
	if view.CleanupStatus != "pending_delete" {
		t.Fatalf("expected the swept content to be queued for release, got %q", view.CleanupStatus)
	}
}
