package httpserver

import (
	"bytes"
	"context"
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

// pickupTestClock lets a test move the service clock instead of sleeping until
// a share expires.
type pickupTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *pickupTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *pickupTestClock) Advance(step time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(step)
}

// newPickupClockTestServer serves a service whose clock the test controls.
func newPickupClockTestServer(t *testing.T) (http.Handler, *pickupTestClock) {
	t.Helper()
	t.Setenv("PASTEBOX_DEV_AUTH_TOKENS", "true")
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	clock := &pickupTestClock{now: time.Now().UTC()}
	service := app.NewForTest(clock.Now)
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)
	return handler, clock
}

// newPickupLogTestServer captures every log line so a test can assert that
// pickup credentials never end up in them.
func newPickupLogTestServer(t *testing.T, logs *bytes.Buffer) http.Handler {
	t.Helper()
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	service := app.New(cfg)
	return NewWithService(cfg, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})), service)
}

type pickupResponse struct {
	Token string `json:"token"`
	URL   string `json:"url"`
}

// sendOneFileTransfer publishes a one-file transfer and returns the published
// transfer view, which carries the pickup code on the sender's side.
func sendOneFileTransfer(t *testing.T, owner *httpTestClient, key string) app.TransferView {
	t.Helper()
	transfer := createTransfer(t, owner, fmt.Sprintf(`{"idempotencyKey":%q,"expiresInSeconds":3600,"items":[{"itemId":"itm-1","fileName":"report.txt","contentType":"text/plain","size":11}]}`, key))
	uploadTransferItem(t, owner, transfer.ID, "itm-1", "report.txt", []byte("pickup-body"))
	return publishTransfer(t, owner, transfer.ID)
}

func resolvePickup(t *testing.T, client *httpTestClient, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatalf("encode pickup body: %v", err)
	}
	return client.json(http.MethodPost, "/api/v1/pickups", string(body))
}

func TestTransferPickupCodeResolvesTheSameShareAsTheLink(t *testing.T) {
	_, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "pickup-owner@example.com", "Owner")

	published := sendOneFileTransfer(t, owner, "pickup-1")
	code := published.PickupCode
	if len(code) != app.PickupCodeLength {
		t.Fatalf("expected a %d-character pickup code, got %q", app.PickupCodeLength, code)
	}
	for _, char := range code {
		if !strings.ContainsRune(app.PickupCodeAlphabet, char) {
			t.Fatalf("pickup code %q contains confusable character %q", code, char)
		}
	}
	if published.Share == nil {
		t.Fatalf("expected a published transfer to expose its share")
	}

	// Resolving does not open the share, so it must not consume a visit.
	shares := owner.json(http.MethodGet, "/api/v1/shares", "")
	assertStatus(t, shares, http.StatusOK)
	var sharesBody struct {
		Shares []app.ShareView `json:"shares"`
	}
	decodeResponse(t, shares, &sharesBody)
	if len(sharesBody.Shares) != 1 || sharesBody.Shares[0].VisitCount != 0 {
		t.Fatalf("expected resolving a code to leave visit counts untouched, got %#v", sharesBody.Shares)
	}

	// Recipients type what they see, not a canonical form.
	recipient := newHTTPTestClient(t, handler)
	typed := strings.ToLower(code[:3]) + " " + strings.ToLower(code[3:])
	resolved := resolvePickup(t, recipient, typed)
	assertStatus(t, resolved, http.StatusOK)
	var pickup pickupResponse
	decodeResponse(t, resolved, &pickup)
	if pickup.Token != published.Share.Token {
		t.Fatalf("expected the code to resolve share token %q, got %q", published.Share.Token, pickup.Token)
	}
	if !strings.HasSuffix(pickup.URL, "/s/"+published.Share.Token) {
		t.Fatalf("expected a share URL for the resolved token, got %q", pickup.URL)
	}

	// The resolved token opens exactly what the link opens.
	access := recipient.json(http.MethodPost, "/api/v1/shares/"+pickup.Token+"/access", `{"password":""}`)
	assertStatus(t, access, http.StatusOK)
	var accessed struct {
		Paste app.PasteView `json:"paste"`
	}
	decodeResponse(t, access, &accessed)
	if accessed.Paste.ID != published.PasteID {
		t.Fatalf("expected the pickup code to open paste %q, got %q", published.PasteID, accessed.Paste.ID)
	}
}

