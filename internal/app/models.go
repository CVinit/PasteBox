package app

import (
	"pastebox/internal/plans"
	"time"
)

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
	Limit  int
	Offset int
}
