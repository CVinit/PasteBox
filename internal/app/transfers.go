package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"pastebox/internal/plans"
)

const (
	TransferStatusDraft     = "draft"
	TransferStatusPublished = "published"
	TransferStatusCanceled  = "canceled"

	TransferItemPending  = "pending"
	TransferItemUploaded = "uploaded"

	// maxTransferItems bounds one send independently of the plan limit so a
	// single request cannot declare an unbounded item list.
	maxTransferItems = 200
)

// TransferItemUploadPreflight carries the resolved transfer/item identity
// between the streaming preflight and the prepared-upload step.
type TransferItemUploadPreflight struct {
	MaxBytes int64

	transferID string
	itemID     string
	userID     string
	pasteID    string
	guestToken string
}

func transferIdempotencyKey(userID string, key string) string {
	return userID + "\x00" + key
}

func transferItemKey(transferID string, itemID string) string {
	return transferID + "\x00" + itemID
}

func (s *Service) cacheTransferLocked(transfer Transfer) *Transfer {
	cached := transfer
	s.transfersByID[cached.ID] = &cached
	if cached.IdempotencyKey != "" {
		s.transferIDByIdemKey[transferIdempotencyKey(cached.UserID, cached.IdempotencyKey)] = cached.ID
	}
	return &cached
}

func (s *Service) createTransferLocked(ctx context.Context, transfer *Transfer) error {
	if s.content.Transfers != nil {
		stored := *transfer
		s.mu.Unlock()
		err := s.content.Transfers.CreateTransfer(ctx, stored)
		s.mu.Lock()
		if err != nil {
			if errors.Is(err, ErrStoreConflict) {
				return E(http.StatusConflict, "transfer_conflict", "transfer already exists")
			}
			return err
		}
	}
	s.cacheTransferLocked(*transfer)
	return nil
}

func (s *Service) updateTransferLocked(ctx context.Context, transfer *Transfer) error {
	if s.content.Transfers != nil {
		stored := *transfer
		s.mu.Unlock()
		err := s.content.Transfers.UpdateTransfer(ctx, stored)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cacheTransferLocked(*transfer)
	return nil
}

func (s *Service) transferByIDLocked(ctx context.Context, id string) (*Transfer, error) {
	if s.content.Transfers != nil {
		s.mu.Unlock()
		loaded, err := s.content.Transfers.TransferByID(ctx, id)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "transfer_not_found", "transfer not found")
			}
			return nil, err
		}
		return s.cacheTransferLocked(loaded), nil
	}
	transfer := s.transfersByID[id]
	if transfer == nil {
		return nil, E(http.StatusNotFound, "transfer_not_found", "transfer not found")
	}
	return transfer, nil
}

func (s *Service) ownedTransferLocked(ctx context.Context, userID string, transferID string) (*Transfer, error) {
	transfer, err := s.transferByIDLocked(ctx, transferID)
	if err != nil {
		return nil, err
	}
	if transfer.UserID != userID {
		return nil, E(http.StatusNotFound, "transfer_not_found", "transfer not found")
	}
	return transfer, nil
}

// transferByIdempotencyKeyLocked returns (nil, nil) when the key is unused.
func (s *Service) transferByIdempotencyKeyLocked(ctx context.Context, userID string, key string) (*Transfer, error) {
	if key == "" {
		return nil, nil
	}
	if s.content.Transfers != nil {
		s.mu.Unlock()
		loaded, err := s.content.Transfers.TransferByIdempotencyKey(ctx, userID, key)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return s.cacheTransferLocked(loaded), nil
	}
	transferID := s.transferIDByIdemKey[transferIdempotencyKey(userID, key)]
	if transferID == "" {
		return nil, nil
	}
	transfer := s.transfersByID[transferID]
	if transfer == nil {
		return nil, nil
	}
	return transfer, nil
}

func (s *Service) listTransfersByUserLocked(ctx context.Context, userID string) ([]Transfer, error) {
	if s.content.Transfers != nil {
		s.mu.Unlock()
		loaded, err := s.content.Transfers.ListTransfersByUser(ctx, userID)
		s.mu.Lock()
		if err != nil {
			return nil, fmt.Errorf("load user transfers: %w", err)
		}
		out := make([]Transfer, 0, len(loaded))
		for _, transfer := range loaded {
			out = append(out, *s.cacheTransferLocked(transfer))
		}
		return out, nil
	}
	out := []Transfer{}
	for _, transfer := range s.transfersByID {
		if transfer.UserID == userID {
			out = append(out, *transfer)
		}
	}
	return out, nil
}

