package app

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrObjectNotFound = errors.New("object not found")

type ContentStores struct {
	Pastes      PasteStore
	Attachments AttachmentStore
	ObjectRefs  ObjectRefStore
	Shares      ShareStore
	Transfers   TransferStore
	// PickupAttempts is the shared pickup-code guess counter. It is optional:
	// without it the service keeps a process-local counter, which is only
	// correct for a single API instance.
	PickupAttempts PickupAttemptStore
}

type ObjectStore interface {
	PutObject(ctx context.Context, key string, content []byte, contentType string) error
	GetObject(ctx context.Context, key string) ([]byte, error)
	DeleteObject(ctx context.Context, key string) error
}

type ObjectStream struct {
	Body        io.ReadCloser
	Size        int64
	ContentType string
}

type StreamingObjectStore interface {
	PutObjectStream(ctx context.Context, key string, content io.Reader, size int64, contentType string) error
	OpenObject(ctx context.Context, key string) (ObjectStream, error)
}

type ScanResult struct {
	Status string
	Risk   string
}

type Scanner interface {
	Scan(ctx context.Context, fileName string, contentType string, content []byte) (ScanResult, error)
}

// StreamingScanner avoids buffering large attachment objects in worker memory.
// Legacy Scanner implementations remain supported for tests and small local fixtures.
type StreamingScanner interface {
	ScanStream(ctx context.Context, fileName string, contentType string, content io.Reader, size int64) (ScanResult, error)
}

type PasteStore interface {
	CreatePaste(ctx context.Context, paste Paste) error
	PasteByID(ctx context.Context, id string) (Paste, error)
	ListPastes(ctx context.Context) ([]Paste, error)
	ListPastesByUser(ctx context.Context, userID string) ([]Paste, error)
	UpdatePaste(ctx context.Context, paste Paste) error
}

type PagedPasteStore interface {
	ListPastesByUserPage(ctx context.Context, userID string, limit int, offset int) ([]Paste, error)
}

type PagedAllPasteStore interface {
	ListPastesPage(ctx context.Context, limit int, offset int) ([]Paste, error)
}

type CleanupPasteStore interface {
	ListPastesForCleanup(ctx context.Context, limit int) ([]Paste, error)
}

type CleanupCoordinator interface {
	WithCleanupLock(ctx context.Context, fn func(context.Context) error) error
}

type UserContentMetrics struct {
	ActivePasteCount   int
	ActiveStorageBytes int64
}

type UserContentMetricsStore interface {
	UserContentMetrics(ctx context.Context, userID string) (UserContentMetrics, error)
}

// FilteredPagedPasteStore applies the list filters before database pagination.
// Stores that do not implement it fall back to a user-scoped full list so
// filters do not silently skip matching records.
type FilteredPagedPasteStore interface {
	ListPastesByUserPageWithOptions(ctx context.Context, userID string, opts ListOptions, limit int, offset int) ([]Paste, error)
}

type AttachmentStore interface {
	CreateAttachment(ctx context.Context, attachment Attachment) error
	AttachmentByID(ctx context.Context, id string) (Attachment, error)
	ListAttachments(ctx context.Context) ([]Attachment, error)
	ListAttachmentsByPaste(ctx context.Context, pasteID string) ([]Attachment, error)
	UpdateAttachment(ctx context.Context, attachment Attachment) error
	DeleteAttachment(ctx context.Context, id string) error
}

type PagedAttachmentStore interface {
	ListAttachmentsPage(ctx context.Context, query string, limit int, offset int) ([]Attachment, error)
}

// AtomicAttachmentDownloadStore increments a durable download counter without
// reading and writing the whole attachment record in the service cache.
type AtomicAttachmentDownloadStore interface {
	IncrementAttachmentDownload(ctx context.Context, id string) (Attachment, error)
}

