package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"pastebox/internal/app"
)

// transferAccessBody is the share password a recipient types. The field is
// spelled out here so both entry points send the same body shape.
func transferAccessBody(password string) string {
	encoded, err := json.Marshal(map[string]string{"password": password})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// openShareThroughLink is the link entry point: the recipient already holds the
// share token.
func openShareThroughLink(client *httpTestClient, token string, password string) *httptest.ResponseRecorder {
	return client.json(http.MethodPost, "/api/v1/shares/"+token+"/access", transferAccessBody(password))
}

// openShareThroughCode is the pickup-code entry point: the recipient types the
// 6-character code, resolves it, then opens whatever token it belongs to.
func openShareThroughCode(t *testing.T, client *httpTestClient, code string, password string) *httptest.ResponseRecorder {
	t.Helper()
	resolved := resolvePickup(t, client, code)
	if resolved.Code != http.StatusOK {
		return resolved
	}
	var pickup pickupResponse
	decodeResponse(t, resolved, &pickup)
	return openShareThroughLink(client, pickup.Token, password)
}

func publishedShareExpiry(t *testing.T, published app.TransferView) time.Time {
	t.Helper()
	if published.Share == nil {
		t.Fatalf("expected a published transfer to expose its share")
	}
	return published.Share.ExpiresAt
}

// publishOneFileTransfer publishes a one-file transfer asking for the given
// lifetime, with the share settings under test injected as raw JSON, and
// returns the sender's view, which carries the pickup code.
func publishOneFileTransfer(t *testing.T, owner *httpTestClient, key string, expiresInSeconds int64, extraJSON string) app.TransferView {
	t.Helper()
	body := fmt.Sprintf(
		`{"idempotencyKey":%q,"expiresInSeconds":%d,%s"items":[{"itemId":"itm-1","fileName":"report.txt","contentType":"text/plain","size":11}]}`,
		key,
		expiresInSeconds,
		extraJSON,
	)
	transfer := createTransfer(t, owner, body)
	uploadTransferItem(t, owner, transfer.ID, "itm-1", "report.txt", []byte("access-body"))
	return publishTransfer(t, owner, transfer.ID)
}

// sendProtectedTransfer is the matrix's one-hour send: the expiry is not what
// the permission cases are about.
func sendProtectedTransfer(t *testing.T, owner *httpTestClient, key string, extraJSON string) app.TransferView {
	t.Helper()
	return publishOneFileTransfer(t, owner, key, 3600, extraJSON)
}

// TestTransferEntryPointsEnforceTheSameShareAuth is the HTTP permission matrix
// for issue #8: a link and a pickup code must reach the same share under the
// same password and login rules, so neither entry point can be used to walk
// around the sender's privacy settings.
func TestTransferEntryPointsEnforceTheSameShareAuth(t *testing.T) {
	_, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "access-matrix@example.com", "Owner")

	open := sendProtectedTransfer(t, owner, "access-open", "")
	password := sendProtectedTransfer(t, owner, "access-password", `"password":"hunter2",`)
	login := sendProtectedTransfer(t, owner, "access-login", `"loginRequired":true,`)
	both := sendProtectedTransfer(t, owner, "access-both", `"password":"hunter2","loginRequired":true,`)

	account := newHTTPTestClient(t, handler)
	registerHTTPUser(t, account, "access-viewer@example.com", "Viewer")

	cases := []struct {
		name      string
		published app.TransferView
		entry     string
		viewer    *httpTestClient
		password  string
		want      int
	}{
		{"open link guest", open, "link", nil, "", http.StatusOK},
		{"open code guest", open, "code", nil, "", http.StatusOK},
		{"open link account", open, "link", account, "", http.StatusOK},
		{"open code account", open, "code", account, "", http.StatusOK},

		{"password link guest without password", password, "link", nil, "", http.StatusUnauthorized},
		{"password code guest without password", password, "code", nil, "", http.StatusUnauthorized},
		{"password link guest wrong password", password, "link", nil, "wrong", http.StatusUnauthorized},
		{"password code guest wrong password", password, "code", nil, "wrong", http.StatusUnauthorized},
		{"password link guest right password", password, "link", nil, "hunter2", http.StatusOK},
		{"password code guest right password", password, "code", nil, "hunter2", http.StatusOK},
		{"password link account without password", password, "link", account, "", http.StatusUnauthorized},
		{"password code account without password", password, "code", account, "", http.StatusUnauthorized},

		{"login link guest", login, "link", nil, "", http.StatusUnauthorized},
		{"login code guest", login, "code", nil, "", http.StatusUnauthorized},
		{"login link account", login, "link", account, "", http.StatusOK},
		{"login code account", login, "code", account, "", http.StatusOK},

		{"password+login link guest right password", both, "link", nil, "hunter2", http.StatusUnauthorized},
		{"password+login code guest right password", both, "code", nil, "hunter2", http.StatusUnauthorized},
		{"password+login link account wrong password", both, "link", account, "wrong", http.StatusUnauthorized},
		{"password+login code account wrong password", both, "code", account, "wrong", http.StatusUnauthorized},
		{"password+login link account right password", both, "link", account, "hunter2", http.StatusOK},
		{"password+login code account right password", both, "code", account, "hunter2", http.StatusOK},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			viewer := testCase.viewer
			if viewer == nil {
				viewer = newHTTPTestClient(t, handler)
			}
			var res *httptest.ResponseRecorder
			if testCase.entry == "link" {
				res = openShareThroughLink(viewer, testCase.published.Share.Token, testCase.password)
			} else {
				res = openShareThroughCode(t, viewer, testCase.published.PickupCode, testCase.password)
			}
			assertStatus(t, res, testCase.want)
			if testCase.want != http.StatusOK {
				return
			}
			// Both entry points must land on the same share and the same content.
			var body struct {
				Paste app.PasteView `json:"paste"`
				Share app.ShareView `json:"share"`
			}
			decodeResponse(t, res, &body)
			if body.Paste.ID != testCase.published.PasteID {
				t.Fatalf("expected %s entry to open paste %s, got %s", testCase.entry, testCase.published.PasteID, body.Paste.ID)
			}
			if body.Share.Token != testCase.published.Share.Token {
				t.Fatalf("expected %s entry to open share %s, got %s", testCase.entry, testCase.published.Share.Token, body.Share.Token)
			}
		})
	}
}

