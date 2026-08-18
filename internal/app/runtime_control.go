package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"pastebox/internal/config"
	"pastebox/internal/plans"
)

const (
	RuntimeLogLevelDebug = "debug"
	RuntimeLogLevelInfo  = "info"
	RuntimeLogLevelWarn  = "warn"
	RuntimeLogLevelError = "error"

	runtimeConfigID  = "default"
	guestEmailDomain = "guest.localhost"
)

type RuntimeConfig struct {
	ID             string                 `json:"id"`
	LogLevel       string                 `json:"logLevel"`
	GuestUploads   GuestUploadConfig      `json:"guestUploads"`
	Registration   RegistrationConfig     `json:"registration"`
	RateLimits     RuntimeRateLimitConfig `json:"rateLimits"`
	Limits         LimitConfig            `json:"limits"`
	ProviderStatus ProviderStatus         `json:"providerStatus"`
	Alerts         AlertConfig            `json:"alerts"`
	Managed        config.ManagedConfig   `json:"managed"`
	UpdatedAt      time.Time              `json:"updatedAt"`
}

type GuestUploadConfig struct {
	Enabled                  bool  `json:"enabled"`
	RequireTurnstile         bool  `json:"requireTurnstile"`
	RetentionSeconds         int64 `json:"retentionSeconds"`
	ActivePasteLimit         int   `json:"activePasteLimit"`
	ActiveStorageBytes       int64 `json:"activeStorageBytes"`
	SingleTextBytes          int64 `json:"singleTextBytes"`
	SingleFileBytes          int64 `json:"singleFileBytes"`
	SinglePasteBytes         int64 `json:"singlePasteBytes"`
	AttachmentsPerPasteLimit int   `json:"attachmentsPerPasteLimit"`
	DailyUploadBytes         int64 `json:"dailyUploadBytes"`
	DailyShareDownloadBytes  int64 `json:"dailyShareDownloadBytes"`
	ShareDownloadsEnabled    bool  `json:"shareDownloadsEnabled"`
}

type LimitConfig struct {
	FreePlanID  string   `json:"freePlanId"`
	PaidPlanIDs []string `json:"paidPlanIds"`
}

type RegistrationConfig struct {
	AllowedDomains           []string `json:"allowedDomains"`
	RequireEmailVerification bool     `json:"requireEmailVerification"`
	RequireTurnstile         bool     `json:"requireTurnstile"`
	TurnstileSiteKey         string   `json:"turnstileSiteKey,omitempty"`
}

type RuntimeRateLimitConfig struct {
	Enabled                bool `json:"enabled"`
	WindowSeconds          int  `json:"windowSeconds"`
	EmailVerificationLimit int  `json:"emailVerificationLimit"`
	RegisterLimit          int  `json:"registerLimit"`
	LoginLimit             int  `json:"loginLimit"`
	WriteLimit             int  `json:"writeLimit"`
	UploadLimit            int  `json:"uploadLimit"`
	ShareCreateLimit       int  `json:"shareCreateLimit"`
	ShareAccessLimit       int  `json:"shareAccessLimit"`
	DownloadLimit          int  `json:"downloadLimit"`
	WebhookLimit           int  `json:"webhookLimit"`
}

type ProviderStatus struct {
	Mailer    ProviderConfigStatus `json:"mailer"`
	Google    ProviderConfigStatus `json:"google"`
	GitHub    ProviderConfigStatus `json:"github"`
	Turnstile ProviderConfigStatus `json:"turnstile"`
	Telegram  ProviderConfigStatus `json:"telegram"`
	S3        ProviderConfigStatus `json:"s3"`
	Stripe    ProviderConfigStatus `json:"stripe"`
	Epusdt    ProviderConfigStatus `json:"epusdt"`
}

type ProviderConfigStatus struct {
	Provider        string            `json:"provider"`
	Configured      bool              `json:"configured"`
	SecretManaged   bool              `json:"secretManaged"`
	RequiredEnv     []string          `json:"requiredEnv"`
	MissingEnv      []string          `json:"missingEnv"`
	NonSensitive    map[string]string `json:"nonSensitive,omitempty"`
	LastTestStatus  string            `json:"lastTestStatus,omitempty"`
	LastTestMessage string            `json:"lastTestMessage,omitempty"`
}

type AlertConfig struct {
	Enabled                     bool    `json:"enabled"`
	TelegramEnabled             bool    `json:"telegramEnabled"`
	Silent                      bool    `json:"silent"`
	CooldownSeconds             int64   `json:"cooldownSeconds"`
	CPUPercentThreshold         float64 `json:"cpuPercentThreshold"`
	MemoryPercentThreshold      float64 `json:"memoryPercentThreshold"`
	DiskPercentThreshold        float64 `json:"diskPercentThreshold"`
	ObjectStorageBytesThreshold int64   `json:"objectStorageBytesThreshold"`
	ScanFailureDepthThreshold   int     `json:"scanFailureDepthThreshold"`
	FailedJobDepthThreshold     int     `json:"failedJobDepthThreshold"`
	MailFailedDepthThreshold    int     `json:"mailFailedDepthThreshold"`
	ReportsOpenThreshold        int     `json:"reportsOpenThreshold"`
}

type RuntimeConfigStore interface {
	RuntimeConfig(ctx context.Context) (RuntimeConfig, config.ManagedSecrets, bool, error)
	SaveRuntimeConfig(ctx context.Context, cfg RuntimeConfig, secrets config.ManagedSecrets) error
}

type RuntimeConfigAuditStore interface {
	SaveRuntimeConfigWithAudit(ctx context.Context, cfg RuntimeConfig, secrets config.ManagedSecrets, audit AuditLog) error
}

type RuntimeConfigChangeHook func(RuntimeConfig, config.Config) error

type CatalogWriter interface {
	SaveCatalog(ctx context.Context, catalog plans.Catalog) error
}

type RuntimeConfigPatch struct {
	LogLevel     *string                      `json:"logLevel,omitempty"`
	GuestUploads *GuestUploadConfigPatch      `json:"guestUploads,omitempty"`
	Registration *RegistrationConfigPatch     `json:"registration,omitempty"`
	RateLimits   *RuntimeRateLimitConfigPatch `json:"rateLimits,omitempty"`
	Alerts       *AlertConfigPatch            `json:"alerts,omitempty"`
}

type ManagedConfigView struct {
	Config    config.ManagedConfig `json:"config"`
	Secrets   ManagedSecretStatus  `json:"secrets"`
	UpdatedAt time.Time            `json:"updatedAt"`
}

type ManagedConfigUpdate struct {
	Config  config.ManagedConfig `json:"config"`
	Secrets ManagedSecretPatch   `json:"secrets"`
}

type ManagedSecretStatus struct {
	S3AccessKey         bool `json:"s3AccessKey"`
	S3SecretKey         bool `json:"s3SecretKey"`
	GoogleClientSecret  bool `json:"googleClientSecret"`
	GitHubClientSecret  bool `json:"githubClientSecret"`
	TurnstileSecretKey  bool `json:"turnstileSecretKey"`
	TelegramBotToken    bool `json:"telegramBotToken"`
	SMTPPassword        bool `json:"smtpPassword"`
	StripeWebhookSecret bool `json:"stripeWebhookSecret"`
	EpusdtSecretKey     bool `json:"epusdtSecretKey"`
}

type ManagedSecretPatch struct {
	S3AccessKey         *string `json:"s3AccessKey,omitempty"`
	S3SecretKey         *string `json:"s3SecretKey,omitempty"`
	GoogleClientSecret  *string `json:"googleClientSecret,omitempty"`
	GitHubClientSecret  *string `json:"githubClientSecret,omitempty"`
	TurnstileSecretKey  *string `json:"turnstileSecretKey,omitempty"`
	TelegramBotToken    *string `json:"telegramBotToken,omitempty"`
	SMTPPassword        *string `json:"smtpPassword,omitempty"`
	StripeWebhookSecret *string `json:"stripeWebhookSecret,omitempty"`
	EpusdtSecretKey     *string `json:"epusdtSecretKey,omitempty"`
}

type GuestUploadConfigPatch struct {
	Enabled                  *bool  `json:"enabled,omitempty"`
	RequireTurnstile         *bool  `json:"requireTurnstile,omitempty"`
	RetentionSeconds         *int64 `json:"retentionSeconds,omitempty"`
	ActivePasteLimit         *int   `json:"activePasteLimit,omitempty"`
	ActiveStorageBytes       *int64 `json:"activeStorageBytes,omitempty"`
	SingleTextBytes          *int64 `json:"singleTextBytes,omitempty"`
	SingleFileBytes          *int64 `json:"singleFileBytes,omitempty"`
	SinglePasteBytes         *int64 `json:"singlePasteBytes,omitempty"`
	AttachmentsPerPasteLimit *int   `json:"attachmentsPerPasteLimit,omitempty"`
	DailyUploadBytes         *int64 `json:"dailyUploadBytes,omitempty"`
	DailyShareDownloadBytes  *int64 `json:"dailyShareDownloadBytes,omitempty"`
	ShareDownloadsEnabled    *bool  `json:"shareDownloadsEnabled,omitempty"`
}

type RegistrationConfigPatch struct {
	AllowedDomains           *[]string `json:"allowedDomains,omitempty"`
	RequireEmailVerification *bool     `json:"requireEmailVerification,omitempty"`
	RequireTurnstile         *bool     `json:"requireTurnstile,omitempty"`
	TurnstileSiteKey         *string   `json:"turnstileSiteKey,omitempty"`
}

type RuntimeRateLimitConfigPatch struct {
	Enabled                *bool `json:"enabled,omitempty"`
	WindowSeconds          *int  `json:"windowSeconds,omitempty"`
	EmailVerificationLimit *int  `json:"emailVerificationLimit,omitempty"`
	RegisterLimit          *int  `json:"registerLimit,omitempty"`
	LoginLimit             *int  `json:"loginLimit,omitempty"`
	WriteLimit             *int  `json:"writeLimit,omitempty"`
	UploadLimit            *int  `json:"uploadLimit,omitempty"`
	ShareCreateLimit       *int  `json:"shareCreateLimit,omitempty"`
	ShareAccessLimit       *int  `json:"shareAccessLimit,omitempty"`
	DownloadLimit          *int  `json:"downloadLimit,omitempty"`
	WebhookLimit           *int  `json:"webhookLimit,omitempty"`
}

type AlertConfigPatch struct {
	Enabled                     *bool    `json:"enabled,omitempty"`
	TelegramEnabled             *bool    `json:"telegramEnabled,omitempty"`
	Silent                      *bool    `json:"silent,omitempty"`
	CooldownSeconds             *int64   `json:"cooldownSeconds,omitempty"`
	CPUPercentThreshold         *float64 `json:"cpuPercentThreshold,omitempty"`
	MemoryPercentThreshold      *float64 `json:"memoryPercentThreshold,omitempty"`
	DiskPercentThreshold        *float64 `json:"diskPercentThreshold,omitempty"`
	ObjectStorageBytesThreshold *int64   `json:"objectStorageBytesThreshold,omitempty"`
	ScanFailureDepthThreshold   *int     `json:"scanFailureDepthThreshold,omitempty"`
	FailedJobDepthThreshold     *int     `json:"failedJobDepthThreshold,omitempty"`
	MailFailedDepthThreshold    *int     `json:"mailFailedDepthThreshold,omitempty"`
	ReportsOpenThreshold        *int     `json:"reportsOpenThreshold,omitempty"`
}

