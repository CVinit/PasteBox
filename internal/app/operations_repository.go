package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Service) createOrderLocked(ctx context.Context, order *Order) error {
	if s.ops.Orders != nil {
		storedOrder := *order
		s.mu.Unlock()
		err := s.ops.Orders.CreateOrder(ctx, storedOrder)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cacheOrderLocked(*order)
	return nil
}

func (s *Service) updateOrderLocked(ctx context.Context, order *Order) error {
	if s.ops.Orders != nil {
		storedOrder := *order
		s.mu.Unlock()
		err := s.ops.Orders.UpdateOrder(ctx, storedOrder)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cacheOrderLocked(*order)
	return nil
}

func (s *Service) orderByIDLocked(ctx context.Context, id string) (*Order, error) {
	if s.ops.Orders != nil {
		s.mu.Unlock()
		loaded, err := s.ops.Orders.OrderByID(ctx, id)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "order_not_found", "order not found")
			}
			return nil, err
		}
		return s.cacheOrderLocked(loaded), nil
	}
	order := s.ordersByID[id]
	if order == nil {
		return nil, E(http.StatusNotFound, "order_not_found", "order not found")
	}
	return order, nil
}

func (s *Service) cacheOrderLocked(order Order) *Order {
	cached := order
	s.ordersByID[cached.ID] = &cached
	return &cached
}

func (s *Service) ordersByUserLocked(ctx context.Context, userID string) ([]Order, error) {
	if s.ops.Orders != nil {
		s.mu.Unlock()
		orders, err := s.ops.Orders.ListOrdersByUser(ctx, userID)
		s.mu.Lock()
		if err != nil {
			return nil, err
		}
		for _, order := range orders {
			s.cacheOrderLocked(order)
		}
		return orders, nil
	}
	out := []Order{}
	for _, order := range s.ordersByID {
		if order.UserID == userID {
			out = append(out, *order)
		}
	}
	return out, nil
}

func (s *Service) createReportLocked(ctx context.Context, report *Report) error {
	if s.ops.Reports != nil {
		storedReport := *report
		s.mu.Unlock()
		err := s.ops.Reports.CreateReport(ctx, storedReport)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cacheReportLocked(*report)
	return nil
}

func (s *Service) updateReportStatusLocked(ctx context.Context, reportID string, status string) (*Report, error) {
	if s.ops.Reports != nil {
		s.mu.Unlock()
		err := s.ops.Reports.UpdateReportStatus(ctx, reportID, status)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "report_not_found", "report not found")
			}
			return nil, err
		}
	}
	report, err := s.reportByIDLocked(ctx, reportID)
	if err != nil {
		return nil, err
	}
	report.Status = status
	s.cacheReportLocked(*report)
	return report, nil
}

func (s *Service) reportByIDLocked(ctx context.Context, id string) (*Report, error) {
	if s.ops.Reports != nil {
		s.mu.Unlock()
		loaded, err := s.ops.Reports.ReportByID(ctx, id)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, E(http.StatusNotFound, "report_not_found", "report not found")
			}
			return nil, err
		}
		return s.cacheReportLocked(loaded), nil
	}
	for _, report := range s.reports {
		if report.ID == id {
			return report, nil
		}
	}
	return nil, E(http.StatusNotFound, "report_not_found", "report not found")
}

func (s *Service) cacheReportLocked(report Report) *Report {
	cached := report
	for i, existing := range s.reports {
		if existing.ID == cached.ID {
			s.reports[i] = &cached
			return &cached
		}
	}
	s.reports = append(s.reports, &cached)
	return &cached
}

func (s *Service) createWebhookEventLocked(ctx context.Context, event *WebhookEvent) error {
	if s.ops.WebhookEvents != nil {
		storedEvent := *event
		s.mu.Unlock()
		err := s.ops.WebhookEvents.CreateWebhookEvent(ctx, storedEvent)
		s.mu.Lock()
		if err != nil {
			if errors.Is(err, ErrStoreConflict) {
				s.mu.Unlock()
				loaded, err := s.ops.WebhookEvents.WebhookEventByIdempotencyKey(ctx, event.IdempotencyKey)
				s.mu.Lock()
				if err != nil {
					return err
				}
				s.cacheWebhookEventLocked(loaded)
				return nil
			}
			return err
		}
	}
	s.cacheWebhookEventLocked(*event)
	return nil
}

