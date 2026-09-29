package app

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"
)

func uploadTestTransferItem(t *testing.T, svc *Service, userID string, transferID string, itemID string, fileName string, content []byte) (TransferView, AttachmentView) {
	t.Helper()
	preflight, err := svc.PreflightTransferItemUploadWithContext(context.Background(), userID, transferID, itemID)
	if err != nil {
		t.Fatalf("preflight transfer item: %v", err)
	}
	upload, err := PrepareAttachmentUploadWithLimit(fileName, "text/plain", bytes.NewReader(content), preflight.MaxBytes)
	if err != nil {
		t.Fatalf("prepare transfer item: %v", err)
	}
	defer upload.Close()
	view, attachment, err := svc.AddPreparedTransferItemWithContext(context.Background(), preflight, upload)
	if err != nil {
		t.Fatalf("add transfer item: %v", err)
	}
	return view, attachment
}

func transferInput(items ...TransferItemInput) TransferInput {
	return TransferInput{ExpiresInSeconds: 3600, Items: items}
}

func transferItem(itemID string, fileName string) TransferItemInput {
	return TransferItemInput{ItemID: itemID, FileName: fileName, ContentType: "text/plain"}
}

func TestCreateTransferIsIdempotentByKey(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "transfer@example.com")

	first, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, TransferInput{
		IdempotencyKey:   "send-1",
		ExpiresInSeconds: 3600,
		Items:            []TransferItemInput{transferItem("itm-1", "a.txt")},
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	if first.Status != TransferStatusDraft || first.PasteID == "" || len(first.Items) != 1 || first.Items[0].Status != TransferItemPending {
		t.Fatalf("unexpected draft transfer: %#v", first)
	}

	// Reusing the key must not create a second record or a second paste.
	retry, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, TransferInput{
		IdempotencyKey:   "send-1",
		ExpiresInSeconds: 3600,
		Items:            []TransferItemInput{transferItem("itm-1", "a.txt")},
	})
	if err != nil {
		t.Fatalf("retry create transfer: %v", err)
	}
	if retry.ID != first.ID || retry.PasteID != first.PasteID {
		t.Fatalf("expected the idempotent retry to reuse the transfer, got %#v", retry)
	}
	pastes, err := svc.ListPastes(user.User.ID, ListOptions{})
	if err != nil {
		t.Fatalf("list pastes: %v", err)
	}
	if len(pastes) != 1 {
		t.Fatalf("expected one record after an idempotent retry, got %d", len(pastes))
	}

	// A fresh key is a new send.
	other, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, TransferInput{
		IdempotencyKey:   "send-2",
		ExpiresInSeconds: 3600,
		Items:            []TransferItemInput{transferItem("itm-1", "a.txt")},
	})
	if err != nil {
		t.Fatalf("create second transfer: %v", err)
	}
	if other.ID == first.ID {
		t.Fatalf("expected a new transfer for a new key, got %s", other.ID)
	}
}

func TestCreateTransferValidatesDeclaredItems(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "items@example.com")

	if _, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, transferInput()); !hasAppStatus(err, http.StatusBadRequest) {
		t.Fatalf("expected an empty item list to be rejected, got %v", err)
	}
	duplicated := transferInput(transferItem("itm-1", "a.txt"), transferItem("itm-1", "b.txt"))
	if _, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, duplicated); !hasAppStatus(err, http.StatusBadRequest) {
		t.Fatalf("expected duplicate item ids to be rejected, got %v", err)
	}
	if _, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, transferInput(transferItem("", "a.txt"))); !hasAppStatus(err, http.StatusBadRequest) {
		t.Fatalf("expected an empty item id to be rejected, got %v", err)
	}

	created, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, transferInput(transferItem("itm-1", "../escape.txt")))
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	if created.Items[0].FileName != "escape.txt" {
		t.Fatalf("expected the declared file name to be sanitized, got %q", created.Items[0].FileName)
	}

	// An unknown item id is a missing resource, not an upload target.
	if _, err := svc.PreflightTransferItemUploadWithContext(context.Background(), user.User.ID, created.ID, "itm-missing"); !hasAppStatus(err, http.StatusNotFound) {
		t.Fatalf("expected an unknown item to be rejected, got %v", err)
	}
}

