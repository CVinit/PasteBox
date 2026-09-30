package httpserver

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

// newMultiFileTransferServer starts a server whose guest upload limits are small
// enough to drive a real partial failure without allocating megabytes in tests.
func newMultiFileTransferServer(t *testing.T, guest app.GuestUploadConfig) (*app.Service, http.Handler, string) {
	t.Helper()
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	service := app.New(cfg)
	admin, err := service.SeedAdmin("multifile-admin@example.com", "password123")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := service.AdminUpdateRuntimeConfig(admin.ID, app.RuntimeConfigPatch{
		GuestUploads: &app.GuestUploadConfigPatch{
			Enabled:                  ptr(guest.Enabled),
			SingleFileBytes:          ptr(guest.SingleFileBytes),
			SinglePasteBytes:         ptr(guest.SinglePasteBytes),
			AttachmentsPerPasteLimit: ptr(guest.AttachmentsPerPasteLimit),
			ActiveStorageBytes:       ptr(guest.ActiveStorageBytes),
			DailyUploadBytes:         ptr(guest.DailyUploadBytes),
		},
	}); err != nil {
		t.Fatalf("configure guest limits: %v", err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)
	return service, handler, admin.ID
}

// setGuestPasteBytes raises or lowers the guest total size limit mid-test so a
// quota failure can be recovered the way a real limit change would allow.
func setGuestPasteBytes(t *testing.T, service *app.Service, adminID string, limit int64) {
	t.Helper()
	if _, err := service.AdminUpdateRuntimeConfig(adminID, app.RuntimeConfigPatch{
		GuestUploads: &app.GuestUploadConfigPatch{SinglePasteBytes: ptr(limit)},
	}); err != nil {
		t.Fatalf("update guest paste limit: %v", err)
	}
}

func guestMultiFileConfig() app.GuestUploadConfig {
	return app.GuestUploadConfig{
		Enabled:                  true,
		SingleFileBytes:          64,
		SinglePasteBytes:         24,
		AttachmentsPerPasteLimit: 5,
		ActiveStorageBytes:       1 << 20,
		DailyUploadBytes:         1 << 20,
	}
}

func createGuestTransfer(t *testing.T, client *httpTestClient, token string, items string) app.TransferView {
	t.Helper()
	res := client.json(http.MethodPost, "/api/v1/guest/transfers",
		`{"guestToken":"`+token+`","expiresInSeconds":3600,"items":[`+items+`]}`)
	assertStatus(t, res, http.StatusCreated)
	var body struct {
		Transfer app.TransferView `json:"transfer"`
	}
	decodeResponse(t, res, &body)
	return body.Transfer
}

func uploadGuestTransferItem(t *testing.T, client *httpTestClient, token string, transferID string, itemID string, fileName string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	return client.multipartWithFields(
		"/api/v1/guest/transfers/"+transferID+"/items/"+itemID,
		"file", fileName, content, map[string]string{"guestToken": token},
	)
}

func decodeUploadResponse(t *testing.T, res *httptest.ResponseRecorder) transferUploadResponse {
	t.Helper()
	var body transferUploadResponse
	decodeResponse(t, res, &body)
	return body
}

func decodeErrorCode(t *testing.T, res *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	decodeResponse(t, res, &body)
	return body["error"]
}

// TestGuestMultiFileTransferPartialFailureRetryAndPublish covers the queue
// contract: one send holds several files, a file that fails stays retryable
// while its siblings are not re-uploaded, and the share only exists once every
// declared item finished uploading.
func TestGuestMultiFileTransferPartialFailureRetryAndPublish(t *testing.T) {
	service, handler, adminID := newMultiFileTransferServer(t, guestMultiFileConfig())
	client := newHTTPTestClient(t, handler)

	const token = "guest-multifile-token"
	transfer := createGuestTransfer(t, client, token, `{"itemId":"itm-1","fileName":"a.txt"},{"itemId":"itm-2","fileName":"b.txt"},{"itemId":"itm-3","fileName":"c.txt"}`)
	if len(transfer.Items) != 3 || transfer.Share != nil {
		t.Fatalf("expected a three item draft without credentials, got %#v", transfer)
	}

	first := decodeUploadResponse(t, uploadGuestTransferItem(t, client, token, transfer.ID, "itm-1", "a.txt", []byte("aaaaaaaa")))
	second := decodeUploadResponse(t, uploadGuestTransferItem(t, client, token, transfer.ID, "itm-2", "b.txt", []byte("bbbbbbbb")))
	if first.Attachment.ID == "" || second.Attachment.ID == "" {
		t.Fatalf("expected the first two files to upload, got %#v / %#v", first.Attachment, second.Attachment)
	}

	// The third file breaks the send total, so it fails on its own and the
	// refusal names the limit that actually applied.
	third := uploadGuestTransferItem(t, client, token, transfer.ID, "itm-3", "c.txt", bytes.Repeat([]byte("c"), 16))
	assertStatus(t, third, http.StatusRequestEntityTooLarge)
	if code := decodeErrorCode(t, third); code != "paste_too_large" {
		t.Fatalf("expected paste_too_large for the send that ran out of total size, got %q", code)
	}

	// A partially uploaded send must not be publishable, and the failure must
	// not leave share credentials behind.
	incomplete := client.json(http.MethodPost, "/api/v1/guest/transfers/"+transfer.ID+"/publish", `{"guestToken":"`+token+`"}`)
	assertStatus(t, incomplete, http.StatusConflict)
	if code := decodeErrorCode(t, incomplete); code != "transfer_incomplete" {
		t.Fatalf("expected transfer_incomplete, got %q", code)
	}

	// Retrying a finished item reuses its attachment instead of re-uploading it.
	retried := decodeUploadResponse(t, uploadGuestTransferItem(t, client, token, transfer.ID, "itm-1", "a.txt", []byte("aaaaaaaa")))
	if retried.Attachment.ID != first.Attachment.ID {
		t.Fatalf("expected the finished item to keep attachment %s, got %s", first.Attachment.ID, retried.Attachment.ID)
	}

	// Once the limit allows it, only the failed item is retried and the send
	// becomes publishable.
	setGuestPasteBytes(t, service, adminID, 64)
	recovered := uploadGuestTransferItem(t, client, token, transfer.ID, "itm-3", "c.txt", bytes.Repeat([]byte("c"), 16))
	assertStatus(t, recovered, http.StatusCreated)
	recoveredBody := decodeUploadResponse(t, recovered)
	if len(recoveredBody.Transfer.Items) != 3 {
		t.Fatalf("expected the manifest to keep three items, got %#v", recoveredBody.Transfer.Items)
	}

	for _, attachmentID := range []string{first.Attachment.ID, second.Attachment.ID, recoveredBody.Attachment.ID} {
		if err := service.RunAttachmentScan(staticHTTPScanner{result: app.ScanResult{Status: "clean"}}, attachmentID); err != nil {
			t.Fatalf("run clean scan: %v", err)
		}
	}

	publish := client.json(http.MethodPost, "/api/v1/guest/transfers/"+transfer.ID+"/publish", `{"guestToken":"`+token+`"}`)
	assertStatus(t, publish, http.StatusOK)
	var published transferResponse
	decodeResponse(t, publish, &published)
	if published.Transfer.Share == nil || published.Transfer.Share.Token == "" {
		t.Fatalf("expected share credentials after recovery, got %#v", published.Transfer)
	}

	// The recipient receives the complete list and can download every file.
	recipient := newHTTPTestClient(t, handler)
	access := recipient.json(http.MethodPost, "/api/v1/shares/"+published.Transfer.Share.Token+"/access", `{"password":""}`)
	assertStatus(t, access, http.StatusOK)
	var accessBody struct {
		Paste app.PasteView `json:"paste"`
	}
	decodeResponse(t, access, &accessBody)
	if len(accessBody.Paste.Attachments) != 3 {
		t.Fatalf("expected the shared record to expose three files, got %#v", accessBody.Paste.Attachments)
	}
	// One claim covers every file of the batch, so the recipient downloads the
	// whole list inside a single session.
	claimTransferShare(t, recipient, published.Transfer.Share.Token, "", "claim-multifile-recovery")
	for _, attachment := range accessBody.Paste.Attachments {
		download := downloadSharedAttachment(t, recipient, published.Transfer.Share.Token, attachment.ID)
		assertStatus(t, download, http.StatusOK)
		if download.Body.Len() != int(attachment.Size) {
			t.Fatalf("expected %d bytes for %s, got %d", attachment.Size, attachment.FileName, download.Body.Len())
		}
	}
}

// TestTransferUploadNamesTheLimitThatApplied covers the multi-file quota
// message: a send that runs out of daily traffic or account storage must name
// that limit, with the same status the single-upload path reports, instead of
// blaming the size of one file.
func TestTransferUploadNamesTheLimitThatApplied(t *testing.T) {
	for _, probe := range []struct {
		name   string
		config app.GuestUploadConfig
		status int
		code   string
	}{
		{
			name: "daily upload traffic",
			config: app.GuestUploadConfig{
				Enabled: true, SingleFileBytes: 64, SinglePasteBytes: 1024,
				AttachmentsPerPasteLimit: 5, ActiveStorageBytes: 1 << 20, DailyUploadBytes: 24,
			},
			status: http.StatusForbidden,
			code:   "daily_upload_limit",
		},
		{
			name: "account storage",
			config: app.GuestUploadConfig{
				Enabled: true, SingleFileBytes: 64, SinglePasteBytes: 1024,
				AttachmentsPerPasteLimit: 5, ActiveStorageBytes: 24, DailyUploadBytes: 1 << 20,
			},
			status: http.StatusForbidden,
			code:   "storage_limit",
		},
		{
			name: "send total size",
			config: app.GuestUploadConfig{
				Enabled: true, SingleFileBytes: 64, SinglePasteBytes: 24,
				AttachmentsPerPasteLimit: 5, ActiveStorageBytes: 1 << 20, DailyUploadBytes: 1 << 20,
			},
			status: http.StatusRequestEntityTooLarge,
			code:   "paste_too_large",
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			_, handler, _ := newMultiFileTransferServer(t, probe.config)
			client := newHTTPTestClient(t, handler)

			const token = "guest-limit-token"
			transfer := createGuestTransfer(t, client, token, `{"itemId":"itm-1","fileName":"a.txt"},{"itemId":"itm-2","fileName":"b.txt"},{"itemId":"itm-3","fileName":"c.txt"}`)
			uploadGuestTransferItem(t, client, token, transfer.ID, "itm-1", "a.txt", []byte("aaaaaaaa"))
			uploadGuestTransferItem(t, client, token, transfer.ID, "itm-2", "b.txt", []byte("bbbbbbbb"))

			third := uploadGuestTransferItem(t, client, token, transfer.ID, "itm-3", "c.txt", bytes.Repeat([]byte("c"), 16))
			assertStatus(t, third, probe.status)
			if code := decodeErrorCode(t, third); code != probe.code {
				t.Fatalf("expected %s, got %q", probe.code, code)
			}
		})
	}
}

