package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"pastebox/internal/plans"
)

const maxAttachmentUploadBytes int64 = 5 << 30

type PreparedAttachmentUpload struct {
	FileName    string
	ContentType string
	Size        int64
	SHA256      string
	ImageWidth  int
	ImageHeight int

	file *os.File
	path string
}

type AttachmentDownload struct {
	Attachment AttachmentView
	Body       io.ReadCloser
	Size       int64
}

func PrepareAttachmentUpload(fileName string, contentType string, body io.Reader) (*PreparedAttachmentUpload, error) {
	if body == nil {
		return nil, E(http.StatusBadRequest, "missing_file", "file is required")
	}
	tmp, err := os.CreateTemp("", "pastebox-upload-*")
	if err != nil {
		return nil, fmt.Errorf("create upload temp file: %w", err)
	}
	upload := &PreparedAttachmentUpload{
		FileName: strings.TrimSpace(fileName),
		file:     tmp,
		path:     tmp.Name(),
	}
	if upload.FileName == "" {
		upload.FileName = "attachment"
	}
	defer func() {
		if err != nil {
			_ = upload.Close()
		}
	}()

	hash := sha256.New()
	prefix := &prefixBuffer{limit: 512}
	size, err := copyAttachmentUpload(tmp, body, hash, prefix, maxAttachmentUploadBytes)
	if err != nil {
		return nil, err
	}
	upload.Size = size
	upload.SHA256 = hex.EncodeToString(hash.Sum(nil))
	upload.ContentType = normalizeAttachmentContentType(upload.FileName, contentType, prefix.Bytes())
	upload.ImageWidth, upload.ImageHeight = imageDimensionsFromFile(upload.ContentType, tmp)
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind upload temp file: %w", err)
	}
	return upload, nil
}

func (u *PreparedAttachmentUpload) Close() error {
	if u == nil {
		return nil
	}
	var closeErr error
	if u.file != nil {
		closeErr = u.file.Close()
		u.file = nil
	}
	if u.path != "" {
		removeErr := os.Remove(u.path)
		u.path = ""
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return errors.Join(closeErr, removeErr)
		}
	}
	return closeErr
}

func (u *PreparedAttachmentUpload) reader() (io.Reader, error) {
	if u == nil || u.file == nil {
		return nil, errors.New("prepared attachment upload is closed")
	}
	if _, err := u.file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind upload temp file: %w", err)
	}
	return u.file, nil
}

func (s *Service) AddAttachmentStream(userID string, pasteID string, fileName string, contentType string, body io.Reader) (AttachmentView, error) {
	upload, err := PrepareAttachmentUpload(fileName, contentType, body)
	if err != nil {
		return AttachmentView{}, err
	}
	defer upload.Close()
	return s.AddPreparedAttachment(userID, pasteID, upload)
}

func (s *Service) AddPreparedAttachment(userID string, pasteID string, upload *PreparedAttachmentUpload) (AttachmentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(userID, pasteID)
	if err != nil {
		return AttachmentView{}, err
	}
	user := s.usersByID[userID]
	plan, _ := s.planForUserLocked(user)
	return s.addPreparedAttachmentLocked(user, paste, plan, upload)
}

func (s *Service) AddGuestAttachmentStream(token string, pasteID string, fileName string, contentType string, body io.Reader, turnstileToken string, remoteIP string) (AttachmentView, error) {
	upload, err := PrepareAttachmentUpload(fileName, contentType, body)
	if err != nil {
		return AttachmentView{}, err
	}
	defer upload.Close()
	return s.AddPreparedGuestAttachment(token, pasteID, upload, turnstileToken, remoteIP)
}

