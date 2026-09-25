package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const (
	defaultPasteListLimit    = 1000
	maxPasteListLimit        = 1000
	initialContentCacheLimit = 1000
)

func normalizePasteListOptions(opts ListOptions) (int, int) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultPasteListLimit
	}
	if limit > maxPasteListLimit {
		limit = maxPasteListLimit
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (s *Service) CreatePasteWithContext(ctx context.Context, userID string, input PasteInput) (PasteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(ctx, userID)
	if err != nil {
		return PasteView{}, err
	}
	plan, err := s.planForUserLocked(user)
	if err != nil {
		return PasteView{}, err
	}
	if err := s.ensureCanCreatePasteLocked(ctx, user, plan, input, 0, 0); err != nil {
		return PasteView{}, err
	}
	tags := normalizeTags(input.Tags)
	now := s.now().UTC()
	expiresAt := resolveExpiresAt(now, input.ExpiresInSeconds, plan)
	paste := &Paste{
		ID:         s.newID("pst"),
		UserID:     user.ID,
		Title:      strings.TrimSpace(input.Title),
		Text:       input.Text,
		Tags:       tags,
		Pinned:     input.Pinned,
		Favorite:   input.Favorite,
		Status:     "active",
		ScanStatus: "clean",
		ExpiresAt:  expiresAt,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if textBytes := int64(len([]byte(paste.Text))); textBytes > 0 {
		if tx, ok := s.transactions.(PasteDailyMetricTransactionStore); ok {
			s.mu.Unlock()
			err := tx.CreatePasteWithDailyMetric(ctx, *paste, now, textBytes)
			s.mu.Lock()
			if err != nil {
				return PasteView{}, err
			}
			s.cachePasteLocked(*paste)
			return s.viewPasteLocked(paste), nil
		}
		if err := s.recordDailyUploadLocked(ctx, user.ID, textBytes); err != nil {
			return PasteView{}, err
		}
	}
	if err := s.createPasteLocked(ctx, paste); err != nil {
		return PasteView{}, err
	}
	return s.viewPasteLocked(paste), nil
}

func (s *Service) ListPastes(userID string, opts ListOptions) ([]PasteView, error) {
	return s.ListPastesWithContext(context.Background(), userID, opts)
}

func (s *Service) ListPastesWithContext(ctx context.Context, userID string, opts ListOptions) ([]PasteView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	limit, offset := normalizePasteListOptions(opts)
	if _, err := s.activeUserWithContext(ctx, userID); err != nil {
		return nil, err
	}

	paged, ok := s.content.Pastes.(PagedPasteStore)
	if !ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.refreshContentCachesLocked(ctx); err != nil {
			return nil, err
		}
		pastes := make([]*Paste, 0)
		for _, paste := range s.pastesByID {
			if paste.UserID == userID && s.isPasteVisibleLocked(paste) {
				pastes = append(pastes, paste)
			}
		}
		return s.listPasteViewsLocked(pastes, opts, limit, offset, false), nil
	}

	listNow := s.now().UTC()
	pageApplied := true
	var loaded []Paste
	var err error
	if filtered, supportsFilters := s.content.Pastes.(FilteredPagedPasteStore); supportsFilters {
		loaded, err = filtered.ListPastesByUserPageWithOptions(ctx, userID, opts, limit, offset)
	} else if strings.TrimSpace(opts.Query) == "" && strings.TrimSpace(opts.Filter) == "" && strings.TrimSpace(opts.Tag) == "" {
		loaded, err = paged.ListPastesByUserPage(ctx, userID, limit, offset)
	} else {
		loaded, err = s.content.Pastes.ListPastesByUser(ctx, userID)
		pageApplied = false
	}
	if err != nil {
		return nil, fmt.Errorf("load user pastes: %w", err)
	}
	type pasteRecord struct {
		paste       Paste
		attachments []Attachment
	}
	records := make([]pasteRecord, 0, len(loaded))
	for _, loadedPaste := range loaded {
		if loadedPaste.UserID != userID || loadedPaste.Status != "active" || !loadedPaste.ExpiresAt.After(listNow) {
			continue
		}
		record := pasteRecord{paste: loadedPaste}
		if s.content.Attachments != nil {
			record.attachments, err = s.content.Attachments.ListAttachmentsByPaste(ctx, loadedPaste.ID)
			if err != nil {
				return nil, fmt.Errorf("load paste attachments: %w", err)
			}
		}
		records = append(records, record)
	}
	var userShares []Share
	if s.content.Shares != nil {
		userShares, err = s.content.Shares.ListSharesByUser(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("load user shares: %w", err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, share := range userShares {
		s.cacheShareLocked(share)
	}
	pastes := make([]*Paste, 0, len(records))
	for _, record := range records {
		if !s.isPasteVisibleLocked(&record.paste) {
			continue
		}
		record.paste.AttachmentIDs = nil
		paste := s.cachePasteLocked(record.paste)
		for _, attachment := range record.attachments {
			s.cacheAttachmentLocked(attachment)
		}
		pastes = append(pastes, paste)
	}
	return s.listPasteViewsLocked(pastes, opts, limit, offset, pageApplied), nil
}

func (s *Service) listPasteViewsLocked(pastes []*Paste, opts ListOptions, limit int, offset int, paged bool) []PasteView {
	out := []PasteView{}
	query := strings.ToLower(strings.TrimSpace(opts.Query))
	filter := strings.TrimSpace(opts.Filter)
	tag := strings.ToLower(strings.TrimSpace(opts.Tag))
	for _, paste := range pastes {
		view := s.viewPasteLocked(paste)
		if !matchesPaste(view, query, filter, tag) {
			continue
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if !paged {
		if offset >= len(out) {
			return []PasteView{}
		}
		end := offset + limit
		if end > len(out) {
			end = len(out)
		}
		out = out[offset:end]
	}
	return out
}

func (s *Service) GetPasteWithContext(ctx context.Context, userID string, id string) (PasteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(ctx, userID, id)
	if err != nil {
		return PasteView{}, err
	}
	if !s.isPasteVisibleLocked(paste) {
		return PasteView{}, E(http.StatusGone, "paste_expired", "paste is expired or deleted")
	}
	return s.viewPasteLocked(paste), nil
}

func (s *Service) UpdatePasteWithContext(ctx context.Context, userID string, id string, patch PastePatch) (PasteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(ctx, userID, id)
	if err != nil {
		return PasteView{}, err
	}
	if !s.isPasteVisibleLocked(paste) {
		return PasteView{}, E(http.StatusGone, "paste_expired", "paste is expired or deleted")
	}
	originalPaste := *paste
	originalPaste.Tags = append([]string(nil), paste.Tags...)
	user := s.usersByID[userID]
	plan, _ := s.planForUserLocked(user)
	nextText := paste.Text
	if patch.Text != nil {
		nextText = *patch.Text
	}
	if int64(len([]byte(nextText))) > plan.SingleTextBytes {
		return PasteView{}, E(http.StatusRequestEntityTooLarge, "text_too_large", "text exceeds plan limit")
	}
	currentTextBytes := int64(len([]byte(paste.Text)))
	nextTextBytes := int64(len([]byte(nextText)))
	textDelta := nextTextBytes - currentTextBytes
	attachmentsBytes := s.pasteSizeLocked(paste) - currentTextBytes
	if attachmentsBytes+nextTextBytes > plan.SinglePasteBytes {
		return PasteView{}, E(http.StatusRequestEntityTooLarge, "paste_too_large", "paste exceeds plan total size")
	}
	quota, err := s.quotaLocked(ctx, user.ID, plan)
	if err != nil {
		return PasteView{}, err
	}
	nextStorageBytes := quota.ActiveStorageBytes - currentTextBytes + nextTextBytes
	if nextStorageBytes > plan.ActiveStorageBytes {
		return PasteView{}, E(http.StatusForbidden, "storage_limit", "active storage exceeds plan limit")
	}
	if textDelta > 0 && quota.DailyUploadBytes+textDelta > plan.DailyUploadBytes {
		return PasteView{}, E(http.StatusForbidden, "daily_upload_limit", "daily upload traffic exceeds plan limit")
	}
	var nextTags []string
	if patch.HasTags {
		nextTags = normalizeTags(patch.Tags)
		if err := ensureTagsWithinPlan(plan, paste.Tags, nextTags); err != nil {
			return PasteView{}, err
		}
	}
	if textDelta > 0 {
		if tx, ok := s.transactions.(PasteDailyMetricTransactionStore); ok {
			nextPaste := *paste
			nextPaste.Tags = append([]string(nil), paste.Tags...)
			if patch.Title != nil {
				nextPaste.Title = strings.TrimSpace(*patch.Title)
			}
			if patch.Text != nil {
				nextPaste.Text = *patch.Text
			}
			if patch.HasTags {
				nextPaste.Tags = nextTags
			}
			if patch.Pinned != nil {
				nextPaste.Pinned = *patch.Pinned
			}
			if patch.Favorite != nil {
				nextPaste.Favorite = *patch.Favorite
			}
			nextPaste.UpdatedAt = s.now().UTC()
			s.mu.Unlock()
			err := tx.UpdatePasteWithDailyMetric(ctx, nextPaste, nextPaste.UpdatedAt, textDelta)
			s.mu.Lock()
			if err != nil {
				return PasteView{}, err
			}
			s.cachePasteLocked(nextPaste)
			return s.viewPasteLocked(&nextPaste), nil
		}
	}
	if patch.Title != nil {
		paste.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.Text != nil {
		paste.Text = *patch.Text
	}
	if patch.HasTags {
		paste.Tags = nextTags
	}
	if patch.Pinned != nil {
		paste.Pinned = *patch.Pinned
	}
	if patch.Favorite != nil {
		paste.Favorite = *patch.Favorite
	}
	paste.UpdatedAt = s.now().UTC()
	if err := s.updatePasteLocked(ctx, paste); err != nil {
		return PasteView{}, err
	}
	if textDelta > 0 {
		if err := s.recordDailyUploadLocked(ctx, user.ID, textDelta); err != nil {
			rollbackErr := s.updatePasteLocked(ctx, &originalPaste)
			return PasteView{}, errors.Join(err, rollbackErr)
		}
	}
	return s.viewPasteLocked(paste), nil
}

func (s *Service) DeletePasteWithContext(ctx context.Context, userID string, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(ctx, userID, id)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	paste.Status = "pending_delete"
	paste.UpdatedAt = now
	if err := s.updatePasteLocked(ctx, paste); err != nil {
		return err
	}
	for _, attachmentID := range paste.AttachmentIDs {
		if attachment := s.attachmentsByID[attachmentID]; attachment != nil {
			attachment.Status = "pending_delete"
			if err := s.updateAttachmentLocked(ctx, attachment); err != nil {
				return err
			}
		}
	}
	for _, share := range s.sharesByID {
		if share.PasteID == paste.ID && share.RevokedAt == nil {
			share.RevokedAt = &now
			if err := s.updateShareLocked(ctx, share); err != nil {
				return err
			}
		}
	}
	if err := s.scheduleCleanupJobLocked(ctx, paste.ID, now); err != nil {
		return err
	}
	return nil
}

func (s *Service) ExtendPasteWithContext(ctx context.Context, userID string, id string, expiresInSeconds int64) (PasteView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(ctx, userID, id)
	if err != nil {
		return PasteView{}, err
	}
	if !s.isPasteVisibleLocked(paste) {
		return PasteView{}, E(http.StatusGone, "paste_expired", "expired paste cannot be extended")
	}
	user := s.usersByID[userID]
	plan, _ := s.planForUserLocked(user)
	now := s.now().UTC()
	nextExpiresAt := resolveExpiresAt(now, expiresInSeconds, plan)
	if nextExpiresAt.Before(paste.ExpiresAt) {
		return PasteView{}, E(http.StatusBadRequest, "invalid_expiration", "new expiration must extend the paste")
	}
	if err := s.ensureUserCanWriteLocked(ctx, user, plan); err != nil {
		return PasteView{}, err
	}
	paste.ExpiresAt = nextExpiresAt
	paste.UpdatedAt = now
	if err := s.updatePasteLocked(ctx, paste); err != nil {
		return PasteView{}, err
	}
	return s.viewPasteLocked(paste), nil
}

func (s *Service) ExtendPaste(userID string, id string, expiresInSeconds int64) (PasteView, error) {
	return s.ExtendPasteWithContext(context.Background(), userID, id, expiresInSeconds)
}

func (s *Service) DeletePaste(userID string, id string) error {
	return s.DeletePasteWithContext(context.Background(), userID, id)
}

func (s *Service) UpdatePaste(userID string, id string, patch PastePatch) (PasteView, error) {
	return s.UpdatePasteWithContext(context.Background(), userID, id, patch)
}

func (s *Service) GetPaste(userID string, id string) (PasteView, error) {
	return s.GetPasteWithContext(context.Background(), userID, id)
}

func (s *Service) CreatePaste(userID string, input PasteInput) (PasteView, error) {
	return s.CreatePasteWithContext(context.Background(), userID, input)
}
