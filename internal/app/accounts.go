package app

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *Service) UpdateProfileWithContext(ctx context.Context, userID string, displayName string, language string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(ctx, userID)
	if err != nil {
		return UserView{}, err
	}
	if strings.TrimSpace(displayName) != "" {
		user.DisplayName = strings.TrimSpace(displayName)
	}
	if strings.TrimSpace(language) != "" {
		user.Language = NormalizeUserLanguage(language)
	}
	user.UpdatedAt = s.now().UTC()
	if err := s.updateUserLocked(ctx, user); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(ctx, user)
}

func (s *Service) UnlinkOAuthIdentityWithContext(ctx context.Context, userID string, provider string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(ctx, userID)
	if err != nil {
		return UserView{}, err
	}
	provider = normalizeProvider(provider)
	if provider == "" {
		return UserView{}, E(http.StatusBadRequest, "invalid_oauth_provider", "oauth provider is required")
	}
	identities, err := s.oauthIdentitiesByUserLocked(ctx, user.ID)
	if err != nil {
		return UserView{}, err
	}
	if !hasOAuthProvider(identities, provider) {
		return UserView{}, E(http.StatusNotFound, "oauth_identity_not_linked", "oauth provider is not linked")
	}
	if err := s.deleteOAuthIdentityLocked(ctx, user.ID, provider); err != nil {
		return UserView{}, err
	}
	if err := s.auditLocked(ctx, user.ID, "auth.oauth_unlinked", user.ID, map[string]any{"provider": provider}); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(ctx, user)
}

func (s *Service) RequestAccountDeletionWithContext(ctx context.Context, userID string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(ctx, userID)
	if err != nil {
		return UserView{}, err
	}
	now := s.now().UTC()
	scheduled := now.Add(7 * 24 * time.Hour)
	user.DeleteRequestedAt = &now
	user.DeleteScheduledAt = &scheduled
	if err := s.updateUserLocked(ctx, user); err != nil {
		return UserView{}, err
	}
	if err := s.auditLocked(ctx, user.ID, "account.deletion_requested", user.ID, map[string]any{"scheduledAt": scheduled}); err != nil {
		return UserView{}, err
	}
	if err := s.mail(ctx, user.Email, "PasteBox account deletion requested", "Your account is scheduled for deletion in 7 days."); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(ctx, user)
}

func (s *Service) CancelAccountDeletionWithContext(ctx context.Context, userID string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(ctx, userID)
	if err != nil {
		return UserView{}, err
	}
	user.DeleteRequestedAt = nil
	user.DeleteScheduledAt = nil
	if err := s.updateUserLocked(ctx, user); err != nil {
		return UserView{}, err
	}
	if err := s.auditLocked(ctx, user.ID, "account.deletion_canceled", user.ID, nil); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(ctx, user)
}

