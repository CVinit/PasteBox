package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"pastebox/internal/app"
	"pastebox/internal/config"
	"pastebox/internal/postgres"
)

func TestAdminCreateCommandWritesDatabaseWithoutEchoingPassword(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runAdminCreateWith([]string{"--email", "Admin@Example.COM", "--password", "password123"}, &stdout, &stderr, func(_ context.Context, email string, _ string) (app.UserView, error) {
		return app.UserView{Email: strings.ToLower(email)}, nil
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "admin account created or updated: admin@example.com") {
		t.Fatalf("expected normalized admin email in output, got %q", output)
	}
	if strings.Contains(output, "PASTEBOX_BOOTSTRAP") {
		t.Fatalf("output must not contain bootstrap environment settings: %q", output)
	}
	if strings.Contains(output, "password123") {
		t.Fatalf("bootstrap output must not echo the password: %q", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestAdminCreateCommandRequiresEmailAndPassword(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"admin", "create", "--email", "admin@example.com"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected usage exit code 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--email and --password are required") {
		t.Fatalf("expected validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestMailReadinessRequiresSMTPInProduction(t *testing.T) {
	component := mailReadinessNotConfigured(config.Config{AppEnv: "production"})
	if component.Name != "mail" || component.Status != "fail" || !strings.Contains(component.Message, "required in production") {
		t.Fatalf("expected production mail readiness failure, got %#v", component)
	}
}

func TestMailReadinessCanSkipSMTPOutsideProduction(t *testing.T) {
	component := mailReadinessNotConfigured(config.Config{AppEnv: "development"})
	if component.Name != "mail" || component.Status != "skipped" || !strings.Contains(component.Message, "not configured") {
		t.Fatalf("expected development mail readiness skip, got %#v", component)
	}
}

func TestScannerReadinessRequiresClamAVInProduction(t *testing.T) {
	component := scannerReadinessComponent(context.Background(), config.Config{AppEnv: "production"})
	if component.Name != "scanner" || component.Status != "fail" || !strings.Contains(component.Message, "required in production") {
		t.Fatalf("expected production scanner readiness failure, got %#v", component)
	}
}

func TestScannerReadinessCanSkipClamAVOutsideProduction(t *testing.T) {
	component := scannerReadinessComponent(context.Background(), config.Config{AppEnv: "development"})
	if component.Name != "scanner" || component.Status != "skipped" || !strings.Contains(component.Message, "not configured") {
		t.Fatalf("expected development scanner readiness skip, got %#v", component)
	}
}

func TestHTTPServerTimeoutAllowsStreamingByDefault(t *testing.T) {
	if got := httpServerTimeout(0); got != 0 {
		t.Fatalf("expected zero timeout to stay disabled, got %s", got)
	}
	if got := httpServerTimeout(-1); got != 0 {
		t.Fatalf("expected negative timeout to be clamped to disabled, got %s", got)
	}
	if got := httpServerTimeout(45); got != 45*time.Second {
		t.Fatalf("expected configured timeout in seconds, got %s", got)
	}
}

func TestApplyRuntimeLogLevel(t *testing.T) {
	levelVar := newLogLevelVar(slog.LevelInfo)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	applyRuntimeLogLevel("DEBUG", levelVar, logger)
	if got := levelVar.Level(); got != slog.LevelDebug {
		t.Fatalf("expected debug level, got %s", got)
	}
	applyRuntimeLogLevel("trace", levelVar, logger)
	if got := levelVar.Level(); got != slog.LevelDebug {
		t.Fatalf("invalid level should leave current level unchanged, got %s", got)
	}
	applyRuntimeLogLevel("warn", levelVar, logger)
	if got := levelVar.Level(); got != slog.LevelWarn {
		t.Fatalf("expected warn level, got %s", got)
	}
}

func TestScannerReadinessChecksClamAVTCPReachability(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})
	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
			accepted <- struct{}{}
		}
	}()

	component := scannerReadinessComponent(context.Background(), config.Config{
		AppEnv: "production",
		Scanner: config.ScannerConfig{
			Provider: "clamav",
			ClamAV:   config.ClamAVConfig{Addr: listener.Addr().String()},
		},
	})
	if component.Name != "scanner" || component.Status != "ok" {
		t.Fatalf("expected reachable ClamAV scanner readiness, got %#v", component)
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("scanner readiness did not connect to the ClamAV address")
	}
}

func TestWorkerServiceConfigIgnoresBootstrapAdminCredentials(t *testing.T) {
	cfg := config.Config{
		AppEnv:                 "production",
		WorkerID:               "worker-1",
		BootstrapAdminEmail:    "admin@example.com",
		BootstrapAdminPassword: "bootstrap-secret",
	}

	workerCfg := workerServiceConfig(cfg)
	if workerCfg.BootstrapAdminEmail != "" || workerCfg.BootstrapAdminPassword != "" {
		t.Fatalf("worker must not seed or rotate bootstrap admin credentials: %#v", workerCfg)
	}
	if workerCfg.AppEnv != cfg.AppEnv || workerCfg.WorkerID != cfg.WorkerID {
		t.Fatalf("worker service config should preserve non-bootstrap settings, got %#v", workerCfg)
	}
}

func TestMigrateCommandsUseConfiguredDatabaseURL(t *testing.T) {
	t.Setenv("PASTEBOX_DATABASE_URL", "not-a-postgres-url")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"migrate", "status"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected migrate status to fail on invalid DSN, got %d", code)
	}
	if !strings.Contains(stderr.String(), "migrate status failed") {
		t.Fatalf("expected migrate status error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"migrate", "up"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected migrate up to fail on invalid DSN, got %d", code)
	}
	if !strings.Contains(stderr.String(), "migrate up failed") {
		t.Fatalf("expected migrate up error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRequiresExplicitProductionEnvironment(t *testing.T) {
	required := []string{
		"PASTEBOX_IMAGE",
		"PASTEBOX_APP_ENV",
		"PASTEBOX_CONFIG_ENCRYPTION_KEY",
		"PASTEBOX_PUBLIC_URL",
		"PASTEBOX_SUPPORT_EMAIL",
		"PASTEBOX_ABUSE_EMAIL",
		"PASTEBOX_CSRF_SECRET",
		"PASTEBOX_METRICS_TOKEN",
		"PASTEBOX_CORS_ALLOWED_ORIGINS",
		"PASTEBOX_RATE_LIMIT_ENABLED",
		"PASTEBOX_RATE_LIMIT_WINDOW_SECONDS",
		"PASTEBOX_RATE_LIMIT_AUTH",
		"PASTEBOX_RATE_LIMIT_WRITE",
		"PASTEBOX_RATE_LIMIT_UPLOAD",
		"PASTEBOX_RATE_LIMIT_DOWNLOAD",
		"PASTEBOX_RATE_LIMIT_WEBHOOK",
		"PASTEBOX_DOMAIN",
		"PASTEBOX_ADMIN_EMAIL",
		"PASTEBOX_POSTGRES_PASSWORD",
		"PASTEBOX_DATABASE_URL",
		"PASTEBOX_REDIS_ADDR",
		"PASTEBOX_S3_ENDPOINT",
		"PASTEBOX_S3_BUCKET",
		"PASTEBOX_S3_REGION",
		"PASTEBOX_S3_ACCESS_KEY",
		"PASTEBOX_S3_SECRET_KEY",
		"PASTEBOX_SCANNER_PROVIDER",
		"PASTEBOX_CLAMAV_ADDR",
		"PASTEBOX_GOOGLE_OAUTH_CLIENT_ID",
		"PASTEBOX_GOOGLE_OAUTH_CLIENT_SECRET",
		"PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL",
		"PASTEBOX_MAILER_PROVIDER",
		"PASTEBOX_SMTP_HOST",
		"PASTEBOX_SMTP_PORT",
		"PASTEBOX_SMTP_USERNAME",
		"PASTEBOX_SMTP_PASSWORD",
		"PASTEBOX_SMTP_FROM_EMAIL",
		"PASTEBOX_SMTP_TLS_MODE",
		"PASTEBOX_STRIPE_ENABLED",
		"PASTEBOX_STRIPE_WEBHOOK_SECRET",
		"PASTEBOX_STRIPE_CHECKOUT_URL_TEMPLATE",
		"PASTEBOX_EPUSDT_ENABLED",
		"PASTEBOX_EPUSDT_PID",
		"PASTEBOX_EPUSDT_SECRET_KEY",
		"PASTEBOX_EPUSDT_CHECKOUT_URL_TEMPLATE",
		"PASTEBOX_EPUSDT_ADDRESS",
		"PASTEBOX_EPUSDT_CHAIN",
		"PASTEBOX_BOOTSTRAP_ADMIN_EMAIL",
		"PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD",
		"PASTEBOX_RESTIC_REPOSITORY",
		"PASTEBOX_RESTIC_PASSWORD",
		"PASTEBOX_BACKUP_S3_ACCESS_KEY",
		"PASTEBOX_BACKUP_S3_SECRET_KEY",
		"PASTEBOX_WAL_ARCHIVE_TIMEOUT_SECONDS",
		"PASTEBOX_WAL_ARCHIVE_MAX_AGE_SECONDS",
		"PASTEBOX_WAL_ARCHIVE_WAIT_SECONDS",
	}
	for _, key := range required {
		t.Setenv(key, "")
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected missing production env to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "PASTEBOX_APP_ENV") || !strings.Contains(stderr.String(), "PASTEBOX_CONFIG_ENCRYPTION_KEY") {
		t.Fatalf("expected missing env names in stderr, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightPassesWithRequiredEnvironment(t *testing.T) {
	setValidProductionEnv(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected preflight to pass, got %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "production preflight passed") {
		t.Fatalf("expected preflight success output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestProductionRootOnlyPreflightValidatesPublicDeployIdentity(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{name: "domain", key: "PASTEBOX_DOMAIN", value: "localhost", expected: "PASTEBOX_DOMAIN must be a real production hostname"},
		{name: "admin email", key: "PASTEBOX_ADMIN_EMAIL", value: "admin@localhost", expected: "PASTEBOX_ADMIN_EMAIL must use a production email domain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv("PASTEBOX_PUBLIC_URL", "")
			t.Setenv(tt.key, tt.value)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := run([]string{"preflight", "production"}, &stdout, &stderr); code != 1 {
				t.Fatalf("expected root-only preflight to fail, got %d", code)
			}
			if !strings.Contains(stderr.String(), tt.expected) {
				t.Fatalf("expected %q, got %q", tt.expected, stderr.String())
			}
		})
	}
}

func TestProductionPreflightRejectsPlaceholderSecrets(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_S3_SECRET_KEY", "CHANGE_ME_OBJECT_STORAGE_SECRET_KEY")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected placeholder secret to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "placeholder values remain") || !strings.Contains(stderr.String(), "PASTEBOX_S3_SECRET_KEY") {
		t.Fatalf("expected placeholder validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsLatestImage(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_IMAGE", "ghcr.io/cvinit/pastebox:latest")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected latest image to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "must be a sha-* tag or digest") {
		t.Fatalf("expected pinned image validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsNonShaImageTag(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_IMAGE", "ghcr.io/cvinit/pastebox:v1.2.3")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected mutable version tag to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "must be a sha-* tag or digest") {
		t.Fatalf("expected pinned image validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightAllowsDigestImage(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_IMAGE", "ghcr.io/cvinit/pastebox@sha256:0123456789abcdef")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected digest image to pass, got %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "production preflight passed") {
		t.Fatalf("expected preflight success output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestProductionPreflightRequiresHTTPSPublicURL(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_PUBLIC_URL", "http://pastebox.example.com")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected HTTP public URL to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "must use https://") {
		t.Fatalf("expected HTTPS validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsInvalidPublicURLShape(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{
			name:     "local host",
			value:    "https://localhost",
			expected: "PASTEBOX_PUBLIC_URL must use a real production domain",
		},
		{
			name:     "path",
			value:    "https://pastebox.app/app",
			expected: "PASTEBOX_PUBLIC_URL must be the production origin without a path",
		},
		{
			name:     "query",
			value:    "https://pastebox.app?app=pastebox",
			expected: "PASTEBOX_PUBLIC_URL must be the production origin without query or fragment",
		},
		{
			name:     "userinfo",
			value:    "https://user@pastebox.app",
			expected: "PASTEBOX_PUBLIC_URL must not include userinfo",
		},
		{
			name:     "reserved example domain",
			value:    "https://pastebox.example.com",
			expected: "PASTEBOX_PUBLIC_URL must use a real production domain",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv("PASTEBOX_PUBLIC_URL", tt.value)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"preflight", "production"}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected invalid public URL to fail, got %d", code)
			}
			if !strings.Contains(stderr.String(), tt.expected) {
				t.Fatalf("expected %q in stderr, got %q", tt.expected, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
		})
	}
}

func TestProductionPreflightRejectsInvalidProductionDomainConfig(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{
			name:     "domain with scheme",
			key:      "PASTEBOX_DOMAIN",
			value:    "https://pastebox.app",
			expected: "PASTEBOX_DOMAIN must be a hostname",
		},
		{
			name:     "local domain",
			key:      "PASTEBOX_DOMAIN",
			value:    "localhost",
			expected: "PASTEBOX_DOMAIN must be a production hostname",
		},
		{
			name:     "domain mismatch",
			key:      "PASTEBOX_DOMAIN",
			value:    "admin.pastebox.app",
			expected: "PASTEBOX_DOMAIN must match PASTEBOX_PUBLIC_URL host",
		},
		{
			name:     "invalid acme email",
			key:      "PASTEBOX_ADMIN_EMAIL",
			value:    "PasteBox Admin <admin@pastebox.app>",
			expected: "PASTEBOX_ADMIN_EMAIL must be a valid public email address",
		},
		{
			name:     "reserved example domain",
			key:      "PASTEBOX_DOMAIN",
			value:    "pastebox.example.com",
			expected: "PASTEBOX_DOMAIN must be a production hostname",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv(tt.key, tt.value)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"preflight", "production"}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected invalid production domain config to fail, got %d", code)
			}
			if !strings.Contains(stderr.String(), tt.expected) {
				t.Fatalf("expected %q in stderr, got %q", tt.expected, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
		})
	}
}