func (s *Service) createTransferItemLocked(ctx context.Context, item *TransferItem) error {
	if s.content.Transfers != nil {
		stored := *item
		s.mu.Unlock()
		err := s.content.Transfers.CreateTransferItem(ctx, stored)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.transferItems[transferItemKey(item.TransferID, item.ItemID)] = item
	return nil
}

func (s *Service) updateTransferItemLocked(ctx context.Context, item *TransferItem) error {
	if s.content.Transfers != nil {
		stored := *item
		s.mu.Unlock()
		err := s.content.Transfers.UpdateTransferItem(ctx, stored)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.transferItems[transferItemKey(item.TransferID, item.ItemID)] = item
	return nil
}

func (s *Service) transferItemLocked(ctx context.Context, transferID string, itemID string) (*TransferItem, error) {
	if s.content.Transfers != nil {
		s.mu.Unlock()
		loaded, err := s.content.Transfers.TransferItem(ctx, transferID, itemID)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "transfer_item_not_found", "transfer item not found")
			}
			return nil, err
		}
		return &loaded, nil
	}
	item := s.transferItems[transferItemKey(transferID, itemID)]
	if item == nil {
		return nil, E(http.StatusNotFound, "transfer_item_not_found", "transfer item not found")
	}
	return item, nil
}

func (s *Service) transferItemsLocked(ctx context.Context, transferID string) ([]TransferItem, error) {
	if s.content.Transfers != nil {
		s.mu.Unlock()
		loaded, err := s.content.Transfers.ListTransferItems(ctx, transferID)
		s.mu.Lock()
		if err != nil {
			return nil, fmt.Errorf("load transfer items: %w", err)
		}
		return loaded, nil
	}
	out := []TransferItem{}
	for _, item := range s.transferItems {
		if item.TransferID == transferID {
			out = append(out, *item)
		}
	}
	return out, nil
}

func (s *Service) viewTransferLocked(ctx context.Context, transfer *Transfer) (TransferView, error) {
	items, err := s.transferItemsLocked(ctx, transfer.ID)
	if err != nil {
		return TransferView{}, err
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].ItemID < items[j].ItemID
	})
	view := TransferView{
		ID:          transfer.ID,
		Status:      transfer.Status,
		PasteID:     transfer.PasteID,
		Items:       make([]TransferItemView, 0, len(items)),
		ExpiresAt:   transfer.ExpiresAt,
		CreatedAt:   transfer.CreatedAt,
		UpdatedAt:   transfer.UpdatedAt,
		PublishedAt: transfer.PublishedAt,
		CanceledAt:  transfer.CanceledAt,
	}
	for _, item := range items {
		itemView := TransferItemView{
			ItemID:       item.ItemID,
			FileName:     item.FileName,
			ContentType:  item.ContentType,
			Size:         item.Size,
			Status:       item.Status,
			AttachmentID: item.AttachmentID,
		}
		if item.AttachmentID != "" {
			if attachment, err := s.attachmentByIDLocked(ctx, item.AttachmentID); err == nil && attachment != nil {
				itemView.FileName = attachment.FileName
				itemView.ContentType = attachment.ContentType
				itemView.Size = attachment.Size
				itemView.ScanStatus = attachment.ScanStatus
			}
		}
		view.Items = append(view.Items, itemView)
	}
	if transfer.ShareID != "" {
		share, err := s.shareByIDLocked(ctx, transfer.ShareID)
		if err == nil && share != nil {
			shareView := s.viewShareLocked(share)
			view.Share = &shareView
		}
	}
	return view, nil
}

