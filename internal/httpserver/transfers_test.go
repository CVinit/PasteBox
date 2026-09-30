package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

type transferResponse struct {
	Transfer app.TransferView `json:"transfer"`
}

type transferUploadResponse struct {
	Transfer   app.TransferView   `json:"transfer"`
	Attachment app.AttachmentView `json:"attachment"`
}

func newTransferTestServer(t *testing.T) (*app.Service, http.Handler) {
	t.Helper()
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	service := app.New(cfg)
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)
	return service, handler
}

func createTransfer(t *testing.T, client *httpTestClient, body string) app.TransferView {
	t.Helper()
	res := client.json(http.MethodPost, "/api/v1/transfers", body)
	assertStatus(t, res, http.StatusCreated)
	var decoded transferResponse
	decodeResponse(t, res, &decoded)
	return decoded.Transfer
}

func uploadTransferItem(t *testing.T, client *httpTestClient, transferID string, itemID string, fileName string, content []byte) transferUploadResponse {
	t.Helper()
	res := client.multipart("/api/v1/transfers/"+transferID+"/items/"+itemID, "file", fileName, content)
	assertStatus(t, res, http.StatusCreated)
	var decoded transferUploadResponse
	decodeResponse(t, res, &decoded)
	return decoded
}

func publishTransfer(t *testing.T, client *httpTestClient, transferID string) app.TransferView {
	t.Helper()
	res := client.json(http.MethodPost, "/api/v1/transfers/"+transferID+"/publish", "")
	assertStatus(t, res, http.StatusOK)
	var decoded transferResponse
	decodeResponse(t, res, &decoded)
	return decoded.Transfer
}