func TestPickupCodeAnswersUnknownAndRevokedCodesAlike(t *testing.T) {
	_, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "pickup-revoked@example.com", "Owner")

	published := sendOneFileTransfer(t, owner, "pickup-revoked")
	recipient := newHTTPTestClient(t, handler)

	unknown := resolvePickup(t, recipient, "ZZZZZZ")
	assertStatus(t, unknown, http.StatusNotFound)
	var unknownBody map[string]string
	decodeResponse(t, unknown, &unknownBody)
	if unknownBody["error"] != "pickup_not_found" {
		t.Fatalf("expected pickup_not_found for an unknown code, got %#v", unknownBody)
	}

	revoked := owner.json(http.MethodDelete, "/api/v1/shares/"+published.Share.ID, "")
	assertStatus(t, revoked, http.StatusOK)

	afterRevoke := resolvePickup(t, recipient, published.PickupCode)
	assertStatus(t, afterRevoke, http.StatusNotFound)
	var revokedBody map[string]string
	decodeResponse(t, afterRevoke, &revokedBody)
	if revokedBody["error"] != unknownBody["error"] || revokedBody["message"] != unknownBody["message"] {
		t.Fatalf("expected revoked and unknown codes to answer alike, got %#v and %#v", revokedBody, unknownBody)
	}

	// The revoked link keeps failing the same way, so a code adds no bypass.
	link := recipient.json(http.MethodPost, "/api/v1/shares/"+published.Share.Token+"/access", `{"password":""}`)
	assertStatus(t, link, http.StatusGone)
}

func TestPickupCodeStopsResolvingAfterShareExpiry(t *testing.T) {
	handler, clock := newPickupClockTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "pickup-expiry@example.com", "Owner")

	published := sendOneFileTransfer(t, owner, "pickup-expiry")
	recipient := newHTTPTestClient(t, handler)

	fresh := resolvePickup(t, recipient, published.PickupCode)
	assertStatus(t, fresh, http.StatusOK)

	clock.Advance(2 * time.Hour)

	expired := resolvePickup(t, recipient, published.PickupCode)
	assertStatus(t, expired, http.StatusNotFound)
	var expiredBody map[string]string
	decodeResponse(t, expired, &expiredBody)
	if expiredBody["error"] != "pickup_not_found" {
		t.Fatalf("expected an expired code to report pickup_not_found, got %#v", expiredBody)
	}
}

func TestPickupCodeAttemptsAreRateLimited(t *testing.T) {
	_, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "pickup-limit@example.com", "Owner")
	published := sendOneFileTransfer(t, owner, "pickup-limit")

	recipient := newHTTPTestClient(t, handler)
	for i := 0; i < 10; i++ {
		guessed := resolvePickup(t, recipient, "AAAAA"+string(app.PickupCodeAlphabet[i]))
		assertStatus(t, guessed, http.StatusNotFound)
	}

	// A 6-character code space is small enough that guessing has to cost
	// something: the next attempt is refused before any lookup happens.
	blocked := resolvePickup(t, recipient, "AAAAA"+string(app.PickupCodeAlphabet[10]))
	assertStatus(t, blocked, http.StatusTooManyRequests)
	if got := blocked.Header().Get("Retry-After"); got == "" || got == "0" {
		t.Fatalf("expected a Retry-After header on a rate-limited pickup attempt, got %q", got)
	}
	var blockedBody map[string]string
	decodeResponse(t, blocked, &blockedBody)
	if blockedBody["error"] != "pickup_rate_limited" {
		t.Fatalf("expected pickup_rate_limited, got %#v", blockedBody)
	}

	// A correct code is refused too, so the limit does not leak which guesses
	// were close.
	correctWhileBlocked := resolvePickup(t, recipient, published.PickupCode)
	assertStatus(t, correctWhileBlocked, http.StatusTooManyRequests)
}