func (s *Service) AddPreparedGuestAttachment(token string, pasteID string, upload *PreparedAttachmentUpload, turnstileToken string, remoteIP string) (AttachmentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.runtimeConfig.GuestUploads
	if !cfg.Enabled {
		return AttachmentView{}, E(http.StatusForbidden, "guest_uploads_disabled", "guest uploads are disabled")
	}
	if cfg.RequireTurnstile {
		if err := s.verifyTurnstileLocked(context.Background(), turnstileToken, remoteIP); err != nil {
			return AttachmentView{}, err
		}
	}
	user, err := s.guestUserForTokenLocked(strings.TrimSpace(token))
	if err != nil {
		return AttachmentView{}, err
	}
	paste, err := s.pasteByIDLocked(pasteID)
	if err != nil || paste.UserID != user.ID {
		return AttachmentView{}, E(http.StatusNotFound, "paste_not_found", "paste not found")
	}
	plan := guestPlan(cfg)
	return s.addPreparedAttachmentLocked(user, paste, plan, upload)
}

func (s *Service) OpenAttachment(userID string, attachmentID string) (AttachmentDownload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	attachment, err := s.attachmentByIDLocked(attachmentID)
	if err != nil || attachment.UserID != userID {
		return AttachmentDownload{}, E(http.StatusNotFound, "attachment_not_found", "attachment not found")
	}
	paste, err := s.pasteByIDLocked(attachment.PasteID)
	if err != nil || !s.isPasteVisibleLocked(paste) || attachment.Status != "active" {
		return AttachmentDownload{}, E(http.StatusGone, "attachment_unavailable", "attachment is unavailable")
	}
	if attachment.ScanStatus == "malicious" {
		return AttachmentDownload{}, E(http.StatusForbidden, "malicious_file", "file is blocked")
	}
	object, err := s.objectStreamLocked(attachment)
	if err != nil {
		return AttachmentDownload{}, E(http.StatusGone, "attachment_unavailable", "attachment content is unavailable")
	}
	attachment.DownloadN++
	if err := s.updateAttachmentLocked(attachment); err != nil {
		_ = object.Body.Close()
		return AttachmentDownload{}, err
	}
	return AttachmentDownload{Attachment: viewAttachment(attachment), Body: object.Body, Size: attachment.Size}, nil
}

func (s *Service) OpenSharedAttachment(token string, password string, attachmentID string, viewerUserID string) (AttachmentDownload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	share, paste, err := s.validShareLocked(token, password, viewerUserID, true)
	if err != nil {
		return AttachmentDownload{}, err
	}
	attachment, err := s.attachmentByIDLocked(attachmentID)
	if err != nil || attachment.PasteID != paste.ID || attachment.Status != "active" {
		return AttachmentDownload{}, E(http.StatusNotFound, "attachment_not_found", "attachment not found")
	}
	if attachment.ScanStatus != "clean" {
		return AttachmentDownload{}, E(http.StatusForbidden, "scan_not_clean", "public downloads require clean scan status")
	}
	owner := s.usersByID[share.UserID]
	plan, _ := s.planForUserLocked(owner)
	downloadBytes, err := s.dailyMetricLocked(share.UserID, "share_download")
	if err != nil {
		return AttachmentDownload{}, err
	}
	if downloadBytes+attachment.Size > plan.DailyShareDownloadBytes {
		return AttachmentDownload{}, E(http.StatusForbidden, "daily_download_limit", "daily share download traffic exceeds plan limit")
	}
	object, err := s.objectStreamLocked(attachment)
	if err != nil {
		return AttachmentDownload{}, E(http.StatusGone, "attachment_unavailable", "attachment content is unavailable")
	}
	now := s.now().UTC()
	if err := s.recordDailyShareDownloadLocked(share.UserID, attachment.Size); err != nil {
		_ = object.Body.Close()
		return AttachmentDownload{}, err
	}
	share.DownloadCount++
	share.LastDownloadedAt = &now
	attachment.DownloadN++
	if err := s.updateShareLocked(share); err != nil {
		_ = object.Body.Close()
		return AttachmentDownload{}, err
	}
	if err := s.updateAttachmentLocked(attachment); err != nil {
		_ = object.Body.Close()
		return AttachmentDownload{}, err
	}
	return AttachmentDownload{Attachment: viewAttachment(attachment), Body: object.Body, Size: attachment.Size}, nil
}

