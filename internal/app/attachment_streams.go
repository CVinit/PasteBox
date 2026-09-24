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
	"time"

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

type AttachmentUploadPreflight struct {
	MaxBytes int64

	guestToken string
	pasteID    string
}

type AttachmentDownload struct {
	Attachment AttachmentView
	Body       io.ReadCloser
	Size       int64
}

type preparedObjectStorage struct {
	objectKey   string
	inMemory    bool
	content     []byte
	refReserved bool
}

func PrepareAttachmentUpload(fileName string, contentType string, body io.Reader) (*PreparedAttachmentUpload, error) {
	return PrepareAttachmentUploadWithLimit(fileName, contentType, body, maxAttachmentUploadBytes)
}

func PrepareAttachmentUploadWithLimit(fileName string, contentType string, body io.Reader, maxBytes int64) (*PreparedAttachmentUpload, error) {
	if body == nil {
		return nil, E(http.StatusBadRequest, "missing_file", "file is required")
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	if maxBytes > maxAttachmentUploadBytes {
		maxBytes = maxAttachmentUploadBytes
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
	size, err := copyAttachmentUpload(tmp, body, hash, prefix, maxBytes)
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

func (s *Service) PreflightAttachmentUploadWithContext(ctx context.Context, userID string, pasteID string) (AttachmentUploadPreflight, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(ctx, userID, pasteID)
	if err != nil {
		return AttachmentUploadPreflight{}, err
	}
	user := s.usersByID[userID]
	plan, _ := s.planForUserLocked(user)
	maxBytes, err := s.attachmentUploadLimitLocked(ctx, user, paste, plan)
	if err != nil {
		return AttachmentUploadPreflight{}, err
	}
	return AttachmentUploadPreflight{MaxBytes: maxBytes, pasteID: pasteID}, nil
}

func (s *Service) PreflightGuestAttachmentUpload(ctx context.Context, token string, pasteID string, turnstileToken string, remoteIP string) (AttachmentUploadPreflight, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.runtimeConfig.GuestUploads
	if !cfg.Enabled {
		return AttachmentUploadPreflight{}, E(http.StatusForbidden, "guest_uploads_disabled", "guest uploads are disabled")
	}
	if cfg.RequireTurnstile {
		if err := s.verifyTurnstileLocked(ctx, turnstileToken, remoteIP); err != nil {
			return AttachmentUploadPreflight{}, err
		}
	}
	token = strings.TrimSpace(token)
	user, err := s.guestUserForTokenLocked(ctx, token)
	if err != nil {
		return AttachmentUploadPreflight{}, err
	}
	paste, err := s.pasteByIDLocked(ctx, pasteID)
	if err != nil || paste.UserID != user.ID {
		return AttachmentUploadPreflight{}, E(http.StatusNotFound, "paste_not_found", "paste not found")
	}
	maxBytes, err := s.attachmentUploadLimitLocked(ctx, user, paste, guestPlan(cfg))
	if err != nil {
		return AttachmentUploadPreflight{}, err
	}
	return AttachmentUploadPreflight{MaxBytes: maxBytes, guestToken: token, pasteID: pasteID}, nil
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

func (s *Service) AddAttachmentStreamWithContext(ctx context.Context, userID string, pasteID string, fileName string, contentType string, body io.Reader) (AttachmentView, error) {
	preflight, err := s.PreflightAttachmentUploadWithContext(ctx, userID, pasteID)
	if err != nil {
		return AttachmentView{}, err
	}
	upload, err := PrepareAttachmentUploadWithLimit(fileName, contentType, body, preflight.MaxBytes)
	if err != nil {
		return AttachmentView{}, err
	}
	defer upload.Close()
	return s.AddPreparedAttachmentWithContext(ctx, userID, pasteID, upload)
}

func (s *Service) AddPreparedAttachment(userID string, pasteID string, upload *PreparedAttachmentUpload) (AttachmentView, error) {
	return s.AddPreparedAttachmentWithContext(context.Background(), userID, pasteID, upload)
}

func (s *Service) AddPreparedAttachmentWithContext(ctx context.Context, userID string, pasteID string, upload *PreparedAttachmentUpload) (AttachmentView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	objectKey, err := s.preflightPreparedAttachment(ctx, userID, pasteID, upload)
	if err != nil {
		return AttachmentView{}, err
	}
	releaseObjectKey := s.lockObjectKey(objectKey)
	defer releaseObjectKey()
	stored, err := s.reserveAndStorePreparedObject(ctx, objectKey, upload)
	if err != nil {
		return AttachmentView{}, err
	}
	view, err := s.finalizePreparedAttachment(ctx, userID, pasteID, upload, stored)
	if err != nil && stored.refReserved {
		if cleanupErr := s.releaseReservedObjectRef(ctx, stored); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}
	return view, err
}

func (s *Service) AddGuestAttachmentStreamWithContext(ctx context.Context, token string, pasteID string, fileName string, contentType string, body io.Reader, turnstileToken string, remoteIP string) (AttachmentView, error) {
	preflight, err := s.PreflightGuestAttachmentUpload(ctx, token, pasteID, turnstileToken, remoteIP)
	if err != nil {
		return AttachmentView{}, err
	}
	upload, err := PrepareAttachmentUploadWithLimit(fileName, contentType, body, preflight.MaxBytes)
	if err != nil {
		return AttachmentView{}, err
	}
	defer upload.Close()
	return s.AddPreflightedGuestAttachmentWithContext(ctx, preflight, upload)
}

func (s *Service) AddPreparedGuestAttachment(token string, pasteID string, upload *PreparedAttachmentUpload, turnstileToken string, remoteIP string) (AttachmentView, error) {
	return s.AddPreparedGuestAttachmentWithContext(context.Background(), token, pasteID, upload, turnstileToken, remoteIP)
}

func (s *Service) AddPreparedGuestAttachmentWithContext(ctx context.Context, token string, pasteID string, upload *PreparedAttachmentUpload, turnstileToken string, remoteIP string) (AttachmentView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	objectKey, err := s.preflightPreparedGuestAttachment(ctx, token, pasteID, upload, turnstileToken, remoteIP, true)
	if err != nil {
		return AttachmentView{}, err
	}
	releaseObjectKey := s.lockObjectKey(objectKey)
	defer releaseObjectKey()
	stored, err := s.reserveAndStorePreparedObject(ctx, objectKey, upload)
	if err != nil {
		return AttachmentView{}, err
	}
	view, err := s.finalizePreparedGuestAttachment(ctx, token, pasteID, upload, stored)
	if err != nil && stored.refReserved {
		if cleanupErr := s.releaseReservedObjectRef(ctx, stored); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}
	return view, err
}

func (s *Service) AddPreflightedGuestAttachmentWithContext(ctx context.Context, preflight AttachmentUploadPreflight, upload *PreparedAttachmentUpload) (AttachmentView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if preflight.guestToken == "" || preflight.pasteID == "" {
		return AttachmentView{}, E(http.StatusBadRequest, "invalid_upload_preflight", "guest attachment upload preflight is invalid")
	}
	objectKey, err := s.preflightPreparedGuestAttachment(ctx, preflight.guestToken, preflight.pasteID, upload, "", "", false)
	if err != nil {
		return AttachmentView{}, err
	}
	releaseObjectKey := s.lockObjectKey(objectKey)
	defer releaseObjectKey()
	stored, err := s.reserveAndStorePreparedObject(ctx, objectKey, upload)
	if err != nil {
		return AttachmentView{}, err
	}
	view, err := s.finalizePreparedGuestAttachment(ctx, preflight.guestToken, preflight.pasteID, upload, stored)
	if err != nil && stored.refReserved {
		if cleanupErr := s.releaseReservedObjectRef(ctx, stored); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}
	return view, err
}

func (s *Service) lockObjectKey(objectKey string) func() {
	s.objectLocksMu.Lock()
	lock := s.objectLocks[objectKey]
	if lock == nil {
		lock = &objectKeyLock{}
		s.objectLocks[objectKey] = lock
	}
	lock.refs++
	s.objectLocksMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.objectLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.objectLocks, objectKey)
		}
		s.objectLocksMu.Unlock()
	}
}

func (s *Service) preflightPreparedAttachment(ctx context.Context, userID string, pasteID string, upload *PreparedAttachmentUpload) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(ctx, userID, pasteID)
	if err != nil {
		return "", err
	}
	user := s.usersByID[userID]
	plan, _ := s.planForUserLocked(user)
	if err := s.validatePreparedAttachmentLocked(ctx, user, paste, plan, upload); err != nil {
		return "", err
	}
	return preparedAttachmentObjectKey(user.ID, upload), nil
}