type AdminPlanUpdate struct {
	Plans  []plans.Plan  `json:"plans"`
	Prices []plans.Price `json:"prices"`
}

type RuntimeResourceSnapshot struct {
	CollectedAt              time.Time `json:"collectedAt"`
	CPUPercent               float64   `json:"cpuPercent"`
	MemoryUsedBytes          uint64    `json:"memoryUsedBytes"`
	MemoryTotalBytes         uint64    `json:"memoryTotalBytes"`
	MemoryPercent            float64   `json:"memoryPercent"`
	DiskUsedBytes            uint64    `json:"diskUsedBytes"`
	DiskTotalBytes           uint64    `json:"diskTotalBytes"`
	DiskPercent              float64   `json:"diskPercent"`
	ObjectStorageBytes       int64     `json:"objectStorageBytes"`
	ObjectStorageObjectCount int       `json:"objectStorageObjectCount"`
}

type RuntimePanel struct {
	Config      RuntimeConfig           `json:"config"`
	Resources   RuntimeResourceSnapshot `json:"resources"`
	Operational OperationalMetrics      `json:"operational"`
	Alerts      []AlertEvent            `json:"alerts"`
}

type ManualWorkItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	TargetID  string    `json:"targetId"`
	Status    string    `json:"status"`
	Risk      string    `json:"risk,omitempty"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type GuestCreatePasteInput struct {
	Token            string
	Title            string
	Text             string
	Tags             []string
	ExpiresInSeconds int64
	TurnstileToken   string
	RemoteIP         string
}

type AlertSender interface {
	SendAlert(ctx context.Context, message string, silent bool) error
}

type AlertEvent struct {
	ID          string     `json:"id"`
	Fingerprint string     `json:"fingerprint"`
	Level       string     `json:"level"`
	Message     string     `json:"message"`
	Status      string     `json:"status"`
	LastError   string     `json:"lastError,omitempty"`
	SentAt      *time.Time `json:"sentAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type AlertEventStore interface {
	CreateAlertEvent(ctx context.Context, event AlertEvent) error
	UpdateAlertEvent(ctx context.Context, event AlertEvent) error
	ListAlertEvents(ctx context.Context, limit int) ([]AlertEvent, error)
}

type TurnstileVerifier interface {
	Verify(ctx context.Context, token string, remoteIP string) error
}

type turnstileVerifier struct {
	cfg    config.TurnstileConfig
	client *http.Client
}

func NewTurnstileVerifier(cfg config.Config) TurnstileVerifier {
	if cfg.AppEnv == "production" {
		cfg.Turnstile.VerifyURL = config.DefaultTurnstileVerifyURL
	}
	return &turnstileVerifier{
		cfg:    cfg.Turnstile,
		client: newSensitiveHTTPClient(5 * time.Second),
	}
}

func (v *turnstileVerifier) Verify(ctx context.Context, token string, remoteIP string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return E(http.StatusForbidden, "turnstile_required", "Turnstile token is required")
	}
	if len(token) > 2048 {
		return E(http.StatusForbidden, "turnstile_invalid", "Turnstile token is invalid")
	}
	secret := strings.TrimSpace(v.cfg.SecretKey)
	if secret == "" {
		return E(http.StatusServiceUnavailable, "turnstile_not_configured", "Turnstile secret is not configured")
	}
	verifyURL := strings.TrimSpace(v.cfg.VerifyURL)
	if verifyURL == "" {
		verifyURL = config.DefaultTurnstileVerifyURL
	}
	payload := map[string]string{"secret": secret, "response": token}
	if ip := strings.TrimSpace(remoteIP); ip != "" && net.ParseIP(ip) != nil {
		payload["remoteip"] = ip
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, verifyURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := v.client.Do(req)
	if err != nil {
		return E(http.StatusGatewayTimeout, "turnstile_timeout", "Turnstile verification timed out")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return E(http.StatusForbidden, "turnstile_failed", "Turnstile verification failed")
	}
	var decoded struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return E(http.StatusForbidden, "turnstile_failed", "Turnstile verification failed")
	}
	if !decoded.Success {
		code := "turnstile_failed"
		for _, item := range decoded.ErrorCodes {
			if item == "timeout-or-duplicate" {
				code = "turnstile_timeout_or_duplicate"
				break
			}
		}
		return E(http.StatusForbidden, code, "Turnstile verification failed")
	}
	return nil
}

type TelegramSender struct {
	cfg    config.TelegramConfig
	client *http.Client
}

func NewTelegramSender(cfg config.Config) AlertSender {
	if cfg.AppEnv == "production" {
		cfg.Telegram.APIBaseURL = config.DefaultTelegramAPIBaseURL
	}
	return &TelegramSender{cfg: cfg.Telegram, client: newSensitiveHTTPClient(10 * time.Second)}
}

func (s *TelegramSender) SendAlert(ctx context.Context, message string, silent bool) error {
	token := strings.TrimSpace(s.cfg.BotToken)
	chatID := strings.TrimSpace(s.cfg.ChatID)
	if token == "" || chatID == "" {
		return E(http.StatusServiceUnavailable, "telegram_not_configured", "Telegram bot token and chat ID are required")
	}
	base := strings.TrimRight(strings.TrimSpace(s.cfg.APIBaseURL), "/")
	if base == "" {
		base = config.DefaultTelegramAPIBaseURL
	}
	payload := map[string]any{
		"chat_id":              chatID,
		"text":                 sanitizeAlertMessage(message, token),
		"disable_notification": silent,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("telegram sendMessage returned HTTP %d", res.StatusCode)
	}
	return nil
}

func newSensitiveHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func defaultRuntimeConfig(cfg config.Config) RuntimeConfig {
	turnstileConfigured := strings.TrimSpace(cfg.Turnstile.SiteKey) != "" && strings.TrimSpace(cfg.Turnstile.SecretKey) != ""
	managed, _ := config.ManagedFromConfig(cfg)
	return RuntimeConfig{
		ID:       runtimeConfigID,
		LogLevel: runtimeLogLevelFromConfig(cfg),
		GuestUploads: GuestUploadConfig{
			Enabled:                  true,
			RequireTurnstile:         false,
			RetentionSeconds:         6 * 60 * 60,
			ActivePasteLimit:         5,
			ActiveStorageBytes:       50 * 1024 * 1024,
			SingleTextBytes:          64 * 1024,
			SingleFileBytes:          10 * 1024 * 1024,
			SinglePasteBytes:         15 * 1024 * 1024,
			AttachmentsPerPasteLimit: 3,
			DailyUploadBytes:         100 * 1024 * 1024,
			DailyShareDownloadBytes:  100 * 1024 * 1024,
			ShareDownloadsEnabled:    true,
		},
		Registration: RegistrationConfig{
			AllowedDomains:           []string{"gmail.com", "outlook.com", "hotmail.com", "icloud.com", "qq.com", "163.com", "example.com", "example.org"},
			RequireEmailVerification: true,
			RequireTurnstile:         turnstileConfigured,
			TurnstileSiteKey:         strings.TrimSpace(cfg.Turnstile.SiteKey),
		},
		RateLimits: RuntimeRateLimitConfig{
			Enabled:                cfg.RateLimit.Enabled,
			WindowSeconds:          cfg.RateLimit.WindowSeconds,
			EmailVerificationLimit: max(5, cfg.RateLimit.AuthLimit/6),
			RegisterLimit:          max(5, cfg.RateLimit.AuthLimit/6),
			LoginLimit:             cfg.RateLimit.AuthLimit,
			WriteLimit:             cfg.RateLimit.WriteLimit,
			UploadLimit:            cfg.RateLimit.UploadLimit,
			ShareCreateLimit:       cfg.RateLimit.WriteLimit,
			ShareAccessLimit:       cfg.RateLimit.AuthLimit,
			DownloadLimit:          cfg.RateLimit.DownloadLimit,
			WebhookLimit:           cfg.RateLimit.WebhookLimit,
		},
		Limits:         LimitConfig{FreePlanID: "free", PaidPlanIDs: []string{"plus", "pro"}},
		ProviderStatus: providerStatusFromConfig(cfg, map[string]string{}),
		Alerts: AlertConfig{
			Enabled:                     true,
			TelegramEnabled:             false,
			Silent:                      false,
			CooldownSeconds:             15 * 60,
			CPUPercentThreshold:         90,
			MemoryPercentThreshold:      90,
			DiskPercentThreshold:        90,
			ObjectStorageBytesThreshold: 0,
			ScanFailureDepthThreshold:   1,
			FailedJobDepthThreshold:     1,
			MailFailedDepthThreshold:    1,
			ReportsOpenThreshold:        10,
		},
		Managed:   managed,
		UpdatedAt: time.Now().UTC(),
	}
}

