package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
	"pastebox/internal/config"
	"pastebox/internal/httpserver"
	"pastebox/internal/mailer"
	"pastebox/internal/objectstore"
	"pastebox/internal/postgres"
	"pastebox/internal/scanner"
	"pastebox/internal/worker"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "api":
			return runAPI(stdout)
		case "admin":
			return runAdmin(args[1:], stdout, stderr)
		case "migrate":
			return runMigrate(args[1:], stdout, stderr)
		case "preflight":
			return runPreflight(args[1:], stdout, stderr)
		case "worker":
			return runWorker(args[1:], stdout, stderr)
		case "help", "-h", "--help":
			printUsage(stdout)
			return 0
		default:
			fmt.Fprintf(stderr, "unknown command %q\n", args[0])
			printUsage(stderr)
			return 2
		}
	}

	return runAPI(stdout)
}

func runAPI(stdout io.Writer) int {
	cfg := config.FromEnv()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer startupCancel()
	service, pool, objects, err := newProductionService(startupCtx, cfg)
	if err != nil {
		logger.Error("service setup failed", "error", err)
		return 1
	}
	defer pool.Close()

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpserver.NewWithServiceAndReadiness(cfg, logger, service, productionReadinessChecker(cfg, pool, objects)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("pastebox api listening", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		logger.Error("api server failed", "error", err)
		return 1
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("api server shutdown failed", "error", err)
		return 1
	}

	logger.Info("api server stopped")
	return 0
}

type objectHealthChecker interface {
	Health(ctx context.Context) error
}

func productionReadinessChecker(cfg config.Config, pool *pgxpool.Pool, objects objectHealthChecker) httpserver.ReadinessChecker {
	return func(ctx context.Context) []httpserver.ReadinessComponent {
		components := []httpserver.ReadinessComponent{
			readinessCheck(ctx, "database", func(checkCtx context.Context) error {
				return pool.Ping(checkCtx)
			}),
			readinessCheck(ctx, "object_storage", func(checkCtx context.Context) error {
				if objects == nil {
					return errors.New("object storage health check is not configured")
				}
				return objects.Health(checkCtx)
			}),
			readinessCheck(ctx, "redis", func(checkCtx context.Context) error {
				return tcpReadiness(checkCtx, cfg.RedisAddr)
			}),
			readinessCheck(ctx, "worker_queue", func(checkCtx context.Context) error {
				_, err := postgres.NewJobStore(pool).ListRunnableJobs(checkCtx, 1, time.Now().UTC())
				return err
			}),
		}
		if strings.EqualFold(strings.TrimSpace(cfg.MailerProvider), "smtp") {
			components = append(components, readinessCheck(ctx, "mail", func(checkCtx context.Context) error {
				return tcpReadiness(checkCtx, net.JoinHostPort(cfg.SMTP.Host, strconv.Itoa(cfg.SMTP.Port)))
			}))
		} else {
			components = append(components, httpserver.ReadinessComponent{Name: "mail", Status: "skipped", Message: "smtp provider is not configured"})
		}
		return components
	}
}

func readinessCheck(ctx context.Context, name string, check func(context.Context) error) httpserver.ReadinessComponent {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := check(checkCtx); err != nil {
		return httpserver.ReadinessComponent{Name: name, Status: "fail", Message: err.Error()}
	}
	return httpserver.ReadinessComponent{Name: name, Status: "ok"}
}

func tcpReadiness(ctx context.Context, address string) error {
	if strings.TrimSpace(address) == "" {
		return errors.New("address is not configured")
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	return conn.Close()
}

func newProductionService(ctx context.Context, cfg config.Config) (*app.Service, *pgxpool.Pool, objectHealthChecker, error) {
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("postgres pool setup: %w", err)
	}
	objects, err := objectstore.NewS3Store(cfg.S3)
	if err != nil {
		pool.Close()
		return nil, nil, nil, fmt.Errorf("object store setup: %w", err)
	}
	service, err := app.NewWithStorage(ctx, cfg, app.Stores{
		Auth: app.AuthStores{
			Users:           postgres.NewUserStore(pool),
			Sessions:        postgres.NewSessionStore(pool),
			Tokens:          postgres.NewAuthTokenStore(pool),
			LoginFailures:   postgres.NewLoginFailureStore(pool),
			OAuthIdentities: postgres.NewOAuthIdentityStore(pool),
		},
		Content: app.ContentStores{
			Pastes:      postgres.NewPasteStore(pool),
			Attachments: postgres.NewAttachmentStore(pool),
			Shares:      postgres.NewShareStore(pool),
		},
		Objects: objects,
		Operational: app.OperationalStores{
			Orders:        postgres.NewOrderStore(pool),
			WebhookEvents: postgres.NewWebhookEventStore(pool),
			Reports:       postgres.NewReportStore(pool),
			Queues:        postgres.NewJobStore(pool),
			Mails:         postgres.NewMailStore(pool),
		},
		DailyMetrics: postgres.NewDailyMetricStore(pool),
		Catalog:      postgres.NewCatalogStore(pool),
		AuditLogs:    postgres.NewAuditLogStore(pool),
	})
	if err != nil {
		pool.Close()
		return nil, nil, nil, err
	}
	return service, pool, objects, nil
}

func runAdmin(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printAdminUsage(stderr)
		return 2
	}
	switch args[0] {
	case "create":
		return runAdminCreate(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printAdminUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown admin command %q\n", args[0])
		printAdminUsage(stderr)
		return 2
	}
}

func runMigrate(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "status":
			return runMigrateStatus(stdout, stderr)
		case "up":
			return runMigrateUp(stdout, stderr)
		case "help", "-h", "--help":
			printMigrateUsage(stdout)
			return 0
		default:
			fmt.Fprintf(stderr, "unknown migrate command %q\n", args[0])
			printMigrateUsage(stderr)
			return 2
		}
	}
	printMigrateUsage(stderr)
	return 2
}

