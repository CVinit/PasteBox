package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

func TestContentMetadataStoresRoundTripPasteAttachmentObjectAndShare(t *testing.T) {
	databaseURL := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL to run PostgreSQL content metadata integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := ApplyMigrations(ctx, databaseURL); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	userID := "usr_content_metadata_test"
	pasteID := "pst_content_metadata_test"
	attachmentID := "att_content_metadata_test"
	shareID := "shr_content_metadata_test"
	objectKey := "objects/content-metadata-test"
	tokenHash := "token_hash_content_metadata_test"
	cleanupContentMetadataTestRows(ctx, t, pool, userID, pasteID, attachmentID, shareID, objectKey, tokenHash)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanupContentMetadataTestRows(cleanupCtx, t, pool, userID, pasteID, attachmentID, shareID, objectKey, tokenHash)
	})

	now := time.Now().UTC()
	if err := NewUserStore(pool).CreateUser(ctx, app.User{
		ID:            userID,
		Email:         "content-metadata-test@example.com",
		DisplayName:   "Content Metadata Test",
		Language:      "en",
		PasswordHash:  "argon2-test-hash",
		Role:          "user",
		EmailVerified: true,
		PlanID:        "free",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	pasteStore := NewPasteStore(pool)
	paste := app.Paste{
		ID:         pasteID,
		UserID:     userID,
		Title:      "metadata paste",
		Text:       "hello",
		Tags:       []string{"alpha", "beta"},
		Pinned:     true,
		Favorite:   false,
		Status:     "active",
		ScanStatus: "clean",
		ExpiresAt:  now.Add(24 * time.Hour),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := pasteStore.CreatePaste(ctx, paste); err != nil {
		t.Fatalf("create paste: %v", err)
	}
	page, err := pasteStore.ListPastesByUserPage(ctx, userID, 1, 0)
	if err != nil {
		t.Fatalf("list paged pastes: %v", err)
	}
	if len(page) != 1 || page[0].ID != pasteID {
		t.Fatalf("expected one active paste page, got %#v", page)
	}
	loadedPaste, err := pasteStore.PasteByID(ctx, pasteID)
	if err != nil {
		t.Fatalf("read paste: %v", err)
	}
	if loadedPaste.ID != pasteID || loadedPaste.Text != "hello" || len(loadedPaste.Tags) != 2 || loadedPaste.Tags[0] != "alpha" || !loadedPaste.Pinned {
		t.Fatalf("unexpected paste: %#v", loadedPaste)
	}
	paste.Text = "updated"
	paste.Tags = nil
	paste.Pinned = false
	paste.Favorite = true
	paste.Status = "pending_delete"
	paste.UpdatedAt = now.Add(time.Hour)
	if err := pasteStore.UpdatePaste(ctx, paste); err != nil {
		t.Fatalf("update paste: %v", err)
	}
	updatedPaste, err := pasteStore.PasteByID(ctx, pasteID)
	if err != nil {
		t.Fatalf("read updated paste: %v", err)
	}
	if updatedPaste.Text != "updated" || updatedPaste.Pinned || !updatedPaste.Favorite || updatedPaste.Status != "pending_delete" || len(updatedPaste.Tags) != 0 {
		t.Fatalf("unexpected updated paste: %#v", updatedPaste)
	}
	pastes, err := pasteStore.ListPastesByUser(ctx, userID)
	if err != nil {
		t.Fatalf("list pastes: %v", err)
	}
	if len(pastes) != 1 || pastes[0].ID != pasteID {
		t.Fatalf("expected one paste for user, got %#v", pastes)
	}
	allPastes, err := pasteStore.ListPastes(ctx)
	if err != nil {
		t.Fatalf("list all pastes: %v", err)
	}
	foundPaste := false
	for _, listedPaste := range allPastes {
		if listedPaste.ID == pasteID && listedPaste.Status == "pending_delete" {
			foundPaste = true
			break
		}
	}
	if !foundPaste {
		t.Fatalf("expected updated paste in global listing, got %#v", allPastes)
	}
	if _, err := pasteStore.PasteByID(ctx, "pst_content_metadata_missing"); !errors.Is(err, ErrPasteNotFound) {
		t.Fatalf("expected missing paste error, got %v", err)
	}

	attachmentStore := NewAttachmentStore(pool)
	objectRef := ObjectRef{ObjectKey: objectKey, RefCount: 1, Size: 12, SHA256: "sha", CreatedAt: now, UpdatedAt: now}
	if err := attachmentStore.UpsertObjectRef(ctx, objectRef); err != nil {
		t.Fatalf("upsert object ref: %v", err)
	}
	loadedRef, err := attachmentStore.ObjectRef(ctx, objectKey)
	if err != nil {
		t.Fatalf("read object ref: %v", err)
	}
	if loadedRef.ObjectKey != objectKey || loadedRef.RefCount != 1 || loadedRef.Size != 12 {
		t.Fatalf("unexpected object ref: %#v", loadedRef)
	}
	objectRef.RefCount = 2
	objectRef.UpdatedAt = now.Add(time.Minute)
	if err := attachmentStore.UpsertObjectRef(ctx, objectRef); err != nil {
		t.Fatalf("update object ref: %v", err)
	}
	updatedRef, err := attachmentStore.ObjectRef(ctx, objectKey)
	if err != nil {
		t.Fatalf("read updated object ref: %v", err)
	}
	if updatedRef.RefCount != 2 {
		t.Fatalf("expected updated ref count, got %#v", updatedRef)
	}
	incrementedRef, err := attachmentStore.IncrementObjectRef(ctx, ObjectRef{ObjectKey: objectKey, Size: 12, SHA256: "sha", UpdatedAt: now.Add(2 * time.Minute)})
	if err != nil {
		t.Fatalf("increment object ref atomically: %v", err)
	}
	if incrementedRef.RefCount != 3 {
		t.Fatalf("expected atomic object ref count 3, got %#v", incrementedRef)
	}
	remainingRef, removed, err := attachmentStore.DecrementObjectRef(ctx, objectKey)
	if err != nil {
		t.Fatalf("decrement object ref atomically: %v", err)
	}
	if removed || remainingRef.RefCount != 2 {
		t.Fatalf("expected two remaining object refs, removed=%t ref=%#v", removed, remainingRef)
	}

	attachment := app.Attachment{
		ID:          attachmentID,
		UserID:      userID,
		PasteID:     pasteID,
		FileName:    "image.png",
		ContentType: "image/png",
		Size:        12,
		SHA256:      "sha",
		ObjectKey:   objectKey,
		Status:      "active",
		ScanStatus:  "clean",
		Risk:        "",
		ImageWidth:  100,
		ImageHeight: 50,
		CreatedAt:   now.Add(2 * time.Minute),
		DownloadN:   1,
	}
	if err := attachmentStore.CreateAttachment(ctx, attachment); err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	updatedDownload, err := attachmentStore.IncrementAttachmentDownload(ctx, attachmentID)
	if err != nil {
		t.Fatalf("increment attachment download: %v", err)
	}
	if updatedDownload.DownloadN != 2 {
		t.Fatalf("expected atomic attachment download count 2, got %#v", updatedDownload)
	}
	loadedAttachment, err := attachmentStore.AttachmentByID(ctx, attachmentID)
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	if loadedAttachment.ID != attachmentID || loadedAttachment.ObjectKey != objectKey || loadedAttachment.DownloadN != 2 || loadedAttachment.Content != nil {
		t.Fatalf("unexpected attachment: %#v", loadedAttachment)
	}
	shareStore := NewShareStore(pool)
	visitedAt := now.Add(3 * time.Minute)
	share := app.Share{
		ID:            shareID,
		PasteID:       pasteID,
		UserID:        userID,
		TokenHash:     tokenHash,
		Token:         "encrypted-token-placeholder",
		PasswordHash:  "password-hash",
		LoginRequired: true,
		MaxVisits:     5,
		MaxDownloads:  2,
		VisitCount:    1,
		DownloadCount: 0,
		ExpiresAt:     now.Add(2 * time.Hour),
		CreatedAt:     now,
		LastVisitedAt: &visitedAt,
	}
	if err := shareStore.CreateShare(ctx, share); err != nil {
		t.Fatalf("create share: %v", err)
	}
	visitedShare, err := shareStore.ConsumeShareVisit(ctx, shareID, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("consume share visit: %v", err)
	}
	if visitedShare.VisitCount != 2 || visitedShare.LastVisitedAt == nil {
		t.Fatalf("unexpected consumed share visit: %#v", visitedShare)
	}
	consumedShare, consumedAttachment, err := shareStore.ConsumeShareDownload(ctx, shareID, attachmentID, userID, 1000, now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("consume share download: %v", err)
	}
	if consumedShare.DownloadCount != 1 || consumedShare.LastDownloadedAt == nil || consumedAttachment.DownloadN != 3 {
		t.Fatalf("unexpected consumed share download: share=%#v attachment=%#v", consumedShare, consumedAttachment)
	}
	attachment.Status = "frozen"
	attachment.ScanStatus = "malicious"
	attachment.Risk = "signature"
	attachment.DownloadN = 3
	if err := attachmentStore.UpdateAttachment(ctx, attachment); err != nil {
		t.Fatalf("update attachment: %v", err)
	}
	attachments, err := attachmentStore.ListAttachmentsByPaste(ctx, pasteID)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(attachments) != 1 || attachments[0].Status != "frozen" || attachments[0].Risk != "signature" || attachments[0].DownloadN != 3 {
		t.Fatalf("unexpected attachments: %#v", attachments)
	}
	allAttachments, err := attachmentStore.ListAttachments(ctx)
	if err != nil {
		t.Fatalf("list all attachments: %v", err)
	}
	foundAttachment := false
	for _, listedAttachment := range allAttachments {
		if listedAttachment.ID == attachmentID && listedAttachment.Status == "frozen" && listedAttachment.Risk == "signature" {
			foundAttachment = true
			break
		}
	}
	if !foundAttachment {
		t.Fatalf("expected updated attachment in global listing, got %#v", allAttachments)
	}
	loadedShare, err := shareStore.ShareByTokenHash(ctx, tokenHash)
	if err != nil {
		t.Fatalf("read share by token hash: %v", err)
	}
	if loadedShare.ID != shareID || loadedShare.Token != "encrypted-token-placeholder" || loadedShare.LastVisitedAt == nil {
		t.Fatalf("unexpected share: %#v", loadedShare)
	}
	downloadedAt := now.Add(4 * time.Minute)
	failedAt := now.Add(5 * time.Minute)
	revokedAt := now.Add(6 * time.Minute)
	share.VisitCount = 2
	share.DownloadCount = 1
	share.LastDownloadedAt = &downloadedAt
	share.LastAccessFailure = &failedAt
	share.RevokedAt = &revokedAt
	if err := shareStore.UpdateShare(ctx, share); err != nil {
		t.Fatalf("update share: %v", err)
	}
	shares, err := shareStore.ListSharesByUser(ctx, userID)
	if err != nil {
		t.Fatalf("list shares: %v", err)
	}
	if len(shares) != 1 || shares[0].DownloadCount != 1 || shares[0].RevokedAt == nil || shares[0].LastAccessFailure == nil {
		t.Fatalf("unexpected shares: %#v", shares)
	}
	allShares, err := shareStore.ListShares(ctx)
	if err != nil {
		t.Fatalf("list all shares: %v", err)
	}
	foundShare := false
	for _, listedShare := range allShares {
		if listedShare.ID == shareID && listedShare.DownloadCount == 1 && listedShare.RevokedAt != nil && listedShare.LastAccessFailure != nil {
			foundShare = true
			break
		}
	}
	if !foundShare {
		t.Fatalf("expected updated share in global listing, got %#v", allShares)
	}
	duplicate := share
	duplicate.ID = "shr_content_metadata_duplicate"
	if err := shareStore.CreateShare(ctx, duplicate); !errors.Is(err, ErrShareTokenExists) {
		t.Fatalf("expected duplicate share token error, got %v", err)
	}
	if _, err := shareStore.ShareByID(ctx, "shr_content_metadata_missing"); !errors.Is(err, ErrShareNotFound) {
		t.Fatalf("expected missing share error, got %v", err)
	}
	if err := attachmentStore.DeleteAttachment(ctx, attachmentID); err != nil {
		t.Fatalf("delete attachment: %v", err)
	}
	if err := attachmentStore.DeleteAttachment(ctx, "att_content_metadata_missing"); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("expected missing attachment error, got %v", err)
	}
}

func cleanupContentMetadataTestRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, userID string, pasteID string, attachmentID string, shareID string, objectKey string, tokenHash string) {
	t.Helper()
	_, _ = pool.Exec(ctx, `DELETE FROM shares WHERE id = $1 OR token_hash = $2`, shareID, tokenHash)
	_, _ = pool.Exec(ctx, `DELETE FROM attachments WHERE id = $1`, attachmentID)
	_, _ = pool.Exec(ctx, `DELETE FROM object_refs WHERE object_key = $1`, objectKey)
	_, _ = pool.Exec(ctx, `DELETE FROM pastes WHERE id = $1`, pasteID)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
}