func providerStatusFromConfig(cfg config.Config, tests map[string]string) ProviderStatus {
	status := ProviderStatus{
		Mailer: ProviderConfigStatus{
			Provider:      cfg.MailerProvider,
			SecretManaged: true,
			RequiredEnv:   []string{"mailer.provider", "smtp.host", "smtp.from_email", "smtp.password"},
			NonSensitive:  map[string]string{"fromEmail": cfg.SMTP.FromEmail, "host": cfg.SMTP.Host, "tlsMode": cfg.SMTP.TLSMode},
		},
		Google: ProviderConfigStatus{
			Provider:      "google",
			SecretManaged: true,
			RequiredEnv:   []string{"google.client_id", "google.client_secret", "google.redirect_url"},
			NonSensitive:  map[string]string{"clientIdConfigured": strconv.FormatBool(strings.TrimSpace(cfg.GoogleOAuth.ClientID) != ""), "redirectUrl": cfg.GoogleOAuth.RedirectURL},
		},
		GitHub: ProviderConfigStatus{
			Provider:      "github",
			SecretManaged: true,
			RequiredEnv:   []string{"github.client_id", "github.client_secret", "github.redirect_url"},
			NonSensitive:  map[string]string{"clientIdConfigured": strconv.FormatBool(strings.TrimSpace(cfg.GitHubOAuth.ClientID) != ""), "redirectUrl": cfg.GitHubOAuth.RedirectURL},
		},
		Turnstile: ProviderConfigStatus{
			Provider:      "cloudflare_turnstile",
			SecretManaged: true,
			RequiredEnv:   []string{"turnstile.site_key", "turnstile.secret_key"},
			NonSensitive:  map[string]string{"siteKeyConfigured": strconv.FormatBool(strings.TrimSpace(cfg.Turnstile.SiteKey) != "")},
		},
		Telegram: ProviderConfigStatus{
			Provider:      "telegram",
			SecretManaged: true,
			RequiredEnv:   []string{"telegram.bot_token", "telegram.chat_id"},
			NonSensitive:  map[string]string{"chatIdConfigured": strconv.FormatBool(strings.TrimSpace(cfg.Telegram.ChatID) != "")},
		},
		S3: ProviderConfigStatus{
			Provider:      "s3",
			SecretManaged: true,
			RequiredEnv:   []string{"s3.endpoint", "s3.bucket", "s3.access_key", "s3.secret_key"},
			NonSensitive:  map[string]string{"endpoint": cfg.S3.Endpoint, "bucket": cfg.S3.Bucket, "region": cfg.S3.Region},
		},
		Stripe: ProviderConfigStatus{
			Provider:      "stripe",
			SecretManaged: true,
			RequiredEnv:   []string{"stripe.enabled", "stripe.webhook_secret", "stripe.checkout_url_template"},
			NonSensitive:  map[string]string{"enabled": strconv.FormatBool(cfg.StripeEnabled), "checkoutUrlConfigured": strconv.FormatBool(strings.TrimSpace(cfg.Stripe.CheckoutURLTemplate) != "")},
		},
		Epusdt: ProviderConfigStatus{
			Provider:      "epusdt",
			SecretManaged: true,
			RequiredEnv:   []string{"epusdt.enabled", "epusdt.pid", "epusdt.secret_key", "epusdt.checkout_url_template", "epusdt.address"},
			NonSensitive:  map[string]string{"enabled": strconv.FormatBool(cfg.EpusdtEnabled), "chain": cfg.Epusdt.Chain},
		},
	}
	fillProviderConfigured(&status.Mailer, map[string]string{
		"mailer.provider": cfg.MailerProvider,
		"smtp.host":       cfg.SMTP.Host,
		"smtp.from_email": cfg.SMTP.FromEmail,
		"smtp.password":   cfg.SMTP.Password,
	})
	fillProviderConfigured(&status.Google, map[string]string{
		"google.client_id":     cfg.GoogleOAuth.ClientID,
		"google.client_secret": cfg.GoogleOAuth.ClientSecret,
		"google.redirect_url":  cfg.GoogleOAuth.RedirectURL,
	})
	fillProviderConfigured(&status.GitHub, map[string]string{
		"github.client_id":     cfg.GitHubOAuth.ClientID,
		"github.client_secret": cfg.GitHubOAuth.ClientSecret,
		"github.redirect_url":  cfg.GitHubOAuth.RedirectURL,
	})
	fillProviderConfigured(&status.Turnstile, map[string]string{
		"turnstile.site_key":   cfg.Turnstile.SiteKey,
		"turnstile.secret_key": cfg.Turnstile.SecretKey,
	})
	fillProviderConfigured(&status.Telegram, map[string]string{
		"telegram.bot_token": cfg.Telegram.BotToken,
		"telegram.chat_id":   cfg.Telegram.ChatID,
	})
	fillProviderConfigured(&status.S3, map[string]string{
		"s3.endpoint":   cfg.S3.Endpoint,
		"s3.bucket":     cfg.S3.Bucket,
		"s3.access_key": cfg.S3.AccessKey,
		"s3.secret_key": cfg.S3.SecretKey,
	})
	stripeEnabled := ""
	if cfg.StripeEnabled {
		stripeEnabled = "true"
	}
	fillProviderConfigured(&status.Stripe, map[string]string{
		"stripe.enabled":               stripeEnabled,
		"stripe.webhook_secret":        cfg.Stripe.WebhookSecret,
		"stripe.checkout_url_template": cfg.Stripe.CheckoutURLTemplate,
	})
	epusdtEnabled := ""
	if cfg.EpusdtEnabled {
		epusdtEnabled = "true"
	}
	fillProviderConfigured(&status.Epusdt, map[string]string{
		"epusdt.enabled":               epusdtEnabled,
		"epusdt.pid":                   cfg.Epusdt.PID,
		"epusdt.secret_key":            cfg.Epusdt.SecretKey,
		"epusdt.checkout_url_template": cfg.Epusdt.CheckoutURLTemplate,
		"epusdt.address":               cfg.Epusdt.Address,
	})
	applyProviderTestStatus(&status.Mailer, tests["mailer"])
	applyProviderTestStatus(&status.Google, tests["google"])
	applyProviderTestStatus(&status.GitHub, tests["github"])
	applyProviderTestStatus(&status.Turnstile, tests["turnstile"])
	applyProviderTestStatus(&status.Telegram, tests["telegram"])
	applyProviderTestStatus(&status.S3, tests["s3"])
	applyProviderTestStatus(&status.Stripe, tests["stripe"])
	applyProviderTestStatus(&status.Epusdt, tests["epusdt"])
	return status
}

func fillProviderConfigured(status *ProviderConfigStatus, values map[string]string) {
	status.MissingEnv = make([]string, 0, len(status.RequiredEnv))
	for _, key := range status.RequiredEnv {
		if strings.TrimSpace(values[key]) == "" {
			status.MissingEnv = append(status.MissingEnv, key)
		}
	}
	status.Configured = len(status.MissingEnv) == 0
}

func applyProviderTestStatus(status *ProviderConfigStatus, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	status.LastTestStatus = value
	status.LastTestMessage = value
}

func cloneRuntimeConfig(cfg RuntimeConfig) RuntimeConfig {
	cfg.Registration.AllowedDomains = append([]string(nil), cfg.Registration.AllowedDomains...)
	cfg.Limits.PaidPlanIDs = append([]string(nil), cfg.Limits.PaidPlanIDs...)
	cfg.ProviderStatus = cloneProviderStatus(cfg.ProviderStatus)
	cfg.Managed.Site.CORSAllowedOrigins = append([]string(nil), cfg.Managed.Site.CORSAllowedOrigins...)
	return cfg
}

func NormalizeRuntimeLogLevel(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case RuntimeLogLevelDebug:
		return RuntimeLogLevelDebug, true
	case RuntimeLogLevelInfo:
		return RuntimeLogLevelInfo, true
	case RuntimeLogLevelWarn, "warning":
		return RuntimeLogLevelWarn, true
	case RuntimeLogLevelError:
		return RuntimeLogLevelError, true
	default:
		return "", false
	}
}

func runtimeLogLevelFromConfig(cfg config.Config) string {
	if level, ok := NormalizeRuntimeLogLevel(cfg.LogLevel.String()); ok {
		return level
	}
	return RuntimeLogLevelInfo
}

func cloneProviderStatus(status ProviderStatus) ProviderStatus {
	status.Mailer = cloneProviderConfigStatus(status.Mailer)
	status.Google = cloneProviderConfigStatus(status.Google)
	status.GitHub = cloneProviderConfigStatus(status.GitHub)
	status.Turnstile = cloneProviderConfigStatus(status.Turnstile)
	status.Telegram = cloneProviderConfigStatus(status.Telegram)
	status.S3 = cloneProviderConfigStatus(status.S3)
	status.Stripe = cloneProviderConfigStatus(status.Stripe)
	status.Epusdt = cloneProviderConfigStatus(status.Epusdt)
	return status
}

func cloneProviderConfigStatus(status ProviderConfigStatus) ProviderConfigStatus {
	status.RequiredEnv = append([]string{}, status.RequiredEnv...)
	status.MissingEnv = append([]string{}, status.MissingEnv...)
	if status.NonSensitive != nil {
		out := map[string]string{}
		for k, v := range status.NonSensitive {
			out[k] = v
		}
		status.NonSensitive = out
	}
	return status
}

func defaultRuntimeResourceSnapshot() RuntimeResourceSnapshot {
	now := time.Now().UTC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	totalMemory := mem.Sys
	if totalMemory == 0 {
		totalMemory = mem.Alloc
	}
	var stat syscall.Statfs_t
	diskTotal := uint64(0)
	diskUsed := uint64(0)
	if err := syscall.Statfs(".", &stat); err == nil {
		diskTotal = stat.Blocks * uint64(stat.Bsize)
		diskFree := stat.Bavail * uint64(stat.Bsize)
		if diskTotal >= diskFree {
			diskUsed = diskTotal - diskFree
		}
	}
	return RuntimeResourceSnapshot{
		CollectedAt:      now,
		CPUPercent:       0,
		MemoryUsedBytes:  mem.Alloc,
		MemoryTotalBytes: totalMemory,
		MemoryPercent:    percentFloat(float64(mem.Alloc), float64(totalMemory)),
		DiskUsedBytes:    diskUsed,
		DiskTotalBytes:   diskTotal,
		DiskPercent:      percentFloat(float64(diskUsed), float64(diskTotal)),
	}
}

func percentFloat(value float64, total float64) float64 {
	if total <= 0 || value <= 0 {
		return 0
	}
	return math.Round((value/total)*1000) / 10
}

func (s *Service) PublicGuestUploadsConfig() GuestUploadConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimeConfig.GuestUploads
}

func (s *Service) PublicRegistrationConfig() RegistrationConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneRuntimeConfig(s.runtimeConfig).Registration
}

func (s *Service) PublicRateLimitsConfig() RuntimeRateLimitConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimeConfig.RateLimits
}

func (s *Service) loadRuntimeConfig(ctx context.Context) error {
	if s.runtime == nil {
		return nil
	}
	loaded, secrets, ok, err := s.runtime.RuntimeConfig(ctx)
	if err != nil {
		return fmt.Errorf("load runtime config: %w", err)
	}
	if ok {
		legacyImport := loaded.Managed.Version == 0
		if legacyImport {
			loaded.Managed, secrets = config.ManagedFromConfig(s.rootConfig)
		}
		s.applyLoadedRuntimeConfigLocked(loaded, secrets)
		if legacyImport {
			return s.runtime.SaveRuntimeConfig(ctx, s.runtimeConfig, s.managedSecrets)
		}
		return nil
	}
	return s.runtime.SaveRuntimeConfig(ctx, s.runtimeConfig, s.managedSecrets)
}

func (s *Service) RefreshRuntimeConfig(ctx context.Context) (RuntimeConfig, error) {
	s.mu.Lock()
	store := s.runtime
	current := cloneRuntimeConfig(s.runtimeConfig)
	s.mu.Unlock()
	if store == nil {
		return current, nil
	}

	loaded, secrets, ok, err := store.RuntimeConfig(ctx)
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("refresh runtime config: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !ok {
		return cloneRuntimeConfig(s.runtimeConfig), nil
	}
	if !loaded.UpdatedAt.IsZero() && loaded.UpdatedAt.Before(s.runtimeConfig.UpdatedAt) {
		return cloneRuntimeConfig(s.runtimeConfig), nil
	}
	prepared, effective := s.prepareRuntimeConfigLocked(loaded, secrets)
	if hook := s.runtimeConfigChange; hook != nil {
		if err := hook(prepared, effective); err != nil {
			rollbackErr := hook(cloneRuntimeConfig(s.runtimeConfig), cloneEffectiveConfig(s.cfg))
			return RuntimeConfig{}, errors.Join(fmt.Errorf("apply refreshed runtime config: %w", err), rollbackErr)
		}
	}
	s.applyPreparedRuntimeConfigLocked(prepared, secrets, effective)
	return cloneRuntimeConfig(s.runtimeConfig), nil
}

func (s *Service) SetRuntimeConfigChangeHook(hook RuntimeConfigChangeHook) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := cloneRuntimeConfig(s.runtimeConfig)
	effective := cloneEffectiveConfig(s.cfg)
	if hook != nil {
		if err := hook(cfg, effective); err != nil {
			return fmt.Errorf("apply initial runtime config: %w", err)
		}
	}
	s.runtimeConfigChange = hook
	return nil
}

func (s *Service) EffectiveConfig() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneEffectiveConfig(s.cfg)
}

func (s *Service) applyLoadedRuntimeConfigLocked(loaded RuntimeConfig, secrets config.ManagedSecrets) {
	loaded, effective := s.prepareRuntimeConfigLocked(loaded, secrets)
	s.applyPreparedRuntimeConfigLocked(loaded, secrets, effective)
}