func TestTransferItemUploadRetryReusesAttachment(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "retry@example.com")
	transfer, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, transferInput(transferItem("itm-1", "a.txt")))
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}

	view, attachment := uploadTestTransferItem(t, svc, user.User.ID, transfer.ID, "itm-1", "a.txt", []byte("payload"))
	if view.Items[0].Status != TransferItemUploaded || view.Items[0].AttachmentID != attachment.ID {
		t.Fatalf("expected the item to record its attachment, got %#v", view.Items)
	}

	// Retrying with identical bytes reuses the stored attachment.
	_, retried := uploadTestTransferItem(t, svc, user.User.ID, transfer.ID, "itm-1", "a.txt", []byte("payload"))
	if retried.ID != attachment.ID {
		t.Fatalf("expected the retry to reuse attachment %s, got %s", attachment.ID, retried.ID)
	}
	paste, err := svc.GetPaste(user.User.ID, transfer.PasteID)
	if err != nil {
		t.Fatalf("get paste: %v", err)
	}
	if len(paste.Attachments) != 1 {
		t.Fatalf("expected one attachment after a retry, got %d", len(paste.Attachments))
	}

	// Different bytes for the same declared item are a conflict.
	preflight, err := svc.PreflightTransferItemUploadWithContext(context.Background(), user.User.ID, transfer.ID, "itm-1")
	if err != nil {
		t.Fatalf("preflight retry: %v", err)
	}
	upload, err := PrepareAttachmentUploadWithLimit("a.txt", "text/plain", bytes.NewReader([]byte("other")), preflight.MaxBytes)
	if err != nil {
		t.Fatalf("prepare conflicting upload: %v", err)
	}
	defer upload.Close()
	if _, _, err := svc.AddPreparedTransferItemWithContext(context.Background(), preflight, upload); !hasAppStatus(err, http.StatusConflict) {
		t.Fatalf("expected a conflicting retry to fail, got %v", err)
	}
}

func TestPublishTransferRequiresEveryItemAndCleanScan(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "publish@example.com")
	transfer, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, transferInput(
		transferItem("itm-1", "a.txt"),
		transferItem("itm-2", "b.txt"),
	))
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}

	// Nothing is published while an item is missing.
	if _, err := svc.PublishTransferWithContext(context.Background(), user.User.ID, transfer.ID); !hasAppStatus(err, http.StatusConflict) {
		t.Fatalf("expected an incomplete transfer to be rejected, got %v", err)
	}
	_, first := uploadTestTransferItem(t, svc, user.User.ID, transfer.ID, "itm-1", "a.txt", []byte("a"))
	if _, err := svc.PublishTransferWithContext(context.Background(), user.User.ID, transfer.ID); !hasAppStatus(err, http.StatusConflict) {
		t.Fatalf("expected a partially uploaded transfer to be rejected, got %v", err)
	}

	_, second := uploadTestTransferItem(t, svc, user.User.ID, transfer.ID, "itm-2", "b.txt", []byte("b"))
	runCleanScan(t, svc, first.ID)
	runCleanScan(t, svc, second.ID)

	// A malicious attachment blocks publishing outright.
	flagged, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, transferInput(transferItem("itm-1", "bad.bin")))
	if err != nil {
		t.Fatalf("create flagged transfer: %v", err)
	}
	_, flaggedAttachment := uploadTestTransferItem(t, svc, user.User.ID, flagged.ID, "itm-1", "bad.bin", []byte("bad"))
	if err := svc.RunAttachmentScan(staticScanner{result: ScanResult{Status: "malicious", Risk: "test"}}, flaggedAttachment.ID); err != nil {
		t.Fatalf("run malicious scan: %v", err)
	}
	if _, err := svc.PublishTransferWithContext(context.Background(), user.User.ID, flagged.ID); !hasAppStatus(err, http.StatusForbidden) {
		t.Fatalf("expected a malicious attachment to block publishing, got %v", err)
	}

	published, err := svc.PublishTransferWithContext(context.Background(), user.User.ID, transfer.ID)
	if err != nil {
		t.Fatalf("publish transfer: %v", err)
	}
	if published.Status != TransferStatusPublished || published.Share == nil || published.Share.Token == "" {
		t.Fatalf("unexpected published transfer: %#v", published)
	}
	if !published.Share.ExpiresAt.Equal(transfer.ExpiresAt) {
		t.Fatalf("expected the share to inherit the transfer expiry, got %s", published.Share.ExpiresAt)
	}
	retry, err := svc.PublishTransferWithContext(context.Background(), user.User.ID, transfer.ID)
	if err != nil {
		t.Fatalf("publish retry: %v", err)
	}
	if retry.Share == nil || retry.Share.ID != published.Share.ID {
		t.Fatalf("expected the publish retry to reuse share %s, got %#v", published.Share.ID, retry.Share)
	}
	shares, err := svc.ListShares(user.User.ID)
	if err != nil {
		t.Fatalf("list shares: %v", err)
	}
	if len(shares) != 1 {
		t.Fatalf("expected exactly one share after a publish retry, got %d", len(shares))
	}
	if shares[0].PasteID != transfer.PasteID {
		t.Fatalf("expected the share to point at the transfer record, got %#v", shares[0])
	}
}

