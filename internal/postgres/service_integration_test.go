package postgres

import (
	"context"
	"errors"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

func TestServiceWithPostgresStoresPreservesLaunchStateAcrossRestart(t *testing.T) {
	databaseURL := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL to run PostgreSQL service integration test")
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
	t.Cleanup(pool.Close)

	userEmail := "service-persistence-user@example.com"
	adminEmail := "service-persistence-admin@example.com"
	cleanupServiceIntegrationRows(ctx, t, pool, userEmail, adminEmail)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanupServiceIntegrationRows(cleanupCtx, t, pool, userEmail, adminEmail)
	})

	cfg := config.FromEnv()
	cfg.PublicURL = "https://pastebox.example.test"
	cfg.DevAuthTokens = true
	cfg.StripeEnabled = true
	cfg.MailerProvider = "log"
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""

	service := newPostgresBackedService(t, ctx, pool, cfg)
	admin, err := service.SeedAdmin(adminEmail, "password123")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	auth, err := service.Register(ctx, app.RegisterInput{
		Email:       userEmail,
		Password:    "password123",
		DisplayName: "Service Persistence User",
	})
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	if auth.SessionID == "" || auth.DevEmailVerificationToken == "" {
		t.Fatalf("expected session and dev verification token, got %#v", auth)
	}
	if _, err := service.FinishEmailVerification(auth.DevEmailVerificationToken); err != nil {
		t.Fatalf("finish email verification: %v", err)
	}
	created, err := service.CreatePaste(auth.User.ID, app.PasteInput{
		Title:            "Launch state",
		Text:             "hello",
		Tags:             []string{"phase1", "postgres"},
		Pinned:           true,
		ExpiresInSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("create paste: %v", err)
	}
	updatedTitle := "Launch state updated"
	updatedText := "hello durable postgres"
	updated, err := service.UpdatePaste(auth.User.ID, created.ID, app.PastePatch{
		Title: &updatedTitle,
		Text:  &updatedText,
	})
	if err != nil {
		t.Fatalf("update paste: %v", err)
	}
	if updated.Title != updatedTitle || updated.Text != updatedText {
		t.Fatalf("unexpected updated paste: %#v", updated)
	}
	share, err := service.CreateShare(auth.User.ID, created.ID, app.ShareInput{
		Password:         "share-password",
		MaxVisits:        2,
		MaxDownloads:     1,
		ExpiresInSeconds: 1800,
	})
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	if _, _, err := service.AccessShare(share.Token, "share-password", ""); err != nil {
		t.Fatalf("access share before restart: %v", err)
	}
	order, err := service.CreateOrder(auth.User.ID, "stripe", "plus", "monthly")
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := service.MarkOrderPaid(admin.ID, order.ID, "tx-service-persistence", "SUP-456 persisted manual payment correction"); err != nil {
		t.Fatalf("mark order paid: %v", err)
	}
	if _, err := service.Report(auth.User.ID, "share:"+share.Token, "abuse"); err != nil {
		t.Fatalf("create report: %v", err)
	}
	if _, err := service.RequestAccountDeletion(auth.User.ID); err != nil {
		t.Fatalf("request account deletion: %v", err)
	}

	restarted := newPostgresBackedService(t, ctx, pool, cfg)
	userAfterRestart, err := restarted.UserForSession(auth.SessionID)
	if err != nil {
		t.Fatalf("user for persisted session: %v", err)
	}
	if userAfterRestart.Email != userEmail || !userAfterRestart.EmailVerified || userAfterRestart.PlanID != "plus" || userAfterRestart.DeleteRequestedAt == nil {
		t.Fatalf("unexpected restarted user: %#v", userAfterRestart)
	}
	pasteAfterRestart, err := restarted.GetPaste(auth.User.ID, created.ID)
	if err != nil {
		t.Fatalf("get paste after restart: %v", err)
	}
	if pasteAfterRestart.Text != updatedText || pasteAfterRestart.ShareCount != 1 || !slices.Contains(pasteAfterRestart.Tags, "phase1") {
		t.Fatalf("unexpected restarted paste: %#v", pasteAfterRestart)
	}
	pastesAfterRestart, err := restarted.ListPastes(auth.User.ID, app.ListOptions{Query: "durable"})
	if err != nil {
		t.Fatalf("list pastes after restart: %v", err)
	}
	if len(pastesAfterRestart) != 1 || pastesAfterRestart[0].ID != created.ID {
		t.Fatalf("expected updated paste in query result after restart, got %#v", pastesAfterRestart)
	}
	_, shareAfterRestart, err := restarted.AccessShare(share.Token, "share-password", "")
	if err != nil {
		t.Fatalf("access share after restart: %v", err)
	}
	if shareAfterRestart.VisitCount != 2 {
		t.Fatalf("expected share visit count to persist and increment, got %#v", shareAfterRestart)
	}
	quotaAfterRestart, err := restarted.Quota(auth.User.ID)
	if err != nil {
		t.Fatalf("quota after restart: %v", err)
	}
	if quotaAfterRestart.ActivePasteCount != 1 || quotaAfterRestart.DailyUploadBytes != int64(len([]byte("hello"))+len([]byte(" durable postgres"))) {
		t.Fatalf("unexpected quota after restart: %#v", quotaAfterRestart)
	}
	ordersAfterRestart, err := restarted.ListOrders(auth.User.ID)
	if err != nil {
		t.Fatalf("list orders after restart: %v", err)
	}
	if len(ordersAfterRestart) != 1 || ordersAfterRestart[0].Status != "paid" || ordersAfterRestart[0].TxID != "tx-service-persistence" {
		t.Fatalf("expected paid order after restart, got %#v", ordersAfterRestart)
	}
	exportAfterRestart, err := restarted.ExportUser(auth.User.ID)
	if err != nil {
		t.Fatalf("export user after restart: %v", err)
	}
	if got := exportAfterRestart["pastes"].([]app.PasteView); len(got) != 1 || got[0].ID != created.ID {
		t.Fatalf("expected export to include paste after restart, got %#v", exportAfterRestart["pastes"])
	}
	if got := exportAfterRestart["orders"].([]app.Order); len(got) != 1 || got[0].ID != order.ID || got[0].Status != "paid" {
		t.Fatalf("expected export to include paid order after restart, got %#v", exportAfterRestart["orders"])
	}
	if got := exportAfterRestart["reports"].([]app.Report); len(got) != 1 || got[0].UserID != auth.User.ID || got[0].Target != "share:"+share.Token {
		t.Fatalf("expected export to include report after restart, got %#v", exportAfterRestart["reports"])
	}
	if got := exportAfterRestart["webhookEvents"].([]app.WebhookEvent); !containsWebhookEventForTarget(got, order.ID, "payment.succeeded") {
		t.Fatalf("expected export to include order webhook event after restart, got %#v", exportAfterRestart["webhookEvents"])
	}
	if got := exportAfterRestart["auditLogs"].([]app.AuditLog); !containsAuditTargetAction(got, order.ID, "billing.order_paid") {
		t.Fatalf("expected export to include scoped billing audit log after restart, got %#v", exportAfterRestart["auditLogs"])
	}
	if got := exportAfterRestart["auditLogs"].([]app.AuditLog); !containsAuditTargetAction(got, auth.User.ID, "account.export") {
		t.Fatalf("expected export to include account export audit log after restart, got %#v", exportAfterRestart["auditLogs"])
	}
	queuesAfterRestart, err := restarted.AdminQueues(admin.ID)
	if err != nil {
		t.Fatalf("admin queues after restart: %v", err)
	}
	if reports := queuesAfterRestart["reports"].([]*app.Report); !containsReport(reports, auth.User.ID, "share:"+share.Token, "abuse") {
		t.Fatalf("expected report after restart, got %#v", queuesAfterRestart["reports"])
	}
	auditAfterRestart, err := restarted.AdminAuditLogs(admin.ID)
	if err != nil {
		t.Fatalf("audit logs after restart: %v", err)
	}
	if !containsAuditAction(auditAfterRestart, "billing.order_paid") {
		t.Fatalf("expected billing audit log after restart, got %#v", auditAfterRestart)
	}
	if err := restarted.ExecuteAccountDeletion(auth.User.ID); err != nil {
		t.Fatalf("execute account deletion after restart: %v", err)
	}

	deletedRestart := newPostgresBackedService(t, ctx, pool, cfg)
	if _, err := deletedRestart.UserForSession(auth.SessionID); !hasAppStatus(err, http.StatusUnauthorized) {
		t.Fatalf("expected deleted user session to be invalid after restart, got %v", err)
	}
	if _, _, err := deletedRestart.AccessShare(share.Token, "share-password", ""); !hasAppStatus(err, http.StatusGone) {
		t.Fatalf("expected deleted account share to be revoked after restart, got %v", err)
	}
	deletedPaste, err := NewPasteStore(pool).PasteByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("read deleted paste row: %v", err)
	}
	if deletedPaste.Status != "pending_delete" {
		t.Fatalf("expected account deletion to persist pending-delete paste, got %#v", deletedPaste)
	}
}

