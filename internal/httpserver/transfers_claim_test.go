package httpserver

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

type transferClaimResponse struct {
	Claim      app.TransferClaimView `json:"claim"`
	ClaimToken string                `json:"claimToken"`
	Text       string                `json:"text"`
}

type transferAccessResponse struct {
	Paste    app.PasteView           `json:"paste"`
	Share    app.ShareView           `json:"share"`
	Transfer *app.TransferAccessView `json:"transfer"`
}

// claimRequest is the raw claim call, so tests can assert the status of a
// refused claim (wrong password, spent quota, ended session).
func claimRequest(client *httpTestClient, token string, password string, operationID string) *httptest.ResponseRecorder {
	body, err := json.Marshal(map[string]string{"operationId": operationID, "password": password})
	if err != nil {
		panic(err)
	}
	return client.json(http.MethodPost, "/api/v1/shares/"+token+"/claims", string(body))
}

// claimTransferShare spends one claim slot through the share-scoped claim route
// and returns the claim the recipient then downloads with. The credential comes
// back as a cookie on the same client, exactly like a browser.
func claimTransferShare(t *testing.T, client *httpTestClient, token string, password string, operationID string) transferClaimResponse {
	t.Helper()
	res := claimRequest(client, token, password, operationID)
	assertStatus(t, res, http.StatusOK)
	var decoded transferClaimResponse
	decodeResponse(t, res, &decoded)
	if decoded.Claim.ID == "" || decoded.ClaimToken == "" {
		t.Fatalf("expected a claim with a credential, got %#v", decoded)
	}
	return decoded
}

// openAndClaimShare opens the share page and claims it, which is what the
// receiver does before a download.
func openAndClaimShare(t *testing.T, client *httpTestClient, token string, password string, operationID string) transferClaimResponse {
	t.Helper()
	assertStatus(t, openShareThroughLink(client, token, password), http.StatusOK)
	return claimTransferShare(t, client, token, password, operationID)
}

// openTransferShare opens a share and returns the page body, including the
// claim state a transfer-backed share reports.
func openTransferShare(t *testing.T, client *httpTestClient, token string, password string) transferAccessResponse {
	t.Helper()
	res := openShareThroughLink(client, token, password)
	assertStatus(t, res, http.StatusOK)
	var decoded transferAccessResponse
	decodeResponse(t, res, &decoded)
	return decoded
}

func downloadSharedAttachment(t *testing.T, client *httpTestClient, token string, attachmentID string) *httptest.ResponseRecorder {
	t.Helper()
	return client.json(http.MethodGet, "/api/v1/shares/"+token+"/attachments/"+attachmentID+"/download", "")
}

// publishFileTransferWithQuota publishes a one-file transfer with an explicit
// claim quota and share settings, and scans the file clean so it is
// downloadable. It is how the claim tests choose how many slots to grant.
func publishFileTransferWithQuota(t *testing.T, service *app.Service, owner *httpTestClient, key string, quota int, extraJSON string) app.TransferView {
	t.Helper()
	transfer := createTransfer(t, owner, fmt.Sprintf(
		`{"idempotencyKey":%q,"expiresInSeconds":3600,"claimQuota":%d,%s"items":[{"itemId":"itm-1","fileName":"one.txt","contentType":"text/plain","size":3}]}`,
		key, quota, extraJSON,
	))
	uploaded := uploadTransferItem(t, owner, transfer.ID, "itm-1", "one.txt", []byte("one"))
	scanTransferItemClean(t, service, uploaded.Attachment.ID)
	return publishTransfer(t, owner, transfer.ID)
}

func publishTwoFileTransfer(t *testing.T, service *app.Service, owner *httpTestClient, key string, quota int) app.TransferView {
	t.Helper()
	transfer := createTransfer(t, owner, fmt.Sprintf(
		`{"idempotencyKey":%q,"expiresInSeconds":3600,"claimQuota":%d,"items":[{"itemId":"itm-1","fileName":"one.txt","contentType":"text/plain","size":3},{"itemId":"itm-2","fileName":"two.txt","contentType":"text/plain","size":3}]}`,
		key, quota,
	))
	first := uploadTransferItem(t, owner, transfer.ID, "itm-1", "one.txt", []byte("one"))
	second := uploadTransferItem(t, owner, transfer.ID, "itm-2", "two.txt", []byte("two"))
	scanTransferItemClean(t, service, first.Attachment.ID)
	scanTransferItemClean(t, service, second.Attachment.ID)
	return publishTransfer(t, owner, transfer.ID)
}

