package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func (s *Service) ListPastesLocked(userID string, opts ListOptions) ([]PasteView, error) {
	out := []PasteView{}
	query := strings.ToLower(strings.TrimSpace(opts.Query))
	filter := strings.TrimSpace(opts.Filter)
	tag := strings.ToLower(strings.TrimSpace(opts.Tag))
	for _, paste := range s.pastesByID {
		if paste.UserID != userID || !s.isPasteVisibleLocked(paste) {
			continue
		}
		view := s.viewPasteLocked(paste)
		if !matchesPaste(view, query, filter, tag) {
			continue
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) loadContentCaches(ctx context.Context) error {
	if s.canLoadBoundedContentCaches() {
		return s.loadBoundedContentCaches(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshContentCachesLocked(ctx)
}

func (s *Service) canLoadBoundedContentCaches() bool {
	if s.content.Pastes != nil {
		if _, ok := s.content.Pastes.(PagedAllPasteStore); !ok {
			return false
		}
	}
	if s.content.Attachments != nil {
		if _, ok := s.content.Attachments.(PagedAttachmentStore); !ok {
			return false
		}
	}
	if s.content.Shares != nil {
		if _, ok := s.content.Shares.(PagedShareStore); !ok {
			return false
		}
	}
	if s.content.ObjectRefs != nil {
		if _, ok := s.content.ObjectRefs.(AtomicObjectRefStore); !ok {
			return false
		}
	}
	return true
}

func (s *Service) loadBoundedContentCaches(ctx context.Context) error {
	var pastes []Paste
	var attachments []Attachment
	var shares []Share
	var err error
	if store, ok := s.content.Pastes.(PagedAllPasteStore); ok {
		pastes, err = store.ListPastesPage(ctx, initialContentCacheLimit, 0)
		if err != nil {
			return fmt.Errorf("load initial pastes: %w", err)
		}
	}
	if store, ok := s.content.Attachments.(PagedAttachmentStore); ok {
		attachments, err = store.ListAttachmentsPage(ctx, "", initialContentCacheLimit, 0)
		if err != nil {
			return fmt.Errorf("load initial attachments: %w", err)
		}
	}
	if store, ok := s.content.Shares.(PagedShareStore); ok {
		shares, err = store.ListSharesPage(ctx, initialContentCacheLimit, 0)
		if err != nil {
			return fmt.Errorf("load initial shares: %w", err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pastesByID = map[string]*Paste{}
	for _, paste := range pastes {
		s.cachePasteLocked(paste)
	}
	s.attachmentsByID = map[string]*Attachment{}
	for _, paste := range s.pastesByID {
		paste.AttachmentIDs = nil
	}
	for _, attachment := range attachments {
		s.cacheAttachmentLocked(attachment)
	}
	if err := s.rebuildObjectRefsLocked(ctx); err != nil {
		return fmt.Errorf("rebuild object refs: %w", err)
	}
	s.sharesByID = map[string]*Share{}
	s.shareIDByToken = map[string]string{}
	for _, share := range shares {
		s.cacheShareLocked(share)
	}
	return nil
}

func (s *Service) refreshContentCachesLocked(ctx context.Context) error {
	if s.content.Pastes != nil {
		s.mu.Unlock()
		pastes, err := s.content.Pastes.ListPastes(ctx)
		s.mu.Lock()
		if err != nil {
			return fmt.Errorf("load pastes: %w", err)
		}
		s.pastesByID = map[string]*Paste{}
		for _, paste := range pastes {
			s.cachePasteLocked(paste)
		}
	}
	if s.content.Attachments != nil {
		s.mu.Unlock()
		attachments, err := s.content.Attachments.ListAttachments(ctx)
		s.mu.Lock()
		if err != nil {
			return fmt.Errorf("load attachments: %w", err)
		}
		s.attachmentsByID = map[string]*Attachment{}
		for _, paste := range s.pastesByID {
			paste.AttachmentIDs = nil
		}
		for _, attachment := range attachments {
			s.cacheAttachmentLocked(attachment)
		}
		if err := s.rebuildObjectRefsLocked(ctx); err != nil {
			return fmt.Errorf("rebuild object refs: %w", err)
		}
	}
	if s.content.Shares != nil {
		s.mu.Unlock()
		shares, err := s.content.Shares.ListShares(ctx)
		s.mu.Lock()
		if err != nil {
			return fmt.Errorf("load shares: %w", err)
		}
		s.sharesByID = map[string]*Share{}
		s.shareIDByToken = map[string]string{}
		for _, share := range shares {
			s.cacheShareLocked(share)
		}
	}
	return nil
}

func (s *Service) loadOperationalCaches(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshOrderCachesLocked(ctx); err != nil {
		return err
	}
	if s.ops.WebhookEvents != nil {
		var events []WebhookEvent
		var err error
		if store, ok := s.ops.WebhookEvents.(PagedWebhookEventStore); ok {
			s.mu.Unlock()
			events, err = store.ListWebhookEventsPage(ctx, initialContentCacheLimit, 0)
			s.mu.Lock()
		} else {
			s.mu.Unlock()
			events, err = s.ops.WebhookEvents.ListWebhookEvents(ctx)
			s.mu.Lock()
		}
		if err != nil {
			return fmt.Errorf("load webhook events: %w", err)
		}
		for _, event := range events {
			s.cacheWebhookEventLocked(event)
		}
	}
	if s.ops.Reports != nil {
		var reports []Report
		var err error
		if store, ok := s.ops.Reports.(PagedReportStore); ok {
			s.mu.Unlock()
			reports, err = store.ListReportsPage(ctx, initialContentCacheLimit, 0)
			s.mu.Lock()
		} else {
			s.mu.Unlock()
			reports, err = s.ops.Reports.ListReports(ctx)
			s.mu.Lock()
		}
		if err != nil {
			return fmt.Errorf("load reports: %w", err)
		}
		for _, report := range reports {
			s.cacheReportLocked(report)
		}
	}
	if s.ops.Queues != nil {
		if err := s.refreshQueueCachesLocked(ctx); err != nil {
			return err
		}
	}
	if s.ops.Mails != nil {
		return s.refreshMailCacheLocked(ctx)
	}
	return nil
}

func (s *Service) refreshOrderCachesLocked(ctx context.Context) error {
	if s.ops.Orders == nil {
		return nil
	}
	var orders []Order
	var err error
	if store, ok := s.ops.Orders.(PagedOrderStore); ok {
		s.mu.Unlock()
		orders, err = store.ListOrdersPage(ctx, initialContentCacheLimit, 0)
		s.mu.Lock()
	} else {
		s.mu.Unlock()
		orders, err = s.ops.Orders.ListOrders(ctx)
		s.mu.Lock()
	}
	if err != nil {
		return fmt.Errorf("load orders: %w", err)
	}
	s.ordersByID = map[string]*Order{}
	for _, order := range orders {
		s.cacheOrderLocked(order)
	}
	return nil
}
