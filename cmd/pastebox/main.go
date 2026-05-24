package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"pastebox/internal/app"
	"pastebox/internal/config"
	"pastebox/internal/httpserver"
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

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpserver.New(cfg, logger),
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
			fmt.Fprintln(stdout, "database migrations are not configured yet")
			fmt.Fprintln(stdout, "Phase 1 must add PostgreSQL migrations before production traffic is allowed")
			return 0
		case "up":
			fmt.Fprintln(stderr, "database migrations are not implemented yet")
			fmt.Fprintln(stderr, "Refusing to report success until Phase 1 adds real migration files and a runner.")
			return 1
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
		fmt.Fprintf(stderr, "production preflight failed: PASTEBOX_IMAGE must be a pinned non-latest tag or digest, got %q\n", image)
		return 1
	}

	fmt.Fprintln(stdout, "production preflight passed")
	return 0
}

func isPinnedImage(image string) bool {
	if image == "" || strings.HasSuffix(image, ":latest") {
		return false
	}
	if strings.Contains(image, "@sha256:") {
		return true
	}

	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	return lastColon > lastSlash
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
