package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestAdminCreateCommandEmitsBootstrapEnvWithoutPassword(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"admin", "create", "--email", "Admin@Example.COM", "--password", "password123"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "created bootstrap admin admin@example.com") {
		t.Fatalf("expected normalized admin email in output, got %q", output)
	}
	if !strings.Contains(output, "PASTEBOX_BOOTSTRAP_ADMIN_EMAIL=admin@example.com") {
		t.Fatalf("expected bootstrap email env, got %q", output)
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
		"PASTEBOX_PUBLIC_URL",
		"PASTEBOX_CSRF_SECRET",
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
		"PASTEBOX_BOOTSTRAP_ADMIN_EMAIL",
		"PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD",
		"PASTEBOX_RESTIC_REPOSITORY",
		"PASTEBOX_RESTIC_PASSWORD",
		"PASTEBOX_BACKUP_S3_ACCESS_KEY",
		"PASTEBOX_BACKUP_S3_SECRET_KEY",
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
	if !strings.Contains(stderr.String(), "PASTEBOX_APP_ENV") || !strings.Contains(stderr.String(), "PASTEBOX_PUBLIC_URL") {
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

func TestProductionPreflightRejectsGoogleOAuthRedirectHostMismatch(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL", "https://auth.example.com/api/v1/auth/google/callback")

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
	if !strings.Contains(stderr.String(), "PASTEBOX_SMTP_HOST must point to the production SMTP service") {
		t.Fatalf("expected SMTP host validation error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
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

func setValidProductionEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PASTEBOX_IMAGE", "ghcr.io/cvinit/pastebox:sha-abc123")
	t.Setenv("PASTEBOX_APP_ENV", "production")
	t.Setenv("PASTEBOX_PUBLIC_URL", "https://pastebox.example.com")
	t.Setenv("PASTEBOX_CSRF_SECRET", "csrf-secret-32-bytes-minimum-prod")
	t.Setenv("PASTEBOX_DOMAIN", "pastebox.example.com")
	t.Setenv("PASTEBOX_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PASTEBOX_POSTGRES_PASSWORD", "db-secret")
	t.Setenv("PASTEBOX_DATABASE_URL", "postgres://pastebox:secret@postgres:5432/pastebox?sslmode=disable")
	t.Setenv("PASTEBOX_REDIS_ADDR", "redis:6379")
	t.Setenv("PASTEBOX_S3_ENDPOINT", "https://objects.example.com")
	t.Setenv("PASTEBOX_S3_BUCKET", "pastebox-prod")
	t.Setenv("PASTEBOX_S3_REGION", "us-east-1")
	t.Setenv("PASTEBOX_S3_ACCESS_KEY", "access-key")
	t.Setenv("PASTEBOX_S3_SECRET_KEY", "secret-key")
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_CLIENT_ID", "google-client-id")
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_CLIENT_SECRET", "google-client-secret")
	t.Setenv("PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL", "https://pastebox.example.com/api/v1/auth/google/callback")
	t.Setenv("PASTEBOX_MAILER_PROVIDER", "smtp")
	t.Setenv("PASTEBOX_SMTP_HOST", "smtp.example.com")
	t.Setenv("PASTEBOX_SMTP_PORT", "587")
	t.Setenv("PASTEBOX_SMTP_USERNAME", "smtp-user")
	t.Setenv("PASTEBOX_SMTP_PASSWORD", "smtp-secret")
	t.Setenv("PASTEBOX_SMTP_FROM_EMAIL", "noreply@pastebox.example.com")
	t.Setenv("PASTEBOX_SMTP_TLS_MODE", "starttls")
	t.Setenv("PASTEBOX_BOOTSTRAP_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD", "change-me")
	t.Setenv("PASTEBOX_RESTIC_REPOSITORY", "s3:https://objects.example.com/pastebox-backups")
	t.Setenv("PASTEBOX_RESTIC_PASSWORD", "restic-secret")
	t.Setenv("PASTEBOX_BACKUP_S3_ACCESS_KEY", "backup-access-key")
	t.Setenv("PASTEBOX_BACKUP_S3_SECRET_KEY", "backup-secret-key")
}