func (s *Service) prepareRuntimeConfigLocked(loaded RuntimeConfig, secrets config.ManagedSecrets) (RuntimeConfig, config.Config) {
	loaded = normalizeRuntimeConfig(loaded, s.rootConfig)
	effective := config.ApplyManaged(s.rootConfig, loaded.Managed, secrets)
	loaded.Registration.TurnstileSiteKey = strings.TrimSpace(effective.Turnstile.SiteKey)
	loaded.ProviderStatus = providerStatusFromConfig(effective, providerTestStatuses(loaded.ProviderStatus))
	return cloneRuntimeConfig(loaded), cloneEffectiveConfig(effective)
}

func (s *Service) applyPreparedRuntimeConfigLocked(loaded RuntimeConfig, secrets config.ManagedSecrets, effective config.Config) {
	s.managedSecrets = secrets
	s.cfg = effective
	if !s.turnstileVerifierCustom {
		s.turnstileVerifier = NewTurnstileVerifier(effective)
	}
	s.runtimeConfig = cloneRuntimeConfig(loaded)
}

func cloneEffectiveConfig(cfg config.Config) config.Config {
	cfg.CORSAllowedOrigins = append([]string(nil), cfg.CORSAllowedOrigins...)
	return cfg
}

func providerTestStatuses(status ProviderStatus) map[string]string {
	return map[string]string{
		"mailer":    status.Mailer.LastTestStatus,
		"google":    status.Google.LastTestStatus,
		"github":    status.GitHub.LastTestStatus,
		"turnstile": status.Turnstile.LastTestStatus,
		"telegram":  status.Telegram.LastTestStatus,
		"s3":        status.S3.LastTestStatus,
		"stripe":    status.Stripe.LastTestStatus,
		"epusdt":    status.Epusdt.LastTestStatus,
	}
}

func normalizeRuntimeConfig(cfg RuntimeConfig, env config.Config) RuntimeConfig {
	base := defaultRuntimeConfig(env)
	if strings.TrimSpace(cfg.ID) == "" {
		cfg.ID = runtimeConfigID
	}
	if cfg.Managed.Version == 0 {
		cfg.Managed = base.Managed
	}
	if level, ok := NormalizeRuntimeLogLevel(cfg.LogLevel); ok {
		cfg.LogLevel = level
	} else {
		cfg.LogLevel = base.LogLevel
	}
	if cfg.GuestUploads.RetentionSeconds <= 0 {
		cfg.GuestUploads.RetentionSeconds = base.GuestUploads.RetentionSeconds
	}
	if cfg.GuestUploads.ActivePasteLimit <= 0 {
		cfg.GuestUploads.ActivePasteLimit = base.GuestUploads.ActivePasteLimit
	}
	if cfg.GuestUploads.ActiveStorageBytes <= 0 {
		cfg.GuestUploads.ActiveStorageBytes = base.GuestUploads.ActiveStorageBytes
	}
	if cfg.GuestUploads.SingleTextBytes <= 0 {
		cfg.GuestUploads.SingleTextBytes = base.GuestUploads.SingleTextBytes
	}
	if cfg.GuestUploads.SingleFileBytes <= 0 {
		cfg.GuestUploads.SingleFileBytes = base.GuestUploads.SingleFileBytes
	}
	if cfg.GuestUploads.SinglePasteBytes <= 0 {
		cfg.GuestUploads.SinglePasteBytes = base.GuestUploads.SinglePasteBytes
	}
	if cfg.GuestUploads.AttachmentsPerPasteLimit <= 0 {
		cfg.GuestUploads.AttachmentsPerPasteLimit = base.GuestUploads.AttachmentsPerPasteLimit
	}
	if cfg.GuestUploads.DailyUploadBytes <= 0 {
		cfg.GuestUploads.DailyUploadBytes = base.GuestUploads.DailyUploadBytes
	}
	registrationWasEmpty := len(cfg.Registration.AllowedDomains) == 0 &&
		!cfg.Registration.RequireEmailVerification &&
		!cfg.Registration.RequireTurnstile &&
		strings.TrimSpace(cfg.Registration.TurnstileSiteKey) == ""
	cfg.Registration.AllowedDomains = normalizeDomainList(cfg.Registration.AllowedDomains)
	if len(cfg.Registration.AllowedDomains) == 0 {
		cfg.Registration.AllowedDomains = append([]string{}, base.Registration.AllowedDomains...)
	}
	if registrationWasEmpty {
		cfg.Registration.RequireEmailVerification = base.Registration.RequireEmailVerification
		cfg.Registration.RequireTurnstile = base.Registration.RequireTurnstile
	}
	cfg.Registration.TurnstileSiteKey = strings.TrimSpace(cfg.Managed.Turnstile.SiteKey)
	rateLimitsWereEmpty := cfg.RateLimits.WindowSeconds <= 0 &&
		cfg.RateLimits.EmailVerificationLimit <= 0 &&
		cfg.RateLimits.RegisterLimit <= 0 &&
		cfg.RateLimits.LoginLimit <= 0 &&
		cfg.RateLimits.WriteLimit <= 0 &&
		cfg.RateLimits.UploadLimit <= 0 &&
		cfg.RateLimits.ShareCreateLimit <= 0 &&
		cfg.RateLimits.ShareAccessLimit <= 0 &&
		cfg.RateLimits.DownloadLimit <= 0 &&
		cfg.RateLimits.WebhookLimit <= 0
	if rateLimitsWereEmpty {
		cfg.RateLimits.Enabled = base.RateLimits.Enabled
	}
	if cfg.RateLimits.WindowSeconds <= 0 {
		cfg.RateLimits.WindowSeconds = base.RateLimits.WindowSeconds
	}
	if cfg.RateLimits.EmailVerificationLimit <= 0 {
		cfg.RateLimits.EmailVerificationLimit = base.RateLimits.EmailVerificationLimit
	}
	if cfg.RateLimits.RegisterLimit <= 0 {
		cfg.RateLimits.RegisterLimit = base.RateLimits.RegisterLimit
	}
	if cfg.RateLimits.LoginLimit <= 0 {
		cfg.RateLimits.LoginLimit = base.RateLimits.LoginLimit
	}
	if cfg.RateLimits.WriteLimit <= 0 {
		cfg.RateLimits.WriteLimit = base.RateLimits.WriteLimit
	}
	if cfg.RateLimits.UploadLimit <= 0 {
		cfg.RateLimits.UploadLimit = base.RateLimits.UploadLimit
	}
	if cfg.RateLimits.ShareCreateLimit <= 0 {
		cfg.RateLimits.ShareCreateLimit = base.RateLimits.ShareCreateLimit
	}
	if cfg.RateLimits.ShareAccessLimit <= 0 {
		cfg.RateLimits.ShareAccessLimit = base.RateLimits.ShareAccessLimit
	}
	if cfg.RateLimits.DownloadLimit <= 0 {
		cfg.RateLimits.DownloadLimit = base.RateLimits.DownloadLimit
	}
	if cfg.RateLimits.WebhookLimit <= 0 {
		cfg.RateLimits.WebhookLimit = base.RateLimits.WebhookLimit
	}
	if strings.TrimSpace(cfg.Limits.FreePlanID) == "" {
		cfg.Limits.FreePlanID = base.Limits.FreePlanID
	}
	if len(cfg.Limits.PaidPlanIDs) == 0 {
		cfg.Limits.PaidPlanIDs = base.Limits.PaidPlanIDs
	}
	if cfg.Alerts.CooldownSeconds <= 0 {
		cfg.Alerts.CooldownSeconds = base.Alerts.CooldownSeconds
	}
	if cfg.Alerts.CPUPercentThreshold <= 0 {
		cfg.Alerts.CPUPercentThreshold = base.Alerts.CPUPercentThreshold
	}
	if cfg.Alerts.MemoryPercentThreshold <= 0 {
		cfg.Alerts.MemoryPercentThreshold = base.Alerts.MemoryPercentThreshold
	}
	if cfg.Alerts.DiskPercentThreshold <= 0 {
		cfg.Alerts.DiskPercentThreshold = base.Alerts.DiskPercentThreshold
	}
	if cfg.ProviderStatus.Mailer.Provider == "" {
		cfg.ProviderStatus = base.ProviderStatus
	}
	if cfg.UpdatedAt.IsZero() {
		cfg.UpdatedAt = time.Now().UTC()
	}
	return cloneRuntimeConfig(cfg)
}

func (s *Service) AdminRuntimeConfig(actorID string) (RuntimeConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return RuntimeConfig{}, err
	}
	s.runtimeConfig.ProviderStatus = providerStatusFromConfig(s.cfg, providerTestStatuses(s.runtimeConfig.ProviderStatus))
	return cloneRuntimeConfig(s.runtimeConfig), nil
}

func (s *Service) AdminManagedConfig(actorID string) (ManagedConfigView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return ManagedConfigView{}, err
	}
	return s.managedConfigViewLocked(), nil
}

func (s *Service) AdminUpdateManagedConfig(actorID string, update ManagedConfigUpdate) (ManagedConfigView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return ManagedConfigView{}, err
	}
	managed, err := normalizeManagedConfig(update.Config, s.rootConfig.AppEnv)
	if err != nil {
		return ManagedConfigView{}, err
	}
	if err := validateManagedSecretRebinding(s.runtimeConfig.Managed, managed, s.managedSecrets, update.Secrets); err != nil {
		return ManagedConfigView{}, err
	}
	secrets := applyManagedSecretPatch(s.managedSecrets, update.Secrets)
	next := cloneRuntimeConfig(s.runtimeConfig)
	next.Managed = managed
	next.Registration.TurnstileSiteKey = managed.Turnstile.SiteKey
	next.UpdatedAt = s.now().UTC()
	audit := AuditLog{
		ID: s.newID("aud"), ActorID: actorID, Action: "admin.managed_config_update", Target: runtimeConfigID,
		Metadata: managedConfigAuditMetadata(managed, secrets), CreatedAt: next.UpdatedAt,
	}
	if err := s.commitRuntimeConfigLocked(next, secrets, audit, true); err != nil {
		return ManagedConfigView{}, err
	}
	return s.managedConfigViewLocked(), nil
}

func (s *Service) managedConfigViewLocked() ManagedConfigView {
	managed := cloneRuntimeConfig(s.runtimeConfig).Managed
	return ManagedConfigView{
		Config:    managed,
		Secrets:   managedSecretStatus(s.managedSecrets),
		UpdatedAt: s.runtimeConfig.UpdatedAt,
	}
}

func managedSecretStatus(secrets config.ManagedSecrets) ManagedSecretStatus {
	return ManagedSecretStatus{
		S3AccessKey: strings.TrimSpace(secrets.S3AccessKey) != "", S3SecretKey: strings.TrimSpace(secrets.S3SecretKey) != "",
		GoogleClientSecret: strings.TrimSpace(secrets.GoogleClientSecret) != "", GitHubClientSecret: strings.TrimSpace(secrets.GitHubClientSecret) != "",
		TurnstileSecretKey: strings.TrimSpace(secrets.TurnstileSecretKey) != "", TelegramBotToken: strings.TrimSpace(secrets.TelegramBotToken) != "",
		SMTPPassword: strings.TrimSpace(secrets.SMTPPassword) != "", StripeWebhookSecret: strings.TrimSpace(secrets.StripeWebhookSecret) != "",
		EpusdtSecretKey: strings.TrimSpace(secrets.EpusdtSecretKey) != "",
	}
}

