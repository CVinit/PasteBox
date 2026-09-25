package app

import (
	"context"
	"net/http"
	"pastebox/internal/plans"
	"strings"
	"time"
)

func (s *Service) ownerPasteLocked(ctx context.Context, userID string, id string) (*Paste, error) {
	if _, err := s.activeUserLocked(ctx, userID); err != nil {
		return nil, err
	}
	paste, err := s.pasteByIDLocked(ctx, id)
	if err != nil {
		if isStoreNotFound(err) || isAppStatus(err, http.StatusNotFound) {
			return nil, E(http.StatusNotFound, "paste_not_found", "paste not found")
		}
		return nil, err
	}
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

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneCatalog(catalog plans.Catalog) plans.Catalog {
	return plans.Catalog{
		Plans:  append([]plans.Plan(nil), catalog.Plans...),
		Prices: append([]plans.Price(nil), catalog.Prices...),
	}
}

func (s *Service) ensureUserCanWriteLocked(ctx context.Context, user *User, plan plans.Plan) error {
	if !user.EmailVerified {
		return E(http.StatusForbidden, "email_not_verified", "email verification is required before writing content")
	}
	quota, err := s.quotaLocked(ctx, user.ID, plan)
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

func (s *Service) ensureCanCreatePasteLocked(ctx context.Context, user *User, plan plans.Plan, input PasteInput, extraBytes int64, extraAttachments int) error {
	if err := s.ensureUserCanWriteLocked(ctx, user, plan); err != nil {
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
	quota, err := s.quotaLocked(ctx, user.ID, plan)
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

func (s *Service) quotaLocked(ctx context.Context, userID string, plan plans.Plan) (QuotaView, error) {
	var activeCount int
	var activeStorage int64
	if metricsStore, ok := s.content.Pastes.(UserContentMetricsStore); ok {
		s.mu.Unlock()
		metrics, err := metricsStore.UserContentMetrics(ctx, userID)
		s.mu.Lock()
		if err != nil {
			return QuotaView{}, err
		}
		activeCount = metrics.ActivePasteCount
		activeStorage = metrics.ActiveStorageBytes
	} else {
		for _, paste := range s.pastesByID {
			if paste.UserID != userID || !s.isPasteVisibleLocked(paste) {
				continue
			}
			activeCount++
			activeStorage += s.pasteSizeLocked(paste)
		}
	}
	upload, err := s.dailyMetricLocked(ctx, userID, "upload")
	if err != nil {
		return QuotaView{}, err
	}
	download, err := s.dailyMetricLocked(ctx, userID, "share_download")
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

func (s *Service) validShareAccessLocked(ctx context.Context, token string, password string, viewerUserID string, forDownload bool, passwordVerified bool) (*Share, *Paste, error) {
	share, err := s.shareByTokenHashLocked(ctx, tokenHash(token))
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
	if _, atomic := s.content.Shares.(AtomicShareStore); !atomic {
		if share.MaxVisits > 0 && !forDownload && share.VisitCount >= share.MaxVisits {
			return nil, nil, E(http.StatusGone, "visit_limit_reached", "share visit limit reached")
		}
		if share.MaxDownloads > 0 && forDownload && share.DownloadCount >= share.MaxDownloads {
			return nil, nil, E(http.StatusGone, "download_limit_reached", "share download limit reached")
		}
	}
	if share.PasswordHash != "" && !passwordVerified {
		return nil, nil, E(http.StatusUnauthorized, "invalid_share_password", "share password is invalid")
	}
	paste, err := s.pasteByIDLocked(ctx, share.PasteID)
	if err != nil {
		return nil, nil, err
	}
	if !s.isPasteVisibleLocked(paste) {
		return nil, nil, E(http.StatusGone, "paste_expired", "paste is expired or deleted")
	}
	return share, paste, nil
}

func (s *Service) verifySharePasswordForAccess(ctx context.Context, token string, password string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	share, err := s.shareByTokenHashLocked(ctx, tokenHash(token))
	if err != nil {
		s.mu.Unlock()
		return false, E(http.StatusNotFound, "share_not_found", "share not found")
	}
	storedHash := share.PasswordHash
	s.mu.Unlock()

	valid, needsUpgrade, err := verifySharePassword(storedHash, password)
	if err != nil {
		return false, err
	}
	if !valid {
		s.mu.Lock()
		defer s.mu.Unlock()
		share, loadErr := s.shareByTokenHashLocked(ctx, tokenHash(token))
		if loadErr != nil {
			return false, E(http.StatusNotFound, "share_not_found", "share not found")
		}
		now := s.now().UTC()
		share.LastAccessFailure = &now
		if err := s.updateShareLocked(ctx, share); err != nil {
			return false, err
		}
		return false, E(http.StatusUnauthorized, "invalid_share_password", "share password is invalid")
	}
	if !needsUpgrade {
		return true, nil
	}

	upgradedHash, err := hashSharePassword(password)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	share, err = s.shareByTokenHashLocked(ctx, tokenHash(token))
	if err != nil {
		return false, E(http.StatusNotFound, "share_not_found", "share not found")
	}
	if share.PasswordHash == storedHash {
		share.PasswordHash = upgradedHash
		if err := s.updateShareLocked(ctx, share); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *Service) dailyMetricLocked(ctx context.Context, userID string, kind string) (int64, error) {
	s.mu.Unlock()
	defer s.mu.Lock()
	return s.dailyMetrics.DailyMetric(ctx, userID, kind, s.now().UTC())
}

func (s *Service) recordDailyUploadLocked(ctx context.Context, userID string, bytes int64) error {
	s.mu.Unlock()
	defer s.mu.Lock()
	return s.dailyMetrics.RecordDailyMetric(ctx, userID, "upload", s.now().UTC(), bytes)
}

func (s *Service) recordDailyShareDownloadLocked(ctx context.Context, userID string, bytes int64) error {
	s.mu.Unlock()
	defer s.mu.Lock()
	return s.dailyMetrics.RecordDailyMetric(ctx, userID, "share_download", s.now().UTC(), bytes)
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