func (s *Service) preflightPreparedGuestAttachment(ctx context.Context, token string, pasteID string, upload *PreparedAttachmentUpload, turnstileToken string, remoteIP string, verifyTurnstile bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.runtimeConfig.GuestUploads
	if !cfg.Enabled {
		return "", E(http.StatusForbidden, "guest_uploads_disabled", "guest uploads are disabled")
	}
	if verifyTurnstile && cfg.RequireTurnstile {
		if err := s.verifyTurnstileLocked(ctx, turnstileToken, remoteIP); err != nil {
			return "", err
		}
	}
	user, err := s.guestUserForTokenLocked(ctx, strings.TrimSpace(token))
	if err != nil {
		return "", err
	}
	paste, err := s.pasteByIDLocked(ctx, pasteID)
	if err != nil || paste.UserID != user.ID {
		return "", E(http.StatusNotFound, "paste_not_found", "paste not found")
	}
	plan := guestPlan(cfg)
	if err := s.validatePreparedAttachmentLocked(ctx, user, paste, plan, upload); err != nil {
		return "", err
	}
	return preparedAttachmentObjectKey(user.ID, upload), nil
}

func (s *Service) attachmentUploadLimitLocked(ctx context.Context, user *User, paste *Paste, plan plans.Plan) (int64, error) {
	if err := s.validatePreparedAttachmentLocked(ctx, user, paste, plan, &PreparedAttachmentUpload{}); err != nil {
		return 0, err
	}
	quota, err := s.quotaLocked(ctx, user.ID, plan)
	if err != nil {
		return 0, err
	}
	maxBytes := maxAttachmentUploadBytes
	for _, available := range []int64{
		plan.SingleFileBytes,
		plan.SinglePasteBytes - s.pasteSizeLocked(paste),
		plan.ActiveStorageBytes - quota.ActiveStorageBytes,
		plan.DailyUploadBytes - quota.DailyUploadBytes,
	} {
		if available < maxBytes {
			maxBytes = available
		}
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	return maxBytes, nil
}

func (s *Service) finalizePreparedAttachment(ctx context.Context, userID string, pasteID string, upload *PreparedAttachmentUpload, stored preparedObjectStorage) (AttachmentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	paste, err := s.ownerPasteLocked(ctx, userID, pasteID)
	if err != nil {
		s.rollbackPreparedObjectLocked(ctx, stored)
		return AttachmentView{}, err
	}
	user := s.usersByID[userID]
	plan, _ := s.planForUserLocked(user)
	if err := s.validatePreparedAttachmentLocked(ctx, user, paste, plan, upload); err != nil {
		s.rollbackPreparedObjectLocked(ctx, stored)
		return AttachmentView{}, err
	}
	return s.createPreparedAttachmentForPasteLocked(ctx, user.ID, paste, upload, stored)
}

func (s *Service) finalizePreparedGuestAttachment(ctx context.Context, token string, pasteID string, upload *PreparedAttachmentUpload, stored preparedObjectStorage) (AttachmentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.runtimeConfig.GuestUploads
	if !cfg.Enabled {
		s.rollbackPreparedObjectLocked(ctx, stored)
		return AttachmentView{}, E(http.StatusForbidden, "guest_uploads_disabled", "guest uploads are disabled")
	}
	user, err := s.guestUserForTokenLocked(ctx, strings.TrimSpace(token))
	if err != nil {
		s.rollbackPreparedObjectLocked(ctx, stored)
		return AttachmentView{}, err
	}
	paste, err := s.pasteByIDLocked(ctx, pasteID)
	if err != nil || paste.UserID != user.ID {
		s.rollbackPreparedObjectLocked(ctx, stored)
		return AttachmentView{}, E(http.StatusNotFound, "paste_not_found", "paste not found")
	}
	plan := guestPlan(cfg)
	if err := s.validatePreparedAttachmentLocked(ctx, user, paste, plan, upload); err != nil {
		s.rollbackPreparedObjectLocked(ctx, stored)
		return AttachmentView{}, err
	}
	return s.createPreparedAttachmentForPasteLocked(ctx, user.ID, paste, upload, stored)
}

func (s *Service) OpenAttachment(userID string, attachmentID string) (AttachmentDownload, error) {
	return s.OpenAttachmentWithContext(context.Background(), userID, attachmentID)
}

func (s *Service) OpenAttachmentWithContext(ctx context.Context, userID string, attachmentID string) (AttachmentDownload, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	attachment, err := s.attachmentByIDLocked(ctx, attachmentID)
	if err != nil || attachment.UserID != userID {
		s.mu.Unlock()
		return AttachmentDownload{}, E(http.StatusNotFound, "attachment_not_found", "attachment not found")
	}
	paste, err := s.pasteByIDLocked(ctx, attachment.PasteID)
	if err != nil || !s.isPasteVisibleLocked(paste) || attachment.Status != "active" {
		s.mu.Unlock()
		return AttachmentDownload{}, E(http.StatusGone, "attachment_unavailable", "attachment is unavailable")
	}
	if attachment.ScanStatus == "malicious" {
		s.mu.Unlock()
		return AttachmentDownload{}, E(http.StatusForbidden, "malicious_file", "file is blocked")
	}
	snapshot := *attachment
	s.mu.Unlock()

	object, err := s.objectStream(ctx, &snapshot)
	if err != nil {
		return AttachmentDownload{}, E(http.StatusGone, "attachment_unavailable", "attachment content is unavailable")
	}

	s.mu.Lock()
	attachment, err = s.attachmentByIDLocked(ctx, attachmentID)
	paste, pasteErr := s.pasteByIDLocked(ctx, snapshot.PasteID)
	if err != nil || pasteErr != nil || !s.isPasteVisibleLocked(paste) || attachment.UserID != userID || attachment.Status != "active" || attachment.ScanStatus == "malicious" || attachment.ObjectKey != snapshot.ObjectKey {
		s.mu.Unlock()
		_ = object.Body.Close()
		return AttachmentDownload{}, E(http.StatusGone, "attachment_unavailable", "attachment is unavailable")
	}
	if atomicStore, ok := s.content.Attachments.(AtomicAttachmentDownloadStore); ok {
		s.mu.Unlock()
		updated, updateErr := atomicStore.IncrementAttachmentDownload(ctx, attachmentID)
		if updateErr != nil {
			_ = object.Body.Close()
			return AttachmentDownload{}, updateErr
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		attachment, err = s.attachmentByIDLocked(ctx, attachmentID)
		paste, pasteErr = s.pasteByIDLocked(ctx, snapshot.PasteID)
		if err != nil || pasteErr != nil || !s.isPasteVisibleLocked(paste) || attachment.UserID != userID || attachment.Status != "active" || attachment.ScanStatus == "malicious" || attachment.ObjectKey != snapshot.ObjectKey {
			_ = object.Body.Close()
			return AttachmentDownload{}, E(http.StatusGone, "attachment_unavailable", "attachment content is unavailable")
		}
		if updated.DownloadN > attachment.DownloadN {
			attachment = s.cacheAttachmentLocked(updated)
		}
		return AttachmentDownload{Attachment: viewAttachment(attachment), Body: object.Body, Size: attachment.Size}, nil
	}
	defer s.mu.Unlock()
	attachment.DownloadN++
	if err := s.updateAttachmentLocked(ctx, attachment); err != nil {
		_ = object.Body.Close()
		return AttachmentDownload{}, err
	}
	return AttachmentDownload{Attachment: viewAttachment(attachment), Body: object.Body, Size: attachment.Size}, nil
}

func (s *Service) OpenSharedAttachment(token string, password string, attachmentID string, viewerUserID string) (AttachmentDownload, error) {
	return s.openSharedAttachment(context.Background(), token, password, attachmentID, viewerUserID, false)
}

func (s *Service) OpenSharedAttachmentWithAccessGrant(token string, attachmentID string, viewerUserID string) (AttachmentDownload, error) {
	return s.OpenSharedAttachmentWithAccessGrantContext(context.Background(), token, attachmentID, viewerUserID)
}

func (s *Service) OpenSharedAttachmentWithAccessGrantContext(ctx context.Context, token string, attachmentID string, viewerUserID string) (AttachmentDownload, error) {
	return s.openSharedAttachment(ctx, token, "", attachmentID, viewerUserID, true)
}

func (s *Service) openSharedAttachment(ctx context.Context, token string, password string, attachmentID string, viewerUserID string, passwordVerified bool) (AttachmentDownload, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !passwordVerified {
		verified, err := s.verifySharePasswordForAccess(ctx, token, password)
		if err != nil {
			return AttachmentDownload{}, err
		}
		passwordVerified = verified
	}
	s.mu.Lock()
	share, paste, err := s.validShareAccessLocked(ctx, token, password, viewerUserID, true, passwordVerified)
	if err != nil {
		s.mu.Unlock()
		return AttachmentDownload{}, err
	}
	attachment, err := s.attachmentByIDLocked(ctx, attachmentID)
	if err != nil || attachment.PasteID != paste.ID || attachment.Status != "active" {
		s.mu.Unlock()
		return AttachmentDownload{}, E(http.StatusNotFound, "attachment_not_found", "attachment not found")
	}
	if attachment.ScanStatus != "clean" {
		s.mu.Unlock()
		return AttachmentDownload{}, E(http.StatusForbidden, "scan_not_clean", "public downloads require clean scan status")
	}
	owner := s.usersByID[share.UserID]
	plan, _ := s.planForUserLocked(owner)
	atomicStore, atomic := s.content.Shares.(AtomicShareStore)
	if atomic {
		shareID := share.ID
		ownerID := share.UserID
		now := s.now().UTC()
		snapshot := *attachment
		s.mu.Unlock()

		object, openErr := s.objectStream(ctx, &snapshot)
		if openErr != nil {
			return AttachmentDownload{}, E(http.StatusGone, "attachment_unavailable", "attachment content is unavailable")
		}
		consumed, updatedAttachment, consumeErr := atomicStore.ConsumeShareDownload(ctx, shareID, snapshot.ID, ownerID, plan.DailyShareDownloadBytes, now)
		if consumeErr != nil {
			_ = object.Body.Close()
			if isStoreNotFound(consumeErr) {
				return AttachmentDownload{}, E(http.StatusNotFound, "attachment_not_found", "attachment not found")
			}
			return AttachmentDownload{}, consumeErr
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		share = s.cacheShareLocked(consumed)
		attachment = s.cacheAttachmentLocked(updatedAttachment)
		return AttachmentDownload{Attachment: viewAttachment(attachment), Body: object.Body, Size: attachment.Size}, nil
	}
	downloadBytes, err := s.dailyMetricLocked(ctx, share.UserID, "share_download")
	if err != nil {
		s.mu.Unlock()
		return AttachmentDownload{}, err
	}
	if downloadBytes+attachment.Size > plan.DailyShareDownloadBytes {
		s.mu.Unlock()
		return AttachmentDownload{}, E(http.StatusForbidden, "daily_download_limit", "daily share download traffic exceeds plan limit")
	}
	snapshot := *attachment
	s.mu.Unlock()

	object, err := s.objectStream(ctx, &snapshot)
	if err != nil {
		return AttachmentDownload{}, E(http.StatusGone, "attachment_unavailable", "attachment content is unavailable")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	share, paste, err = s.validShareAccessLocked(ctx, token, password, viewerUserID, true, passwordVerified)
	if err != nil {
		_ = object.Body.Close()
		return AttachmentDownload{}, err
	}
	attachment, err = s.attachmentByIDLocked(ctx, attachmentID)
	if err != nil || attachment.PasteID != paste.ID || attachment.Status != "active" || attachment.ScanStatus != "clean" || attachment.ObjectKey != snapshot.ObjectKey {
		_ = object.Body.Close()
		return AttachmentDownload{}, E(http.StatusGone, "attachment_unavailable", "attachment content is unavailable")
	}
	owner = s.usersByID[share.UserID]
	plan, _ = s.planForUserLocked(owner)
	downloadBytes, err = s.dailyMetricLocked(ctx, share.UserID, "share_download")
	if err != nil || downloadBytes+attachment.Size > plan.DailyShareDownloadBytes {
		_ = object.Body.Close()
		if err != nil {
			return AttachmentDownload{}, err
		}
		return AttachmentDownload{}, E(http.StatusForbidden, "daily_download_limit", "daily share download traffic exceeds plan limit")
	}
	if err := s.commitSharedDownloadLocked(ctx, share, attachment, s.now().UTC()); err != nil {
		_ = object.Body.Close()
		return AttachmentDownload{}, err
	}
	return AttachmentDownload{Attachment: viewAttachment(attachment), Body: object.Body, Size: attachment.Size}, nil
}

func (s *Service) validatePreparedAttachmentLocked(ctx context.Context, user *User, paste *Paste, plan plans.Plan, upload *PreparedAttachmentUpload) error {
	if upload == nil {
		return E(http.StatusBadRequest, "missing_file", "file is required")
	}
	if !s.isPasteVisibleLocked(paste) {
		return E(http.StatusGone, "paste_expired", "cannot attach to expired paste")
	}
	if len(paste.AttachmentIDs)+1 > plan.AttachmentsPerPasteLimit {
		return E(http.StatusBadRequest, "too_many_attachments", "attachment count exceeds plan limit")
	}
	if upload.Size > plan.SingleFileBytes {
		return E(http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds plan limit")
	}
	if s.pasteSizeLocked(paste)+upload.Size > plan.SinglePasteBytes {
		return E(http.StatusRequestEntityTooLarge, "paste_too_large", "paste exceeds plan total size")
	}
	if err := s.ensureCanCreatePasteLocked(ctx, user, plan, PasteInput{ExpiresInSeconds: int64(paste.ExpiresAt.Sub(s.now().UTC()).Seconds())}, upload.Size, 1); err != nil {
		return err
	}
	return nil
}

func preparedAttachmentObjectKey(userID string, upload *PreparedAttachmentUpload) string {
	return userID + "/" + upload.SHA256
}

func (s *Service) reserveAndStorePreparedObject(ctx context.Context, objectKey string, upload *PreparedAttachmentUpload) (preparedObjectStorage, error) {
	stored := preparedObjectStorage{objectKey: objectKey}
	atomicStore, ok := s.content.ObjectRefs.(AtomicObjectRefStore)
	if !ok {
		return s.storePreparedObject(ctx, objectKey, upload, stored)
	}
	reserveAt := s.now().UTC()
	var resultErr error
	resultErr = s.withObjectRefLock(ctx, objectKey, func(lockCtx context.Context, lockedStore AtomicObjectRefStore) error {
		if lockedStore == nil {
			lockedStore = atomicStore
		}
		ref, err := lockedStore.IncrementObjectRef(lockCtx, ObjectRef{
			ObjectKey: objectKey,
			Size:      upload.Size,
			SHA256:    upload.SHA256,
			CreatedAt: reserveAt,
			UpdatedAt: reserveAt,
		})
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.objectRefs[objectKey] = ref.RefCount
		s.mu.Unlock()
		stored.refReserved = true
		stored, resultErr = s.storePreparedObject(lockCtx, objectKey, upload, stored)
		if resultErr == nil {
			return nil
		}
		cleanupErr := s.releaseReservedObjectRefWithStore(lockCtx, lockedStore, stored)
		return errors.Join(resultErr, cleanupErr)
	})
	return stored, resultErr
}

func (s *Service) releaseReservedObjectRef(ctx context.Context, stored preparedObjectStorage) error {
	if !stored.refReserved {
		return nil
	}
	atomicStore, ok := s.content.ObjectRefs.(AtomicObjectRefStore)
	if !ok {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.withObjectRefLock(cleanupCtx, stored.objectKey, func(lockCtx context.Context, lockedStore AtomicObjectRefStore) error {
		if lockedStore == nil {
			lockedStore = atomicStore
		}
		return s.releaseReservedObjectRefWithStore(lockCtx, lockedStore, stored)
	})
}

func (s *Service) releaseReservedObjectRefWithStore(ctx context.Context, store AtomicObjectRefStore, stored preparedObjectStorage) error {
	ref, removed, err := store.DecrementObjectRef(ctx, stored.objectKey)
	if err != nil {
		return err
	}
	var deleteErr error
	if removed {
		deleteErr = s.deleteObjectWithContext(ctx, stored.objectKey)
	}
	s.mu.Lock()
	if removed {
		delete(s.objectRefs, stored.objectKey)
	} else {
		s.objectRefs[stored.objectKey] = ref.RefCount
	}
	s.mu.Unlock()
	return deleteErr
}

func (s *Service) withObjectRefLock(ctx context.Context, objectKey string, fn func(context.Context, AtomicObjectRefStore) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if coordinator, ok := s.content.ObjectRefs.(ObjectRefCoordinator); ok {
		return coordinator.WithObjectRefLock(ctx, objectKey, fn)
	}
	store, ok := s.content.ObjectRefs.(AtomicObjectRefStore)
	if !ok {
		return fn(ctx, nil)
	}
	return fn(ctx, store)
}

func (s *Service) storePreparedObject(ctx context.Context, objectKey string, upload *PreparedAttachmentUpload, stored preparedObjectStorage) (preparedObjectStorage, error) {
	reader, err := upload.reader()
	if err != nil {
		return stored, err
	}
	if s.objectStore != nil {
		if streaming, ok := s.objectStore.(StreamingObjectStore); ok {
			if err := streaming.PutObjectStream(ctx, objectKey, reader, upload.Size, upload.ContentType); err != nil {
				return stored, fmt.Errorf("put object: %w", err)
			}
			return stored, nil
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return stored, fmt.Errorf("read object content: %w", err)
		}
		if err := s.objectStore.PutObject(ctx, objectKey, data, upload.ContentType); err != nil {
			return stored, fmt.Errorf("put object: %w", err)
		}
		return stored, nil
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return stored, fmt.Errorf("read object content: %w", err)
	}
	stored.inMemory = true
	stored.content = data
	return stored, nil
}

func (s *Service) createPreparedAttachmentForPasteLocked(ctx context.Context, userID string, paste *Paste, upload *PreparedAttachmentUpload, stored preparedObjectStorage) (AttachmentView, error) {
	now := s.now().UTC()
	status, scanStatus, risk := "active", "pending", classifyAttachmentRisk(upload.FileName, upload.ContentType)
	objectKey := stored.objectKey
	if objectKey == "" {
		objectKey = preparedAttachmentObjectKey(userID, upload)
	}
	existingObjectRefs := s.objectRefs[objectKey]
	if stored.inMemory {
		s.objects[objectKey] = append([]byte(nil), stored.content...)
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
	if err := s.createAttachmentLocked(ctx, attachment); err != nil {
		s.rollbackUnreferencedStoredObjectLocked(ctx, attachment.ObjectKey, existingObjectRefs)
		return AttachmentView{}, err
	}
	attachmentCreated := true
	if !stored.refReserved {
		if err := s.incrementObjectRefLocked(ctx, attachment, existingObjectRefs, now); err != nil {
			_ = s.deleteAttachmentLocked(ctx, attachment)
			s.rollbackUnreferencedStoredObjectLocked(ctx, attachment.ObjectKey, existingObjectRefs)
			return AttachmentView{}, err
		}
	}
	previousPasteScanStatus := paste.ScanStatus
	previousPasteUpdatedAt := paste.UpdatedAt
	paste.ScanStatus = aggregateScanStatus(s.attachmentsForPasteLocked(paste))
	paste.UpdatedAt = now
	if err := s.updatePasteLocked(ctx, paste); err != nil {
		s.rollbackAttachmentCreateLocked(ctx, paste, previousPasteScanStatus, previousPasteUpdatedAt, attachment, stored, attachmentCreated, false, false)
		return AttachmentView{}, err
	}
	pasteUpdated := true
	scanQueueCreated := false
	if err := s.scheduleScanJobLocked(ctx, attachment.ID, now); err != nil {
		s.rollbackAttachmentCreateLocked(ctx, paste, previousPasteScanStatus, previousPasteUpdatedAt, attachment, stored, attachmentCreated, pasteUpdated, false)
		return AttachmentView{}, err
	}
	scanQueueCreated = true
	if err := s.recordDailyUploadLocked(ctx, userID, upload.Size); err != nil {
		s.rollbackAttachmentCreateLocked(ctx, paste, previousPasteScanStatus, previousPasteUpdatedAt, attachment, stored, attachmentCreated, pasteUpdated, scanQueueCreated)
		return AttachmentView{}, err
	}
	return viewAttachment(attachment), nil
}

func (s *Service) rollbackPreparedObjectLocked(ctx context.Context, stored preparedObjectStorage) {
	if stored.objectKey == "" {
		return
	}
	if stored.refReserved {
		return
	}
	previousRefs := s.objectRefs[stored.objectKey]
	if stored.inMemory {
		if previousRefs == 0 {
			delete(s.objects, stored.objectKey)
		}
		return
	}
	s.rollbackUnreferencedStoredObjectLocked(ctx, stored.objectKey, previousRefs)
}

func (s *Service) objectStream(ctx context.Context, attachment *Attachment) (ObjectStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.objectStore != nil {
		if streaming, ok := s.objectStore.(StreamingObjectStore); ok {
			return streaming.OpenObject(ctx, attachment.ObjectKey)
		}
		content, err := s.objectStore.GetObject(ctx, attachment.ObjectKey)
		if err != nil {
			return ObjectStream{}, err
		}
		return ObjectStream{Body: io.NopCloser(bytes.NewReader(content)), Size: int64(len(content)), ContentType: attachment.ContentType}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
		readBuf := buf
		if remaining := limit - total; remaining >= 0 && int64(len(readBuf)) > remaining+1 {
			readBuf = readBuf[:remaining+1]
		}
		n, readErr := src.Read(readBuf)
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

func (s *Service) AddGuestAttachmentStream(token string, pasteID string, fileName string, contentType string, body io.Reader, turnstileToken string, remoteIP string) (AttachmentView, error) {
	return s.AddGuestAttachmentStreamWithContext(context.Background(), token, pasteID, fileName, contentType, body, turnstileToken, remoteIP)
}

func (s *Service) AddAttachmentStream(userID string, pasteID string, fileName string, contentType string, body io.Reader) (AttachmentView, error) {
	return s.AddAttachmentStreamWithContext(context.Background(), userID, pasteID, fileName, contentType, body)
}

func (s *Service) PreflightAttachmentUpload(userID string, pasteID string) (AttachmentUploadPreflight, error) {
	return s.PreflightAttachmentUploadWithContext(context.Background(), userID, pasteID)
}
