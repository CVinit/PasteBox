package config

import (
	"log/slog"
	"testing"
)

func TestFromEnvUsesPasteBoxDefaults(t *testing.T) {
	t.Setenv("PASTEBOX_APP_NAME", "")
	t.Setenv("PASTEBOX_HTTP_ADDR", "")
	t.Setenv("PASTEBOX_STRIPE_ENABLED", "")

	cfg := FromEnv()

	if cfg.AppName != "PasteBox" {
		t.Fatalf("expected PasteBox app name, got %q", cfg.AppName)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("expected default HTTP address, got %q", cfg.HTTPAddr)
	}
	if cfg.StripeEnabled {
		t.Fatal("expected Stripe to be disabled by default")
	}
}

func TestFromEnvParsesBooleansAndLogLevel(t *testing.T) {
	t.Setenv("PASTEBOX_S3_REGION", "auto")
	t.Setenv("PASTEBOX_S3_USE_PATH_STYLE", "false")
	t.Setenv("PASTEBOX_EPUSDT_ENABLED", "true")
	t.Setenv("PASTEBOX_LOG_LEVEL", "DEBUG")
	t.Setenv("PASTEBOX_PUBLIC_URL", "https://pastebox.example.com")
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_CLIENT_ID", "google-client-id")
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_CLIENT_SECRET", "google-client-secret")

	cfg := FromEnv()

	if cfg.S3.UsePathStyle {
		t.Fatal("expected S3 path-style flag to parse as false")
	}
	if cfg.S3.Region != "auto" {
		t.Fatalf("expected S3 region from env, got %q", cfg.S3.Region)
	}
	if !cfg.EpusdtEnabled {
		t.Fatal("expected Epusdt flag to parse as true")
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("expected debug log level, got %s", cfg.LogLevel)
	}
	if cfg.GoogleOAuth.ClientID != "google-client-id" || cfg.GoogleOAuth.ClientSecret != "google-client-secret" {
		t.Fatalf("expected Google OAuth credentials from env, got %#v", cfg.GoogleOAuth)
	}
	if cfg.GoogleOAuth.RedirectURL != "https://pastebox.example.com/api/v1/auth/google/callback" {
		t.Fatalf("expected default Google OAuth redirect URL from public URL, got %q", cfg.GoogleOAuth.RedirectURL)
	}
}