func TestTransferSendPublishAndDownloadHTTPContract(t *testing.T) {
	service, handler := newTransferTestServer(t)
	client := newHTTPTestClient(t, handler)
	registerHTTPUser(t, client, "sender@example.com", "Sender")

	transfer := createTransfer(t, client, `{"idempotencyKey":"send-1","expiresInSeconds":3600,"items":[{"itemId":"itm-1","fileName":"report.txt","contentType":"text/plain","size":13}]}`)
	if transfer.ID == "" || transfer.Status != "draft" || transfer.PasteID == "" {
		t.Fatalf("unexpected transfer body: %#v", transfer)
	}
	if len(transfer.Items) != 1 || transfer.Items[0].Status != "pending" || transfer.Items[0].FileName != "report.txt" {
		t.Fatalf("unexpected transfer items: %#v", transfer.Items)
	}
	if transfer.Share != nil {
		t.Fatalf("expected a draft transfer to expose no share, got %#v", transfer.Share)
	}

	// A retried create with the same idempotency key reuses the same transfer
	// instead of creating a second record.
	retried := createTransfer(t, client, `{"idempotencyKey":"send-1","expiresInSeconds":3600,"items":[{"itemId":"itm-1","fileName":"report.txt","contentType":"text/plain","size":13}]}`)
	if retried.ID != transfer.ID {
		t.Fatalf("expected idempotent create to reuse transfer %s, got %s", transfer.ID, retried.ID)
	}

	// Publishing before every file finished uploading must not expose anything.
	incomplete := client.json(http.MethodPost, "/api/v1/transfers/"+transfer.ID+"/publish", "")
	assertStatus(t, incomplete, http.StatusConflict)
	var incompleteBody map[string]string
	decodeResponse(t, incomplete, &incompleteBody)
	if incompleteBody["error"] != "transfer_incomplete" {
		t.Fatalf("expected transfer_incomplete, got %#v", incompleteBody)
	}
	noShares := client.json(http.MethodGet, "/api/v1/shares", "")
	assertStatus(t, noShares, http.StatusOK)
	var noSharesBody map[string]json.RawMessage
	decodeResponse(t, noShares, &noSharesBody)
	if got := string(noSharesBody["shares"]); got != "[]" {
		t.Fatalf("expected an unpublished transfer to create no share, got %s", got)
	}

	uploaded := uploadTransferItem(t, client, transfer.ID, "itm-1", "report.txt", []byte("transfer-body"))
	if uploaded.Attachment.ID == "" || uploaded.Attachment.PasteID != transfer.PasteID || uploaded.Attachment.Size != int64(len("transfer-body")) {
		t.Fatalf("unexpected attachment body: %#v", uploaded.Attachment)
	}
	if len(uploaded.Transfer.Items) != 1 || uploaded.Transfer.Items[0].Status != "uploaded" || uploaded.Transfer.Items[0].AttachmentID != uploaded.Attachment.ID {
		t.Fatalf("expected the transfer item to be marked uploaded, got %#v", uploaded.Transfer.Items)
	}

	// Retrying the same item must reuse the stored attachment, not duplicate it.
	retryUpload := client.multipart("/api/v1/transfers/"+transfer.ID+"/items/itm-1", "file", "report.txt", []byte("transfer-body"))
	assertStatus(t, retryUpload, http.StatusCreated)
	var retryBody transferUploadResponse
	decodeResponse(t, retryUpload, &retryBody)
	if retryBody.Attachment.ID != uploaded.Attachment.ID {
		t.Fatalf("expected the retried upload to reuse attachment %s, got %s", uploaded.Attachment.ID, retryBody.Attachment.ID)
	}
	record := client.json(http.MethodGet, "/api/v1/pastes/"+transfer.PasteID, "")
	assertStatus(t, record, http.StatusOK)
	var recordBody app.PasteView
	decodeResponse(t, record, &recordBody)
	if len(recordBody.Attachments) != 1 {
		t.Fatalf("expected one attachment on the transfer record, got %#v", recordBody.Attachments)
	}

	// Retrying with different bytes is a conflict rather than a silent replace.
	conflictUpload := client.multipart("/api/v1/transfers/"+transfer.ID+"/items/itm-1", "file", "report.txt", []byte("other-body"))
	assertStatus(t, conflictUpload, http.StatusConflict)
	var conflictBody map[string]string
	decodeResponse(t, conflictUpload, &conflictBody)
	if conflictBody["error"] != "transfer_item_conflict" {
		t.Fatalf("expected transfer_item_conflict, got %#v", conflictBody)
	}

	if err := service.RunAttachmentScan(staticHTTPScanner{result: app.ScanResult{Status: "clean"}}, uploaded.Attachment.ID); err != nil {
		t.Fatalf("run clean scan: %v", err)
	}

	published := publishTransfer(t, client, transfer.ID)
	if published.Status != "published" || published.PublishedAt == nil {
		t.Fatalf("expected a published transfer, got %#v", published)
	}
	if published.Share == nil || published.Share.Token == "" || published.Share.URL == "" {
		t.Fatalf("expected the published transfer to expose share credentials, got %#v", published.Share)
	}
	if !published.Share.ExpiresAt.After(time.Now()) {
		t.Fatalf("expected the share to keep a future expiry, got %#v", published.Share.ExpiresAt)
	}

	// Publish retries are idempotent: the same share is returned and no second
	// share row appears.
	republished := publishTransfer(t, client, transfer.ID)
	if republished.Share == nil || republished.Share.ID != published.Share.ID || republished.Share.Token != published.Share.Token {
		t.Fatalf("expected publish retry to reuse share %s, got %#v", published.Share.ID, republished.Share)
	}
	shares := client.json(http.MethodGet, "/api/v1/shares", "")
	assertStatus(t, shares, http.StatusOK)
	var sharesBody struct {
		Shares []app.ShareView `json:"shares"`
	}
	decodeResponse(t, shares, &sharesBody)
	if len(sharesBody.Shares) != 1 {
		t.Fatalf("expected exactly one share for the transfer, got %#v", sharesBody.Shares)
	}

	// The sender can find the send in transfer records and in content records.
	transfers := client.json(http.MethodGet, "/api/v1/transfers", "")
	assertStatus(t, transfers, http.StatusOK)
	var transfersBody struct {
		Transfers []app.TransferView `json:"transfers"`
	}
	decodeResponse(t, transfers, &transfersBody)
	if len(transfersBody.Transfers) != 1 || transfersBody.Transfers[0].ID != transfer.ID {
		t.Fatalf("expected the transfer in the sender records, got %#v", transfersBody.Transfers)
	}
	records := client.json(http.MethodGet, "/api/v1/pastes", "")
	assertStatus(t, records, http.StatusOK)
	var recordsBody struct {
		Pastes []app.PasteView `json:"pastes"`
	}
	decodeResponse(t, records, &recordsBody)
	found := false
	for _, paste := range recordsBody.Pastes {
		if paste.ID == transfer.PasteID {
			found = true
			if len(paste.Attachments) != 1 {
				t.Fatalf("expected the record to keep its attachment, got %#v", paste.Attachments)
			}
		}
	}
	if !found {
		t.Fatalf("expected the transfer record in /pastes, got %#v", recordsBody.Pastes)
	}

	// The recipient opens the link, claims it, and downloads the single file.
	recipient := newHTTPTestClient(t, handler)
	access := recipient.json(http.MethodPost, "/api/v1/shares/"+published.Share.Token+"/access", `{"password":""}`)
	assertStatus(t, access, http.StatusOK)
	claimTransferShare(t, recipient, published.Share.Token, "", "claim-transfer-download")
	download := downloadSharedAttachment(t, recipient, published.Share.Token, uploaded.Attachment.ID)
	assertStatus(t, download, http.StatusOK)
	if got := download.Body.String(); got != "transfer-body" {
		t.Fatalf("expected the shared download body, got %q", got)
	}
}

