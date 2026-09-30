package httpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pastebox/internal/app"
)

// TestBurnAfterReadingIsOptInOverHTTP pins the switch to what the sender asks
// for: it defaults to off, and the send reports it back so the success panel and
// the records list can say what will happen.
func TestBurnAfterReadingIsOptInOverHTTP(t *testing.T) {
	service, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "burn-optin@example.com", "Owner")

	ordinary := publishFileTransferWithQuota(t, service, owner, "burn-off", 1, "")
	if ordinary.BurnAfterReading {
		t.Fatalf("expected an ordinary send to keep the switch off, got %#v", ordinary)
	}
	burning := publishFileTransferWithQuota(t, service, owner, "burn-on", 1, `"burnAfterReading":true,`)
	if !burning.BurnAfterReading {
		t.Fatalf("expected the burn switch to be stored, got %#v", burning)
	}
	// The switch travels with the send, so the records list can show it.
	sender := owner.json(http.MethodGet, "/api/v1/transfers/"+burning.ID, "")
	assertStatus(t, sender, http.StatusOK)
	var body transferResponse
	decodeResponse(t, sender, &body)
	if !body.Transfer.BurnAfterReading || body.Transfer.DestroyedAt != nil {
		t.Fatalf("expected a live burn send, got %#v", body.Transfer)
	}
}

// TestBurnTransferEndsWithItsLastSessionOverHTTP is the multi-recipient
// acceptance test: ending one session leaves the remaining slots alone, and the
// send — link, content and claim quota — ends with the last session.
func TestBurnTransferEndsWithItsLastSessionOverHTTP(t *testing.T) {
	service, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "burn-flow@example.com", "Owner")

	burning := publishBurnTwoFileTransfer(t, service, owner, "burn-flow", 2)
	token := burning.Share.Token
	items := burning.Items

	first := newHTTPTestClient(t, handler)
	firstClaim := openAndClaimShare(t, first, token, "", "op-first")
	second := newHTTPTestClient(t, handler)
	secondClaim := openAndClaimShare(t, second, token, "", "op-second")

	// The first session ends: the second slot was claimed, but its session is
	// still live, so the send and its content stay exactly as they were.
	complete := first.json(http.MethodPost, "/api/v1/shares/"+token+"/claims/"+firstClaim.Claim.ID+"/complete", "")
	assertStatus(t, complete, http.StatusOK)
	sender := owner.json(http.MethodGet, "/api/v1/transfers/"+burning.ID, "")
	assertStatus(t, sender, http.StatusOK)
	var liveBody transferResponse
	decodeResponse(t, sender, &liveBody)
	if liveBody.Transfer.Status != app.TransferStatusPublished || liveBody.Transfer.DestroyedAt != nil {
		t.Fatalf("expected the send to survive the first session, got %#v", liveBody.Transfer)
	}
	// The second session can still download every file of the batch.
	for _, item := range items {
		assertStatus(t, downloadSharedAttachment(t, second, token, item.AttachmentID), http.StatusOK)
	}

	// The last session ends: the send is destroyed, so every entry point is
	// refused with the terminal state and the content is queued for release.
	lastComplete := second.json(http.MethodPost, "/api/v1/shares/"+token+"/claims/"+secondClaim.Claim.ID+"/complete", "")
	assertStatus(t, lastComplete, http.StatusOK)

	sender = owner.json(http.MethodGet, "/api/v1/transfers/"+burning.ID, "")
	assertStatus(t, sender, http.StatusOK)
	var destroyedBody transferResponse
	decodeResponse(t, sender, &destroyedBody)
	destroyed := destroyedBody.Transfer
	if destroyed.Status != app.TransferStatusDestroyed || destroyed.DestroyedAt == nil {
		t.Fatalf("expected a destroyed send, got %#v", destroyed)
	}
	if destroyed.DestroyReason != app.TransferDestroyReasonClaimsEnded {
		t.Fatalf("expected the claims to be the reason, got %q", destroyed.DestroyReason)
	}
	if destroyed.CleanupStatus != "pending_delete" {
		t.Fatalf("expected the content to be marked for deletion, got %q", destroyed.CleanupStatus)
	}
	if destroyed.Share == nil || destroyed.Share.RevokedAt == nil {
		t.Fatalf("expected the share to be revoked, got %#v", destroyed.Share)
	}

	for name, res := range map[string]*httptest.ResponseRecorder{
		"page":     openShareThroughLink(second, token, ""),
		"download": downloadSharedAttachment(t, second, token, items[0].AttachmentID),
		"claim":    claimRequest(second, token, "", "op-third"),
	} {
		assertStatus(t, res, http.StatusGone)
		if code := decodeErrorCode(t, res); code != "transfer_destroyed" {
			t.Fatalf("expected %s to report transfer_destroyed, got %q", name, code)
		}
	}
	// A fresh visitor gets the same answer instead of a broken page.
	stranger := newHTTPTestClient(t, handler)
	fresh := openShareThroughLink(stranger, token, "")
	assertStatus(t, fresh, http.StatusGone)
	if code := decodeErrorCode(t, fresh); code != "transfer_destroyed" {
		t.Fatalf("expected a fresh visitor to see the destroyed state, got %q", code)
	}
	// The pickup code stays deliberately opaque, so a burned send is simply not
	// resolvable any more rather than announcing which code existed.
	pickup := resolvePickupRequest(stranger, burning.PickupCode)
	assertStatus(t, pickup, http.StatusNotFound)
}

