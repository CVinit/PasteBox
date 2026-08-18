package config

const (
	ManagedConfigVersion      = 1
	DefaultTurnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	DefaultTelegramAPIBaseURL = "https://api.telegram.org"
)

type ManagedConfig struct {
	Version                      int                    `json:"version"`
	Site                         ManagedSiteConfig      `json:"site"`
	WorkerHeartbeatMaxAgeSeconds int                    `json:"workerHeartbeatMaxAgeSeconds"`
	S3                           ManagedS3Config        `json:"s3"`
	Scanner                      ScannerConfig          `json:"scanner"`
	GoogleOAuth                  ManagedOAuthConfig     `json:"googleOAuth"`
	GitHubOAuth                  ManagedOAuthConfig     `json:"githubOAuth"`
	Turnstile                    ManagedTurnstileConfig `json:"turnstile"`
	Telegram                     ManagedTelegramConfig  `json:"telegram"`
	MailerProvider               string                 `json:"mailerProvider"`
	SMTP                         ManagedSMTPConfig      `json:"smtp"`
	DevAuthTokens                bool                   `json:"devAuthTokens"`
	StripeEnabled                bool                   `json:"stripeEnabled"`
	EpusdtEnabled                bool                   `json:"epusdtEnabled"`
	Stripe                       ManagedStripeConfig    `json:"stripe"`
	Epusdt                       ManagedEpusdtConfig    `json:"epusdt"`
}

type ManagedSiteConfig struct {
	AppName            string   `json:"appName"`
	PublicURL          string   `json:"publicUrl"`
	SupportEmail       string   `json:"supportEmail"`
	AbuseEmail         string   `json:"abuseEmail"`
	CORSAllowedOrigins []string `json:"corsAllowedOrigins"`
}

type ManagedS3Config struct {
	Endpoint     string `json:"endpoint"`
	Bucket       string `json:"bucket"`
	Region       string `json:"region"`
	UsePathStyle bool   `json:"usePathStyle"`
}

type ManagedOAuthConfig struct {
	ClientID    string `json:"clientId"`
	RedirectURL string `json:"redirectUrl"`
}

type ManagedTurnstileConfig struct {
	SiteKey   string `json:"siteKey"`
	VerifyURL string `json:"verifyUrl"`
}

type ManagedTelegramConfig struct {
	ChatID     string `json:"chatId"`
	APIBaseURL string `json:"apiBaseUrl"`
}

type ManagedSMTPConfig struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	FromEmail string `json:"fromEmail"`
	FromName  string `json:"fromName"`
	TLSMode   string `json:"tlsMode"`
}

type ManagedStripeConfig struct {
	CheckoutURLTemplate string `json:"checkoutUrlTemplate"`
}

type ManagedEpusdtConfig struct {
	PID                 string `json:"pid"`
	CheckoutURLTemplate string `json:"checkoutUrlTemplate"`
	Address             string `json:"address"`
	Chain               string `json:"chain"`
}

// ManagedSecrets is intentionally separate from ManagedConfig so it cannot be
// serialized by admin or public runtime-config responses.
type ManagedSecrets struct {
	S3AccessKey         string
	S3SecretKey         string
	GoogleClientSecret  string
	GitHubClientSecret  string
	TurnstileSecretKey  string
	TelegramBotToken    string
	SMTPPassword        string
	StripeWebhookSecret string
	EpusdtSecretKey     string
}