func TestTransferRejectsForeignOwnersAndIncompleteItems(t *testing.T) {
	_, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "owner-transfer@example.com", "Owner")
	stranger := newHTTPTestClient(t, handler)
	registerHTTPUser(t, stranger, "stranger-transfer@example.com", "Stranger")

	transfer := createTransfer(t, owner, `{"expiresInSeconds":3600,"items":[{"itemId":"itm-1","fileName":"a.txt"},{"itemId":"itm-2","fileName":"b.txt"}]}`)

	for _, probe := range []struct {
		name string
		run  func() *httptest.ResponseRecorder
	}{
		{name: "get", run: func() *httptest.ResponseRecorder {
			return stranger.json(http.MethodGet, "/api/v1/transfers/"+transfer.ID, "")
		}},
		{name: "upload", run: func() *httptest.ResponseRecorder {
			return stranger.multipart("/api/v1/transfers/"+transfer.ID+"/items/itm-1", "file", "a.txt", []byte("a"))
		}},
		{name: "publish", run: func() *httptest.ResponseRecorder {
			return stranger.json(http.MethodPost, "/api/v1/transfers/"+transfer.ID+"/publish", "")
		}},
		{name: "cancel", run: func() *httptest.ResponseRecorder {
			return stranger.json(http.MethodPost, "/api/v1/transfers/"+transfer.ID+"/cancel", "")
		}},
	} {
		res := probe.run()
		if res.Code != http.StatusNotFound {
			t.Fatalf("expected %s from a foreign owner to be hidden, got %d: %s", probe.name, res.Code, res.Body.String())
		}
	}

	// One uploaded file is not enough to publish a two-file transfer.
	uploadTransferItem(t, owner, transfer.ID, "itm-1", "a.txt", []byte("a"))
	partial := owner.json(http.MethodPost, "/api/v1/transfers/"+transfer.ID+"/publish", "")
	assertStatus(t, partial, http.StatusConflict)

	uploadTransferItem(t, owner, transfer.ID, "itm-2", "b.txt", []byte("b"))
	published := publishTransfer(t, owner, transfer.ID)
	if published.Share == nil || len(published.Items) != 2 {
		t.Fatalf("expected both items to publish, got %#v", published)
	}

	// Uploading into a published transfer is refused.
	late := owner.multipart("/api/v1/transfers/"+transfer.ID+"/items/itm-3", "file", "c.txt", []byte("c"))
	assertStatus(t, late, http.StatusNotFound)
}

