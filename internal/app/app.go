package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"pastebox/internal/config"
	"pastebox/internal/plans"
)

type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

func E(status int, code string, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func ErrorResponse(err error) (int, map[string]any) {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Status, map[string]any{"error": appErr.Code, "message": appErr.Message}
	}
	return http.StatusInternalServerError, map[string]any{"error": "internal_error", "message": "internal server error"}
}

type Service struct {
	mu sync.Mutex
	// objectWriteMu keeps object writes and metadata commits atomic without holding mu across network I/O.
	objectWriteMu sync.Mutex
	cfg           config.Config
	now           func() time.Time
	catalog       plans.Catalog
	catalogStore  CatalogStore
	auth          AuthStores
	content       ContentStores
	objectStore   ObjectStore
	ops           OperationalStores
	audit         AuditLogStore
	runtime       RuntimeConfigStore
	redemptions   RedemptionStore
	alerts        AlertEventStore

	usersByID             map[string]*User
	userIDByEmail         map[string]string
	sessionsByID          map[string]*Session
	emailVerifies         map[string]*AuthToken
	passwordResets        map[string]*AuthToken
	loginFailures         map[string]*LoginFailure
	oauthIdentities       map[string]*OAuthIdentity
	pastesByID            map[string]*Paste
	attachmentsByID       map[string]*Attachment
	objects               map[string][]byte
	objectRefs            map[string]int
	dailyMetrics          DailyMetricStore
	sharesByID            map[string]*Share
	shareIDByToken        map[string]string
	ordersByID            map[string]*Order
	webhookEventKeys      map[string]string
	webhookEvents         []*WebhookEvent
	auditLogs             []*AuditLog
	reports               []*Report
	cleanupJobs           []*QueueItem
	cleanupFailures       []*QueueItem
	scanJobs              []*QueueItem
	scanFailures          []*QueueItem
	failedJobs            []*QueueItem
	mails                 []*Mail
	runtimeConfig         RuntimeConfig
	runtimeConfigChange   func(RuntimeConfig)
	redemptionBatches     map[string]*RedemptionBatch
	redemptionCodesByHash map[string]*RedemptionCode
	redemptionRecords     []*RedemptionRecord
	alertEvents           []*AlertEvent
	alertSender           AlertSender
	turnstileVerifier     TurnstileVerifier
	turnstileTokenHashes  map[string]time.Time
	resourceSnapshot      func() RuntimeResourceSnapshot
	nextID                int64
}

func New(cfg config.Config) *Service {
	return NewWithStores(cfg, AuthStores{}, nil)
}

func NewWithStores(cfg config.Config, authStores AuthStores, dailyMetrics DailyMetricStore) *Service {
	svc, err := NewWithStorage(context.Background(), cfg, Stores{
		Auth:         authStores,
		DailyMetrics: dailyMetrics,
	})
	if err != nil {
		panic(err)
	}
	return svc
}

func NewWithStorage(ctx context.Context, cfg config.Config, stores Stores) (*Service, error) {
	catalog := plans.DefaultCatalog()
	if stores.Catalog != nil {
		loaded, err := stores.Catalog.Catalog(ctx)
		if err != nil {
			return nil, fmt.Errorf("load plan catalog: %w", err)
		}
		catalog = cloneCatalog(loaded)
	}

	svc := &Service{
		cfg:                   cfg,
		now:                   time.Now,
		catalog:               catalog,
		catalogStore:          stores.Catalog,
		auth:                  stores.Auth,
		content:               stores.Content,
		objectStore:           stores.Objects,
		ops:                   stores.Operational,
		audit:                 stores.AuditLogs,
		runtime:               stores.RuntimeConfigs,
		redemptions:           stores.Redemptions,
		alerts:                stores.AlertEvents,
		usersByID:             map[string]*User{},
		userIDByEmail:         map[string]string{},
		sessionsByID:          map[string]*Session{},
		emailVerifies:         map[string]*AuthToken{},
		passwordResets:        map[string]*AuthToken{},
		loginFailures:         map[string]*LoginFailure{},
		oauthIdentities:       map[string]*OAuthIdentity{},
		pastesByID:            map[string]*Paste{},
		attachmentsByID:       map[string]*Attachment{},
		objects:               map[string][]byte{},
		objectRefs:            map[string]int{},
		dailyMetrics:          newMemoryDailyMetricStore(),
		sharesByID:            map[string]*Share{},
		shareIDByToken:        map[string]string{},
		ordersByID:            map[string]*Order{},
		webhookEventKeys:      map[string]string{},
		runtimeConfig:         defaultRuntimeConfig(cfg),
		redemptionBatches:     map[string]*RedemptionBatch{},
		redemptionCodesByHash: map[string]*RedemptionCode{},
		redemptionRecords:     []*RedemptionRecord{},
		alertEvents:           []*AlertEvent{},
		turnstileVerifier:     NewTurnstileVerifier(cfg),
		turnstileTokenHashes:  map[string]time.Time{},
		resourceSnapshot:      defaultRuntimeResourceSnapshot,
	}
	if stores.DailyMetrics != nil {
		svc.dailyMetrics = stores.DailyMetrics
	}
	if err := svc.loadRuntimeConfig(ctx); err != nil {
		return nil, err
	}
	if err := svc.loadRedemptionCaches(ctx); err != nil {
		return nil, err
	}
	if err := svc.loadAlertEvents(ctx); err != nil {
		return nil, err
	}
	if err := svc.loadContentCaches(ctx); err != nil {
		return nil, err
	}
	if err := svc.loadOperationalCaches(ctx); err != nil {
		return nil, err
	}
	if cfg.BootstrapAdminEmail != "" && cfg.BootstrapAdminPassword != "" {
		if _, err := svc.SeedAdmin(cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword); err != nil {
			return nil, fmt.Errorf("bootstrap admin: %w", err)
		}
	}
	return svc, nil
}

func NewForTest(now func() time.Time) *Service {
	svc := New(config.FromEnv())
	svc.now = now
	return svc
}

func NewWithDailyMetricStore(cfg config.Config, dailyMetrics DailyMetricStore) *Service {
	return NewWithStores(cfg, AuthStores{}, dailyMetrics)
}

type Stores struct {
	Auth           AuthStores
	Content        ContentStores
	Objects        ObjectStore
	Operational    OperationalStores
	DailyMetrics   DailyMetricStore
	Catalog        CatalogStore
	AuditLogs      AuditLogStore
	RuntimeConfigs RuntimeConfigStore
	Redemptions    RedemptionStore
	AlertEvents    AlertEventStore
}

type CatalogStore interface {
	Catalog(ctx context.Context) (plans.Catalog, error)
}

type AuditLogStore interface {
	RecordAuditLog(ctx context.Context, log AuditLog) error
	AuditLogs(ctx context.Context, limit int) ([]AuditLog, error)
	AuditLogsForActorOrTargets(ctx context.Context, actorID string, targets []string, limit int) ([]AuditLog, error)
}

type User struct {
	ID                string
	Email             string
	DisplayName       string
	Language          string
	PasswordHash      string
	Role              string
	EmailVerified     bool
	PlanID            string
	PlanExpiresAt     *time.Time
	Frozen            bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeleteRequestedAt *time.Time
	DeleteScheduledAt *time.Time
	DeletedAt         *time.Time
}

type UserView struct {
	ID                string     `json:"id"`
	Email             string     `json:"email"`
	DisplayName       string     `json:"displayName"`
	Language          string     `json:"language"`
	Role              string     `json:"role"`
	EmailVerified     bool       `json:"emailVerified"`
	PlanID            string     `json:"planId"`
	PlanExpiresAt     *time.Time `json:"planExpiresAt,omitempty"`
	OAuthProviders    []string   `json:"oauthProviders"`
	Frozen            bool       `json:"frozen"`
	CreatedAt         time.Time  `json:"createdAt"`
	DeleteRequestedAt *time.Time `json:"deleteRequestedAt,omitempty"`
	DeleteScheduledAt *time.Time `json:"deleteScheduledAt,omitempty"`
}

