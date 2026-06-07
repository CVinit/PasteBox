package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AppName            string
	AppEnv             string
	HTTPAddr           string
	PublicURL          string
	LogLevel           slog.Level
	SupportEmail       string
	AbuseEmail         string
	CSRFSecret         string
	MetricsToken       string
	CORSAllowedOrigins []string
	RateLimit          RateLimitConfig

	DatabaseURL                  string
	RedisAddr                    string
	WorkerID                     string
	WorkerHeartbeatMaxAgeSeconds int

	S3          S3Config
	Scanner     ScannerConfig
	GoogleOAuth GoogleOAuthConfig
	Turnstile   TurnstileConfig
	Telegram    TelegramConfig

	MailerProvider string
	SMTP           SMTPConfig
	DevAuthTokens  bool
	StripeEnabled  bool
	EpusdtEnabled  bool
	Stripe         StripeConfig
	Epusdt         EpusdtConfig

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

type ScannerConfig struct {
	Provider string
	ClamAV   ClamAVConfig
}

type ClamAVConfig struct {
	Addr    string
	Timeout int
}

type GoogleOAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

type TurnstileConfig struct {
	SiteKey   string
	SecretKey string
	VerifyURL string
}

type TelegramConfig struct {
	BotToken   string
	ChatID     string
	APIBaseURL string
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

type StripeConfig struct {
	WebhookSecret       string
	CheckoutURLTemplate string
}

type EpusdtConfig struct {
	PID                 string
	SecretKey           string
	CheckoutURLTemplate string
	Address             string
	Chain               string
}

type RateLimitConfig struct {
	Enabled       bool
	WindowSeconds int
	AuthLimit     int
	WriteLimit    int
	UploadLimit   int
	DownloadLimit int
	WebhookLimit  int
}

func FromEnv() Config {
	publicURL := envString("PASTEBOX_PUBLIC_URL", "http://localhost:5173")
	return Config{
		AppName:            envString("PASTEBOX_APP_NAME", "PasteBox"),
		AppEnv:             envString("PASTEBOX_APP_ENV", "development"),
		HTTPAddr:           envString("PASTEBOX_HTTP_ADDR", ":8080"),
		PublicURL:          publicURL,
		LogLevel:           envLogLevel("PASTEBOX_LOG_LEVEL", slog.LevelInfo),
		SupportEmail:       envString("PASTEBOX_SUPPORT_EMAIL", "support@localhost"),
		AbuseEmail:         envString("PASTEBOX_ABUSE_EMAIL", "abuse@localhost"),
		CSRFSecret:         envString("PASTEBOX_CSRF_SECRET", "development-csrf-secret"),
		MetricsToken:       envString("PASTEBOX_METRICS_TOKEN", ""),
		CORSAllowedOrigins: envCSV("PASTEBOX_CORS_ALLOWED_ORIGINS", originFromURL(publicURL)),
		RateLimit: RateLimitConfig{
			Enabled:       envBool("PASTEBOX_RATE_LIMIT_ENABLED", true),
			WindowSeconds: envInt("PASTEBOX_RATE_LIMIT_WINDOW_SECONDS", 60),
			AuthLimit:     envInt("PASTEBOX_RATE_LIMIT_AUTH", 60),
			WriteLimit:    envInt("PASTEBOX_RATE_LIMIT_WRITE", 300),
			UploadLimit:   envInt("PASTEBOX_RATE_LIMIT_UPLOAD", 120),
			DownloadLimit: envInt("PASTEBOX_RATE_LIMIT_DOWNLOAD", 600),
			WebhookLimit:  envInt("PASTEBOX_RATE_LIMIT_WEBHOOK", 300),
		},

		DatabaseURL:                  envString("PASTEBOX_DATABASE_URL", "postgres://pastebox:pastebox@localhost:5432/pastebox?sslmode=disable"),
		RedisAddr:                    envString("PASTEBOX_REDIS_ADDR", "localhost:6379"),
		WorkerID:                     envString("PASTEBOX_WORKER_ID", ""),
		WorkerHeartbeatMaxAgeSeconds: envInt("PASTEBOX_WORKER_HEARTBEAT_MAX_AGE_SECONDS", 120),

		S3: S3Config{
			Endpoint:     envString("PASTEBOX_S3_ENDPOINT", "http://localhost:9000"),
			Bucket:       envString("PASTEBOX_S3_BUCKET", "pastebox"),
			Region:       envString("PASTEBOX_S3_REGION", "us-east-1"),
			AccessKey:    envString("PASTEBOX_S3_ACCESS_KEY", "pastebox"),
			SecretKey:    envString("PASTEBOX_S3_SECRET_KEY", "pastebox-secret"),
			UsePathStyle: envBool("PASTEBOX_S3_USE_PATH_STYLE", true),
		},
		Scanner: ScannerConfig{
			Provider: envString("PASTEBOX_SCANNER_PROVIDER", "heuristic"),
			ClamAV: ClamAVConfig{
				Addr:    envString("PASTEBOX_CLAMAV_ADDR", "localhost:3310"),
				Timeout: envInt("PASTEBOX_CLAMAV_TIMEOUT_SECONDS", 30),
			},
		},
		GoogleOAuth: GoogleOAuthConfig{
			ClientID:     envString("PASTEBOX_GOOGLE_OAUTH_CLIENT_ID", ""),
			ClientSecret: envString("PASTEBOX_GOOGLE_OAUTH_CLIENT_SECRET", ""),
			RedirectURL:  envString("PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL", strings.TrimRight(publicURL, "/")+"/api/v1/auth/google/callback"),
		},
		Turnstile: TurnstileConfig{
			SiteKey:   envString("PASTEBOX_TURNSTILE_SITE_KEY", ""),
			SecretKey: envString("PASTEBOX_TURNSTILE_SECRET_KEY", ""),
			VerifyURL: envString("PASTEBOX_TURNSTILE_VERIFY_URL", "https://challenges.cloudflare.com/turnstile/v0/siteverify"),
		},
		Telegram: TelegramConfig{
			BotToken:   envString("PASTEBOX_TELEGRAM_BOT_TOKEN", ""),
			ChatID:     envString("PASTEBOX_TELEGRAM_CHAT_ID", ""),
			APIBaseURL: envString("PASTEBOX_TELEGRAM_API_BASE_URL", "https://api.telegram.org"),
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
		Stripe: StripeConfig{
			WebhookSecret:       envString("PASTEBOX_STRIPE_WEBHOOK_SECRET", ""),
			CheckoutURLTemplate: envString("PASTEBOX_STRIPE_CHECKOUT_URL_TEMPLATE", ""),
		},
		Epusdt: EpusdtConfig{
			PID:                 envString("PASTEBOX_EPUSDT_PID", ""),
			SecretKey:           envString("PASTEBOX_EPUSDT_SECRET_KEY", ""),
			CheckoutURLTemplate: envString("PASTEBOX_EPUSDT_CHECKOUT_URL_TEMPLATE", ""),
			Address:             envString("PASTEBOX_EPUSDT_ADDRESS", ""),
			Chain:               envString("PASTEBOX_EPUSDT_CHAIN", "USDT-TRC20"),
		},

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

func envCSV(key string, fallback string) []string {
	value := os.Getenv(key)
	if value == "" {
		value = fallback
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func originFromURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	scheme, rest, ok := strings.Cut(trimmed, "://")
	if !ok || scheme == "" || rest == "" {
		return trimmed
	}
	if slash := strings.Index(rest, "/"); slash >= 0 {
		rest = rest[:slash]
	}
	return scheme + "://" + rest
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
