package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"pastebox/internal/plans"
	"sort"
	"strings"
	"time"
)

func (s *Service) ReportWithContext(ctx context.Context, userID string, target string, reason string) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if userID != "" {
		if _, err := s.activeUserLocked(ctx, userID); err != nil {
			return Report{}, err
		}
	}
	report := &Report{ID: s.newID("rpt"), UserID: userID, Target: target, Reason: strings.TrimSpace(reason), Status: "open", CreatedAt: s.now().UTC()}
	if err := s.createReportLocked(ctx, report); err != nil {
		return Report{}, err
	}
	actorID := userID
	if actorID == "" {
		actorID = "anonymous"
	}
	if err := s.auditLocked(ctx, actorID, "support.report_created", report.ID, map[string]any{
		"reportedTarget": report.Target,
		"anonymous":      userID == "",
	}); err != nil {
		return Report{}, err
	}
	return *report, nil
}

func (s *Service) AdminResolveReportWithContext(ctx context.Context, actorID string, reportID string, status string) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return Report{}, err
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "resolved"
	}
	if status != "open" && status != "resolved" && status != "dismissed" {
		return Report{}, E(http.StatusBadRequest, "invalid_report_status", "report status must be open, resolved, or dismissed")
	}
	originalReport, err := s.reportByIDLocked(ctx, reportID)
	if err != nil {
		return Report{}, err
	}
	originalStatus := originalReport.Status
	report, err := s.updateReportStatusLocked(ctx, reportID, status)
	if err != nil {
		return Report{}, err
	}
	if err := s.auditLocked(ctx, actorID, "admin.report_status", report.ID, map[string]any{"status": status}); err != nil {
		_, rollbackErr := s.updateReportStatusLocked(ctx, reportID, originalStatus)
		return Report{}, errors.Join(err, rollbackErr)
	}
	return *report, nil
}

func (s *Service) AdminDashboardWithContext(ctx context.Context, actorID string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return nil, err
	}
	metrics, err := s.operationalMetricsLocked(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"users":                 metrics.UserCount,
		"activePastes":          metrics.ActivePastes,
		"activeStorageBytes":    metrics.ActiveStorageBytes,
		"reportsOpen":           metrics.ReportsOpen,
		"cleanupQueueDepth":     metrics.CleanupQueueDepth,
		"scanQueueDepth":        metrics.ScanQueueDepth,
		"scanFailureQueueDepth": metrics.ScanFailureDepth,
		"failedJobQueueDepth":   metrics.FailedJobDepth,
		"orders":                sumOrderCounts(metrics.OrdersByStatus),
		"webhookEvents":         metrics.WebhookEvents,
	}, nil
}

func (s *Service) OperationalMetricsWithContext(ctx context.Context) (OperationalMetrics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.operationalMetricsLocked(ctx)
}

func (s *Service) operationalMetricsLocked(ctx context.Context) (OperationalMetrics, error) {
	if store, ok := s.ops.Orders.(OperationalMetricsStore); ok {
		s.mu.Unlock()
		metrics, err := store.OperationalMetrics(ctx)
		s.mu.Lock()
		return metrics, err
	}
	if err := s.refreshQueueCachesLocked(ctx); err != nil {
		return OperationalMetrics{}, err
	}
	if err := s.refreshMailCacheLocked(ctx); err != nil {
		return OperationalMetrics{}, err
	}
	users, err := s.listUsersLocked(ctx)
	if err != nil {
		return OperationalMetrics{}, err
	}
	var activePastes int
	var storage int64
	for _, paste := range s.pastesByID {
		if s.isPasteVisibleLocked(paste) {
			activePastes++
			storage += s.pasteSizeLocked(paste)
		}
	}
	ordersByStatus := map[string]int{}
	for _, order := range s.ordersByID {
		ordersByStatus[order.Status]++
	}
	failedMails, err := s.mailQueueItemsLocked(ctx, "failed", 1000)
	if err != nil {
		return OperationalMetrics{}, err
	}
	return OperationalMetrics{
		UserCount:           len(users),
		ActivePastes:        activePastes,
		ActiveStorageBytes:  storage,
		ReportsOpen:         countReports(s.reports, "open"),
		CleanupQueueDepth:   len(s.cleanupJobs),
		CleanupFailureDepth: len(s.cleanupFailures),
		ScanQueueDepth:      len(s.scanJobs),
		ScanFailureDepth:    len(s.scanFailures),
		FailedJobDepth:      len(s.failedJobs),
		MailQueueDepth:      len(s.mails),
		MailFailedDepth:     len(failedMails),
		WebhookEvents:       len(s.webhookEvents),
		OrdersByStatus:      ordersByStatus,
	}, nil
}