type Session struct {
	ID        string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

type AuthToken struct {
	Hash      string
	UserID    string
	Email     string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

type LoginFailure struct {
	Count       int
	WindowStart time.Time
	LockedUntil time.Time
}

type OAuthIdentity struct {
	UserID    string
	Provider  string
	Subject   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AuthResult struct {
	User                      UserView  `json:"user"`
	SessionID                 string    `json:"-"`
	ExpiresAt                 time.Time `json:"sessionExpiresAt"`
	DevEmailVerificationToken string    `json:"devEmailVerificationToken,omitempty"`
}

type Paste struct {
	ID            string
	UserID        string
	Title         string
	Text          string
	Tags          []string
	Pinned        bool
	Favorite      bool
	Status        string
	ScanStatus    string
	ExpiresAt     time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	AttachmentIDs []string
}

type PasteView struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	Text          string           `json:"text"`
	TextPreview   string           `json:"textPreview"`
	Tags          []string         `json:"tags"`
	Pinned        bool             `json:"pinned"`
	Favorite      bool             `json:"favorite"`
	Status        string           `json:"status"`
	ScanStatus    string           `json:"scanStatus"`
	ShareCount    int              `json:"shareCount"`
	SizeBytes     int64            `json:"sizeBytes"`
	ExpiresAt     time.Time        `json:"expiresAt"`
	CreatedAt     time.Time        `json:"createdAt"`
	UpdatedAt     time.Time        `json:"updatedAt"`
	Attachments   []AttachmentView `json:"attachments"`
	Expired       bool             `json:"expired"`
	SecondsToLive int64            `json:"secondsToLive"`
}

type Attachment struct {
	ID          string
	UserID      string
	PasteID     string
	FileName    string
	ContentType string
	Size        int64
	SHA256      string
	ObjectKey   string
	Status      string
	ScanStatus  string
	Risk        string
	ImageWidth  int
	ImageHeight int
	Content     []byte
	CreatedAt   time.Time
	DownloadN   int64
}

type ImagePreview struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type AttachmentView struct {
	ID            string        `json:"id"`
	PasteID       string        `json:"pasteId"`
	FileName      string        `json:"fileName"`
	ContentType   string        `json:"contentType"`
	Size          int64         `json:"size"`
	SHA256        string        `json:"sha256"`
	Status        string        `json:"status"`
	ScanStatus    string        `json:"scanStatus"`
	Risk          string        `json:"risk,omitempty"`
	ImagePreview  *ImagePreview `json:"imagePreview,omitempty"`
	DownloadCount int64         `json:"downloadCount"`
	CreatedAt     time.Time     `json:"createdAt"`
}

type Share struct {
	ID                string
	PasteID           string
	UserID            string
	TokenHash         string
	Token             string
	PasswordHash      string
	LoginRequired     bool
	MaxVisits         int
	MaxDownloads      int
	VisitCount        int
	DownloadCount     int
	ExpiresAt         time.Time
	RevokedAt         *time.Time
	CreatedAt         time.Time
	LastVisitedAt     *time.Time
	LastDownloadedAt  *time.Time
	LastAccessFailure *time.Time
}

type ShareView struct {
	ID               string     `json:"id"`
	PasteID          string     `json:"pasteId"`
	Token            string     `json:"token"`
	URL              string     `json:"url"`
	HasPassword      bool       `json:"hasPassword"`
	LoginRequired    bool       `json:"loginRequired"`
	MaxVisits        int        `json:"maxVisits"`
	MaxDownloads     int        `json:"maxDownloads"`
	VisitCount       int        `json:"visitCount"`
	DownloadCount    int        `json:"downloadCount"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	RevokedAt        *time.Time `json:"revokedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	LastVisitedAt    *time.Time `json:"lastVisitedAt,omitempty"`
	LastDownloadedAt *time.Time `json:"lastDownloadedAt,omitempty"`
}

type Order struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	Provider    string     `json:"provider"`
	PlanID      string     `json:"planId"`
	Period      string     `json:"period"`
	AmountCents int64      `json:"amountCents"`
	Currency    string     `json:"currency"`
	Status      string     `json:"status"`
	CheckoutURL string     `json:"checkoutUrl,omitempty"`
	Address     string     `json:"address,omitempty"`
	Chain       string     `json:"chain,omitempty"`
	TxID        string     `json:"txId,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	PaidAt      *time.Time `json:"paidAt,omitempty"`
}

type WebhookEvent struct {
	ID             string         `json:"id"`
	Provider       string         `json:"provider"`
	EventType      string         `json:"eventType"`
	TargetID       string         `json:"targetId"`
	IdempotencyKey string         `json:"idempotencyKey"`
	Processed      bool           `json:"processed"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	ReceivedAt     time.Time      `json:"receivedAt"`
}

type AuditLog struct {
	ID        string         `json:"id"`
	ActorID   string         `json:"actorId"`
	Action    string         `json:"action"`
	Target    string         `json:"target"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

type Report struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId,omitempty"`
	Target    string    `json:"target"`
	Reason    string    `json:"reason"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

type QueueItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	TargetID  string    `json:"targetId"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	Attempts  int       `json:"attempts"`
	RunAfter  time.Time `json:"runAfter"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type BillingPrice struct {
	ID              string `json:"id"`
	PlanID          string `json:"planId"`
	Period          string `json:"period"`
	AmountCents     int64  `json:"amountCents"`
	Currency        string `json:"currency"`
	Visible         bool   `json:"visible"`
	PurchaseEnabled bool   `json:"purchaseEnabled"`
	StripeEnabled   bool   `json:"stripeEnabled"`
	EpusdtEnabled   bool   `json:"epusdtEnabled"`
}

type AdminAttachmentView struct {
	AttachmentView
	UserID     string `json:"userId"`
	PasteTitle string `json:"pasteTitle"`
}

type AdminShareView struct {
	ShareView
	UserID string `json:"userId"`
}

type Mail struct {
	ID        string    `json:"id"`
	To        string    `json:"to"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

type MailQueueItem struct {
	ID        string     `json:"id"`
	To        string     `json:"to"`
	Subject   string     `json:"subject"`
	Status    string     `json:"status"`
	Attempts  int        `json:"attempts"`
	LastError string     `json:"lastError,omitempty"`
	RunAfter  time.Time  `json:"runAfter"`
	CreatedAt time.Time  `json:"createdAt"`
	SentAt    *time.Time `json:"sentAt,omitempty"`
}

type OperationalMetrics struct {
	UserCount           int            `json:"userCount"`
	ActivePastes        int            `json:"activePastes"`
	ActiveStorageBytes  int64          `json:"activeStorageBytes"`
	ReportsOpen         int            `json:"reportsOpen"`
	CleanupQueueDepth   int            `json:"cleanupQueueDepth"`
	CleanupFailureDepth int            `json:"cleanupFailureDepth"`
	ScanQueueDepth      int            `json:"scanQueueDepth"`
	ScanFailureDepth    int            `json:"scanFailureDepth"`
	FailedJobDepth      int            `json:"failedJobDepth"`
	MailQueueDepth      int            `json:"mailQueueDepth"`
	MailFailedDepth     int            `json:"mailFailedDepth"`
	WebhookEvents       int            `json:"webhookEvents"`
	OrdersByStatus      map[string]int `json:"ordersByStatus"`
}

type QuotaView struct {
	Plan                    plans.Plan `json:"plan"`
	ActivePasteCount        int        `json:"activePasteCount"`
	ActiveStorageBytes      int64      `json:"activeStorageBytes"`
	DailyUploadBytes        int64      `json:"dailyUploadBytes"`
	DailyShareDownloadBytes int64      `json:"dailyShareDownloadBytes"`
	OverLimit               bool       `json:"overLimit"`
}

type RegisterInput struct {
	Email                 string
	Password              string
	DisplayName           string
	Language              string
	EmailVerificationCode string
	TurnstileToken        string
	RemoteIP              string
}

type PasteInput struct {
	Title            string
	Text             string
	Tags             []string
	Pinned           bool
	Favorite         bool
	ExpiresInSeconds int64
}

type PastePatch struct {
	Title    *string
	Text     *string
	Tags     []string
	HasTags  bool
	Pinned   *bool
	Favorite *bool
}

type ShareInput struct {
	Password         string
	LoginRequired    bool
	MaxVisits        int
	MaxDownloads     int
	ExpiresInSeconds int64
}

type BillingWebhookInput struct {
	Provider       string
	EventType      string
	OrderID        string
	TxID           string
	IdempotencyKey string
	Metadata       map[string]any
}

type ListOptions struct {
	Query  string
	Filter string
	Tag    string
}

func (s *Service) StartRegistrationEmailVerification(_ context.Context, email string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return nil, E(http.StatusBadRequest, "invalid_email", "valid email is required")
	}
	if err := s.ensureAllowedRegistrationEmailLocked(email); err != nil {
		return nil, err
	}
	if _, err := s.userByEmailLocked(email); err == nil {
		return nil, E(http.StatusConflict, "email_exists", "email is already registered")
	} else if !isStoreNotFound(err) && !isAppStatus(err, http.StatusNotFound) {
		return nil, err
	}

	token := verificationCode()
	authToken := AuthToken{Hash: registrationVerificationHash(email, token), Email: email, ExpiresAt: s.now().UTC().Add(15 * time.Minute)}
	if err := s.createAuthTokenLocked("registration_email_verification", authToken); err != nil {
		return nil, err
	}
	s.emailVerifies[authToken.Hash] = &authToken
	if err := s.mail(email, "Your PasteBox registration code", fmt.Sprintf("Your PasteBox registration code is %s.\n\nThis code expires in 15 minutes.", token)); err != nil {
		return nil, err
	}
	return s.authTokenResponse(token, "registration verification sent"), nil
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (AuthResult, error) {
	email := normalizeEmail(input.Email)
	if email == "" || !strings.Contains(email, "@") {
		return AuthResult{}, E(http.StatusBadRequest, "invalid_email", "valid email is required")
	}
	if len(input.Password) < 8 {
		return AuthResult{}, E(http.StatusBadRequest, "weak_password", "password must be at least 8 characters")
	}
	if err := s.verifyRegistrationTurnstile(ctx, input.TurnstileToken, input.RemoteIP); err != nil {
		return AuthResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureAllowedRegistrationEmailLocked(email); err != nil {
		return AuthResult{}, err
	}
	if _, err := s.userByEmailLocked(email); err == nil {
		return AuthResult{}, E(http.StatusConflict, "email_exists", "email is already registered")
	} else if !isStoreNotFound(err) && !isAppStatus(err, http.StatusNotFound) {
		return AuthResult{}, err
	}
	if s.runtimeConfig.Registration.RequireEmailVerification {
		if err := s.consumeRegistrationEmailVerificationLocked(email, input.EmailVerificationCode); err != nil {
			return AuthResult{}, err
		}
	}

	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		return AuthResult{}, err
	}

	now := s.now().UTC()
	user := &User{
		ID:            s.newID("usr"),
		Email:         email,
		DisplayName:   defaultString(strings.TrimSpace(input.DisplayName), email),
		Language:      NormalizeUserLanguage(input.Language),
		PasswordHash:  passwordHash,
		Role:          "user",
		EmailVerified: true,
		PlanID:        "free",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.createUserLocked(user); err != nil {
		if errors.Is(err, ErrStoreConflict) {
			return AuthResult{}, E(http.StatusConflict, "email_exists", "email is already registered")
		}
		return AuthResult{}, err
	}
	if err := s.mail(user.Email, "Welcome to PasteBox", "Your PasteBox account is ready."); err != nil {
		return AuthResult{}, err
	}
	result, err := s.newSessionLocked(user)
	if err != nil {
		return AuthResult{}, err
	}
	return result, nil
}

func (s *Service) Login(_ context.Context, email string, password string) (AuthResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	email = normalizeEmail(email)
	if err := s.checkLoginRateLimitLocked(email); err != nil {
		return AuthResult{}, err
	}
	user, err := s.userByEmailLocked(email)
	if err != nil {
		if recordErr := s.recordLoginFailureLocked(email); recordErr != nil {
			return AuthResult{}, recordErr
		}
		return AuthResult{}, E(http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
	}
	if user.DeletedAt != nil || user.Frozen {
		return AuthResult{}, E(http.StatusForbidden, "account_unavailable", "account is unavailable")
	}
	if err := verifyPassword(user.PasswordHash, password); err != nil {
		if recordErr := s.recordLoginFailureLocked(email); recordErr != nil {
			return AuthResult{}, recordErr
		}
		return AuthResult{}, E(http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
	}
	if !user.EmailVerified {
		return AuthResult{}, E(http.StatusForbidden, "email_not_verified", "email verification is required before password login")
	}
	if err := s.deleteLoginFailureLocked(email); err != nil {
		return AuthResult{}, err
	}
	if err := s.mail(user.Email, "New PasteBox login", "A new device logged in to your PasteBox account."); err != nil {
		return AuthResult{}, err
	}
	return s.newSessionLocked(user)
}

func (s *Service) GoogleOAuth(ctx context.Context, email string, displayName string, googleSubject string, language string) (AuthResult, error) {
	return s.OAuthLogin(ctx, "google", email, displayName, googleSubject, language)
}

func (s *Service) GitHubOAuth(ctx context.Context, email string, displayName string, githubSubject string, language string) (AuthResult, error) {
	return s.OAuthLogin(ctx, "github", email, displayName, githubSubject, language)
}

func (s *Service) OAuthLogin(_ context.Context, provider string, email string, displayName string, subject string, language string) (AuthResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return AuthResult{}, E(http.StatusBadRequest, "invalid_oauth_provider", "oauth provider is required")
	}
	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return AuthResult{}, E(http.StatusBadRequest, "invalid_email", "valid email is required")
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return AuthResult{}, E(http.StatusBadRequest, "missing_oauth_subject", "oauth subject is required")
	}

	if identity, ok, err := s.oauthIdentityByProviderSubjectLocked(provider, subject); err != nil {
		return AuthResult{}, err
	} else if ok {
		user, err := s.activeUserLocked(identity.UserID)
		if err != nil {
			return AuthResult{}, err
		}
		if strings.TrimSpace(displayName) != "" {
			user.DisplayName = strings.TrimSpace(displayName)
			user.EmailVerified = true
			user.UpdatedAt = s.now().UTC()
			if err := s.updateUserLocked(user); err != nil {
				return AuthResult{}, err
			}
		}
		if err := s.auditLocked(user.ID, "auth."+provider+"_oauth", user.ID, map[string]any{"provider": provider}); err != nil {
			return AuthResult{}, err
		}
		return s.newSessionLocked(user)
	}

	if user, err := s.userByEmailLocked(email); err == nil {
		if user.DeletedAt != nil || user.Frozen {
			return AuthResult{}, E(http.StatusForbidden, "account_unavailable", "account is unavailable")
		}
		if identities, err := s.oauthIdentitiesByUserLocked(user.ID); err != nil {
			return AuthResult{}, err
		} else if hasOAuthProvider(identities, provider) {
			return AuthResult{}, E(http.StatusConflict, "oauth_identity_conflict", "oauth account is already linked to a different identity")
		}
		user.EmailVerified = true
		if strings.TrimSpace(displayName) != "" {
			user.DisplayName = strings.TrimSpace(displayName)
		}
		user.UpdatedAt = s.now().UTC()
		if err := s.updateUserLocked(user); err != nil {
			return AuthResult{}, err
		}
		now := s.now().UTC()
		if err := s.createOAuthIdentityLocked(&OAuthIdentity{UserID: user.ID, Provider: provider, Subject: subject, CreatedAt: now, UpdatedAt: now}); err != nil {
			return AuthResult{}, err
		}
		if err := s.auditLocked(user.ID, "auth.oauth_linked", user.ID, map[string]any{"provider": provider}); err != nil {
			return AuthResult{}, err
		}
		if err := s.auditLocked(user.ID, "auth."+provider+"_oauth", user.ID, map[string]any{"provider": provider}); err != nil {
			return AuthResult{}, err
		}
		return s.newSessionLocked(user)
	} else if !isStoreNotFound(err) && !isAppStatus(err, http.StatusNotFound) {
		return AuthResult{}, err
	}
	if err := s.ensureAllowedRegistrationEmailLocked(email); err != nil {
		return AuthResult{}, err
	}

	passwordHash, err := hashPassword(newToken())
	if err != nil {
		return AuthResult{}, err
	}
	now := s.now().UTC()
	user := &User{
		ID:            s.newID("usr"),
		Email:         email,
		DisplayName:   defaultString(strings.TrimSpace(displayName), email),
		Language:      NormalizeUserLanguage(language),
		PasswordHash:  passwordHash,
		Role:          "user",
		EmailVerified: true,
		PlanID:        "free",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.createUserLocked(user); err != nil {
		if errors.Is(err, ErrStoreConflict) {
			return AuthResult{}, E(http.StatusConflict, "email_exists", "email is already registered")
		}
		return AuthResult{}, err
	}
	if err := s.createOAuthIdentityLocked(&OAuthIdentity{UserID: user.ID, Provider: provider, Subject: subject, CreatedAt: now, UpdatedAt: now}); err != nil {
		return AuthResult{}, err
	}
	if err := s.auditLocked(user.ID, "auth.oauth_linked", user.ID, map[string]any{"provider": provider}); err != nil {
		return AuthResult{}, err
	}
	if err := s.auditLocked(user.ID, "auth."+provider+"_oauth", user.ID, map[string]any{"provider": provider}); err != nil {
		return AuthResult{}, err
	}
	if err := s.mail(user.Email, "Welcome to PasteBox", "Your "+provider+"-authenticated PasteBox account is ready."); err != nil {
		return AuthResult{}, err
	}
	return s.newSessionLocked(user)
}

func (s *Service) StartEmailVerification(userID string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(userID)
	if err != nil {
		return nil, err
	}
	if user.EmailVerified {
		return map[string]string{"message": "email already verified"}, nil
	}
	token, err := s.issueEmailVerificationLocked(user)
	if err != nil {
		return nil, err
	}
	return s.authTokenResponse(token, "verification sent"), nil
}

func (s *Service) FinishEmailVerification(token string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	authToken, err := s.consumeTokenLocked("email_verification", s.emailVerifies, token)
	if err != nil {
		return UserView{}, err
	}
	user, err := s.activeUserLocked(authToken.UserID)
	if err != nil {
		return UserView{}, E(http.StatusUnauthorized, "user_not_found", "user not found")
	}
	user.EmailVerified = true
	user.UpdatedAt = s.now().UTC()
	if err := s.updateUserLocked(user); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(user)
}

func (s *Service) StartPasswordReset(_ context.Context, email string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.userByEmailLocked(email)
	if err != nil {
		return nil, err
	}
	if !user.EmailVerified {
		return nil, E(http.StatusForbidden, "email_not_verified", "verify email before requesting password reset")
	}
	token := newToken()
	hash := tokenHash(token)
	authToken := AuthToken{Hash: hash, UserID: user.ID, Email: user.Email, ExpiresAt: s.now().UTC().Add(30 * time.Minute)}
	if err := s.createAuthTokenLocked("password_reset", authToken); err != nil {
		return nil, err
	}
	s.passwordResets[hash] = &authToken
	if err := s.mail(user.Email, "Reset your PasteBox password", s.authLinkBody("Reset your PasteBox password", "/password-reset", token, 30*time.Minute)); err != nil {
		return nil, err
	}
	return s.authTokenResponse(token, "password reset sent"), nil
}

func (s *Service) FinishPasswordReset(_ context.Context, token string, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(password) < 8 {
		return E(http.StatusBadRequest, "weak_password", "password must be at least 8 characters")
	}
	authToken, err := s.consumeTokenLocked("password_reset", s.passwordResets, token)
	if err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	user, err := s.activeUserLocked(authToken.UserID)
	if err != nil {
		return err
	}
	user.PasswordHash = hash
	user.UpdatedAt = s.now().UTC()
	if err := s.updateUserLocked(user); err != nil {
		return err
	}
	if err := s.revokeUserSessionsLocked(user.ID); err != nil {
		return err
	}
	if err := s.mail(user.Email, "PasteBox password changed", "Your password was changed."); err != nil {
		return err
	}
	return nil
}

func (s *Service) UserForSession(sessionID string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.userForSessionLocked(sessionID)
	if err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(user)
}

func (s *Service) Logout(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	if s.auth.Sessions != nil {
		_ = s.auth.Sessions.RevokeSession(context.Background(), sessionID, now)
	}
	if session := s.sessionsByID[sessionID]; session != nil {
		session.RevokedAt = &now
	}
}

func (s *Service) LogoutAll(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.revokeUserSessionsLocked(userID)
}

func (s *Service) UpdateProfile(userID string, displayName string, language string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(userID)
	if err != nil {
		return UserView{}, err
	}
	if strings.TrimSpace(displayName) != "" {
		user.DisplayName = strings.TrimSpace(displayName)
	}
	if strings.TrimSpace(language) != "" {
		user.Language = NormalizeUserLanguage(language)
	}
	user.UpdatedAt = s.now().UTC()
	if err := s.updateUserLocked(user); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(user)
}

func (s *Service) UnlinkOAuthIdentity(userID string, provider string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(userID)
	if err != nil {
		return UserView{}, err
	}
	provider = normalizeProvider(provider)
	if provider == "" {
		return UserView{}, E(http.StatusBadRequest, "invalid_oauth_provider", "oauth provider is required")
	}
	identities, err := s.oauthIdentitiesByUserLocked(user.ID)
	if err != nil {
		return UserView{}, err
	}
	if !hasOAuthProvider(identities, provider) {
		return UserView{}, E(http.StatusNotFound, "oauth_identity_not_linked", "oauth provider is not linked")
	}
	if err := s.deleteOAuthIdentityLocked(user.ID, provider); err != nil {
		return UserView{}, err
	}
	if err := s.auditLocked(user.ID, "auth.oauth_unlinked", user.ID, map[string]any{"provider": provider}); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(user)
}

func (s *Service) RequestAccountDeletion(userID string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(userID)
	if err != nil {
		return UserView{}, err
	}
	now := s.now().UTC()
	scheduled := now.Add(7 * 24 * time.Hour)
	user.DeleteRequestedAt = &now
	user.DeleteScheduledAt = &scheduled
	if err := s.updateUserLocked(user); err != nil {
		return UserView{}, err
	}
	if err := s.auditLocked(user.ID, "account.deletion_requested", user.ID, map[string]any{"scheduledAt": scheduled}); err != nil {
		return UserView{}, err
	}
	if err := s.mail(user.Email, "PasteBox account deletion requested", "Your account is scheduled for deletion in 7 days."); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(user)
}

func (s *Service) CancelAccountDeletion(userID string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(userID)
	if err != nil {
		return UserView{}, err
	}
	user.DeleteRequestedAt = nil
	user.DeleteScheduledAt = nil
	if err := s.updateUserLocked(user); err != nil {
		return UserView{}, err
	}
	if err := s.auditLocked(user.ID, "account.deletion_canceled", user.ID, nil); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(user)
}

func (s *Service) ExecuteAccountDeletion(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(userID)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	user.DeletedAt = &now
	user.Frozen = true
	user.DeleteRequestedAt = &now
	user.DeleteScheduledAt = &now
	if err := s.updateUserLocked(user); err != nil {
		return err
	}
	pasteCount := 0
	for _, paste := range s.pastesByID {
		if paste.UserID == user.ID && paste.Status == "active" {
			paste.Status = "pending_delete"
			paste.UpdatedAt = now
			if err := s.updatePasteLocked(paste); err != nil {
				return err
			}
			pasteCount++
		}
	}
	shareCount := 0
	for _, share := range s.sharesByID {
		if share.UserID == user.ID && share.RevokedAt == nil {
			share.RevokedAt = &now
			if err := s.updateShareLocked(share); err != nil {
				return err
			}
			shareCount++
		}
	}
	if err := s.revokeUserSessionsLocked(user.ID); err != nil {
		return err
	}
	return s.auditLocked(user.ID, "account.deleted", user.ID, map[string]any{
		"pasteCount": pasteCount,
		"shareCount": shareCount,
	})
}

func (s *Service) CreatePaste(userID string, input PasteInput) (PasteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(userID)
	if err != nil {
		return PasteView{}, err
	}
	plan, err := s.planForUserLocked(user)
	if err != nil {
		return PasteView{}, err
	}
	if err := s.ensureCanCreatePasteLocked(user, plan, input, 0, 0); err != nil {
		return PasteView{}, err
	}
	tags := normalizeTags(input.Tags)
	now := s.now().UTC()
	expiresAt := resolveExpiresAt(now, input.ExpiresInSeconds, plan)
	paste := &Paste{
		ID:         s.newID("pst"),
		UserID:     user.ID,
		Title:      strings.TrimSpace(input.Title),
		Text:       input.Text,
		Tags:       tags,
		Pinned:     input.Pinned,
		Favorite:   input.Favorite,
		Status:     "active",
		ScanStatus: "clean",
		ExpiresAt:  expiresAt,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if textBytes := int64(len([]byte(paste.Text))); textBytes > 0 {
		if err := s.recordDailyUploadLocked(user.ID, textBytes); err != nil {
			return PasteView{}, err
		}
	}
	if err := s.createPasteLocked(paste); err != nil {
		return PasteView{}, err
	}
	return s.viewPasteLocked(paste), nil
}

func (s *Service) ListPastes(userID string, opts ListOptions) ([]PasteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.activeUserLocked(userID); err != nil {
		return nil, err
	}
	if err := s.refreshContentCachesLocked(context.Background()); err != nil {
		return nil, err
	}
	out := []PasteView{}
	query := strings.ToLower(strings.TrimSpace(opts.Query))
	filter := strings.TrimSpace(opts.Filter)
	tag := strings.ToLower(strings.TrimSpace(opts.Tag))
	for _, paste := range s.pastesByID {
		if paste.UserID != userID || !s.isPasteVisibleLocked(paste) {
			continue
		}
		view := s.viewPasteLocked(paste)
		if !matchesPaste(view, query, filter, tag) {
			continue
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *Service) GetPaste(userID string, id string) (PasteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(userID, id)
	if err != nil {
		return PasteView{}, err
	}
	if !s.isPasteVisibleLocked(paste) {
		return PasteView{}, E(http.StatusGone, "paste_expired", "paste is expired or deleted")
	}
	return s.viewPasteLocked(paste), nil
}

func (s *Service) UpdatePaste(userID string, id string, patch PastePatch) (PasteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(userID, id)
	if err != nil {
		return PasteView{}, err
	}
	if !s.isPasteVisibleLocked(paste) {
		return PasteView{}, E(http.StatusGone, "paste_expired", "paste is expired or deleted")
	}
	user := s.usersByID[userID]
	plan, _ := s.planForUserLocked(user)
	nextText := paste.Text
	if patch.Text != nil {
		nextText = *patch.Text
	}
	if int64(len([]byte(nextText))) > plan.SingleTextBytes {
		return PasteView{}, E(http.StatusRequestEntityTooLarge, "text_too_large", "text exceeds plan limit")
	}
	currentTextBytes := int64(len([]byte(paste.Text)))
	nextTextBytes := int64(len([]byte(nextText)))
	attachmentsBytes := s.pasteSizeLocked(paste) - currentTextBytes
	if attachmentsBytes+nextTextBytes > plan.SinglePasteBytes {
		return PasteView{}, E(http.StatusRequestEntityTooLarge, "paste_too_large", "paste exceeds plan total size")
	}
	quota, err := s.quotaLocked(user.ID, plan)
	if err != nil {
		return PasteView{}, err
	}
	nextStorageBytes := quota.ActiveStorageBytes - currentTextBytes + nextTextBytes
	if nextStorageBytes > plan.ActiveStorageBytes {
		return PasteView{}, E(http.StatusForbidden, "storage_limit", "active storage exceeds plan limit")
	}
	if textDelta := nextTextBytes - currentTextBytes; textDelta > 0 && quota.DailyUploadBytes+textDelta > plan.DailyUploadBytes {
		return PasteView{}, E(http.StatusForbidden, "daily_upload_limit", "daily upload traffic exceeds plan limit")
	}
	var nextTags []string
	if patch.HasTags {
		nextTags = normalizeTags(patch.Tags)
		if err := ensureTagsWithinPlan(plan, paste.Tags, nextTags); err != nil {
			return PasteView{}, err
		}
	}
	if textDelta := nextTextBytes - currentTextBytes; textDelta > 0 {
		if err := s.recordDailyUploadLocked(user.ID, textDelta); err != nil {
			return PasteView{}, err
		}
	}
	if patch.Title != nil {
		paste.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.Text != nil {
		paste.Text = *patch.Text
	}
	if patch.HasTags {
		paste.Tags = nextTags
	}
	if patch.Pinned != nil {
		paste.Pinned = *patch.Pinned
	}
	if patch.Favorite != nil {
		paste.Favorite = *patch.Favorite
	}
	paste.UpdatedAt = s.now().UTC()
	if err := s.updatePasteLocked(paste); err != nil {
		return PasteView{}, err
	}
	return s.viewPasteLocked(paste), nil
}

func (s *Service) DeletePaste(userID string, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(userID, id)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	paste.Status = "pending_delete"
	paste.UpdatedAt = now
	if err := s.updatePasteLocked(paste); err != nil {
		return err
	}
	for _, attachmentID := range paste.AttachmentIDs {
		if attachment := s.attachmentsByID[attachmentID]; attachment != nil {
			attachment.Status = "pending_delete"
			if err := s.updateAttachmentLocked(attachment); err != nil {
				return err
			}
		}
	}
	for _, share := range s.sharesByID {
		if share.PasteID == paste.ID && share.RevokedAt == nil {
			share.RevokedAt = &now
			if err := s.updateShareLocked(share); err != nil {
				return err
			}
		}
	}
	if err := s.scheduleCleanupJobLocked(paste.ID, now); err != nil {
		return err
	}
	return nil
}

func (s *Service) ExtendPaste(userID string, id string, expiresInSeconds int64) (PasteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(userID, id)
	if err != nil {
		return PasteView{}, err
	}
	if !s.isPasteVisibleLocked(paste) {
		return PasteView{}, E(http.StatusGone, "paste_expired", "expired paste cannot be extended")
	}
	user := s.usersByID[userID]
	plan, _ := s.planForUserLocked(user)
	now := s.now().UTC()
	nextExpiresAt := resolveExpiresAt(now, expiresInSeconds, plan)
	if nextExpiresAt.Before(paste.ExpiresAt) {
		return PasteView{}, E(http.StatusBadRequest, "invalid_expiration", "new expiration must extend the paste")
	}
	if err := s.ensureUserCanWriteLocked(user, plan); err != nil {
		return PasteView{}, err
	}
	paste.ExpiresAt = nextExpiresAt
	paste.UpdatedAt = now
	if err := s.updatePasteLocked(paste); err != nil {
		return PasteView{}, err
	}
	return s.viewPasteLocked(paste), nil
}

func (s *Service) AddAttachment(userID string, pasteID string, fileName string, contentType string, content []byte) (AttachmentView, error) {
	upload, err := PrepareAttachmentUpload(fileName, contentType, bytes.NewReader(content))
	if err != nil {
		return AttachmentView{}, err
	}
	defer upload.Close()
	return s.AddPreparedAttachment(userID, pasteID, upload)
}

func (s *Service) DownloadAttachment(userID string, attachmentID string) (AttachmentView, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	attachment, err := s.attachmentByIDLocked(attachmentID)
	if err != nil || attachment.UserID != userID {
		return AttachmentView{}, nil, E(http.StatusNotFound, "attachment_not_found", "attachment not found")
	}
	paste, err := s.pasteByIDLocked(attachment.PasteID)
	if err != nil || !s.isPasteVisibleLocked(paste) || attachment.Status != "active" {
		return AttachmentView{}, nil, E(http.StatusGone, "attachment_unavailable", "attachment is unavailable")
	}
	if attachment.ScanStatus == "malicious" {
		return AttachmentView{}, nil, E(http.StatusForbidden, "malicious_file", "file is blocked")
	}
	content, err := s.objectContentLocked(attachment)
	if err != nil {
		return AttachmentView{}, nil, E(http.StatusGone, "attachment_unavailable", "attachment content is unavailable")
	}
	attachment.DownloadN++
	if err := s.updateAttachmentLocked(attachment); err != nil {
		return AttachmentView{}, nil, err
	}
	return viewAttachment(attachment), content, nil
}

func (s *Service) CreateShare(userID string, pasteID string, input ShareInput) (ShareView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(userID, pasteID)
	if err != nil {
		return ShareView{}, err
	}
	return s.createShareForPasteLocked(userID, paste, input)
}

func (s *Service) createShareForPasteLocked(userID string, paste *Paste, input ShareInput) (ShareView, error) {
	if !s.isPasteVisibleLocked(paste) {
		return ShareView{}, E(http.StatusGone, "paste_expired", "cannot share expired paste")
	}
	for _, attachment := range s.attachmentsForPasteLocked(paste) {
		if attachment.Status == "active" && attachment.ScanStatus == "malicious" {
			return ShareView{}, E(http.StatusForbidden, "malicious_file", "known malicious files cannot be shared")
		}
	}
	now := s.now().UTC()
	expiresAt := paste.ExpiresAt
	if input.ExpiresInSeconds > 0 {
		requested := now.Add(time.Duration(input.ExpiresInSeconds) * time.Second)
		if requested.Before(expiresAt) {
			expiresAt = requested
		}
	}
	token := newToken()
	share := &Share{
		ID:               s.newID("shr"),
		PasteID:          paste.ID,
		UserID:           userID,
		Token:            token,
		TokenHash:        tokenHash(token),
		PasswordHash:     optionalPasswordHash(input.Password),
		LoginRequired:    input.LoginRequired,
		MaxVisits:        max(input.MaxVisits, 0),
		MaxDownloads:     max(input.MaxDownloads, 0),
		ExpiresAt:        expiresAt,
		CreatedAt:        now,
		LastVisitedAt:    nil,
		LastDownloadedAt: nil,
	}
	if err := s.createShareLocked(share); err != nil {
		return ShareView{}, err
	}
	return s.viewShareLocked(share), nil
}

func (s *Service) ListShares(userID string) ([]ShareView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.activeUserLocked(userID); err != nil {
		return nil, err
	}
	out := []ShareView{}
	for _, share := range s.sharesByID {
		if share.UserID == userID {
			out = append(out, s.viewShareLocked(share))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) RevokeShare(userID string, shareID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	share, err := s.shareByIDLocked(shareID)
	if err != nil || share.UserID != userID {
		return E(http.StatusNotFound, "share_not_found", "share not found")
	}
	now := s.now().UTC()
	share.RevokedAt = &now
	if err := s.updateShareLocked(share); err != nil {
		return err
	}
	return nil
}

func (s *Service) AccessShare(token string, password string, viewerUserID string) (PasteView, ShareView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	share, paste, err := s.validShareLocked(token, password, viewerUserID, false)
	if err != nil {
		return PasteView{}, ShareView{}, err
	}
	now := s.now().UTC()
	share.VisitCount++
	share.LastVisitedAt = &now
	if err := s.updateShareLocked(share); err != nil {
		return PasteView{}, ShareView{}, err
	}
	return s.viewPasteLocked(paste), s.viewShareLocked(share), nil
}

func (s *Service) DownloadSharedAttachment(token string, password string, attachmentID string, viewerUserID string) (AttachmentView, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	share, paste, err := s.validShareLocked(token, password, viewerUserID, true)
	if err != nil {
		return AttachmentView{}, nil, err
	}
	attachment, err := s.attachmentByIDLocked(attachmentID)
	if err != nil || attachment.PasteID != paste.ID || attachment.Status != "active" {
		return AttachmentView{}, nil, E(http.StatusNotFound, "attachment_not_found", "attachment not found")
	}
	if attachment.ScanStatus != "clean" {
		return AttachmentView{}, nil, E(http.StatusForbidden, "scan_not_clean", "public downloads require clean scan status")
	}
	owner := s.usersByID[share.UserID]
	plan, _ := s.planForUserLocked(owner)
	downloadBytes, err := s.dailyMetricLocked(share.UserID, "share_download")
	if err != nil {
		return AttachmentView{}, nil, err
	}
	if downloadBytes+attachment.Size > plan.DailyShareDownloadBytes {
		return AttachmentView{}, nil, E(http.StatusForbidden, "daily_download_limit", "daily share download traffic exceeds plan limit")
	}
	content, err := s.objectContentLocked(attachment)
	if err != nil {
		return AttachmentView{}, nil, E(http.StatusGone, "attachment_unavailable", "attachment content is unavailable")
	}
	now := s.now().UTC()
	if err := s.recordDailyShareDownloadLocked(share.UserID, attachment.Size); err != nil {
		return AttachmentView{}, nil, err
	}
	share.DownloadCount++
	share.LastDownloadedAt = &now
	attachment.DownloadN++
	if err := s.updateShareLocked(share); err != nil {
		return AttachmentView{}, nil, err
	}
	if err := s.updateAttachmentLocked(attachment); err != nil {
		return AttachmentView{}, nil, err
	}
	return viewAttachment(attachment), content, nil
}

func (s *Service) Quota(userID string) (QuotaView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, err := s.activeUserLocked(userID)
	if err != nil {
		return QuotaView{}, err
	}
	plan, _ := s.planForUserLocked(user)
	return s.quotaLocked(user.ID, plan)
}

func (s *Service) PlanCatalog() plans.Catalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneCatalog(s.catalog)
}

func (s *Service) Prices() struct {
	Plans  []plans.Plan   `json:"plans"`
	Prices []BillingPrice `json:"prices"`
} {
	s.mu.Lock()
	defer s.mu.Unlock()

	prices := make([]BillingPrice, 0, len(s.catalog.Prices))
	for _, price := range s.catalog.Prices {
		prices = append(prices, BillingPrice{
			ID:              price.ID,
			PlanID:          price.PlanID,
			Period:          price.Period,
			AmountCents:     price.AmountCents,
			Currency:        price.Currency,
			Visible:         price.Visible,
			PurchaseEnabled: price.PurchaseEnabled,
			StripeEnabled:   s.cfg.StripeEnabled,
			EpusdtEnabled:   s.cfg.EpusdtEnabled,
		})
	}
	return struct {
		Plans  []plans.Plan   `json:"plans"`
		Prices []BillingPrice `json:"prices"`
	}{
		Plans:  cloneCatalog(s.catalog).Plans,
		Prices: prices,
	}
}

func (s *Service) CreateOrder(userID string, provider string, planID string, period string) (Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.activeUserLocked(userID); err != nil {
		return Order{}, err
	}
	provider = normalizeProvider(provider)
	if provider != "stripe" && provider != "epusdt" {
		return Order{}, E(http.StatusBadRequest, "invalid_provider", "provider must be stripe or epusdt")
	}
	if planID != "plus" && planID != "pro" {
		return Order{}, E(http.StatusBadRequest, "invalid_plan", "paid order requires plus or pro")
	}
	if period == "" {
		period = "monthly"
	}
	price, ok := plans.FindPrice(s.catalog, planID, period)
	if !ok {
		return Order{}, E(http.StatusBadRequest, "invalid_price", "price is not available")
	}
	now := s.now().UTC()
	expiresAt := now.Add(30 * time.Minute)
	orderID := s.newID("ord")
	checkoutURL, address, chain, err := s.paymentDetailsForOrderLocked(provider, orderID, planID, period, price)
	if err != nil {
		return Order{}, err
	}
	order := &Order{
		ID:          orderID,
		UserID:      userID,
		Provider:    provider,
		PlanID:      planID,
		Period:      period,
		AmountCents: price.AmountCents,
		Currency:    price.Currency,
		Status:      "pending",
		CheckoutURL: checkoutURL,
		Address:     address,
		Chain:       chain,
		CreatedAt:   now,
		ExpiresAt:   &expiresAt,
	}
	if err := s.createOrderLocked(order); err != nil {
		return Order{}, err
	}
	if _, err := s.recordWebhookEventLocked(provider, "checkout.created", order.ID, "checkout.created:"+order.ID, map[string]any{"planId": planID, "period": period}); err != nil {
		return Order{}, err
	}
	return *order, nil
}

func (s *Service) paymentDetailsForOrderLocked(provider string, orderID string, planID string, period string, price plans.Price) (string, string, string, error) {
	switch provider {
	case "stripe":
		if s.cfg.AppEnv == "production" && !s.cfg.StripeEnabled {
			return "", "", "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "Stripe is not enabled")
		}
		checkoutURL, err := s.renderPaymentURLLocked(s.cfg.Stripe.CheckoutURLTemplate, provider, orderID, planID, period, price)
		if err != nil {
			return "", "", "", err
		}
		if checkoutURL == "" {
			if s.cfg.AppEnv == "production" {
				return "", "", "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "Stripe checkout URL template is not configured")
			}
			checkoutURL = fmt.Sprintf("%s/dev/checkout/%s?orderId=%s", strings.TrimRight(s.cfg.PublicURL, "/"), planID, url.QueryEscape(orderID))
		}
		return checkoutURL, "", "", nil
	case "epusdt":
		if s.cfg.AppEnv == "production" && !s.cfg.EpusdtEnabled {
			return "", "", "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "Epusdt is not enabled")
		}
		checkoutURL, err := s.renderPaymentURLLocked(s.cfg.Epusdt.CheckoutURLTemplate, provider, orderID, planID, period, price)
		if err != nil {
			return "", "", "", err
		}
		address := strings.TrimSpace(s.cfg.Epusdt.Address)
		chain := strings.TrimSpace(s.cfg.Epusdt.Chain)
		if chain == "" {
			chain = "USDT-TRC20"
		}
		if checkoutURL == "" || address == "" {
			if s.cfg.AppEnv == "production" {
				return "", "", "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "Epusdt checkout URL and payment address are not configured")
			}
			if checkoutURL == "" {
				checkoutURL = fmt.Sprintf("%s/dev/checkout/%s?orderId=%s", strings.TrimRight(s.cfg.PublicURL, "/"), planID, url.QueryEscape(orderID))
			}
			if address == "" {
				address = "TDEVPASTEBOXUSDTTRC20"
			}
		}
		return checkoutURL, address, chain, nil
	default:
		return "", "", "", E(http.StatusBadRequest, "invalid_provider", "provider must be stripe or epusdt")
	}
}

func (s *Service) renderPaymentURLLocked(template string, provider string, orderID string, planID string, period string, price plans.Price) (string, error) {
	template = strings.TrimSpace(template)
	if template == "" {
		return "", nil
	}
	successURL := strings.TrimRight(s.cfg.PublicURL, "/") + "/?view=billing&orderId=" + url.QueryEscape(orderID)
	cancelURL := strings.TrimRight(s.cfg.PublicURL, "/") + "/?view=billing&orderId=" + url.QueryEscape(orderID) + "&status=cancelled"
	replacer := strings.NewReplacer(
		"{order_id}", url.QueryEscape(orderID),
		"{orderId}", url.QueryEscape(orderID),
		"{plan_id}", url.QueryEscape(planID),
		"{planId}", url.QueryEscape(planID),
		"{period}", url.QueryEscape(period),
		"{price_id}", url.QueryEscape(price.ID),
		"{priceId}", url.QueryEscape(price.ID),
		"{amount_cents}", url.QueryEscape(fmt.Sprintf("%d", price.AmountCents)),
		"{amountCents}", url.QueryEscape(fmt.Sprintf("%d", price.AmountCents)),
		"{currency}", url.QueryEscape(price.Currency),
		"{provider}", url.QueryEscape(provider),
		"{success_url}", url.QueryEscape(successURL),
		"{successUrl}", url.QueryEscape(successURL),
		"{cancel_url}", url.QueryEscape(cancelURL),
		"{cancelUrl}", url.QueryEscape(cancelURL),
	)
	rendered := replacer.Replace(template)
	parsed, err := url.Parse(rendered)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "payment checkout URL template rendered an invalid URL")
	}
	if s.cfg.AppEnv == "production" && parsed.Scheme != "https" {
		return "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "production payment checkout URL must use https")
	}
	if s.cfg.AppEnv == "production" && isLocalHost(parsed.Hostname()) {
		return "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "production payment checkout URL must not point to a local host")
	}
	return rendered, nil
}

func (s *Service) MarkOrderPaid(actorID string, orderID string, txID string, reason string) (Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return Order{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Order{}, E(http.StatusBadRequest, "manual_reason_required", "manual payment corrections require a support reason")
	}
	if len(reason) > 500 {
		return Order{}, E(http.StatusBadRequest, "manual_reason_too_long", "manual payment correction reason must be 500 characters or fewer")
	}
	txID = strings.TrimSpace(txID)
	metadata := map[string]any{
		"manual": true,
		"reason": reason,
	}
	return s.markOrderPaidLocked(actorID, orderID, txID, "manual.payment:"+orderID+":"+txID, metadata)
}

func (s *Service) ProcessBillingWebhook(input BillingWebhookInput) (WebhookEvent, *Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	provider := normalizeProvider(input.Provider)
	eventType := strings.TrimSpace(input.EventType)
	if provider == "" || eventType == "" {
		return WebhookEvent{}, nil, E(http.StatusBadRequest, "invalid_webhook", "provider and event type are required")
	}
	orderID := strings.TrimSpace(input.OrderID)
	txID := strings.TrimSpace(input.TxID)
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = provider + ":" + eventType + ":" + orderID + ":" + txID
	}
	if event, ok, err := s.webhookEventByKeyLocked(idempotencyKey); err != nil {
		return WebhookEvent{}, nil, err
	} else if ok {
		var order *Order
		if event.TargetID != "" {
			loaded, err := s.orderByIDLocked(event.TargetID)
			if err != nil && !isAppStatus(err, http.StatusNotFound) {
				return WebhookEvent{}, nil, err
			}
			order = loaded
		}
		return event, order, nil
	}
	metadata := cloneMetadata(input.Metadata)
	if txID != "" {
		metadata["txId"] = txID
	}

	switch eventType {
	case "payment.succeeded", "checkout.session.completed", "invoice.paid", "epusdt.payment.succeeded":
		order, err := s.markOrderPaidLocked("webhook:"+provider, orderID, txID, idempotencyKey, metadata)
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		event, ok, err := s.webhookEventByKeyLocked(idempotencyKey)
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		if !ok {
			return WebhookEvent{}, nil, E(http.StatusInternalServerError, "webhook_event_missing", "webhook event was not recorded")
		}
		return event, &order, nil
	case "payment.failed", "invoice.payment_failed", "epusdt.payment.failed":
		if err := s.applyOrderLifecycleStatusLocked(orderID, "failed", false); err != nil {
			return WebhookEvent{}, nil, err
		}
		event, err := s.recordWebhookEventLocked(provider, eventType, orderID, idempotencyKey, metadata)
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		order, _ := s.orderByIDLocked(orderID)
		return event, order, nil
	case "subscription.deleted", "subscription.canceled", "customer.subscription.deleted", "epusdt.payment.canceled":
		if err := s.applyOrderLifecycleStatusLocked(orderID, "canceled", true); err != nil {
			return WebhookEvent{}, nil, err
		}
		event, err := s.recordWebhookEventLocked(provider, eventType, orderID, idempotencyKey, metadata)
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		order, _ := s.orderByIDLocked(orderID)
		return event, order, nil
	case "refund.created", "charge.refunded":
		if err := s.applyOrderLifecycleStatusLocked(orderID, "refunded", true); err != nil {
			return WebhookEvent{}, nil, err
		}
		event, err := s.recordWebhookEventLocked(provider, eventType, orderID, idempotencyKey, metadata)
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		order, _ := s.orderByIDLocked(orderID)
		return event, order, nil
	case "payment.expired", "checkout.session.expired", "epusdt.payment.expired":
		if err := s.applyOrderLifecycleStatusLocked(orderID, "expired", false); err != nil {
			return WebhookEvent{}, nil, err
		}
		event, err := s.recordWebhookEventLocked(provider, eventType, orderID, idempotencyKey, metadata)
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		order, _ := s.orderByIDLocked(orderID)
		return event, order, nil
	default:
		event, err := s.recordWebhookEventLocked(provider, eventType, orderID, idempotencyKey, metadata)
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		order, _ := s.orderByIDLocked(orderID)
		return event, order, nil
	}
}

func (s *Service) applyOrderLifecycleStatusLocked(orderID string, status string, revokePlan bool) error {
	if strings.TrimSpace(orderID) == "" {
		return nil
	}
	order, err := s.orderByIDLocked(orderID)
	if err != nil {
		if isAppStatus(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return s.applyLoadedOrderLifecycleStatusLocked("webhook:"+order.Provider, order, status, revokePlan, nil)
}

func (s *Service) applyLoadedOrderLifecycleStatusLocked(actorID string, order *Order, status string, revokePlan bool, metadata map[string]any) error {
	if order == nil {
		return nil
	}
	if order.Status == status {
		return nil
	}
	previousStatus := order.Status
	if previousStatus == "paid" && !revokePlan {
		return nil
	}
	planRevoked := false
	if revokePlan && previousStatus == "paid" {
		user, err := s.userByIDLocked(order.UserID)
		if err != nil {
			return err
		}
		if user.PlanID == order.PlanID {
			user.PlanID = "free"
			user.PlanExpiresAt = nil
			if err := s.updateUserLocked(user); err != nil {
				return err
			}
			planRevoked = true
		}
	}
	order.Status = status
	if err := s.updateOrderLocked(order); err != nil {
		return err
	}
	auditMetadata := cloneMetadata(metadata)
	auditMetadata["planId"] = order.PlanID
	auditMetadata["provider"] = order.Provider
	auditMetadata["previousStatus"] = previousStatus
	auditMetadata["planRevoked"] = planRevoked
	return s.auditLocked(actorID, "billing.order_"+status, order.ID, auditMetadata)
}

func (s *Service) ReplayWebhookEvent(actorID string, eventID string) (WebhookEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return WebhookEvent{}, err
	}
	original, err := s.webhookEventByIDLocked(eventID)
	if err != nil {
		return WebhookEvent{}, err
	}
	if original == nil {
		return WebhookEvent{}, E(http.StatusNotFound, "webhook_event_not_found", "webhook event not found")
	}
	metadata := cloneMetadata(original.Metadata)
	metadata["replayedFrom"] = original.ID
	replayKey := original.IdempotencyKey + ":replay:" + s.newID("rpl")
	var event WebhookEvent
	if original.EventType == "payment.succeeded" && original.TargetID != "" {
		if order, err := s.orderByIDLocked(original.TargetID); err != nil && !isAppStatus(err, http.StatusNotFound) {
			return WebhookEvent{}, err
		} else if order != nil && order.Status != "paid" {
			_, err := s.markOrderPaidLocked(actorID, original.TargetID, stringFromMetadata(original.Metadata, "txId"), replayKey, metadata)
			if err != nil {
				return WebhookEvent{}, err
			}
			loaded, ok, err := s.webhookEventByKeyLocked(replayKey)
			if err != nil {
				return WebhookEvent{}, err
			}
			if ok {
				event = loaded
			}
		}
	}
	if event.ID == "" {
		var err error
		event, err = s.recordWebhookEventLocked(original.Provider, "webhook.replayed", original.TargetID, replayKey, metadata)
		if err != nil {
			return WebhookEvent{}, err
		}
	}
	if err := s.auditLocked(actorID, "admin.webhook_replay", original.ID, map[string]any{"replayEventId": event.ID}); err != nil {
		return WebhookEvent{}, err
	}
	return event, nil
}

func (s *Service) markOrderPaidLocked(actorID string, orderID string, txID string, eventKey string, metadata map[string]any) (Order, error) {
	order, err := s.orderByIDLocked(orderID)
	if err != nil {
		return Order{}, err
	}
	if order.Status == "paid" {
		if eventKey != "" {
			metadata = cloneMetadata(metadata)
			if order.TxID != "" {
				metadata["txId"] = order.TxID
			}
			if _, err := s.recordWebhookEventLocked(order.Provider, "payment.succeeded", order.ID, eventKey, metadata); err != nil {
				return Order{}, err
			}
		}
		return *order, nil
	}
	now := s.now().UTC()
	order.Status = "paid"
	order.TxID = strings.TrimSpace(txID)
	order.PaidAt = &now
	user, err := s.userByIDLocked(order.UserID)
	if err != nil {
		return Order{}, err
	}
	user.PlanID = order.PlanID
	days := 30
	if order.Period == "yearly" {
		days = 365
	}
	expires := now.Add(time.Duration(days) * 24 * time.Hour)
	user.PlanExpiresAt = &expires
	if err := s.updateUserLocked(user); err != nil {
		return Order{}, err
	}
	if err := s.updateOrderLocked(order); err != nil {
		return Order{}, err
	}
	auditMetadata := cloneMetadata(metadata)
	auditMetadata["planId"] = order.PlanID
	auditMetadata["provider"] = order.Provider
	if order.TxID != "" {
		auditMetadata["txId"] = order.TxID
	}
	if err := s.auditLocked(actorID, "billing.order_paid", order.ID, auditMetadata); err != nil {
		return Order{}, err
	}
	metadata = cloneMetadata(metadata)
	if order.TxID != "" {
		metadata["txId"] = order.TxID
	}
	if _, err := s.recordWebhookEventLocked(order.Provider, "payment.succeeded", order.ID, eventKey, metadata); err != nil {
		return Order{}, err
	}
	if err := s.mail(user.Email, "PasteBox payment received", "Your membership is active."); err != nil {
		return Order{}, err
	}
	return *order, nil
}

func (s *Service) ListOrders(userID string) ([]Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.activeUserLocked(userID); err != nil {
		return nil, err
	}
	out, err := s.ordersByUserLocked(userID)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) RunBillingReconciliation(actorID string) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	actorID = strings.TrimSpace(actorID)
	if actorID != "" {
		if err := s.requireAdminLocked(actorID); err != nil {
			return nil, err
		}
	}
	if err := s.refreshOrderCachesLocked(context.Background()); err != nil {
		return nil, err
	}
	actor := actorID
	if actor == "" {
		actor = "system:billing_reconcile"
	}
	now := s.now().UTC()
	result := map[string]int{"checkedOrders": 0, "pendingOrders": 0, "expiredOrders": 0}
	for _, order := range s.ordersByID {
		result["checkedOrders"]++
		if order.Status != "pending" {
			continue
		}
		result["pendingOrders"]++
		if order.ExpiresAt == nil || order.ExpiresAt.After(now) {
			continue
		}
		if err := s.applyLoadedOrderLifecycleStatusLocked(actor, order, "expired", false, map[string]any{"source": "billing_reconcile"}); err != nil {
			return nil, err
		}
		result["expiredOrders"]++
	}
	return result, nil
}

func (s *Service) ExportUser(userID string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, err := s.activeUserLocked(userID)
	if err != nil {
		return nil, err
	}
	pastes, _ := s.ListPastesLocked(userID, ListOptions{})
	shares := []ShareView{}
	for _, share := range s.sharesByID {
		if share.UserID == userID {
			shares = append(shares, s.viewShareLocked(share))
		}
	}
	sort.Slice(shares, func(i, j int) bool { return shares[i].CreatedAt.After(shares[j].CreatedAt) })
	orders, err := s.ordersByUserLocked(userID)
	if err != nil {
		return nil, err
	}
	sort.Slice(orders, func(i, j int) bool { return orders[i].CreatedAt.After(orders[j].CreatedAt) })
	reports := s.reportsByUserLocked(userID)
	webhookEvents := s.webhookEventsForOrdersLocked(orders)
	exportedAt := s.now().UTC()
	if err := s.auditLocked(userID, "account.export", userID, map[string]any{
		"pasteCount":        len(pastes),
		"shareCount":        len(shares),
		"orderCount":        len(orders),
		"reportCount":       len(reports),
		"webhookEventCount": len(webhookEvents),
	}); err != nil {
		return nil, err
	}
	auditLogs, err := s.auditLogsForExportLocked(userID, pastes, shares, orders, reports, webhookEvents)
	if err != nil {
		return nil, err
	}
	userView, err := s.viewUserLocked(user)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"user":          userView,
		"pastes":        pastes,
		"shares":        shares,
		"orders":        orders,
		"reports":       reports,
		"webhookEvents": webhookEvents,
		"auditLogs":     auditLogs,
		"exportedAt":    exportedAt,
	}, nil
}

func (s *Service) reportsByUserLocked(userID string) []Report {
	reports := []Report{}
	for _, report := range s.reports {
		if report.UserID == userID {
			reports = append(reports, *report)
		}
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].CreatedAt.After(reports[j].CreatedAt) })
	return reports
}

func (s *Service) webhookEventsForOrdersLocked(orders []Order) []WebhookEvent {
	orderIDs := map[string]struct{}{}
	for _, order := range orders {
		orderIDs[order.ID] = struct{}{}
	}
	events := []WebhookEvent{}
	for _, event := range s.webhookEvents {
		if _, ok := orderIDs[event.TargetID]; ok {
			events = append(events, cloneWebhookEvent(*event))
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].ReceivedAt.After(events[j].ReceivedAt) })
	return events
}

func (s *Service) auditLogsForExportLocked(userID string, pastes []PasteView, shares []ShareView, orders []Order, reports []Report, webhookEvents []WebhookEvent) ([]AuditLog, error) {
	targets := exportAuditTargets(userID, pastes, shares, orders, reports, webhookEvents)
	if s.audit != nil {
		return s.audit.AuditLogsForActorOrTargets(context.Background(), userID, targets, 1000)
	}
	targetSet := map[string]struct{}{}
	for _, target := range targets {
		targetSet[target] = struct{}{}
	}
	logs := []AuditLog{}
	for _, log := range s.auditLogs {
		if log.ActorID == userID {
			logs = append(logs, cloneAuditLog(*log))
			continue
		}
		if _, ok := targetSet[log.Target]; ok {
			logs = append(logs, cloneAuditLog(*log))
		}
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].CreatedAt.After(logs[j].CreatedAt) })
	if len(logs) > 1000 {
		return logs[:1000], nil
	}
	return logs, nil
}