func TestTransferCancelReleasesQuotaAndRejectsPublished(t *testing.T) {
	service, handler := newTransferTestServer(t)
	client := newHTTPTestClient(t, handler)
	registerHTTPUser(t, client, "cancel@example.com", "Cancel")

	// Canceling an unfinished transfer frees the record and its quota.
	transfer := createTransfer(t, client, `{"expiresInSeconds":3600,"items":[{"itemId":"itm-1","fileName":"a.txt"}]}`)
	uploadTransferItem(t, client, transfer.ID, "itm-1", "a.txt", []byte("a"))

	quota := client.json(http.MethodGet, "/api/v1/quota", "")
	assertStatus(t, quota, http.StatusOK)
	var quotaBefore app.QuotaView
	decodeResponse(t, quota, &quotaBefore)
	if quotaBefore.ActivePasteCount != 1 || quotaBefore.ActiveStorageBytes == 0 {
		t.Fatalf("expected the draft transfer to hold quota, got %#v", quotaBefore)
	}

	cancel := client.json(http.MethodPost, "/api/v1/transfers/"+transfer.ID+"/cancel", "")
	assertStatus(t, cancel, http.StatusOK)
	var canceled transferResponse
	decodeResponse(t, cancel, &canceled)
	if canceled.Transfer.Status != "canceled" || canceled.Transfer.CanceledAt == nil {
		t.Fatalf("expected a canceled transfer, got %#v", canceled.Transfer)
	}

	// Repeating the cancel keeps the same terminal state.
	repeat := client.json(http.MethodPost, "/api/v1/transfers/"+transfer.ID+"/cancel", "")
	assertStatus(t, repeat, http.StatusOK)
	var repeated transferResponse
	decodeResponse(t, repeat, &repeated)
	if repeated.Transfer.Status != "canceled" || !repeated.Transfer.CanceledAt.Equal(*canceled.Transfer.CanceledAt) {
		t.Fatalf("expected cancel retries to be idempotent, got %#v", repeated.Transfer)
	}

	quota = client.json(http.MethodGet, "/api/v1/quota", "")
	assertStatus(t, quota, http.StatusOK)
	var quotaAfter app.QuotaView
	decodeResponse(t, quota, &quotaAfter)
	if quotaAfter.ActivePasteCount != 0 || quotaAfter.ActiveStorageBytes != 0 {
		t.Fatalf("expected cancel to release the record quota, got %#v", quotaAfter)
	}

	// An unfinished transfer cannot be published once it is canceled.
	publishCanceled := client.json(http.MethodPost, "/api/v1/transfers/"+transfer.ID+"/publish", "")
	assertStatus(t, publishCanceled, http.StatusGone)

	// A published transfer is not cancelable: its link is revoked through the
	// share controls instead.
	published := createTransfer(t, client, `{"expiresInSeconds":3600,"items":[{"itemId":"itm-1","fileName":"b.txt"}]}`)
	publishedUpload := uploadTransferItem(t, client, published.ID, "itm-1", "b.txt", []byte("b"))
	if err := service.RunAttachmentScan(staticHTTPScanner{result: app.ScanResult{Status: "clean"}}, publishedUpload.Attachment.ID); err != nil {
		t.Fatalf("run clean scan: %v", err)
	}
	live := publishTransfer(t, client, published.ID)
	if live.Share == nil {
		t.Fatalf("expected share credentials after publish, got %#v", live)
	}
	rejected := client.json(http.MethodPost, "/api/v1/transfers/"+published.ID+"/cancel", "")
	assertStatus(t, rejected, http.StatusConflict)
	var rejectedBody map[string]string
	decodeResponse(t, rejected, &rejectedBody)
	if rejectedBody["error"] != "transfer_not_draft" {
		t.Fatalf("expected transfer_not_draft, got %#v", rejectedBody)
	}
	recipient := newHTTPTestClient(t, handler)
	access := recipient.json(http.MethodPost, "/api/v1/shares/"+live.Share.Token+"/access", `{"password":""}`)
	assertStatus(t, access, http.StatusOK)
}

