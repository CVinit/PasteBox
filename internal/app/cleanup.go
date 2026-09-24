package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

func (s *Service) RunAttachmentScan(scanner Scanner, attachmentID string) error {
	return s.RunAttachmentScanWithContext(context.Background(), scanner, attachmentID)
}

func (s *Service) RunAttachmentScanWithContext(ctx context.Context, scanner Scanner, attachmentID string) error {
	if scanner == nil {
		return errors.New("scanner is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	attachment, err := s.attachmentByIDLocked(ctx, attachmentID)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if attachment.Status != "active" || attachment.ScanStatus == "malicious" {
		s.mu.Unlock()
		return nil
	}
	fileName := attachment.FileName
	contentType := attachment.ContentType
	objectKey := attachment.ObjectKey
	snapshot := *attachment
	s.mu.Unlock()

	object, err := s.objectStream(ctx, &snapshot)
	if err != nil {
		return err
	}
	defer object.Body.Close()
	var result ScanResult
	var scanErr error
	if streaming, ok := scanner.(StreamingScanner); ok {
		result, scanErr = streaming.ScanStream(ctx, fileName, contentType, object.Body, object.Size)
	} else {
		const legacyScanMemoryLimit = 64 << 20
		content, readErr := io.ReadAll(io.LimitReader(object.Body, legacyScanMemoryLimit+1))
		if readErr != nil {
			return readErr
		}
		if len(content) > legacyScanMemoryLimit {
			return fmt.Errorf("legacy scanner cannot buffer attachments larger than %d bytes", legacyScanMemoryLimit)
		}
		result, scanErr = scanner.Scan(ctx, fileName, contentType, content)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	attachment, err = s.attachmentByIDLocked(ctx, attachmentID)
	if err != nil {
		return err
	}
	if attachment.Status != "active" || attachment.ScanStatus == "malicious" || attachment.ObjectKey != objectKey {
		return nil
	}
	if scanErr != nil {
		if err := s.applyAttachmentScanResultLocked(ctx, attachment, ScanResult{Status: "scan_failed", Risk: "scanner_unavailable"}); err != nil {
			return err
		}
		return scanErr
	}
	switch result.Status {
	case "clean", "malicious", "scan_failed":
		return s.applyAttachmentScanResultLocked(ctx, attachment, result)
	default:
		if err := s.applyAttachmentScanResultLocked(ctx, attachment, ScanResult{Status: "scan_failed", Risk: "invalid_scanner_verdict"}); err != nil {
			return err
		}
		return fmt.Errorf("invalid scanner verdict %q", result.Status)
	}
}

func (s *Service) applyAttachmentScanResultLocked(ctx context.Context, attachment *Attachment, result ScanResult) error {
	now := s.now().UTC()
	attachment.ScanStatus = result.Status
	attachment.Risk = result.Risk
	if attachment.ScanStatus == "malicious" && attachment.Risk == "" {
		attachment.Risk = "malware_detected"
	}
	if attachment.ScanStatus == "clean" && attachment.Risk == "" {
		attachment.Risk = classifyAttachmentRisk(attachment.FileName, attachment.ContentType)
	}
	if err := s.updateAttachmentLocked(ctx, attachment); err != nil {
		return err
	}
	paste, err := s.pasteByIDLocked(ctx, attachment.PasteID)
	if err != nil {
		return err
	}
	paste.ScanStatus = aggregateScanStatus(s.attachmentsForPasteLocked(paste))
	paste.UpdatedAt = now
	if err := s.updatePasteLocked(ctx, paste); err != nil {
		return err
	}
	if attachment.ScanStatus == "scan_failed" {
		return s.markScanFailureLocked(ctx, attachment.ID, defaultString(attachment.Risk, "scan_failed"), now)
	}
	if attachment.ScanStatus == "clean" {
		if err := s.deleteQueueItemsByKindTargetLocked(ctx, &s.scanFailures, "scan_failed", attachment.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RunCleanup(actorID string) (map[string]int, error) {
	return s.RunCleanupWithContext(context.Background(), actorID)
}

func (s *Service) RunCleanupWithContext(ctx context.Context, actorID string) (map[string]int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if store, ok := s.content.Pastes.(CleanupPasteStore); ok {
		if coordinator, ok := s.content.Pastes.(CleanupCoordinator); ok {
			var result map[string]int
			err := coordinator.WithCleanupLock(ctx, func(ctx context.Context) error {
				var err error
				result, err = s.runBoundedCleanup(ctx, actorID, store)
				return err
			})
			return result, err
		}
		return s.runBoundedCleanup(ctx, actorID, store)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if actorID != "" {
		if err := s.requireAdminLocked(ctx, actorID); err != nil {
			return nil, err
		}
	}
	if err := s.refreshContentCachesLocked(ctx); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	result := map[string]int{"expired": 0, "deletedAttachments": 0, "deletedPastes": 0}
	for _, paste := range s.pastesByID {
		expired, deletedAttachments, deletedPastes, err := s.cleanupPasteLocked(ctx, paste, now)
		if err != nil {
			return nil, err
		}
		result["expired"] += expired
		result["deletedAttachments"] += deletedAttachments
		result["deletedPastes"] += deletedPastes
	}
	return result, nil
}

func (s *Service) runBoundedCleanup(ctx context.Context, actorID string, store CleanupPasteStore) (map[string]int, error) {
	if actorID != "" {
		actor, err := s.activeUserWithContext(ctx, actorID)
		if err != nil {
			return nil, err
		}
		if actor.Role != "admin" {
			return nil, E(http.StatusForbidden, "admin_required", "admin role required")
		}
	}
	const batchLimit = 100
	result := map[string]int{"expired": 0, "deletedAttachments": 0, "deletedPastes": 0}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pastes, err := store.ListPastesForCleanup(ctx, batchLimit)
		if err != nil {
			return nil, err
		}
		if len(pastes) == 0 {
			return result, nil
		}
		progressed := false
		for _, loadedPaste := range pastes {
			var attachments []Attachment
			if s.content.Attachments != nil {
				attachments, err = s.content.Attachments.ListAttachmentsByPaste(ctx, loadedPaste.ID)
				if err != nil {
					return nil, fmt.Errorf("load cleanup attachments: %w", err)
				}
			}

			s.mu.Lock()
			loadedPaste.AttachmentIDs = nil
			paste := s.cachePasteLocked(loadedPaste)
			for _, attachment := range attachments {
				s.cacheAttachmentLocked(attachment)
			}
			expired, deletedAttachments, deletedPastes, cleanupErr := s.cleanupPasteLocked(ctx, paste, s.now().UTC())
			s.mu.Unlock()
			if cleanupErr != nil {
				return nil, cleanupErr
			}
			result["expired"] += expired
			result["deletedAttachments"] += deletedAttachments
			result["deletedPastes"] += deletedPastes
			progressed = progressed || expired > 0 || deletedAttachments > 0 || deletedPastes > 0
		}
		if !progressed {
			return result, nil
		}
	}
}

func (s *Service) cleanupPasteLocked(ctx context.Context, paste *Paste, now time.Time) (int, int, int, error) {
	if paste == nil {
		return 0, 0, 0, nil
	}
	expired := 0
	deletedAttachments := 0
	deletedPastes := 0
	if paste.Status == "active" && !paste.ExpiresAt.After(now) {
		paste.Status = "pending_delete"
		paste.UpdatedAt = now
		expired++
		if err := s.updatePasteLocked(ctx, paste); err != nil {
			return 0, 0, 0, err
		}
		for _, id := range paste.AttachmentIDs {
			if att := s.attachmentsByID[id]; att != nil {
				att.Status = "pending_delete"
				if err := s.updateAttachmentLocked(ctx, att); err != nil {
					return 0, 0, 0, err
				}
			}
		}
	}
	if paste.Status != "pending_delete" {
		return expired, 0, 0, nil
	}

	allDeleted := true
	for _, id := range paste.AttachmentIDs {
		att := s.attachmentsByID[id]
		if att == nil {
			continue
		}
		if att.Status == "pending_delete" {
			previousAttachment := *att
			previousAttachment.Content = append([]byte(nil), att.Content...)
			previousRefs := s.objectRefs[att.ObjectKey]
			att.Status = "deleted"
			att.Content = nil
			if previousRefs <= 1 {
				if err := s.updateAttachmentLocked(ctx, att); err != nil {
					s.cacheAttachmentLocked(previousAttachment)
					return 0, 0, 0, err
				}
				if err := s.decrementObjectRefLocked(ctx, att); err != nil {
					restoreErr := s.updateAttachmentLocked(ctx, &previousAttachment)
					if restoreErr != nil {
						return 0, 0, 0, errors.Join(err, restoreErr)
					}
					return 0, 0, 0, err
				}
			} else {
				if err := s.decrementObjectRefLocked(ctx, att); err != nil {
					if _, atomic := s.content.ObjectRefs.(AtomicObjectRefStore); atomic {
						s.objectRefs[att.ObjectKey] = previousRefs
					} else {
						_ = s.restoreObjectRefAfterCleanupFailureLocked(ctx, &previousAttachment, previousRefs, now)
					}
					s.cacheAttachmentLocked(previousAttachment)
					return 0, 0, 0, err
				}
				if err := s.updateAttachmentLocked(ctx, att); err != nil {
					restoreErr := s.restoreObjectRefAfterCleanupFailureLocked(ctx, &previousAttachment, previousRefs, now)
					s.cacheAttachmentLocked(previousAttachment)
					if restoreErr != nil {
						return 0, 0, 0, errors.Join(err, restoreErr)
					}
					return 0, 0, 0, err
				}
			}
			deletedAttachments++
		}
		if att.Status != "deleted" {
			allDeleted = false
		}
	}
	if allDeleted {
		paste.Status = "deleted"
		paste.UpdatedAt = now
		if err := s.updatePasteLocked(ctx, paste); err != nil {
			return 0, 0, 0, err
		}
		deletedPastes++
	}
	return expired, deletedAttachments, deletedPastes, nil
}
