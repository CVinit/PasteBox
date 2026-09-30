package httpserver

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"pastebox/internal/app"
)

// pngItemFixture is a real PNG so the upload path sniffs image/png the way a
// browser-encoded image would.
func pngItemFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 15, G: 118, B: 110, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

// TestTransferTextSendPublishesLinkAndPickupCode covers issue #9's text send:
// text alone is a complete transfer, it publishes without an upload step, and
// the link and the 6-character code read back the same text.
func TestTransferTextSendPublishesLinkAndPickupCode(t *testing.T) {
	_, handler := newTransferTestServer(t)
	sender := newHTTPTestClient(t, handler)
	registerHTTPUser(t, sender, "text-sender@example.com", "Sender")

	const body = `{"idempotencyKey":"text-1","expiresInSeconds":3600,"title":"Release notes","text":"deploy at 09:00","items":[]}`
	transfer := createTransfer(t, sender, body)
	if transfer.Status != app.TransferStatusDraft || transfer.PasteID == "" {
		t.Fatalf("unexpected text draft: %#v", transfer)
	}
	if len(transfer.Items) != 0 || transfer.Share != nil {
		t.Fatalf("expected a text draft with no files and no share, got %#v", transfer)
	}

	// A retried create with the same key reuses the same text send.
	retried := createTransfer(t, sender, body)
	if retried.ID != transfer.ID {
		t.Fatalf("expected the text send retry to reuse %s, got %s", transfer.ID, retried.ID)
	}

	// Text has nothing to upload, so it publishes without a file manifest.
	published := publishTransfer(t, sender, transfer.ID)
	if published.Status != app.TransferStatusPublished || published.Share == nil || published.PickupCode == "" {
		t.Fatalf("expected a published text send with credentials, got %#v", published)
	}

	// The record keeps the body and the title the sender typed.
	record := sender.json(http.MethodGet, "/api/v1/pastes/"+transfer.PasteID, "")
	assertStatus(t, record, http.StatusOK)
	var recordBody app.PasteView
	decodeResponse(t, record, &recordBody)
	if recordBody.Text != "deploy at 09:00" || recordBody.Title != "Release notes" || len(recordBody.Attachments) != 0 {
		t.Fatalf("unexpected text record: %#v", recordBody)
	}

	// Both entry points reach the same text send, and neither hands out the
	// body: opening the page reports the claim state, and the claim is the only
	// place the text is read.
	recipient := newHTTPTestClient(t, handler)
	link := openShareThroughLink(recipient, published.Share.Token, "")
	assertStatus(t, link, http.StatusOK)
	var linkBody transferAccessResponse
	decodeResponse(t, link, &linkBody)
	if linkBody.Paste.Text != "" || linkBody.Paste.ID != transfer.PasteID {
		t.Fatalf("expected the link to withhold the text until it is claimed, got %#v", linkBody.Paste)
	}
	if linkBody.Transfer == nil || linkBody.Transfer.Kind != app.TransferClaimKindText || linkBody.Transfer.Claimed {
		t.Fatalf("expected an unclaimed text transfer state, got %#v", linkBody.Transfer)
	}
	if linkBody.Transfer.ClaimQuota != 1 || linkBody.Transfer.ClaimsRemaining != 1 {
		t.Fatalf("expected one anonymous claim by default, got %#v", linkBody.Transfer)
	}

	code := openShareThroughCode(t, recipient, published.PickupCode, "")
	assertStatus(t, code, http.StatusOK)
	var codeBody struct {
		Paste app.PasteView `json:"paste"`
		Share app.ShareView `json:"share"`
	}
	decodeResponse(t, code, &codeBody)
	if codeBody.Paste.ID != transfer.PasteID || codeBody.Share.Token != published.Share.Token {
		t.Fatalf("expected the pickup code to reach the same text share, got %#v", codeBody)
	}

	claim := claimTransferShare(t, recipient, published.Share.Token, "", "claim-text-read")
	if claim.Claim.Kind != app.TransferClaimKindText || claim.Text != "deploy at 09:00" {
		t.Fatalf("expected the claim to hand out the sent text, got %#v", claim)
	}
	// Re-opening the page with the claim credential keeps reading the text the
	// recipient already paid for instead of asking for another claim.
	reopened := openTransferShare(t, recipient, published.Share.Token, "")
	if !reopened.Transfer.Claimed || reopened.Paste.Text != "deploy at 09:00" {
		t.Fatalf("expected a live claim to keep the text readable, got %#v", reopened)
	}
}