func TestProductionPreflightRejectsDevelopmentCSRFSecret(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_CSRF_SECRET", "development-csrf-secret")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected development CSRF secret to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "PASTEBOX_CSRF_SECRET must be a production random secret") {
		t.Fatalf("expected CSRF secret validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsShortMetricsToken(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_METRICS_TOKEN", "short")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected short metrics token to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "PASTEBOX_METRICS_TOKEN must be a production random token") {
		t.Fatalf("expected metrics token validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsInvalidPublicContactEmails(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{
			name:     "invalid support email",
			key:      "PASTEBOX_SUPPORT_EMAIL",
			value:    "support",
			expected: "PASTEBOX_SUPPORT_EMAIL must be a valid public email address",
		},
		{
			name:     "local support domain",
			key:      "PASTEBOX_SUPPORT_EMAIL",
			value:    "support@localhost",
			expected: "PASTEBOX_SUPPORT_EMAIL must use a production email domain",
		},
		{
			name:     "local abuse domain",
			key:      "PASTEBOX_ABUSE_EMAIL",
			value:    "abuse@localhost",
			expected: "PASTEBOX_ABUSE_EMAIL must use a production email domain",
		},
		{
			name:     "display name not allowed",
			key:      "PASTEBOX_ABUSE_EMAIL",
			value:    "PasteBox Abuse <abuse@pastebox.app>",
			expected: "PASTEBOX_ABUSE_EMAIL must be a valid public email address",
		},
		{
			name:     "reserved example domain",
			key:      "PASTEBOX_SUPPORT_EMAIL",
			value:    "support@pastebox.example.com",
			expected: "PASTEBOX_SUPPORT_EMAIL must use a production email domain",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv(tt.key, tt.value)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"preflight", "production"}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected invalid public contact to fail, got %d", code)
			}
			if !strings.Contains(stderr.String(), tt.expected) {
				t.Fatalf("expected public contact validation error containing %q, got %q", tt.expected, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
		})
	}
}