func TestGuestTransferCancelReleasesQuota(t *testing.T) {
	_, handler := newTransferTestServer(t)
	client := newHTTPTestClient(t, handler)

	const guestToken = "guest-cancel-token"
	create := client.json(http.MethodPost, "/api/v1/guest/transfers", `{"guestToken":"`+guestToken+`","expiresInSeconds":3600,"items":[{"itemId":"itm-1","fileName":"guest.txt"}]}`)
	assertStatus(t, create, http.StatusCreated)
	var created struct {
		Transfer app.TransferView `json:"transfer"`
	}
	decodeResponse(t, create, &created)
	upload := client.multipartWithFields("/api/v1/guest/transfers/"+created.Transfer.ID+"/items/itm-1", "file", "guest.txt", []byte("guest"), map[string]string{"guestToken": guestToken})
	assertStatus(t, upload, http.StatusCreated)

	quota := client.json(http.MethodGet, "/api/v1/quota", "")
	assertStatus(t, quota, http.StatusUnauthorized)

	cancel := client.json(http.MethodPost, "/api/v1/guest/transfers/"+created.Transfer.ID+"/cancel", `{"guestToken":"`+guestToken+`"}`)
	assertStatus(t, cancel, http.StatusOK)
	var canceled transferResponse
	decodeResponse(t, cancel, &canceled)
	if canceled.Transfer.Status != "canceled" {
		t.Fatalf("expected a canceled guest transfer, got %#v", canceled.Transfer)
	}

	// The canceled transfer accepts no further uploads and cannot be published.
	late := client.multipartWithFields("/api/v1/guest/transfers/"+created.Transfer.ID+"/items/itm-1", "file", "guest.txt", []byte("guest"), map[string]string{"guestToken": guestToken})
	assertStatus(t, late, http.StatusConflict)
	publish := client.json(http.MethodPost, "/api/v1/guest/transfers/"+created.Transfer.ID+"/publish", `{"guestToken":"`+guestToken+`"}`)
	assertStatus(t, publish, http.StatusGone)

	// A different guest token cannot cancel someone else's transfer.
	other := newHTTPTestClient(t, handler)
	foreign := other.json(http.MethodPost, "/api/v1/guest/transfers/"+created.Transfer.ID+"/cancel", `{"guestToken":"other-guest-token"}`)
	assertStatus(t, foreign, http.StatusNotFound)
}