func applyManagedSecretPatch(current config.ManagedSecrets, patch ManagedSecretPatch) config.ManagedSecrets {
	apply := func(target *string, value *string) {
		if value != nil {
			*target = strings.TrimSpace(*value)
		}
	}
	apply(&current.S3AccessKey, patch.S3AccessKey)
	apply(&current.S3SecretKey, patch.S3SecretKey)
	apply(&current.GoogleClientSecret, patch.GoogleClientSecret)
	apply(&current.GitHubClientSecret, patch.GitHubClientSecret)
	apply(&current.TurnstileSecretKey, patch.TurnstileSecretKey)
	apply(&current.TelegramBotToken, patch.TelegramBotToken)
	apply(&current.SMTPPassword, patch.SMTPPassword)
	apply(&current.StripeWebhookSecret, patch.StripeWebhookSecret)
	apply(&current.EpusdtSecretKey, patch.EpusdtSecretKey)
	return current
}

func validateManagedSecretRebinding(current, next config.ManagedConfig, secrets config.ManagedSecrets, patch ManagedSecretPatch) error {
	if normalizedEndpoint(current.S3.Endpoint) != normalizedEndpoint(next.S3.Endpoint) &&
		(strings.TrimSpace(secrets.S3AccessKey) != "" || strings.TrimSpace(secrets.S3SecretKey) != "") &&
		(patch.S3AccessKey == nil || patch.S3SecretKey == nil) {
		return E(http.StatusBadRequest, "secret_reentry_required", "S3 access key and secret key must be submitted again when the endpoint changes")
	}
	if (strings.TrimSpace(current.SMTP.Host) != strings.TrimSpace(next.SMTP.Host) || current.SMTP.Port != next.SMTP.Port ||
		strings.ToLower(strings.TrimSpace(current.SMTP.TLSMode)) != strings.ToLower(strings.TrimSpace(next.SMTP.TLSMode))) &&
		strings.TrimSpace(secrets.SMTPPassword) != "" && patch.SMTPPassword == nil {
		return E(http.StatusBadRequest, "secret_reentry_required", "SMTP password must be submitted again when the host, port, or TLS mode changes")
	}
	if normalizedEndpoint(current.Turnstile.VerifyURL) != normalizedEndpoint(next.Turnstile.VerifyURL) &&
		strings.TrimSpace(secrets.TurnstileSecretKey) != "" && patch.TurnstileSecretKey == nil {
		return E(http.StatusBadRequest, "secret_reentry_required", "Turnstile secret key must be submitted again when the verification URL changes")
	}
	if normalizedEndpoint(current.Telegram.APIBaseURL) != normalizedEndpoint(next.Telegram.APIBaseURL) &&
		strings.TrimSpace(secrets.TelegramBotToken) != "" && patch.TelegramBotToken == nil {
		return E(http.StatusBadRequest, "secret_reentry_required", "Telegram bot token must be submitted again when the API URL changes")
	}
	return nil
}

func normalizedEndpoint(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func normalizeManagedConfig(managed config.ManagedConfig, appEnv string) (config.ManagedConfig, error) {
	managed.Version = config.ManagedConfigVersion
	managed.Site.AppName = strings.TrimSpace(managed.Site.AppName)
	managed.Site.PublicURL = strings.TrimRight(strings.TrimSpace(managed.Site.PublicURL), "/")
	managed.Site.SupportEmail = strings.TrimSpace(managed.Site.SupportEmail)
	managed.Site.AbuseEmail = strings.TrimSpace(managed.Site.AbuseEmail)
	if managed.Site.AppName == "" {
		return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_site_config", "application name is required")
	}
	if err := validateManagedHTTPURL(managed.Site.PublicURL, appEnv == "production"); err != nil {
		return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_public_url", err.Error())
	}
	for _, address := range []string{managed.Site.SupportEmail, managed.Site.AbuseEmail} {
		if _, err := mail.ParseAddress(address); err != nil {
			return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_support_email", "support and abuse email addresses must be valid")
		}
	}
	managed.Site.CORSAllowedOrigins = normalizeOrigins(managed.Site.CORSAllowedOrigins)
	if len(managed.Site.CORSAllowedOrigins) == 0 {
		managed.Site.CORSAllowedOrigins = []string{managed.Site.PublicURL}
	}
	if managed.WorkerHeartbeatMaxAgeSeconds <= 0 {
		return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_worker_config", "worker heartbeat max age must be positive")
	}
	managed.S3.Endpoint = strings.TrimRight(strings.TrimSpace(managed.S3.Endpoint), "/")
	managed.S3.Bucket = strings.TrimSpace(managed.S3.Bucket)
	managed.S3.Region = strings.TrimSpace(managed.S3.Region)
	if managed.S3.Endpoint != "" {
		if err := validateManagedHTTPURL(managed.S3.Endpoint, appEnv == "production"); err != nil {
			return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_s3_config", err.Error())
		}
	}
	managed.Scanner.Provider = strings.ToLower(strings.TrimSpace(managed.Scanner.Provider))
	if managed.Scanner.Provider != "heuristic" && managed.Scanner.Provider != "clamav" {
		return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_scanner_config", "scanner provider must be heuristic or clamav")
	}
	if managed.Scanner.ClamAV.Timeout <= 0 {
		return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_scanner_config", "scanner timeout must be positive")
	}
	managed.MailerProvider = strings.ToLower(strings.TrimSpace(managed.MailerProvider))
	if managed.MailerProvider != "log" && managed.MailerProvider != "smtp" {
		return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_mailer_config", "mailer provider must be log or smtp")
	}
	managed.SMTP.TLSMode = strings.ToLower(strings.TrimSpace(managed.SMTP.TLSMode))
	managed.SMTP.Host = strings.TrimSpace(managed.SMTP.Host)
	managed.SMTP.Username = strings.TrimSpace(managed.SMTP.Username)
	managed.SMTP.FromEmail = strings.TrimSpace(managed.SMTP.FromEmail)
	managed.SMTP.FromName = strings.TrimSpace(managed.SMTP.FromName)
	if managed.SMTP.TLSMode != "none" && managed.SMTP.TLSMode != "starttls" && managed.SMTP.TLSMode != "tls" {
		return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_mailer_config", "SMTP TLS mode must be none, starttls, or tls")
	}
	if managed.SMTP.Port <= 0 || managed.SMTP.Port > 65535 {
		return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_mailer_config", "SMTP port is invalid")
	}
	if _, err := mail.ParseAddress(managed.SMTP.FromEmail); err != nil {
		return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_mailer_config", "SMTP from email is invalid")
	}
	managed.GoogleOAuth.RedirectURL = strings.TrimSpace(managed.GoogleOAuth.RedirectURL)
	managed.GitHubOAuth.RedirectURL = strings.TrimSpace(managed.GitHubOAuth.RedirectURL)
	managed.Turnstile.VerifyURL = strings.TrimRight(strings.TrimSpace(managed.Turnstile.VerifyURL), "/")
	managed.Telegram.APIBaseURL = strings.TrimRight(strings.TrimSpace(managed.Telegram.APIBaseURL), "/")
	if appEnv == "production" {
		if managed.Turnstile.VerifyURL == "" {
			managed.Turnstile.VerifyURL = config.DefaultTurnstileVerifyURL
		}
		if managed.Turnstile.VerifyURL != config.DefaultTurnstileVerifyURL {
			return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_provider_url", "production Turnstile verification URL must use the official Cloudflare endpoint")
		}
		if managed.Telegram.APIBaseURL == "" {
			managed.Telegram.APIBaseURL = config.DefaultTelegramAPIBaseURL
		}
		if managed.Telegram.APIBaseURL != config.DefaultTelegramAPIBaseURL {
			return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_provider_url", "production Telegram API URL must use the official endpoint")
		}
	}
	for _, candidate := range []string{managed.GoogleOAuth.RedirectURL, managed.GitHubOAuth.RedirectURL, managed.Turnstile.VerifyURL, managed.Telegram.APIBaseURL} {
		if strings.TrimSpace(candidate) != "" {
			if err := validateManagedHTTPURL(candidate, appEnv == "production"); err != nil {
				return config.ManagedConfig{}, E(http.StatusBadRequest, "invalid_provider_url", err.Error())
			}
		}
	}
	return managed, nil
}

func validateManagedHTTPURL(raw string, requireHTTPS bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("URL must be an absolute HTTP or HTTPS URL")
	}
	if requireHTTPS && parsed.Scheme != "https" {
		return fmt.Errorf("production URL must use HTTPS")
	}
	if parsed.User != nil {
		return fmt.Errorf("URL must not contain embedded credentials")
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("URL must not contain a fragment")
	}
	return nil
}

