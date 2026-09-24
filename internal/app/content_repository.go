package app

import (
	"context"
	"errors"
	"net/http"
	"time"
)

func (s *Service) createPasteLocked(ctx context.Context, paste *Paste) error {
	if s.content.Pastes != nil {
		storedPaste := *paste
		s.mu.Unlock()
		err := s.content.Pastes.CreatePaste(ctx, storedPaste)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cachePasteLocked(*paste)
	return nil
}

func (s *Service) updatePasteLocked(ctx context.Context, paste *Paste) error {
	if s.content.Pastes != nil {
		storedPaste := *paste
		s.mu.Unlock()
		err := s.content.Pastes.UpdatePaste(ctx, storedPaste)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cachePasteLocked(*paste)
	return nil
}

func (s *Service) pasteByIDLocked(ctx context.Context, id string) (*Paste, error) {

	if s.content.Pastes != nil {
		// Publish the paste and its attachments together, after all reads finish.
		// Otherwise another lookup can replace the cached paste during a slow query.
		s.mu.Unlock()
		loaded, err := s.content.Pastes.PasteByID(ctx, id)
		var attachments []Attachment
		var shares []Share
		if err == nil && s.content.Attachments != nil {
			attachments, err = s.content.Attachments.ListAttachmentsByPaste(ctx, id)
		}
		if store, ok := s.content.Shares.(SharesByPasteStore); err == nil && ok {
			shares, err = store.ListSharesByPaste(ctx, id)
		}
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "paste_not_found", "paste not found")
			}
			return nil, err
		}
		paste := s.cachePasteLocked(loaded)
		if s.content.Attachments != nil {
			paste.AttachmentIDs = nil
		}
		for _, attachment := range attachments {
			s.cacheAttachmentLocked(attachment)
		}
		for _, share := range shares {
			s.cacheShareLocked(share)
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

func (s *Service) createAttachmentLocked(ctx context.Context, attachment *Attachment) error {
	if s.content.Attachments != nil {
		storedAttachment := *attachment
		s.mu.Unlock()
		err := s.content.Attachments.CreateAttachment(ctx, storedAttachment)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cacheAttachmentLocked(*attachment)
	return nil
}

func (s *Service) updateAttachmentLocked(ctx context.Context, attachment *Attachment) error {
	if s.content.Attachments != nil {
		storedAttachment := *attachment
		s.mu.Unlock()
		err := s.content.Attachments.UpdateAttachment(ctx, storedAttachment)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cacheAttachmentLocked(*attachment)
	return nil
}

func (s *Service) deleteAttachmentLocked(ctx context.Context, attachment *Attachment) error {
	if s.content.Attachments != nil {
		s.mu.Unlock()
		err := s.content.Attachments.DeleteAttachment(ctx, attachment.ID)
		s.mu.Lock()
		if err != nil && !isStoreNotFound(err) {
			return err
		}
	}
	delete(s.attachmentsByID, attachment.ID)
	if paste := s.pastesByID[attachment.PasteID]; paste != nil {
		paste.AttachmentIDs = removeString(paste.AttachmentIDs, attachment.ID)
	}
	return nil
}

func (s *Service) attachmentByIDLocked(ctx context.Context, id string) (*Attachment, error) {
	if s.content.Attachments != nil {
		s.mu.Unlock()
		loaded, err := s.content.Attachments.AttachmentByID(ctx, id)
		s.mu.Lock()
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

func (s *Service) rebuildObjectRefsLocked(ctx context.Context) error {
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
	if _, atomic := s.content.ObjectRefs.(AtomicObjectRefStore); atomic {
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
		if err := s.persistObjectRefLocked(ctx, attachment, refs, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) incrementObjectRefLocked(ctx context.Context, attachment *Attachment, previousRefs int, now time.Time) error {
	if attachment.ObjectKey == "" {
		return nil
	}
	if atomicStore, ok := s.content.ObjectRefs.(AtomicObjectRefStore); ok {
		input := ObjectRef{
			ObjectKey: attachment.ObjectKey,
			Size:      attachment.Size,
			SHA256:    attachment.SHA256,
			CreatedAt: attachment.CreatedAt,
			UpdatedAt: now,
		}
		s.mu.Unlock()
		ref, err := atomicStore.IncrementObjectRef(ctx, input)
		s.mu.Lock()
		if err != nil {
			return err
		}
		s.objectRefs[attachment.ObjectKey] = ref.RefCount
		return nil
	}
	nextRefs := previousRefs + 1
	if err := s.persistObjectRefLocked(ctx, attachment, nextRefs, now); err != nil {
		return err
	}
	s.objectRefs[attachment.ObjectKey] = nextRefs
	return nil
}

func (s *Service) decrementObjectRefLocked(ctx context.Context, attachment *Attachment) error {
	if attachment.ObjectKey == "" {
		return nil
	}
	if atomicStore, ok := s.content.ObjectRefs.(AtomicObjectRefStore); ok {
		if coordinator, coordinated := s.content.ObjectRefs.(ObjectRefCoordinator); coordinated {
			snapshot := *attachment
			attachment = &snapshot
			var ref ObjectRef
			var removed bool
			// Uploads acquire the object lock before publishing their cache entry.
			// Waiting for that lock while holding mu would invert the lock order.
			s.mu.Unlock()
			err := coordinator.WithObjectRefLock(ctx, attachment.ObjectKey, func(lockCtx context.Context, lockedStore AtomicObjectRefStore) error {
				var err error
				ref, removed, err = lockedStore.DecrementObjectRef(lockCtx, attachment.ObjectKey)
				if err != nil || !removed {
					return err
				}
				if s.objectStore != nil {
					if err := s.deleteObjectWithContext(lockCtx, attachment.ObjectKey); err != nil {
						restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(lockCtx), 5*time.Second)
						defer cancel()
						_, restoreErr := lockedStore.IncrementObjectRef(restoreCtx, ObjectRef{
							ObjectKey: attachment.ObjectKey,
							Size:      attachment.Size,
							SHA256:    attachment.SHA256,
							CreatedAt: attachment.CreatedAt,
							UpdatedAt: s.now().UTC(),
						})
						return errors.Join(err, restoreErr)
					}
				}
				return nil
			})
			s.mu.Lock()
			if err != nil {
				return err
			}
			if removed {
				delete(s.objectRefs, attachment.ObjectKey)
				delete(s.objects, attachment.ObjectKey)
			} else {
				s.objectRefs[attachment.ObjectKey] = ref.RefCount
			}
			return nil
		}
		key := attachment.ObjectKey
		s.mu.Unlock()
		ref, removed, err := atomicStore.DecrementObjectRef(ctx, key)
		s.mu.Lock()
		if err != nil {
			return err
		}
		if removed {
			if err := s.deleteObjectLocked(ctx, attachment.ObjectKey); err != nil {
				_, restoreErr := atomicStore.IncrementObjectRef(ctx, ObjectRef{
					ObjectKey: attachment.ObjectKey,
					Size:      attachment.Size,
					SHA256:    attachment.SHA256,
					CreatedAt: attachment.CreatedAt,
					UpdatedAt: s.now().UTC(),
				})
				if restoreErr == nil {
					s.objectRefs[attachment.ObjectKey] = 1
				}
				return errors.Join(err, restoreErr)
			}
			delete(s.objectRefs, attachment.ObjectKey)
			return nil
		}
		s.objectRefs[attachment.ObjectKey] = ref.RefCount
		return nil
	}
	currentRefs := s.objectRefs[attachment.ObjectKey]
	nextRefs := currentRefs - 1
	if nextRefs > 0 {
		if err := s.persistObjectRefLocked(ctx, attachment, nextRefs, s.now().UTC()); err != nil {
			return err
		}
		s.objectRefs[attachment.ObjectKey] = nextRefs
		return nil
	}
	if s.content.ObjectRefs != nil {
		if err := s.deleteObjectLocked(ctx, attachment.ObjectKey); err != nil {
			return err
		}
		if err := s.content.ObjectRefs.DeleteObjectRef(ctx, attachment.ObjectKey); err != nil && !isStoreNotFound(err) {
			return err
		}
	} else {
		if err := s.deleteObjectLocked(ctx, attachment.ObjectKey); err != nil {
			return err
		}
	}
	delete(s.objectRefs, attachment.ObjectKey)
	return nil
}

func (s *Service) persistObjectRefLocked(ctx context.Context, attachment *Attachment, refs int, now time.Time) error {
	if s.content.ObjectRefs == nil || attachment.ObjectKey == "" {
		return nil
	}
	if _, atomic := s.content.ObjectRefs.(AtomicObjectRefStore); atomic {
		return nil
	}
	snapshot := *attachment
	attachment = &snapshot
	s.mu.Unlock()
	defer s.mu.Lock()
	if refs <= 0 {
		if err := s.content.ObjectRefs.DeleteObjectRef(ctx, attachment.ObjectKey); err != nil && !isStoreNotFound(err) {
			return err
		}
		return nil
	}
	createdAt := attachment.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	return s.content.ObjectRefs.UpsertObjectRef(ctx, ObjectRef{
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

func (s *Service) restoreObjectRefAfterCleanupFailureLocked(ctx context.Context, attachment *Attachment, refs int, now time.Time) error {
	if attachment.ObjectKey == "" {
		return nil
	}
	if atomicStore, ok := s.content.ObjectRefs.(AtomicObjectRefStore); ok {
		input := ObjectRef{
			ObjectKey: attachment.ObjectKey,
			Size:      attachment.Size,
			SHA256:    attachment.SHA256,
			CreatedAt: attachment.CreatedAt,
			UpdatedAt: now,
		}
		s.mu.Unlock()
		ref, err := atomicStore.IncrementObjectRef(ctx, input)
		s.mu.Lock()
		if err != nil {
			return err
		}
		s.objectRefs[attachment.ObjectKey] = ref.RefCount
		return nil
	}
	if refs <= 0 {
		delete(s.objectRefs, attachment.ObjectKey)
		return nil
	}
	s.objectRefs[attachment.ObjectKey] = refs
	return s.persistObjectRefLocked(ctx, attachment, refs, now)
}

func (s *Service) createShareLocked(ctx context.Context, share *Share) error {
	if s.content.Shares != nil {
		storedShare := *share
		s.mu.Unlock()
		err := s.content.Shares.CreateShare(ctx, storedShare)
		s.mu.Lock()
		if err != nil {
			if errors.Is(err, ErrStoreConflict) {
				return E(http.StatusConflict, "share_token_conflict", "share token already exists")
			}
			return err
		}
	}
	s.cacheShareLocked(*share)
	return nil
}

func (s *Service) updateShareLocked(ctx context.Context, share *Share) error {
	if s.content.Shares != nil {
		storedShare := *share
		s.mu.Unlock()
		err := s.content.Shares.UpdateShare(ctx, storedShare)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cacheShareLocked(*share)
	return nil
}

func (s *Service) shareByIDLocked(ctx context.Context, id string) (*Share, error) {
	if s.content.Shares != nil {
		s.mu.Unlock()
		loaded, err := s.content.Shares.ShareByID(ctx, id)
		s.mu.Lock()
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

func (s *Service) shareByTokenHashLocked(ctx context.Context, tokenHash string) (*Share, error) {
	if s.content.Shares != nil {
		s.mu.Unlock()
		loaded, err := s.content.Shares.ShareByTokenHash(ctx, tokenHash)
		s.mu.Lock()
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