func TestCancelTransferReleasesQuotaAndRejectsPublished(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "cancel-transfer@example.com")

	transfer, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, transferInput(transferItem("itm-1", "a.txt")))
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	uploadTestTransferItem(t, svc, user.User.ID, transfer.ID, "itm-1", "a.txt", []byte("payload"))

	quota, err := svc.Quota(user.User.ID)
	if err != nil {
		t.Fatalf("quota before cancel: %v", err)
	}
	if quota.ActivePasteCount != 1 || quota.ActiveStorageBytes == 0 {
		t.Fatalf("expected the draft to hold quota, got %#v", quota)
	}

	canceled, err := svc.CancelTransferWithContext(context.Background(), user.User.ID, transfer.ID)
	if err != nil {
		t.Fatalf("cancel transfer: %v", err)
	}
	if canceled.Status != TransferStatusCanceled || canceled.CanceledAt == nil {
		t.Fatalf("unexpected canceled transfer: %#v", canceled)
	}
	repeat, err := svc.CancelTransferWithContext(context.Background(), user.User.ID, transfer.ID)
	if err != nil {
		t.Fatalf("repeat cancel: %v", err)
	}
	if !repeat.CanceledAt.Equal(*canceled.CanceledAt) {
		t.Fatalf("expected cancel retries to be idempotent, got %#v", repeat)
	}
	quota, err = svc.Quota(user.User.ID)
	if err != nil {
		t.Fatalf("quota after cancel: %v", err)
	}
	if quota.ActivePasteCount != 0 || quota.ActiveStorageBytes != 0 {
		t.Fatalf("expected cancel to release the quota, got %#v", quota)
	}

	// Canceled transfers accept no uploads and cannot be published.
	if _, err := svc.PreflightTransferItemUploadWithContext(context.Background(), user.User.ID, transfer.ID, "itm-1"); !hasAppStatus(err, http.StatusConflict) {
		t.Fatalf("expected uploads into a canceled transfer to be rejected, got %v", err)
	}
	if _, err := svc.PublishTransferWithContext(context.Background(), user.User.ID, transfer.ID); !hasAppStatus(err, http.StatusGone) {
		t.Fatalf("expected publishing a canceled transfer to be rejected, got %v", err)
	}

	// Published transfers are not cancelable; their share is revoked instead.
	live, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, transferInput(transferItem("itm-1", "b.txt")))
	if err != nil {
		t.Fatalf("create published transfer: %v", err)
	}
	_, attachment := uploadTestTransferItem(t, svc, user.User.ID, live.ID, "itm-1", "b.txt", []byte("b"))
	runCleanScan(t, svc, attachment.ID)
	if _, err := svc.PublishTransferWithContext(context.Background(), user.User.ID, live.ID); err != nil {
		t.Fatalf("publish live transfer: %v", err)
	}
	if _, err := svc.CancelTransferWithContext(context.Background(), user.User.ID, live.ID); !hasAppStatus(err, http.StatusConflict) {
		t.Fatalf("expected a published transfer to reject cancel, got %v", err)
	}
}