func TestProductionPreflightRejectsUnsafeBootstrapAdminPassword(t *testing.T) {
	for _, password := range []string{
		"short",
		"change-me-bootstrap-password",
		"pastebox-admin-secret-123",
	} {
		t.Run(password, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv("PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD", password)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"preflight", "production"}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected unsafe bootstrap password to fail, got %d", code)
			}
			if !strings.Contains(stderr.String(), "PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD") {
				t.Fatalf("expected bootstrap password validation error, got %q", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
		})
	}
}

func TestProductionPreflightRejectsUnsafeCORSOrigins(t *testing.T) {
	tests := []struct {
		name     string
		origins  string
		expected string
	}{
		{
			name:     "wildcard",
			origins:  "*",
			expected: "invalid origin",
		},
		{
			name:     "http",
			origins:  "http://pastebox.app",
			expected: "must use https://",
		},
		{
			name:     "local",
			origins:  "https://localhost:5173",
			expected: "must use real production domains",
		},
		{
			name:     "missing public origin",
			origins:  "https://admin.pastebox.app",
			expected: "must include PASTEBOX_PUBLIC_URL origin",
		},
		{
			name:     "path",
			origins:  "https://pastebox.app/app",
			expected: "exact origins",
		},
		{
			name:     "reserved example domain",
			origins:  "https://pastebox.example.com",
			expected: "must use real production domains",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv("PASTEBOX_CORS_ALLOWED_ORIGINS", tt.origins)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"preflight", "production"}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected unsafe CORS origin to fail, got %d", code)
			}
			if !strings.Contains(stderr.String(), tt.expected) {
				t.Fatalf("expected CORS validation error containing %q, got %q", tt.expected, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
		})
	}
}

