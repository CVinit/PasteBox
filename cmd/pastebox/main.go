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
		case "admin":
			return runAdmin(args[1:], stdout, stderr)
		case "help", "-h", "--help":
			printUsage(stdout)
			return 0
		default:
			fmt.Fprintf(stderr, "unknown command %q\n", args[0])
			printUsage(stderr)
			return 2
		}
	}

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
	fmt.Fprintln(w, "  pastebox admin create --email <email> --password <password>")
}

func printAdminUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  pastebox admin create --email <email> --password <password>")
}
