package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"
)

func (s *Service) CreateShareWithContext(ctx context.Context, userID string, pasteID string, input ShareInput) (ShareView, error) {
	passwordHash, err := hashSharePassword(input.Password)
	if err != nil {
		return ShareView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(ctx, userID, pasteID)
	if err != nil {
		return ShareView{}, err
	}
	return s.createShareForPasteLocked(ctx, userID, paste, input, passwordHash)
}

func (s *Service) createShareForPasteLocked(ctx context.Context, userID string, paste *Paste, input ShareInput, passwordHash string) (ShareView, error) {
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
		PasswordHash:     passwordHash,
		LoginRequired:    input.LoginRequired,
		MaxVisits:        max(input.MaxVisits, 0),
		MaxDownloads:     max(input.MaxDownloads, 0),
		ExpiresAt:        expiresAt,
		CreatedAt:        now,
		LastVisitedAt:    nil,
		LastDownloadedAt: nil,
	}
	if err := s.createShareLocked(ctx, share); err != nil {
		return ShareView{}, err
	}
	return s.viewShareLocked(share), nil
}

func (s *Service) ListShares(userID string) ([]ShareView, error) {
	return s.ListSharesWithContext(context.Background(), userID)
}

func (s *Service) ListSharesWithContext(ctx context.Context, userID string) ([]ShareView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.content.Shares != nil {
		if _, err := s.activeUserWithContext(ctx, userID); err != nil {
			return nil, err
		}
		shares, err := s.content.Shares.ListSharesByUser(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("load user shares: %w", err)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		out := make([]ShareView, 0, len(shares))
		for _, share := range shares {
			out = append(out, s.viewShareLocked(s.cacheShareLocked(share)))
		}
		sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
		return out, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.activeUserLocked(ctx, userID); err != nil {
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

func (s *Service) RevokeShareWithContext(ctx context.Context, userID string, shareID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	share, err := s.shareByIDLocked(ctx, shareID)
	if err != nil || share.UserID != userID {
		return E(http.StatusNotFound, "share_not_found", "share not found")
	}
	now := s.now().UTC()
	share.RevokedAt = &now
	if err := s.updateShareLocked(ctx, share); err != nil {
		return err
	}
	return nil
}

func (s *Service) AccessShare(token string, password string, viewerUserID string) (PasteView, ShareView, error) {
	return s.AccessShareWithContext(context.Background(), token, password, viewerUserID)
}

func (s *Service) AccessShareWithContext(ctx context.Context, token string, password string, viewerUserID string) (PasteView, ShareView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	passwordVerified, err := s.verifySharePasswordForAccess(ctx, token, password)
	if err != nil {
		return PasteView{}, ShareView{}, err
	}

	s.mu.Lock()
	share, paste, err := s.validShareAccessLocked(ctx, token, password, viewerUserID, false, passwordVerified)
	if err != nil {
		s.mu.Unlock()
		return PasteView{}, ShareView{}, err
	}
	if atomicStore, ok := s.content.Shares.(AtomicShareStore); ok {
		shareID := share.ID
		now := s.now().UTC()
		s.mu.Unlock()
		consumed, consumeErr := atomicStore.ConsumeShareVisit(ctx, shareID, now)
		if consumeErr != nil {
			if isStoreNotFound(consumeErr) {
				return PasteView{}, ShareView{}, E(http.StatusNotFound, "share_not_found", "share not found")
			}
			return PasteView{}, ShareView{}, consumeErr
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		share = s.cacheShareLocked(consumed)
		return s.viewPasteLocked(paste), s.viewShareLocked(share), nil
	}
	defer s.mu.Unlock()
	now := s.now().UTC()
	share.VisitCount++
	share.LastVisitedAt = &now
	if err := s.updateShareLocked(ctx, share); err != nil {
		return PasteView{}, ShareView{}, err
	}
	return s.viewPasteLocked(paste), s.viewShareLocked(share), nil
}

func (s *Service) DownloadSharedAttachment(token string, password string, attachmentID string, viewerUserID string) (AttachmentView, []byte, error) {
	return s.DownloadSharedAttachmentWithContext(context.Background(), token, password, attachmentID, viewerUserID)
}

func (s *Service) DownloadSharedAttachmentWithContext(ctx context.Context, token string, password string, attachmentID string, viewerUserID string) (AttachmentView, []byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	passwordVerified, err := s.verifySharePasswordForAccess(ctx, token, password)
	if err != nil {
		return AttachmentView{}, nil, err
	}

	s.mu.Lock()
	share, paste, err := s.validShareAccessLocked(ctx, token, password, viewerUserID, true, passwordVerified)
	if err != nil {
		s.mu.Unlock()
		return AttachmentView{}, nil, err
	}
	attachment, err := s.attachmentByIDLocked(ctx, attachmentID)
	if err != nil || attachment.PasteID != paste.ID || attachment.Status != "active" {
		s.mu.Unlock()
		return AttachmentView{}, nil, E(http.StatusNotFound, "attachment_not_found", "attachment not found")
	}
	if attachment.ScanStatus != "clean" {
		s.mu.Unlock()
		return AttachmentView{}, nil, E(http.StatusForbidden, "scan_not_clean", "public downloads require clean scan status")
	}
	owner := s.usersByID[share.UserID]
	plan, _ := s.planForUserLocked(owner)
	if atomicStore, ok := s.content.Shares.(AtomicShareStore); ok {
		shareID := share.ID
		ownerID := share.UserID
		snapshot := *attachment
		now := s.now().UTC()
		s.mu.Unlock()
		content, contentErr := s.objectContentWithContext(ctx, &snapshot)
		if contentErr != nil {
			return AttachmentView{}, nil, E(http.StatusGone, "attachment_unavailable", "attachment content is unavailable")
		}
		consumed, updatedAttachment, consumeErr := atomicStore.ConsumeShareDownload(ctx, shareID, snapshot.ID, ownerID, plan.DailyShareDownloadBytes, now)
		if consumeErr != nil {
			if isStoreNotFound(consumeErr) {
				return AttachmentView{}, nil, E(http.StatusNotFound, "attachment_not_found", "attachment not found")
			}
			return AttachmentView{}, nil, consumeErr
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		share = s.cacheShareLocked(consumed)
		attachment = s.cacheAttachmentLocked(updatedAttachment)
		return viewAttachment(attachment), content, nil
	}
	downloadBytes, err := s.dailyMetricLocked(ctx, share.UserID, "share_download")
	if err != nil {
		s.mu.Unlock()
		return AttachmentView{}, nil, err
	}
	if downloadBytes+attachment.Size > plan.DailyShareDownloadBytes {
		s.mu.Unlock()
		return AttachmentView{}, nil, E(http.StatusForbidden, "daily_download_limit", "daily share download traffic exceeds plan limit")
	}
	content, err := s.objectContentLocked(ctx, attachment)
	if err != nil {
		s.mu.Unlock()
		return AttachmentView{}, nil, E(http.StatusGone, "attachment_unavailable", "attachment content is unavailable")
	}
	if err := s.commitSharedDownloadLocked(ctx, share, attachment, s.now().UTC()); err != nil {
		s.mu.Unlock()
		return AttachmentView{}, nil, err
	}
	s.mu.Unlock()
	return viewAttachment(attachment), content, nil
}

func (s *Service) commitSharedDownloadLocked(ctx context.Context, share *Share, attachment *Attachment, now time.Time) error {
	originalShare := *share
	originalAttachment := *attachment
	share.DownloadCount++
	share.LastDownloadedAt = &now
	if err := s.updateShareLocked(ctx, share); err != nil {
		return err
	}
	attachment.DownloadN++
	if err := s.updateAttachmentLocked(ctx, attachment); err != nil {
		rollbackErr := s.updateShareLocked(ctx, &originalShare)
		return errors.Join(err, rollbackErr)
	}
	if err := s.recordDailyShareDownloadLocked(ctx, share.UserID, attachment.Size); err != nil {
		rollbackAttachmentErr := s.updateAttachmentLocked(ctx, &originalAttachment)
		rollbackShareErr := s.updateShareLocked(ctx, &originalShare)
		return errors.Join(err, rollbackAttachmentErr, rollbackShareErr)
	}
	return nil
}

func (s *Service) RevokeShare(userID string, shareID string) error {
	return s.RevokeShareWithContext(context.Background(), userID, shareID)
}

func (s *Service) CreateShare(userID string, pasteID string, input ShareInput) (ShareView, error) {
	return s.CreateShareWithContext(context.Background(), userID, pasteID, input)
}