// TestBurnTransferExpiryIsReportedAsItsTerminalState covers a lifetime that ran
// out with slots left: the next visitor both triggers and sees the terminal
// state instead of a generic expiry.
func TestBurnTransferExpiryIsReportedAsItsTerminalState(t *testing.T) {
	service, handler, clock := newClaimClockTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "burn-expiry-http@example.com", "Owner")

	transfer := createTransfer(t, owner, `{"idempotencyKey":"burn-expiry","expiresInSeconds":600,"claimQuota":3,"burnAfterReading":true,"items":[{"itemId":"itm-1","fileName":"one.txt","contentType":"text/plain","size":3}]}`)
	uploaded := uploadTransferItem(t, owner, transfer.ID, "itm-1", "one.txt", []byte("one"))
	scanTransferItemClean(t, service, uploaded.Attachment.ID)
	published := publishTransfer(t, owner, transfer.ID)
	if !published.BurnAfterReading {
		t.Fatalf("expected a burn send, got %#v", published)
	}

	recipient := newHTTPTestClient(t, handler)
	openAndClaimShare(t, recipient, published.Share.Token, "", "op-first")
	// A claim never outlives its share, so the session is over the moment the
	// lifetime is.
	clock.Advance(601 * time.Second)
	res := openShareThroughLink(recipient, published.Share.Token, "")
	assertStatus(t, res, http.StatusGone)
	if code := decodeErrorCode(t, res); code != "transfer_destroyed" {
		t.Fatalf("expected the expired burn send to report its terminal state, got %q", code)
	}

	sender := owner.json(http.MethodGet, "/api/v1/transfers/"+published.ID, "")
	assertStatus(t, sender, http.StatusOK)
	var body transferResponse
	decodeResponse(t, sender, &body)
	if body.Transfer.Status != app.TransferStatusDestroyed || body.Transfer.DestroyReason != app.TransferDestroyReasonExpired {
		t.Fatalf("expected an expiry reason, got %#v", body.Transfer)
	}
	// The session that was open cannot read content that is being released.
	download := downloadSharedAttachment(t, recipient, published.Share.Token, published.Items[0].AttachmentID)
	assertStatus(t, download, http.StatusGone)
}