func exportAuditTargets(userID string, pastes []PasteView, shares []ShareView, orders []Order, reports []Report, webhookEvents []WebhookEvent) []string {
	targets := map[string]struct{}{userID: {}}
	for _, paste := range pastes {
		targets[paste.ID] = struct{}{}
		for _, attachment := range paste.Attachments {
			targets[attachment.ID] = struct{}{}
		}
	}
	for _, share := range shares {
		targets[share.ID] = struct{}{}
	}
	for _, order := range orders {
		targets[order.ID] = struct{}{}
	}
	for _, report := range reports {
		targets[report.ID] = struct{}{}
	}
	for _, event := range webhookEvents {
		targets[event.ID] = struct{}{}
	}
	out := make([]string, 0, len(targets))
	for target := range targets {
		if strings.TrimSpace(target) != "" {
			out = append(out, target)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Service) Report(userID string, target string, reason string) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if userID != "" {
		if _, err := s.activeUserLocked(userID); err != nil {
			return Report{}, err
		}
	}
	report := &Report{ID: s.newID("rpt"), UserID: userID, Target: target, Reason: strings.TrimSpace(reason), Status: "open", CreatedAt: s.now().UTC()}
	if err := s.createReportLocked(report); err != nil {
		return Report{}, err
	}
	actorID := userID
	if actorID == "" {
		actorID = "anonymous"
	}
	if err := s.auditLocked(actorID, "support.report_created", report.ID, map[string]any{
		"reportedTarget": report.Target,
		"anonymous":      userID == "",
	}); err != nil {
		return Report{}, err
	}
	return *report, nil
}

func (s *Service) AdminResolveReport(actorID string, reportID string, status string) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return Report{}, err
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "resolved"
	}
	if status != "open" && status != "resolved" && status != "dismissed" {
		return Report{}, E(http.StatusBadRequest, "invalid_report_status", "report status must be open, resolved, or dismissed")
	}
	report, err := s.updateReportStatusLocked(reportID, status)
	if err != nil {
		return Report{}, err
	}
	if err := s.auditLocked(actorID, "admin.report_status", report.ID, map[string]any{"status": status}); err != nil {
		return Report{}, err
	}
	return *report, nil
}