func TestProductionPreflightRejectsDisabledOrInvalidRateLimits(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{
			name:     "disabled",
			key:      "PASTEBOX_RATE_LIMIT_ENABLED",
			value:    "false",
			expected: "PASTEBOX_RATE_LIMIT_ENABLED must be true",
		},
		{
			name:     "window",
			key:      "PASTEBOX_RATE_LIMIT_WINDOW_SECONDS",
			value:    "0",
			expected: "PASTEBOX_RATE_LIMIT_WINDOW_SECONDS must be positive",
		},
		{
			name:     "auth",
			key:      "PASTEBOX_RATE_LIMIT_AUTH",
			value:    "0",
			expected: "PASTEBOX_RATE_LIMIT_AUTH must be positive",
		},
		{
			name:     "write",
			key:      "PASTEBOX_RATE_LIMIT_WRITE",
			value:    "-1",
			expected: "PASTEBOX_RATE_LIMIT_WRITE must be positive",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv(tt.key, tt.value)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"preflight", "production"}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected invalid rate limit config to fail, got %d", code)
			}
			if !strings.Contains(stderr.String(), tt.expected) {
				t.Fatalf("expected rate limit validation error containing %q, got %q", tt.expected, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
		})
	}
}

func TestProductionPreflightRejectsGoogleOAuthRedirectHostMismatch(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL", "https://auth.pastebox.app/api/v1/auth/google/callback")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected OAuth redirect host mismatch to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "host must match PASTEBOX_PUBLIC_URL host") {
		t.Fatalf("expected Google OAuth redirect host validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsReservedGoogleOAuthRedirectHost(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL", "https://pastebox.example.com/api/v1/auth/google/callback")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected reserved OAuth redirect host to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL must use a real production domain") {
		t.Fatalf("expected reserved OAuth redirect host validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRequiresSMTPProvider(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_MAILER_PROVIDER", "log")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected non-SMTP provider to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "PASTEBOX_MAILER_PROVIDER must be smtp") {
		t.Fatalf("expected SMTP provider validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsLocalSMTPHost(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_SMTP_HOST", "localhost")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected local SMTP host to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "PASTEBOX_SMTP_HOST must point to a real production SMTP service") {
		t.Fatalf("expected SMTP host validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsReservedSMTPConfigHosts(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{
			name:     "smtp host",
			key:      "PASTEBOX_SMTP_HOST",
			value:    "smtp.example.com",
			expected: "PASTEBOX_SMTP_HOST must point to a real production SMTP service",
		},
		{
			name:     "from email",
			key:      "PASTEBOX_SMTP_FROM_EMAIL",
			value:    "noreply@example.com",
			expected: "PASTEBOX_SMTP_FROM_EMAIL must use a production email domain",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv(tt.key, tt.value)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"preflight", "production"}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected reserved SMTP config to fail, got %d", code)
			}
			if !strings.Contains(stderr.String(), tt.expected) {
				t.Fatalf("expected %q in stderr, got %q", tt.expected, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
		})
	}
}