func ManagedFromConfig(cfg Config) (ManagedConfig, ManagedSecrets) {
	managed := ManagedConfig{
		Version: ManagedConfigVersion,
		Site: ManagedSiteConfig{
			AppName:            cfg.AppName,
			PublicURL:          cfg.PublicURL,
			SupportEmail:       cfg.SupportEmail,
			AbuseEmail:         cfg.AbuseEmail,
			CORSAllowedOrigins: append([]string{}, cfg.CORSAllowedOrigins...),
		},
		WorkerHeartbeatMaxAgeSeconds: cfg.WorkerHeartbeatMaxAgeSeconds,
		S3: ManagedS3Config{
			Endpoint:     cfg.S3.Endpoint,
			Bucket:       cfg.S3.Bucket,
			Region:       cfg.S3.Region,
			UsePathStyle: cfg.S3.UsePathStyle,
		},
		Scanner:        cfg.Scanner,
		GoogleOAuth:    ManagedOAuthConfig{ClientID: cfg.GoogleOAuth.ClientID, RedirectURL: cfg.GoogleOAuth.RedirectURL},
		GitHubOAuth:    ManagedOAuthConfig{ClientID: cfg.GitHubOAuth.ClientID, RedirectURL: cfg.GitHubOAuth.RedirectURL},
		Turnstile:      ManagedTurnstileConfig{SiteKey: cfg.Turnstile.SiteKey, VerifyURL: cfg.Turnstile.VerifyURL},
		Telegram:       ManagedTelegramConfig{ChatID: cfg.Telegram.ChatID, APIBaseURL: cfg.Telegram.APIBaseURL},
		MailerProvider: cfg.MailerProvider,
		SMTP: ManagedSMTPConfig{
			Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, Username: cfg.SMTP.Username,
			FromEmail: cfg.SMTP.FromEmail, FromName: cfg.SMTP.FromName, TLSMode: cfg.SMTP.TLSMode,
		},
		DevAuthTokens: cfg.DevAuthTokens,
		StripeEnabled: cfg.StripeEnabled,
		EpusdtEnabled: cfg.EpusdtEnabled,
		Stripe:        ManagedStripeConfig{CheckoutURLTemplate: cfg.Stripe.CheckoutURLTemplate},
		Epusdt: ManagedEpusdtConfig{
			PID: cfg.Epusdt.PID, CheckoutURLTemplate: cfg.Epusdt.CheckoutURLTemplate,
			Address: cfg.Epusdt.Address, Chain: cfg.Epusdt.Chain,
		},
	}
	secrets := ManagedSecrets{
		S3AccessKey: cfg.S3.AccessKey, S3SecretKey: cfg.S3.SecretKey,
		GoogleClientSecret: cfg.GoogleOAuth.ClientSecret, GitHubClientSecret: cfg.GitHubOAuth.ClientSecret,
		TurnstileSecretKey: cfg.Turnstile.SecretKey, TelegramBotToken: cfg.Telegram.BotToken,
		SMTPPassword: cfg.SMTP.Password, StripeWebhookSecret: cfg.Stripe.WebhookSecret,
		EpusdtSecretKey: cfg.Epusdt.SecretKey,
	}
	return managed, secrets
}

func ApplyManaged(root Config, managed ManagedConfig, secrets ManagedSecrets) Config {
	root.AppName = managed.Site.AppName
	root.PublicURL = managed.Site.PublicURL
	root.SupportEmail = managed.Site.SupportEmail
	root.AbuseEmail = managed.Site.AbuseEmail
	root.CORSAllowedOrigins = append([]string{}, managed.Site.CORSAllowedOrigins...)
	root.WorkerHeartbeatMaxAgeSeconds = managed.WorkerHeartbeatMaxAgeSeconds
	root.S3 = S3Config{
		Endpoint: managed.S3.Endpoint, Bucket: managed.S3.Bucket, Region: managed.S3.Region,
		AccessKey: secrets.S3AccessKey, SecretKey: secrets.S3SecretKey, UsePathStyle: managed.S3.UsePathStyle,
	}
	root.Scanner = managed.Scanner
	root.GoogleOAuth = OAuthConfig{ClientID: managed.GoogleOAuth.ClientID, ClientSecret: secrets.GoogleClientSecret, RedirectURL: managed.GoogleOAuth.RedirectURL}
	root.GitHubOAuth = OAuthConfig{ClientID: managed.GitHubOAuth.ClientID, ClientSecret: secrets.GitHubClientSecret, RedirectURL: managed.GitHubOAuth.RedirectURL}
	root.Turnstile = TurnstileConfig{SiteKey: managed.Turnstile.SiteKey, SecretKey: secrets.TurnstileSecretKey, VerifyURL: managed.Turnstile.VerifyURL}
	root.Telegram = TelegramConfig{BotToken: secrets.TelegramBotToken, ChatID: managed.Telegram.ChatID, APIBaseURL: managed.Telegram.APIBaseURL}
	root.MailerProvider = managed.MailerProvider
	root.SMTP = SMTPConfig{
		Host: managed.SMTP.Host, Port: managed.SMTP.Port, Username: managed.SMTP.Username,
		Password: secrets.SMTPPassword, FromEmail: managed.SMTP.FromEmail,
		FromName: managed.SMTP.FromName, TLSMode: managed.SMTP.TLSMode,
	}
	root.DevAuthTokens = managed.DevAuthTokens
	root.StripeEnabled = managed.StripeEnabled
	root.EpusdtEnabled = managed.EpusdtEnabled
	root.Stripe = StripeConfig{WebhookSecret: secrets.StripeWebhookSecret, CheckoutURLTemplate: managed.Stripe.CheckoutURLTemplate}
	root.Epusdt = EpusdtConfig{
		PID: managed.Epusdt.PID, SecretKey: secrets.EpusdtSecretKey,
		CheckoutURLTemplate: managed.Epusdt.CheckoutURLTemplate,
		Address:             managed.Epusdt.Address, Chain: managed.Epusdt.Chain,
	}
	return root
}