func (s *Service) addPreparedAttachmentLocked(user *User, paste *Paste, plan plans.Plan, upload *PreparedAttachmentUpload) (AttachmentView, error) {
	if upload == nil {
		return AttachmentView{}, E(http.StatusBadRequest, "missing_file", "file is required")
	}
	if !s.isPasteVisibleLocked(paste) {
		return AttachmentView{}, E(http.StatusGone, "paste_expired", "cannot attach to expired paste")
	}
	if len(paste.AttachmentIDs)+1 > plan.AttachmentsPerPasteLimit {
		return AttachmentView{}, E(http.StatusBadRequest, "too_many_attachments", "attachment count exceeds plan limit")
	}
	if upload.Size > plan.SingleFileBytes {
		return AttachmentView{}, E(http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds plan limit")
	}
	if s.pasteSizeLocked(paste)+upload.Size > plan.SinglePasteBytes {
		return AttachmentView{}, E(http.StatusRequestEntityTooLarge, "paste_too_large", "paste exceeds plan total size")
	}
	if err := s.ensureCanCreatePasteLocked(user, plan, PasteInput{ExpiresInSeconds: int64(paste.ExpiresAt.Sub(s.now().UTC()).Seconds())}, upload.Size, 1); err != nil {
		return AttachmentView{}, err
	}
	return s.createPreparedAttachmentForPasteLocked(user.ID, paste, upload)
}

func (s *Service) createPreparedAttachmentForPasteLocked(userID string, paste *Paste, upload *PreparedAttachmentUpload) (AttachmentView, error) {
	now := s.now().UTC()
	status, scanStatus, risk := "active", "pending", classifyAttachmentRisk(upload.FileName, upload.ContentType)
	objectKey := userID + "/" + upload.SHA256
	existingObjectRefs := s.objectRefs[objectKey]
	reader, err := upload.reader()
	if err != nil {
		return AttachmentView{}, err
	}
	if err := s.putObjectStreamLocked(objectKey, reader, upload.Size, upload.ContentType); err != nil {
		return AttachmentView{}, err
	}
	attachment := &Attachment{
		ID:          s.newID("att"),
		UserID:      userID,
		PasteID:     paste.ID,
		FileName:    sanitizeFileName(upload.FileName),
		ContentType: upload.ContentType,
		Size:        upload.Size,
		SHA256:      upload.SHA256,
		ObjectKey:   objectKey,
		Status:      status,
		ScanStatus:  scanStatus,
		Risk:        risk,
		ImageWidth:  upload.ImageWidth,
		ImageHeight: upload.ImageHeight,
		CreatedAt:   now,
	}
	if err := s.createAttachmentLocked(attachment); err != nil {
		s.rollbackUnreferencedStoredObjectLocked(attachment.ObjectKey, existingObjectRefs)
		return AttachmentView{}, err
	}
	attachmentCreated := true
	if err := s.incrementObjectRefLocked(attachment, existingObjectRefs, now); err != nil {
		_ = s.deleteAttachmentLocked(attachment)
		s.rollbackUnreferencedStoredObjectLocked(attachment.ObjectKey, existingObjectRefs)
		return AttachmentView{}, err
	}
	previousPasteScanStatus := paste.ScanStatus
	previousPasteUpdatedAt := paste.UpdatedAt
	paste.ScanStatus = aggregateScanStatus(s.attachmentsForPasteLocked(paste))
	paste.UpdatedAt = now
	if err := s.updatePasteLocked(paste); err != nil {
		s.rollbackAttachmentCreateLocked(paste, previousPasteScanStatus, previousPasteUpdatedAt, attachment, attachmentCreated, false, false)
		return AttachmentView{}, err
	}
	pasteUpdated := true
	scanQueueCreated := false
	if err := s.scheduleScanJobLocked(attachment.ID, now); err != nil {
		s.rollbackAttachmentCreateLocked(paste, previousPasteScanStatus, previousPasteUpdatedAt, attachment, attachmentCreated, pasteUpdated, false)
		return AttachmentView{}, err
	}
	scanQueueCreated = true
	if err := s.recordDailyUploadLocked(userID, upload.Size); err != nil {
		s.rollbackAttachmentCreateLocked(paste, previousPasteScanStatus, previousPasteUpdatedAt, attachment, attachmentCreated, pasteUpdated, scanQueueCreated)
		return AttachmentView{}, err
	}
	return viewAttachment(attachment), nil
}

func (s *Service) putObjectStreamLocked(key string, content io.Reader, size int64, contentType string) error {
	if s.objectStore != nil {
		if streaming, ok := s.objectStore.(StreamingObjectStore); ok {
			if err := streaming.PutObjectStream(context.Background(), key, content, size, contentType); err != nil {
				return fmt.Errorf("put object: %w", err)
			}
			return nil
		}
		data, err := io.ReadAll(content)
		if err != nil {
			return fmt.Errorf("read object content: %w", err)
		}
		if err := s.objectStore.PutObject(context.Background(), key, data, contentType); err != nil {
			return fmt.Errorf("put object: %w", err)
		}
		return nil
	}
	data, err := io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("read object content: %w", err)
	}
	s.objects[key] = data
	return nil
}