// TestBurnRevocationOnlyInvalidatesTheLinkOverHTTP pins the approved trigger
// list: revoking the share takes the link and every open session away, but it
// does not destroy the content — only the claims ending or the lifetime ending
// does that.
func TestBurnRevocationOnlyInvalidatesTheLinkOverHTTP(t *testing.T) {
	service, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "burn-revoke-http@example.com", "Owner")

	published := publishFileTransferWithQuota(t, service, owner, "burn-revoke", 2, `"burnAfterReading":true,`)
	recipient := newHTTPTestClient(t, handler)
	openAndClaimShare(t, recipient, published.Share.Token, "", "op-first")

	revoke := owner.json(http.MethodDelete, "/api/v1/shares/"+published.Share.ID, "")
	assertStatus(t, revoke, http.StatusOK)

	sender := owner.json(http.MethodGet, "/api/v1/transfers/"+published.ID, "")
	assertStatus(t, sender, http.StatusOK)
	var body transferResponse
	decodeResponse(t, sender, &body)
	if body.Transfer.Status != app.TransferStatusPublished || body.Transfer.DestroyedAt != nil || body.Transfer.CleanupStatus != "active" {
		t.Fatalf("expected a revoked burn send to keep its content, got %#v", body.Transfer)
	}
	// The link, the code and the session that was open all stop working.
	if res := openShareThroughLink(recipient, published.Share.Token, ""); res.Code != http.StatusGone {
		t.Fatalf("expected the revoked link to be refused, got %d", res.Code)
	}
	if res := downloadSharedAttachment(t, recipient, published.Share.Token, published.Items[0].AttachmentID); res.Code != http.StatusGone {
		t.Fatalf("expected the live session to stop with the revocation, got %d", res.Code)
	}
	if res := resolvePickupRequest(recipient, published.PickupCode); res.Code != http.StatusNotFound {
		t.Fatalf("expected the revoked code to stop resolving, got %d", res.Code)
	}
}

// TestOrdinaryTransferSurvivesItsLastClaimOverHTTP is the regression guard for
// the default: a send that never asked to burn keeps its content even after
// every slot was claimed and every session ended.
func TestOrdinaryTransferSurvivesItsLastClaimOverHTTP(t *testing.T) {
	service, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "burn-regression@example.com", "Owner")

	published := publishFileTransferWithQuota(t, service, owner, "no-burn", 1, "")
	recipient := newHTTPTestClient(t, handler)
	claim := openAndClaimShare(t, recipient, published.Share.Token, "", "op-only")
	complete := recipient.json(http.MethodPost, "/api/v1/shares/"+published.Share.Token+"/claims/"+claim.Claim.ID+"/complete", "")
	assertStatus(t, complete, http.StatusOK)

	sender := owner.json(http.MethodGet, "/api/v1/transfers/"+published.ID, "")
	assertStatus(t, sender, http.StatusOK)
	var body transferResponse
	decodeResponse(t, sender, &body)
	if body.Transfer.Status != app.TransferStatusPublished || body.Transfer.DestroyedAt != nil || body.Transfer.CleanupStatus != "active" {
		t.Fatalf("expected an ordinary send to survive its last claim, got %#v", body.Transfer)
	}
	if body.Transfer.Share == nil || body.Transfer.Share.RevokedAt != nil {
		t.Fatalf("expected the ordinary share to stay live, got %#v", body.Transfer.Share)
	}
}

// publishBurnTwoFileTransfer publishes a two-file burning send with an explicit
// claim quota, scanned clean so both files are downloadable.
func publishBurnTwoFileTransfer(t *testing.T, service *app.Service, owner *httpTestClient, key string, quota int) app.TransferView {
	t.Helper()
	transfer := createTransfer(t, owner, fmt.Sprintf(
		`{"idempotencyKey":%q,"expiresInSeconds":3600,"claimQuota":%d,"burnAfterReading":true,"items":[{"itemId":"itm-1","fileName":"one.txt","contentType":"text/plain","size":3},{"itemId":"itm-2","fileName":"two.txt","contentType":"text/plain","size":3}]}`,
		key, quota,
	))
	first := uploadTransferItem(t, owner, transfer.ID, "itm-1", "one.txt", []byte("one"))
	second := uploadTransferItem(t, owner, transfer.ID, "itm-2", "two.txt", []byte("two"))
	scanTransferItemClean(t, service, first.Attachment.ID)
	scanTransferItemClean(t, service, second.Attachment.ID)
	return publishTransfer(t, owner, transfer.ID)
}

func resolvePickupRequest(client *httpTestClient, code string) *httptest.ResponseRecorder {
	return client.json(http.MethodPost, "/api/v1/pickups", `{"code":"`+code+`"}`)
}