func (s *Service) AdminUsers(actorID string) ([]UserView, error) {
	return s.AdminUsersWithContext(context.Background(), actorID, ListOptions{})
}

func (s *Service) AdminUsersWithContext(ctx context.Context, actorID string, opts ListOptions) ([]UserView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	limit, offset := normalizePasteListOptions(opts)
	actor, err := s.activeUserWithContext(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if actor.Role != "admin" {
		return nil, E(http.StatusForbidden, "admin_required", "admin role required")
	}

	var users []User
	pageApplied := false
	if store, ok := s.auth.Users.(PagedUserStore); ok {
		users, err = store.ListUsersPage(ctx, limit, offset)
		pageApplied = true
	} else if s.auth.Users != nil {
		users, err = s.auth.Users.ListUsers(ctx)
	} else {
		s.mu.Lock()
		users = make([]User, 0, len(s.usersByID))
		for _, user := range s.usersByID {
			users = append(users, *user)
		}
		s.mu.Unlock()
	}
	if err != nil {
		return nil, fmt.Errorf("load admin users: %w", err)
	}
	if !pageApplied {
		sort.Slice(users, func(i, j int) bool {
			if users[i].CreatedAt.Equal(users[j].CreatedAt) {
				return users[i].ID > users[j].ID
			}
			return users[i].CreatedAt.After(users[j].CreatedAt)
		})
		users = sliceUsersForPage(users, limit, offset)
	}

	out := make([]UserView, 0, len(users))
	for _, user := range users {
		view, err := s.viewUserWithContext(ctx, user)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	s.mu.Lock()
	for _, user := range users {
		s.cacheUserLocked(user)
	}
	s.mu.Unlock()
	return out, nil
}

func sliceUsersForPage(users []User, limit int, offset int) []User {
	if offset >= len(users) {
		return []User{}
	}
	end := offset + limit
	if end > len(users) {
		end = len(users)
	}
	return users[offset:end]
}

func (s *Service) AdminSetUserPlanWithContext(ctx context.Context, actorID string, userID string, planID string, expiresAt *time.Time, reason string, ticketID string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return UserView{}, err
	}
	user, err := s.userByIDLocked(ctx, userID)
	if err != nil {
		return UserView{}, err
	}
	if _, ok := plans.Find(s.catalog, planID); !ok {
		return UserView{}, E(http.StatusBadRequest, "invalid_plan", "plan does not exist")
	}
	originalUser := *user
	originalUser.PlanExpiresAt = cloneTimePtr(user.PlanExpiresAt)
	reason = strings.TrimSpace(reason)
	ticketID = strings.TrimSpace(ticketID)
	if reason == "" && ticketID == "" {
		return UserView{}, E(http.StatusBadRequest, "admin_plan_reason_required", "plan changes require a support reason or ticket id")
	}
	oldPlanID := user.PlanID
	oldExpiresAt := user.PlanExpiresAt
	user.PlanID = planID
	user.PlanExpiresAt = expiresAt
	user.UpdatedAt = s.now().UTC()
	if err := s.updateUserLocked(ctx, user); err != nil {
		return UserView{}, err
	}
	metadata := map[string]any{
		"oldPlanId":    oldPlanID,
		"newPlanId":    planID,
		"oldExpiresAt": oldExpiresAt,
		"newExpiresAt": expiresAt,
	}
	if reason != "" {
		metadata["reason"] = reason
	}
	if ticketID != "" {
		metadata["ticketId"] = ticketID
	}
	if err := s.auditLocked(ctx, actorID, "admin.user_plan_set", userID, metadata); err != nil {
		rollbackErr := s.updateUserLocked(ctx, &originalUser)
		return UserView{}, errors.Join(err, rollbackErr)
	}
	return s.viewUserLocked(ctx, user)
}

func (s *Service) AdminFreezeUserWithContext(ctx context.Context, actorID string, userID string, frozen bool) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return UserView{}, err
	}
	user, err := s.userByIDLocked(ctx, userID)
	if err != nil {
		return UserView{}, err
	}
	originalUser := *user
	originalUser.PlanExpiresAt = cloneTimePtr(user.PlanExpiresAt)
	user.Frozen = frozen
	user.UpdatedAt = s.now().UTC()
	if err := s.updateUserLocked(ctx, user); err != nil {
		return UserView{}, err
	}
	if err := s.auditLocked(ctx, actorID, "admin.user_freeze", userID, map[string]any{"frozen": frozen}); err != nil {
		rollbackErr := s.updateUserLocked(ctx, &originalUser)
		return UserView{}, errors.Join(err, rollbackErr)
	}
	return s.viewUserLocked(ctx, user)
}