func scanTransferItemClean(t *testing.T, service *app.Service, attachmentID string) {
	t.Helper()
	if err := service.RunAttachmentScan(staticHTTPScanner{result: app.ScanResult{Status: "clean"}}, attachmentID); err != nil {
		t.Fatalf("run clean scan: %v", err)
	}
}

// newClaimClockTestServer is newPickupClockTestServer plus the service, because
// the claim tests both move the clock and scan uploaded files clean.
func newClaimClockTestServer(t *testing.T) (*app.Service, http.Handler, *pickupTestClock) {
	t.Helper()
	t.Setenv("PASTEBOX_DEV_AUTH_TOKENS", "true")
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	clock := &pickupTestClock{now: time.Now().UTC()}
	service := app.NewForTest(clock.Now)
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)
	return service, handler, clock
}

// TestTransferClaimQuotaAndSessionCoverTheBatch is the issue #10 acceptance
// test: one claim spends one slot, the session it opens covers every file of
// the batch, and neither opening the page nor a retried claim spends another.
func TestTransferClaimQuotaAndSessionCoverTheBatch(t *testing.T) {
	service, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "claim-quota@example.com", "Owner")

	published := publishTwoFileTransfer(t, service, owner, "claim-quota", 2)
	token := published.Share.Token
	items := published.Items

	// Preview and status reads are free: they report the claim count without
	// spending a slot, and the default quota is visible before claiming.
	first := newHTTPTestClient(t, handler)
	opened := openTransferShare(t, first, token, "")
	if opened.Transfer == nil || opened.Transfer.Claimed {
		t.Fatalf("expected an unclaimed transfer state, got %#v", opened.Transfer)
	}
	if opened.Transfer.ClaimQuota != 2 || opened.Transfer.ClaimedCount != 0 || opened.Transfer.ClaimsRemaining != 2 {
		t.Fatalf("expected a full quota on preview, got %#v", opened.Transfer)
	}
	if again := openTransferShare(t, first, token, ""); again.Transfer.ClaimedCount != 0 {
		t.Fatalf("expected repeated previews to spend no slot, got %#v", again.Transfer)
	}

	// A download without a claim is refused: the 15-minute page-access grant is
	// not a claim session.
	unclaimed := downloadSharedAttachment(t, first, token, items[0].AttachmentID)
	assertStatus(t, unclaimed, http.StatusForbidden)
	if code := decodeErrorCode(t, unclaimed); code != "claim_required" {
		t.Fatalf("expected claim_required before claiming, got %q", code)
	}

	claim := claimTransferShare(t, first, token, "", "op-first")
	if claim.Claim.Kind != app.TransferClaimKindFile || claim.Claim.Status != app.TransferClaimStatusActive {
		t.Fatalf("expected a live file claim, got %#v", claim)
	}
	if claimed := openTransferShare(t, first, token, ""); claimed.Transfer.ClaimedCount != 1 || !claimed.Transfer.Claimed {
		t.Fatalf("expected one spent slot and a live claim, got %#v", claimed.Transfer)
	}

	// Every file of the batch, downloaded twice, uses the one session.
	for _, item := range items {
		assertStatus(t, downloadSharedAttachment(t, first, token, item.AttachmentID), http.StatusOK)
		assertStatus(t, downloadSharedAttachment(t, first, token, item.AttachmentID), http.StatusOK)
	}

	// Retrying the claim with the same operation id returns the same session
	// instead of spending a second slot.
	replay := claimTransferShare(t, first, token, "", "op-first")
	if replay.Claim.ID != claim.Claim.ID {
		t.Fatalf("expected the claim retry to reuse %s, got %s", claim.Claim.ID, replay.Claim.ID)
	}
	if after := openTransferShare(t, first, token, ""); after.Transfer.ClaimedCount != 1 {
		t.Fatalf("expected the claim retry to spend no slot, got %#v", after.Transfer)
	}

	// A second recipient spends the last slot, and the sender sees the count.
	second := newHTTPTestClient(t, handler)
	secondClaim := claimTransferShare(t, second, token, "", "op-second")
	if secondClaim.Claim.ID == claim.Claim.ID {
		t.Fatalf("expected a separate claim for the second recipient")
	}
	sender := owner.json(http.MethodGet, "/api/v1/transfers/"+published.ID, "")
	assertStatus(t, sender, http.StatusOK)
	var senderBody transferResponse
	decodeResponse(t, sender, &senderBody)
	if senderBody.Transfer.ClaimQuota != 2 || senderBody.Transfer.ClaimedCount != 2 {
		t.Fatalf("expected the sender to see both spent slots, got %#v", senderBody.Transfer)
	}

	// The quota is exhausted for new claims...
	third := newHTTPTestClient(t, handler)
	refused := claimRequest(third, token, "", "op-third")
	assertStatus(t, refused, http.StatusGone)
	if code := decodeErrorCode(t, refused); code != "claim_quota_exhausted" {
		t.Fatalf("expected claim_quota_exhausted, got %q", code)
	}
	// ...but the sessions that already paid keep working.
	assertStatus(t, downloadSharedAttachment(t, first, token, items[0].AttachmentID), http.StatusOK)
	assertStatus(t, downloadSharedAttachment(t, second, token, items[1].AttachmentID), http.StatusOK)

	// Completing a session ends it early without refunding the slot, and only
	// that session.
	completed := first.json(http.MethodPost, "/api/v1/shares/"+token+"/claims/"+claim.Claim.ID+"/complete", "")
	assertStatus(t, completed, http.StatusOK)
	ended := downloadSharedAttachment(t, first, token, items[0].AttachmentID)
	assertStatus(t, ended, http.StatusGone)
	if code := decodeErrorCode(t, ended); code != "claim_ended" {
		t.Fatalf("expected claim_ended after completing, got %q", code)
	}
	// Completing twice is a no-op, and the other recipient is unaffected.
	assertStatus(t, first.json(http.MethodPost, "/api/v1/shares/"+token+"/claims/"+claim.Claim.ID+"/complete", ""), http.StatusOK)
	assertStatus(t, downloadSharedAttachment(t, second, token, items[1].AttachmentID), http.StatusOK)
}