// TestGuestTransferEntryPointsEnforceTheSameShareAuth keeps the guest sender on
// the same rules as an account sender: the guest share password protects the
// link and the pickup code alike, and a guest cannot require login because the
// existing guest boundary forbids it.
func TestGuestTransferEntryPointsEnforceTheSameShareAuth(t *testing.T) {
	_, handler := newTransferTestServer(t)

	sender := newHTTPTestClient(t, handler)
	created := sender.json(http.MethodPost, "/api/v1/guest/transfers",
		`{"idempotencyKey":"guest-access","expiresInSeconds":3600,"password":"hunter2","items":[{"itemId":"itm-1","fileName":"report.txt","contentType":"text/plain","size":11}]}`)
	assertStatus(t, created, http.StatusCreated)
	var createBody struct {
		GuestToken string           `json:"guestToken"`
		Transfer   app.TransferView `json:"transfer"`
	}
	decodeResponse(t, created, &createBody)
	if createBody.GuestToken == "" {
		t.Fatalf("expected a guest token for the send")
	}
	upload := uploadGuestTransferItem(t, sender, createBody.GuestToken, createBody.Transfer.ID, "itm-1", "report.txt", []byte("access-body"))
	assertStatus(t, upload, http.StatusCreated)
	publishedRes := sender.json(http.MethodPost, "/api/v1/guest/transfers/"+createBody.Transfer.ID+"/publish",
		`{"guestToken":"`+createBody.GuestToken+`"}`)
	assertStatus(t, publishedRes, http.StatusOK)
	var publishBody transferResponse
	decodeResponse(t, publishedRes, &publishBody)
	published := publishBody.Transfer
	if published.Share == nil || !published.Share.HasPassword {
		t.Fatalf("expected a password protected guest share, got %#v", published.Share)
	}

	recipient := newHTTPTestClient(t, handler)
	assertStatus(t, openShareThroughLink(recipient, published.Share.Token, ""), http.StatusUnauthorized)
	assertStatus(t, openShareThroughCode(t, recipient, published.PickupCode, ""), http.StatusUnauthorized)
	assertStatus(t, openShareThroughLink(recipient, published.Share.Token, "hunter2"), http.StatusOK)
	assertStatus(t, openShareThroughCode(t, recipient, published.PickupCode, "hunter2"), http.StatusOK)

	// The guest boundary predates this ticket: guests cannot require login, and
	// the transfer namespace refuses it instead of quietly dropping the setting
	// and publishing a share the sender believes is login protected.
	loginRequired := sender.json(http.MethodPost, "/api/v1/guest/transfers",
		`{"idempotencyKey":"guest-login","expiresInSeconds":3600,"loginRequired":true,"items":[{"itemId":"itm-1","fileName":"report.txt","contentType":"text/plain","size":11}]}`)
	assertStatus(t, loginRequired, http.StatusBadRequest)
	if code := decodeErrorCode(t, loginRequired); code != "guest_share_login_required" {
		t.Fatalf("expected guest_share_login_required, got %q", code)
	}
}