// TestTransferSendRefusesEmptyAndOverLimitContent keeps the content rules of a
// send explicit: a send needs text or files, and text is plan-limited exactly
// like a record body.
func TestTransferSendRefusesEmptyAndOverLimitContent(t *testing.T) {
	_, handler := newTransferTestServer(t)
	client := newHTTPTestClient(t, handler)
	registerHTTPUser(t, client, "text-limits@example.com", "Sender")

	empty := client.json(http.MethodPost, "/api/v1/transfers", `{"expiresInSeconds":3600,"items":[]}`)
	assertStatus(t, empty, http.StatusBadRequest)
	if code := decodeErrorCode(t, empty); code != "transfer_content_required" {
		t.Fatalf("expected transfer_content_required for a send with no content, got %q", code)
	}

	// Whitespace is not content, so it is refused at the same point instead of
	// creating a draft that could never publish.
	blank := client.json(http.MethodPost, "/api/v1/transfers", `{"expiresInSeconds":3600,"text":"   \n\t","items":[]}`)
	assertStatus(t, blank, http.StatusBadRequest)
	if code := decodeErrorCode(t, blank); code != "transfer_content_required" {
		t.Fatalf("expected transfer_content_required for a whitespace-only send, got %q", code)
	}

	overLimit, err := json.Marshal(map[string]any{
		"expiresInSeconds": 3600,
		"text":             strings.Repeat("x", 256*1024+1),
		"items":            []app.TransferItemInput{},
	})
	if err != nil {
		t.Fatalf("encode oversized text body: %v", err)
	}
	tooLarge := client.json(http.MethodPost, "/api/v1/transfers", string(overLimit))
	assertStatus(t, tooLarge, http.StatusRequestEntityTooLarge)
	if code := decodeErrorCode(t, tooLarge); code != "text_too_large" {
		t.Fatalf("expected text_too_large, got %q", code)
	}

	// Tags stay plan-gated on a send, so a send cannot be used to write tags the
	// plan does not allow.
	tagged := client.json(http.MethodPost, "/api/v1/transfers", `{"expiresInSeconds":3600,"text":"tagged","tags":["work"],"items":[]}`)
	assertStatus(t, tagged, http.StatusForbidden)
	if code := decodeErrorCode(t, tagged); code != "tag_limit" {
		t.Fatalf("expected tag_limit, got %q", code)
	}
}

// TestTransferTextSendKeepsShareProtections checks that a text send is not a
// softer path: the password that protects a file send protects its text too,
// through both the link and the code.
func TestTransferTextSendKeepsShareProtections(t *testing.T) {
	_, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "text-protect@example.com", "Owner")

	transfer := createTransfer(t, owner, `{"idempotencyKey":"text-protect","expiresInSeconds":3600,"password":"hunter2","text":"secret text","items":[]}`)
	published := publishTransfer(t, owner, transfer.ID)

	recipient := newHTTPTestClient(t, handler)
	assertStatus(t, openShareThroughLink(recipient, published.Share.Token, ""), http.StatusUnauthorized)
	assertStatus(t, openShareThroughLink(recipient, published.Share.Token, "wrong"), http.StatusUnauthorized)
	assertStatus(t, openShareThroughCode(t, recipient, published.PickupCode, ""), http.StatusUnauthorized)

	opened := openShareThroughLink(recipient, published.Share.Token, "hunter2")
	assertStatus(t, opened, http.StatusOK)
	var body transferAccessResponse
	decodeResponse(t, opened, &body)
	if body.Paste.Text != "" {
		t.Fatalf("expected the protected text to stay hidden until it is claimed, got %q", body.Paste.Text)
	}
	// The claim is protected by the same password as the page, so neither entry
	// point can be used to read the text without it.
	assertStatus(t, claimRequest(recipient, published.Share.Token, "", "claim-text-nopw"), http.StatusUnauthorized)
	assertStatus(t, claimRequest(recipient, published.Share.Token, "wrong", "claim-text-wrongpw"), http.StatusUnauthorized)
	claim := claimTransferShare(t, recipient, published.Share.Token, "hunter2", "claim-text-protected")
	if claim.Text != "secret text" {
		t.Fatalf("expected the password to reveal the sent text on claim, got %q", claim.Text)
	}
}