func (s *Service) cacheWebhookEventLocked(event WebhookEvent) *WebhookEvent {
	cached := event
	cached.Metadata = cloneMetadata(event.Metadata)
	for i, existing := range s.webhookEvents {
		if existing.ID == cached.ID {
			s.webhookEvents[i] = &cached
			s.webhookEventKeys[cached.IdempotencyKey] = cached.ID
			return &cached
		}
	}
	s.webhookEvents = append(s.webhookEvents, &cached)
	s.webhookEventKeys[cached.IdempotencyKey] = cached.ID
	return &cached
}

func (s *Service) createQueueItemLocked(ctx context.Context, queue *[]*QueueItem, item *QueueItem) error {
	if item.Status == "" {
		item.Status = "failed"
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = s.now().UTC()
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	if item.RunAfter.IsZero() {
		item.RunAfter = item.UpdatedAt
	}
	if s.ops.Queues != nil {
		storedItem := *item
		s.mu.Unlock()
		err := s.ops.Queues.CreateQueueItem(ctx, storedItem)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cacheQueueItemLocked(queue, *item)
	return nil
}

func (s *Service) deleteQueueItemsByKindTargetLocked(ctx context.Context, queue *[]*QueueItem, kind string, targetID string) error {
	if s.ops.Queues != nil {
		s.mu.Unlock()
		err := s.ops.Queues.DeleteQueueItemsByKindTarget(ctx, kind, targetID)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.removeQueueItemLocked(queue, targetID)
	return nil
}

func (s *Service) refreshQueueCachesLocked(ctx context.Context) error {
	if s.ops.Queues == nil {
		return nil
	}
	s.cleanupJobs = []*QueueItem{}
	s.cleanupFailures = []*QueueItem{}
	s.scanJobs = []*QueueItem{}
	s.scanFailures = []*QueueItem{}
	s.failedJobs = []*QueueItem{}

	s.mu.Unlock()
	cleanupJobs, err := s.ops.Queues.ListQueueItemsByKind(ctx, "cleanup")
	s.mu.Lock()
	if err != nil {
		return fmt.Errorf("load cleanup jobs: %w", err)
	}
	for _, item := range cleanupJobs {
		if item.Status == "pending" {
			s.cacheQueueItemLocked(&s.cleanupJobs, item)
		}
	}
	s.mu.Unlock()
	cleanupFailures, err := s.ops.Queues.ListQueueItemsByKind(ctx, "cleanup_failed")
	s.mu.Lock()
	if err != nil {
		return fmt.Errorf("load cleanup failures: %w", err)
	}
	for _, item := range cleanupFailures {
		s.cacheQueueItemLocked(&s.cleanupFailures, item)
	}
	s.mu.Unlock()
	scanJobs, err := s.ops.Queues.ListQueueItemsByKind(ctx, "scan")
	s.mu.Lock()
	if err != nil {
		return fmt.Errorf("load scan jobs: %w", err)
	}
	for _, item := range scanJobs {
		if item.Status == "pending" {
			s.cacheQueueItemLocked(&s.scanJobs, item)
		}
	}
	s.mu.Unlock()
	scanFailures, err := s.ops.Queues.ListQueueItemsByKind(ctx, "scan_failed")
	s.mu.Lock()
	if err != nil {
		return fmt.Errorf("load scan failures: %w", err)
	}
	for _, item := range scanFailures {
		s.cacheQueueItemLocked(&s.scanFailures, item)
	}
	s.mu.Unlock()
	failedJobs, err := s.ops.Queues.ListQueueItemsByStatus(ctx, "failed", 100)
	s.mu.Lock()
	if err != nil {
		return fmt.Errorf("load failed jobs: %w", err)
	}
	for _, item := range failedJobs {
		s.cacheQueueItemLocked(&s.failedJobs, item)
	}
	return nil
}

func (s *Service) cacheQueueItemLocked(queue *[]*QueueItem, item QueueItem) {
	cached := item
	for i, existing := range *queue {
		if existing.ID == cached.ID {
			(*queue)[i] = &cached
			return
		}
	}
	*queue = append(*queue, &cached)
}

func (s *Service) scheduleCleanupJobLocked(ctx context.Context, targetID string, now time.Time) error {
	return s.createQueueItemLocked(ctx, &s.cleanupJobs, &QueueItem{
		ID:        s.newID("job"),
		Kind:      "cleanup",
		TargetID:  targetID,
		Status:    "pending",
		RunAfter:  now,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func (s *Service) scheduleScanJobLocked(ctx context.Context, targetID string, now time.Time) error {
	return s.createQueueItemLocked(ctx, &s.scanJobs, &QueueItem{
		ID:        s.newID("job"),
		Kind:      "scan",
		TargetID:  targetID,
		Status:    "pending",
		RunAfter:  now,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func (s *Service) markScanFailureLocked(ctx context.Context, targetID string, reason string, now time.Time) error {
	if err := s.deleteQueueItemsByKindTargetLocked(ctx, &s.scanFailures, "scan_failed", targetID); err != nil {
		return err
	}
	return s.createQueueItemLocked(ctx, &s.scanFailures, &QueueItem{
		ID:        s.newID("scanq"),
		Kind:      "scan_failed",
		TargetID:  targetID,
		Status:    "failed",
		Error:     reason,
		Attempts:  1,
		RunAfter:  now,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func (s *Service) cacheMailLocked(mail Mail) *Mail {
	cached := mail
	for i, existing := range s.mails {
		if existing.ID == cached.ID {
			s.mails[i] = &cached
			return &cached
		}
	}
	s.mails = append(s.mails, &cached)
	return &cached
}

func (s *Service) refreshMailCacheLocked(ctx context.Context) error {
	if s.ops.Mails == nil {
		return nil
	}
	s.mu.Unlock()
	mails, err := s.ops.Mails.QueuedMails(ctx, 1000)
	s.mu.Lock()
	if err != nil {
		return fmt.Errorf("load queued mails: %w", err)
	}
	s.mails = []*Mail{}
	for _, mail := range mails {
		s.cacheMailLocked(mail)
	}
	return nil
}

func (s *Service) mailQueueItemsLocked(ctx context.Context, status string, limit int) ([]MailQueueItem, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "queued"
	}
	if s.ops.Mails != nil {
		s.mu.Unlock()
		defer s.mu.Lock()
		return s.ops.Mails.MailQueueItems(ctx, status, limit)
	}
	if status != "queued" {
		return []MailQueueItem{}, nil
	}
	items := make([]MailQueueItem, 0, len(s.mails))
	for _, mail := range s.mails {
		if mail == nil {
			continue
		}
		items = append(items, MailQueueItem{
			ID:        mail.ID,
			To:        mail.To,
			Subject:   mail.Subject,
			Status:    "queued",
			RunAfter:  mail.CreatedAt,
			CreatedAt: mail.CreatedAt,
		})
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return items, nil
}

func (s *Service) createMailLocked(ctx context.Context, mail *Mail) error {
	if s.ops.Mails != nil {
		storedMail := *mail
		s.mu.Unlock()
		err := s.ops.Mails.QueueMail(ctx, storedMail)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cacheMailLocked(*mail)
	return nil
}

// queueMail performs the potentially slow operational-store write without
// holding the service state lock, then publishes the queued mail locally.
func (s *Service) queueMail(ctx context.Context, mail Mail) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.ops.Mails != nil {
		if err := s.ops.Mails.QueueMail(ctx, mail); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cacheMailLocked(mail)
	s.mu.Unlock()
	return nil
}