func (s *Service) AdminDashboard(actorID string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return nil, err
	}
	metrics, err := s.operationalMetricsLocked(context.Background())
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"users":                 metrics.UserCount,
		"activePastes":          metrics.ActivePastes,
		"activeStorageBytes":    metrics.ActiveStorageBytes,
		"reportsOpen":           metrics.ReportsOpen,
		"cleanupQueueDepth":     metrics.CleanupQueueDepth,
		"scanQueueDepth":        metrics.ScanQueueDepth,
		"scanFailureQueueDepth": metrics.ScanFailureDepth,
		"failedJobQueueDepth":   metrics.FailedJobDepth,
		"orders":                len(s.ordersByID),
		"webhookEvents":         metrics.WebhookEvents,
	}, nil
}

func (s *Service) OperationalMetrics() (OperationalMetrics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.operationalMetricsLocked(context.Background())
}

func (s *Service) operationalMetricsLocked(ctx context.Context) (OperationalMetrics, error) {
	if err := s.refreshQueueCachesLocked(ctx); err != nil {
		return OperationalMetrics{}, err
	}
	if err := s.refreshMailCacheLocked(ctx); err != nil {
		return OperationalMetrics{}, err
	}
	users, err := s.listUsersLocked()
	if err != nil {
		return OperationalMetrics{}, err
	}
	var activePastes int
	var storage int64
	for _, paste := range s.pastesByID {
		if s.isPasteVisibleLocked(paste) {
			activePastes++
			storage += s.pasteSizeLocked(paste)
		}
	}
	ordersByStatus := map[string]int{}
	for _, order := range s.ordersByID {
		ordersByStatus[order.Status]++
	}
	failedMails, err := s.mailQueueItemsLocked(ctx, "failed", 1000)
	if err != nil {
		return OperationalMetrics{}, err
	}
	return OperationalMetrics{
		UserCount:           len(users),
		ActivePastes:        activePastes,
		ActiveStorageBytes:  storage,
		ReportsOpen:         countReports(s.reports, "open"),
		CleanupQueueDepth:   len(s.cleanupJobs),
		CleanupFailureDepth: len(s.cleanupFailures),
		ScanQueueDepth:      len(s.scanJobs),
		ScanFailureDepth:    len(s.scanFailures),
		FailedJobDepth:      len(s.failedJobs),
		MailQueueDepth:      len(s.mails),
		MailFailedDepth:     len(failedMails),
		WebhookEvents:       len(s.webhookEvents),
		OrdersByStatus:      ordersByStatus,
	}, nil
}

func (s *Service) AdminUsers(actorID string) ([]UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return nil, err
	}
	users, err := s.listUsersLocked()
	if err != nil {
		return nil, err
	}
	out := make([]UserView, 0, len(users))
	for _, user := range users {
		view, err := s.viewUserLocked(&user)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) AdminSetUserPlan(actorID string, userID string, planID string, expiresAt *time.Time, reason string, ticketID string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return UserView{}, err
	}
	user, err := s.userByIDLocked(userID)
	if err != nil {
		return UserView{}, err
	}
	if _, ok := plans.Find(s.catalog, planID); !ok {
		return UserView{}, E(http.StatusBadRequest, "invalid_plan", "plan does not exist")
	}
	reason = strings.TrimSpace(reason)
	ticketID = strings.TrimSpace(ticketID)
	if reason == "" && ticketID == "" {
		return UserView{}, E(http.StatusBadRequest, "admin_plan_reason_required", "plan changes require a support reason or ticket id")
	}
	oldPlanID := user.PlanID
	oldExpiresAt := user.PlanExpiresAt
	user.PlanID = planID
	user.PlanExpiresAt = expiresAt
	user.UpdatedAt = s.now().UTC()
	if err := s.updateUserLocked(user); err != nil {
		return UserView{}, err
	}
	metadata := map[string]any{
		"oldPlanId":    oldPlanID,
		"newPlanId":    planID,
		"oldExpiresAt": oldExpiresAt,
		"newExpiresAt": expiresAt,
	}
	if reason != "" {
		metadata["reason"] = reason
	}
	if ticketID != "" {
		metadata["ticketId"] = ticketID
	}
	if err := s.auditLocked(actorID, "admin.user_plan_set", userID, metadata); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(user)
}

func (s *Service) AdminFreezeUser(actorID string, userID string, frozen bool) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return UserView{}, err
	}
	user, err := s.userByIDLocked(userID)
	if err != nil {
		return UserView{}, err
	}
	user.Frozen = frozen
	user.UpdatedAt = s.now().UTC()
	if err := s.updateUserLocked(user); err != nil {
		return UserView{}, err
	}
	if err := s.auditLocked(actorID, "admin.user_freeze", userID, map[string]any{"frozen": frozen}); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(user)
}

func (s *Service) AdminPastes(actorID string) ([]PasteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return nil, err
	}
	out := []PasteView{}
	for _, paste := range s.pastesByID {
		out = append(out, s.viewPasteLocked(paste))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) AdminAttachments(actorID string, query string) ([]AdminAttachmentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	out := []AdminAttachmentView{}
	for _, attachment := range s.attachmentsByID {
		paste := s.pastesByID[attachment.PasteID]
		view := AdminAttachmentView{AttachmentView: viewAttachment(attachment), UserID: attachment.UserID}
		if paste != nil {
			view.PasteTitle = paste.Title
		}
		haystack := strings.ToLower(attachment.UserID + "\n" + attachment.FileName + "\n" + attachment.SHA256 + "\n" + attachment.Status + "\n" + attachment.ScanStatus)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) AdminShares(actorID string) ([]AdminShareView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return nil, err
	}
	out := []AdminShareView{}
	for _, share := range s.sharesByID {
		out = append(out, AdminShareView{ShareView: s.viewShareLocked(share), UserID: share.UserID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) AdminOrders(actorID string) ([]Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return nil, err
	}
	out := make([]Order, 0, len(s.ordersByID))
	for _, order := range s.ordersByID {
		out = append(out, *order)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) AdminWebhookEvents(actorID string) ([]WebhookEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return nil, err
	}
	out := make([]WebhookEvent, 0, len(s.webhookEvents))
	for _, event := range s.webhookEvents {
		out = append(out, *event)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReceivedAt.After(out[j].ReceivedAt) })
	return out, nil
}

func (s *Service) AdminTakedownPaste(actorID string, pasteID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return err
	}
	paste, err := s.pasteByIDLocked(pasteID)
	if err != nil {
		return err
	}
	paste.Status = "taken_down"
	paste.UpdatedAt = s.now().UTC()
	if err := s.updatePasteLocked(paste); err != nil {
		return err
	}
	if err := s.auditLocked(actorID, "admin.paste_takedown", pasteID, nil); err != nil {
		return err
	}
	return nil
}

