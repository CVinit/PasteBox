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
	ID        string
	PasteID   string
	UserID    string
	TokenHash string
	Token     string
	// PickupCode is the 6-character code a recipient can type instead of the
	// share link. It is empty for shares that were never published as a
	// transfer, and those shares stay link-only.
	PickupCode        string
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

// Transfer is one send: a batch of files that share a single share link and
// expiry. The files themselves live in the dedicated paste referenced by
// PasteID, so quota, scanning and object-reference accounting stay in the
// existing content modules.
type Transfer struct {
	ID             string
	UserID         string
	PasteID        string
	Status         string
	IdempotencyKey string
	ShareID        string
	PasswordHash   string
	LoginRequired  bool
	// ClaimQuota is how many anonymous claim slots the sender granted. It is at
	// least one, so every published transfer is claimed before its content is
	// read or downloaded.
	ClaimQuota int
	// ClaimedCount is how many slots have been spent. It is maintained together
	// with the claim rows so concurrent claims cannot oversell the quota.
	ClaimedCount int
	// BurnAfterReading destroys this send's content once nobody can be handed
	// it any more: every claim slot was spent and every session ended, or the
	// share lifetime is over. It is off by default and is only chosen at send
	// time, so an older send never starts destroying itself.
	BurnAfterReading bool
	ExpiresAt        time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	PublishedAt      *time.Time
	CanceledAt       *time.Time
	// DestroyedAt is when the send entered its terminal state. Access is
	// already refused at that point; the bytes are released afterwards.
	DestroyedAt *time.Time
	// DestroyReason names what ended the send, so the sender's view can say
	// whether the claims ran out or the lifetime did.
	DestroyReason string
}

// TransferClaim is one anonymous claim of a published transfer. Claiming spends
// one slot from the transfer quota; a file claim also opens a bounded session
// that covers every file of the batch, while a text claim only stays replayable
// for a short recovery window.
type TransferClaim struct {
	ID          string
	TransferID  string
	ShareID     string
	Kind        string
	OperationID string
	Token       string
	TokenHash   string
	Status      string
	ExpiresAt   time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
}

type TransferItem struct {
	TransferID   string
	ItemID       string
	FileName     string
	ContentType  string
	Size         int64
	AttachmentID string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type TransferItemView struct {
	ItemID       string `json:"itemId"`
	FileName     string `json:"fileName"`
	ContentType  string `json:"contentType"`
	Size         int64  `json:"size"`
	Status       string `json:"status"`
	AttachmentID string `json:"attachmentId,omitempty"`
	ScanStatus   string `json:"scanStatus,omitempty"`
}

type TransferView struct {
	ID      string             `json:"id"`
	Status  string             `json:"status"`
	PasteID string             `json:"pasteId"`
	Items   []TransferItemView `json:"items"`
	Share   *ShareView         `json:"share,omitempty"`
	// PickupCode is the code for the published share. Transfer views are only
	// served to the sender, so a recipient never receives the code here.
	PickupCode string `json:"pickupCode,omitempty"`
	// ClaimQuota and ClaimedCount let the sender see how many anonymous claims
	// the transfer allows and how many have been spent.
	ClaimQuota   int `json:"claimQuota"`
	ClaimedCount int `json:"claimedCount"`
	// BurnAfterReading tells the sender whether this send destroys itself, and
	// DestroyedAt/DestroyReason report the terminal state once it has.
	BurnAfterReading bool       `json:"burnAfterReading"`
	DestroyedAt      *time.Time `json:"destroyedAt,omitempty"`
	DestroyReason    string     `json:"destroyReason,omitempty"`
	// CleanupStatus is how far the background cleanup has got: the content is
	// marked for deletion first, so access is already gone while the bytes are
	// still being released.
	CleanupStatus string     `json:"cleanupStatus,omitempty"`
	ExpiresAt     time.Time  `json:"expiresAt"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	PublishedAt   *time.Time `json:"publishedAt,omitempty"`
	CanceledAt    *time.Time `json:"canceledAt,omitempty"`
}

// TransferClaimView is the claim state a recipient sees. It never carries
// content: a text body is returned by the claim itself, not by status reads.
type TransferClaimView struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// TransferClaimResult is the answer to an active claim. ClaimToken is the
// credential that authorizes downloads of the claimed session; Text is present
// for a text transfer and is the only place the body is handed out.
type TransferClaimResult struct {
	Claim      TransferClaimView `json:"claim"`
	ClaimToken string            `json:"claimToken,omitempty"`
	Text       string            `json:"text,omitempty"`
}

// TransferAccessView describes the claim state of a transfer-backed share to a
// recipient. Claimed reports whether this caller already holds a live claim, so
// a page reload keeps the session it paid for instead of asking for a new one.
type TransferAccessView struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	ClaimQuota      int    `json:"claimQuota"`
	ClaimedCount    int    `json:"claimedCount"`
	ClaimsRemaining int    `json:"claimsRemaining"`
	Claimed         bool   `json:"claimed"`
	// ClaimID is the claim this caller holds, so a reloaded page can still end
	// its own session. The claim credential stays in the cookie.
	ClaimID        string     `json:"claimId,omitempty"`
	ClaimExpiresAt *time.Time `json:"claimExpiresAt,omitempty"`
	ExpiresAt      time.Time  `json:"expiresAt"`
}

type TransferItemInput struct {
	ItemID      string `json:"itemId"`
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
}

type TransferInput struct {
	IdempotencyKey   string
	ExpiresInSeconds int64
	Password         string
	LoginRequired    bool
	// ClaimQuota is how many anonymous claims the send grants. Zero means the
	// default of one, so a send is never left without a way to be claimed.
	ClaimQuota int
	// BurnAfterReading asks the service to destroy this send once it can no
	// longer be handed to anybody. False keeps the ordinary expiry-only
	// lifetime, which is what every older send keeps.
	BurnAfterReading bool
	// Title, Text and Tags describe what the send carries. A send with items is
	// a file send and its title falls back to the declared file names; a send
	// with text and no items is a text send.
	Title string
	Text  string
	Tags  []string
	Items []TransferItemInput
}

type GuestCreateTransferInput struct {
	Token            string
	TurnstileToken   string
	RemoteIP         string
	IdempotencyKey   string
	ExpiresInSeconds int64
	Password         string
	// LoginRequired is refused for guests, matching guest shares: a guest send
	// has no account to check against, so accepting it would promise a privacy
	// setting the service cannot enforce.
	LoginRequired bool
	ClaimQuota    int
	// BurnAfterReading is the same destructive switch an account send offers.
	// A guest send is destroyed on the same conditions; the switch never turns
	// itself on, so a guest who does not ask for it keeps the ordinary
	// retention window.
	BurnAfterReading bool
	Title            string
	Text             string
	Items            []TransferItemInput
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