func newPostgresBackedService(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg config.Config) *app.Service {
	t.Helper()
	svc, err := app.NewWithStorage(ctx, cfg, app.Stores{
		Auth: app.AuthStores{
			Users:         NewUserStore(pool),
			Sessions:      NewSessionStore(pool),
			Tokens:        NewAuthTokenStore(pool),
			LoginFailures: NewLoginFailureStore(pool),
		},
		Content: app.ContentStores{
			Pastes:      NewPasteStore(pool),
			Attachments: NewAttachmentStore(pool),
			Shares:      NewShareStore(pool),
		},
		Operational: app.OperationalStores{
			Orders:        NewOrderStore(pool),
			WebhookEvents: NewWebhookEventStore(pool),
			Reports:       NewReportStore(pool),
			Queues:        NewJobStore(pool),
			Mails:         NewMailStore(pool),
		},
		DailyMetrics: NewDailyMetricStore(pool),
		Catalog:      NewCatalogStore(pool),
		AuditLogs:    NewAuditLogStore(pool),
	})
	if err != nil {
		t.Fatalf("new postgres-backed service: %v", err)
	}
	return svc
}

func cleanupServiceIntegrationRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, userEmail string, adminEmail string) {
	t.Helper()
	emails := []string{userEmail, adminEmail}
	statements := []string{
		`DELETE FROM webhook_events WHERE target_id IN (SELECT id FROM orders WHERE user_id IN (SELECT id FROM users WHERE email = ANY($1::text[])))`,
		`DELETE FROM audit_logs WHERE actor_id IN (SELECT id FROM users WHERE email = ANY($1::text[])) OR target IN (SELECT id FROM orders WHERE user_id IN (SELECT id FROM users WHERE email = ANY($1::text[])))`,
		`DELETE FROM jobs WHERE target_id IN (SELECT id FROM pastes WHERE user_id IN (SELECT id FROM users WHERE email = ANY($1::text[])))`,
		`DELETE FROM reports WHERE user_id IN (SELECT id FROM users WHERE email = ANY($1::text[]))`,
		`DELETE FROM mails WHERE recipient = ANY($1::text[])`,
		`DELETE FROM orders WHERE user_id IN (SELECT id FROM users WHERE email = ANY($1::text[]))`,
		`DELETE FROM daily_metrics WHERE user_id IN (SELECT id FROM users WHERE email = ANY($1::text[]))`,
		`DELETE FROM shares WHERE user_id IN (SELECT id FROM users WHERE email = ANY($1::text[]))`,
		`DELETE FROM attachments WHERE user_id IN (SELECT id FROM users WHERE email = ANY($1::text[]))`,
		`DELETE FROM pastes WHERE user_id IN (SELECT id FROM users WHERE email = ANY($1::text[]))`,
		`DELETE FROM auth_tokens WHERE user_id IN (SELECT id FROM users WHERE email = ANY($1::text[]))`,
		`DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email = ANY($1::text[]))`,
		`DELETE FROM login_failures WHERE email = ANY($1::text[])`,
		`DELETE FROM users WHERE email = ANY($1::text[])`,
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement, emails); err != nil {
			t.Fatalf("cleanup service integration rows: %v", err)
		}
	}
}

func containsAuditAction(logs []app.AuditLog, action string) bool {
	for _, log := range logs {
		if log.Action == action {
			return true
		}
	}
	return false
}

func containsAuditTargetAction(logs []app.AuditLog, target string, action string) bool {
	for _, log := range logs {
		if log.Target == target && log.Action == action {
			return true
		}
	}
	return false
}

func containsWebhookEventForTarget(events []app.WebhookEvent, targetID string, eventType string) bool {
	for _, event := range events {
		if event.TargetID == targetID && event.EventType == eventType {
			return true
		}
	}
	return false
}

func containsReport(reports []*app.Report, userID string, target string, reason string) bool {
	for _, report := range reports {
		if report.UserID == userID && report.Target == target && report.Reason == reason {
			return true
		}
	}
	return false
}

func hasAppStatus(err error, status int) bool {
	var appErr *app.Error
	return errors.As(err, &appErr) && appErr.Status == status
}