// TestTransferIncompleteManifestStaysPrivate checks that an unpublished send
// exposes no credentials, stays hidden from other visitors, and freezes its
// manifest once published.
func TestTransferIncompleteManifestStaysPrivate(t *testing.T) {
	service, handler, _ := newMultiFileTransferServer(t, guestMultiFileConfig())
	client := newHTTPTestClient(t, handler)

	const token = "guest-private-token"
	transfer := createGuestTransfer(t, client, token, `{"itemId":"itm-1","fileName":"a.txt"},{"itemId":"itm-2","fileName":"b.txt"}`)
	uploaded := decodeUploadResponse(t, uploadGuestTransferItem(t, client, token, transfer.ID, "itm-1", "a.txt", []byte("aaaaaaaa")))
	if uploaded.Attachment.ID == "" {
		t.Fatalf("expected the first file to upload, got %#v", uploaded)
	}

	// Another visitor holding a different guest token cannot see or publish it.
	other := newHTTPTestClient(t, handler)
	foreign := other.json(http.MethodPost, "/api/v1/guest/transfers/"+transfer.ID+"/publish", `{"guestToken":"someone-else"}`)
	assertStatus(t, foreign, http.StatusNotFound)
	foreignUpload := uploadGuestTransferItem(t, other, "someone-else", transfer.ID, "itm-2", "b.txt", []byte("bbbbbbbb"))
	assertStatus(t, foreignUpload, http.StatusNotFound)

	// An incomplete send returns no credentials, so there is no link to leak.
	incomplete := client.json(http.MethodPost, "/api/v1/guest/transfers/"+transfer.ID+"/publish", `{"guestToken":"`+token+`"}`)
	assertStatus(t, incomplete, http.StatusConflict)
	if strings.Contains(incomplete.Body.String(), "share") {
		t.Fatalf("expected an incomplete publish to expose no share, got %s", incomplete.Body.String())
	}

	// Nothing is downloadable without share credentials.
	anonymous := newHTTPTestClient(t, handler)
	download := anonymous.json(http.MethodGet, "/api/v1/shares/"+transfer.ID+"/attachments/"+uploaded.Attachment.ID+"/download", "")
	if download.Code == http.StatusOK {
		t.Fatalf("expected an unshared attachment to stay unreachable, got %d", download.Code)
	}

	// Completing and publishing freezes the manifest: a declared item keeps its
	// attachment, and an undeclared item can never join the send.
	uploadGuestTransferItem(t, client, token, transfer.ID, "itm-2", "b.txt", []byte("bbbbbbbb"))
	if err := service.RunAttachmentScan(staticHTTPScanner{result: app.ScanResult{Status: "clean"}}, uploaded.Attachment.ID); err != nil {
		t.Fatalf("run clean scan: %v", err)
	}
	publish := client.json(http.MethodPost, "/api/v1/guest/transfers/"+transfer.ID+"/publish", `{"guestToken":"`+token+`"}`)
	assertStatus(t, publish, http.StatusOK)
	var published transferResponse
	decodeResponse(t, publish, &published)

	late := uploadGuestTransferItem(t, client, token, transfer.ID, "itm-3", "c.txt", []byte("c"))
	assertStatus(t, late, http.StatusNotFound)
	replaced := uploadGuestTransferItem(t, client, token, transfer.ID, "itm-1", "a.txt", []byte("different"))
	assertStatus(t, replaced, http.StatusConflict)
	recipient := newHTTPTestClient(t, handler)
	access := recipient.json(http.MethodPost, "/api/v1/shares/"+published.Transfer.Share.Token+"/access", `{"password":""}`)
	assertStatus(t, access, http.StatusOK)
	var accessBody struct {
		Paste app.PasteView `json:"paste"`
	}
	decodeResponse(t, access, &accessBody)
	if len(accessBody.Paste.Attachments) != 2 {
		t.Fatalf("expected the frozen manifest to hold two files, got %#v", accessBody.Paste.Attachments)
	}
}

