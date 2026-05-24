package config

import (
	"log/slog"
	"os"
	"strconv"
)

type Config struct {
	AppName   string
	AppEnv    string
	HTTPAddr  string
	PublicURL string
	LogLevel  slog.Level

	DatabaseURL string
	RedisAddr   string

	S3 S3Config

	MailerProvider string
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

func FromEnv() Config {
	return Config{
		AppName:   envString("PASTEBOX_APP_NAME", "PasteBox"),
		AppEnv:    envString("PASTEBOX_APP_ENV", "development"),
		HTTPAddr:  envString("PASTEBOX_HTTP_ADDR", ":8080"),
		PublicURL: envString("PASTEBOX_PUBLIC_URL", "http://localhost:5173"),
		LogLevel:  envLogLevel("PASTEBOX_LOG_LEVEL", slog.LevelInfo),

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

		MailerProvider: envString("PASTEBOX_MAILER_PROVIDER", "log"),
		DevAuthTokens:  envBool("PASTEBOX_DEV_AUTH_TOKENS", false),
		StripeEnabled:  envBool("PASTEBOX_STRIPE_ENABLED", false),
		EpusdtEnabled:  envBool("PASTEBOX_EPUSDT_ENABLED", false),

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