func (s *Service) ExecuteAccountDeletionWithContext(ctx context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.activeUserLocked(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.refreshUserContentLocked(ctx, userID); err != nil {
		return err
	}
	now := s.now().UTC()
	user.DeletedAt = &now
	user.Frozen = true
	user.DeleteRequestedAt = &now
	user.DeleteScheduledAt = &now
	if err := s.updateUserLocked(ctx, user); err != nil {
		return err
	}
	pasteCount := 0
	for _, paste := range s.pastesByID {
		if paste.UserID == user.ID && paste.Status == "active" {
			paste.Status = "pending_delete"
			paste.UpdatedAt = now
			if err := s.updatePasteLocked(ctx, paste); err != nil {
				return err
			}
			pasteCount++
		}
	}
	shareCount := 0
	for _, share := range s.sharesByID {
		if share.UserID == user.ID && share.RevokedAt == nil {
			share.RevokedAt = &now
			if err := s.updateShareLocked(ctx, share); err != nil {
				return err
			}
			shareCount++
		}
	}
	if err := s.revokeUserSessionsLocked(ctx, user.ID); err != nil {
		return err
	}
	return s.auditLocked(ctx, user.ID, "account.deleted", user.ID, map[string]any{
		"pasteCount": pasteCount,
		"shareCount": shareCount,
	})
}

func (s *Service) ExportUserWithContext(ctx context.Context, userID string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, err := s.activeUserLocked(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.refreshUserContentLocked(ctx, userID); err != nil {
		return nil, err
	}
	pastes, _ := s.ListPastesLocked(userID, ListOptions{})
	shares := []ShareView{}
	for _, share := range s.sharesByID {
		if share.UserID == userID {
			shares = append(shares, s.viewShareLocked(share))
		}
	}
	sort.Slice(shares, func(i, j int) bool { return shares[i].CreatedAt.After(shares[j].CreatedAt) })
	orders, err := s.ordersByUserLocked(ctx, userID)
	if err != nil {
		return nil, err
	}
	sort.Slice(orders, func(i, j int) bool { return orders[i].CreatedAt.After(orders[j].CreatedAt) })

	reports := s.reportsByUserLocked(userID)
	if store, ok := s.ops.Reports.(UserReportStore); ok {
		s.mu.Unlock()
		reports, err = store.ListReportsByUser(ctx, userID)
		s.mu.Lock()
		if err != nil {
			return nil, err
		}
	}
	webhookEvents := s.webhookEventsForOrdersLocked(orders)
	if store, ok := s.ops.WebhookEvents.(UserWebhookEventStore); ok {
		s.mu.Unlock()
		webhookEvents, err = store.ListWebhookEventsByUser(ctx, userID)
		s.mu.Lock()
		if err != nil {
			return nil, err
		}
	}
	exportedAt := s.now().UTC()
	if err := s.auditLocked(ctx, userID, "account.export", userID, map[string]any{
		"pasteCount":        len(pastes),
		"shareCount":        len(shares),
		"orderCount":        len(orders),
		"reportCount":       len(reports),
		"webhookEventCount": len(webhookEvents),
	}); err != nil {
		return nil, err
	}
	auditLogs, err := s.auditLogsForExportLocked(ctx, userID, pastes, shares, orders, reports, webhookEvents)
	if err != nil {
		return nil, err
	}
	userView, err := s.viewUserLocked(ctx, user)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"user":          userView,
		"pastes":        pastes,
		"shares":        shares,
		"orders":        orders,
		"reports":       reports,
		"webhookEvents": webhookEvents,
		"auditLogs":     auditLogs,
		"exportedAt":    exportedAt,
	}, nil
}

func (s *Service) reportsByUserLocked(userID string) []Report {
	reports := []Report{}
	for _, report := range s.reports {
		if report.UserID == userID {
			reports = append(reports, *report)
		}
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].CreatedAt.After(reports[j].CreatedAt) })
	return reports
}

func (s *Service) webhookEventsForOrdersLocked(orders []Order) []WebhookEvent {
	orderIDs := map[string]struct{}{}
	for _, order := range orders {
		orderIDs[order.ID] = struct{}{}
	}
	events := []WebhookEvent{}
	for _, event := range s.webhookEvents {
		if _, ok := orderIDs[event.TargetID]; ok {
			events = append(events, cloneWebhookEvent(*event))
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].ReceivedAt.After(events[j].ReceivedAt) })
	return events
}

func (s *Service) auditLogsForExportLocked(ctx context.Context, userID string, pastes []PasteView, shares []ShareView, orders []Order, reports []Report, webhookEvents []WebhookEvent) ([]AuditLog, error) {
	targets := exportAuditTargets(userID, pastes, shares, orders, reports, webhookEvents)
	if s.audit != nil {
		s.mu.Unlock()
		defer s.mu.Lock()
		return s.audit.AuditLogsForActorOrTargets(ctx, userID, targets, 1000)
	}
	targetSet := map[string]struct{}{}
	for _, target := range targets {
		targetSet[target] = struct{}{}
	}
	logs := []AuditLog{}
	for _, log := range s.auditLogs {
		if log.ActorID == userID {
			logs = append(logs, cloneAuditLog(*log))
			continue
		}
		if _, ok := targetSet[log.Target]; ok {
			logs = append(logs, cloneAuditLog(*log))
		}
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].CreatedAt.After(logs[j].CreatedAt) })
	if len(logs) > 1000 {
		return logs[:1000], nil
	}
	return logs, nil
}