func runMigrateStatus(stdout io.Writer, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	statuses, err := postgres.MigrationStatuses(ctx, config.FromEnv().DatabaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "migrate status failed: %v\n", err)
		return 1
	}
	for _, status := range statuses {
		state := "pending"
		if status.Dirty {
			state = "dirty"
		} else if status.Applied {
			state = "applied"
		}
		fmt.Fprintf(stdout, "%06d %s %s\n", status.Migration.Version, state, status.Migration.Name)
	}
	return 0
}

func runMigrateUp(stdout io.Writer, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	applied, err := postgres.ApplyMigrations(ctx, config.FromEnv().DatabaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "migrate up failed: %v\n", err)
		return 1
	}
	if len(applied) == 0 {
		fmt.Fprintln(stdout, "database migrations already up to date")
		return 0
	}
	for _, migration := range applied {
		fmt.Fprintf(stdout, "applied %06d %s\n", migration.Version, migration.Name)
	}
	return 0
}

func runPreflight(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "production":
			return runProductionPreflight(stdout, stderr)
		case "help", "-h", "--help":
			printPreflightUsage(stdout)
			return 0
		default:
			fmt.Fprintf(stderr, "unknown preflight command %q\n", args[0])
			printPreflightUsage(stderr)
			return 2
		}
	}
	printPreflightUsage(stderr)
	return 2
}