// TestTransferConcurrentItemUploadKeepsOneAttachment covers the retry
// guarantee under concurrency: racing uploads of one declared item bind a
// single attachment and never duplicate it on the send record.
func TestTransferConcurrentItemUploadKeepsOneAttachment(t *testing.T) {
	service, handler, _ := newMultiFileTransferServer(t, guestMultiFileConfig())
	client := newHTTPTestClient(t, handler)
	client.ensureCSRF()

	const token = "guest-concurrent-token"
	transfer := createGuestTransfer(t, client, token, `{"itemId":"itm-1","fileName":"a.txt"}`)

	content, contentType := multipartPayload(t, "file", "a.txt", []byte("aaaaaaaa"), nil)
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/api/v1/guest/transfers/"+transfer.ID+"/items/itm-1", bytes.NewReader(content))
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-PasteBox-Guest-Token", token)
			req.Header.Set(csrfHeaderName, client.csrfToken)
			for _, cookie := range client.cookies {
				req.AddCookie(cookie)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			results[i] = res
		}()
	}
	close(start)
	wg.Wait()

	attachmentIDs := map[string]bool{}
	for i, res := range results {
		assertStatus(t, res, http.StatusCreated)
		attachmentIDs[decodeUploadResponse(t, res).Attachment.ID] = true
		if i == 0 {
			continue
		}
	}
	if len(attachmentIDs) != 1 {
		t.Fatalf("expected concurrent uploads to share one attachment, got %#v", attachmentIDs)
	}

	var attachmentID string
	for id := range attachmentIDs {
		attachmentID = id
	}
	if err := service.RunAttachmentScan(staticHTTPScanner{result: app.ScanResult{Status: "clean"}}, attachmentID); err != nil {
		t.Fatalf("run clean scan: %v", err)
	}
	publish := client.json(http.MethodPost, "/api/v1/guest/transfers/"+transfer.ID+"/publish", `{"guestToken":"`+token+`"}`)
	assertStatus(t, publish, http.StatusOK)
	var published transferResponse
	decodeResponse(t, publish, &published)
	if published.Transfer.Share == nil {
		t.Fatalf("expected share credentials after concurrent uploads, got %#v", published.Transfer)
	}

	recipient := newHTTPTestClient(t, handler)
	access := recipient.json(http.MethodPost, "/api/v1/shares/"+published.Transfer.Share.Token+"/access", `{"password":""}`)
	assertStatus(t, access, http.StatusOK)
	var accessBody struct {
		Paste app.PasteView `json:"paste"`
	}
	decodeResponse(t, access, &accessBody)
	if len(accessBody.Paste.Attachments) != 1 {
		t.Fatalf("expected one attachment after a racing upload, got %#v", accessBody.Paste.Attachments)
	}
}

