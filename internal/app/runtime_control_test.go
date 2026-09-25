package app

import (
	"reflect"
	"testing"
	"time"

	"pastebox/internal/config"
)

func TestNormalizeRuntimeConfigDefaultsAndPreservesOverrides(t *testing.T) {
	env := config.Config{
		Turnstile: config.TurnstileConfig{SiteKey: "env-site-key", SecretKey: "env-secret"},
		RateLimit: config.RateLimitConfig{Enabled: true, WindowSeconds: 60, AuthLimit: 120, WriteLimit: 80, UploadLimit: 20, DownloadLimit: 40, WebhookLimit: 10},
	}
	base := defaultRuntimeConfig(env)
	got := normalizeRuntimeConfig(RuntimeConfig{}, env)
	if got.ID != runtimeConfigID || got.Managed.Version != base.Managed.Version || got.LogLevel != base.LogLevel {
		t.Fatalf("empty runtime config identity/managed/log defaults: %#v", got)
	}
	if got.GuestUploads.RetentionSeconds != base.GuestUploads.RetentionSeconds ||
		got.GuestUploads.ActivePasteLimit != base.GuestUploads.ActivePasteLimit ||
		got.GuestUploads.ActiveStorageBytes != base.GuestUploads.ActiveStorageBytes ||
		got.GuestUploads.SingleTextBytes != base.GuestUploads.SingleTextBytes ||
		got.GuestUploads.SingleFileBytes != base.GuestUploads.SingleFileBytes ||
		got.GuestUploads.SinglePasteBytes != base.GuestUploads.SinglePasteBytes ||
		got.GuestUploads.AttachmentsPerPasteLimit != base.GuestUploads.AttachmentsPerPasteLimit ||
		got.GuestUploads.DailyUploadBytes != base.GuestUploads.DailyUploadBytes {
		t.Fatalf("empty runtime config did not fill guest upload limits: %#v", got.GuestUploads)
	}
	if !reflect.DeepEqual(got.Registration.AllowedDomains, base.Registration.AllowedDomains) ||
		got.Registration.RequireEmailVerification != base.Registration.RequireEmailVerification ||
		got.Registration.RequireTurnstile != base.Registration.RequireTurnstile ||
		got.Registration.TurnstileSiteKey != base.Managed.Turnstile.SiteKey {
		t.Fatalf("empty runtime config did not fill registration defaults: %#v", got.Registration)
	}
	if !reflect.DeepEqual(got.RateLimits, base.RateLimits) || !reflect.DeepEqual(got.Limits, base.Limits) {
		t.Fatalf("empty runtime config did not fill rate/plan defaults: rate=%#v plans=%#v", got.RateLimits, got.Limits)
	}
	if got.Alerts.CooldownSeconds != base.Alerts.CooldownSeconds ||
		got.Alerts.CPUPercentThreshold != base.Alerts.CPUPercentThreshold ||
		got.Alerts.MemoryPercentThreshold != base.Alerts.MemoryPercentThreshold ||
		got.Alerts.DiskPercentThreshold != base.Alerts.DiskPercentThreshold {
		t.Fatalf("empty runtime config did not fill alert thresholds: %#v", got.Alerts)
	}
	if !reflect.DeepEqual(got.ProviderStatus, base.ProviderStatus) {
		t.Fatalf("empty runtime config did not fill provider status: %#v", got.ProviderStatus)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("expected normalization to fill a missing update timestamp")
	}

	updatedAt := time.Date(2026, 9, 25, 12, 30, 0, 0, time.UTC)
	custom := base
	custom.ID = "runtime-custom"
	custom.LogLevel = " Warning "
	custom.GuestUploads.RetentionSeconds = 123
	custom.Registration.AllowedDomains = []string{" @Example.COM ", "example.com", "invalid", " ", "z.org", "a.org"}
	custom.Registration.RequireEmailVerification = false
	custom.Registration.RequireTurnstile = true
	custom.Registration.TurnstileSiteKey = "stale-site-key"
	custom.Managed.Turnstile.SiteKey = " custom-site-key "
	custom.RateLimits = RuntimeRateLimitConfig{Enabled: false, WindowSeconds: 123}
	custom.Limits.FreePlanID = "free-custom"
	custom.Limits.PaidPlanIDs = []string{"paid-custom"}
	custom.Alerts.CooldownSeconds = 123
	custom.ProviderStatus.Mailer.Provider = "smtp-custom"
	custom.UpdatedAt = updatedAt

	got = normalizeRuntimeConfig(custom, env)
	if got.ID != custom.ID || got.LogLevel != RuntimeLogLevelWarn || got.GuestUploads.RetentionSeconds != 123 {
		t.Fatalf("custom runtime values were not preserved: %#v", got)
	}
	if !reflect.DeepEqual(got.Registration.AllowedDomains, []string{"a.org", "example.com", "z.org"}) {
		t.Fatalf("registration domains were not normalized: %#v", got.Registration.AllowedDomains)
	}
	if got.Registration.RequireEmailVerification || !got.Registration.RequireTurnstile || got.Registration.TurnstileSiteKey != "custom-site-key" {
		t.Fatalf("custom registration settings were not preserved: %#v", got.Registration)
	}
	if got.RateLimits.Enabled || got.RateLimits.WindowSeconds != 123 || got.RateLimits.LoginLimit != base.RateLimits.LoginLimit {
		t.Fatalf("partial rate limits did not preserve the disable override and fill defaults: %#v", got.RateLimits)
	}
	if got.Limits.FreePlanID != "free-custom" || !reflect.DeepEqual(got.Limits.PaidPlanIDs, []string{"paid-custom"}) {
		t.Fatalf("custom plan limits were not preserved: %#v", got.Limits)
	}
	if got.Alerts.CooldownSeconds != 123 || got.Alerts.CPUPercentThreshold != base.Alerts.CPUPercentThreshold {
		t.Fatalf("custom alert settings were not preserved: %#v", got.Alerts)
	}
	if got.ProviderStatus.Mailer.Provider != "smtp-custom" || !got.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("custom provider status or timestamp was not preserved: %#v", got)
	}
}
