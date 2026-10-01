package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

// Widen the check-to-record interval deterministically, without replacing the
// actual database counter or lookup path.
type slowPickupAttempts struct{ *PickupAttemptStore }

func (s slowPickupAttempts) PickupAttemptCount(ctx context.Context, key string, window time.Duration, now time.Time) (app.PickupAttemptWindow, error) {
	state, err := s.PickupAttemptStore.PickupAttemptCount(ctx, key, window, now)
	select {
	case <-time.After(40 * time.Millisecond):
	case <-ctx.Done():
		return state, ctx.Err()
	}
	return state, err
}

func TestPickupFailureAdmissionAcrossInstances(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, second := pickupTestPools(t, ctx)
	key := fmt.Sprintf("concurrent-pickup-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM pickup_code_attempts WHERE attempt_key=$1", "pickup:"+key)
	})
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword = "", ""
	firstStore := slowPickupAttempts{NewPickupAttemptStore(pool)}
	secondStore := slowPickupAttempts{NewPickupAttemptStore(second)}
	first := newPostgresBackedService(t, ctx, pool, cfg, func(s *app.Stores) { s.Content.PickupAttempts = firstStore })
	other := newPostgresBackedService(t, ctx, second, cfg, func(s *app.Stores) { s.Content.PickupAttempts = secondStore })
	for range 9 {
		if _, err := first.ResolvePickupCodeWithContext(ctx, "ZZZZZZ", key); !hasAppStatus(err, 404) {
			t.Fatalf("seed failure: %v", err)
		}
	}
	user := seedStatusUser(t, ctx, pool, "pickup-success")
	transfer, err := first.CreateTransferWithContext(ctx, user.ID, app.TransferInput{Text: "valid pickup", ExpiresInSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	published, err := first.PublishTransferWithContext(ctx, user.ID, transfer.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := other.ResolvePickupCodeWithContext(ctx, published.PickupCode, key); err != nil {
			t.Fatalf("valid lookup consumed failure budget: %v", err)
		}
	}
	attempts, err := firstStore.PickupAttemptCount(ctx, "pickup:"+key, time.Minute, time.Now())
	if err != nil || attempts.Count != 9 {
		t.Fatalf("success changed failure budget: %#v %v", attempts, err)
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	for i := range 8 {
		go func() {
			<-start
			service := first
			if i%2 == 1 {
				service = other
			}
			_, err := service.ResolvePickupCodeWithContext(ctx, "ZZZZZZ", key)
			results <- err
		}()
	}
	close(start)
	admitted := 0
	for range 8 {
		err := <-results
		var domain *app.Error
		if !errors.As(err, &domain) {
			t.Fatalf("unexpected error: %v", err)
		}
		if domain.Code == "pickup_not_found" {
			admitted++
		} else if domain.Code != "pickup_rate_limited" {
			t.Fatalf("unexpected code: %s", domain.Code)
		}
	}
	if admitted != 1 {
		t.Fatalf("admitted %d failed lookups with only one failure left", admitted)
	}
}