func (s *Service) AdminPastes(actorID string) ([]PasteView, error) {
	return s.AdminPastesWithContext(context.Background(), actorID, ListOptions{})
}

func (s *Service) AdminPastesWithContext(ctx context.Context, actorID string, opts ListOptions) ([]PasteView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	limit, offset := normalizePasteListOptions(opts)
	actor, err := s.activeUserWithContext(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if actor.Role != "admin" {
		return nil, E(http.StatusForbidden, "admin_required", "admin role required")
	}

	var pastes []Paste
	pageApplied := false
	if store, ok := s.content.Pastes.(PagedAllPasteStore); ok {
		pastes, err = store.ListPastesPage(ctx, limit, offset)
		pageApplied = true
	} else if s.content.Pastes != nil {
		pastes, err = s.content.Pastes.ListPastes(ctx)
	} else {
		s.mu.Lock()
		pastes = make([]Paste, 0, len(s.pastesByID))
		for _, paste := range s.pastesByID {
			pastes = append(pastes, *paste)
		}
		s.mu.Unlock()
	}
	if err != nil {
		return nil, fmt.Errorf("load admin pastes: %w", err)
	}
	if !pageApplied {
		sort.Slice(pastes, func(i, j int) bool {
			if pastes[i].CreatedAt.Equal(pastes[j].CreatedAt) {
				return pastes[i].ID > pastes[j].ID
			}
			return pastes[i].CreatedAt.After(pastes[j].CreatedAt)
		})
		pastes = slicePastesForPage(pastes, limit, offset)
	}

	type pasteRecord struct {
		paste       Paste
		attachments []Attachment
		shares      []Share
	}
	records := make([]pasteRecord, 0, len(pastes))
	for _, paste := range pastes {
		record := pasteRecord{paste: paste}
		if s.content.Attachments != nil {
			record.attachments, err = s.content.Attachments.ListAttachmentsByPaste(ctx, paste.ID)
			if err != nil {
				return nil, fmt.Errorf("load admin paste attachments: %w", err)
			}
		}
		if store, ok := s.content.Shares.(SharesByPasteStore); ok {
			record.shares, err = store.ListSharesByPaste(ctx, paste.ID)
			if err != nil {
				return nil, fmt.Errorf("load admin paste shares: %w", err)
			}
		}
		records = append(records, record)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PasteView, 0, len(records))
	for _, record := range records {
		record.paste.AttachmentIDs = nil
		paste := s.cachePasteLocked(record.paste)
		for _, attachment := range record.attachments {
			s.cacheAttachmentLocked(attachment)
		}
		for _, share := range record.shares {
			s.cacheShareLocked(share)
		}
		out = append(out, s.viewPasteLocked(paste))
	}
	return out, nil
}

func slicePastesForPage(pastes []Paste, limit int, offset int) []Paste {
	if offset >= len(pastes) {
		return []Paste{}
	}
	end := offset + limit
	if end > len(pastes) {
		end = len(pastes)
	}
	return pastes[offset:end]
}

func (s *Service) AdminAttachments(actorID string, query string) ([]AdminAttachmentView, error) {
	return s.AdminAttachmentsWithContext(context.Background(), actorID, ListOptions{Query: query})
}

func (s *Service) AdminAttachmentsWithContext(ctx context.Context, actorID string, opts ListOptions) ([]AdminAttachmentView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	limit, offset := normalizePasteListOptions(opts)
	actor, err := s.activeUserWithContext(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if actor.Role != "admin" {
		return nil, E(http.StatusForbidden, "admin_required", "admin role required")
	}

	query := strings.ToLower(strings.TrimSpace(opts.Query))
	var attachments []Attachment
	pageApplied := false
	if store, ok := s.content.Attachments.(PagedAttachmentStore); ok {
		attachments, err = store.ListAttachmentsPage(ctx, query, limit, offset)
		pageApplied = true
	} else if s.content.Attachments != nil {
		attachments, err = s.content.Attachments.ListAttachments(ctx)
	} else {
		s.mu.Lock()
		attachments = make([]Attachment, 0, len(s.attachmentsByID))
		for _, attachment := range s.attachmentsByID {
			attachments = append(attachments, *attachment)
		}
		s.mu.Unlock()
	}
	if err != nil {
		return nil, fmt.Errorf("load admin attachments: %w", err)
	}
	if !pageApplied {
		filtered := attachments[:0]
		for _, attachment := range attachments {
			haystack := strings.ToLower(attachment.UserID + "\n" + attachment.FileName + "\n" + attachment.SHA256 + "\n" + attachment.Status + "\n" + attachment.ScanStatus)
			if query == "" || strings.Contains(haystack, query) {
				filtered = append(filtered, attachment)
			}
		}
		attachments = filtered
		sort.Slice(attachments, func(i, j int) bool {
			if attachments[i].CreatedAt.Equal(attachments[j].CreatedAt) {
				return attachments[i].ID > attachments[j].ID
			}
			return attachments[i].CreatedAt.After(attachments[j].CreatedAt)
		})
		attachments = sliceAttachmentsForPage(attachments, limit, offset)
	}

	type attachmentRecord struct {
		attachment Attachment
		pasteTitle string
		paste      *Paste
	}
	records := make([]attachmentRecord, 0, len(attachments))
	for _, attachment := range attachments {
		record := attachmentRecord{attachment: attachment}
		if s.content.Pastes != nil {
			paste, pasteErr := s.content.Pastes.PasteByID(ctx, attachment.PasteID)
			if pasteErr != nil && !isStoreNotFound(pasteErr) {
				return nil, fmt.Errorf("load admin attachment paste: %w", pasteErr)
			}
			if pasteErr == nil {
				record.paste = &paste
				record.pasteTitle = paste.Title
			}
		}
		records = append(records, record)
	}

	out := make([]AdminAttachmentView, 0, len(records))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range records {
		if record.paste != nil {
			s.cachePasteLocked(*record.paste)
		}
		s.cacheAttachmentLocked(record.attachment)
		out = append(out, AdminAttachmentView{
			AttachmentView: viewAttachment(&record.attachment),
			UserID:         record.attachment.UserID,
			PasteTitle:     record.pasteTitle,
		})
	}
	return out, nil
}

func sliceAttachmentsForPage(attachments []Attachment, limit int, offset int) []Attachment {
	if offset >= len(attachments) {
		return []Attachment{}
	}
	end := offset + limit
	if end > len(attachments) {
		end = len(attachments)
	}
	return attachments[offset:end]
}

func (s *Service) AdminShares(actorID string) ([]AdminShareView, error) {
	return s.AdminSharesWithContext(context.Background(), actorID, ListOptions{})
}

func (s *Service) AdminSharesWithContext(ctx context.Context, actorID string, opts ListOptions) ([]AdminShareView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	limit, offset := normalizePasteListOptions(opts)
	actor, err := s.activeUserWithContext(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if actor.Role != "admin" {
		return nil, E(http.StatusForbidden, "admin_required", "admin role required")
	}

	var shares []Share
	pageApplied := false
	if store, ok := s.content.Shares.(PagedShareStore); ok {
		shares, err = store.ListSharesPage(ctx, limit, offset)
		pageApplied = true
	} else if s.content.Shares != nil {
		shares, err = s.content.Shares.ListShares(ctx)
	} else {
		s.mu.Lock()
		shares = make([]Share, 0, len(s.sharesByID))
		for _, share := range s.sharesByID {
			shares = append(shares, *share)
		}
		s.mu.Unlock()
	}
	if err != nil {
		return nil, fmt.Errorf("load admin shares: %w", err)
	}
	if !pageApplied {
		sort.Slice(shares, func(i, j int) bool {
			if shares[i].CreatedAt.Equal(shares[j].CreatedAt) {
				return shares[i].ID > shares[j].ID
			}
			return shares[i].CreatedAt.After(shares[j].CreatedAt)
		})
		shares = sliceSharesForPage(shares, limit, offset)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AdminShareView, 0, len(shares))
	for _, share := range shares {
		cached := s.cacheShareLocked(share)
		out = append(out, AdminShareView{ShareView: s.viewShareLocked(cached), UserID: share.UserID})
	}
	return out, nil
}

func sliceSharesForPage(shares []Share, limit int, offset int) []Share {
	if offset >= len(shares) {
		return []Share{}
	}
	end := offset + limit
	if end > len(shares) {
		end = len(shares)
	}
	return shares[offset:end]
}

func (s *Service) AdminOrdersWithContext(ctx context.Context, actorID string) ([]Order, error) {
	return s.AdminOrdersPage(ctx, actorID, ListOptions{})
}

func (s *Service) AdminOrdersPage(ctx context.Context, actorID string, opts ListOptions) ([]Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return nil, err
	}
	limit, offset := normalizePasteListOptions(opts)
	if store, ok := s.ops.Orders.(PagedOrderStore); ok {
		s.mu.Unlock()
		rows, err := store.ListOrdersPage(ctx, limit, offset)
		s.mu.Lock()
		return rows, err
	}
	out := make([]Order, 0, len(s.ordersByID))
	for _, order := range s.ordersByID {
		out = append(out, *order)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if offset >= len(out) {
		return []Order{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}

func (s *Service) AdminWebhookEventsWithContext(ctx context.Context, actorID string) ([]WebhookEvent, error) {
	return s.AdminWebhookEventsPage(ctx, actorID, ListOptions{})
}

func (s *Service) AdminWebhookEventsPage(ctx context.Context, actorID string, opts ListOptions) ([]WebhookEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return nil, err
	}
	limit, offset := normalizePasteListOptions(opts)
	if store, ok := s.ops.WebhookEvents.(PagedWebhookEventStore); ok {
		s.mu.Unlock()
		rows, err := store.ListWebhookEventsPage(ctx, limit, offset)
		s.mu.Lock()
		return rows, err
	}
	out := make([]WebhookEvent, 0, len(s.webhookEvents))
	for _, event := range s.webhookEvents {
		out = append(out, *event)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReceivedAt.After(out[j].ReceivedAt) })
	if offset >= len(out) {
		return []WebhookEvent{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}

func (s *Service) AdminTakedownPasteWithContext(ctx context.Context, actorID string, pasteID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return err
	}
	paste, err := s.pasteByIDLocked(ctx, pasteID)
	if err != nil {
		return err
	}
	originalPaste := *paste
	originalPaste.AttachmentIDs = append([]string(nil), paste.AttachmentIDs...)
	paste.Status = "taken_down"
	paste.UpdatedAt = s.now().UTC()
	if err := s.updatePasteLocked(ctx, paste); err != nil {
		return err
	}
	if err := s.auditLocked(ctx, actorID, "admin.paste_takedown", pasteID, nil); err != nil {
		rollbackErr := s.updatePasteLocked(ctx, &originalPaste)
		return errors.Join(err, rollbackErr)
	}
	return nil
}

func (s *Service) AdminFreezeAttachmentWithContext(ctx context.Context, actorID string, attachmentID string, frozen bool) (AttachmentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return AttachmentView{}, err
	}
	attachment, err := s.attachmentByIDLocked(ctx, attachmentID)
	if err != nil {
		return AttachmentView{}, err
	}
	originalAttachment := *attachment
	var originalPaste *Paste
	if paste := s.pastesByID[attachment.PasteID]; paste != nil {
		copyPaste := *paste
		copyPaste.AttachmentIDs = append([]string(nil), paste.AttachmentIDs...)
		originalPaste = &copyPaste
	}
	if frozen {
		attachment.Status = "frozen"
		attachment.Risk = defaultString(attachment.Risk, "admin_frozen")
	} else {
		attachment.Status = "active"
		if attachment.Risk == "admin_frozen" {
			attachment.Risk = ""
		}
	}
	if err := s.updateAttachmentLocked(ctx, attachment); err != nil {
		return AttachmentView{}, err
	}
	if paste := s.pastesByID[attachment.PasteID]; paste != nil {
		paste.ScanStatus = aggregateScanStatus(s.attachmentsForPasteLocked(paste))
		paste.UpdatedAt = s.now().UTC()
		if err := s.updatePasteLocked(ctx, paste); err != nil {
			return AttachmentView{}, err
		}
	}
	if err := s.auditLocked(ctx, actorID, "admin.attachment_freeze", attachmentID, map[string]any{"frozen": frozen}); err != nil {
		rollbackAttachmentErr := s.updateAttachmentLocked(ctx, &originalAttachment)
		var rollbackPasteErr error
		if originalPaste != nil {
			rollbackPasteErr = s.updatePasteLocked(ctx, originalPaste)
		}
		return AttachmentView{}, errors.Join(err, rollbackAttachmentErr, rollbackPasteErr)
	}
	return viewAttachment(attachment), nil
}

func (s *Service) AdminRetryScanWithContext(ctx context.Context, actorID string, attachmentID string) (AttachmentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return AttachmentView{}, err
	}
	attachment, err := s.attachmentByIDLocked(ctx, attachmentID)
	if err != nil {
		return AttachmentView{}, err
	}
	if attachment.ScanStatus == "malicious" {
		return AttachmentView{}, E(http.StatusForbidden, "malicious_file", "malicious files cannot be auto-retried")
	}
	now := s.now().UTC()
	attachment.ScanStatus = "pending"
	attachment.Risk = classifyAttachmentRisk(attachment.FileName, attachment.ContentType)
	if err := s.updateAttachmentLocked(ctx, attachment); err != nil {
		return AttachmentView{}, err
	}
	if err := s.deleteQueueItemsByKindTargetLocked(ctx, &s.scanFailures, "scan_failed", attachment.ID); err != nil {
		return AttachmentView{}, err
	}
	if err := s.deleteQueueItemsByKindTargetLocked(ctx, &s.scanJobs, "scan", attachment.ID); err != nil {
		return AttachmentView{}, err
	}
	if err := s.scheduleScanJobLocked(ctx, attachment.ID, now); err != nil {
		return AttachmentView{}, err
	}
	if paste := s.pastesByID[attachment.PasteID]; paste != nil {
		paste.ScanStatus = aggregateScanStatus(s.attachmentsForPasteLocked(paste))
		paste.UpdatedAt = now
		if err := s.updatePasteLocked(ctx, paste); err != nil {
			return AttachmentView{}, err
		}
	}
	if err := s.auditLocked(ctx, actorID, "admin.scan_retry", attachmentID, nil); err != nil {
		return AttachmentView{}, err
	}
	return viewAttachment(attachment), nil
}

func (s *Service) AdminRevokeShareWithContext(ctx context.Context, actorID string, shareID string) (ShareView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return ShareView{}, err
	}
	share, err := s.shareByIDLocked(ctx, shareID)
	if err != nil {
		return ShareView{}, err
	}
	originalShare := *share
	now := s.now().UTC()
	share.RevokedAt = &now
	if err := s.updateShareLocked(ctx, share); err != nil {
		return ShareView{}, err
	}
	if err := s.auditLocked(ctx, actorID, "admin.share_revoke", shareID, nil); err != nil {
		rollbackErr := s.updateShareLocked(ctx, &originalShare)
		return ShareView{}, errors.Join(err, rollbackErr)
	}
	return s.viewShareLocked(share), nil
}

func (s *Service) AdminAuditLogsWithContext(ctx context.Context, actorID string) ([]AuditLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return nil, err
	}
	if s.audit != nil {
		s.mu.Unlock()
		defer s.mu.Lock()
		return s.audit.AuditLogs(ctx, 100)
	}
	out := make([]AuditLog, 0, len(s.auditLogs))
	for _, log := range s.auditLogs {
		out = append(out, *log)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) AdminQueuesWithContext(ctx context.Context, actorID string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return nil, err
	}

	if err := s.refreshQueueCachesLocked(ctx); err != nil {
		return nil, err
	}
	queuedMails, err := s.mailQueueItemsLocked(ctx, "queued", 100)
	if err != nil {
		return nil, err
	}
	failedMails, err := s.mailQueueItemsLocked(ctx, "failed", 100)
	if err != nil {
		return nil, err
	}
	cleanupFailures := s.cleanupFailures
	if cleanupFailures == nil {
		cleanupFailures = []*QueueItem{}
	}
	cleanupJobs := s.cleanupJobs
	if cleanupJobs == nil {
		cleanupJobs = []*QueueItem{}
	}
	scanJobs := s.scanJobs
	if scanJobs == nil {
		scanJobs = []*QueueItem{}
	}
	scanFailures := s.scanFailures
	if scanFailures == nil {
		scanFailures = []*QueueItem{}
	}
	failedJobs := s.failedJobs
	if failedJobs == nil {
		failedJobs = []*QueueItem{}
	}

	reports := s.reports
	if store, ok := s.ops.Reports.(PagedReportStore); ok {
		s.mu.Unlock()
		loaded, err := store.ListReportsPage(ctx, initialContentCacheLimit, 0)
		s.mu.Lock()
		if err != nil {
			return nil, err
		}
		reports = make([]*Report, 0, len(loaded))
		for _, report := range loaded {
			snapshot := report
			reports = append(reports, &snapshot)
		}
	}
	if reports == nil {
		reports = []*Report{}
	}
	return map[string]any{
		"cleanupJobs":     cleanupJobs,
		"cleanupFailures": cleanupFailures,
		"scanJobs":        scanJobs,
		"scanFailures":    scanFailures,
		"failedJobs":      failedJobs,
		"queuedMails":     queuedMails,
		"failedMails":     failedMails,
		"reports":         reports,
	}, nil
}