func TestLegacySharesKeepWorkingWithoutPickupCodes(t *testing.T) {
	_, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "pickup-legacy@example.com", "Owner")

	created := owner.json(http.MethodPost, "/api/v1/pastes", `{"title":"Legacy","text":"hello","tags":[],"expiresInSeconds":3600}`)
	assertStatus(t, created, http.StatusCreated)
	var paste app.PasteView
	decodeResponse(t, created, &paste)

	shareResponse := owner.json(http.MethodPost, "/api/v1/pastes/"+paste.ID+"/shares", `{"password":"","loginRequired":false,"maxVisits":0,"maxDownloads":0,"expiresInSeconds":0}`)
	assertStatus(t, shareResponse, http.StatusCreated)
	var shareBody map[string]json.RawMessage
	decodeResponse(t, shareResponse, &shareBody)
	if _, ok := shareBody["pickupCode"]; ok {
		t.Fatalf("expected a plain share to expose no pickup code, got %#v", shareBody)
	}
	var share app.ShareView
	decodeResponse(t, shareResponse, &share)

	// The old link still opens the share.
	recipient := newHTTPTestClient(t, handler)
	access := recipient.json(http.MethodPost, "/api/v1/shares/"+share.Token+"/access", `{"password":""}`)
	assertStatus(t, access, http.StatusOK)

	// And a code that was never issued does not resolve to it.
	guessed := resolvePickup(t, recipient, "234567")
	assertStatus(t, guessed, http.StatusNotFound)
}

func TestPickupCodeGrantsNoSenderControls(t *testing.T) {
	_, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "pickup-scope@example.com", "Owner")
	published := sendOneFileTransfer(t, owner, "pickup-scope")

	recipient := newHTTPTestClient(t, handler)
	resolved := resolvePickup(t, recipient, published.PickupCode)
	assertStatus(t, resolved, http.StatusOK)
	var payload map[string]json.RawMessage
	decodeResponse(t, resolved, &payload)
	if len(payload) != 2 {
		t.Fatalf("expected a pickup lookup to return only the recipient credential, got %#v", payload)
	}
	for _, key := range []string{"token", "url"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected %q in the pickup lookup response, got %#v", key, payload)
		}
	}

	// Holding the code (or the token it resolves to) still does not authorize
	// the sender's management routes.
	revoke := recipient.json(http.MethodDelete, "/api/v1/shares/"+published.Share.ID, "")
	assertStatus(t, revoke, http.StatusUnauthorized)
	transfers := recipient.json(http.MethodGet, "/api/v1/transfers", "")
	assertStatus(t, transfers, http.StatusUnauthorized)
}

func TestPickupCredentialsStayOutOfLogs(t *testing.T) {
	var logs bytes.Buffer
	handler := newPickupLogTestServer(t, &logs)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "pickup-logs@example.com", "Owner")
	published := sendOneFileTransfer(t, owner, "pickup-logs")

	recipient := newHTTPTestClient(t, handler)
	resolved := resolvePickup(t, recipient, published.PickupCode)
	assertStatus(t, resolved, http.StatusOK)

	logged := logs.String()
	// Anchor the check: without these lines the credential assertions below
	// would pass on an empty log.
	for _, expected := range []string{"transfer published", "pickup code resolved"} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("expected %q in the captured logs, got %d bytes", expected, len(logged))
		}
	}
	if strings.Contains(logged, published.PickupCode) {
		t.Fatalf("expected the pickup code to stay out of logs")
	}
	if strings.Contains(logged, published.Share.Token) {
		t.Fatalf("expected the share token to stay out of logs")
	}
}

// sharedPickupAttempts stands in for the PostgreSQL counter: it proves the
// service consults a shared store instead of its process-local fallback.
type sharedPickupAttempts struct {
	mu       sync.Mutex
	windows  map[string]app.PickupAttemptWindow
	reads    int
	failures int
}