func TestGuestTransferPublishAndDownloadHTTPContract(t *testing.T) {
	service, handler := newTransferTestServer(t)
	client := newHTTPTestClient(t, handler)

	const guestToken = "guest-transfer-token"
	create := client.json(http.MethodPost, "/api/v1/guest/transfers", `{"guestToken":"`+guestToken+`","expiresInSeconds":3600,"items":[{"itemId":"itm-1","fileName":"guest.txt","contentType":"text/plain","size":11}]}`)
	assertStatus(t, create, http.StatusCreated)
	var created struct {
		GuestToken string           `json:"guestToken"`
		Transfer   app.TransferView `json:"transfer"`
	}
	decodeResponse(t, create, &created)
	if created.GuestToken != guestToken || created.Transfer.ID == "" || created.Transfer.Status != "draft" {
		t.Fatalf("unexpected guest transfer body: %#v", created)
	}

	upload := client.multipartWithFields("/api/v1/guest/transfers/"+created.Transfer.ID+"/items/itm-1", "file", "guest.txt", []byte("guest-body"), map[string]string{"guestToken": guestToken})
	assertStatus(t, upload, http.StatusCreated)
	var uploaded transferUploadResponse
	decodeResponse(t, upload, &uploaded)
	if uploaded.Attachment.ID == "" || uploaded.Attachment.PasteID != created.Transfer.PasteID {
		t.Fatalf("unexpected guest attachment body: %#v", uploaded.Attachment)
	}
	if err := service.RunAttachmentScan(staticHTTPScanner{result: app.ScanResult{Status: "clean"}}, uploaded.Attachment.ID); err != nil {
		t.Fatalf("run clean scan: %v", err)
	}

	publish := client.json(http.MethodPost, "/api/v1/guest/transfers/"+created.Transfer.ID+"/publish", `{"guestToken":"`+guestToken+`"}`)
	assertStatus(t, publish, http.StatusOK)
	var published transferResponse
	decodeResponse(t, publish, &published)
	if published.Transfer.Status != "published" || published.Transfer.Share == nil {
		t.Fatalf("unexpected guest publish body: %#v", published.Transfer)
	}

	recipient := newHTTPTestClient(t, handler)
	access := recipient.json(http.MethodPost, "/api/v1/shares/"+published.Transfer.Share.Token+"/access", `{"password":""}`)
	assertStatus(t, access, http.StatusOK)
	claimTransferShare(t, recipient, published.Transfer.Share.Token, "", "claim-guest-download")
	download := downloadSharedAttachment(t, recipient, published.Transfer.Share.Token, uploaded.Attachment.ID)
	assertStatus(t, download, http.StatusOK)
	if got := download.Body.String(); got != "guest-body" {
		t.Fatalf("expected the guest shared download body, got %q", got)
	}

	// A different guest token cannot touch the transfer.
	other := newHTTPTestClient(t, handler)
	foreign := other.json(http.MethodPost, "/api/v1/guest/transfers/"+created.Transfer.ID+"/publish", `{"guestToken":"other-guest-token"}`)
	assertStatus(t, foreign, http.StatusNotFound)
}

func TestLegacyPasteShareDownloadStillWorksAlongsideTransfers(t *testing.T) {
	service, handler := newTransferTestServer(t)
	client := newHTTPTestClient(t, handler)
	registerHTTPUser(t, client, "legacy@example.com", "Legacy")

	createPaste := client.json(http.MethodPost, "/api/v1/pastes", `{"title":"Legacy","text":"hello","tags":[],"expiresInSeconds":3600}`)
	assertStatus(t, createPaste, http.StatusCreated)
	var paste app.PasteView
	decodeResponse(t, createPaste, &paste)

	upload := client.multipart("/api/v1/pastes/"+paste.ID+"/attachments", "file", "legacy.txt", []byte("legacy"))
	assertStatus(t, upload, http.StatusCreated)
	var attachment app.AttachmentView
	decodeResponse(t, upload, &attachment)
	if err := service.RunAttachmentScan(staticHTTPScanner{result: app.ScanResult{Status: "clean"}}, attachment.ID); err != nil {
		t.Fatalf("run clean scan: %v", err)
	}

	createShare := client.json(http.MethodPost, "/api/v1/pastes/"+paste.ID+"/shares", `{"password":"","loginRequired":false,"maxVisits":0,"maxDownloads":0,"expiresInSeconds":1800}`)
	assertStatus(t, createShare, http.StatusCreated)
	var share app.ShareView
	decodeResponse(t, createShare, &share)

	recipient := newHTTPTestClient(t, handler)
	access := recipient.json(http.MethodPost, "/api/v1/shares/"+share.Token+"/access", `{"password":""}`)
	assertStatus(t, access, http.StatusOK)
	download := recipient.json(http.MethodGet, "/api/v1/shares/"+share.Token+"/attachments/"+attachment.ID+"/download", "")
	assertStatus(t, download, http.StatusOK)
	if got := download.Body.String(); got != "legacy" {
		t.Fatalf("expected the legacy shared download body, got %q", got)
	}
}