func normalizeTransferItems(plan plans.Plan, input []TransferItemInput) ([]TransferItem, error) {
	if len(input) == 0 {
		return nil, E(http.StatusBadRequest, "transfer_items_required", "at least one file is required")
	}
	if len(input) > maxTransferItems {
		return nil, E(http.StatusRequestEntityTooLarge, "transfer_items_limit", "too many files in one transfer")
	}
	if plan.AttachmentsPerPasteLimit > 0 && len(input) > plan.AttachmentsPerPasteLimit {
		return nil, E(http.StatusForbidden, "attachment_limit", "files exceed the plan attachment limit")
	}
	seen := map[string]bool{}
	items := make([]TransferItem, 0, len(input))
	for _, raw := range input {
		itemID := strings.TrimSpace(raw.ItemID)
		if itemID == "" || len(itemID) > 128 {
			return nil, E(http.StatusBadRequest, "invalid_transfer_item", "file item id is invalid")
		}
		if seen[itemID] {
			return nil, E(http.StatusBadRequest, "duplicate_transfer_item", "file item ids must be unique")
		}
		seen[itemID] = true
		items = append(items, TransferItem{
			ItemID:      itemID,
			FileName:    sanitizeFileName(raw.FileName),
			ContentType: strings.TrimSpace(raw.ContentType),
			Size:        max64(raw.Size, 0),
			Status:      TransferItemPending,
		})
	}
	return items, nil
}

func transferPasteTitle(items []TransferItem) string {
	if len(items) == 0 {
		return ""
	}
	title := strings.TrimSpace(items[0].FileName)
	if len(items) > 1 {
		title = fmt.Sprintf("%s +%d", title, len(items)-1)
	}
	return title
}

func (s *Service) CreateTransferWithContext(ctx context.Context, userID string, input TransferInput) (TransferView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(ctx, userID)
	if err != nil {
		return TransferView{}, err
	}
	plan, err := s.planForUserLocked(user)
	if err != nil {
		return TransferView{}, err
	}
	now := s.now().UTC()
	return s.createTransferForUserLocked(ctx, user, plan, input, resolveExpiresAt(now, input.ExpiresInSeconds, plan))
}

func (s *Service) CreateGuestTransferWithContext(ctx context.Context, input GuestCreateTransferInput) (string, TransferView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.runtimeConfig.GuestUploads
	if !cfg.Enabled {
		return "", TransferView{}, E(http.StatusForbidden, "guest_uploads_disabled", "guest uploads are disabled")
	}
	if cfg.RequireTurnstile {
		if err := s.verifyTurnstileLocked(ctx, input.TurnstileToken, input.RemoteIP); err != nil {
			return "", TransferView{}, err
		}
	}
	token := strings.TrimSpace(input.Token)
	if token == "" {
		token = newToken()
	}
	user, err := s.guestUserForTokenLocked(ctx, token)
	if err != nil {
		return "", TransferView{}, err
	}
	now := s.now().UTC()
	expiresSeconds := input.ExpiresInSeconds
	if expiresSeconds <= 0 || expiresSeconds > cfg.RetentionSeconds {
		expiresSeconds = cfg.RetentionSeconds
	}
	view, err := s.createTransferForUserLocked(ctx, user, guestPlan(cfg), TransferInput{
		IdempotencyKey: input.IdempotencyKey,
		Password:       input.Password,
		Items:          input.Items,
	}, now.Add(time.Duration(expiresSeconds)*time.Second))
	if err != nil {
		return "", TransferView{}, err
	}
	return token, view, nil
}