func newSharedPickupAttempts() *sharedPickupAttempts {
	return &sharedPickupAttempts{windows: map[string]app.PickupAttemptWindow{}}
}

func (s *sharedPickupAttempts) PickupAttemptCount(_ context.Context, key string, window time.Duration, now time.Time) (app.PickupAttemptWindow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	attempt := s.windows[key]
	if attempt.Start.IsZero() || !attempt.Start.Add(window).After(now) {
		return app.PickupAttemptWindow{Start: now}, nil
	}
	return attempt, nil
}

func (s *sharedPickupAttempts) RecordPickupFailure(_ context.Context, key string, window time.Duration, now time.Time) (app.PickupAttemptWindow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures++
	attempt := s.windows[key]
	if attempt.Start.IsZero() || !attempt.Start.Add(window).After(now) {
		attempt = app.PickupAttemptWindow{Start: now}
	}
	attempt.Count++
	s.windows[key] = attempt
	return attempt, nil
}

func (s *sharedPickupAttempts) snapshot() (reads int, failures int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads, s.failures
}

func newSharedAttemptServer(t *testing.T, attempts *sharedPickupAttempts) http.Handler {
	t.Helper()
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	service, err := app.NewWithStorage(context.Background(), cfg, app.Stores{
		Content: app.ContentStores{PickupAttempts: attempts},
	})
	if err != nil {
		t.Fatalf("new service with shared attempt store: %v", err)
	}
	return NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)
}

func TestPickupBudgetIsSpentFromTheSharedStore(t *testing.T) {
	attempts := newSharedPickupAttempts()
	handler := newSharedAttemptServer(t, attempts)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "pickup-shared@example.com", "Owner")
	sendOneFileTransfer(t, owner, "pickup-shared")

	recipient := newHTTPTestClient(t, handler)
	for i := 0; i < 10; i++ {
		guessed := resolvePickup(t, recipient, "AAAAA"+string(app.PickupCodeAlphabet[i]))
		assertStatus(t, guessed, http.StatusNotFound)
	}
	blocked := resolvePickup(t, recipient, "AAAAA"+string(app.PickupCodeAlphabet[10]))
	assertStatus(t, blocked, http.StatusTooManyRequests)

	// The budget came from the shared counter, not from this process's map.
	reads, failures := attempts.snapshot()
	if failures != 10 {
		t.Fatalf("expected the shared store to record 10 failures, got %d", failures)
	}
	if reads < 11 {
		t.Fatalf("expected the shared store to be read before every attempt, got %d reads", reads)
	}
}

func TestSuccessfulPickupsDoNotSpendTheGuessBudget(t *testing.T) {
	_, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "pickup-fair@example.com", "Owner")
	published := sendOneFileTransfer(t, owner, "pickup-fair")

	// A working code keeps working: only failed guesses are counted, so a real
	// recipient is never locked out by other people's guesses.
	recipient := newHTTPTestClient(t, handler)
	for i := 0; i < 12; i++ {
		resolved := resolvePickup(t, recipient, published.PickupCode)
		assertStatus(t, resolved, http.StatusOK)
	}

	// Guesses are still counted after all those successes.
	for i := 0; i < 10; i++ {
		guessed := resolvePickup(t, recipient, "AAAAA"+string(app.PickupCodeAlphabet[i]))
		assertStatus(t, guessed, http.StatusNotFound)
	}
	blocked := resolvePickup(t, recipient, "AAAAA"+string(app.PickupCodeAlphabet[10]))
	assertStatus(t, blocked, http.StatusTooManyRequests)
}

