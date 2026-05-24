package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AppName    string
	AppEnv     string
	HTTPAddr   string
	PublicURL  string
	LogLevel   slog.Level
	CSRFSecret string

	DatabaseURL string
	RedisAddr   string

	S3          S3Config
	GoogleOAuth GoogleOAuthConfig

	MailerProvider string
	SMTP           SMTPConfig
	DevAuthTokens  bool
	StripeEnabled  bool
	EpusdtEnabled  bool

	BootstrapAdminEmail    string
	BootstrapAdminPassword string
}

type S3Config struct {
	Endpoint     string
	Bucket       string
	Region       string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
}

type GoogleOAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

type SMTPConfig struct {
	Host      string
	Port      int
	Username  string
	Password  string
	FromEmail string
	FromName  string
	TLSMode   string
}

func FromEnv() Config {
	publicURL := envString("PASTEBOX_PUBLIC_URL", "http://localhost:5173")
	return Config{
		AppName:    envString("PASTEBOX_APP_NAME", "PasteBox"),
		AppEnv:     envString("PASTEBOX_APP_ENV", "development"),
		HTTPAddr:   envString("PASTEBOX_HTTP_ADDR", ":8080"),
		PublicURL:  publicURL,
		LogLevel:   envLogLevel("PASTEBOX_LOG_LEVEL", slog.LevelInfo),
		CSRFSecret: envString("PASTEBOX_CSRF_SECRET", "development-csrf-secret"),

		DatabaseURL: envString("PASTEBOX_DATABASE_URL", "postgres://pastebox:pastebox@localhost:5432/pastebox?sslmode=disable"),
		RedisAddr:   envString("PASTEBOX_REDIS_ADDR", "localhost:6379"),

		S3: S3Config{
			Endpoint:     envString("PASTEBOX_S3_ENDPOINT", "http://localhost:9000"),
			Bucket:       envString("PASTEBOX_S3_BUCKET", "pastebox"),
			Region:       envString("PASTEBOX_S3_REGION", "us-east-1"),
			AccessKey:    envString("PASTEBOX_S3_ACCESS_KEY", "pastebox"),
			SecretKey:    envString("PASTEBOX_S3_SECRET_KEY", "pastebox-secret"),
			UsePathStyle: envBool("PASTEBOX_S3_USE_PATH_STYLE", true),
		},
		GoogleOAuth: GoogleOAuthConfig{
			ClientID:     envString("PASTEBOX_GOOGLE_OAUTH_CLIENT_ID", ""),
			ClientSecret: envString("PASTEBOX_GOOGLE_OAUTH_CLIENT_SECRET", ""),
			RedirectURL:  envString("PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL", strings.TrimRight(publicURL, "/")+"/api/v1/auth/google/callback"),
		},

		MailerProvider: envString("PASTEBOX_MAILER_PROVIDER", "log"),
		SMTP: SMTPConfig{
			Host:      envString("PASTEBOX_SMTP_HOST", "localhost"),
			Port:      envInt("PASTEBOX_SMTP_PORT", 1025),
			Username:  envString("PASTEBOX_SMTP_USERNAME", ""),
			Password:  envString("PASTEBOX_SMTP_PASSWORD", ""),
			FromEmail: envString("PASTEBOX_SMTP_FROM_EMAIL", "noreply@localhost"),
			FromName:  envString("PASTEBOX_SMTP_FROM_NAME", "PasteBox"),
			TLSMode:   envString("PASTEBOX_SMTP_TLS_MODE", "starttls"),
		},
		DevAuthTokens: envBool("PASTEBOX_DEV_AUTH_TOKENS", false),
		StripeEnabled: envBool("PASTEBOX_STRIPE_ENABLED", false),
		EpusdtEnabled: envBool("PASTEBOX_EPUSDT_ENABLED", false),

		BootstrapAdminEmail:    envString("PASTEBOX_BOOTSTRAP_ADMIN_EMAIL", ""),
		BootstrapAdminPassword: envString("PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD", ""),
	}
}

func (c Config) ExposeDevAuthTokens() bool {
	return c.AppEnv != "production" && c.DevAuthTokens
}

func envString(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envLogLevel(key string, fallback slog.Level) slog.Level {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return fallback
	}
	return level
}
