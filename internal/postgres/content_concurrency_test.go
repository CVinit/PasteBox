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

func TestContentLimitsAcrossDatabaseConnections(t *testing.T) {
	dsn := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL integration database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, e := ApplyMigrations(ctx, dsn); e != nil {
		t.Fatal(e)
	}
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	second, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(second.Close)
	now := time.Now().UTC()
	id := fmt.Sprintf("concurrent_%d", now.UnixNano())
	user := app.User{ID: id, Email: id + "@example.com", DisplayName: "Concurrent", Language: "en", PasswordHash: "hash", Role: "user", PlanID: "free", EmailVerified: true, CreatedAt: now, UpdatedAt: now}
	if e := NewUserStore(pool).CreateUser(ctx, user); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, e := pool.Exec(c, `DELETE FROM users WHERE id=$1`, id); e != nil {
			t.Error(e)
		}
	})
	paste := app.Paste{ID: id + "_paste", UserID: id, Title: "filtered", Text: "body", Tags: []string{"tag"}, Favorite: true, Status: "active", ScanStatus: "clean", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
	ps := NewPasteStore(pool)
	if e := ps.CreatePaste(ctx, paste); e != nil {
		t.Fatal(e)
	}
	attachment := app.Attachment{ID: id + "_attachment", UserID: id, PasteID: paste.ID, FileName: "test.txt", ContentType: "text/plain", Size: 12, SHA256: "digest", ObjectKey: id + "_key", Status: "active", ScanStatus: "clean", CreatedAt: now}
	as := NewAttachmentStore(pool)
	if e := as.CreateAttachment(ctx, attachment); e != nil {
		t.Fatal(e)
	}
	share := app.Share{ID: id + "_share", UserID: id, PasteID: paste.ID, TokenHash: id + "_hash", Token: id + "_token", MaxVisits: 3, MaxDownloads: 2, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	stores := []*ShareStore{NewShareStore(pool), NewShareStore(second)}
	if e := stores[0].CreateShare(ctx, share); e != nil {
		t.Fatal(e)
	}
	run := func(want int, call func(int) error) {
		t.Helper()
		start := make(chan struct{})
		results := make(chan error, 12)
		for i := range 12 {
			go func() { <-start; results <- call(i) }()
		}
		close(start)
		success := 0
		for range 12 {
			err := <-results
			if err == nil {
				success++
				continue
			}
			var domain *app.Error
			if !errors.As(err, &domain) {
				t.Fatalf("unexpected concurrent error: %v", err)
			}
		}
		if success != want {
			t.Fatalf("successful requests=%d want %d", success, want)
		}
	}
	run(3, func(i int) error { _, e := stores[i%2].ConsumeShareVisit(ctx, share.ID, now); return e })
	run(2, func(i int) error {
		_, _, e := stores[i%2].ConsumeShareDownload(ctx, share.ID, attachment.ID, id, 1000, now)
		return e
	})
	// A stale metadata writer must not roll back counters or undo revocation.
	share.RevokedAt = &now
	if e := stores[0].UpdateShare(ctx, share); e != nil {
		t.Fatal(e)
	}
	share.RevokedAt = nil
	if e := stores[1].UpdateShare(ctx, share); e != nil {
		t.Fatal(e)
	}
	loaded, e := stores[0].ShareByID(ctx, share.ID)
	if e != nil || loaded.VisitCount != 3 || loaded.DownloadCount != 2 || loaded.RevokedAt == nil {
		t.Fatalf("stale write weakened share limits: %#v, %v", loaded, e)
	}
	if e := as.UpdateAttachment(ctx, attachment); e != nil {
		t.Fatal(e)
	}
	got, e := as.AttachmentByID(ctx, attachment.ID)
	if e != nil || got.DownloadN != 2 {
		t.Fatalf("stale download count: %#v, %v", got, e)
	}
	share.ID += "_daily"
	share.TokenHash += "_daily"
	share.MaxVisits = 0
	share.MaxDownloads = 0
	if e := stores[0].CreateShare(ctx, share); e != nil {
		t.Fatal(e)
	}
	// Two earlier downloads consumed 24 bytes. Exactly one more fits in 36 bytes.
	run(1, func(i int) error {
		_, _, e := stores[i%2].ConsumeShareDownload(ctx, share.ID, attachment.ID, id, 36, now)
		return e
	})
	metrics, e := NewDailyMetricStore(pool).DailyMetric(ctx, id, "share_download", now)
	if e != nil || metrics != 36 {
		t.Fatalf("daily traffic=%d, %v", metrics, e)
	}
	t.Run("database pagination and filters", func(t *testing.T) {
		filtered, e := ps.ListPastesByUserPageWithOptions(ctx, id, app.ListOptions{Query: "FILTERED", Tag: "tag", Filter: "favorites"}, 1, 0)
		if e != nil || len(filtered) != 1 {
			t.Fatalf("filtered page: %#v, %v", filtered, e)
		}
		empty, e := ps.ListPastesByUserPageWithOptions(ctx, id, app.ListOptions{Tag: "missing"}, 1, 0)
		if e != nil || len(empty) != 0 {
			t.Fatalf("tag isolation: %#v, %v", empty, e)
		}
		if _, e := ps.ListPastesPage(ctx, 1, 0); e != nil {
			t.Fatal(e)
		}
		if _, e := ps.ListPastesForCleanup(ctx, 1); e != nil {
			t.Fatal(e)
		}
		if m, e := ps.UserContentMetrics(ctx, id); e != nil || m.ActivePasteCount != 1 || m.ActiveStorageBytes != 16 {
			t.Fatalf("content metrics: %#v, %v", m, e)
		}
		if rows, e := as.ListAttachmentsPage(ctx, id, 1, 0); e != nil || len(rows) != 1 {
			t.Fatalf("attachment page: %#v, %v", rows, e)
		}
		if rows, e := stores[0].ListSharesByPaste(ctx, paste.ID); e != nil || len(rows) != 2 {
			t.Fatalf("paste shares: %#v, %v", rows, e)
		}
		if rows, e := stores[0].ListSharesPage(ctx, 1, 0); e != nil || len(rows) != 1 {
			t.Fatalf("share page: %#v, %v", rows, e)
		}
		if rows, e := NewUserStore(pool).ListUsersPage(ctx, 1, 0); e != nil || len(rows) != 1 {
			t.Fatalf("user page: %#v, %v", rows, e)
		}
	})
	t.Run("object reference lock cancellation", func(t *testing.T) {
		key := id + "_ref"
		defer as.DeleteObjectRef(ctx, key)
		if _, e := as.IncrementObjectRef(ctx, app.ObjectRef{ObjectKey: key, Size: 12, SHA256: "digest", CreatedAt: now, UpdatedAt: now}); e != nil {
			t.Fatal(e)
		}
		started := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- as.WithObjectRefLock(ctx, key, func(lockCtx context.Context, store app.AtomicObjectRefStore) error {
				_, err := store.IncrementObjectRef(lockCtx, app.ObjectRef{ObjectKey: key, Size: 12, SHA256: "digest", CreatedAt: now, UpdatedAt: now})
				close(started)
				<-release
				return err
			})
		}()
		<-started
		blocked, stop := context.WithTimeout(ctx, 50*time.Millisecond)
		_, _, err := NewAttachmentStore(second).DecrementObjectRef(blocked, key)
		stop()
		close(release)
		if err == nil {
			t.Fatal("cleanup crossed an upload's object lock")
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		ref, removed, err := as.DecrementObjectRef(ctx, key)
		if err != nil || removed || ref.RefCount != 1 {
			t.Fatalf("cleanup removed a referenced object: %#v %v %v", ref, removed, err)
		}
	})
}