func (s *Service) createTransferForUserLocked(ctx context.Context, user *User, plan plans.Plan, input TransferInput, expiresAt time.Time) (TransferView, error) {
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	existing, err := s.transferByIdempotencyKeyLocked(ctx, user.ID, idempotencyKey)
	if err != nil {
		return TransferView{}, err
	}
	if existing != nil {
		return s.viewTransferLocked(ctx, existing)
	}
	items, err := normalizeTransferItems(plan, input.Items)
	if err != nil {
		return TransferView{}, err
	}
	if err := s.ensureCanCreatePasteLocked(ctx, user, plan, PasteInput{}, 0, 0); err != nil {
		return TransferView{}, err
	}
	passwordHash, err := hashSharePassword(input.Password)
	if err != nil {
		return TransferView{}, err
	}
	now := s.now().UTC()
	paste := &Paste{
		ID:         s.newID("pst"),
		UserID:     user.ID,
		Title:      transferPasteTitle(items),
		Status:     "active",
		ScanStatus: "clean",
		ExpiresAt:  expiresAt,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.createPasteLocked(ctx, paste); err != nil {
		return TransferView{}, err
	}
	transfer := &Transfer{
		ID:             s.newID("trf"),
		UserID:         user.ID,
		PasteID:        paste.ID,
		Status:         TransferStatusDraft,
		IdempotencyKey: idempotencyKey,
		PasswordHash:   passwordHash,
		LoginRequired:  input.LoginRequired,
		ExpiresAt:      expiresAt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.createTransferLocked(ctx, transfer); err != nil {
		if discardErr := s.discardTransferPasteLocked(ctx, paste, now); discardErr != nil {
			return TransferView{}, errors.Join(err, discardErr)
		}
		return TransferView{}, err
	}
	for i := range items {
		item := items[i]
		item.TransferID = transfer.ID
		item.CreatedAt = now
		item.UpdatedAt = now
		if err := s.createTransferItemLocked(ctx, &item); err != nil {
			return TransferView{}, err
		}
	}
	return s.viewTransferLocked(ctx, transfer)
}

func (s *Service) GetTransferWithContext(ctx context.Context, userID string, transferID string) (TransferView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.activeUserLocked(ctx, userID); err != nil {
		return TransferView{}, err
	}
	transfer, err := s.ownedTransferLocked(ctx, userID, transferID)
	if err != nil {
		return TransferView{}, err
	}
	return s.viewTransferLocked(ctx, transfer)
}

func (s *Service) ListTransfersWithContext(ctx context.Context, userID string) ([]TransferView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.activeUserLocked(ctx, userID); err != nil {
		return nil, err
	}
	transfers, err := s.listTransfersByUserLocked(ctx, userID)
	if err != nil {
		return nil, err
	}
	sort.Slice(transfers, func(i, j int) bool {
		if !transfers[i].CreatedAt.Equal(transfers[j].CreatedAt) {
			return transfers[i].CreatedAt.After(transfers[j].CreatedAt)
		}
		return transfers[i].ID > transfers[j].ID
	})
	out := make([]TransferView, 0, len(transfers))
	for i := range transfers {
		view, err := s.viewTransferLocked(ctx, &transfers[i])
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

func (s *Service) PreflightTransferItemUploadWithContext(ctx context.Context, userID string, transferID string, itemID string) (TransferItemUploadPreflight, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	transfer, err := s.ownedTransferLocked(ctx, userID, transferID)
	if err != nil {
		return TransferItemUploadPreflight{}, err
	}
	user, err := s.activeUserLocked(ctx, userID)
	if err != nil {
		return TransferItemUploadPreflight{}, err
	}
	plan, err := s.planForUserLocked(user)
	if err != nil {
		return TransferItemUploadPreflight{}, err
	}
	return s.preflightTransferItemUploadLocked(ctx, transfer, itemID, user, plan, "")
}

func (s *Service) PreflightGuestTransferItemUpload(ctx context.Context, token string, transferID string, itemID string, turnstileToken string, remoteIP string) (TransferItemUploadPreflight, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.runtimeConfig.GuestUploads
	if !cfg.Enabled {
		return TransferItemUploadPreflight{}, E(http.StatusForbidden, "guest_uploads_disabled", "guest uploads are disabled")
	}
	if cfg.RequireTurnstile {
		if err := s.verifyTurnstileLocked(ctx, turnstileToken, remoteIP); err != nil {
			return TransferItemUploadPreflight{}, err
		}
	}
	token = strings.TrimSpace(token)
	user, err := s.guestUserForTokenLocked(ctx, token)
	if err != nil {
		return TransferItemUploadPreflight{}, err
	}
	transfer, err := s.transferByIDLocked(ctx, transferID)
	if err != nil || transfer.UserID != user.ID {
		return TransferItemUploadPreflight{}, E(http.StatusNotFound, "transfer_not_found", "transfer not found")
	}
	return s.preflightTransferItemUploadLocked(ctx, transfer, itemID, user, guestPlan(cfg), token)
}

func (s *Service) preflightTransferItemUploadLocked(ctx context.Context, transfer *Transfer, itemID string, user *User, plan plans.Plan, guestToken string) (TransferItemUploadPreflight, error) {
	// The item id is part of the request path, so an unknown item is a missing
	// resource regardless of the transfer state.
	if _, err := s.transferItemLocked(ctx, transfer.ID, itemID); err != nil {
		return TransferItemUploadPreflight{}, err
	}
	if transfer.Status != TransferStatusDraft {
		return TransferItemUploadPreflight{}, E(http.StatusConflict, "transfer_not_draft", "only draft transfers accept uploads")
	}
	if !transfer.ExpiresAt.After(s.now().UTC()) {
		return TransferItemUploadPreflight{}, E(http.StatusGone, "transfer_expired", "transfer has expired")
	}
	paste, err := s.pasteByIDLocked(ctx, transfer.PasteID)
	if err != nil {
		return TransferItemUploadPreflight{}, err
	}
	if paste.UserID != transfer.UserID {
		return TransferItemUploadPreflight{}, E(http.StatusNotFound, "transfer_not_found", "transfer not found")
	}
	maxBytes, err := s.attachmentUploadLimitLocked(ctx, user, paste, plan)
	if err != nil {
		return TransferItemUploadPreflight{}, err
	}
	return TransferItemUploadPreflight{
		MaxBytes:   maxBytes,
		transferID: transfer.ID,
		itemID:     itemID,
		userID:     user.ID,
		pasteID:    paste.ID,
		guestToken: guestToken,
	}, nil
}

// AddPreparedTransferItemWithContext stores one uploaded file for a declared
// transfer item. Retrying the same item returns the attachment that was already
// stored, so a failed request cannot duplicate an attachment or an object
// reference; retrying with different bytes is a conflict.
//
// The item lock below serializes concurrent uploads of the same item inside one
// process. Two processes uploading the same item at the same instant can still
// both store bytes; publish only ever binds one attachment per item, so the
// loser's object is reclaimed by the normal object-reference cleanup.
func (s *Service) AddPreparedTransferItemWithContext(ctx context.Context, preflight TransferItemUploadPreflight, upload *PreparedAttachmentUpload) (TransferView, AttachmentView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if preflight.transferID == "" || preflight.itemID == "" || preflight.pasteID == "" {
		return TransferView{}, AttachmentView{}, E(http.StatusBadRequest, "invalid_upload_preflight", "transfer item upload preflight is invalid")
	}
	release := s.lockObjectKey("transfer-item:" + preflight.transferID + "/" + preflight.itemID)
	defer release()

	view, attachment, done, err := s.finishTransferItemUploadLocked(ctx, preflight, upload)
	if err != nil || done {
		return view, attachment, err
	}

	var stored AttachmentView
	if preflight.guestToken != "" {
		stored, err = s.AddPreflightedGuestAttachmentWithContext(ctx, AttachmentUploadPreflight{
			guestToken: preflight.guestToken,
			pasteID:    preflight.pasteID,
		}, upload)
	} else {
		stored, err = s.AddPreparedAttachmentWithContext(ctx, preflight.userID, preflight.pasteID, upload)
	}
	if err != nil {
		return TransferView{}, AttachmentView{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	transfer, err := s.transferByIDLocked(ctx, preflight.transferID)
	if err != nil {
		return TransferView{}, AttachmentView{}, err
	}
	item, err := s.transferItemLocked(ctx, preflight.transferID, preflight.itemID)
	if err != nil {
		return TransferView{}, AttachmentView{}, err
	}
	now := s.now().UTC()
	item.AttachmentID = stored.ID
	item.FileName = stored.FileName
	item.ContentType = stored.ContentType
	item.Size = stored.Size
	item.Status = TransferItemUploaded
	item.UpdatedAt = now
	if err := s.updateTransferItemLocked(ctx, item); err != nil {
		return TransferView{}, AttachmentView{}, err
	}
	view, err = s.viewTransferLocked(ctx, transfer)
	if err != nil {
		return TransferView{}, AttachmentView{}, err
	}
	return view, stored, nil
}

// finishTransferItemUploadLocked reports done=true when the item was already
// uploaded, either returning the matching attachment or a conflict.
func (s *Service) finishTransferItemUploadLocked(ctx context.Context, preflight TransferItemUploadPreflight, upload *PreparedAttachmentUpload) (TransferView, AttachmentView, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	transfer, err := s.transferByIDLocked(ctx, preflight.transferID)
	if err != nil {
		return TransferView{}, AttachmentView{}, true, err
	}
	if transfer.Status != TransferStatusDraft {
		return TransferView{}, AttachmentView{}, true, E(http.StatusConflict, "transfer_not_draft", "only draft transfers accept uploads")
	}
	item, err := s.transferItemLocked(ctx, preflight.transferID, preflight.itemID)
	if err != nil {
		return TransferView{}, AttachmentView{}, true, err
	}
	if item.Status != TransferItemUploaded {
		return TransferView{}, AttachmentView{}, false, nil
	}
	conflict := E(http.StatusConflict, "transfer_item_conflict", "file item was already uploaded with different content")
	existing, err := s.attachmentByIDLocked(ctx, item.AttachmentID)
	if err != nil || existing == nil || existing.Size != upload.Size || existing.SHA256 != upload.SHA256 {
		return TransferView{}, AttachmentView{}, true, conflict
	}
	view, err := s.viewTransferLocked(ctx, transfer)
	if err != nil {
		return TransferView{}, AttachmentView{}, true, err
	}
	return view, viewAttachment(existing), true, nil
}

func (s *Service) PublishTransferWithContext(ctx context.Context, userID string, transferID string) (TransferView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.activeUserLocked(ctx, userID); err != nil {
		return TransferView{}, err
	}
	return s.publishTransferLocked(ctx, userID, transferID)
}

func (s *Service) PublishGuestTransferWithContext(ctx context.Context, token string, transferID string) (TransferView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.runtimeConfig.GuestUploads
	if !cfg.Enabled {
		return TransferView{}, E(http.StatusForbidden, "guest_uploads_disabled", "guest uploads are disabled")
	}
	user, err := s.guestUserForTokenLocked(ctx, strings.TrimSpace(token))
	if err != nil {
		return TransferView{}, err
	}
	return s.publishTransferLocked(ctx, user.ID, transferID)
}

func (s *Service) publishTransferLocked(ctx context.Context, userID string, transferID string) (TransferView, error) {
	transfer, err := s.ownedTransferLocked(ctx, userID, transferID)
	if err != nil {
		return TransferView{}, err
	}
	switch transfer.Status {
	case TransferStatusPublished:
		return s.viewTransferLocked(ctx, transfer)
	case TransferStatusCanceled:
		return TransferView{}, E(http.StatusGone, "transfer_canceled", "transfer was canceled")
	}
	now := s.now().UTC()
	if !transfer.ExpiresAt.After(now) {
		return TransferView{}, E(http.StatusGone, "transfer_expired", "transfer has expired")
	}
	paste, err := s.pasteByIDLocked(ctx, transfer.PasteID)
	if err != nil {
		return TransferView{}, err
	}
	if !s.isPasteVisibleLocked(paste) {
		return TransferView{}, E(http.StatusGone, "transfer_expired", "transfer has expired")
	}
	if err := s.ensureTransferCompleteLocked(ctx, transfer, paste); err != nil {
		return TransferView{}, err
	}
	shareToken := newToken()
	share := &Share{
		ID:            s.newID("shr"),
		PasteID:       paste.ID,
		UserID:        userID,
		Token:         shareToken,
		TokenHash:     tokenHash(shareToken),
		PasswordHash:  transfer.PasswordHash,
		LoginRequired: transfer.LoginRequired,
		ExpiresAt:     transfer.ExpiresAt,
		CreatedAt:     now,
	}
	if atomicStore, ok := s.content.Transfers.(AtomicTransferStore); ok {
		storedShare := *share
		s.mu.Unlock()
		updated, publishErr := atomicStore.PublishTransfer(ctx, transfer.ID, storedShare, now)
		s.mu.Lock()
		if publishErr != nil {
			if isStoreNotFound(publishErr) {
				return TransferView{}, E(http.StatusNotFound, "transfer_not_found", "transfer not found")
			}
			if errors.Is(publishErr, ErrTransferStoreCanceled) {
				return TransferView{}, E(http.StatusGone, "transfer_canceled", "transfer was canceled")
			}
			if errors.Is(publishErr, ErrStoreConflict) {
				return TransferView{}, E(http.StatusConflict, "transfer_incomplete", "every file must finish uploading before the transfer can be published")
			}
			return TransferView{}, publishErr
		}
		transfer = s.cacheTransferLocked(updated)
		// Only a committed share may enter the cache: a publish retry returns
		// the stored row without inserting this request's fresh credentials.
		if updated.ShareID != "" {
			_, _ = s.shareByIDLocked(ctx, updated.ShareID)
		}
		return s.viewTransferLocked(ctx, transfer)
	}
	if err := s.createShareLocked(ctx, share); err != nil {
		return TransferView{}, err
	}
	transfer.Status = TransferStatusPublished
	transfer.ShareID = share.ID
	transfer.PublishedAt = &now
	transfer.UpdatedAt = now
	if err := s.updateTransferLocked(ctx, transfer); err != nil {
		return TransferView{}, err
	}
	return s.viewTransferLocked(ctx, transfer)
}

func (s *Service) ensureTransferCompleteLocked(ctx context.Context, transfer *Transfer, paste *Paste) error {
	items, err := s.transferItemsLocked(ctx, transfer.ID)
	if err != nil {
		return err
	}
	incomplete := E(http.StatusConflict, "transfer_incomplete", "every file must finish uploading before the transfer can be published")
	if len(items) == 0 {
		return incomplete
	}
	for _, item := range items {
		if item.Status != TransferItemUploaded || item.AttachmentID == "" {
			return incomplete
		}
		attachment, err := s.attachmentByIDLocked(ctx, item.AttachmentID)
		if err != nil || attachment == nil || attachment.PasteID != paste.ID || attachment.Status != "active" {
			return incomplete
		}
		if attachment.ScanStatus == "malicious" {
			return E(http.StatusForbidden, "malicious_file", "known malicious files cannot be shared")
		}
	}
	return nil
}

func (s *Service) CancelTransferWithContext(ctx context.Context, userID string, transferID string) (TransferView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.activeUserLocked(ctx, userID); err != nil {
		return TransferView{}, err
	}
	return s.cancelTransferLocked(ctx, userID, transferID)
}

func (s *Service) CancelGuestTransferWithContext(ctx context.Context, token string, transferID string) (TransferView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.runtimeConfig.GuestUploads
	if !cfg.Enabled {
		return TransferView{}, E(http.StatusForbidden, "guest_uploads_disabled", "guest uploads are disabled")
	}
	user, err := s.guestUserForTokenLocked(ctx, strings.TrimSpace(token))
	if err != nil {
		return TransferView{}, err
	}
	return s.cancelTransferLocked(ctx, user.ID, transferID)
}

// cancelTransferLocked drops an unfinished transfer and schedules its backing
// content for cleanup, so an abandoned upload stops holding quota. Repeating
// the call is a no-op. Published transfers are not cancelable here: their share
// is revoked through the existing share controls and their content is only
// destroyed by an explicit destruction policy.
func (s *Service) cancelTransferLocked(ctx context.Context, userID string, transferID string) (TransferView, error) {
	transfer, err := s.ownedTransferLocked(ctx, userID, transferID)
	if err != nil {
		return TransferView{}, err
	}
	switch transfer.Status {
	case TransferStatusCanceled:
		return s.viewTransferLocked(ctx, transfer)
	case TransferStatusPublished:
		return TransferView{}, E(http.StatusConflict, "transfer_not_draft", "published transfers cannot be canceled")
	}
	now := s.now().UTC()
	paste, err := s.pasteByIDLocked(ctx, transfer.PasteID)
	if err != nil {
		return TransferView{}, err
	}
	if err := s.discardTransferPasteLocked(ctx, paste, now); err != nil {
		return TransferView{}, err
	}
	transfer.Status = TransferStatusCanceled
	transfer.CanceledAt = &now
	transfer.UpdatedAt = now
	if err := s.updateTransferLocked(ctx, transfer); err != nil {
		return TransferView{}, err
	}
	return s.viewTransferLocked(ctx, transfer)
}

// discardTransferPasteLocked marks an unfinished transfer's paste and its
// attachments for deletion and schedules the existing cleanup worker. Object
// bytes are released by the cleanup path, which keeps reference counting for
// content-addressed objects intact.
func (s *Service) discardTransferPasteLocked(ctx context.Context, paste *Paste, now time.Time) error {
	if paste.Status == "active" {
		paste.Status = "pending_delete"
		paste.UpdatedAt = now
		if err := s.updatePasteLocked(ctx, paste); err != nil {
			return err
		}
		for _, attachment := range s.attachmentsForPasteLocked(paste) {
			if attachment.Status != "active" {
				continue
			}
			attachment.Status = "pending_delete"
			if err := s.updateAttachmentLocked(ctx, attachment); err != nil {
				return err
			}
		}
		if err := s.scheduleCleanupJobLocked(ctx, paste.ID, now); err != nil {
			return err
		}
	}
	return nil
}