func TestProductionPreflightRejectsPlainSMTP(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_SMTP_TLS_MODE", "none")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected plain SMTP to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "PASTEBOX_SMTP_TLS_MODE must be starttls or tls") {
		t.Fatalf("expected SMTP TLS validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsLocalObjectStorageEndpoint(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_S3_ENDPOINT", "http://localhost:9000")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected local object storage endpoint to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "PASTEBOX_S3_ENDPOINT must use https:// managed object storage") {
		t.Fatalf("expected managed object storage validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsReservedObjectAndBackupEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{
			name:     "object storage",
			key:      "PASTEBOX_S3_ENDPOINT",
			value:    "https://objects.example.com",
			expected: "PASTEBOX_S3_ENDPOINT must point to real off-host managed object storage",
		},
		{
			name:     "backup repository",
			key:      "PASTEBOX_RESTIC_REPOSITORY",
			value:    "s3:https://backups.example.com/pastebox-backups",
			expected: "PASTEBOX_RESTIC_REPOSITORY must point to real off-host managed object storage",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv(tt.key, tt.value)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"preflight", "production"}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected reserved storage endpoint to fail, got %d", code)
			}
			if !strings.Contains(stderr.String(), tt.expected) {
				t.Fatalf("expected %q in stderr, got %q", tt.expected, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
		})
	}
}