// TestTransferClaimSessionExpiresAndNeverOutlivesTheShare pins the session
// window: a file session lasts at most 30 minutes, stops at the share expiry,
// and cannot be revived once it passed.
func TestTransferClaimSessionExpiresAndNeverOutlivesTheShare(t *testing.T) {
	service, handler, clock := newClaimClockTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "claim-expiry@example.com", "Owner")

	published := publishFileTransferWithQuota(t, service, owner, "claim-expiry", 1, "")
	token := published.Share.Token
	recipient := newHTTPTestClient(t, handler)
	claim := claimTransferShare(t, recipient, token, "", "op-expiry")
	if want := clock.Now().Add(app.TransferClaimSessionTTL); !claim.Claim.ExpiresAt.Equal(want) {
		t.Fatalf("expected a %s session, got %s", app.TransferClaimSessionTTL, claim.Claim.ExpiresAt.Sub(clock.Now()))
	}
	if published.ExpiresAt.Before(claim.Claim.ExpiresAt) {
		t.Fatalf("expected the session to stay inside the share expiry %s, got %s", published.ExpiresAt, claim.Claim.ExpiresAt)
	}

	clock.Advance(app.TransferClaimSessionTTL - time.Minute)
	assertStatus(t, downloadSharedAttachment(t, recipient, token, published.Items[0].AttachmentID), http.StatusOK)

	clock.Advance(2 * time.Minute)
	expired := downloadSharedAttachment(t, recipient, token, published.Items[0].AttachmentID)
	assertStatus(t, expired, http.StatusGone)
	if code := decodeErrorCode(t, expired); code != "claim_ended" {
		t.Fatalf("expected claim_ended after the session window, got %q", code)
	}
	// The window is bounded: replaying the claim cannot revive the session.
	assertStatus(t, claimRequest(recipient, token, "", "op-expiry"), http.StatusGone)

	// A share that expires before the session window bounds the session, so a
	// claim can never outlive the content it points at.
	shortTransfer := createTransfer(t, owner, `{"idempotencyKey":"claim-expiry-short","expiresInSeconds":600,"claimQuota":1,"items":[{"itemId":"itm-1","fileName":"short.txt","contentType":"text/plain","size":3}]}`)
	shortUpload := uploadTransferItem(t, owner, shortTransfer.ID, "itm-1", "short.txt", []byte("one"))
	scanTransferItemClean(t, service, shortUpload.Attachment.ID)
	short := publishTransfer(t, owner, shortTransfer.ID)
	shortRecipient := newHTTPTestClient(t, handler)
	shortClaim := claimTransferShare(t, shortRecipient, short.Share.Token, "", "op-short")
	if !shortClaim.Claim.ExpiresAt.Equal(short.Share.ExpiresAt) {
		t.Fatalf("expected the session to stop at the share expiry %s, got %s", short.Share.ExpiresAt, shortClaim.Claim.ExpiresAt)
	}
}