// freePlanRetentionSeconds reads the active plan's retention from the public
// catalog so the test tracks the configured policy instead of a copied number.
func freePlanRetentionSeconds(t *testing.T, client *httpTestClient) int64 {
	t.Helper()
	res := client.json(http.MethodGet, "/api/v1/plans", "")
	assertStatus(t, res, http.StatusOK)
	var body struct {
		Plans []struct {
			ID                  string `json:"id"`
			MaxRetentionSeconds int64  `json:"maxRetentionSeconds"`
		} `json:"plans"`
	}
	decodeResponse(t, res, &body)
	for _, plan := range body.Plans {
		if plan.ID == "free" {
			if plan.MaxRetentionSeconds <= 0 {
				t.Fatalf("expected the free plan to publish a positive retention, got %d", plan.MaxRetentionSeconds)
			}
			return plan.MaxRetentionSeconds
		}
	}
	t.Fatalf("expected the public catalog to list the free plan, got %#v", body.Plans)
	return 0
}

// TestTransferShareExpiryNeverOutlivesItsContent locks in the retention rule: a
// requested expiry beyond the plan or guest retention is clamped, the share and
// the content it points at expire together, and both entry points stop working
// once that moment passes.
func TestTransferShareExpiryNeverOutlivesItsContent(t *testing.T) {
	handler, clock := newPickupClockTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "retention-owner@example.com", "Owner")

	retention := freePlanRetentionSeconds(t, owner)
	now := clock.Now()

	requested := retention * 10
	published := publishOneFileTransfer(t, owner, "retention-account", requested, "")

	wantExpiry := now.Add(time.Duration(retention) * time.Second)
	if !published.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expected the transfer expiry to clamp to the plan retention %s, got %s", wantExpiry, published.ExpiresAt)
	}
	if got := publishedShareExpiry(t, published); !got.Equal(wantExpiry) {
		t.Fatalf("expected the share expiry to equal the content expiry %s, got %s", wantExpiry, got)
	}
	// The content the share points at must carry the same expiry.
	recipient := newHTTPTestClient(t, handler)
	opened := openShareThroughLink(recipient, published.Share.Token, "")
	assertStatus(t, opened, http.StatusOK)
	var openedBody struct {
		Paste app.PasteView `json:"paste"`
	}
	decodeResponse(t, opened, &openedBody)
	if !openedBody.Paste.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expected the shared content to expire with the share at %s, got %s", wantExpiry, openedBody.Paste.ExpiresAt)
	}

	// A guest send clamps to the guest retention instead of the plan retention.
	guest := newHTTPTestClient(t, handler)
	guestCreated := guest.json(http.MethodPost, "/api/v1/guest/transfers", fmt.Sprintf(
		`{"idempotencyKey":"retention-guest","expiresInSeconds":%d,"items":[{"itemId":"itm-1","fileName":"report.txt","contentType":"text/plain","size":11}]}`,
		requested,
	))
	assertStatus(t, guestCreated, http.StatusCreated)
	var guestBody struct {
		GuestToken string           `json:"guestToken"`
		Transfer   app.TransferView `json:"transfer"`
	}
	decodeResponse(t, guestCreated, &guestBody)
	guestRetention := guestBody.Transfer.ExpiresAt.Sub(now)
	if guestRetention <= 0 || guestRetention >= time.Duration(requested)*time.Second {
		t.Fatalf("expected the guest expiry to clamp to the guest retention, got %s", guestRetention)
	}

	// Past the expiry the link is gone and the code no longer resolves.
	clock.Advance(time.Duration(retention) * time.Second)
	expiredLink := openShareThroughLink(newHTTPTestClient(t, handler), published.Share.Token, "")
	assertStatus(t, expiredLink, http.StatusGone)
	if code := decodeErrorCode(t, expiredLink); code != "share_expired" {
		t.Fatalf("expected share_expired for an expired link, got %q", code)
	}
	expiredCode := resolvePickup(t, newHTTPTestClient(t, handler), published.PickupCode)
	assertStatus(t, expiredCode, http.StatusNotFound)
	if code := decodeErrorCode(t, expiredCode); code != "pickup_not_found" {
		t.Fatalf("expected pickup_not_found for an expired code, got %q", code)
	}
}