func (s *Service) AdminFreezeAttachment(actorID string, attachmentID string, frozen bool) (AttachmentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return AttachmentView{}, err
	}
	attachment, err := s.attachmentByIDLocked(attachmentID)
	if err != nil {
		return AttachmentView{}, err
	}
	if frozen {
		attachment.Status = "frozen"
		attachment.Risk = defaultString(attachment.Risk, "admin_frozen")
	} else {
		attachment.Status = "active"
		if attachment.Risk == "admin_frozen" {
			attachment.Risk = ""
		}
	}
	if err := s.updateAttachmentLocked(attachment); err != nil {
		return AttachmentView{}, err
	}
	if paste := s.pastesByID[attachment.PasteID]; paste != nil {
		paste.ScanStatus = aggregateScanStatus(s.attachmentsForPasteLocked(paste))
		paste.UpdatedAt = s.now().UTC()
		if err := s.updatePasteLocked(paste); err != nil {
			return AttachmentView{}, err
		}
	}
	if err := s.auditLocked(actorID, "admin.attachment_freeze", attachmentID, map[string]any{"frozen": frozen}); err != nil {
		return AttachmentView{}, err
	}
	return viewAttachment(attachment), nil
}

func (s *Service) AdminRetryScan(actorID string, attachmentID string) (AttachmentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return AttachmentView{}, err
	}
	attachment, err := s.attachmentByIDLocked(attachmentID)
	if err != nil {
		return AttachmentView{}, err
	}
	if attachment.ScanStatus == "malicious" {
		return AttachmentView{}, E(http.StatusForbidden, "malicious_file", "malicious files cannot be auto-retried")
	}
	now := s.now().UTC()
	attachment.ScanStatus = "pending"
	attachment.Risk = classifyAttachmentRisk(attachment.FileName, attachment.ContentType)
	if err := s.updateAttachmentLocked(attachment); err != nil {
		return AttachmentView{}, err
	}
	if err := s.deleteQueueItemsByKindTargetLocked(&s.scanFailures, "scan_failed", attachment.ID); err != nil {
		return AttachmentView{}, err
	}
	if err := s.deleteQueueItemsByKindTargetLocked(&s.scanJobs, "scan", attachment.ID); err != nil {
		return AttachmentView{}, err
	}
	if err := s.scheduleScanJobLocked(attachment.ID, now); err != nil {
		return AttachmentView{}, err
	}
	if paste := s.pastesByID[attachment.PasteID]; paste != nil {
		paste.ScanStatus = aggregateScanStatus(s.attachmentsForPasteLocked(paste))
		paste.UpdatedAt = now
		if err := s.updatePasteLocked(paste); err != nil {
			return AttachmentView{}, err
		}
	}
	if err := s.auditLocked(actorID, "admin.scan_retry", attachmentID, nil); err != nil {
		return AttachmentView{}, err
	}
	return viewAttachment(attachment), nil
}

func (s *Service) RunAttachmentScan(scanner Scanner, attachmentID string) error {
	if scanner == nil {
		return errors.New("scanner is required")
	}

	s.mu.Lock()
	attachment, err := s.attachmentByIDLocked(attachmentID)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if attachment.Status != "active" || attachment.ScanStatus == "malicious" {
		s.mu.Unlock()
		return nil
	}
	content, err := s.objectContentLocked(attachment)
	fileName := attachment.FileName
	contentType := attachment.ContentType
	objectKey := attachment.ObjectKey
	s.mu.Unlock()
	if err != nil {
		return err
	}

	result, scanErr := scanner.Scan(context.Background(), fileName, contentType, content)

	s.mu.Lock()
	defer s.mu.Unlock()
	attachment, err = s.attachmentByIDLocked(attachmentID)
	if err != nil {
		return err
	}
	if attachment.Status != "active" || attachment.ScanStatus == "malicious" || attachment.ObjectKey != objectKey {
		return nil
	}
	if scanErr != nil {
		if err := s.applyAttachmentScanResultLocked(attachment, ScanResult{Status: "scan_failed", Risk: "scanner_unavailable"}); err != nil {
			return err
		}
		return scanErr
	}
	switch result.Status {
	case "clean", "malicious", "scan_failed":
		return s.applyAttachmentScanResultLocked(attachment, result)
	default:
		if err := s.applyAttachmentScanResultLocked(attachment, ScanResult{Status: "scan_failed", Risk: "invalid_scanner_verdict"}); err != nil {
			return err
		}
		return fmt.Errorf("invalid scanner verdict %q", result.Status)
	}
}

func (s *Service) applyAttachmentScanResultLocked(attachment *Attachment, result ScanResult) error {
	now := s.now().UTC()
	attachment.ScanStatus = result.Status
	attachment.Risk = result.Risk
	if attachment.ScanStatus == "malicious" && attachment.Risk == "" {
		attachment.Risk = "malware_detected"
	}
	if attachment.ScanStatus == "clean" && attachment.Risk == "" {
		attachment.Risk = classifyAttachmentRisk(attachment.FileName, attachment.ContentType)
	}
	if err := s.updateAttachmentLocked(attachment); err != nil {
		return err
	}
	paste, err := s.pasteByIDLocked(attachment.PasteID)
	if err != nil {
		return err
	}
	paste.ScanStatus = aggregateScanStatus(s.attachmentsForPasteLocked(paste))
	paste.UpdatedAt = now
	if err := s.updatePasteLocked(paste); err != nil {
		return err
	}
	if attachment.ScanStatus == "scan_failed" {
		return s.markScanFailureLocked(attachment.ID, defaultString(attachment.Risk, "scan_failed"), now)
	}
	if attachment.ScanStatus == "clean" {
		if err := s.deleteQueueItemsByKindTargetLocked(&s.scanFailures, "scan_failed", attachment.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) AdminRevokeShare(actorID string, shareID string) (ShareView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return ShareView{}, err
	}
	share, err := s.shareByIDLocked(shareID)
	if err != nil {
		return ShareView{}, err
	}
	now := s.now().UTC()
	share.RevokedAt = &now
	if err := s.updateShareLocked(share); err != nil {
		return ShareView{}, err
	}
	if err := s.auditLocked(actorID, "admin.share_revoke", shareID, nil); err != nil {
		return ShareView{}, err
	}
	return s.viewShareLocked(share), nil
}

func (s *Service) AdminAuditLogs(actorID string) ([]AuditLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return nil, err
	}
	if s.audit != nil {
		return s.audit.AuditLogs(context.Background(), 100)
	}
	out := make([]AuditLog, 0, len(s.auditLogs))
	for _, log := range s.auditLogs {
		out = append(out, *log)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) AdminQueues(actorID string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err := s.refreshQueueCachesLocked(ctx); err != nil {
		return nil, err
	}
	queuedMails, err := s.mailQueueItemsLocked(ctx, "queued", 100)
	if err != nil {
		return nil, err
	}
	failedMails, err := s.mailQueueItemsLocked(ctx, "failed", 100)
	if err != nil {
		return nil, err
	}
	cleanupFailures := s.cleanupFailures
	if cleanupFailures == nil {
		cleanupFailures = []*QueueItem{}
	}
	cleanupJobs := s.cleanupJobs
	if cleanupJobs == nil {
		cleanupJobs = []*QueueItem{}
	}
	scanJobs := s.scanJobs
	if scanJobs == nil {
		scanJobs = []*QueueItem{}
	}
	scanFailures := s.scanFailures
	if scanFailures == nil {
		scanFailures = []*QueueItem{}
	}
	failedJobs := s.failedJobs
	if failedJobs == nil {
		failedJobs = []*QueueItem{}
	}
	reports := s.reports
	if reports == nil {
		reports = []*Report{}
	}
	return map[string]any{
		"cleanupJobs":     cleanupJobs,
		"cleanupFailures": cleanupFailures,
		"scanJobs":        scanJobs,
		"scanFailures":    scanFailures,
		"failedJobs":      failedJobs,
		"queuedMails":     queuedMails,
		"failedMails":     failedMails,
		"reports":         reports,
	}, nil
}

func (s *Service) RunCleanup(actorID string) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actorID != "" {
		if err := s.requireAdminLocked(actorID); err != nil {
			return nil, err
		}
	}
	if err := s.refreshContentCachesLocked(context.Background()); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	expired := 0
	deletedAttachments := 0
	deletedPastes := 0
	for _, paste := range s.pastesByID {
		if paste.Status == "active" && !paste.ExpiresAt.After(now) {
			paste.Status = "pending_delete"
			paste.UpdatedAt = now
			expired++
			if err := s.updatePasteLocked(paste); err != nil {
				return nil, err
			}
			for _, id := range paste.AttachmentIDs {
				if att := s.attachmentsByID[id]; att != nil {
					att.Status = "pending_delete"
					if err := s.updateAttachmentLocked(att); err != nil {
						return nil, err
					}
				}
			}
		}
		if paste.Status == "pending_delete" {
			allDeleted := true
			for _, id := range paste.AttachmentIDs {
				att := s.attachmentsByID[id]
				if att == nil {
					continue
				}
				if att.Status == "pending_delete" {
					previousAttachment := *att
					previousAttachment.Content = append([]byte(nil), att.Content...)
					previousRefs := s.objectRefs[att.ObjectKey]
					att.Status = "deleted"
					att.Content = nil
					if previousRefs <= 1 {
						if err := s.updateAttachmentLocked(att); err != nil {
							s.cacheAttachmentLocked(previousAttachment)
							return nil, err
						}
						if err := s.decrementObjectRefLocked(att); err != nil {
							restoreErr := s.updateAttachmentLocked(&previousAttachment)
							if restoreErr != nil {
								return nil, errors.Join(err, restoreErr)
							}
							return nil, err
						}
					} else {
						if err := s.decrementObjectRefLocked(att); err != nil {
							_ = s.restoreObjectRefAfterCleanupFailureLocked(&previousAttachment, previousRefs, now)
							s.cacheAttachmentLocked(previousAttachment)
							return nil, err
						}
						if err := s.updateAttachmentLocked(att); err != nil {
							restoreErr := s.restoreObjectRefAfterCleanupFailureLocked(&previousAttachment, previousRefs, now)
							s.cacheAttachmentLocked(previousAttachment)
							if restoreErr != nil {
								return nil, errors.Join(err, restoreErr)
							}
							return nil, err
						}
					}
					deletedAttachments++
				}
				if att.Status != "deleted" {
					allDeleted = false
				}
			}
			if allDeleted {
				paste.Status = "deleted"
				paste.UpdatedAt = now
				if err := s.updatePasteLocked(paste); err != nil {
					return nil, err
				}
				deletedPastes++
			}
		}
	}
	return map[string]int{"expired": expired, "deletedAttachments": deletedAttachments, "deletedPastes": deletedPastes}, nil
}

func (s *Service) SeedAdmin(email string, password string) (UserView, error) {
	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return UserView{}, E(http.StatusBadRequest, "invalid_email", "valid email is required")
	}
	if len(password) < 8 {
		return UserView{}, E(http.StatusBadRequest, "weak_password", "password must be at least 8 characters")
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return UserView{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	user, err := s.userByEmailLocked(email)
	if err != nil && !isStoreNotFound(err) && !isAppStatus(err, http.StatusNotFound) {
		return UserView{}, err
	}
	if user == nil {
		user = &User{
			ID:          s.newID("usr"),
			Email:       email,
			DisplayName: "PasteBox Admin",
			Language:    "en",
			PlanID:      "free",
			CreatedAt:   now,
		}
	}
	if strings.TrimSpace(user.DisplayName) == "" {
		user.DisplayName = "PasteBox Admin"
	}
	if strings.TrimSpace(user.Language) == "" {
		user.Language = "en"
	}
	if strings.TrimSpace(user.PlanID) == "" {
		user.PlanID = "free"
	}
	user.PasswordHash = passwordHash
	user.Role = "admin"
	user.EmailVerified = true
	user.Frozen = false
	user.UpdatedAt = now
	user.DeleteRequestedAt = nil
	user.DeleteScheduledAt = nil
	user.DeletedAt = nil

	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	if _, exists := s.usersByID[user.ID]; !exists {
		if err := s.createUserLocked(user); err != nil {
			if errors.Is(err, ErrStoreConflict) {
				return UserView{}, E(http.StatusConflict, "email_exists", "email is already registered")
			}
			return UserView{}, err
		}
	} else if err := s.updateUserLocked(user); err != nil {
		return UserView{}, err
	}
	if err := s.deleteLoginFailureLocked(email); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(user)
}

func (s *Service) ListPastesLocked(userID string, opts ListOptions) ([]PasteView, error) {
	out := []PasteView{}
	query := strings.ToLower(strings.TrimSpace(opts.Query))
	filter := strings.TrimSpace(opts.Filter)
	tag := strings.ToLower(strings.TrimSpace(opts.Tag))
	for _, paste := range s.pastesByID {
		if paste.UserID != userID || !s.isPasteVisibleLocked(paste) {
			continue
		}
		view := s.viewPasteLocked(paste)
		if !matchesPaste(view, query, filter, tag) {
			continue
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) loadContentCaches(ctx context.Context) error {
	return s.refreshContentCachesLocked(ctx)
}

func (s *Service) refreshContentCachesLocked(ctx context.Context) error {
	if s.content.Pastes != nil {
		pastes, err := s.content.Pastes.ListPastes(ctx)
		if err != nil {
			return fmt.Errorf("load pastes: %w", err)
		}
		s.pastesByID = map[string]*Paste{}
		for _, paste := range pastes {
			s.cachePasteLocked(paste)
		}
	}
	if s.content.Attachments != nil {
		attachments, err := s.content.Attachments.ListAttachments(ctx)
		if err != nil {
			return fmt.Errorf("load attachments: %w", err)
		}
		s.attachmentsByID = map[string]*Attachment{}
		for _, paste := range s.pastesByID {
			paste.AttachmentIDs = nil
		}
		for _, attachment := range attachments {
			s.cacheAttachmentLocked(attachment)
		}
		if err := s.rebuildObjectRefsLocked(); err != nil {
			return fmt.Errorf("rebuild object refs: %w", err)
		}
	}
	if s.content.Shares != nil {
		shares, err := s.content.Shares.ListShares(ctx)
		if err != nil {
			return fmt.Errorf("load shares: %w", err)
		}
		s.sharesByID = map[string]*Share{}
		s.shareIDByToken = map[string]string{}
		for _, share := range shares {
			s.cacheShareLocked(share)
		}
	}
	return nil
}

func (s *Service) loadOperationalCaches(ctx context.Context) error {
	if err := s.refreshOrderCachesLocked(ctx); err != nil {
		return err
	}
	if s.ops.WebhookEvents != nil {
		events, err := s.ops.WebhookEvents.ListWebhookEvents(ctx)
		if err != nil {
			return fmt.Errorf("load webhook events: %w", err)
		}
		for _, event := range events {
			s.cacheWebhookEventLocked(event)
		}
	}
	if s.ops.Reports != nil {
		reports, err := s.ops.Reports.ListReports(ctx)
		if err != nil {
			return fmt.Errorf("load reports: %w", err)
		}
		for _, report := range reports {
			s.cacheReportLocked(report)
		}
	}
	if s.ops.Queues != nil {
		if err := s.refreshQueueCachesLocked(ctx); err != nil {
			return err
		}
	}
	if s.ops.Mails != nil {
		return s.refreshMailCacheLocked(ctx)
	}
	return nil
}

func (s *Service) refreshOrderCachesLocked(ctx context.Context) error {
	if s.ops.Orders == nil {
		return nil
	}
	orders, err := s.ops.Orders.ListOrders(ctx)
	if err != nil {
		return fmt.Errorf("load orders: %w", err)
	}
	s.ordersByID = map[string]*Order{}
	for _, order := range orders {
		s.cacheOrderLocked(order)
	}
	return nil
}

func (s *Service) createPasteLocked(paste *Paste) error {
	if s.content.Pastes != nil {
		if err := s.content.Pastes.CreatePaste(context.Background(), *paste); err != nil {
			return err
		}
	}
	s.cachePasteLocked(*paste)
	return nil
}

func (s *Service) updatePasteLocked(paste *Paste) error {
	if s.content.Pastes != nil {
		if err := s.content.Pastes.UpdatePaste(context.Background(), *paste); err != nil {
			return err
		}
	}
	s.cachePasteLocked(*paste)
	return nil
}

func (s *Service) pasteByIDLocked(id string) (*Paste, error) {
	if s.content.Pastes != nil {
		loaded, err := s.content.Pastes.PasteByID(context.Background(), id)
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "paste_not_found", "paste not found")
			}
			return nil, err
		}
		paste := s.cachePasteLocked(loaded)
		if s.content.Attachments != nil {
			attachments, err := s.content.Attachments.ListAttachmentsByPaste(context.Background(), paste.ID)
			if err != nil {
				return nil, err
			}
			paste.AttachmentIDs = paste.AttachmentIDs[:0]
			for _, attachment := range attachments {
				s.cacheAttachmentLocked(attachment)
			}
		}
		return paste, nil
	}
	paste := s.pastesByID[id]
	if paste == nil {
		return nil, E(http.StatusNotFound, "paste_not_found", "paste not found")
	}
	return paste, nil
}

func (s *Service) cachePasteLocked(paste Paste) *Paste {
	cached := paste
	cached.AttachmentIDs = append([]string(nil), paste.AttachmentIDs...)
	s.pastesByID[cached.ID] = &cached
	return &cached
}

func (s *Service) createAttachmentLocked(attachment *Attachment) error {
	if s.content.Attachments != nil {
		if err := s.content.Attachments.CreateAttachment(context.Background(), *attachment); err != nil {
			return err
		}
	}
	s.cacheAttachmentLocked(*attachment)
	return nil
}