func exportAuditTargets(userID string, pastes []PasteView, shares []ShareView, orders []Order, reports []Report, webhookEvents []WebhookEvent) []string {
	targets := map[string]struct{}{userID: {}}
	for _, paste := range pastes {
		targets[paste.ID] = struct{}{}
		for _, attachment := range paste.Attachments {
			targets[attachment.ID] = struct{}{}
		}
	}
	for _, share := range shares {
		targets[share.ID] = struct{}{}
	}
	for _, order := range orders {
		targets[order.ID] = struct{}{}
	}
	for _, report := range reports {
		targets[report.ID] = struct{}{}
	}
	for _, event := range webhookEvents {
		targets[event.ID] = struct{}{}
	}
	out := make([]string, 0, len(targets))
	for target := range targets {
		if strings.TrimSpace(target) != "" {
			out = append(out, target)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Service) SeedAdminWithContext(ctx context.Context, email string, password string) (UserView, error) {
	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return UserView{}, E(http.StatusBadRequest, "invalid_email", "valid email is required")
	}
	if len(password) < 8 {
		return UserView{}, E(http.StatusBadRequest, "weak_password", "password must be at least 8 characters")
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return UserView{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	user, err := s.userByEmailLocked(ctx, email)
	if err != nil && !isStoreNotFound(err) && !isAppStatus(err, http.StatusNotFound) {
		return UserView{}, err
	}
	if user == nil {
		user = &User{
			ID:          s.newID("usr"),
			Email:       email,
			DisplayName: "PasteBox Admin",
			Language:    "en",
			PlanID:      "free",
			CreatedAt:   now,
		}
	}
	if strings.TrimSpace(user.DisplayName) == "" {
		user.DisplayName = "PasteBox Admin"
	}
	if strings.TrimSpace(user.Language) == "" {
		user.Language = "en"
	}
	if strings.TrimSpace(user.PlanID) == "" {
		user.PlanID = "free"
	}
	user.PasswordHash = passwordHash
	user.Role = "admin"
	user.EmailVerified = true
	user.Frozen = false
	user.UpdatedAt = now
	user.DeleteRequestedAt = nil
	user.DeleteScheduledAt = nil
	user.DeletedAt = nil

	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	if _, exists := s.usersByID[user.ID]; !exists {
		if err := s.createUserLocked(ctx, user); err != nil {
			if errors.Is(err, ErrStoreConflict) {
				return UserView{}, E(http.StatusConflict, "email_exists", "email is already registered")
			}
			return UserView{}, err
		}
	} else if err := s.updateUserLocked(ctx, user); err != nil {
		return UserView{}, err
	}
	if err := s.deleteLoginFailureLocked(ctx, email); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(ctx, user)
}

func (s *Service) SeedAdmin(email string, password string) (UserView, error) {
	return s.SeedAdminWithContext(context.Background(), email, password)
}

func (s *Service) ExportUser(userID string) (map[string]any, error) {
	return s.ExportUserWithContext(context.Background(), userID)
}

func (s *Service) ExecuteAccountDeletion(userID string) error {
	return s.ExecuteAccountDeletionWithContext(context.Background(), userID)
}

func (s *Service) CancelAccountDeletion(userID string) (UserView, error) {
	return s.CancelAccountDeletionWithContext(context.Background(), userID)
}

func (s *Service) RequestAccountDeletion(userID string) (UserView, error) {
	return s.RequestAccountDeletionWithContext(context.Background(), userID)
}

func (s *Service) UnlinkOAuthIdentity(userID string, provider string) (UserView, error) {
	return s.UnlinkOAuthIdentityWithContext(context.Background(), userID, provider)
}

func (s *Service) UpdateProfile(userID string, displayName string, language string) (UserView, error) {
	return s.UpdateProfileWithContext(context.Background(), userID, displayName, language)
}

// Full account operations must not depend on the initial admin cache window.
func (s *Service) refreshUserContentLocked(ctx context.Context, userID string) error {
	if s.content.Pastes != nil {
		s.mu.Unlock()
		pastes, err := s.content.Pastes.ListPastesByUser(ctx, userID)
		s.mu.Lock()
		if err != nil {
			return err
		}
		for _, paste := range pastes {
			cached := s.cachePasteLocked(paste)
			if s.content.Attachments != nil {
				s.mu.Unlock()
				attachments, err := s.content.Attachments.ListAttachmentsByPaste(ctx, paste.ID)
				s.mu.Lock()
				if err != nil {
					return err
				}
				cached.AttachmentIDs = nil
				for _, attachment := range attachments {
					s.cacheAttachmentLocked(attachment)
				}
			}
		}
	}
	if s.content.Shares != nil {
		s.mu.Unlock()
		shares, err := s.content.Shares.ListSharesByUser(ctx, userID)
		s.mu.Lock()
		if err != nil {
			return err
		}
		for _, share := range shares {
			s.cacheShareLocked(share)
		}
	}
	return nil
}
