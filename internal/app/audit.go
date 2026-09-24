package app

import (
	"context"
	"net/http"
	"strings"
)

func (s *Service) requireAdminLocked(ctx context.Context, userID string) error {
	user, err := s.activeUserLocked(ctx, userID)
	if err != nil {
		return err
	}
	if user.Role != "admin" {
		return E(http.StatusForbidden, "admin_required", "admin role required")
	}
	return nil
}

func (s *Service) auditLocked(ctx context.Context, actorID string, action string, target string, metadata map[string]any) error {
	log := &AuditLog{ID: s.newID("aud"), ActorID: actorID, Action: action, Target: target, Metadata: cloneMetadata(metadata), CreatedAt: s.now().UTC()}
	if s.audit != nil {
		storedLog := *log
		s.mu.Unlock()
		err := s.audit.RecordAuditLog(ctx, storedLog)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}
	s.cacheAuditLogLocked(*log)
	return nil
}

func (s *Service) cacheAuditLogLocked(log AuditLog) *AuditLog {
	cached := cloneAuditLog(log)
	for i, existing := range s.auditLogs {
		if existing != nil && existing.ID == cached.ID {
			s.auditLogs[i] = &cached
			return &cached
		}
	}
	s.auditLogs = append(s.auditLogs, &cached)
	return &cached
}

func (s *Service) recordWebhookEventLocked(ctx context.Context, provider string, eventType string, targetID string, idempotencyKey string, metadata map[string]any) (WebhookEvent, error) {
	provider = defaultString(normalizeProvider(provider), "local")
	eventType = strings.TrimSpace(eventType)
	if idempotencyKey == "" {
		idempotencyKey = provider + ":" + eventType + ":" + targetID + ":" + s.newID("idem")
	}
	if event, ok, err := s.webhookEventByKeyLocked(ctx, idempotencyKey); err != nil {
		return WebhookEvent{}, err
	} else if ok {
		return event, nil
	}
	event := &WebhookEvent{
		ID:             s.newID("wh"),
		Provider:       provider,
		EventType:      eventType,
		TargetID:       strings.TrimSpace(targetID),
		IdempotencyKey: idempotencyKey,
		Processed:      true,
		Metadata:       cloneMetadata(metadata),
		ReceivedAt:     s.now().UTC(),
	}
	if err := s.createWebhookEventLocked(ctx, event); err != nil {
		return WebhookEvent{}, err
	}
	loaded, ok, err := s.webhookEventByKeyLocked(ctx, idempotencyKey)
	if err != nil {
		return WebhookEvent{}, err
	}
	if ok {
		return loaded, nil
	}
	return *event, nil
}

func (s *Service) webhookEventByKeyLocked(ctx context.Context, idempotencyKey string) (WebhookEvent, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s.ops.WebhookEvents != nil {
		s.mu.Unlock()
		event, err := s.ops.WebhookEvents.WebhookEventByIdempotencyKey(ctx, idempotencyKey)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return WebhookEvent{}, false, nil
			}
			return WebhookEvent{}, false, err
		}
		return *s.cacheWebhookEventLocked(event), true, nil
	}
	eventID := s.webhookEventKeys[idempotencyKey]
	if eventID == "" {
		return WebhookEvent{}, false, nil
	}
	event, err := s.webhookEventByIDLocked(ctx, eventID)
	if err != nil {
		return WebhookEvent{}, false, err
	}
	if event == nil {
		return WebhookEvent{}, false, nil
	}
	return *event, true, nil
}

func (s *Service) webhookEventByIDLocked(ctx context.Context, eventID string) (*WebhookEvent, error) {
	if s.ops.WebhookEvents != nil {
		s.mu.Unlock()
		event, err := s.ops.WebhookEvents.WebhookEventByID(ctx, eventID)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return s.cacheWebhookEventLocked(event), nil
	}
	for _, event := range s.webhookEvents {
		if event.ID == eventID {
			return event, nil
		}
	}
	return nil, nil
}
