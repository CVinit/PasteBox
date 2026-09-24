package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

func (s *Service) AddAttachmentWithContext(ctx context.Context, userID string, pasteID string, fileName string, contentType string, content []byte) (AttachmentView, error) {
	return s.AddAttachmentStreamWithContext(ctx, userID, pasteID, fileName, contentType, bytes.NewReader(content))
}

func (s *Service) DownloadAttachmentWithContext(ctx context.Context, userID string, attachmentID string) (AttachmentView, []byte, error) {
	download, err := s.OpenAttachmentWithContext(ctx, userID, attachmentID)
	if err != nil {
		return AttachmentView{}, nil, err
	}
	defer download.Body.Close()
	content, err := io.ReadAll(download.Body)
	if err != nil {
		return AttachmentView{}, nil, fmt.Errorf("read attachment: %w", err)
	}
	return download.Attachment, content, nil
}

func (s *Service) removeQueueItemLocked(queue *[]*QueueItem, targetID string) {
	filtered := (*queue)[:0]
	for _, item := range *queue {
		if item.TargetID != targetID {
			filtered = append(filtered, item)
		}
	}
	*queue = filtered
}

func (s *Service) rollbackAttachmentCreateLocked(ctx context.Context, paste *Paste, previousScanStatus string, previousUpdatedAt time.Time, attachment *Attachment, stored preparedObjectStorage, attachmentCreated bool, pasteUpdated bool, scanQueueCreated bool) {
	if scanQueueCreated {
		_ = s.deleteQueueItemsByKindTargetLocked(ctx, &s.scanJobs, "scan", attachment.ID)
		_ = s.deleteQueueItemsByKindTargetLocked(ctx, &s.scanFailures, "scan_failed", attachment.ID)
	}
	if pasteUpdated {
		paste.ScanStatus = previousScanStatus
		paste.UpdatedAt = previousUpdatedAt
		_ = s.updatePasteLocked(ctx, paste)
	} else {
		paste.ScanStatus = previousScanStatus
		paste.UpdatedAt = previousUpdatedAt
	}
	if attachmentCreated {
		_ = s.deleteAttachmentLocked(ctx, attachment)
	}
	if !stored.refReserved {
		s.rollbackStoredObjectLocked(ctx, attachment.ObjectKey)
	}
}

func (s *Service) rollbackStoredObjectLocked(ctx context.Context, objectKey string) {
	if objectKey == "" {
		return
	}
	if _, atomic := s.content.ObjectRefs.(AtomicObjectRefStore); atomic {
		return
	}
	if refs := s.objectRefs[objectKey]; refs > 0 {
		s.objectRefs[objectKey] = refs - 1
		if s.objectRefs[objectKey] > 0 {
			if attachment := s.attachmentForObjectKeyLocked(objectKey); attachment != nil {
				_ = s.persistObjectRefLocked(ctx, attachment, s.objectRefs[objectKey], s.now().UTC())
			}
			return
		}
	}
	delete(s.objectRefs, objectKey)
	if err := s.deleteObjectLocked(ctx, objectKey); err != nil {
		return
	}
	if s.content.ObjectRefs != nil {
		if err := s.content.ObjectRefs.DeleteObjectRef(ctx, objectKey); err != nil && !isStoreNotFound(err) {
			return
		}
	}
}

func (s *Service) rollbackUnreferencedStoredObjectLocked(ctx context.Context, objectKey string, previousRefs int) {
	if previousRefs > 0 {
		return
	}
	_ = s.deleteObjectLocked(ctx, objectKey)
}

func (s *Service) objectContentWithContext(ctx context.Context, attachment *Attachment) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.objectStore != nil {
		content, err := s.objectStore.GetObject(ctx, attachment.ObjectKey)
		if err != nil {
			return nil, err
		}
		return content, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	content, ok := s.objects[attachment.ObjectKey]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return append([]byte(nil), content...), nil
}

func (s *Service) objectContentLocked(ctx context.Context, attachment *Attachment) ([]byte, error) {
	if s.objectStore != nil {
		key := attachment.ObjectKey
		s.mu.Unlock()
		content, err := s.objectStore.GetObject(ctx, key)
		s.mu.Lock()
		if err != nil {
			return nil, err
		}
		return content, nil
	}
	content, ok := s.objects[attachment.ObjectKey]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return append([]byte(nil), content...), nil
}

func (s *Service) deleteObjectWithContext(ctx context.Context, key string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.objectStore != nil {
		if err := s.objectStore.DeleteObject(ctx, key); err != nil && !errors.Is(err, ErrObjectNotFound) {
			return fmt.Errorf("delete object: %w", err)
		}
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *Service) deleteObjectLocked(ctx context.Context, key string) error {
	if s.objectStore != nil {
		s.mu.Unlock()
		err := s.objectStore.DeleteObject(ctx, key)
		s.mu.Lock()
		if err != nil && !errors.Is(err, ErrObjectNotFound) {
			return fmt.Errorf("delete object: %w", err)
		}
		return nil
	}
	delete(s.objects, key)
	return nil
}

func aggregateScanStatus(attachments []*Attachment) string {
	status := "clean"
	for _, attachment := range attachments {
		if attachment.ScanStatus == "malicious" {
			return "malicious"
		}
		if attachment.ScanStatus == "scan_failed" {
			status = "scan_failed"
		}
		if attachment.ScanStatus == "pending" && status == "clean" {
			status = "pending"
		}
	}
	return status
}

func sanitizeFileName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "/" || name == "" {
		return "attachment.bin"
	}
	return name
}

func (s *Service) DownloadAttachment(userID string, attachmentID string) (AttachmentView, []byte, error) {
	return s.DownloadAttachmentWithContext(context.Background(), userID, attachmentID)
}

func (s *Service) AddAttachment(userID string, pasteID string, fileName string, contentType string, content []byte) (AttachmentView, error) {
	return s.AddAttachmentWithContext(context.Background(), userID, pasteID, fileName, contentType, content)
}