func runProductionPreflight(stdout io.Writer, stderr io.Writer) int {
	cfg := config.FromEnv()
	var missing []string
	var placeholders []string
	for _, key := range []string{
		"PASTEBOX_IMAGE",
		"PASTEBOX_APP_ENV",
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
		"PASTEBOX_EPUSDT_ENABLED",
		"PASTEBOX_EPUSDT_PID",
		"PASTEBOX_EPUSDT_SECRET_KEY",
		"PASTEBOX_BOOTSTRAP_ADMIN_EMAIL",
		"PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD",
		"PASTEBOX_RESTIC_REPOSITORY",
		"PASTEBOX_RESTIC_PASSWORD",
		"PASTEBOX_BACKUP_S3_ACCESS_KEY",
		"PASTEBOX_BACKUP_S3_SECRET_KEY",
		"PASTEBOX_WAL_ARCHIVE_TIMEOUT_SECONDS",
		"PASTEBOX_WAL_ARCHIVE_MAX_AGE_SECONDS",
		"PASTEBOX_WAL_ARCHIVE_WAIT_SECONDS",
	} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			missing = append(missing, key)
			continue
		}
		if strings.Contains(value, "CHANGE_ME") {
			placeholders = append(placeholders, key)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "production preflight failed: missing %s\n", strings.Join(missing, ", "))
		return 1
	}
	if len(placeholders) > 0 {
		fmt.Fprintf(stderr, "production preflight failed: placeholder values remain in %s\n", strings.Join(placeholders, ", "))
		return 1
	}
	if cfg.AppEnv != "production" {
		fmt.Fprintf(stderr, "production preflight failed: PASTEBOX_APP_ENV must be production, got %q\n", cfg.AppEnv)
		return 1
	}
	if !strings.HasPrefix(cfg.PublicURL, "https://") {
		fmt.Fprintf(stderr, "production preflight failed: PASTEBOX_PUBLIC_URL must use https://, got %q\n", cfg.PublicURL)
		return 1
	}
	if err := validatePublicContactEmails(cfg); err != nil {
		fmt.Fprintf(stderr, "production preflight failed: %v\n", err)
		return 1
	}
	if len(strings.TrimSpace(cfg.CSRFSecret)) < 32 || cfg.CSRFSecret == "development-csrf-secret" {
		fmt.Fprintln(stderr, "production preflight failed: PASTEBOX_CSRF_SECRET must be a production random secret at least 32 characters long")
		return 1
	}
	if len(strings.TrimSpace(cfg.MetricsToken)) < 32 {
		fmt.Fprintln(stderr, "production preflight failed: PASTEBOX_METRICS_TOKEN must be a production random token at least 32 characters long")
		return 1
	}
	if err := validateCORSOrigins(cfg); err != nil {
		fmt.Fprintf(stderr, "production preflight failed: %v\n", err)
		return 1
	}
	if err := validateRateLimitConfig(cfg); err != nil {
		fmt.Fprintf(stderr, "production preflight failed: %v\n", err)
		return 1
	}
	if err := validateGoogleOAuthRedirectURL(cfg.GoogleOAuth.RedirectURL, cfg.PublicURL); err != nil {
		fmt.Fprintf(stderr, "production preflight failed: %v\n", err)
		return 1
	}
	if err := validateSMTPConfig(cfg); err != nil {
		fmt.Fprintf(stderr, "production preflight failed: %v\n", err)
		return 1
	}
	if image := strings.TrimSpace(os.Getenv("PASTEBOX_IMAGE")); !isPinnedImage(image) {
		fmt.Fprintf(stderr, "production preflight failed: PASTEBOX_IMAGE must be a sha-* tag or digest, got %q\n", image)
		return 1
	}
	if err := validateRemoteHTTPSEndpoint(cfg.S3.Endpoint, "PASTEBOX_S3_ENDPOINT"); err != nil {
		fmt.Fprintf(stderr, "production preflight failed: %v\n", err)
		return 1
	}
	if err := validateResticRepository(strings.TrimSpace(os.Getenv("PASTEBOX_RESTIC_REPOSITORY"))); err != nil {
		fmt.Fprintf(stderr, "production preflight failed: %v\n", err)
		return 1
	}
	if err := validateScannerConfig(cfg); err != nil {
		fmt.Fprintf(stderr, "production preflight failed: %v\n", err)
		return 1
	}
	if err := validateBillingConfig(cfg); err != nil {
		fmt.Fprintf(stderr, "production preflight failed: %v\n", err)
		return 1
	}
	if err := validateWALArchiveConfig(); err != nil {
		fmt.Fprintf(stderr, "production preflight failed: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "production preflight passed")
	return 0
}

func isPinnedImage(image string) bool {
	image = strings.TrimSpace(image)
	if image == "" || strings.HasSuffix(image, ":latest") {
		return false
	}
	if strings.Contains(image, "@sha256:") {
		return true
	}

	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon <= lastSlash {
		return false
	}
	return strings.HasPrefix(image[lastColon+1:], "sha-")
}

func validateGoogleOAuthRedirectURL(raw string, publicRaw string) error {
	redirectURL, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || redirectURL.Scheme == "" || redirectURL.Host == "" {
		return fmt.Errorf("PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL must be a valid https URL, got %q", raw)
	}
	if redirectURL.Scheme != "https" {
		return fmt.Errorf("PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL must use https://, got %q", raw)
	}
	if redirectURL.Path != "/api/v1/auth/google/callback" {
		return fmt.Errorf("PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL must end with /api/v1/auth/google/callback, got %q", raw)
	}
	if isLocalHost(redirectURL.Hostname()) {
		return fmt.Errorf("PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL must use the production domain, got local host %q", redirectURL.Hostname())
	}
	publicURL, err := url.Parse(strings.TrimSpace(publicRaw))
	if err != nil || publicURL.Host == "" {
		return fmt.Errorf("PASTEBOX_PUBLIC_URL must be valid before validating Google OAuth redirect URL, got %q", publicRaw)
	}
	if !strings.EqualFold(redirectURL.Hostname(), publicURL.Hostname()) {
		return fmt.Errorf("PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL host must match PASTEBOX_PUBLIC_URL host, got %q", redirectURL.Hostname())
	}
	return nil
}

func validateCORSOrigins(cfg config.Config) error {
	if len(cfg.CORSAllowedOrigins) == 0 {
		return fmt.Errorf("PASTEBOX_CORS_ALLOWED_ORIGINS must include the production UI origin")
	}
	publicURL, err := url.Parse(strings.TrimSpace(cfg.PublicURL))
	if err != nil || publicURL.Scheme == "" || publicURL.Host == "" {
		return fmt.Errorf("PASTEBOX_PUBLIC_URL must be valid before validating CORS origins, got %q", cfg.PublicURL)
	}
	publicOrigin := publicURL.Scheme + "://" + publicURL.Host
	hasPublicOrigin := false
	for _, origin := range cfg.CORSAllowedOrigins {
		parsed, err := url.Parse(strings.TrimSpace(origin))
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("PASTEBOX_CORS_ALLOWED_ORIGINS contains invalid origin %q", origin)
		}
		if parsed.Scheme != "https" {
			return fmt.Errorf("PASTEBOX_CORS_ALLOWED_ORIGINS origins must use https:// in production, got %q", origin)
		}
		if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(origin, "*") {
			return fmt.Errorf("PASTEBOX_CORS_ALLOWED_ORIGINS entries must be exact origins without paths, queries, fragments, or wildcards, got %q", origin)
		}
		if isLocalHost(parsed.Hostname()) {
			return fmt.Errorf("PASTEBOX_CORS_ALLOWED_ORIGINS must not include local hosts in production, got %q", origin)
		}
		if strings.EqualFold(parsed.Scheme+"://"+parsed.Host, publicOrigin) {
			hasPublicOrigin = true
		}
	}
	if !hasPublicOrigin {
		return fmt.Errorf("PASTEBOX_CORS_ALLOWED_ORIGINS must include PASTEBOX_PUBLIC_URL origin %q", publicOrigin)
	}
	return nil
}

