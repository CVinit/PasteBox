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

type SharesByPasteStore interface {
	ListSharesByPaste(ctx context.Context, pasteID string) ([]Share, error)
}

type AtomicShareStore interface {
	ConsumeShareVisit(ctx context.Context, shareID string, now time.Time) (Share, error)
	ConsumeShareDownload(ctx context.Context, shareID string, attachmentID string, userID string, dailyLimit int64, now time.Time) (Share, Attachment, error)
}
