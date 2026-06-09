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

type PasteStore interface {
	CreatePaste(ctx context.Context, paste Paste) error
	PasteByID(ctx context.Context, id string) (Paste, error)
	ListPastes(ctx context.Context) ([]Paste, error)
	ListPastesByUser(ctx context.Context, userID string) ([]Paste, error)
	UpdatePaste(ctx context.Context, paste Paste) error
}

type AttachmentStore interface {
	CreateAttachment(ctx context.Context, attachment Attachment) error
	AttachmentByID(ctx context.Context, id string) (Attachment, error)
	ListAttachments(ctx context.Context) ([]Attachment, error)
	ListAttachmentsByPaste(ctx context.Context, pasteID string) ([]Attachment, error)
	UpdateAttachment(ctx context.Context, attachment Attachment) error
	DeleteAttachment(ctx context.Context, id string) error
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

type ShareStore interface {
	CreateShare(ctx context.Context, share Share) error
	ShareByID(ctx context.Context, id string) (Share, error)
	ShareByTokenHash(ctx context.Context, tokenHash string) (Share, error)
	ListShares(ctx context.Context) ([]Share, error)
	ListSharesByUser(ctx context.Context, userID string) ([]Share, error)
	UpdateShare(ctx context.Context, share Share) error
}