// TestTransferPublishRaceReturnsOneShare covers the publish competition: two
// simultaneous publishes of the same send agree on one set of credentials.
func TestTransferPublishRaceReturnsOneShare(t *testing.T) {
	service, handler, _ := newMultiFileTransferServer(t, guestMultiFileConfig())
	client := newHTTPTestClient(t, handler)
	client.ensureCSRF()

	const token = "guest-publish-race-token"
	transfer := createGuestTransfer(t, client, token, `{"itemId":"itm-1","fileName":"a.txt"},{"itemId":"itm-2","fileName":"b.txt"}`)
	for _, item := range []struct{ id, name, body string }{{"itm-1", "a.txt", "aaaaaaaa"}, {"itm-2", "b.txt", "bbbbbbbb"}} {
		uploaded := uploadGuestTransferItem(t, client, token, transfer.ID, item.id, item.name, []byte(item.body))
		assertStatus(t, uploaded, http.StatusCreated)
		if err := service.RunAttachmentScan(staticHTTPScanner{result: app.ScanResult{Status: "clean"}}, decodeUploadResponse(t, uploaded).Attachment.ID); err != nil {
			t.Fatalf("run clean scan: %v", err)
		}
	}

	body := `{"guestToken":"` + token + `"}`
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/api/v1/guest/transfers/"+transfer.ID+"/publish", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(csrfHeaderName, client.csrfToken)
			for _, cookie := range client.cookies {
				req.AddCookie(cookie)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			results[i] = res
		}()
	}
	close(start)
	wg.Wait()

	shareIDs := map[string]string{}
	for _, res := range results {
		assertStatus(t, res, http.StatusOK)
		var published transferResponse
		decodeResponse(t, res, &published)
		if published.Transfer.Share == nil {
			t.Fatalf("expected share credentials from a racing publish, got %#v", published.Transfer)
		}
		shareIDs[published.Transfer.Share.ID] = published.Transfer.Share.Token
	}
	if len(shareIDs) != 1 {
		t.Fatalf("expected one share for a racing publish, got %#v", shareIDs)
	}

	// The single share still serves the whole manifest.
	var shareToken string
	for _, value := range shareIDs {
		shareToken = value
	}
	recipient := newHTTPTestClient(t, handler)
	access := recipient.json(http.MethodPost, "/api/v1/shares/"+shareToken+"/access", `{"password":""}`)
	assertStatus(t, access, http.StatusOK)
	var accessBody struct {
		Paste app.PasteView `json:"paste"`
	}
	decodeResponse(t, access, &accessBody)
	if len(accessBody.Paste.Attachments) != 2 {
		t.Fatalf("expected both files on the raced share, got %#v", accessBody.Paste.Attachments)
	}
}