// TestTransferTextClaimIsBoundedAndReplayable covers the text recovery rule: a
// text claim hands out the body once and can be replayed by the same operation
// for a short window, without leaving a permanently readable entry or spending
// a second slot.
func TestTransferTextClaimIsBoundedAndReplayable(t *testing.T) {
	_, handler, clock := newClaimClockTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "text-claim@example.com", "Owner")

	transfer := createTransfer(t, owner, `{"idempotencyKey":"text-claim","expiresInSeconds":3600,"text":"read me once","items":[]}`)
	published := publishTransfer(t, owner, transfer.ID)
	token := published.Share.Token

	recipient := newHTTPTestClient(t, handler)
	first := claimTransferShare(t, recipient, token, "", "op-text")
	if first.Text != "read me once" || first.Claim.Kind != app.TransferClaimKindText {
		t.Fatalf("expected the claim to hand out the text, got %#v", first)
	}
	if want := clock.Now().Add(app.TransferClaimTextWindow); !first.Claim.ExpiresAt.Equal(want) {
		t.Fatalf("expected a %s text window, got %s", app.TransferClaimTextWindow, first.Claim.ExpiresAt.Sub(clock.Now()))
	}
	replay := claimTransferShare(t, recipient, token, "", "op-text")
	if replay.Claim.ID != first.Claim.ID || replay.Text != "read me once" {
		t.Fatalf("expected the text retry to replay the claim, got %#v", replay)
	}
	if after := openTransferShare(t, recipient, token, ""); after.Transfer.ClaimedCount != 1 {
		t.Fatalf("expected one spent slot after the replay, got %#v", after.Transfer)
	}

	// Past the window the claim is gone and cannot be replayed...
	clock.Advance(app.TransferClaimTextWindow + time.Second)
	assertStatus(t, claimRequest(recipient, token, "", "op-text"), http.StatusGone)
	// ...and the slot it spent is not handed back.
	refused := claimRequest(recipient, token, "", "op-text-2")
	assertStatus(t, refused, http.StatusGone)
	if code := decodeErrorCode(t, refused); code != "claim_quota_exhausted" {
		t.Fatalf("expected claim_quota_exhausted, got %q", code)
	}
	if state := openTransferShare(t, recipient, token, ""); state.Transfer.Claimed || state.Paste.Text != "" {
		t.Fatalf("expected the text to stay unreadable after the window, got %#v", state)
	}
}

// TestTransferClaimEnforcesShareCredentialsAndIsolatesSessions keeps the claim
// on the same authorization path as opening the page, and keeps a claim scoped
// to the share it was claimed from.
func TestTransferClaimEnforcesShareCredentialsAndIsolatesSessions(t *testing.T) {
	service, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "claim-auth@example.com", "Owner")

	protected := publishFileTransferWithQuota(t, service, owner, "claim-auth-pw", 1, `"password":"hunter2",`)
	guest := newHTTPTestClient(t, handler)
	assertStatus(t, claimRequest(guest, protected.Share.Token, "", "op-nopw"), http.StatusUnauthorized)
	assertStatus(t, claimRequest(guest, protected.Share.Token, "wrong", "op-wrongpw"), http.StatusUnauthorized)
	// A refused claim spends nothing.
	if state := openTransferShare(t, guest, protected.Share.Token, "hunter2"); state.Transfer.ClaimedCount != 0 {
		t.Fatalf("expected refused claims to spend no slot, got %#v", state.Transfer)
	}

	// The claim needs a client operation id so a retry can be recognized.
	badOperation := guest.json(http.MethodPost, "/api/v1/shares/"+protected.Share.Token+"/claims", `{"password":"hunter2"}`)
	assertStatus(t, badOperation, http.StatusBadRequest)
	if code := decodeErrorCode(t, badOperation); code != "invalid_claim_operation" {
		t.Fatalf("expected invalid_claim_operation, got %q", code)
	}

	// A login-required share refuses an anonymous claim and accepts a signed-in
	// one, exactly like opening the page.
	login := publishFileTransferWithQuota(t, service, owner, "claim-auth-login", 1, `"loginRequired":true,`)
	anon := newHTTPTestClient(t, handler)
	assertStatus(t, claimRequest(anon, login.Share.Token, "", "op-anon"), http.StatusUnauthorized)
	viewer := newHTTPTestClient(t, handler)
	registerHTTPUser(t, viewer, "claim-auth-viewer@example.com", "Viewer")
	claimTransferShare(t, viewer, login.Share.Token, "", "op-viewer")

	// A session is bound to the share it was claimed from: the same browser
	// holding one claim cannot download another share with it.
	other := publishFileTransferWithQuota(t, service, owner, "claim-auth-other", 1, "")
	openTransferShare(t, viewer, other.Share.Token, "")
	crossed := downloadSharedAttachment(t, viewer, other.Share.Token, other.Items[0].AttachmentID)
	assertStatus(t, crossed, http.StatusForbidden)
	if code := decodeErrorCode(t, crossed); code != "claim_required" {
		t.Fatalf("expected a claim from another share to be refused, got %q", code)
	}
}