// TestTransferImageSendKeepsImageContentType covers the image mode of the send
// area: an image is one declared item whose content type survives publish and
// download, which is what the receiving page reads to render it as an image.
func TestTransferImageSendKeepsImageContentType(t *testing.T) {
	service, handler := newTransferTestServer(t)
	client := newHTTPTestClient(t, handler)
	registerHTTPUser(t, client, "image-sender@example.com", "Sender")

	image := pngItemFixture(t)
	transfer := createTransfer(t, client, `{"idempotencyKey":"image-1","expiresInSeconds":3600,"items":[{"itemId":"itm-1","fileName":"shot.png","contentType":"image/png","size":`+strconv.Itoa(len(image))+`}]}`)
	uploaded := uploadTransferItem(t, client, transfer.ID, "itm-1", "shot.png", image)
	if uploaded.Attachment.ContentType != "image/png" {
		t.Fatalf("expected the uploaded image to keep image/png, got %q", uploaded.Attachment.ContentType)
	}
	if err := service.RunAttachmentScan(staticHTTPScanner{result: app.ScanResult{Status: "clean"}}, uploaded.Attachment.ID); err != nil {
		t.Fatalf("run clean scan: %v", err)
	}
	published := publishTransfer(t, client, transfer.ID)

	recipient := newHTTPTestClient(t, handler)
	access := openShareThroughLink(recipient, published.Share.Token, "")
	assertStatus(t, access, http.StatusOK)
	var accessBody struct {
		Paste app.PasteView `json:"paste"`
	}
	decodeResponse(t, access, &accessBody)
	if len(accessBody.Paste.Attachments) != 1 || accessBody.Paste.Attachments[0].ContentType != "image/png" {
		t.Fatalf("expected the recipient to see one image attachment, got %#v", accessBody.Paste.Attachments)
	}

	claimTransferShare(t, recipient, published.Share.Token, "", "claim-image-download")
	download := downloadSharedAttachment(t, recipient, published.Share.Token, uploaded.Attachment.ID)
	assertStatus(t, download, http.StatusOK)
	if got := download.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected the shared download to serve image/png, got %q", got)
	}
	if !bytes.Equal(download.Body.Bytes(), image) {
		t.Fatalf("expected the shared download to serve the uploaded image bytes")
	}
}