// TestTransferExpiryFallsBackToThePlanPolicy documents that the send settings
// stay inside the existing policy: a non-positive expiry falls back to the plan
// retention rather than creating a share that never expires.
func TestTransferExpiryFallsBackToThePlanPolicy(t *testing.T) {
	handler, clock := newPickupClockTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "retention-default@example.com", "Owner")

	retention := freePlanRetentionSeconds(t, owner)
	now := clock.Now()
	published := publishOneFileTransfer(t, owner, "retention-default", 0, "")

	wantExpiry := now.Add(time.Duration(retention) * time.Second)
	if !published.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expected the default expiry to be the plan retention %s, got %s", wantExpiry, published.ExpiresAt)
	}
	if got := publishedShareExpiry(t, published); !got.Equal(wantExpiry) {
		t.Fatalf("expected the default share expiry to equal the content expiry %s, got %s", wantExpiry, got)
	}
}

// TestTransferExpiryClampsRequestedValues keeps the send flow honest: the
// settings the client may offer are exactly the ones the service enforces, so a
// client asking for more than the policy allows gets the clamped value instead
// of a share with a longer life than its content.
func TestTransferExpiryClampsRequestedValues(t *testing.T) {
	handler, _ := newPickupClockTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "retention-clamp@example.com", "Owner")

	retention := freePlanRetentionSeconds(t, owner)
	for _, requested := range []int64{retention + 1, retention * 2, 180 * 24 * 60 * 60} {
		key := "clamp-" + strconv.FormatInt(requested, 10)
		published := publishOneFileTransfer(t, owner, key, requested, "")
		if got := publishedShareExpiry(t, published); got.After(published.ExpiresAt) {
			t.Fatalf("requested %d produced a share expiring after its content: %s > %s", requested, got, published.ExpiresAt)
		}
		if published.ExpiresAt.Sub(published.CreatedAt) > time.Duration(retention)*time.Second {
			t.Fatalf("requested %d outlived the plan retention %d", requested, retention)
		}
	}
}

// TestPickupResolutionIsNotAuthorization keeps the two steps of the code entry
// apart: resolving a code hands back a credential and nothing else, and the
// share still applies the password and login rules before showing any content.
func TestPickupResolutionIsNotAuthorization(t *testing.T) {
	_, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "resolution-owner@example.com", "Owner")

	published := sendProtectedTransfer(t, owner, "resolution-both", `"password":"hunter2","loginRequired":true,`)
	guest := newHTTPTestClient(t, handler)

	resolved := resolvePickup(t, guest, published.PickupCode)
	assertStatus(t, resolved, http.StatusOK)
	var resolution map[string]any
	decodeResponse(t, resolved, &resolution)
	if len(resolution) != 2 || resolution["token"] == nil || resolution["url"] == nil {
		t.Fatalf("expected the resolution to carry only a token and its URL, got %#v", resolution)
	}
	// Resolution is not access: the credential still has to pass the same
	// checks the link faces.
	assertStatus(t, openShareThroughCode(t, guest, published.PickupCode, "hunter2"), http.StatusUnauthorized)
	assertStatus(t, openShareThroughCode(t, guest, published.PickupCode, "wrong"), http.StatusUnauthorized)

	viewer := newHTTPTestClient(t, handler)
	registerHTTPUser(t, viewer, "resolution-viewer@example.com", "Viewer")
	assertStatus(t, openShareThroughCode(t, viewer, published.PickupCode, "hunter2"), http.StatusOK)
}