// TestTransferClaimQuotaDefaultsAndBounds keeps the send setting honest: the
// default is one anonymous claim, and an out-of-range quota is refused instead
// of being silently clamped.
func TestTransferClaimQuotaDefaultsAndBounds(t *testing.T) {
	service, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "claim-bounds@example.com", "Owner")

	defaulted := publishFileTransferWithQuota(t, service, owner, "claim-default", 0, "")
	if defaulted.ClaimQuota != 1 {
		t.Fatalf("expected one claim by default, got %d", defaulted.ClaimQuota)
	}
	for _, body := range []string{
		`{"idempotencyKey":"quota-negative","expiresInSeconds":3600,"claimQuota":-1,"items":[{"itemId":"itm-1","fileName":"a.txt","contentType":"text/plain","size":1}]}`,
		fmt.Sprintf(`{"idempotencyKey":"quota-over","expiresInSeconds":3600,"claimQuota":%d,"items":[{"itemId":"itm-1","fileName":"a.txt","contentType":"text/plain","size":1}]}`, app.MaxTransferClaimQuota+1),
	} {
		res := owner.json(http.MethodPost, "/api/v1/transfers", body)
		assertStatus(t, res, http.StatusBadRequest)
		if code := decodeErrorCode(t, res); code != "invalid_claim_quota" {
			t.Fatalf("expected invalid_claim_quota, got %q", code)
		}
	}
}

// TestGuestTransferClaimUsesTheSameQuotaContract keeps guest sends on the same
// claim contract as account sends.
func TestGuestTransferClaimUsesTheSameQuotaContract(t *testing.T) {
	_, handler := newTransferTestServer(t)
	guest := newHTTPTestClient(t, handler)

	const guestToken = "guest-claim-token"
	created := guest.json(http.MethodPost, "/api/v1/guest/transfers", `{"guestToken":"`+guestToken+`","idempotencyKey":"guest-claim","expiresInSeconds":3600,"claimQuota":2,"items":[{"itemId":"itm-1","fileName":"g.txt","contentType":"text/plain","size":5}]}`)
	assertStatus(t, created, http.StatusCreated)
	var createdBody struct {
		Transfer app.TransferView `json:"transfer"`
	}
	decodeResponse(t, created, &createdBody)
	if createdBody.Transfer.ClaimQuota != 2 {
		t.Fatalf("expected the guest send to keep its quota, got %d", createdBody.Transfer.ClaimQuota)
	}
	uploadGuestTransferItem(t, guest, guestToken, createdBody.Transfer.ID, "itm-1", "g.txt", []byte("guest"))
	published := guest.json(http.MethodPost, "/api/v1/guest/transfers/"+createdBody.Transfer.ID+"/publish", `{"guestToken":"`+guestToken+`"}`)
	assertStatus(t, published, http.StatusOK)
	var publishedBody transferResponse
	decodeResponse(t, published, &publishedBody)
	if publishedBody.Transfer.ClaimQuota != 2 {
		t.Fatalf("expected the guest quota to survive publish, got %d", publishedBody.Transfer.ClaimQuota)
	}

	token := publishedBody.Transfer.Share.Token
	for i := 0; i < 2; i++ {
		recipient := newHTTPTestClient(t, handler)
		claim := claimTransferShare(t, recipient, token, "", fmt.Sprintf("guest-claim-%d", i))
		if claim.Claim.Kind != app.TransferClaimKindFile {
			t.Fatalf("expected a file claim, got %#v", claim)
		}
	}
	third := newHTTPTestClient(t, handler)
	assertStatus(t, claimRequest(third, token, "", "guest-claim-2"), http.StatusGone)
}