func validateRateLimitConfig(cfg config.Config) error {
	if !cfg.RateLimit.Enabled {
		return fmt.Errorf("PASTEBOX_RATE_LIMIT_ENABLED must be true in production")
	}
	if cfg.RateLimit.WindowSeconds <= 0 {
		return fmt.Errorf("PASTEBOX_RATE_LIMIT_WINDOW_SECONDS must be positive")
	}
	limits := map[string]int{
		"PASTEBOX_RATE_LIMIT_AUTH":     cfg.RateLimit.AuthLimit,
		"PASTEBOX_RATE_LIMIT_WRITE":    cfg.RateLimit.WriteLimit,
		"PASTEBOX_RATE_LIMIT_UPLOAD":   cfg.RateLimit.UploadLimit,
		"PASTEBOX_RATE_LIMIT_DOWNLOAD": cfg.RateLimit.DownloadLimit,
		"PASTEBOX_RATE_LIMIT_WEBHOOK":  cfg.RateLimit.WebhookLimit,
	}
	for key, value := range limits {
		if value <= 0 {
			return fmt.Errorf("%s must be positive", key)
		}
	}
	return nil
}

func validatePublicContactEmails(cfg config.Config) error {
	if err := validatePublicEmail("PASTEBOX_SUPPORT_EMAIL", cfg.SupportEmail); err != nil {
		return err
	}
	return validatePublicEmail("PASTEBOX_ABUSE_EMAIL", cfg.AbuseEmail)
}