func (s *Service) objectStreamLocked(attachment *Attachment) (ObjectStream, error) {
	if s.objectStore != nil {
		if streaming, ok := s.objectStore.(StreamingObjectStore); ok {
			return streaming.OpenObject(context.Background(), attachment.ObjectKey)
		}
		content, err := s.objectStore.GetObject(context.Background(), attachment.ObjectKey)
		if err != nil {
			return ObjectStream{}, err
		}
		return ObjectStream{Body: io.NopCloser(bytes.NewReader(content)), Size: int64(len(content)), ContentType: attachment.ContentType}, nil
	}
	content, ok := s.objects[attachment.ObjectKey]
	if !ok {
		return ObjectStream{}, ErrObjectNotFound
	}
	return ObjectStream{Body: io.NopCloser(bytes.NewReader(content)), Size: int64(len(content)), ContentType: attachment.ContentType}, nil
}

func copyAttachmentUpload(dst *os.File, src io.Reader, hash io.Writer, prefix *prefixBuffer, limit int64) (int64, error) {
	buf := make([]byte, 128*1024)
	var total int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if total+int64(n) > limit {
				return total, E(http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds maximum upload limit")
			}
			chunk := buf[:n]
			if _, err := dst.Write(chunk); err != nil {
				return total, fmt.Errorf("write upload temp file: %w", err)
			}
			if _, err := hash.Write(chunk); err != nil {
				return total, fmt.Errorf("hash upload content: %w", err)
			}
			if _, err := prefix.Write(chunk); err != nil {
				return total, fmt.Errorf("buffer upload prefix: %w", err)
			}
			total += int64(n)
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, E(http.StatusBadRequest, "read_failed", "failed to read file")
		}
	}
}

func normalizeAttachmentContentType(fileName string, contentType string, prefix []byte) string {
	normalized := strings.TrimSpace(contentType)
	if normalized == "" || normalized == "application/octet-stream" {
		normalized = http.DetectContentType(prefix)
	}
	if ext := strings.ToLower(filepath.Ext(fileName)); normalized == "application/octet-stream" && ext != "" {
		if guessed := mime.TypeByExtension(ext); guessed != "" {
			normalized = guessed
		}
	}
	return normalized
}

func imageDimensionsFromFile(contentType string, file *os.File) (int, int) {
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return 0, 0
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

type prefixBuffer struct {
	buf   []byte
	limit int
}

func (w *prefixBuffer) Write(p []byte) (int, error) {
	remaining := w.limit - len(w.buf)
	if remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		w.buf = append(w.buf, p[:remaining]...)
	}
	return len(p), nil
}

func (w *prefixBuffer) Bytes() []byte {
	return w.buf
}
