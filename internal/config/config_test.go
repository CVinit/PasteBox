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
}