func validatePublicEmail(key string, value string) error {
	trimmed := strings.TrimSpace(value)
	address, err := mail.ParseAddress(trimmed)
	if err != nil || address.Address != trimmed {
		return fmt.Errorf("%s must be a valid public email address, got %q", key, value)
	}
	_, domain, ok := strings.Cut(address.Address, "@")
	if !ok || isLocalHost(domain) || !strings.Contains(domain, ".") {
		return fmt.Errorf("%s must use a production email domain, got %q", key, value)
	}
	return nil
}

func validateSMTPConfig(cfg config.Config) error {
	if strings.ToLower(strings.TrimSpace(cfg.MailerProvider)) != "smtp" {
		return fmt.Errorf("PASTEBOX_MAILER_PROVIDER must be smtp in production, got %q", cfg.MailerProvider)
	}
	if strings.TrimSpace(cfg.SMTP.Host) == "" {
		return fmt.Errorf("PASTEBOX_SMTP_HOST is required")
	}
	if isLocalHost(cfg.SMTP.Host) {
		return fmt.Errorf("PASTEBOX_SMTP_HOST must point to the production SMTP service, got local host %q", cfg.SMTP.Host)
	}
	rawPort := strings.TrimSpace(os.Getenv("PASTEBOX_SMTP_PORT"))
	port, err := strconv.Atoi(rawPort)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("PASTEBOX_SMTP_PORT must be a valid TCP port, got %q", rawPort)
	}
	if cfg.SMTP.Port != port {
		return fmt.Errorf("PASTEBOX_SMTP_PORT could not be parsed consistently, got %q", rawPort)
	}
	if strings.TrimSpace(cfg.SMTP.Username) == "" || strings.TrimSpace(cfg.SMTP.Password) == "" {
		return fmt.Errorf("PASTEBOX_SMTP_USERNAME and PASTEBOX_SMTP_PASSWORD are required")
	}
	if _, err := mail.ParseAddress(cfg.SMTP.FromEmail); err != nil {
		return fmt.Errorf("PASTEBOX_SMTP_FROM_EMAIL must be a valid email address: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(cfg.SMTP.TLSMode)) {
	case "starttls", "tls":
		return nil
	case "none":
		return fmt.Errorf("PASTEBOX_SMTP_TLS_MODE must be starttls or tls in production, got %q", cfg.SMTP.TLSMode)
	default:
		return fmt.Errorf("PASTEBOX_SMTP_TLS_MODE must be starttls or tls, got %q", cfg.SMTP.TLSMode)
	}
}

func validateRemoteHTTPSEndpoint(raw string, envKey string) error {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return fmt.Errorf("%s must be a valid https URL, got %q", envKey, raw)
	}
	if endpoint.Scheme != "https" {
		return fmt.Errorf("%s must use https:// managed object storage, got %q", envKey, raw)
	}
	host := endpoint.Hostname()
	if isLocalHost(host) {
		return fmt.Errorf("%s must point to off-host managed object storage, got local host %q", envKey, host)
	}
	return nil
}

