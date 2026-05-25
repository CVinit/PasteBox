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
	t.Setenv("PASTEBOX_SCANNER_PROVIDER", "clamav")
	t.Setenv("PASTEBOX_CLAMAV_ADDR", "clamav:3310")
	t.Setenv("PASTEBOX_CLAMAV_TIMEOUT_SECONDS", "45")
	t.Setenv("PASTEBOX_EPUSDT_ENABLED", "true")
	t.Setenv("PASTEBOX_LOG_LEVEL", "DEBUG")
	t.Setenv("PASTEBOX_PUBLIC_URL", "https://pastebox.example.com")
	t.Setenv("PASTEBOX_CORS_ALLOWED_ORIGINS", "https://pastebox.example.com, https://admin.pastebox.example.com, https://pastebox.example.com")
	t.Setenv("PASTEBOX_CSRF_SECRET", "test-csrf-secret")
	t.Setenv("PASTEBOX_METRICS_TOKEN", "metrics-token")
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_CLIENT_ID", "google-client-id")
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_CLIENT_SECRET", "google-client-secret")
	t.Setenv("PASTEBOX_MAILER_PROVIDER", "smtp")
	t.Setenv("PASTEBOX_SMTP_HOST", "smtp.example.com")
	t.Setenv("PASTEBOX_SMTP_PORT", "587")
	t.Setenv("PASTEBOX_SMTP_USERNAME", "smtp-user")
	t.Setenv("PASTEBOX_SMTP_PASSWORD", "smtp-secret")
	t.Setenv("PASTEBOX_SMTP_FROM_EMAIL", "noreply@pastebox.example.com")
	t.Setenv("PASTEBOX_SMTP_FROM_NAME", "PasteBox Mail")
	t.Setenv("PASTEBOX_SMTP_TLS_MODE", "tls")
	t.Setenv("PASTEBOX_STRIPE_ENABLED", "true")
	t.Setenv("PASTEBOX_STRIPE_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("PASTEBOX_EPUSDT_PID", "1000")
	t.Setenv("PASTEBOX_EPUSDT_SECRET_KEY", "epusdt-secret")

	cfg := FromEnv()

	if cfg.S3.UsePathStyle {
		t.Fatal("expected S3 path-style flag to parse as false")
	}
	if cfg.S3.Region != "auto" {
		t.Fatalf("expected S3 region from env, got %q", cfg.S3.Region)
	}
	if cfg.Scanner.Provider != "clamav" || cfg.Scanner.ClamAV.Addr != "clamav:3310" || cfg.Scanner.ClamAV.Timeout != 45 {
		t.Fatalf("expected scanner settings from env, got %#v", cfg.Scanner)
	}
	if !cfg.EpusdtEnabled {
		t.Fatal("expected Epusdt flag to parse as true")
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("expected debug log level, got %s", cfg.LogLevel)
	}
	if cfg.CSRFSecret != "test-csrf-secret" {
		t.Fatalf("expected CSRF secret from env, got %q", cfg.CSRFSecret)
	}
	if cfg.MetricsToken != "metrics-token" {
		t.Fatalf("expected metrics token from env, got %q", cfg.MetricsToken)
	}
	if cfg.GoogleOAuth.ClientID != "google-client-id" || cfg.GoogleOAuth.ClientSecret != "google-client-secret" {
		t.Fatalf("expected Google OAuth credentials from env, got %#v", cfg.GoogleOAuth)
	}
	if cfg.GoogleOAuth.RedirectURL != "https://pastebox.example.com/api/v1/auth/google/callback" {
		t.Fatalf("expected default Google OAuth redirect URL from public URL, got %q", cfg.GoogleOAuth.RedirectURL)
	}
	if len(cfg.CORSAllowedOrigins) != 2 || cfg.CORSAllowedOrigins[0] != "https://pastebox.example.com" || cfg.CORSAllowedOrigins[1] != "https://admin.pastebox.example.com" {
		t.Fatalf("expected parsed CORS origins from env, got %#v", cfg.CORSAllowedOrigins)
	}
	if cfg.MailerProvider != "smtp" || cfg.SMTP.Host != "smtp.example.com" || cfg.SMTP.Port != 587 {
		t.Fatalf("expected SMTP settings from env, got provider=%q smtp=%#v", cfg.MailerProvider, cfg.SMTP)
	}
	if cfg.SMTP.Username != "smtp-user" || cfg.SMTP.Password != "smtp-secret" || cfg.SMTP.FromEmail != "noreply@pastebox.example.com" || cfg.SMTP.FromName != "PasteBox Mail" || cfg.SMTP.TLSMode != "tls" {
		t.Fatalf("unexpected SMTP config: %#v", cfg.SMTP)
	}
	if !cfg.StripeEnabled || cfg.Stripe.WebhookSecret != "whsec_test" {
		t.Fatalf("expected Stripe settings from env, got enabled=%v stripe=%#v", cfg.StripeEnabled, cfg.Stripe)
	}
	if cfg.Epusdt.PID != "1000" || cfg.Epusdt.SecretKey != "epusdt-secret" {
		t.Fatalf("expected Epusdt settings from env, got %#v", cfg.Epusdt)
	}
}

func TestFromEnvDefaultsCORSOriginsToPublicOrigin(t *testing.T) {
	t.Setenv("PASTEBOX_PUBLIC_URL", "https://pastebox.example.com/app")
	t.Setenv("PASTEBOX_CORS_ALLOWED_ORIGINS", "")

	cfg := FromEnv()

	if len(cfg.CORSAllowedOrigins) != 1 || cfg.CORSAllowedOrigins[0] != "https://pastebox.example.com" {
		t.Fatalf("expected CORS default to use public origin only, got %#v", cfg.CORSAllowedOrigins)
	}
}