func normalizeOrigins(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func managedConfigAuditMetadata(managed config.ManagedConfig, secrets config.ManagedSecrets) map[string]any {
	status := managedSecretStatus(secrets)
	return map[string]any{
		"publicUrl":         managed.Site.PublicURL,
		"mailerProvider":    managed.MailerProvider,
		"scannerProvider":   managed.Scanner.Provider,
		"stripeEnabled":     managed.StripeEnabled,
		"epusdtEnabled":     managed.EpusdtEnabled,
		"secretsConfigured": status,
	}
}

func (s *Service) AdminUpdateRuntimeConfig(actorID string, patch RuntimeConfigPatch) (RuntimeConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adminUpdateRuntimeConfigLocked(actorID, patch)
}

func (s *Service) adminUpdateRuntimeConfigLocked(actorID string, patch RuntimeConfigPatch) (RuntimeConfig, error) {
	if err := s.requireAdminLocked(actorID); err != nil {
		return RuntimeConfig{}, err
	}
	next := cloneRuntimeConfig(s.runtimeConfig)
	if patch.LogLevel != nil {
		level, ok := NormalizeRuntimeLogLevel(*patch.LogLevel)
		if !ok {
			return RuntimeConfig{}, E(http.StatusBadRequest, "invalid_log_level", "log level must be debug, info, warn, or error")
		}
		next.LogLevel = level
	}
	if patch.GuestUploads != nil {
		next.GuestUploads = applyGuestUploadConfigPatch(next.GuestUploads, *patch.GuestUploads)
	}
	if patch.Registration != nil {
		next.Registration = applyRegistrationConfigPatch(next.Registration, *patch.Registration)
	}
	if patch.RateLimits != nil {
		next.RateLimits = applyRuntimeRateLimitConfigPatch(next.RateLimits, *patch.RateLimits)
	}
	if patch.Alerts != nil {
		next.Alerts = applyAlertConfigPatch(next.Alerts, *patch.Alerts)
	}
	next.ProviderStatus = providerStatusFromConfig(s.cfg, providerTestStatuses(next.ProviderStatus))
	next.UpdatedAt = s.now().UTC()
	next = normalizeRuntimeConfig(next, s.rootConfig)
	audit := AuditLog{
		ID: s.newID("aud"), ActorID: actorID, Action: "admin.runtime_config_update", Target: runtimeConfigID,
		Metadata: runtimeConfigAuditMetadata(next), CreatedAt: next.UpdatedAt,
	}
	if err := s.commitRuntimeConfigLocked(next, s.managedSecrets, audit, true); err != nil {
		return RuntimeConfig{}, err
	}
	return cloneRuntimeConfig(s.runtimeConfig), nil
}

func applyGuestUploadConfigPatch(cfg GuestUploadConfig, patch GuestUploadConfigPatch) GuestUploadConfig {
	if patch.Enabled != nil {
		cfg.Enabled = *patch.Enabled
	}
	if patch.RequireTurnstile != nil {
		cfg.RequireTurnstile = *patch.RequireTurnstile
	}
	if patch.RetentionSeconds != nil {
		cfg.RetentionSeconds = *patch.RetentionSeconds
	}
	if patch.ActivePasteLimit != nil {
		cfg.ActivePasteLimit = *patch.ActivePasteLimit
	}
	if patch.ActiveStorageBytes != nil {
		cfg.ActiveStorageBytes = *patch.ActiveStorageBytes
	}
	if patch.SingleTextBytes != nil {
		cfg.SingleTextBytes = *patch.SingleTextBytes
	}
	if patch.SingleFileBytes != nil {
		cfg.SingleFileBytes = *patch.SingleFileBytes
	}
	if patch.SinglePasteBytes != nil {
		cfg.SinglePasteBytes = *patch.SinglePasteBytes
	}
	if patch.AttachmentsPerPasteLimit != nil {
		cfg.AttachmentsPerPasteLimit = *patch.AttachmentsPerPasteLimit
	}
	if patch.DailyUploadBytes != nil {
		cfg.DailyUploadBytes = *patch.DailyUploadBytes
	}
	if patch.DailyShareDownloadBytes != nil {
		cfg.DailyShareDownloadBytes = *patch.DailyShareDownloadBytes
	}
	if patch.ShareDownloadsEnabled != nil {
		cfg.ShareDownloadsEnabled = *patch.ShareDownloadsEnabled
	}
	return cfg
}

func applyRegistrationConfigPatch(cfg RegistrationConfig, patch RegistrationConfigPatch) RegistrationConfig {
	if patch.AllowedDomains != nil {
		cfg.AllowedDomains = append([]string{}, (*patch.AllowedDomains)...)
	}
	if patch.RequireEmailVerification != nil {
		cfg.RequireEmailVerification = *patch.RequireEmailVerification
	}
	if patch.RequireTurnstile != nil {
		cfg.RequireTurnstile = *patch.RequireTurnstile
	}
	if patch.TurnstileSiteKey != nil {
		cfg.TurnstileSiteKey = *patch.TurnstileSiteKey
	}
	return cfg
}

func applyRuntimeRateLimitConfigPatch(cfg RuntimeRateLimitConfig, patch RuntimeRateLimitConfigPatch) RuntimeRateLimitConfig {
	if patch.Enabled != nil {
		cfg.Enabled = *patch.Enabled
	}
	if patch.WindowSeconds != nil {
		cfg.WindowSeconds = *patch.WindowSeconds
	}
	if patch.EmailVerificationLimit != nil {
		cfg.EmailVerificationLimit = *patch.EmailVerificationLimit
	}
	if patch.RegisterLimit != nil {
		cfg.RegisterLimit = *patch.RegisterLimit
	}
	if patch.LoginLimit != nil {
		cfg.LoginLimit = *patch.LoginLimit
	}
	if patch.WriteLimit != nil {
		cfg.WriteLimit = *patch.WriteLimit
	}
	if patch.UploadLimit != nil {
		cfg.UploadLimit = *patch.UploadLimit
	}
	if patch.ShareCreateLimit != nil {
		cfg.ShareCreateLimit = *patch.ShareCreateLimit
	}
	if patch.ShareAccessLimit != nil {
		cfg.ShareAccessLimit = *patch.ShareAccessLimit
	}
	if patch.DownloadLimit != nil {
		cfg.DownloadLimit = *patch.DownloadLimit
	}
	if patch.WebhookLimit != nil {
		cfg.WebhookLimit = *patch.WebhookLimit
	}
	return cfg
}

func applyAlertConfigPatch(cfg AlertConfig, patch AlertConfigPatch) AlertConfig {
	if patch.Enabled != nil {
		cfg.Enabled = *patch.Enabled
	}
	if patch.TelegramEnabled != nil {
		cfg.TelegramEnabled = *patch.TelegramEnabled
	}
	if patch.Silent != nil {
		cfg.Silent = *patch.Silent
	}
	if patch.CooldownSeconds != nil {
		cfg.CooldownSeconds = *patch.CooldownSeconds
	}
	if patch.CPUPercentThreshold != nil {
		cfg.CPUPercentThreshold = *patch.CPUPercentThreshold
	}
	if patch.MemoryPercentThreshold != nil {
		cfg.MemoryPercentThreshold = *patch.MemoryPercentThreshold
	}
	if patch.DiskPercentThreshold != nil {
		cfg.DiskPercentThreshold = *patch.DiskPercentThreshold
	}
	if patch.ObjectStorageBytesThreshold != nil {
		cfg.ObjectStorageBytesThreshold = *patch.ObjectStorageBytesThreshold
	}
	if patch.ScanFailureDepthThreshold != nil {
		cfg.ScanFailureDepthThreshold = *patch.ScanFailureDepthThreshold
	}
	if patch.FailedJobDepthThreshold != nil {
		cfg.FailedJobDepthThreshold = *patch.FailedJobDepthThreshold
	}
	if patch.MailFailedDepthThreshold != nil {
		cfg.MailFailedDepthThreshold = *patch.MailFailedDepthThreshold
	}
	if patch.ReportsOpenThreshold != nil {
		cfg.ReportsOpenThreshold = *patch.ReportsOpenThreshold
	}
	return cfg
}

func runtimeConfigAuditMetadata(cfg RuntimeConfig) map[string]any {
	return map[string]any{
		"logLevel":                     cfg.LogLevel,
		"guestUploadsEnabled":          cfg.GuestUploads.Enabled,
		"guestRequireTurnstile":        cfg.GuestUploads.RequireTurnstile,
		"registrationDomains":          cfg.Registration.AllowedDomains,
		"registrationRequireEmail":     cfg.Registration.RequireEmailVerification,
		"registrationRequireTurnstile": cfg.Registration.RequireTurnstile,
		"rateLimitsEnabled":            cfg.RateLimits.Enabled,
		"alertsEnabled":                cfg.Alerts.Enabled,
		"telegramEnabled":              cfg.Alerts.TelegramEnabled,
	}
}

func (s *Service) commitRuntimeConfigLocked(cfg RuntimeConfig, secrets config.ManagedSecrets, audit AuditLog, applyHook bool) error {
	prepared, effective := s.prepareRuntimeConfigLocked(cfg, secrets)
	if s.runtime != nil && s.audit != nil {
		if _, ok := s.runtime.(RuntimeConfigAuditStore); !ok {
			return fmt.Errorf("runtime config store does not support atomic audit persistence")
		}
	}

	oldRuntime := cloneRuntimeConfig(s.runtimeConfig)
	oldEffective := cloneEffectiveConfig(s.cfg)
	hook := s.runtimeConfigChange
	hookApplied := false
	if applyHook && hook != nil {
		if err := hook(prepared, effective); err != nil {
			rollbackErr := hook(oldRuntime, oldEffective)
			return errors.Join(fmt.Errorf("apply runtime config: %w", err), rollbackErr)
		}
		hookApplied = true
	}

	ctx := context.Background()
	var err error
	switch {
	case s.runtime != nil && s.audit != nil:
		err = s.runtime.(RuntimeConfigAuditStore).SaveRuntimeConfigWithAudit(ctx, prepared, secrets, audit)
	case s.runtime != nil:
		err = s.runtime.SaveRuntimeConfig(ctx, prepared, secrets)
	case s.audit != nil:
		err = s.audit.RecordAuditLog(ctx, audit)
	}
	if err != nil {
		if hookApplied {
			rollbackErr := hook(oldRuntime, oldEffective)
			return errors.Join(err, rollbackErr)
		}
		return err
	}

	s.applyPreparedRuntimeConfigLocked(prepared, secrets, effective)
	s.cacheAuditLogLocked(audit)
	return nil
}

func (s *Service) AdminUpdateCatalog(actorID string, update AdminPlanUpdate) (plans.Catalog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return plans.Catalog{}, err
	}
	catalog, err := validateCatalogUpdate(update)
	if err != nil {
		return plans.Catalog{}, err
	}
	if writer, ok := s.catalogWriter(); ok {
		if err := writer.SaveCatalog(context.Background(), catalog); err != nil {
			return plans.Catalog{}, err
		}
	}
	s.catalog = cloneCatalog(catalog)
	if err := s.auditLocked(actorID, "admin.catalog_update", "plans", map[string]any{"plans": len(catalog.Plans), "prices": len(catalog.Prices)}); err != nil {
		return plans.Catalog{}, err
	}
	return cloneCatalog(s.catalog), nil
}

func (s *Service) catalogWriter() (CatalogWriter, bool) {
	writer, ok := any(s.catalogStore).(CatalogWriter)
	if ok {
		return writer, true
	}
	return nil, false
}

func validateCatalogUpdate(update AdminPlanUpdate) (plans.Catalog, error) {
	if len(update.Plans) == 0 {
		return plans.Catalog{}, E(http.StatusBadRequest, "plans_required", "at least one plan is required")
	}
	planIDs := map[string]struct{}{}
	for i := range update.Plans {
		plan := &update.Plans[i]
		plan.ID = strings.ToLower(strings.TrimSpace(plan.ID))
		plan.Name = strings.TrimSpace(plan.Name)
		if plan.ID == "" || plan.Name == "" {
			return plans.Catalog{}, E(http.StatusBadRequest, "invalid_plan", "plan ID and name are required")
		}
		if _, exists := planIDs[plan.ID]; exists {
			return plans.Catalog{}, E(http.StatusBadRequest, "duplicate_plan", "plan IDs must be unique")
		}
		planIDs[plan.ID] = struct{}{}
		if plan.ActivePasteLimit < 0 || plan.ActiveStorageBytes < 0 || plan.SingleTextBytes < 0 || plan.SingleFileBytes < 0 || plan.SinglePasteBytes < 0 || plan.AttachmentsPerPasteLimit < 0 || plan.TagsPerPasteLimit < 0 || plan.MaxRetentionSeconds <= 0 || plan.DailyUploadBytes < 0 || plan.DailyShareDownloadBytes < 0 {
			return plans.Catalog{}, E(http.StatusBadRequest, "invalid_plan_limits", "plan limits must be non-negative and retention must be positive")
		}
		if plan.SingleTextBytes > plan.SinglePasteBytes || plan.SingleFileBytes > plan.SinglePasteBytes {
			return plans.Catalog{}, E(http.StatusBadRequest, "invalid_plan_limits", "single item limits cannot exceed single paste limit")
		}
	}
	priceKeys := map[string]struct{}{}
	for i := range update.Prices {
		price := &update.Prices[i]
		price.ID = strings.TrimSpace(price.ID)
		price.PlanID = strings.ToLower(strings.TrimSpace(price.PlanID))
		price.Period = strings.ToLower(strings.TrimSpace(price.Period))
		price.Currency = strings.ToUpper(strings.TrimSpace(price.Currency))
		if price.ID == "" || price.PlanID == "" || price.Period == "" || price.Currency == "" {
			return plans.Catalog{}, E(http.StatusBadRequest, "invalid_price", "price ID, plan, period, and currency are required")
		}
		if _, ok := planIDs[price.PlanID]; !ok {
			return plans.Catalog{}, E(http.StatusBadRequest, "invalid_price_plan", "price references an unknown plan")
		}
		if price.AmountCents < 0 {
			return plans.Catalog{}, E(http.StatusBadRequest, "invalid_price", "price amount must be non-negative")
		}
		key := price.PlanID + "\x00" + price.Period
		if _, exists := priceKeys[key]; exists {
			return plans.Catalog{}, E(http.StatusBadRequest, "duplicate_price_period", "each plan can only have one price per period")
		}
		priceKeys[key] = struct{}{}
	}
	return plans.Catalog{Plans: append([]plans.Plan(nil), update.Plans...), Prices: append([]plans.Price(nil), update.Prices...)}, nil
}