func (s *Service) updateAttachmentLocked(attachment *Attachment) error {
	if s.content.Attachments != nil {
		if err := s.content.Attachments.UpdateAttachment(context.Background(), *attachment); err != nil {
			return err
		}
	}
	s.cacheAttachmentLocked(*attachment)
	return nil
}

func (s *Service) deleteAttachmentLocked(attachment *Attachment) error {
	if s.content.Attachments != nil {
		if err := s.content.Attachments.DeleteAttachment(context.Background(), attachment.ID); err != nil && !isStoreNotFound(err) {
			return err
		}
	}
	delete(s.attachmentsByID, attachment.ID)
	if paste := s.pastesByID[attachment.PasteID]; paste != nil {
		paste.AttachmentIDs = removeString(paste.AttachmentIDs, attachment.ID)
	}
	return nil
}

func (s *Service) attachmentByIDLocked(id string) (*Attachment, error) {
	if s.content.Attachments != nil {
		loaded, err := s.content.Attachments.AttachmentByID(context.Background(), id)
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "attachment_not_found", "attachment not found")
			}
			return nil, err
		}
		return s.cacheAttachmentLocked(loaded), nil
	}
	attachment := s.attachmentsByID[id]
	if attachment == nil {
		return nil, E(http.StatusNotFound, "attachment_not_found", "attachment not found")
	}
	return attachment, nil
}

func (s *Service) cacheAttachmentLocked(attachment Attachment) *Attachment {
	cached := attachment
	cached.Content = append([]byte(nil), attachment.Content...)
	s.attachmentsByID[cached.ID] = &cached
	if paste := s.pastesByID[cached.PasteID]; paste != nil && !contains(paste.AttachmentIDs, cached.ID) {
		paste.AttachmentIDs = append(paste.AttachmentIDs, cached.ID)
	}
	return &cached
}

func (s *Service) rebuildObjectRefsLocked() error {
	s.objectRefs = map[string]int{}
	for _, attachment := range s.attachmentsByID {
		if attachment.ObjectKey == "" || attachment.Status == "deleted" {
			continue
		}
		s.objectRefs[attachment.ObjectKey]++
	}
	if s.content.ObjectRefs == nil {
		return nil
	}
	now := s.now().UTC()
	for objectKey, refs := range s.objectRefs {
		if refs <= 0 {
			continue
		}
		attachment := s.attachmentForObjectKeyLocked(objectKey)
		if attachment == nil {
			continue
		}
		if err := s.persistObjectRefLocked(attachment, refs, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) incrementObjectRefLocked(attachment *Attachment, previousRefs int, now time.Time) error {
	if attachment.ObjectKey == "" {
		return nil
	}
	nextRefs := previousRefs + 1
	if err := s.persistObjectRefLocked(attachment, nextRefs, now); err != nil {
		return err
	}
	s.objectRefs[attachment.ObjectKey] = nextRefs
	return nil
}

func (s *Service) decrementObjectRefLocked(attachment *Attachment) error {
	if attachment.ObjectKey == "" {
		return nil
	}
	currentRefs := s.objectRefs[attachment.ObjectKey]
	nextRefs := currentRefs - 1
	if nextRefs > 0 {
		if err := s.persistObjectRefLocked(attachment, nextRefs, s.now().UTC()); err != nil {
			return err
		}
		s.objectRefs[attachment.ObjectKey] = nextRefs
		return nil
	}
	if s.content.ObjectRefs != nil {
		if err := s.deleteObjectLocked(attachment.ObjectKey); err != nil {
			return err
		}
		if err := s.content.ObjectRefs.DeleteObjectRef(context.Background(), attachment.ObjectKey); err != nil && !isStoreNotFound(err) {
			return err
		}
	} else {
		if err := s.deleteObjectLocked(attachment.ObjectKey); err != nil {
			return err
		}
	}
	delete(s.objectRefs, attachment.ObjectKey)
	return nil
}

func (s *Service) persistObjectRefLocked(attachment *Attachment, refs int, now time.Time) error {
	if s.content.ObjectRefs == nil || attachment.ObjectKey == "" {
		return nil
	}
	if refs <= 0 {
		if err := s.content.ObjectRefs.DeleteObjectRef(context.Background(), attachment.ObjectKey); err != nil && !isStoreNotFound(err) {
			return err
		}
		return nil
	}
	createdAt := attachment.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	return s.content.ObjectRefs.UpsertObjectRef(context.Background(), ObjectRef{
		ObjectKey: attachment.ObjectKey,
		RefCount:  refs,
		Size:      attachment.Size,
		SHA256:    attachment.SHA256,
		CreatedAt: createdAt,
		UpdatedAt: now,
	})
}

func (s *Service) attachmentForObjectKeyLocked(objectKey string) *Attachment {
	for _, attachment := range s.attachmentsByID {
		if attachment.ObjectKey == objectKey && attachment.Status != "deleted" {
			return attachment
		}
	}
	return nil
}

func (s *Service) restoreObjectRefAfterCleanupFailureLocked(attachment *Attachment, refs int, now time.Time) error {
	if attachment.ObjectKey == "" {
		return nil
	}
	if refs <= 0 {
		delete(s.objectRefs, attachment.ObjectKey)
		return nil
	}
	s.objectRefs[attachment.ObjectKey] = refs
	return s.persistObjectRefLocked(attachment, refs, now)
}

func (s *Service) createShareLocked(share *Share) error {
	if s.content.Shares != nil {
		if err := s.content.Shares.CreateShare(context.Background(), *share); err != nil {
			if errors.Is(err, ErrStoreConflict) {
				return E(http.StatusConflict, "share_token_conflict", "share token already exists")
			}
			return err
		}
	}
	s.cacheShareLocked(*share)
	return nil
}

func (s *Service) updateShareLocked(share *Share) error {
	if s.content.Shares != nil {
		if err := s.content.Shares.UpdateShare(context.Background(), *share); err != nil {
			return err
		}
	}
	s.cacheShareLocked(*share)
	return nil
}

func (s *Service) shareByIDLocked(id string) (*Share, error) {
	if s.content.Shares != nil {
		loaded, err := s.content.Shares.ShareByID(context.Background(), id)
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "share_not_found", "share not found")
			}
			return nil, err
		}
		return s.cacheShareLocked(loaded), nil
	}
	share := s.sharesByID[id]
	if share == nil {
		return nil, E(http.StatusNotFound, "share_not_found", "share not found")
	}
	return share, nil
}

func (s *Service) shareByTokenHashLocked(tokenHash string) (*Share, error) {
	if s.content.Shares != nil {
		loaded, err := s.content.Shares.ShareByTokenHash(context.Background(), tokenHash)
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "share_not_found", "share not found")
			}
			return nil, err
		}
		return s.cacheShareLocked(loaded), nil
	}
	shareID := s.shareIDByToken[tokenHash]
	share := s.sharesByID[shareID]
	if share == nil {
		return nil, E(http.StatusNotFound, "share_not_found", "share not found")
	}
	return share, nil
}

func (s *Service) cacheShareLocked(share Share) *Share {
	cached := share
	s.sharesByID[cached.ID] = &cached
	s.shareIDByToken[cached.TokenHash] = cached.ID
	return &cached
}

func (s *Service) createOrderLocked(order *Order) error {
	if s.ops.Orders != nil {
		if err := s.ops.Orders.CreateOrder(context.Background(), *order); err != nil {
			return err
		}
	}
	s.cacheOrderLocked(*order)
	return nil
}

func (s *Service) updateOrderLocked(order *Order) error {
	if s.ops.Orders != nil {
		if err := s.ops.Orders.UpdateOrder(context.Background(), *order); err != nil {
			return err
		}
	}
	s.cacheOrderLocked(*order)
	return nil
}

func (s *Service) orderByIDLocked(id string) (*Order, error) {
	if s.ops.Orders != nil {
		loaded, err := s.ops.Orders.OrderByID(context.Background(), id)
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "order_not_found", "order not found")
			}
			return nil, err
		}
		return s.cacheOrderLocked(loaded), nil
	}
	order := s.ordersByID[id]
	if order == nil {
		return nil, E(http.StatusNotFound, "order_not_found", "order not found")
	}
	return order, nil
}

func (s *Service) cacheOrderLocked(order Order) *Order {
	cached := order
	s.ordersByID[cached.ID] = &cached
	return &cached
}

func (s *Service) ordersByUserLocked(userID string) ([]Order, error) {
	if s.ops.Orders != nil {
		orders, err := s.ops.Orders.ListOrdersByUser(context.Background(), userID)
		if err != nil {
			return nil, err
		}
		for _, order := range orders {
			s.cacheOrderLocked(order)
		}
		return orders, nil
	}
	out := []Order{}
	for _, order := range s.ordersByID {
		if order.UserID == userID {
			out = append(out, *order)
		}
	}
	return out, nil
}

func (s *Service) createReportLocked(report *Report) error {
	if s.ops.Reports != nil {
		if err := s.ops.Reports.CreateReport(context.Background(), *report); err != nil {
			return err
		}
	}
	s.cacheReportLocked(*report)
	return nil
}

func (s *Service) updateReportStatusLocked(reportID string, status string) (*Report, error) {
	if s.ops.Reports != nil {
		if err := s.ops.Reports.UpdateReportStatus(context.Background(), reportID, status); err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "report_not_found", "report not found")
			}
			return nil, err
		}
	}
	report, err := s.reportByIDLocked(reportID)
	if err != nil {
		return nil, err
	}
	report.Status = status
	s.cacheReportLocked(*report)
	return report, nil
}

func (s *Service) reportByIDLocked(id string) (*Report, error) {
	if s.ops.Reports != nil {
		loaded, err := s.ops.Reports.ReportByID(context.Background(), id)
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "report_not_found", "report not found")
			}
			return nil, err
		}
		return s.cacheReportLocked(loaded), nil
	}
	for _, report := range s.reports {
		if report.ID == id {
			return report, nil
		}
	}
	return nil, E(http.StatusNotFound, "report_not_found", "report not found")
}

func (s *Service) cacheReportLocked(report Report) *Report {
	cached := report
	for i, existing := range s.reports {
		if existing.ID == cached.ID {
			s.reports[i] = &cached
			return &cached
		}
	}
	s.reports = append(s.reports, &cached)
	return &cached
}

func (s *Service) createWebhookEventLocked(event *WebhookEvent) error {
	if s.ops.WebhookEvents != nil {
		if err := s.ops.WebhookEvents.CreateWebhookEvent(context.Background(), *event); err != nil {
			if errors.Is(err, ErrStoreConflict) {
				loaded, err := s.ops.WebhookEvents.WebhookEventByIdempotencyKey(context.Background(), event.IdempotencyKey)
				if err != nil {
					return err
				}
				s.cacheWebhookEventLocked(loaded)
				return nil
			}
			return err
		}
	}
	s.cacheWebhookEventLocked(*event)
	return nil
}

func (s *Service) cacheWebhookEventLocked(event WebhookEvent) *WebhookEvent {
	cached := event
	cached.Metadata = cloneMetadata(event.Metadata)
	for i, existing := range s.webhookEvents {
		if existing.ID == cached.ID {
			s.webhookEvents[i] = &cached
			s.webhookEventKeys[cached.IdempotencyKey] = cached.ID
			return &cached
		}
	}
	s.webhookEvents = append(s.webhookEvents, &cached)
	s.webhookEventKeys[cached.IdempotencyKey] = cached.ID
	return &cached
}

func (s *Service) createQueueItemLocked(queue *[]*QueueItem, item *QueueItem) error {
	if item.Status == "" {
		item.Status = "failed"
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = s.now().UTC()
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	if item.RunAfter.IsZero() {
		item.RunAfter = item.UpdatedAt
	}
	if s.ops.Queues != nil {
		if err := s.ops.Queues.CreateQueueItem(context.Background(), *item); err != nil {
			return err
		}
	}
	s.cacheQueueItemLocked(queue, *item)
	return nil
}

func (s *Service) deleteQueueItemsByKindTargetLocked(queue *[]*QueueItem, kind string, targetID string) error {
	if s.ops.Queues != nil {
		if err := s.ops.Queues.DeleteQueueItemsByKindTarget(context.Background(), kind, targetID); err != nil {
			return err
		}
	}
	s.removeQueueItemLocked(queue, targetID)
	return nil
}

func (s *Service) refreshQueueCachesLocked(ctx context.Context) error {
	if s.ops.Queues == nil {
		return nil
	}
	s.cleanupJobs = []*QueueItem{}
	s.cleanupFailures = []*QueueItem{}
	s.scanJobs = []*QueueItem{}
	s.scanFailures = []*QueueItem{}
	s.failedJobs = []*QueueItem{}

	cleanupJobs, err := s.ops.Queues.ListQueueItemsByKind(ctx, "cleanup")
	if err != nil {
		return fmt.Errorf("load cleanup jobs: %w", err)
	}
	for _, item := range cleanupJobs {
		if item.Status == "pending" {
			s.cacheQueueItemLocked(&s.cleanupJobs, item)
		}
	}
	cleanupFailures, err := s.ops.Queues.ListQueueItemsByKind(ctx, "cleanup_failed")
	if err != nil {
		return fmt.Errorf("load cleanup failures: %w", err)
	}
	for _, item := range cleanupFailures {
		s.cacheQueueItemLocked(&s.cleanupFailures, item)
	}
	scanJobs, err := s.ops.Queues.ListQueueItemsByKind(ctx, "scan")
	if err != nil {
		return fmt.Errorf("load scan jobs: %w", err)
	}
	for _, item := range scanJobs {
		if item.Status == "pending" {
			s.cacheQueueItemLocked(&s.scanJobs, item)
		}
	}
	scanFailures, err := s.ops.Queues.ListQueueItemsByKind(ctx, "scan_failed")
	if err != nil {
		return fmt.Errorf("load scan failures: %w", err)
	}
	for _, item := range scanFailures {
		s.cacheQueueItemLocked(&s.scanFailures, item)
	}
	failedJobs, err := s.ops.Queues.ListQueueItemsByStatus(ctx, "failed", 100)
	if err != nil {
		return fmt.Errorf("load failed jobs: %w", err)
	}
	for _, item := range failedJobs {
		s.cacheQueueItemLocked(&s.failedJobs, item)
	}
	return nil
}

func (s *Service) cacheQueueItemLocked(queue *[]*QueueItem, item QueueItem) {
	cached := item
	for i, existing := range *queue {
		if existing.ID == cached.ID {
			(*queue)[i] = &cached
			return
		}
	}
	*queue = append(*queue, &cached)
}

func (s *Service) scheduleCleanupJobLocked(targetID string, now time.Time) error {
	return s.createQueueItemLocked(&s.cleanupJobs, &QueueItem{
		ID:        s.newID("job"),
		Kind:      "cleanup",
		TargetID:  targetID,
		Status:    "pending",
		RunAfter:  now,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func (s *Service) scheduleScanJobLocked(targetID string, now time.Time) error {
	return s.createQueueItemLocked(&s.scanJobs, &QueueItem{
		ID:        s.newID("job"),
		Kind:      "scan",
		TargetID:  targetID,
		Status:    "pending",
		RunAfter:  now,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func (s *Service) markScanFailureLocked(targetID string, reason string, now time.Time) error {
	if err := s.deleteQueueItemsByKindTargetLocked(&s.scanFailures, "scan_failed", targetID); err != nil {
		return err
	}
	return s.createQueueItemLocked(&s.scanFailures, &QueueItem{
		ID:        s.newID("scanq"),
		Kind:      "scan_failed",
		TargetID:  targetID,
		Status:    "failed",
		Error:     reason,
		Attempts:  1,
		RunAfter:  now,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func (s *Service) cacheMailLocked(mail Mail) *Mail {
	cached := mail
	for i, existing := range s.mails {
		if existing.ID == cached.ID {
			s.mails[i] = &cached
			return &cached
		}
	}
	s.mails = append(s.mails, &cached)
	return &cached
}

func (s *Service) refreshMailCacheLocked(ctx context.Context) error {
	if s.ops.Mails == nil {
		return nil
	}
	mails, err := s.ops.Mails.QueuedMails(ctx, 1000)
	if err != nil {
		return fmt.Errorf("load queued mails: %w", err)
	}
	s.mails = []*Mail{}
	for _, mail := range mails {
		s.cacheMailLocked(mail)
	}
	return nil
}

func (s *Service) mailQueueItemsLocked(ctx context.Context, status string, limit int) ([]MailQueueItem, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "queued"
	}
	if s.ops.Mails != nil {
		return s.ops.Mails.MailQueueItems(ctx, status, limit)
	}
	if status != "queued" {
		return []MailQueueItem{}, nil
	}
	items := make([]MailQueueItem, 0, len(s.mails))
	for _, mail := range s.mails {
		if mail == nil {
			continue
		}
		items = append(items, MailQueueItem{
			ID:        mail.ID,
			To:        mail.To,
			Subject:   mail.Subject,
			Status:    "queued",
			RunAfter:  mail.CreatedAt,
			CreatedAt: mail.CreatedAt,
		})
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return items, nil
}

func (s *Service) createMailLocked(mail *Mail) error {
	if s.ops.Mails != nil {
		if err := s.ops.Mails.QueueMail(context.Background(), *mail); err != nil {
			return err
		}
	}
	s.cacheMailLocked(*mail)
	return nil
}

func (s *Service) createUserLocked(user *User) error {
	if s.auth.Users != nil {
		if err := s.auth.Users.CreateUser(context.Background(), *user); err != nil {
			return err
		}
	}
	s.cacheUserLocked(*user)
	return nil
}

func (s *Service) createOAuthIdentityLocked(identity *OAuthIdentity) error {
	identity.Provider = normalizeProvider(identity.Provider)
	identity.Subject = strings.TrimSpace(identity.Subject)
	if identity.UserID == "" || identity.Provider == "" || identity.Subject == "" {
		return E(http.StatusBadRequest, "invalid_oauth_identity", "oauth identity is incomplete")
	}
	if identity.CreatedAt.IsZero() {
		identity.CreatedAt = s.now().UTC()
	}
	if identity.UpdatedAt.IsZero() {
		identity.UpdatedAt = identity.CreatedAt
	}
	if s.auth.OAuthIdentities != nil {
		if err := s.auth.OAuthIdentities.LinkOAuthIdentity(context.Background(), *identity); err != nil {
			if errors.Is(err, ErrStoreConflict) {
				return E(http.StatusConflict, "oauth_identity_conflict", "oauth identity is already linked")
			}
			return err
		}
	}
	s.cacheOAuthIdentityLocked(*identity)
	return nil
}

func (s *Service) cacheOAuthIdentityLocked(identity OAuthIdentity) {
	identity.Provider = normalizeProvider(identity.Provider)
	identity.Subject = strings.TrimSpace(identity.Subject)
	cached := identity
	s.oauthIdentities[oauthIdentityKey(cached.Provider, cached.Subject)] = &cached
}

func (s *Service) updateUserLocked(user *User) error {
	if s.auth.Users != nil {
		if err := s.auth.Users.UpdateUser(context.Background(), *user); err != nil {
			return err
		}
	}
	s.cacheUserLocked(*user)
	return nil
}

func (s *Service) cacheUserLocked(user User) *User {
	cached := user
	s.usersByID[cached.ID] = &cached
	s.userIDByEmail[cached.Email] = cached.ID
	return &cached
}

func (s *Service) listUsersLocked() ([]User, error) {
	if s.auth.Users != nil {
		users, err := s.auth.Users.ListUsers(context.Background())
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			s.cacheUserLocked(user)
		}
		return users, nil
	}
	users := make([]User, 0, len(s.usersByID))
	for _, user := range s.usersByID {
		users = append(users, *user)
	}
	return users, nil
}

func (s *Service) newSessionLocked(user *User) (AuthResult, error) {
	now := s.now().UTC()
	session := &Session{ID: newToken(), UserID: user.ID, CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	if s.auth.Sessions != nil {
		if err := s.auth.Sessions.CreateSession(context.Background(), *session); err != nil {
			return AuthResult{}, err
		}
	}
	s.sessionsByID[session.ID] = session
	view, err := s.viewUserLocked(user)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: view, SessionID: session.ID, ExpiresAt: session.ExpiresAt}, nil
}

func (s *Service) userForSessionLocked(sessionID string) (*User, error) {
	if sessionID == "" {
		return nil, E(http.StatusUnauthorized, "unauthenticated", "login required")
	}
	var session *Session
	if s.auth.Sessions != nil {
		loaded, err := s.auth.Sessions.SessionByID(context.Background(), sessionID)
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusUnauthorized, "unauthenticated", "login required")
			}
			return nil, err
		}
		session = &loaded
		s.sessionsByID[session.ID] = session
	} else {
		session = s.sessionsByID[sessionID]
	}
	if session == nil || session.RevokedAt != nil || !session.ExpiresAt.After(s.now().UTC()) {
		return nil, E(http.StatusUnauthorized, "unauthenticated", "login required")
	}
	return s.activeUserLocked(session.UserID)
}

func (s *Service) activeUserLocked(userID string) (*User, error) {
	user, err := s.userByIDLocked(userID)
	if err != nil {
		return nil, E(http.StatusUnauthorized, "user_not_found", "user not found")
	}
	if user.DeletedAt != nil {
		return nil, E(http.StatusUnauthorized, "user_not_found", "user not found")
	}
	if user.Frozen {
		return nil, E(http.StatusForbidden, "account_frozen", "account is frozen")
	}
	return user, nil
}

func (s *Service) userByIDLocked(userID string) (*User, error) {
	if s.auth.Users != nil {
		user, err := s.auth.Users.UserByID(context.Background(), userID)
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "user_not_found", "user not found")
			}
			return nil, err
		}
		return s.cacheUserLocked(user), nil
	}
	user := s.usersByID[userID]
	if user == nil {
		return nil, E(http.StatusNotFound, "user_not_found", "user not found")
	}
	return user, nil
}

func (s *Service) userByEmailLocked(email string) (*User, error) {
	email = normalizeEmail(email)
	if s.auth.Users != nil {
		user, err := s.auth.Users.UserByEmail(context.Background(), email)
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "user_not_found", "user not found")
			}
			return nil, err
		}
		return s.cacheUserLocked(user), nil
	}
	userID := s.userIDByEmail[email]
	if userID == "" {
		return nil, E(http.StatusNotFound, "user_not_found", "user not found")
	}
	return s.usersByID[userID], nil
}