// TestTransferClaimQuotaIsAtomicUnderConcurrentClaims drives many simultaneous
// claims through the HTTP boundary. The in-memory service and the PostgreSQL
// store both have to grant exactly the quota, never more.
func TestTransferClaimQuotaIsAtomicUnderConcurrentClaims(t *testing.T) {
	service, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "claim-race@example.com", "Owner")

	const quota = 3
	const attempts = 12
	published := publishFileTransferWithQuota(t, service, owner, "claim-race", quota, "")
	token := published.Share.Token

	csrfTokens := make([]string, attempts)
	csrfCookies := make([]string, attempts)
	for i := range csrfTokens {
		csrfTokens[i], csrfCookies[i] = csrfCredentials(t, handler)
	}

	statuses := make([]int, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/shares/"+token+"/claims", strings.NewReader(fmt.Sprintf(`{"operationId":"race-%d"}`, i)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")
			req.Header.Set(csrfHeaderName, csrfTokens[i])
			req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrfCookies[i]})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			statuses[i] = rec.Code
		}(i)
	}
	wg.Wait()

	granted := 0
	for _, status := range statuses {
		switch status {
		case http.StatusOK:
			granted++
		case http.StatusGone:
		default:
			t.Fatalf("unexpected claim status %d in %v", status, statuses)
		}
	}
	if granted != quota {
		t.Fatalf("expected exactly %d granted claims, got %d (%v)", quota, granted, statuses)
	}
}

// csrfCredentials fetches a CSRF token and its cookie straight from the handler,
// so concurrent tests do not share one client's mutable cookie jar.
func csrfCredentials(t *testing.T, handler http.Handler) (string, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/csrf", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var body struct {
		CSRFToken string `json:"csrfToken"`
	}
	decodeResponse(t, rec, &body)
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			return body.CSRFToken, cookie.Value
		}
	}
	t.Fatalf("expected a csrf cookie in the response")
	return "", ""
}

// TestTransferMixedSendWithholdsItsTextUntilClaimed guards the mixed send: a
// transfer may carry text and files at once, and its text must not be readable
// without spending a claim just because the batch also declares files.
func TestTransferMixedSendWithholdsItsTextUntilClaimed(t *testing.T) {
	_, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "claim-mixed@example.com", "Owner")

	transfer := createTransfer(t, owner, `{"idempotencyKey":"claim-mixed","expiresInSeconds":3600,"text":"secret caption","items":[{"itemId":"itm-1","fileName":"one.txt","contentType":"text/plain","size":3}]}`)
	uploadTransferItem(t, owner, transfer.ID, "itm-1", "one.txt", []byte("one"))
	published := publishTransfer(t, owner, transfer.ID)
	token := published.Share.Token

	recipient := newHTTPTestClient(t, handler)
	opened := openTransferShare(t, recipient, token, "")
	if opened.Paste.Text != "" || opened.Paste.TextPreview != "" {
		t.Fatalf("expected a mixed send to withhold its text before claiming, got %#v", opened.Paste)
	}
	if opened.Transfer == nil || opened.Transfer.Kind != app.TransferClaimKindFile || opened.Transfer.Claimed {
		t.Fatalf("expected an unclaimed file claim state, got %#v", opened.Transfer)
	}

	claimTransferShare(t, recipient, token, "", "claim-mixed")
	claimed := openTransferShare(t, recipient, token, "")
	if claimed.Paste.Text != "secret caption" {
		t.Fatalf("expected the claim to reveal the mixed send text, got %q", claimed.Paste.Text)
	}
}

// TestPlanCatalogPublishesTheClaimBound keeps the send form honest: the client
// derives the claim bound from the published catalog instead of copying it.
func TestPlanCatalogPublishesTheClaimBound(t *testing.T) {
	_, handler := newTransferTestServer(t)
	client := newHTTPTestClient(t, handler)

	res := client.json(http.MethodGet, "/api/v1/plans", "")
	assertStatus(t, res, http.StatusOK)
	var body struct {
		Transfers struct {
			MaxClaimQuota int `json:"maxClaimQuota"`
		} `json:"transfers"`
	}
	decodeResponse(t, res, &body)
	if body.Transfers.MaxClaimQuota != app.MaxTransferClaimQuota {
		t.Fatalf("expected the catalog to publish %d, got %d", app.MaxTransferClaimQuota, body.Transfers.MaxClaimQuota)
	}
}