func validateResticRepository(repository string) error {
	if !strings.HasPrefix(repository, "s3:https://") {
		return fmt.Errorf("PASTEBOX_RESTIC_REPOSITORY must use an off-host S3 HTTPS repository, got %q", repository)
	}
	rawEndpoint := strings.TrimPrefix(repository, "s3:")
	if slash := strings.Index(rawEndpoint[len("https://"):], "/"); slash >= 0 {
		rawEndpoint = rawEndpoint[:len("https://")+slash]
	}
	return validateRemoteHTTPSEndpoint(rawEndpoint, "PASTEBOX_RESTIC_REPOSITORY")
}

func validateScannerConfig(cfg config.Config) error {
	switch strings.ToLower(strings.TrimSpace(cfg.Scanner.Provider)) {
	case "clamav":
		if strings.TrimSpace(cfg.Scanner.ClamAV.Addr) == "" {
			return fmt.Errorf("PASTEBOX_CLAMAV_ADDR is required when PASTEBOX_SCANNER_PROVIDER=clamav")
		}
		host, port, err := net.SplitHostPort(cfg.Scanner.ClamAV.Addr)
		if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
			return fmt.Errorf("PASTEBOX_CLAMAV_ADDR must be host:port, got %q", cfg.Scanner.ClamAV.Addr)
		}
		if cfg.Scanner.ClamAV.Timeout <= 0 {
			return fmt.Errorf("PASTEBOX_CLAMAV_TIMEOUT_SECONDS must be positive")
		}
		return nil
	default:
		return fmt.Errorf("PASTEBOX_SCANNER_PROVIDER must be clamav in production, got %q", cfg.Scanner.Provider)
	}
}

func validateBillingConfig(cfg config.Config) error {
	if !cfg.StripeEnabled {
		return fmt.Errorf("PASTEBOX_STRIPE_ENABLED must be true for first production launch")
	}
	if strings.TrimSpace(cfg.Stripe.WebhookSecret) == "" {
		return fmt.Errorf("PASTEBOX_STRIPE_WEBHOOK_SECRET is required")
	}
	if !strings.HasPrefix(strings.TrimSpace(cfg.Stripe.WebhookSecret), "whsec_") {
		return fmt.Errorf("PASTEBOX_STRIPE_WEBHOOK_SECRET must be a Stripe webhook signing secret")
	}
	if !cfg.EpusdtEnabled {
		return fmt.Errorf("PASTEBOX_EPUSDT_ENABLED must be true for first production launch")
	}
	if strings.TrimSpace(cfg.Epusdt.PID) == "" {
		return fmt.Errorf("PASTEBOX_EPUSDT_PID is required")
	}
	if strings.TrimSpace(cfg.Epusdt.SecretKey) == "" {
		return fmt.Errorf("PASTEBOX_EPUSDT_SECRET_KEY is required")
	}
	return nil
}

func validateWALArchiveConfig() error {
	timeout, err := positiveIntEnv("PASTEBOX_WAL_ARCHIVE_TIMEOUT_SECONDS")
	if err != nil {
		return err
	}
	if timeout > 900 {
		return fmt.Errorf("PASTEBOX_WAL_ARCHIVE_TIMEOUT_SECONDS must be <= 900 to meet the 15 minute RPO target")
	}
	maxAge, err := positiveIntEnv("PASTEBOX_WAL_ARCHIVE_MAX_AGE_SECONDS")
	if err != nil {
		return err
	}
	if maxAge > 900 {
		return fmt.Errorf("PASTEBOX_WAL_ARCHIVE_MAX_AGE_SECONDS must be <= 900 to meet the 15 minute RPO target")
	}
	wait, err := positiveIntEnv("PASTEBOX_WAL_ARCHIVE_WAIT_SECONDS")
	if err != nil {
		return err
	}
	if wait > 900 {
		return fmt.Errorf("PASTEBOX_WAL_ARCHIVE_WAIT_SECONDS must be <= 900 to meet the 15 minute RPO target")
	}
	return nil
}