func (s *Service) AdminProviderTest(actorID string, provider string) (RuntimeConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return RuntimeConfig{}, err
	}
	provider = normalizeProvider(provider)
	next := cloneRuntimeConfig(s.runtimeConfig)
	status := "ok"
	switch provider {
	case "mailer":
		if strings.TrimSpace(s.cfg.MailerProvider) == "smtp" && (strings.TrimSpace(s.cfg.SMTP.Host) == "" || strings.TrimSpace(s.cfg.SMTP.FromEmail) == "") {
			status = "missing_smtp_config"
		}
	case "google":
		if strings.TrimSpace(s.cfg.GoogleOAuth.ClientID) == "" || strings.TrimSpace(s.cfg.GoogleOAuth.ClientSecret) == "" {
			status = "missing_google_oauth_config"
		}
	case "github":
		if strings.TrimSpace(s.cfg.GitHubOAuth.ClientID) == "" || strings.TrimSpace(s.cfg.GitHubOAuth.ClientSecret) == "" {
			status = "missing_github_oauth_config"
		}
	case "turnstile":
		if strings.TrimSpace(s.cfg.Turnstile.SecretKey) == "" {
			status = "missing_turnstile_secret"
		}
	case "telegram":
		if strings.TrimSpace(s.cfg.Telegram.BotToken) == "" || strings.TrimSpace(s.cfg.Telegram.ChatID) == "" {
			status = "missing_telegram_config"
		} else if s.alertSender != nil {
			if err := s.alertSender.SendAlert(context.Background(), "PasteBox Telegram provider test", true); err != nil {
				status = "send_failed"
			}
		}
	case "s3":
		if strings.TrimSpace(s.cfg.S3.Endpoint) == "" || strings.TrimSpace(s.cfg.S3.Bucket) == "" || strings.TrimSpace(s.cfg.S3.AccessKey) == "" || strings.TrimSpace(s.cfg.S3.SecretKey) == "" {
			status = "missing_s3_config"
		}
	case "stripe":
		if !s.cfg.StripeEnabled || strings.TrimSpace(s.cfg.Stripe.WebhookSecret) == "" || strings.TrimSpace(s.cfg.Stripe.CheckoutURLTemplate) == "" {
			status = "missing_stripe_config"
		}
	case "epusdt":
		if !s.cfg.EpusdtEnabled || strings.TrimSpace(s.cfg.Epusdt.PID) == "" || strings.TrimSpace(s.cfg.Epusdt.SecretKey) == "" || strings.TrimSpace(s.cfg.Epusdt.CheckoutURLTemplate) == "" || strings.TrimSpace(s.cfg.Epusdt.Address) == "" {
			status = "missing_epusdt_config"
		}
	default:
		return RuntimeConfig{}, E(http.StatusBadRequest, "invalid_provider", "provider is not supported")
	}
	next.ProviderStatus = providerStatusFromConfig(s.cfg, map[string]string{provider: status})
	next.UpdatedAt = s.now().UTC()
	audit := AuditLog{
		ID: s.newID("aud"), ActorID: actorID, Action: "admin.provider_test", Target: provider,
		Metadata: map[string]any{"status": status}, CreatedAt: next.UpdatedAt,
	}
	if err := s.commitRuntimeConfigLocked(next, s.managedSecrets, audit, false); err != nil {
		return RuntimeConfig{}, err
	}
	return cloneRuntimeConfig(s.runtimeConfig), nil
}

func (s *Service) AdminRuntimePanel(actorID string) (RuntimePanel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return RuntimePanel{}, err
	}
	ops, err := s.operationalMetricsLocked(context.Background())
	if err != nil {
		return RuntimePanel{}, err
	}
	resources := s.resourceSnapshot()
	resources.ObjectStorageBytes, resources.ObjectStorageObjectCount = s.objectStorageUsageLocked()
	alerts := s.alertEventsNewestLocked(20)
	return RuntimePanel{Config: cloneRuntimeConfig(s.runtimeConfig), Resources: resources, Operational: ops, Alerts: alerts}, nil
}

func (s *Service) objectStorageUsageLocked() (int64, int) {
	objects := map[string]int64{}
	for _, attachment := range s.attachmentsByID {
		if attachment.ObjectKey == "" || attachment.Status == "deleted" {
			continue
		}
		objects[attachment.ObjectKey] = attachment.Size
	}
	var total int64
	for _, size := range objects {
		total += size
	}
	return total, len(objects)
}

func (s *Service) AdminManualWorkItems(actorID string) ([]ManualWorkItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return nil, err
	}
	if err := s.refreshQueueCachesLocked(context.Background()); err != nil {
		return nil, err
	}
	items := []ManualWorkItem{}
	for _, attachment := range s.attachmentsByID {
		if attachment.Status == "frozen" || attachment.ScanStatus == "malicious" || attachment.ScanStatus == "scan_failed" {
			items = append(items, ManualWorkItem{
				ID:        attachment.ID,
				Kind:      "attachment",
				TargetID:  attachment.ID,
				Status:    attachment.Status + "/" + attachment.ScanStatus,
				Risk:      attachment.Risk,
				Summary:   attachment.FileName,
				CreatedAt: attachment.CreatedAt,
				UpdatedAt: attachment.CreatedAt,
			})
		}
	}
	for _, job := range s.failedJobs {
		if job == nil {
			continue
		}
		items = append(items, ManualWorkItem{ID: job.ID, Kind: "failed_job", TargetID: job.TargetID, Status: job.Status, Risk: job.Error, Summary: job.Kind, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt})
	}
	for _, job := range s.scanFailures {
		if job == nil {
			continue
		}
		items = append(items, ManualWorkItem{ID: job.ID, Kind: "scan_failure", TargetID: job.TargetID, Status: job.Status, Risk: job.Error, Summary: job.Kind, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt})
	}
	failedMails, err := s.mailQueueItemsLocked(context.Background(), "failed", 100)
	if err != nil {
		return nil, err
	}
	for _, mail := range failedMails {
		updatedAt := mail.RunAfter
		if updatedAt.IsZero() {
			updatedAt = mail.CreatedAt
		}
		items = append(items, ManualWorkItem{ID: mail.ID, Kind: "failed_mail", TargetID: mail.ID, Status: mail.Status, Risk: mail.LastError, Summary: mail.Subject, CreatedAt: mail.CreatedAt, UpdatedAt: updatedAt})
	}
	for _, report := range s.reports {
		if report == nil || report.Status != "open" {
			continue
		}
		items = append(items, ManualWorkItem{ID: report.ID, Kind: "open_report", TargetID: report.Target, Status: report.Status, Summary: report.Reason, CreatedAt: report.CreatedAt, UpdatedAt: report.CreatedAt})
	}
	sortManualWorkItems(items)
	return items, nil
}

func sortManualWorkItems(items []ManualWorkItem) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
}