// TestAuthedMultiFileTransferGroupsRecordsBySend covers the logged-in surface:
// one send with three files stays one transfer record, one content record, and
// one share whose list holds every file.
func TestAuthedMultiFileTransferGroupsRecordsBySend(t *testing.T) {
	service, handler, _ := newMultiFileTransferServer(t, guestMultiFileConfig())
	client := newHTTPTestClient(t, handler)
	registerHTTPUser(t, client, "multifile@example.com", "Multi")

	transfer := createTransfer(t, client, `{"idempotencyKey":"send-multi","expiresInSeconds":3600,"items":[{"itemId":"itm-1","fileName":"report.txt"},{"itemId":"itm-2","fileName":"report.txt"},{"itemId":"itm-3","fileName":"notes.md"}]}`)
	if len(transfer.Items) != 3 {
		t.Fatalf("expected three declared items, got %#v", transfer.Items)
	}

	attachmentIDs := []string{}
	for _, item := range []struct{ id, name string }{{"itm-1", "report.txt"}, {"itm-2", "report.txt"}, {"itm-3", "notes.md"}} {
		uploaded := uploadTransferItem(t, client, transfer.ID, item.id, item.name, []byte("body-"+item.id))
		if err := service.RunAttachmentScan(staticHTTPScanner{result: app.ScanResult{Status: "clean"}}, uploaded.Attachment.ID); err != nil {
			t.Fatalf("run clean scan: %v", err)
		}
		attachmentIDs = append(attachmentIDs, uploaded.Attachment.ID)
	}

	published := publishTransfer(t, client, transfer.ID)
	if published.Share == nil || len(published.Items) != 3 {
		t.Fatalf("expected a published three file send, got %#v", published)
	}

	// Duplicate file names stay separate entries; the send is one record.
	records := client.json(http.MethodGet, "/api/v1/pastes", "")
	assertStatus(t, records, http.StatusOK)
	var recordsBody struct {
		Pastes []app.PasteView `json:"pastes"`
	}
	decodeResponse(t, records, &recordsBody)
	if len(recordsBody.Pastes) != 1 {
		t.Fatalf("expected one record for one send, got %#v", recordsBody.Pastes)
	}
	if got := len(recordsBody.Pastes[0].Attachments); got != 3 {
		t.Fatalf("expected the record to group three files, got %d", got)
	}

	transfers := client.json(http.MethodGet, "/api/v1/transfers", "")
	assertStatus(t, transfers, http.StatusOK)
	var transfersBody struct {
		Transfers []app.TransferView `json:"transfers"`
	}
	decodeResponse(t, transfers, &transfersBody)
	if len(transfersBody.Transfers) != 1 || len(transfersBody.Transfers[0].Items) != 3 {
		t.Fatalf("expected one transfer record with three items, got %#v", transfersBody.Transfers)
	}

	recipient := newHTTPTestClient(t, handler)
	access := recipient.json(http.MethodPost, "/api/v1/shares/"+published.Share.Token+"/access", `{"password":""}`)
	assertStatus(t, access, http.StatusOK)
	var accessBody struct {
		Paste app.PasteView `json:"paste"`
	}
	decodeResponse(t, access, &accessBody)
	if len(accessBody.Paste.Attachments) != 3 {
		t.Fatalf("expected three files on the shared record, got %#v", accessBody.Paste.Attachments)
	}
	seen := map[string]int{}
	for _, attachment := range accessBody.Paste.Attachments {
		seen[attachment.FileName]++
	}
	if seen["report.txt"] != 2 || seen["notes.md"] != 1 {
		t.Fatalf("expected both duplicate names to survive, got %#v", seen)
	}
	claimTransferShare(t, recipient, published.Share.Token, "", "claim-multifile-duplicates")
	for _, id := range attachmentIDs {
		download := downloadSharedAttachment(t, recipient, published.Share.Token, id)
		assertStatus(t, download, http.StatusOK)
	}
}

// TestTransferItemCountLimitRejectsOversizedSends covers the queue's count
// guard: a send that declares more files than the plan allows fails before any
// upload starts.
func TestTransferItemCountLimitRejectsOversizedSends(t *testing.T) {
	_, handler, _ := newMultiFileTransferServer(t, guestMultiFileConfig())
	client := newHTTPTestClient(t, handler)

	items := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		items = append(items, fmt.Sprintf(`{"itemId":"itm-%d","fileName":"f.txt"}`, i+1))
	}
	res := client.json(http.MethodPost, "/api/v1/guest/transfers",
		`{"guestToken":"guest-count-token","expiresInSeconds":3600,"items":[`+strings.Join(items, ",")+`]}`)
	assertStatus(t, res, http.StatusForbidden)
	if code := decodeErrorCode(t, res); code != "attachment_limit" {
		t.Fatalf("expected attachment_limit for too many files, got %q", code)
	}
}