func positiveIntEnv(key string) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, raw)
	}
	return value, nil
}

func isLocalHost(host string) bool {
	normalized := strings.ToLower(strings.Trim(host, "[]"))
	switch normalized {
	case "", "localhost", "host.docker.internal", "minio":
		return true
	}
	ip := net.ParseIP(normalized)
	return ip != nil && ip.IsLoopback()
}

func runWorker(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("pastebox worker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	once := fs.Bool("once", false, "process one runnable job batch and exit")
	batchSize := fs.Int("batch-size", 25, "maximum runnable jobs to process per batch")
	pollInterval := fs.Duration("poll-interval", 30*time.Second, "delay between worker polls")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	cfg := config.FromEnv()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer startupCancel()
	service, pool, _, err := newProductionService(startupCtx, cfg)
	if err != nil {
		logger.Error("worker service setup failed", "error", err)
		return 1
	}
	defer pool.Close()

	mailSender, err := mailer.NewSender(cfg, logger)
	if err != nil {
		logger.Error("worker mailer setup failed", "error", err)
		return 1
	}
	scan, err := scanner.New(cfg.Scanner)
	if err != nil {
		logger.Error("worker scanner setup failed", "error", err)
		return 1
	}

	runner := worker.NewRunnerWithMail(postgres.NewJobStore(pool), postgres.NewMailStore(pool), mailSender, service, worker.Config{
		BatchSize:    *batchSize,
		PollInterval: *pollInterval,
		Logger:       logger,
		Scanner:      scan,
	})

	if *once {
		summary, err := runner.RunOnce(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "worker run once failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "worker processed seen=%d completed=%d retried=%d failed=%d mailSeen=%d mailSent=%d mailRetried=%d mailFailed=%d\n", summary.Seen, summary.Completed, summary.Retried, summary.Failed, summary.MailSeen, summary.MailSent, summary.MailRetried, summary.MailFailed)
		return 0
	}

	logger.Info("pastebox worker started", "env", cfg.AppEnv, "batchSize", *batchSize, "pollInterval", pollInterval.String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runner.Run(ctx); err != nil {
		logger.Error("pastebox worker failed", "error", err)
		return 1
	}

	logger.Info("pastebox worker stopped")
	return 0
}

func runAdminCreate(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("pastebox admin create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "admin email")
	password := fs.String("password", "", "admin password")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*email) == "" || strings.TrimSpace(*password) == "" {
		fmt.Fprintln(stderr, "--email and --password are required")
		return 2
	}

	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	service := app.New(cfg)
	admin, err := service.SeedAdmin(*email, *password)
	if err != nil {
		fmt.Fprintf(stderr, "admin create failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "created bootstrap admin %s\n", admin.Email)
	fmt.Fprintln(stdout, "set these environment variables before starting pastebox:")
	fmt.Fprintf(stdout, "PASTEBOX_BOOTSTRAP_ADMIN_EMAIL=%s\n", admin.Email)
	fmt.Fprintln(stdout, "PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD=<the password you provided>")
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  pastebox")
	fmt.Fprintln(w, "  pastebox api")
	fmt.Fprintln(w, "  pastebox admin create --email <email> --password <password>")
	fmt.Fprintln(w, "  pastebox migrate status|up")
	fmt.Fprintln(w, "  pastebox preflight production")
	fmt.Fprintln(w, "  pastebox worker")
}

func printAdminUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  pastebox admin create --email <email> --password <password>")
}

func printMigrateUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  pastebox migrate status")
	fmt.Fprintln(w, "  pastebox migrate up")
}

func printPreflightUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  pastebox preflight production")
}

func printWorkerUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  pastebox worker [--once] [--batch-size <n>] [--poll-interval <duration>]")
}