func (s *Service) AdminQueues(actorID string) (map[string]any, error) {
	return s.AdminQueuesWithContext(context.Background(), actorID)
}

func (s *Service) AdminAuditLogs(actorID string) ([]AuditLog, error) {
	return s.AdminAuditLogsWithContext(context.Background(), actorID)
}

func (s *Service) AdminRevokeShare(actorID string, shareID string) (ShareView, error) {
	return s.AdminRevokeShareWithContext(context.Background(), actorID, shareID)
}

func (s *Service) AdminRetryScan(actorID string, attachmentID string) (AttachmentView, error) {
	return s.AdminRetryScanWithContext(context.Background(), actorID, attachmentID)
}

func (s *Service) AdminFreezeAttachment(actorID string, attachmentID string, frozen bool) (AttachmentView, error) {
	return s.AdminFreezeAttachmentWithContext(context.Background(), actorID, attachmentID, frozen)
}

func (s *Service) AdminTakedownPaste(actorID string, pasteID string) error {
	return s.AdminTakedownPasteWithContext(context.Background(), actorID, pasteID)
}

func (s *Service) AdminWebhookEvents(actorID string) ([]WebhookEvent, error) {
	return s.AdminWebhookEventsWithContext(context.Background(), actorID)
}

func (s *Service) AdminOrders(actorID string) ([]Order, error) {
	return s.AdminOrdersWithContext(context.Background(), actorID)
}

func (s *Service) AdminFreezeUser(actorID string, userID string, frozen bool) (UserView, error) {
	return s.AdminFreezeUserWithContext(context.Background(), actorID, userID, frozen)
}

func (s *Service) AdminSetUserPlan(actorID string, userID string, planID string, expiresAt *time.Time, reason string, ticketID string) (UserView, error) {
	return s.AdminSetUserPlanWithContext(context.Background(), actorID, userID, planID, expiresAt, reason, ticketID)
}

func (s *Service) OperationalMetrics() (OperationalMetrics, error) {
	return s.OperationalMetricsWithContext(context.Background())
}

func (s *Service) AdminDashboard(actorID string) (map[string]any, error) {
	return s.AdminDashboardWithContext(context.Background(), actorID)
}

func (s *Service) AdminResolveReport(actorID string, reportID string, status string) (Report, error) {
	return s.AdminResolveReportWithContext(context.Background(), actorID, reportID, status)
}

func (s *Service) Report(userID string, target string, reason string) (Report, error) {
	return s.ReportWithContext(context.Background(), userID, target, reason)
}

func sumOrderCounts(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}