func (s *Service) CreateGuestPaste(input GuestCreatePasteInput) (string, PasteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.runtimeConfig.GuestUploads
	if !cfg.Enabled {
		return "", PasteView{}, E(http.StatusForbidden, "guest_uploads_disabled", "guest uploads are disabled")
	}
	if cfg.RequireTurnstile {
		if err := s.verifyTurnstileLocked(context.Background(), input.TurnstileToken, input.RemoteIP); err != nil {
			return "", PasteView{}, err
		}
	}
	token := strings.TrimSpace(input.Token)
	if token == "" {
		token = newToken()
	}
	user, err := s.guestUserForTokenLocked(token)
	if err != nil {
		return "", PasteView{}, err
	}
	plan := guestPlan(cfg)
	tags := normalizeTags(input.Tags)
	if err := s.ensureCanCreatePasteLocked(user, plan, PasteInput{Title: input.Title, Text: input.Text, Tags: tags, ExpiresInSeconds: input.ExpiresInSeconds}, 0, 0); err != nil {
		return "", PasteView{}, err
	}
	now := s.now().UTC()
	expiresSeconds := input.ExpiresInSeconds
	if expiresSeconds <= 0 || expiresSeconds > cfg.RetentionSeconds {
		expiresSeconds = cfg.RetentionSeconds
	}
	paste := &Paste{
		ID:         s.newID("gstpst"),
		UserID:     user.ID,
		Title:      strings.TrimSpace(input.Title),
		Text:       input.Text,
		Tags:       tags,
		Status:     "active",
		ScanStatus: "clean",
		ExpiresAt:  now.Add(time.Duration(expiresSeconds) * time.Second),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if textBytes := int64(len([]byte(paste.Text))); textBytes > 0 {
		if err := s.recordDailyUploadLocked(user.ID, textBytes); err != nil {
			return "", PasteView{}, err
		}
	}
	if err := s.createPasteLocked(paste); err != nil {
		return "", PasteView{}, err
	}
	return token, s.viewPasteLocked(paste), nil
}

func (s *Service) AddGuestAttachment(token string, pasteID string, fileName string, contentType string, content []byte, turnstileToken string, remoteIP string) (AttachmentView, error) {
	return s.AddGuestAttachmentStream(token, pasteID, fileName, contentType, bytes.NewReader(content), turnstileToken, remoteIP)
}

func (s *Service) CreateGuestShare(token string, pasteID string, input ShareInput) (ShareView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.runtimeConfig.GuestUploads
	if !cfg.Enabled {
		return ShareView{}, E(http.StatusForbidden, "guest_uploads_disabled", "guest uploads are disabled")
	}
	if input.LoginRequired {
		return ShareView{}, E(http.StatusBadRequest, "guest_share_login_required", "guest shares cannot require login")
	}
	user, err := s.guestUserForTokenLocked(strings.TrimSpace(token))
	if err != nil {
		return ShareView{}, err
	}
	paste, err := s.pasteByIDLocked(pasteID)
	if err != nil || paste.UserID != user.ID {
		return ShareView{}, E(http.StatusNotFound, "paste_not_found", "paste not found")
	}
	if !s.isPasteVisibleLocked(paste) {
		return ShareView{}, E(http.StatusGone, "paste_expired", "paste has expired")
	}
	return s.createShareForPasteLocked(user.ID, paste, input)
}

func guestPlan(cfg GuestUploadConfig) plans.Plan {
	return plans.Plan{
		ID:                       "guest",
		Name:                     "Guest",
		ActivePasteLimit:         cfg.ActivePasteLimit,
		ActiveStorageBytes:       cfg.ActiveStorageBytes,
		SingleTextBytes:          cfg.SingleTextBytes,
		SingleFileBytes:          cfg.SingleFileBytes,
		SinglePasteBytes:         cfg.SinglePasteBytes,
		AttachmentsPerPasteLimit: cfg.AttachmentsPerPasteLimit,
		TagsPerPasteLimit:        0,
		MaxRetentionSeconds:      cfg.RetentionSeconds,
		DailyUploadBytes:         cfg.DailyUploadBytes,
		DailyShareDownloadBytes:  cfg.DailyShareDownloadBytes,
	}
}

func (s *Service) guestUserForTokenLocked(token string) (*User, error) {
	if strings.TrimSpace(token) == "" {
		return nil, E(http.StatusUnauthorized, "guest_token_required", "guest token is required")
	}
	hash := tokenHash(token)
	email := "guest+" + hash[:16] + "@" + guestEmailDomain
	if user, err := s.userByEmailLocked(email); err == nil {
		return user, nil
	} else if !isStoreNotFound(err) && !isAppStatus(err, http.StatusNotFound) {
		return nil, err
	}
	now := s.now().UTC()
	hashPassword, err := hashPassword(newToken())
	if err != nil {
		return nil, err
	}
	user := &User{
		ID:            s.newID("gst"),
		Email:         email,
		DisplayName:   "Guest",
		Language:      "en",
		PasswordHash:  hashPassword,
		Role:          "user",
		EmailVerified: true,
		PlanID:        "free",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.createUserLocked(user); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *Service) verifyTurnstileLocked(ctx context.Context, token string, remoteIP string) error {
	now := s.now().UTC()
	for hash, seenAt := range s.turnstileTokenHashes {
		if now.Sub(seenAt) > 5*time.Minute {
			delete(s.turnstileTokenHashes, hash)
		}
	}
	hash := tokenHash(token)
	if _, exists := s.turnstileTokenHashes[hash]; exists {
		return E(http.StatusForbidden, "turnstile_timeout_or_duplicate", "Turnstile token was already used")
	}
	if s.turnstileVerifier == nil {
		return E(http.StatusServiceUnavailable, "turnstile_not_configured", "Turnstile verifier is not configured")
	}
	s.mu.Unlock()
	err := s.turnstileVerifier.Verify(ctx, token, remoteIP)
	s.mu.Lock()
	if err != nil {
		return err
	}
	s.turnstileTokenHashes[hash] = now
	return nil
}

func (s *Service) SetAlertSender(sender AlertSender) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alertSender = sender
}

func (s *Service) SetTurnstileVerifier(verifier TurnstileVerifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnstileVerifier = verifier
	s.turnstileVerifierCustom = verifier != nil
}

func (s *Service) SetRuntimeResourceSnapshot(snapshot func() RuntimeResourceSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshot == nil {
		s.resourceSnapshot = defaultRuntimeResourceSnapshot
		return
	}
	s.resourceSnapshot = snapshot
}

func (s *Service) loadAlertEvents(ctx context.Context) error {
	if s.alerts == nil {
		return nil
	}
	events, err := s.alerts.ListAlertEvents(ctx, 100)
	if err != nil {
		return fmt.Errorf("load alert events: %w", err)
	}
	s.alertEvents = s.alertEvents[:0]
	for _, event := range events {
		s.cacheAlertEventLocked(event)
	}
	return nil
}

func (s *Service) cacheAlertEventLocked(event AlertEvent) *AlertEvent {
	cached := event
	for i, existing := range s.alertEvents {
		if existing.ID == cached.ID {
			s.alertEvents[i] = &cached
			return &cached
		}
	}
	s.alertEvents = append(s.alertEvents, &cached)
	return &cached
}

func (s *Service) alertEventsNewestLocked(limit int) []AlertEvent {
	out := make([]AlertEvent, 0, len(s.alertEvents))
	for _, event := range s.alertEvents {
		out = append(out, *event)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Service) EvaluateRuntimeAlerts(ctx context.Context) ([]AlertEvent, error) {
	s.mu.Lock()
	cfg := s.runtimeConfig.Alerts
	if !cfg.Enabled {
		s.mu.Unlock()
		return nil, nil
	}
	resources := s.resourceSnapshot()
	resources.ObjectStorageBytes, resources.ObjectStorageObjectCount = s.objectStorageUsageLocked()
	ops, err := s.operationalMetricsLocked(ctx)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	now := s.now().UTC()
	alerts := s.buildRuntimeAlertsLocked(cfg, resources, ops, now)
	sender := s.alertSender
	if sender == nil && cfg.TelegramEnabled {
		sender = NewTelegramSender(s.cfg)
	}
	s.mu.Unlock()

	for i := range alerts {
		event := alerts[i]
		sendErr := error(nil)
		if cfg.TelegramEnabled {
			if sender == nil {
				sendErr = E(http.StatusServiceUnavailable, "telegram_not_configured", "Telegram sender is not configured")
			} else {
				sendErr = sender.SendAlert(ctx, event.Message, cfg.Silent)
			}
		}
		s.mu.Lock()
		if sendErr != nil {
			event.Status = "failed"
			event.LastError = sanitizeAlertMessage(sendErr.Error(), managedSecretValues(s.managedSecrets)...)
		} else if cfg.TelegramEnabled {
			event.Status = "sent"
			sentAt := s.now().UTC()
			event.SentAt = &sentAt
			event.UpdatedAt = sentAt
		} else {
			event.Status = "suppressed"
		}
		if err := s.saveAlertEventLocked(event); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		alerts[i] = event
		s.mu.Unlock()
	}
	return alerts, nil
}

func (s *Service) buildRuntimeAlertsLocked(cfg AlertConfig, resources RuntimeResourceSnapshot, ops OperationalMetrics, now time.Time) []AlertEvent {
	type candidate struct {
		fingerprint string
		message     string
		active      bool
	}
	candidates := []candidate{
		{"cpu_high", fmt.Sprintf("PasteBox CPU usage high: %.1f%%", resources.CPUPercent), resources.CPUPercent >= cfg.CPUPercentThreshold},
		{"memory_high", fmt.Sprintf("PasteBox memory usage high: %.1f%%", resources.MemoryPercent), resources.MemoryPercent >= cfg.MemoryPercentThreshold},
		{"disk_high", fmt.Sprintf("PasteBox disk usage high: %.1f%%", resources.DiskPercent), resources.DiskPercent >= cfg.DiskPercentThreshold},
		{"object_storage_high", fmt.Sprintf("PasteBox object storage usage high: %d bytes", resources.ObjectStorageBytes), cfg.ObjectStorageBytesThreshold > 0 && resources.ObjectStorageBytes >= cfg.ObjectStorageBytesThreshold},
		{"scan_failures_high", fmt.Sprintf("PasteBox scan failure queue depth: %d", ops.ScanFailureDepth), cfg.ScanFailureDepthThreshold > 0 && ops.ScanFailureDepth >= cfg.ScanFailureDepthThreshold},
		{"failed_jobs_high", fmt.Sprintf("PasteBox failed job depth: %d", ops.FailedJobDepth), cfg.FailedJobDepthThreshold > 0 && ops.FailedJobDepth >= cfg.FailedJobDepthThreshold},
		{"failed_mails_high", fmt.Sprintf("PasteBox failed mail depth: %d", ops.MailFailedDepth), cfg.MailFailedDepthThreshold > 0 && ops.MailFailedDepth >= cfg.MailFailedDepthThreshold},
		{"reports_open_high", fmt.Sprintf("PasteBox open report count: %d", ops.ReportsOpen), cfg.ReportsOpenThreshold > 0 && ops.ReportsOpen >= cfg.ReportsOpenThreshold},
	}
	out := []AlertEvent{}
	for _, item := range candidates {
		if !item.active || s.alertInCooldownLocked(item.fingerprint, now, time.Duration(cfg.CooldownSeconds)*time.Second) {
			continue
		}
		out = append(out, AlertEvent{
			ID:          s.newID("alrt"),
			Fingerprint: item.fingerprint,
			Level:       "warning",
			Message:     sanitizeAlertMessage(item.message, managedSecretValues(s.managedSecrets)...),
			Status:      "pending",
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}
	return out
}

func (s *Service) alertInCooldownLocked(fingerprint string, now time.Time, cooldown time.Duration) bool {
	if cooldown <= 0 {
		return false
	}
	for _, event := range s.alertEvents {
		if event == nil || event.Fingerprint != fingerprint {
			continue
		}
		if now.Sub(event.UpdatedAt) < cooldown {
			return true
		}
	}
	return false
}

func (s *Service) saveAlertEventLocked(event AlertEvent) error {
	if event.UpdatedAt.IsZero() {
		event.UpdatedAt = s.now().UTC()
	}
	if s.alerts != nil {
		if _, exists := s.alertEventByIDLocked(event.ID); exists {
			if err := s.alerts.UpdateAlertEvent(context.Background(), event); err != nil {
				return err
			}
		} else if err := s.alerts.CreateAlertEvent(context.Background(), event); err != nil {
			return err
		}
	}
	s.cacheAlertEventLocked(event)
	return nil
}

func (s *Service) alertEventByIDLocked(id string) (*AlertEvent, bool) {
	for _, event := range s.alertEvents {
		if event != nil && event.ID == id {
			return event, true
		}
	}
	return nil, false
}

func (s *Service) AdminSendTestAlert(actorID string, message string) (AlertEvent, error) {
	s.mu.Lock()
	if err := s.requireAdminLocked(actorID); err != nil {
		s.mu.Unlock()
		return AlertEvent{}, err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "PasteBox Telegram alert test"
	}
	cfg := s.runtimeConfig.Alerts
	sender := s.alertSender
	if sender == nil {
		sender = NewTelegramSender(s.cfg)
	}
	now := s.now().UTC()
	event := AlertEvent{ID: s.newID("alrt"), Fingerprint: "manual_test", Level: "info", Message: sanitizeAlertMessage(message, managedSecretValues(s.managedSecrets)...), Status: "pending", CreatedAt: now, UpdatedAt: now}
	s.mu.Unlock()

	err := sender.SendAlert(context.Background(), event.Message, cfg.Silent)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		event.Status = "failed"
		event.LastError = sanitizeAlertMessage(err.Error(), managedSecretValues(s.managedSecrets)...)
	} else {
		event.Status = "sent"
		sentAt := s.now().UTC()
		event.SentAt = &sentAt
		event.UpdatedAt = sentAt
	}
	if saveErr := s.saveAlertEventLocked(event); saveErr != nil {
		return AlertEvent{}, saveErr
	}
	if auditErr := s.auditLocked(actorID, "admin.alert_test", event.ID, map[string]any{"status": event.Status}); auditErr != nil {
		return AlertEvent{}, auditErr
	}
	return event, nil
}

func managedSecretValues(secrets config.ManagedSecrets) []string {
	return []string{
		secrets.S3AccessKey,
		secrets.S3SecretKey,
		secrets.GoogleClientSecret,
		secrets.GitHubClientSecret,
		secrets.TurnstileSecretKey,
		secrets.TelegramBotToken,
		secrets.SMTPPassword,
		secrets.StripeWebhookSecret,
		secrets.EpusdtSecretKey,
	}
}

func sanitizeAlertMessage(message string, managedSecrets ...string) string {
	message = strings.TrimSpace(message)
	if len(message) > 1000 {
		message = message[:1000]
	}
	secrets := append([]string{
		os.Getenv("PASTEBOX_TELEGRAM_BOT_TOKEN"),
		os.Getenv("PASTEBOX_SMTP_PASSWORD"),
		os.Getenv("PASTEBOX_GOOGLE_OAUTH_CLIENT_SECRET"),
		os.Getenv("PASTEBOX_TURNSTILE_SECRET_KEY"),
	}, managedSecrets...)
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return message
}

func (s *Service) AdminAlertEvents(actorID string) ([]AlertEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return nil, err
	}
	return s.alertEventsNewestLocked(100), nil
}