func (s *Service) oauthIdentityByProviderSubjectLocked(provider string, subject string) (OAuthIdentity, bool, error) {
	provider = normalizeProvider(provider)
	subject = strings.TrimSpace(subject)
	if s.auth.OAuthIdentities != nil {
		identity, err := s.auth.OAuthIdentities.OAuthIdentityByProviderSubject(context.Background(), provider, subject)
		if err != nil {
			if isStoreNotFound(err) {
				return OAuthIdentity{}, false, nil
			}
			return OAuthIdentity{}, false, err
		}
		s.cacheOAuthIdentityLocked(identity)
		return identity, true, nil
	}
	identity := s.oauthIdentities[oauthIdentityKey(provider, subject)]
	if identity == nil {
		return OAuthIdentity{}, false, nil
	}
	return *identity, true, nil
}

func (s *Service) oauthIdentitiesByUserLocked(userID string) ([]OAuthIdentity, error) {
	if s.auth.OAuthIdentities != nil {
		identities, err := s.auth.OAuthIdentities.OAuthIdentitiesByUser(context.Background(), userID)
		if err != nil {
			return nil, err
		}
		for _, identity := range identities {
			s.cacheOAuthIdentityLocked(identity)
		}
		return identities, nil
	}
	identities := []OAuthIdentity{}
	for _, identity := range s.oauthIdentities {
		if identity.UserID == userID {
			identities = append(identities, *identity)
		}
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i].Provider < identities[j].Provider })
	return identities, nil
}

func (s *Service) deleteOAuthIdentityLocked(userID string, provider string) error {
	provider = normalizeProvider(provider)
	if s.auth.OAuthIdentities != nil {
		if err := s.auth.OAuthIdentities.DeleteOAuthIdentity(context.Background(), userID, provider); err != nil {
			if isStoreNotFound(err) {
				return E(http.StatusNotFound, "oauth_identity_not_linked", "oauth provider is not linked")
			}
			return err
		}
	}
	for key, identity := range s.oauthIdentities {
		if identity.UserID == userID && identity.Provider == provider {
			delete(s.oauthIdentities, key)
		}
	}
	return nil
}

func (s *Service) checkLoginRateLimitLocked(email string) error {
	failure, ok, err := s.loginFailureLocked(email)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	now := s.now().UTC()
	if failure.LockedUntil.After(now) {
		return E(http.StatusTooManyRequests, "login_rate_limited", "too many failed login attempts")
	}
	if now.Sub(failure.WindowStart) > 15*time.Minute {
		return s.deleteLoginFailureLocked(email)
	}
	return nil
}

func (s *Service) loginFailureLocked(email string) (LoginFailure, bool, error) {
	if s.auth.LoginFailures != nil {
		failure, err := s.auth.LoginFailures.LoginFailure(context.Background(), email)
		if err != nil {
			if isStoreNotFound(err) {
				return LoginFailure{}, false, nil
			}
			return LoginFailure{}, false, err
		}
		s.loginFailures[email] = &failure
		return failure, true, nil
	}
	failure := s.loginFailures[email]
	if failure == nil {
		return LoginFailure{}, false, nil
	}
	return *failure, true, nil
}

func (s *Service) recordLoginFailureLocked(email string) error {
	now := s.now().UTC()
	failure, ok, err := s.loginFailureLocked(email)
	if err != nil {
		return err
	}
	if !ok || now.Sub(failure.WindowStart) > 15*time.Minute {
		failure = LoginFailure{Count: 1, WindowStart: now}
	} else {
		failure.Count++
	}
	if failure.Count >= 5 {
		failure.LockedUntil = now.Add(15 * time.Minute)
	}
	if s.auth.LoginFailures != nil {
		if err := s.auth.LoginFailures.SaveLoginFailure(context.Background(), email, failure); err != nil {
			return err
		}
	}
	s.loginFailures[email] = &failure
	return nil
}

func (s *Service) deleteLoginFailureLocked(email string) error {
	if s.auth.LoginFailures != nil {
		if err := s.auth.LoginFailures.DeleteLoginFailure(context.Background(), email); err != nil {
			return err
		}
	}
	delete(s.loginFailures, email)
	return nil
}

func (s *Service) createAuthTokenLocked(kind string, token AuthToken) error {
	if s.auth.Tokens != nil {
		if err := s.auth.Tokens.CreateAuthToken(context.Background(), kind, token); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) consumeTokenLocked(kind string, tokens map[string]*AuthToken, token string) (*AuthToken, error) {
	hash := tokenHash(token)
	authToken := tokens[hash]
	if s.auth.Tokens != nil {
		loaded, err := s.auth.Tokens.AuthToken(context.Background(), kind, hash)
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
			}
			return nil, err
		}
		authToken = &loaded
		tokens[hash] = authToken
	}
	if authToken == nil || authToken.UsedAt != nil || !authToken.ExpiresAt.After(s.now().UTC()) {
		return nil, E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
	}
	now := s.now().UTC()
	if s.auth.Tokens != nil {
		if err := s.auth.Tokens.MarkAuthTokenUsed(context.Background(), kind, hash, now); err != nil {
			return nil, err
		}
	}
	authToken.UsedAt = &now
	return authToken, nil
}

func (s *Service) ensureAllowedRegistrationEmailLocked(email string) error {
	domain := emailDomain(email)
	if domain == "" {
		return E(http.StatusBadRequest, "invalid_email", "valid email is required")
	}
	allowed := s.runtimeConfig.Registration.AllowedDomains
	if len(allowed) == 0 {
		return nil
	}
	for _, candidate := range allowed {
		if strings.EqualFold(domain, candidate) {
			return nil
		}
	}
	return E(http.StatusForbidden, "email_domain_not_allowed", "email domain is not allowed for registration")
}

func (s *Service) consumeRegistrationEmailVerificationLocked(email string, code string) error {
	hash := registrationVerificationHash(email, code)
	authToken := s.emailVerifies[hash]
	if s.auth.Tokens != nil {
		loaded, err := s.auth.Tokens.AuthToken(context.Background(), "registration_email_verification", hash)
		if err != nil {
			if isStoreNotFound(err) {
				return E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
			}
			return err
		}
		authToken = &loaded
		s.emailVerifies[hash] = authToken
	}
	if authToken == nil || authToken.UsedAt != nil || !authToken.ExpiresAt.After(s.now().UTC()) {
		return E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
	}
	if subtle.ConstantTimeCompare([]byte(normalizeEmail(authToken.Email)), []byte(normalizeEmail(email))) != 1 {
		return E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
	}
	now := s.now().UTC()
	if s.auth.Tokens != nil {
		if err := s.auth.Tokens.MarkAuthTokenUsed(context.Background(), "registration_email_verification", hash, now); err != nil {
			return err
		}
	}
	authToken.UsedAt = &now
	return nil
}

func (s *Service) verifyRegistrationTurnstile(ctx context.Context, token string, remoteIP string) error {
	s.mu.Lock()
	required := s.runtimeConfig.Registration.RequireTurnstile
	s.mu.Unlock()
	if !required {
		return nil
	}
	return s.VerifyTurnstile(ctx, token, remoteIP)
}

func (s *Service) VerifyTurnstile(ctx context.Context, token string, remoteIP string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verifyTurnstileLocked(ctx, token, remoteIP)
}

func (s *Service) issueEmailVerificationLocked(user *User) (string, error) {
	token := newToken()
	hash := tokenHash(token)
	authToken := AuthToken{Hash: hash, UserID: user.ID, Email: user.Email, ExpiresAt: s.now().UTC().Add(24 * time.Hour)}
	if err := s.createAuthTokenLocked("email_verification", authToken); err != nil {
		return "", err
	}
	s.emailVerifies[hash] = &authToken
	if err := s.mail(user.Email, "Verify your PasteBox email", s.authLinkBody("Verify your PasteBox email", "/email-verification", token, 24*time.Hour)); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) revokeUserSessionsLocked(userID string) error {
	now := s.now().UTC()
	if s.auth.Sessions != nil {
		if _, err := s.auth.Sessions.RevokeUserSessions(context.Background(), userID, now); err != nil {
			return err
		}
	}
	for _, session := range s.sessionsByID {
		if session.UserID == userID && session.RevokedAt == nil {
			session.RevokedAt = &now
		}
	}
	return nil
}

func isStoreNotFound(err error) bool {
	return errors.Is(err, ErrStoreNotFound)
}

func isAppStatus(err error, status int) bool {
	var appErr *Error
	return errors.As(err, &appErr) && appErr.Status == status
}

func (s *Service) ownerPasteLocked(userID string, id string) (*Paste, error) {
	if _, err := s.activeUserLocked(userID); err != nil {
		return nil, err
	}
	paste := s.pastesByID[id]
	if paste == nil || paste.UserID != userID {
		return nil, E(http.StatusNotFound, "paste_not_found", "paste not found")
	}
	return paste, nil
}

func (s *Service) isPasteVisibleLocked(paste *Paste) bool {
	return paste != nil && paste.Status == "active" && paste.ExpiresAt.After(s.now().UTC())
}

func (s *Service) planForUserLocked(user *User) (plans.Plan, error) {
	if user.PlanExpiresAt != nil && !user.PlanExpiresAt.After(s.now().UTC()) {
		user.PlanID = "free"
		user.PlanExpiresAt = nil
	}
	plan, ok := plans.Find(s.catalog, user.PlanID)
	if !ok {
		plan, _ = plans.Find(s.catalog, "free")
	}
	return plan, nil
}

func cloneCatalog(catalog plans.Catalog) plans.Catalog {
	return plans.Catalog{
		Plans:  append([]plans.Plan(nil), catalog.Plans...),
		Prices: append([]plans.Price(nil), catalog.Prices...),
	}
}

func (s *Service) ensureUserCanWriteLocked(user *User, plan plans.Plan) error {
	if !user.EmailVerified {
		return E(http.StatusForbidden, "email_not_verified", "email verification is required before writing content")
	}
	quota, err := s.quotaLocked(user.ID, plan)
	if err != nil {
		return err
	}
	if quota.OverLimit {
		return E(http.StatusForbidden, "quota_read_only", "account is over current plan limits")
	}
	if user.Frozen {
		return E(http.StatusForbidden, "account_frozen", "account is frozen")
	}
	return nil
}

func (s *Service) ensureCanCreatePasteLocked(user *User, plan plans.Plan, input PasteInput, extraBytes int64, extraAttachments int) error {
	if err := s.ensureUserCanWriteLocked(user, plan); err != nil {
		return err
	}
	if err := ensureTagsWithinPlan(plan, nil, normalizeTags(input.Tags)); err != nil {
		return err
	}
	textBytes := int64(len([]byte(input.Text)))
	if textBytes > plan.SingleTextBytes {
		return E(http.StatusRequestEntityTooLarge, "text_too_large", "text exceeds plan limit; upload it as a .txt attachment")
	}
	if textBytes+extraBytes > plan.SinglePasteBytes {
		return E(http.StatusRequestEntityTooLarge, "paste_too_large", "paste exceeds plan total size")
	}
	quota, err := s.quotaLocked(user.ID, plan)
	if err != nil {
		return err
	}
	if extraAttachments == 0 && quota.ActivePasteCount+1 > plan.ActivePasteLimit {
		return E(http.StatusForbidden, "active_paste_limit", "active paste count exceeds plan limit")
	}
	if quota.ActiveStorageBytes+textBytes+extraBytes > plan.ActiveStorageBytes {
		return E(http.StatusForbidden, "storage_limit", "active storage exceeds plan limit")
	}
	if quota.DailyUploadBytes+textBytes+extraBytes > plan.DailyUploadBytes {
		return E(http.StatusForbidden, "daily_upload_limit", "daily upload traffic exceeds plan limit")
	}
	return nil
}

func ensureTagsWithinPlan(plan plans.Plan, currentTags []string, nextTags []string) error {
	if tagsEqual(currentTags, nextTags) {
		return nil
	}
	if len(currentTags) > plan.TagsPerPasteLimit {
		return E(http.StatusForbidden, "tag_limit", "existing tags are read-only on the current plan")
	}
	if len(nextTags) > plan.TagsPerPasteLimit {
		return E(http.StatusForbidden, "tag_limit", "tags exceed plan limit")
	}
	return nil
}

func (s *Service) quotaLocked(userID string, plan plans.Plan) (QuotaView, error) {
	var activeCount int
	var activeStorage int64
	for _, paste := range s.pastesByID {
		if paste.UserID != userID || !s.isPasteVisibleLocked(paste) {
			continue
		}
		activeCount++
		activeStorage += s.pasteSizeLocked(paste)
	}
	upload, err := s.dailyMetricLocked(userID, "upload")
	if err != nil {
		return QuotaView{}, err
	}
	download, err := s.dailyMetricLocked(userID, "share_download")
	if err != nil {
		return QuotaView{}, err
	}
	return QuotaView{
		Plan:                    plan,
		ActivePasteCount:        activeCount,
		ActiveStorageBytes:      activeStorage,
		DailyUploadBytes:        upload,
		DailyShareDownloadBytes: download,
		OverLimit:               activeCount > plan.ActivePasteLimit || activeStorage > plan.ActiveStorageBytes,
	}, nil
}

func (s *Service) pasteSizeLocked(paste *Paste) int64 {
	total := int64(len([]byte(paste.Text)))
	for _, id := range paste.AttachmentIDs {
		if att := s.attachmentsByID[id]; att != nil && att.Status == "active" {
			total += att.Size
		}
	}
	return total
}

func (s *Service) viewPasteLocked(paste *Paste) PasteView {
	attachments := s.attachmentsForPasteLocked(paste)
	attachmentViews := make([]AttachmentView, 0, len(attachments))
	for _, attachment := range attachments {
		attachmentViews = append(attachmentViews, viewAttachment(attachment))
	}
	shareCount := 0
	for _, share := range s.sharesByID {
		if share.PasteID == paste.ID && share.RevokedAt == nil {
			shareCount++
		}
	}
	now := s.now().UTC()
	return PasteView{
		ID:            paste.ID,
		Title:         paste.Title,
		Text:          paste.Text,
		TextPreview:   preview(paste.Text),
		Tags:          append([]string{}, paste.Tags...),
		Pinned:        paste.Pinned,
		Favorite:      paste.Favorite,
		Status:        paste.Status,
		ScanStatus:    paste.ScanStatus,
		ShareCount:    shareCount,
		SizeBytes:     s.pasteSizeLocked(paste),
		ExpiresAt:     paste.ExpiresAt,
		CreatedAt:     paste.CreatedAt,
		UpdatedAt:     paste.UpdatedAt,
		Attachments:   attachmentViews,
		Expired:       !paste.ExpiresAt.After(now),
		SecondsToLive: max64(int64(paste.ExpiresAt.Sub(now).Seconds()), 0),
	}
}

