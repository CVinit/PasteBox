package app

import "context"

type ContentStores struct {
	Pastes      PasteStore
	Attachments AttachmentStore
	Shares      ShareStore
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
}

type ShareStore interface {
	CreateShare(ctx context.Context, share Share) error
	ShareByID(ctx context.Context, id string) (Share, error)
	ShareByTokenHash(ctx context.Context, tokenHash string) (Share, error)
	ListShares(ctx context.Context) ([]Share, error)
	ListSharesByUser(ctx context.Context, userID string) ([]Share, error)
	UpdateShare(ctx context.Context, share Share) error
}
