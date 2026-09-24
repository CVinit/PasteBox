package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

type cancelAwareUserStore struct {
	UserStore
	started chan struct{}
}

func (s *cancelAwareUserStore) UserByID(ctx context.Context, _ string) (User, error) {
	close(s.started)
	<-ctx.Done()
	return User{}, ctx.Err()
}

func TestRequestCancellationReachesUserLookup(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(context.Context, *Service, string) error
	}{
		{"quota", func(ctx context.Context, s *Service, id string) error { _, e := s.QuotaWithContext(ctx, id); return e }},
		{"create paste", func(ctx context.Context, s *Service, id string) error {
			_, e := s.CreatePasteWithContext(ctx, id, PasteInput{Text: "cancel"})
			return e
		}},
		{"profile", func(ctx context.Context, s *Service, id string) error {
			_, e := s.UpdateProfileWithContext(ctx, id, "name", "en")
			return e
		}},
		{"runtime config", func(ctx context.Context, s *Service, id string) error {
			_, e := s.AdminRuntimeConfigWithContext(ctx, id)
			return e
		}},
		{"audit", func(ctx context.Context, s *Service, id string) error {
			_, e := s.AdminAuditLogsWithContext(ctx, id)
			return e
		}},
		{"cleanup", func(ctx context.Context, s *Service, id string) error {
			_, e := s.RunCleanupWithContext(ctx, id)
			return e
		}},
		{"billing", func(ctx context.Context, s *Service, id string) error {
			_, e := s.CreateOrderWithContext(ctx, id, "stripe", "plus", "monthly")
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			s := newTestService(t, &now)
			user := registerTestUser(t, s, "cancel@example.com")
			store := &cancelAwareUserStore{started: make(chan struct{})}
			s.auth.Users = store
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- tc.call(ctx, s, user.User.ID) }()
			select {
			case <-store.started:
			case <-time.After(time.Second):
				t.Fatal("lookup did not start")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("expected cancellation, got %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("request cancellation did not stop lookup")
			}
		})
	}
}

func TestLoginFailuresAreIsolatedByClientIP(t *testing.T) {
	now := time.Now()
	stores := newMemoryAuthStores()
	s := newTestServiceWithAuthStores(t, &now, stores.authStores())
	user := registerTestUser(t, s, "login-scope@example.com")
	for i := 0; i < 5; i++ {
		if _, err := s.LoginFromIP(context.Background(), user.User.Email, "incorrect", "192.0.2.10"); !hasAppCode(err, "invalid_credentials") {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// A second API process sees the durable limit for the same source.
	restarted := newTestServiceWithAuthStores(t, &now, stores.authStores())
	if _, err := restarted.LoginFromIP(context.Background(), user.User.Email, "password123", "192.0.2.10"); !hasAppCode(err, "login_rate_limited") {
		t.Fatalf("same source should be limited: %v", err)
	}
	if _, err := restarted.LoginFromIP(context.Background(), user.User.Email, "password123", "192.0.2.20"); err != nil {
		t.Fatalf("another source must still sign in: %v", err)
	}
	now = now.Add(16 * time.Minute)
	if _, err := restarted.LoginFromIP(context.Background(), user.User.Email, "password123", "192.0.2.10"); err != nil {
		t.Fatalf("expired limit should allow login: %v", err)
	}
}

type blockingLoginUserStore struct {
	UserStore
	started chan struct{}
}

func (s *blockingLoginUserStore) UserByEmail(ctx context.Context, _ string) (User, error) {
	close(s.started)
	<-ctx.Done()
	return User{}, ctx.Err()
}
func TestSlowLoginLookupDoesNotHoldServiceLock(t *testing.T) {
	now := time.Now()
	s := newTestService(t, &now)
	store := &blockingLoginUserStore{started: make(chan struct{})}
	s.auth.Users = store
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := s.LoginFromIP(ctx, "lookup@example.com", "password123", "192.0.2.1"); done <- e }()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("lookup did not start")
	}
	read := make(chan struct{})
	go func() { s.Prices(); close(read) }()
	select {
	case <-read:
	case <-time.After(time.Second):
		t.Fatal("slow login serialized unrelated reads")
	}
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("login cancellation: %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("login did not cancel")
	}
}