// TestTransferSendLeavesExistingRecordsUntouched is the regression guard for
// "a new send never writes to the record the user is looking at": the send gets
// its own record and its own credentials, and an existing note keeps its text
// and its attachment.
func TestTransferSendLeavesExistingRecordsUntouched(t *testing.T) {
	_, handler := newTransferTestServer(t)
	client := newHTTPTestClient(t, handler)
	registerHTTPUser(t, client, "records@example.com", "Owner")

	created := client.json(http.MethodPost, "/api/v1/pastes", `{"title":"Existing note","text":"keep me","tags":[]}`)
	assertStatus(t, created, http.StatusCreated)
	var note app.PasteView
	decodeResponse(t, created, &note)
	assertStatus(t, client.multipart("/api/v1/pastes/"+note.ID+"/attachments", "file", "old.txt", []byte("old-body")), http.StatusCreated)

	transfer := createTransfer(t, client, `{"idempotencyKey":"records-1","expiresInSeconds":3600,"text":"new send","items":[]}`)
	if transfer.PasteID == note.ID {
		t.Fatalf("expected the send to create its own record, got the existing note %s", note.ID)
	}
	published := publishTransfer(t, client, transfer.ID)

	after := client.json(http.MethodGet, "/api/v1/pastes/"+note.ID, "")
	assertStatus(t, after, http.StatusOK)
	var afterBody app.PasteView
	decodeResponse(t, after, &afterBody)
	if afterBody.Text != "keep me" || afterBody.Title != "Existing note" {
		t.Fatalf("expected the existing record body to stay untouched, got %#v", afterBody)
	}
	if len(afterBody.Attachments) != 1 || afterBody.Attachments[0].FileName != "old.txt" {
		t.Fatalf("expected the existing record to keep exactly its own attachment, got %#v", afterBody.Attachments)
	}

	// The send minted one share for its own record, not for the note.
	shares := client.json(http.MethodGet, "/api/v1/shares", "")
	assertStatus(t, shares, http.StatusOK)
	var sharesBody struct {
		Shares []app.ShareView `json:"shares"`
	}
	decodeResponse(t, shares, &sharesBody)
	if len(sharesBody.Shares) != 1 || sharesBody.Shares[0].PasteID != transfer.PasteID || sharesBody.Shares[0].ID != published.Share.ID {
		t.Fatalf("expected only the send's own share, got %#v", sharesBody.Shares)
	}
}

// TestGuestTransferTextSendPublishesPickupCode keeps the guest workbench's text
// mode on the same transfer contract as the account one, under guest limits.
func TestGuestTransferTextSendPublishesPickupCode(t *testing.T) {
	_, handler := newTransferTestServer(t)
	client := newHTTPTestClient(t, handler)

	const token = "guest-text-token"
	res := client.json(http.MethodPost, "/api/v1/guest/transfers", `{"guestToken":"`+token+`","expiresInSeconds":3600,"text":"guest note","items":[]}`)
	assertStatus(t, res, http.StatusCreated)
	var created struct {
		GuestToken string           `json:"guestToken"`
		Transfer   app.TransferView `json:"transfer"`
	}
	decodeResponse(t, res, &created)
	if created.GuestToken != token || len(created.Transfer.Items) != 0 {
		t.Fatalf("unexpected guest text draft: %#v", created)
	}

	publish := client.json(http.MethodPost, "/api/v1/guest/transfers/"+created.Transfer.ID+"/publish", `{"guestToken":"`+token+`"}`)
	assertStatus(t, publish, http.StatusOK)
	var published struct {
		Transfer app.TransferView `json:"transfer"`
	}
	decodeResponse(t, publish, &published)
	if published.Transfer.Share == nil || published.Transfer.PickupCode == "" {
		t.Fatalf("expected the guest text send to publish credentials, got %#v", published.Transfer)
	}

	recipient := newHTTPTestClient(t, handler)
	opened := openShareThroughCode(t, recipient, published.Transfer.PickupCode, "")
	assertStatus(t, opened, http.StatusOK)
	var openedBody transferAccessResponse
	decodeResponse(t, opened, &openedBody)
	if openedBody.Paste.Text != "" {
		t.Fatalf("expected the guest text send to withhold its body until claimed, got %q", openedBody.Paste.Text)
	}
	claim := claimTransferShare(t, recipient, published.Transfer.Share.Token, "", "claim-guest-text-read")
	if claim.Text != "guest note" {
		t.Fatalf("expected the guest text send to read back on claim, got %q", claim.Text)
	}

	// The guest text limit still applies to a text send.
	overLimit, err := json.Marshal(map[string]any{
		"guestToken":       token,
		"expiresInSeconds": 3600,
		"text":             strings.Repeat("x", 64*1024+1),
		"items":            []app.TransferItemInput{},
	})
	if err != nil {
		t.Fatalf("encode oversized guest text: %v", err)
	}
	tooLarge := client.json(http.MethodPost, "/api/v1/guest/transfers", string(overLimit))
	assertStatus(t, tooLarge, http.StatusRequestEntityTooLarge)
	if code := decodeErrorCode(t, tooLarge); code != "text_too_large" {
		t.Fatalf("expected text_too_large for the guest text send, got %q", code)
	}
}
