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
	"pastebox/internal/config"
)

func TestBoundedCachesPreserveExportDeletionAndAdminTotals(t *testing.T) {
	dsn := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := ApplyMigrations(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	prefix := fmt.Sprintf("cache_%d_", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, table := range []string{"audit_logs", "webhook_events", "mails", "users"} {
			if _, err := pool.Exec(cleanupCtx, "DELETE FROM "+table+" WHERE id LIKE $1", prefix+"%"); err != nil {
				t.Errorf("cleanup %s: %v", table, err)
			}
		}
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM audit_logs WHERE actor_id LIKE $1", prefix+"%")
	})
	now := time.Now().UTC()
	users := NewUserStore(pool)
	for _, role := range []string{"user", "admin"} {
		err := users.CreateUser(ctx, app.User{ID: prefix + role, Email: prefix + role + "@example.test", DisplayName: role, Language: "en", PasswordHash: "fixture", Role: role, EmailVerified: true, PlanID: "free", CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pastes (id,user_id,title,text_body,tags,status,scan_status,expires_at,created_at,updated_at) SELECT $1 || n, $2, 'cache test', 'body', '[]', 'active', 'clean', now()+interval '1 day', now()+n*interval '1 second', now() FROM generate_series(1,1002) n`, prefix+"paste_", prefix+"user"); err != nil {
		t.Fatal(err)
	}
	orders := NewOrderStore(pool)
	for i := 0; i < 3; i++ {
		expires := now.Add(-time.Minute)
		if err := orders.CreateOrder(ctx, app.Order{ID: fmt.Sprintf("%sorder_%d", prefix, i), UserID: prefix + "user", Provider: "stripe", PlanID: "plus", Period: "monthly", Currency: "USD", Status: "pending", AmountCents: 900, CreatedAt: now.Add(time.Duration(i) * time.Second), ExpiresAt: &expires}); err != nil {
			t.Fatal(err)
		}
	}
	reports := NewReportStore(pool)
	if err := reports.CreateReport(ctx, app.Report{ID: prefix + "report", UserID: prefix + "user", Target: "paste:" + prefix + "paste_1", Reason: "fixture", Status: "open", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	events := NewWebhookEventStore(pool)
	if err := events.CreateWebhookEvent(ctx, app.WebhookEvent{ID: prefix + "event", TargetID: prefix + "order_0", Provider: "stripe", EventType: "pending", IdempotencyKey: prefix + "key", ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	service := newPostgresBackedService(t, ctx, pool, cfg)
	exported, err := service.ExportUserWithContext(ctx, prefix+"user")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(exported["pastes"].([]app.PasteView)); got != 1002 {
		t.Fatalf("export lost pastes outside initial cache: %d", got)
	}
	if len(exported["reports"].([]app.Report)) != 1 || len(exported["webhookEvents"].([]app.WebhookEvent)) != 1 {
		t.Fatalf("missing scoped export records: %#v", exported)
	}
	dashboard, err := service.AdminDashboardWithContext(ctx, prefix+"admin")
	if err != nil {
		t.Fatal(err)
	}
	if dashboard["activePastes"].(int) < 1002 {
		t.Fatalf("dashboard uses partial cache: %#v", dashboard)
	}
	page, err := service.AdminOrdersPage(ctx, prefix+"admin", app.ListOptions{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ID != prefix+"order_1" {
		t.Fatalf("order pagination: %#v", page)
	}
	eventPage, err := service.AdminWebhookEventsPage(ctx, prefix+"admin", app.ListOptions{Limit: 1})
	if err != nil || len(eventPage) != 1 {
		t.Fatalf("webhook page: %#v, %v", eventPage, err)
	}
	if page, err := reports.ListReportsPage(ctx, 1, 0); err != nil || len(page) != 1 {
		t.Fatalf("report page: %#v %v", page, err)
	}
	result, err := service.RunBillingReconciliationWithContext(ctx, prefix+"admin")
	if err != nil || result["expiredOrders"] < 3 {
		t.Fatalf("billing reconcile: %#v %v", result, err)
	}
	if err := service.ExecuteAccountDeletionWithContext(ctx, prefix+"user"); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pastes WHERE user_id=$1 AND status='active'`, prefix+"user").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("deletion missed durable rows: %d %v", remaining, err)
	}
	// Two independent workers must serialize an entire cleanup sweep.
	cleanupStore := NewPasteStore(pool)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- cleanupStore.WithCleanupLock(ctx, func(context.Context) error { close(entered); <-release; return nil })
	}()
	<-entered
	shortCtx, shortCancel := context.WithTimeout(ctx, 75*time.Millisecond)
	err = NewPasteStore(pool).WithCleanupLock(shortCtx, func(context.Context) error { t.Error("second worker entered held cleanup lock"); return nil })
	shortCancel()
	close(release)
	if err == nil {
		t.Fatal("cleanup lock ignored cancellation")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := cleanupStore.WithCleanupLock(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("cleanup lock not released: %v", err)
	}
}

func TestOAuthAccountTransactionRollsBackProfileAndLink(t *testing.T) {
	dsn := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := ApplyMigrations(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	id := fmt.Sprintf("oauth_atomic_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM audit_logs WHERE actor_id=$1", id)
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", id)
	})
	user := app.User{ID: id, Email: id + "@example.test", DisplayName: "original", Role: "user", Language: "en", PlanID: "free", PasswordHash: "original-password", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := NewUserStore(pool).CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	updated := user
	updated.DisplayName = "linked"
	updated.EmailVerified = true
	identity := app.OAuthIdentity{UserID: id, Provider: "google", Subject: id, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	audit := app.AuditLog{ID: id + "_audit", ActorID: id, Action: "auth.oauth_linked", Target: id, Metadata: map[string]any{"invalid": make(chan int)}, CreatedAt: time.Now()}
	store := NewBusinessTransactionStore(pool)
	if err := store.SaveOAuthAccount(ctx, updated, &identity, []app.AuditLog{audit}); err == nil {
		t.Fatal("expected invalid audit failure")
	}
	actual, err := NewUserStore(pool).UserByID(ctx, id)
	if err != nil || actual.DisplayName != "original" || actual.EmailVerified {
		t.Fatalf("partial profile commit: %#v %v", actual, err)
	}
	if _, err := NewOAuthIdentityStore(pool).OAuthIdentityByProviderSubject(ctx, "google", id); !errors.Is(err, ErrOAuthIdentityNotFound) {
		t.Fatalf("partial identity commit: %v", err)
	}
	audit.Metadata = nil
	if err := store.SaveOAuthAccount(ctx, updated, &identity, []app.AuditLog{audit}); err != nil {
		t.Fatal(err)
	}

	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	service := newPostgresBackedService(t, ctx, pool, cfg)
	login, err := service.OAuthLogin(ctx, "google", user.Email, "linked", id, "en")
	if err != nil || login.User.ID != id {
		t.Fatalf("durable OAuth login: %#v %v", login, err)
	}
	if _, err := service.OAuthLogin(ctx, "github", user.Email, "linked", id+"_github", "en"); err != nil {
		t.Fatalf("link existing account: %v", err)
	}
	view, err := service.UnlinkOAuthIdentityWithContext(ctx, id, "github")
	if err != nil || len(view.OAuthProviders) != 1 || view.OAuthProviders[0] != "google" {
		t.Fatalf("unlink: %#v %v", view, err)
	}
	// A failing duplicate link must preserve the first complete transaction.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = store.SaveOAuthAccount(ctx, updated, &identity, []app.AuditLog{audit}) }()
	}
	wg.Wait()
	actual, err = NewUserStore(pool).UserByID(ctx, id)
	if err != nil || actual.DisplayName != "linked" || !actual.EmailVerified || actual.PasswordHash != user.PasswordHash {
		t.Fatalf("oauth changed unrelated fields: %#v %v", actual, err)
	}
}