type ObjectRef struct {
	ObjectKey string
	RefCount  int
	Size      int64
	SHA256    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ObjectRefStore interface {
	UpsertObjectRef(ctx context.Context, ref ObjectRef) error
	DeleteObjectRef(ctx context.Context, objectKey string) error
}

type AtomicObjectRefStore interface {
	IncrementObjectRef(ctx context.Context, ref ObjectRef) (ObjectRef, error)
	DecrementObjectRef(ctx context.Context, objectKey string) (ObjectRef, bool, error)
}

// ObjectRefCoordinator keeps an object-key lock while the caller updates the
// durable reference and the object store. PostgreSQL implementations use a
// database-backed lock so separate API and worker processes coordinate too.
type ObjectRefCoordinator interface {
	WithObjectRefLock(ctx context.Context, objectKey string, fn func(context.Context, AtomicObjectRefStore) error) error
}

type ShareStore interface {
	CreateShare(ctx context.Context, share Share) error
	ShareByID(ctx context.Context, id string) (Share, error)
	ShareByTokenHash(ctx context.Context, tokenHash string) (Share, error)
	ListShares(ctx context.Context) ([]Share, error)
	ListSharesByUser(ctx context.Context, userID string) ([]Share, error)
	UpdateShare(ctx context.Context, share Share) error
}

type PagedShareStore interface {
	ListSharesPage(ctx context.Context, limit int, offset int) ([]Share, error)
}

// PickupCodeShareStore resolves the 6-character pickup code of a share. Stores
// that do not implement it leave pickup codes unresolvable instead of guessing
// from a stale cache.
type PickupCodeShareStore interface {
	ShareByPickupCode(ctx context.Context, code string) (Share, error)
}

// PickupAttemptWindow is the counter state of one pickup-code client key: when
// its window started, and how many failed guesses landed in it.
type PickupAttemptWindow struct {
	Start time.Time
	Count int
}

// PickupAttemptStore counts failed pickup-code guesses per client key and
// window. The counter must be shared by every API instance, so a guesser cannot
// multiply their budget by spreading attempts across processes. Stores only
// count; how many guesses a client gets stays a service-level decision.
type PickupAttemptStore interface {
	PickupAttemptCount(ctx context.Context, key string, window time.Duration, now time.Time) (PickupAttemptWindow, error)
	RecordPickupFailure(ctx context.Context, key string, window time.Duration, now time.Time) (PickupAttemptWindow, error)
}

type SharesByPasteStore interface {
	ListSharesByPaste(ctx context.Context, pasteID string) ([]Share, error)
}

type AtomicShareStore interface {
	ConsumeShareVisit(ctx context.Context, shareID string, now time.Time) (Share, error)
	ConsumeShareDownload(ctx context.Context, shareID string, attachmentID string, userID string, dailyLimit int64, now time.Time) (Share, Attachment, error)
}

// ErrTransferStoreCanceled lets a store report that a publish lost to a
// concurrent cancel. It also satisfies ErrStoreConflict so callers that only
// care about the conflict still match it.
var ErrTransferStoreCanceled = errors.Join(errors.New("transfer was canceled"), ErrStoreConflict)

type TransferStore interface {
	CreateTransfer(ctx context.Context, transfer Transfer) error
	TransferByID(ctx context.Context, id string) (Transfer, error)
	TransferByIdempotencyKey(ctx context.Context, userID string, key string) (Transfer, error)
	ListTransfersByUser(ctx context.Context, userID string) ([]Transfer, error)
	UpdateTransfer(ctx context.Context, transfer Transfer) error

	CreateTransferItem(ctx context.Context, item TransferItem) error
	TransferItem(ctx context.Context, transferID string, itemID string) (TransferItem, error)
	ListTransferItems(ctx context.Context, transferID string) ([]TransferItem, error)
	UpdateTransferItem(ctx context.Context, item TransferItem) error
}

// AtomicTransferStore publishes a transfer and its share in a single
// transaction. Concurrent publish retries must not mint two shares for the
// same transfer, and a transfer with unfinished items must never be published.
//
// allowNoItems carries the service's decision that this transfer has content
// even though it declares no items (a text send), so the rule about what counts
// as content stays in the service instead of being re-implemented here.
type AtomicTransferStore interface {
	PublishTransfer(ctx context.Context, transferID string, share Share, now time.Time, allowNoItems bool) (Transfer, error)
}