func TestProductionPreflightRejectsLocalResticRepository(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_RESTIC_REPOSITORY", "local:/backups")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected local restic repository to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "PASTEBOX_RESTIC_REPOSITORY must use an off-host S3 HTTPS repository") {
		t.Fatalf("expected off-host backup validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsSharedObjectAndBackupCredentials(t *testing.T) {
	tests := []struct {
		name     string
		envKey   string
		value    string
		expected string
	}{
		{
			name:     "shared access key",
			envKey:   "PASTEBOX_BACKUP_S3_ACCESS_KEY",
			value:    "access-key",
			expected: "PASTEBOX_BACKUP_S3_ACCESS_KEY must be separate from PASTEBOX_S3_ACCESS_KEY",
		},
		{
			name:     "shared secret key",
			envKey:   "PASTEBOX_BACKUP_S3_SECRET_KEY",
			value:    "secret-key",
			expected: "PASTEBOX_BACKUP_S3_SECRET_KEY must be separate from PASTEBOX_S3_SECRET_KEY",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv(tt.envKey, tt.value)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"preflight", "production"}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected shared credential preflight failure, got %d", code)
			}
			if !strings.Contains(stderr.String(), tt.expected) {
				t.Fatalf("expected %q in stderr, got %q", tt.expected, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
		})
	}
}

func TestProductionPreflightRejectsInvalidPaymentCheckoutTemplates(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{
			name:     "stripe http",
			key:      "PASTEBOX_STRIPE_CHECKOUT_URL_TEMPLATE",
			value:    "http://checkout.pastebox-billing.app/session?order_id={order_id}",
			expected: "PASTEBOX_STRIPE_CHECKOUT_URL_TEMPLATE must use https:// payment checkout URLs",
		},
		{
			name:     "epusdt invalid",
			key:      "PASTEBOX_EPUSDT_CHECKOUT_URL_TEMPLATE",
			value:    "not-a-url",
			expected: "PASTEBOX_EPUSDT_CHECKOUT_URL_TEMPLATE must be a valid https payment checkout URL template",
		},
		{
			name:     "stripe reserved host",
			key:      "PASTEBOX_STRIPE_CHECKOUT_URL_TEMPLATE",
			value:    "https://checkout.example.com/session?order_id={order_id}",
			expected: "PASTEBOX_STRIPE_CHECKOUT_URL_TEMPLATE must point to a real production payment checkout service",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv(tt.key, tt.value)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"preflight", "production"}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected invalid checkout template to fail, got %d", code)
			}
			if !strings.Contains(stderr.String(), tt.expected) {
				t.Fatalf("expected checkout template validation error containing %q, got %q", tt.expected, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
		})
	}
}

func TestProductionPreflightRejectsWALArchiveRPOAboveFifteenMinutes(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_WAL_ARCHIVE_TIMEOUT_SECONDS", "901")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected high WAL archive timeout to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "PASTEBOX_WAL_ARCHIVE_TIMEOUT_SECONDS must be <= 900") {
		t.Fatalf("expected WAL timeout validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsInvalidWALArchiveFreshness(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_WAL_ARCHIVE_MAX_AGE_SECONDS", "not-a-number")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected invalid WAL archive max age to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "PASTEBOX_WAL_ARCHIVE_MAX_AGE_SECONDS must be a positive integer") {
		t.Fatalf("expected WAL max-age validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsWALArchiveWaitAboveFifteenMinutes(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_WAL_ARCHIVE_WAIT_SECONDS", "901")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"preflight", "production"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected high WAL archive wait to fail, got %d", code)
	}
	if !strings.Contains(stderr.String(), "PASTEBOX_WAL_ARCHIVE_WAIT_SECONDS must be <= 900") {
		t.Fatalf("expected WAL wait validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestProductionPreflightRejectsInvalidWorkerHeartbeatMaxAge(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{name: "zero", value: "0", expected: "PASTEBOX_WORKER_HEARTBEAT_MAX_AGE_SECONDS must be a positive integer"},
		{name: "not numeric", value: "later", expected: "PASTEBOX_WORKER_HEARTBEAT_MAX_AGE_SECONDS must be a positive integer"},
		{name: "too high", value: "301", expected: "PASTEBOX_WORKER_HEARTBEAT_MAX_AGE_SECONDS must be <= 300"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv("PASTEBOX_WORKER_HEARTBEAT_MAX_AGE_SECONDS", tt.value)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"preflight", "production"}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected invalid worker heartbeat max age to fail, got %d", code)
			}
			if !strings.Contains(stderr.String(), tt.expected) {
				t.Fatalf("expected %q in stderr, got %q", tt.expected, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
		})
	}
}

