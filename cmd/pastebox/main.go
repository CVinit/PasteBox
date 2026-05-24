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
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
	"pastebox/internal/config"
	"pastebox/internal/httpserver"
	"pastebox/internal/objectstore"
	"pastebox/internal/postgres"
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

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Error("postgres pool setup failed", "error", err)
		return 1
	}
	defer pool.Close()

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer startupCancel()
	objects, err := objectstore.NewS3Store(cfg.S3)
	if err != nil {
		logger.Error("object store setup failed", "error", err)
		return 1
	}
	service, err := app.NewWithStorage(startupCtx, cfg, app.Stores{
		Auth: app.AuthStores{
			Users:         postgres.NewUserStore(pool),
			Sessions:      postgres.NewSessionStore(pool),
			Tokens:        postgres.NewAuthTokenStore(pool),
			LoginFailures: postgres.NewLoginFailureStore(pool),
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
		logger.Error("service setup failed", "error", err)
		return 1
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpserver.NewWithService(cfg, logger, service),
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
		"PASTEBOX_BOOTSTRAP_ADMIN_EMAIL",
		"PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD",
		"PASTEBOX_RESTIC_REPOSITORY",
		"PASTEBOX_RESTIC_PASSWORD",
		"PASTEBOX_BACKUP_S3_ACCESS_KEY",
		"PASTEBOX_BACKUP_S3_SECRET_KEY",
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
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			printWorkerUsage(stdout)
			return 0
		default:
			fmt.Fprintf(stderr, "unknown worker command %q\n", args[0])
			printWorkerUsage(stderr)
			return 2
		}
	}

	_ = stdout
	_ = stderr

	cfg := config.FromEnv()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	logger.Info("pastebox worker idle", "env", cfg.AppEnv, "reason", "durable queues are scheduled for Phase 3")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

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
	fmt.Fprintln(w, "  pastebox worker")
}