func (s *Service) attachmentsForPasteLocked(paste *Paste) []*Attachment {
	out := make([]*Attachment, 0, len(paste.AttachmentIDs))
	for _, id := range paste.AttachmentIDs {
		attachment := s.attachmentsByID[id]
		if attachment != nil && attachment.Status != "deleted" {
			out = append(out, attachment)
		}
	}
	return out
}

func (s *Service) viewShareLocked(share *Share) ShareView {
	return ShareView{
		ID:               share.ID,
		PasteID:          share.PasteID,
		Token:            share.Token,
		URL:              strings.TrimRight(s.cfg.PublicURL, "/") + "/s/" + share.Token,
		HasPassword:      share.PasswordHash != "",
		LoginRequired:    share.LoginRequired,
		MaxVisits:        share.MaxVisits,
		MaxDownloads:     share.MaxDownloads,
		VisitCount:       share.VisitCount,
		DownloadCount:    share.DownloadCount,
		ExpiresAt:        share.ExpiresAt,
		RevokedAt:        share.RevokedAt,
		CreatedAt:        share.CreatedAt,
		LastVisitedAt:    share.LastVisitedAt,
		LastDownloadedAt: share.LastDownloadedAt,
	}
}

func (s *Service) validShareLocked(token string, password string, viewerUserID string, forDownload bool) (*Share, *Paste, error) {
	return s.validShareAccessLocked(token, password, viewerUserID, forDownload, false)
}

func (s *Service) validShareAccessLocked(token string, password string, viewerUserID string, forDownload bool, passwordVerified bool) (*Share, *Paste, error) {
	share, err := s.shareByTokenHashLocked(tokenHash(token))
	if err != nil {
		return nil, nil, E(http.StatusNotFound, "share_not_found", "share not found")
	}
	now := s.now().UTC()
	if share.RevokedAt != nil || !share.ExpiresAt.After(now) {
		return nil, nil, E(http.StatusGone, "share_expired", "share is expired or revoked")
	}
	if share.LoginRequired && viewerUserID == "" {
		return nil, nil, E(http.StatusUnauthorized, "login_required", "login required for this share")
	}
	if share.MaxVisits > 0 && !forDownload && share.VisitCount >= share.MaxVisits {
		return nil, nil, E(http.StatusGone, "visit_limit_reached", "share visit limit reached")
	}
	if share.MaxDownloads > 0 && forDownload && share.DownloadCount >= share.MaxDownloads {
		return nil, nil, E(http.StatusGone, "download_limit_reached", "share download limit reached")
	}
	if share.PasswordHash != "" && !passwordVerified && optionalPasswordHash(password) != share.PasswordHash {
		share.LastAccessFailure = &now
		if err := s.updateShareLocked(share); err != nil {
			return nil, nil, err
		}
		return nil, nil, E(http.StatusUnauthorized, "invalid_share_password", "share password is invalid")
	}
	paste, err := s.pasteByIDLocked(share.PasteID)
	if err != nil {
		return nil, nil, err
	}
	if !s.isPasteVisibleLocked(paste) {
		return nil, nil, E(http.StatusGone, "paste_expired", "paste is expired or deleted")
	}
	return share, paste, nil
}

func (s *Service) dailyMetricLocked(userID string, kind string) (int64, error) {
	return s.dailyMetrics.DailyMetric(context.Background(), userID, kind, s.now().UTC())
}

func (s *Service) recordDailyUploadLocked(userID string, bytes int64) error {
	return s.dailyMetrics.RecordDailyMetric(context.Background(), userID, "upload", s.now().UTC(), bytes)
}

func (s *Service) recordDailyShareDownloadLocked(userID string, bytes int64) error {
	return s.dailyMetrics.RecordDailyMetric(context.Background(), userID, "share_download", s.now().UTC(), bytes)
}

func (s *Service) requireAdminLocked(userID string) error {
	user, err := s.activeUserLocked(userID)
	if err != nil {
		return err
	}
	if user.Role != "admin" {
		return E(http.StatusForbidden, "admin_required", "admin role required")
	}
	return nil
}

func (s *Service) auditLocked(actorID string, action string, target string, metadata map[string]any) error {
	log := &AuditLog{ID: s.newID("aud"), ActorID: actorID, Action: action, Target: target, Metadata: cloneMetadata(metadata), CreatedAt: s.now().UTC()}
	if s.audit != nil {
		if err := s.audit.RecordAuditLog(context.Background(), *log); err != nil {
			return err
		}
	}
	s.auditLogs = append(s.auditLogs, log)
	return nil
}

func (s *Service) recordWebhookEventLocked(provider string, eventType string, targetID string, idempotencyKey string, metadata map[string]any) (WebhookEvent, error) {
	provider = defaultString(normalizeProvider(provider), "local")
	eventType = strings.TrimSpace(eventType)
	if idempotencyKey == "" {
		idempotencyKey = provider + ":" + eventType + ":" + targetID + ":" + s.newID("idem")
	}
	if event, ok, err := s.webhookEventByKeyLocked(idempotencyKey); err != nil {
		return WebhookEvent{}, err
	} else if ok {
		return event, nil
	}
	event := &WebhookEvent{
		ID:             s.newID("wh"),
		Provider:       provider,
		EventType:      eventType,
		TargetID:       strings.TrimSpace(targetID),
		IdempotencyKey: idempotencyKey,
		Processed:      true,
		Metadata:       cloneMetadata(metadata),
		ReceivedAt:     s.now().UTC(),
	}
	if err := s.createWebhookEventLocked(event); err != nil {
		return WebhookEvent{}, err
	}
	loaded, ok, err := s.webhookEventByKeyLocked(idempotencyKey)
	if err != nil {
		return WebhookEvent{}, err
	}
	if ok {
		return loaded, nil
	}
	return *event, nil
}

func (s *Service) webhookEventByKeyLocked(idempotencyKey string) (WebhookEvent, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s.ops.WebhookEvents != nil {
		event, err := s.ops.WebhookEvents.WebhookEventByIdempotencyKey(context.Background(), idempotencyKey)
		if err != nil {
			if isStoreNotFound(err) {
				return WebhookEvent{}, false, nil
			}
			return WebhookEvent{}, false, err
		}
		return *s.cacheWebhookEventLocked(event), true, nil
	}
	eventID := s.webhookEventKeys[idempotencyKey]
	if eventID == "" {
		return WebhookEvent{}, false, nil
	}
	event, err := s.webhookEventByIDLocked(eventID)
	if err != nil {
		return WebhookEvent{}, false, err
	}
	if event == nil {
		return WebhookEvent{}, false, nil
	}
	return *event, true, nil
}

func (s *Service) webhookEventByIDLocked(eventID string) (*WebhookEvent, error) {
	if s.ops.WebhookEvents != nil {
		event, err := s.ops.WebhookEvents.WebhookEventByID(context.Background(), eventID)
		if err != nil {
			if isStoreNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return s.cacheWebhookEventLocked(event), nil
	}
	for _, event := range s.webhookEvents {
		if event.ID == eventID {
			return event, nil
		}
	}
	return nil, nil
}

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
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

func oauthIdentityKey(provider string, subject string) string {
	return normalizeProvider(provider) + "\x00" + strings.TrimSpace(subject)
}

func hasOAuthProvider(identities []OAuthIdentity, provider string) bool {
	provider = normalizeProvider(provider)
	for _, identity := range identities {
		if normalizeProvider(identity.Provider) == provider {
			return true
		}
	}
	return false
}

func oauthProviderNames(identities []OAuthIdentity) []string {
	providers := []string{}
	seen := map[string]struct{}{}
	for _, identity := range identities {
		provider := normalizeProvider(identity.Provider)
		if provider == "" {
			continue
		}
		if _, ok := seen[provider]; ok {
			continue
		}
		seen[provider] = struct{}{}
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers
}

func cloneMetadata(metadata map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func cloneWebhookEvent(event WebhookEvent) WebhookEvent {
	event.Metadata = cloneMetadata(event.Metadata)
	return event
}

func cloneAuditLog(log AuditLog) AuditLog {
	log.Metadata = cloneMetadata(log.Metadata)
	return log
}

func stringFromMetadata(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func (s *Service) removeQueueItemLocked(queue *[]*QueueItem, targetID string) {
	filtered := (*queue)[:0]
	for _, item := range *queue {
		if item.TargetID != targetID {
			filtered = append(filtered, item)
		}
	}
	*queue = filtered
}

func (s *Service) rollbackAttachmentCreateLocked(paste *Paste, previousScanStatus string, previousUpdatedAt time.Time, attachment *Attachment, attachmentCreated bool, pasteUpdated bool, scanQueueCreated bool) {
	if scanQueueCreated {
		_ = s.deleteQueueItemsByKindTargetLocked(&s.scanJobs, "scan", attachment.ID)
		_ = s.deleteQueueItemsByKindTargetLocked(&s.scanFailures, "scan_failed", attachment.ID)
	}
	if pasteUpdated {
		paste.ScanStatus = previousScanStatus
		paste.UpdatedAt = previousUpdatedAt
		_ = s.updatePasteLocked(paste)
	} else {
		paste.ScanStatus = previousScanStatus
		paste.UpdatedAt = previousUpdatedAt
	}
	if attachmentCreated {
		_ = s.deleteAttachmentLocked(attachment)
	}
	s.rollbackStoredObjectLocked(attachment.ObjectKey)
}

func (s *Service) rollbackStoredObjectLocked(objectKey string) {
	if objectKey == "" {
		return
	}
	if refs := s.objectRefs[objectKey]; refs > 0 {
		s.objectRefs[objectKey] = refs - 1
		if s.objectRefs[objectKey] > 0 {
			if attachment := s.attachmentForObjectKeyLocked(objectKey); attachment != nil {
				_ = s.persistObjectRefLocked(attachment, s.objectRefs[objectKey], s.now().UTC())
			}
			return
		}
	}
	delete(s.objectRefs, objectKey)
	if err := s.deleteObjectLocked(objectKey); err != nil {
		return
	}
	if s.content.ObjectRefs != nil {
		if err := s.content.ObjectRefs.DeleteObjectRef(context.Background(), objectKey); err != nil && !isStoreNotFound(err) {
			return
		}
	}
}

func (s *Service) rollbackUnreferencedStoredObjectLocked(objectKey string, previousRefs int) {
	if previousRefs > 0 {
		return
	}
	_ = s.deleteObjectLocked(objectKey)
}

func (s *Service) mail(to string, subject string, body string) error {
	return s.createMailLocked(&Mail{ID: s.newID("mail"), To: to, Subject: subject, Body: body, CreatedAt: s.now().UTC()})
}

func (s *Service) authTokenResponse(token string, message string) map[string]string {
	response := map[string]string{"message": message}
	if s.cfg.ExposeDevAuthTokens() {
		response["devToken"] = token
	}
	return response
}

func (s *Service) authLinkBody(action string, routePath string, token string, ttl time.Duration) string {
	link := s.publicURLWithToken(routePath, token)
	return fmt.Sprintf("%s:\n\n%s\n\nThis link expires in %s.", action, link, authTokenTTL(ttl))
}

func (s *Service) publicURLWithToken(routePath string, token string) string {
	base := strings.TrimRight(s.cfg.PublicURL, "/")
	if base == "" {
		base = "http://localhost:5173"
	}
	routePath = "/" + strings.Trim(strings.TrimSpace(routePath), "/")
	values := url.Values{}
	values.Set("token", token)
	return base + routePath + "?" + values.Encode()
}

func authTokenTTL(ttl time.Duration) string {
	if ttl%time.Hour == 0 {
		hours := int(ttl / time.Hour)
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	if ttl%time.Minute == 0 {
		minutes := int(ttl / time.Minute)
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}
	return ttl.String()
}

func (s *Service) newID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(raw[:]))
}

func (s *Service) viewUserLocked(user *User) (UserView, error) {
	identities, err := s.oauthIdentitiesByUserLocked(user.ID)
	if err != nil {
		return UserView{}, err
	}
	view := viewUser(user)
	view.OAuthProviders = oauthProviderNames(identities)
	return view, nil
}

func viewUser(user *User) UserView {
	return UserView{
		ID:                user.ID,
		Email:             user.Email,
		DisplayName:       user.DisplayName,
		Language:          user.Language,
		Role:              user.Role,
		EmailVerified:     user.EmailVerified,
		PlanID:            user.PlanID,
		PlanExpiresAt:     user.PlanExpiresAt,
		OAuthProviders:    []string{},
		Frozen:            user.Frozen,
		CreatedAt:         user.CreatedAt,
		DeleteRequestedAt: user.DeleteRequestedAt,
		DeleteScheduledAt: user.DeleteScheduledAt,
	}
}

func viewAttachment(attachment *Attachment) AttachmentView {
	view := AttachmentView{
		ID:            attachment.ID,
		PasteID:       attachment.PasteID,
		FileName:      attachment.FileName,
		ContentType:   attachment.ContentType,
		Size:          attachment.Size,
		SHA256:        attachment.SHA256,
		Status:        attachment.Status,
		ScanStatus:    attachment.ScanStatus,
		Risk:          attachment.Risk,
		DownloadCount: attachment.DownloadN,
		CreatedAt:     attachment.CreatedAt,
	}
	if attachment.ImageWidth > 0 && attachment.ImageHeight > 0 {
		view.ImagePreview = &ImagePreview{Width: attachment.ImageWidth, Height: attachment.ImageHeight}
	}
	return view
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	return "argon2id$" + base64.RawURLEncoding.EncodeToString(salt) + "$" + base64.RawURLEncoding.EncodeToString(key), nil
}

func verifyPassword(encoded string, password string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return E(http.StatusInternalServerError, "bad_password_hash", "stored password hash is invalid")
	}
	salt, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	want, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return E(http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
	}
	return nil
}

func newToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func verificationCode() string {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(raw)%1000000)
}

func registrationVerificationHash(email string, code string) string {
	return tokenHash(normalizeEmail(email) + "\x00" + strings.TrimSpace(code))
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func optionalPasswordHash(password string) string {
	if password == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range tags {
		for _, part := range strings.Split(raw, ",") {
			tag := strings.ToLower(strings.TrimSpace(part))
			if tag != "" && !seen[tag] {
				seen[tag] = true
				out = append(out, tag)
			}
		}
	}
	sort.Strings(out)
	return out
}

func resolveExpiresAt(now time.Time, expiresInSeconds int64, plan plans.Plan) time.Time {
	if expiresInSeconds <= 0 || expiresInSeconds > plan.MaxRetentionSeconds {
		expiresInSeconds = plan.MaxRetentionSeconds
	}
	return now.Add(time.Duration(expiresInSeconds) * time.Second)
}

func matchesPaste(paste PasteView, query string, filter string, tag string) bool {
	if tag != "" && !contains(paste.Tags, tag) {
		return false
	}
	if query != "" {
		haystack := strings.ToLower(paste.Title + "\n" + paste.Text + "\n" + strings.Join(paste.Tags, " "))
		for _, attachment := range paste.Attachments {
			haystack += "\n" + strings.ToLower(attachment.FileName)
		}
		if !strings.Contains(haystack, query) {
			return false
		}
	}
	switch filter {
	case "", "all":
		return true
	case "text":
		return strings.TrimSpace(paste.Text) != ""
	case "image":
		for _, att := range paste.Attachments {
			if strings.HasPrefix(att.ContentType, "image/") {
				return true
			}
		}
		return false
	case "file":
		return len(paste.Attachments) > 0
	case "expiring":
		return paste.SecondsToLive <= int64(24*time.Hour.Seconds())
	case "shared":
		return paste.ShareCount > 0
	case "favorite":
		return paste.Favorite
	case "pinned":
		return paste.Pinned
	default:
		return true
	}
}

func preview(text string) string {
	trimmed := strings.TrimSpace(text)
	if len([]rune(trimmed)) <= 160 {
		return trimmed
	}
	runes := []rune(trimmed)
	return string(runes[:160]) + "..."
}

func classifyAttachmentRisk(fileName string, contentType string) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == ".exe" || ext == ".bat" || ext == ".cmd" || ext == ".scr" || ext == ".msi" {
		return "executable_file"
	}
	if strings.Contains(strings.ToLower(contentType), "html") || strings.Contains(strings.ToLower(contentType), "svg") {
		return "render_as_download_only"
	}
	return ""
}

func (s *Service) objectContentLocked(attachment *Attachment) ([]byte, error) {
	if s.objectStore != nil {
		content, err := s.objectStore.GetObject(context.Background(), attachment.ObjectKey)
		if err != nil {
			return nil, err
		}
		return content, nil
	}
	content, ok := s.objects[attachment.ObjectKey]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return append([]byte(nil), content...), nil
}

func (s *Service) deleteObjectLocked(key string) error {
	if s.objectStore != nil {
		if err := s.objectStore.DeleteObject(context.Background(), key); err != nil && !errors.Is(err, ErrObjectNotFound) {
			return fmt.Errorf("delete object: %w", err)
		}
		return nil
	}
	delete(s.objects, key)
	return nil
}

func aggregateScanStatus(attachments []*Attachment) string {
	status := "clean"
	for _, attachment := range attachments {
		if attachment.ScanStatus == "malicious" {
			return "malicious"
		}
		if attachment.ScanStatus == "scan_failed" {
			status = "scan_failed"
		}
		if attachment.ScanStatus == "pending" && status == "clean" {
			status = "pending"
		}
	}
	return status
}

func sanitizeFileName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "/" || name == "" {
		return "attachment.bin"
	}
	return name
}

func countReports(reports []*Report, status string) int {
	count := 0
	for _, report := range reports {
		if report.Status == status {
			count++
		}
	}
	return count
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func tagsEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func removeString(values []string, target string) []string {
	out := values[:0]
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}

func NormalizeUserLanguage(language string) string {
	for _, candidate := range strings.Split(language, ",") {
		normalized := strings.TrimSpace(strings.Split(candidate, ";")[0])
		normalized = strings.ToLower(normalized)
		switch {
		case normalized == "zh-tw", normalized == "zh-hk", normalized == "zh-mo", strings.Contains(normalized, "hant"):
			return "zh-TW"
		case normalized == "zh-cn", normalized == "zh-sg", strings.HasPrefix(normalized, "zh"):
			return "zh-CN"
		case strings.HasPrefix(normalized, "es"):
			return "es"
		case strings.HasPrefix(normalized, "en"):
			return "en"
		}
	}
	return "en"
}

func defaultString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func max64(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func ReadAllLimited(buf *bytes.Buffer, limit int64) ([]byte, error) {
	if int64(buf.Len()) > limit {
		return nil, E(http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds request limit")
	}
	return buf.Bytes(), nil
}