func TestWorkerReadinessComponentUsesDurableHeartbeat(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	cfg := config.Config{AppEnv: "production", WorkerHeartbeatMaxAgeSeconds: 120}

	missing := workerReadinessComponent(context.Background(), cfg, fakeWorkerHeartbeatReader{err: postgres.ErrWorkerHeartbeatNotFound}, now)
	if missing.Status != "fail" || !strings.Contains(missing.Message, "worker heartbeat is missing") {
		t.Fatalf("expected missing worker heartbeat failure, got %#v", missing)
	}

	stale := workerReadinessComponent(context.Background(), cfg, fakeWorkerHeartbeatReader{heartbeat: postgres.WorkerHeartbeat{
		WorkerID:   "worker-old",
		LastSeenAt: now.Add(-3 * time.Minute),
	}}, now)
	if stale.Status != "fail" || !strings.Contains(stale.Message, "worker heartbeat stale") {
		t.Fatalf("expected stale worker heartbeat failure, got %#v", stale)
	}

	ok := workerReadinessComponent(context.Background(), cfg, fakeWorkerHeartbeatReader{heartbeat: postgres.WorkerHeartbeat{
		WorkerID:   "worker-current",
		LastSeenAt: now.Add(-30 * time.Second),
	}}, now)
	if ok.Status != "ok" || ok.Message != "" {
		t.Fatalf("expected current worker heartbeat to pass, got %#v", ok)
	}

	dev := workerReadinessComponent(context.Background(), config.Config{AppEnv: "development"}, fakeWorkerHeartbeatReader{}, now)
	if dev.Status != "skipped" {
		t.Fatalf("expected development worker heartbeat to be skipped, got %#v", dev)
	}
}

type fakeWorkerHeartbeatReader struct {
	heartbeat postgres.WorkerHeartbeat
	err       error
}

func (s fakeWorkerHeartbeatReader) LastWorkerHeartbeat(context.Context) (postgres.WorkerHeartbeat, error) {
	if s.err != nil {
		return postgres.WorkerHeartbeat{}, s.err
	}
	return s.heartbeat, nil
}

func setValidProductionEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PASTEBOX_IMAGE", "ghcr.io/cvinit/pastebox:sha-abc123")
	t.Setenv("PASTEBOX_APP_ENV", "production")
	t.Setenv("PASTEBOX_CONFIG_ENCRYPTION_KEY", "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")
	t.Setenv("PASTEBOX_PREFLIGHT_ROOT_ONLY", "true")
	t.Setenv("PASTEBOX_PUBLIC_URL", "https://pastebox.app")
	t.Setenv("PASTEBOX_SUPPORT_EMAIL", "support@pastebox.app")
	t.Setenv("PASTEBOX_ABUSE_EMAIL", "abuse@pastebox.app")
	t.Setenv("PASTEBOX_CSRF_SECRET", "csrf-secret-32-bytes-minimum-prod")
	t.Setenv("PASTEBOX_METRICS_TOKEN", "metrics-token-32-bytes-minimum-prod")
	t.Setenv("PASTEBOX_CORS_ALLOWED_ORIGINS", "https://pastebox.app")
	t.Setenv("PASTEBOX_RATE_LIMIT_ENABLED", "true")
	t.Setenv("PASTEBOX_RATE_LIMIT_WINDOW_SECONDS", "60")
	t.Setenv("PASTEBOX_RATE_LIMIT_AUTH", "60")
	t.Setenv("PASTEBOX_RATE_LIMIT_WRITE", "300")
	t.Setenv("PASTEBOX_RATE_LIMIT_UPLOAD", "120")
	t.Setenv("PASTEBOX_RATE_LIMIT_DOWNLOAD", "600")
	t.Setenv("PASTEBOX_RATE_LIMIT_WEBHOOK", "300")
	t.Setenv("PASTEBOX_DOMAIN", "pastebox.app")
	t.Setenv("PASTEBOX_ADMIN_EMAIL", "admin@pastebox.app")
	t.Setenv("PASTEBOX_POSTGRES_PASSWORD", "db-secret")
	t.Setenv("PASTEBOX_DATABASE_URL", "postgres://pastebox:secret@postgres:5432/pastebox?sslmode=disable")
	t.Setenv("PASTEBOX_REDIS_ADDR", "redis:6379")
	t.Setenv("PASTEBOX_WORKER_ID", "pastebox-worker")
	t.Setenv("PASTEBOX_WORKER_HEARTBEAT_MAX_AGE_SECONDS", "120")
	t.Setenv("PASTEBOX_S3_ENDPOINT", "https://objects.pastebox-storage.app")
	t.Setenv("PASTEBOX_S3_BUCKET", "pastebox-prod")
	t.Setenv("PASTEBOX_S3_REGION", "us-east-1")
	t.Setenv("PASTEBOX_S3_ACCESS_KEY", "access-key")
	t.Setenv("PASTEBOX_S3_SECRET_KEY", "secret-key")
	t.Setenv("PASTEBOX_SCANNER_PROVIDER", "clamav")
	t.Setenv("PASTEBOX_CLAMAV_ADDR", "clamav:3310")
	t.Setenv("PASTEBOX_CLAMAV_TIMEOUT_SECONDS", "30")
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_CLIENT_ID", "google-client-id")
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_CLIENT_SECRET", "google-client-secret")
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL", "https://pastebox.app/api/v1/auth/google/callback")
	t.Setenv("PASTEBOX_MAILER_PROVIDER", "smtp")
	t.Setenv("PASTEBOX_SMTP_HOST", "smtp.pastebox-mail.app")
	t.Setenv("PASTEBOX_SMTP_PORT", "587")
	t.Setenv("PASTEBOX_SMTP_USERNAME", "smtp-user")
	t.Setenv("PASTEBOX_SMTP_PASSWORD", "smtp-secret")
	t.Setenv("PASTEBOX_SMTP_FROM_EMAIL", "noreply@pastebox.app")
	t.Setenv("PASTEBOX_SMTP_TLS_MODE", "starttls")
	t.Setenv("PASTEBOX_STRIPE_ENABLED", "true")
	t.Setenv("PASTEBOX_STRIPE_WEBHOOK_SECRET", "whsec_test_production_webhook_secret")
	t.Setenv("PASTEBOX_STRIPE_CHECKOUT_URL_TEMPLATE", "https://checkout.pastebox-billing.app/session?order_id={order_id}&success_url={success_url}&cancel_url={cancel_url}")
	t.Setenv("PASTEBOX_EPUSDT_ENABLED", "true")
	t.Setenv("PASTEBOX_EPUSDT_PID", "1000")
	t.Setenv("PASTEBOX_EPUSDT_SECRET_KEY", "epusdt-secret")
	t.Setenv("PASTEBOX_EPUSDT_CHECKOUT_URL_TEMPLATE", "https://epusdt.pastebox-billing.app/pay?order_id={order_id}&amount_cents={amount_cents}&currency={currency}")
	t.Setenv("PASTEBOX_EPUSDT_ADDRESS", "TREALUSDTADDRESS")
	t.Setenv("PASTEBOX_EPUSDT_CHAIN", "USDT-TRC20")
	t.Setenv("PASTEBOX_BOOTSTRAP_ADMIN_EMAIL", "admin@pastebox.app")
	t.Setenv("PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD", "bootstrap-random-32-byte-secret")
	t.Setenv("PASTEBOX_RESTIC_REPOSITORY", "s3:https://backups.pastebox-storage.app/pastebox-backups")
	t.Setenv("PASTEBOX_RESTIC_PASSWORD", "restic-secret")
	t.Setenv("PASTEBOX_BACKUP_S3_ACCESS_KEY", "backup-access-key")
	t.Setenv("PASTEBOX_BACKUP_S3_SECRET_KEY", "backup-secret-key")
	t.Setenv("PASTEBOX_WAL_ARCHIVE_TIMEOUT_SECONDS", "900")
	t.Setenv("PASTEBOX_WAL_ARCHIVE_MAX_AGE_SECONDS", "900")
	t.Setenv("PASTEBOX_WAL_ARCHIVE_WAIT_SECONDS", "60")
}