func TestGuestTransferPickupCodeResolves(t *testing.T) {
	_, handler, _ := newMultiFileTransferServer(t, guestMultiFileConfig())
	client := newHTTPTestClient(t, handler)
	const token = "guest-pickup-token"

	transfer := createGuestTransfer(t, client, token, `{"itemId":"itm-1","fileName":"note.txt","contentType":"text/plain","size":10}`)
	upload := uploadGuestTransferItem(t, client, token, transfer.ID, "itm-1", "note.txt", []byte("guest-body"))
	assertStatus(t, upload, http.StatusCreated)

	published := client.json(http.MethodPost, "/api/v1/guest/transfers/"+transfer.ID+"/publish", `{"guestToken":"`+token+`"}`)
	assertStatus(t, published, http.StatusOK)
	var publishedBody transferResponse
	decodeResponse(t, published, &publishedBody)
	code := publishedBody.Transfer.PickupCode
	if len(code) != app.PickupCodeLength || publishedBody.Transfer.Share == nil {
		t.Fatalf("expected a guest send to publish a code, got %#v", publishedBody.Transfer)
	}

	recipient := newHTTPTestClient(t, handler)
	resolved := resolvePickup(t, recipient, code)
	assertStatus(t, resolved, http.StatusOK)
	var pickup pickupResponse
	decodeResponse(t, resolved, &pickup)
	if pickup.Token != publishedBody.Transfer.Share.Token {
		t.Fatalf("expected the guest code to resolve the guest share, got %q", pickup.Token)
	}
}

func TestPickupCodeKeepsSharePasswordAndLoginRules(t *testing.T) {
	_, handler := newTransferTestServer(t)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "pickup-auth@example.com", "Owner")

	passwordTransfer := createTransfer(t, owner, `{"idempotencyKey":"pickup-password","expiresInSeconds":3600,"password":"hunter2","items":[{"itemId":"itm-1","fileName":"report.txt","contentType":"text/plain","size":11}]}`)
	uploadTransferItem(t, owner, passwordTransfer.ID, "itm-1", "report.txt", []byte("pickup-body"))
	passwordPublished := publishTransfer(t, owner, passwordTransfer.ID)

	loginTransfer := createTransfer(t, owner, `{"idempotencyKey":"pickup-login","expiresInSeconds":3600,"loginRequired":true,"items":[{"itemId":"itm-1","fileName":"report.txt","contentType":"text/plain","size":11}]}`)
	uploadTransferItem(t, owner, loginTransfer.ID, "itm-1", "report.txt", []byte("pickup-body"))
	loginPublished := publishTransfer(t, owner, loginTransfer.ID)

	recipient := newHTTPTestClient(t, handler)

	// A code resolves a protected share, but it does not open it: the password
	// still has to be right.
	resolved := resolvePickup(t, recipient, passwordPublished.PickupCode)
	assertStatus(t, resolved, http.StatusOK)
	var pickup pickupResponse
	decodeResponse(t, resolved, &pickup)
	if pickup.Token != passwordPublished.Share.Token {
		t.Fatalf("expected the code to resolve the protected share, got %q", pickup.Token)
	}
	wrongPassword := recipient.json(http.MethodPost, "/api/v1/shares/"+pickup.Token+"/access", `{"password":"wrong"}`)
	assertStatus(t, wrongPassword, http.StatusUnauthorized)
	rightPassword := recipient.json(http.MethodPost, "/api/v1/shares/"+pickup.Token+"/access", `{"password":"hunter2"}`)
	assertStatus(t, rightPassword, http.StatusOK)

	// Login-required shares behave the same through a code.
	loginResolved := resolvePickup(t, recipient, loginPublished.PickupCode)
	assertStatus(t, loginResolved, http.StatusOK)
	var loginPickup pickupResponse
	decodeResponse(t, loginResolved, &loginPickup)
	anonymousAccess := recipient.json(http.MethodPost, "/api/v1/shares/"+loginPickup.Token+"/access", `{"password":""}`)
	assertStatus(t, anonymousAccess, http.StatusUnauthorized)
	viewer := newHTTPTestClient(t, handler)
	registerHTTPUser(t, viewer, "pickup-viewer@example.com", "Viewer")
	signedInAccess := viewer.json(http.MethodPost, "/api/v1/shares/"+loginPickup.Token+"/access", `{"password":""}`)
	assertStatus(t, signedInAccess, http.StatusOK)
}
