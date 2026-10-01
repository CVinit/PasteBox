package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"pastebox/internal/app"
)

// statusEvent is one event-stream event: the name the server sent and its JSON
// payload.
type statusEvent struct {
	Name string
	Data string
}

// statusStream is one open subscription against a real HTTP server, so the test
// observes what a browser observes: a response whose events arrive over time
// and whose connection state is the transport's, not the page's guess.
type statusStream struct {
	t      *testing.T
	cancel context.CancelFunc
	body   io.ReadCloser
	events <-chan statusEvent
	done   chan struct{}
	once   sync.Once
}

// openStatusStream opens a status channel with the cookies of one browser. A
// cookie map rather than a jar keeps the setup shared with the existing handler
// helpers, which drive the same account and share through ordinary requests.
func openStatusStream(t *testing.T, server *httptest.Server, path string, cookies map[string]*http.Cookie) *statusStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
	if err != nil {
		cancel()
		t.Fatalf("build status request: %v", err)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := server.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open status stream %s: %v", path, err)
	}
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		cancel()
		t.Fatalf("expected the status stream to open, got %d: %s", res.StatusCode, body)
	}
	if contentType := res.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		_ = res.Body.Close()
		cancel()
		t.Fatalf("expected an event stream, got content type %q", contentType)
	}
	stream := &statusStream{
		t:      t,
		cancel: cancel,
		body:   res.Body,
		done:   make(chan struct{}),
	}
	stream.events = readStatusEvents(stream.done, res.Body)
	t.Cleanup(stream.close)
	return stream
}

func (s *statusStream) close() {
	s.once.Do(func() {
		close(s.done)
		s.cancel()
		_ = s.body.Close()
	})
}

// nextStatus waits for the next snapshot and decodes it. A test that never
// receives one fails instead of hanging, which is what makes a live channel
// testable at all.
func (s *statusStream) nextStatus() app.AccountStatusView {
	s.t.Helper()
	event := s.nextEvent()
	var snapshot app.AccountStatusView
	if err := json.Unmarshal([]byte(event.Data), &snapshot); err != nil {
		s.t.Fatalf("decode account status %q: %v", event.Data, err)
	}
	return snapshot
}

// nextTransferStatus waits for the next credential-scoped snapshot.
func (s *statusStream) nextTransferStatus() app.TransferStatusView {
	s.t.Helper()
	event := s.nextEvent()
	var snapshot app.TransferStatusView
	if err := json.Unmarshal([]byte(event.Data), &snapshot); err != nil {
		s.t.Fatalf("decode send status %q: %v", event.Data, err)
	}
	return snapshot
}

// nextEvent waits for the next snapshot. Keepalive comments and unnamed lines
// are skipped by the reader, so a test only ever sees real state.
func (s *statusStream) nextEvent() statusEvent {
	s.t.Helper()
	event := s.nextNamedEvent()
	if event.Name != "status" {
		s.t.Fatalf("expected a status event, got %q (%s)", event.Name, event.Data)
	}
	return event
}

// nextNamedEvent waits for the next named event, including the one the server
// sends when it ends a subscription on purpose.
func (s *statusStream) nextNamedEvent() statusEvent {
	s.t.Helper()
	select {
	case event, ok := <-s.events:
		if !ok {
			s.t.Fatal("the status stream closed before it reported any state")
		}
		return event
	case <-time.After(5 * time.Second):
		s.t.Fatal("timed out waiting for a status event")
		return statusEvent{}
	}
}

// readStatusEvents parses an event stream into named events. It runs until the
// body ends or the test closes the stream, so a test never leaks a reader.
func readStatusEvents(done <-chan struct{}, body io.Reader) <-chan statusEvent {
	events := make(chan statusEvent)
	go func() {
		defer close(events)
		reader := bufio.NewReader(body)
		for {
			event, err := readStatusEvent(reader)
			if err != nil {
				return
			}
			if event.Name == "" {
				continue
			}
			select {
			case events <- event:
			case <-done:
				return
			}
		}
	}()
	return events
}

// readStatusEvent reads one event: comment lines are keepalives and carry no
// state, and a blank line ends the event.
func readStatusEvent(reader *bufio.Reader) (statusEvent, error) {
	var event statusEvent
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return statusEvent{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if event.Name != "" {
				return event, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch name {
		case "event":
			event.Name = strings.TrimPrefix(value, " ")
		case "data":
			event.Data = strings.TrimPrefix(value, " ")
		}
	}
}

// statusStreamInterval is the shortened re-read interval the streaming tests
// run with, so a test does not wait seconds for a change it just made.
const statusStreamInterval = 20 * time.Millisecond

// statusStreamServer is the real HTTP server behind the streaming tests, with
// the app service that lets a test drive the same scenario from the inside. The
// interval is set before the server starts serving, so no handler can observe
// it changing.
func statusStreamServer(t *testing.T) (*app.Service, *Server, *httptest.Server) {
	t.Helper()
	service, handler := newTransferTestServer(t)
	handler.statusSyncInterval = statusStreamInterval
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return service, handler, server
}

// TestAccountStatusStreamUpdatesASecondDevice is the account-scope acceptance
// test: a second device of the same account sees the claim count move without
// refreshing, and watching the send never spends a slot of its own.
func TestAccountStatusStreamUpdatesASecondDevice(t *testing.T) {
	service, _, server := statusStreamServer(t)

	owner := newHTTPTestClient(t, server.Config.Handler)
	registerHTTPUser(t, owner, "status-account@example.com", "Owner")
	published := publishFileTransferWithQuota(t, service, owner, "status-account", 1, "")

	stream := openStatusStream(t, server, "/api/v1/me/events", owner.cookies)
	first := stream.nextStatus()
	if len(first.Transfers) != 1 {
		t.Fatalf("expected the account snapshot to carry the send, got %#v", first.Transfers)
	}
	if first.Transfers[0].TransferID != published.ID || first.Transfers[0].State != app.TransferStatusStateClaimable {
		t.Fatalf("unexpected first snapshot %#v", first.Transfers[0])
	}
	if first.Transfers[0].ClaimedCount != 0 || first.Transfers[0].ClaimsRemaining != 1 {
		t.Fatalf("expected the send to start unclaimed, got %#v", first.Transfers[0])
	}
	if len(first.Pastes) != 1 || first.Pastes[0].ID != published.PasteID {
		t.Fatalf("expected the record marker, got %#v", first.Pastes)
	}

	recipient := newHTTPTestClient(t, server.Config.Handler)
	openAndClaimShare(t, recipient, published.Share.Token, "", "op-status-account")

	updated := stream.nextStatus()
	if len(updated.Transfers) != 1 {
		t.Fatalf("expected the same single send, got %#v", updated.Transfers)
	}
	if updated.Transfers[0].ClaimedCount != 1 || updated.Transfers[0].ClaimsRemaining != 0 {
		t.Fatalf("expected the claim to reach the second device, got %#v", updated.Transfers[0])
	}
	if updated.Transfers[0].State != app.TransferStatusStateExhausted {
		t.Fatalf("expected an exhausted send, got %#v", updated.Transfers[0])
	}

	// Watching is free: the subscription reported one claim because one claim
	// happened, not because the channel spent anything.
	stored, err := service.GetTransferWithContext(context.Background(), owner.userID(), published.ID)
	if err != nil {
		t.Fatalf("reload transfer: %v", err)
	}
	if stored.ClaimedCount != 1 {
		t.Fatalf("expected the subscription to spend nothing, got %d claims", stored.ClaimedCount)
	}
}

// TestAccountStatusStreamRejectsAnUnauthenticatedCaller keeps the account
// channel behind the session, so a live feed is never a way to read an account
// that did not sign in.
func TestAccountStatusStreamRejectsAnUnauthenticatedCaller(t *testing.T) {
	_, _, server := statusStreamServer(t)

	res, err := server.Client().Get(server.URL + "/api/v1/me/events")
	if err != nil {
		t.Fatalf("request account status: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected an unauthenticated subscription to be refused, got %d", res.StatusCode)
	}
}

func TestAccountStatusStreamStopsWhenSessionEnds(t *testing.T) {
	for _, ending := range []string{"logout", "logout-all", "expiry", "password-reset"} {
		t.Run(ending, func(t *testing.T) {
			service, handler, clock := newClaimClockTestServer(t)
			handler.(*Server).statusSyncInterval = statusStreamInterval
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)
			owner := newHTTPTestClient(t, handler)
			registerHTTPUser(t, owner, "ended-session@example.com", "Owner")
			userID := owner.userID()
			session := *owner.cookies[sessionCookieName]
			stream := openStatusStream(t, server, "/api/v1/me/events", owner.cookies)
			stream.nextStatus()

			switch ending {
			case "logout", "logout-all":
				assertStatus(t, owner.json(http.MethodPost, "/api/v1/auth/"+ending, ""), http.StatusOK)
			case "password-reset":
				reset := owner.json(http.MethodPost, "/api/v1/auth/password-reset/start", `{"email":"ended-session@example.com"}`)
				assertStatus(t, reset, http.StatusOK)
				var token struct {
					DevToken string `json:"devToken"`
				}
				decodeResponse(t, reset, &token)
				if token.DevToken == "" {
					t.Fatal("missing reset token")
				}
				assertStatus(t, owner.json(http.MethodPost, "/api/v1/auth/password-reset/finish", `{"token":"`+token.DevToken+`","password":"updated-password"}`), http.StatusOK)
			case "expiry":
				clock.Advance(session.Expires.Sub(clock.Now()) + time.Second)
			}
			// A different valid session may create content after this one ends.
			// The old stream must close, not disclose that newer record.
			if _, err := service.CreateTransferWithContext(context.Background(), userID, app.TransferInput{
				Text: "new private record", ExpiresInSeconds: 3600,
			}); err != nil {
				t.Fatalf("create newer record: %v", err)
			}
			event := stream.nextNamedEvent()
			if event.Name != "closed" || !strings.Contains(event.Data, "unauthorized") {
				t.Fatalf("ended session received %q instead of closed/unauthorized", event.Name)
			}
		})
	}
}

func TestAccountStatusStreamPagesHistoryAndWatchesOldEditor(t *testing.T) {
	service, handler, clock := newClaimClockTestServer(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	owner := newHTTPTestClient(t, handler)
	registerHTTPUser(t, owner, "status-pages@example.com", "Owner")
	admin, err := service.SeedAdmin("status-pages-admin@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdminSetUserPlan(admin.ID, owner.userID(), "pro", nil, "test pages", ""); err != nil {
		t.Fatal(err)
	}
	var old app.TransferView
	for i := range app.AccountStatusPageSize + 1 {
		clock.Advance(time.Second)
		transfer, err := service.CreateTransferWithContext(context.Background(), owner.userID(), app.TransferInput{Text: "saved", ExpiresInSeconds: 3600})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			old = transfer
		}
	}
	stream := openStatusStream(t, server, "/api/v1/me/events?pasteId="+old.PasteID, owner.cookies)
	first := stream.nextStatus()
	if len(first.Transfers) != app.AccountStatusPageSize || first.NextTransferCursor == "" || len(first.Pastes) != app.AccountStatusPageSize+1 {
		t.Fatalf("unexpected bounded snapshot: %#v", first)
	}
	stream.close()
	history := openStatusStream(t, server, "/api/v1/me/events?before="+first.NextTransferCursor+"&pasteId="+old.PasteID, owner.cookies)
	last := history.nextStatus()
	if len(last.Transfers) != 1 || last.Transfers[0].TransferID != old.ID || last.NextTransferCursor != "" {
		t.Fatalf("older record disappeared: %#v", last)
	}
}

// TestShareStatusStreamServesRecipientAndSenderWithoutSpendingAClaim covers the
// credential scope from both sides of the same send: the sender that just
// published watches it, the recipient that opened it watches it, a browser that
// holds neither credential is refused, and neither subscription moves the claim
// count.
func TestShareStatusStreamServesRecipientAndSenderWithoutSpendingAClaim(t *testing.T) {
	service, _, server := statusStreamServer(t)

	owner := newHTTPTestClient(t, server.Config.Handler)
	registerHTTPUser(t, owner, "status-share@example.com", "Owner")
	published := publishFileTransferWithQuota(t, service, owner, "status-share", 2, "")
	token := published.Share.Token

	// A browser that never opened the share cannot watch it.
	stranger := newHTTPTestClient(t, server.Config.Handler)
	refused := stranger.json(http.MethodGet, "/api/v1/shares/"+token+"/events", "")
	assertStatus(t, refused, http.StatusUnauthorized)

	// The sender got the same page grant a recipient earns, so the success page
	// watches its own send over the same channel.
	senderStream := openStatusStream(t, server, "/api/v1/shares/"+token+"/events", owner.cookies)
	first := senderStream.nextTransferStatus()
	if first.State != app.TransferStatusStateClaimable || first.ClaimsRemaining != 2 || first.Claimed {
		t.Fatalf("unexpected sender snapshot %#v", first)
	}

	// The recipient opens the share, which is what earns the page grant.
	recipient := newHTTPTestClient(t, server.Config.Handler)
	openTransferShare(t, recipient, token, "")
	recipientStream := openStatusStream(t, server, "/api/v1/shares/"+token+"/events", recipient.cookies)
	if opened := recipientStream.nextTransferStatus(); opened.Claimed || opened.ClaimsRemaining != 2 {
		t.Fatalf("expected an unclaimed recipient view, got %#v", opened)
	}

	claim := claimTransferShare(t, recipient, token, "", "op-status-share")

	// The open connection carries the credential it was opened with, so it
	// reports the claim count moving but not a claim this connection predates.
	claimed := recipientStream.nextTransferStatus()
	if claimed.ClaimedCount != 1 || claimed.ClaimsRemaining != 1 || claimed.Claimed {
		t.Fatalf("expected the claim count to move on the open connection, got %#v", claimed)
	}
	// The sender's page learns about the claim from the same channel.
	senderUpdate := senderStream.nextTransferStatus()
	if senderUpdate.ClaimedCount != 1 || senderUpdate.ClaimsRemaining != 1 {
		t.Fatalf("expected the sender to see the claim, got %#v", senderUpdate)
	}

	// Reconnecting is what makes the recipient's own claim visible to the
	// channel, and it re-reads the authoritative snapshot while spending
	// nothing.
	recipientStream.close()
	reconnected := openStatusStream(t, server, "/api/v1/shares/"+token+"/events", recipient.cookies)
	afterReconnect := reconnected.nextTransferStatus()
	if afterReconnect.ClaimedCount != 1 || !afterReconnect.Claimed || afterReconnect.ClaimID != claim.Claim.ID {
		t.Fatalf("expected the reconnect to report the stored claim, got %#v", afterReconnect)
	}
	stored, err := service.GetTransferWithContext(context.Background(), owner.userID(), published.ID)
	if err != nil {
		t.Fatalf("reload transfer: %v", err)
	}
	if stored.ClaimedCount != 1 {
		t.Fatalf("expected one claim to be spent in total, got %d", stored.ClaimedCount)
	}
}

// TestShareStatusStreamReportsDestructionAcrossDevices is the joint acceptance
// with the burn-after-reading policy: the sender's success page learns that the
// send ended, and why, without touching the send itself.
func TestShareStatusStreamReportsDestructionAcrossDevices(t *testing.T) {
	service, _, server := statusStreamServer(t)

	owner := newHTTPTestClient(t, server.Config.Handler)
	registerHTTPUser(t, owner, "status-burn@example.com", "Owner")
	published := publishFileTransferWithQuota(t, service, owner, "status-burn", 1, `"burnAfterReading":true,`)
	token := published.Share.Token

	stream := openStatusStream(t, server, "/api/v1/shares/"+token+"/events", owner.cookies)
	if live := stream.nextTransferStatus(); live.State != app.TransferStatusStateClaimable || !live.BurnAfterReading {
		t.Fatalf("expected a live burn send, got %#v", live)
	}

	recipient := newHTTPTestClient(t, server.Config.Handler)
	claim := openAndClaimShare(t, recipient, token, "", "op-status-burn")
	complete := recipient.json(http.MethodPost, "/api/v1/shares/"+token+"/claims/"+claim.Claim.ID+"/complete", "")
	assertStatus(t, complete, http.StatusOK)

	destroyed := stream.nextTransferStatus()
	if destroyed.State != app.TransferStatusStateDestroyed {
		t.Fatalf("expected the terminal state to reach the sender, got %#v", destroyed)
	}
	if destroyed.DestroyReason != app.TransferDestroyReasonClaimsEnded || destroyed.DestroyedAt == nil {
		t.Fatalf("expected the reason and time the burn recorded, got %#v", destroyed)
	}
	if destroyed.CleanupStatus != "pending_delete" {
		t.Fatalf("expected the cleanup boundary, got %q", destroyed.CleanupStatus)
	}
}

// TestShareStatusStreamRefusesAForeignGrant proves the page grant is scoped to
// the share it was issued for: a real cookie jar does not present it to another
// share, so one send's page cannot watch a different send.
func TestShareStatusStreamRefusesAForeignGrant(t *testing.T) {
	service, _, server := statusStreamServer(t)

	owner := newHTTPTestClient(t, server.Config.Handler)
	registerHTTPUser(t, owner, "status-grant@example.com", "Owner")
	other := publishFileTransferWithQuota(t, service, owner, "status-grant-other", 1, "")

	// Publish is what hands the sender its page grant; the test keeps the
	// response so it can check the cookie the browser would store.
	transfer := createTransfer(t, owner, `{"idempotencyKey":"status-grant-publish","expiresInSeconds":3600,"claimQuota":1,"items":[{"itemId":"itm-1","fileName":"one.txt","contentType":"text/plain","size":3}]}`)
	uploaded := uploadTransferItem(t, owner, transfer.ID, "itm-1", "one.txt", []byte("one"))
	scanTransferItemClean(t, service, uploaded.Attachment.ID)
	published := owner.json(http.MethodPost, "/api/v1/transfers/"+transfer.ID+"/publish", "")
	assertStatus(t, published, http.StatusOK)
	var body transferResponse
	decodeResponse(t, published, &body)
	grant := cookieFromResponse(t, published, shareAccessCookieName)
	if body.Transfer.Share == nil || body.Transfer.Share.Token == other.Share.Token {
		t.Fatalf("the test needs two different shares, got %#v", body.Transfer.Share)
	}
	if grant.Value == "" || !grant.HttpOnly {
		t.Fatalf("expected publish to hand the sender a page grant, got %#v", grant)
	}
	if want := shareAccessCookiePath(body.Transfer.Share.Token); grant.Path != want {
		t.Fatalf("expected the grant to be scoped to %q, got %q", want, grant.Path)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("build cookie jar: %v", err)
	}
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	jar.SetCookies(base, []*http.Cookie{grant})
	browser := &http.Client{Jar: jar}

	// The grant authorizes the share it belongs to.
	own, err := browser.Get(server.URL + "/api/v1/shares/" + body.Transfer.Share.Token + "/events")
	if err != nil {
		t.Fatalf("open own share status: %v", err)
	}
	if own.StatusCode != http.StatusOK {
		_ = own.Body.Close()
		t.Fatalf("expected the grant to authorize its own share, got %d", own.StatusCode)
	}
	_ = own.Body.Close()

	// It is never presented to another share, so the other send stays closed.
	foreign, err := browser.Get(server.URL + "/api/v1/shares/" + other.Share.Token + "/events")
	if err != nil {
		t.Fatalf("open foreign share status: %v", err)
	}
	defer foreign.Body.Close()
	if foreign.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected another share to refuse the grant, got %d", foreign.StatusCode)
	}
}

// TestStatusStreamReconnectReadsTheAuthoritativeSnapshot is the recovery rule:
// a device that was disconnected while the send moved re-reads the current
// state on reconnect instead of replaying events it may have missed.
func TestStatusStreamReconnectReadsTheAuthoritativeSnapshot(t *testing.T) {
	service, _, server := statusStreamServer(t)

	owner := newHTTPTestClient(t, server.Config.Handler)
	registerHTTPUser(t, owner, "status-reconnect@example.com", "Owner")
	published := publishFileTransferWithQuota(t, service, owner, "status-reconnect", 2, "")
	token := published.Share.Token

	stream := openStatusStream(t, server, "/api/v1/shares/"+token+"/events", owner.cookies)
	if first := stream.nextTransferStatus(); first.ClaimedCount != 0 {
		t.Fatalf("expected an unclaimed send, got %#v", first)
	}
	stream.close()

	// The send moves while the device is away.
	recipient := newHTTPTestClient(t, server.Config.Handler)
	openAndClaimShare(t, recipient, token, "", "op-status-reconnect")

	reconnected := openStatusStream(t, server, "/api/v1/shares/"+token+"/events", owner.cookies)
	after := reconnected.nextTransferStatus()
	if after.ClaimedCount != 1 || after.ClaimsRemaining != 1 {
		t.Fatalf("expected the reconnect to read the current state, got %#v", after)
	}
}

// TestStatusStreamEndsWhenTheServerShutsDown pins the shutdown path: an open
// subscription must not hold the process open until its shutdown timeout, and
// the page that was watching reconnects on its own.
func TestStatusStreamEndsWhenTheServerShutsDown(t *testing.T) {
	service, handler := newTransferTestServer(t)
	handler.statusSyncInterval = statusStreamInterval
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	owner := newHTTPTestClient(t, server.Config.Handler)
	registerHTTPUser(t, owner, "status-shutdown@example.com", "Owner")
	publishFileTransferWithQuota(t, service, owner, "status-shutdown", 1, "")

	stream := openStatusStream(t, server, "/api/v1/me/events", owner.cookies)
	stream.nextStatus()

	handler.ShutdownStatusStreams()

	select {
	case _, ok := <-stream.events:
		if ok {
			t.Fatal("expected the subscription to end with the server")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription outlived the server shutdown")
	}
}

// TestStatusStreamEndsWhenItsCredentialStopsAuthorizing pins the ending the
// server chooses over a stream that looks like a network failure: once the
// account a subscription belongs to may no longer read its own state, the
// stream says so and stops instead of retrying a refusal forever.
func TestStatusStreamEndsWhenItsCredentialStopsAuthorizing(t *testing.T) {
	service, _, server := statusStreamServer(t)

	admin, err := service.SeedAdmin(fmt.Sprintf("status-admin-%d@example.com", time.Now().UnixNano()), "password123")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	owner := newHTTPTestClient(t, server.Config.Handler)
	registerHTTPUser(t, owner, "status-frozen@example.com", "Owner")

	stream := openStatusStream(t, server, "/api/v1/me/events", owner.cookies)
	stream.nextStatus()

	if _, err := service.AdminFreezeUser(admin.ID, owner.userID(), true); err != nil {
		t.Fatalf("freeze the account: %v", err)
	}

	closed := stream.nextNamedEvent()
	if closed.Name != "closed" || !strings.Contains(closed.Data, "unauthorized") {
		t.Fatalf("expected the stream to end with its reason, got %#v", closed)
	}
	select {
	case event, ok := <-stream.events:
		if ok {
			t.Fatalf("expected the subscription to end, got %#v", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription outlived its credential")
	}
}

// TestStatusStreamBoundsOpenSubscriptions pins the connection bound: one scope
// cannot open unbounded subscriptions, and the bound is per credential rather
// than a process-wide refusal that would let one page lock everybody out.
func TestStatusStreamBoundsOpenSubscriptions(t *testing.T) {
	_, _, server := statusStreamServer(t)

	owner := newHTTPTestClient(t, server.Config.Handler)
	registerHTTPUser(t, owner, "status-bound@example.com", "Owner")

	for i := 0; i < maxStatusStreamsPerScope; i++ {
		stream := openStatusStream(t, server, "/api/v1/me/events", owner.cookies)
		stream.nextStatus()
	}

	// The refusal arrives as an event, because the browser cannot read the
	// status code of an event stream that failed.
	limited := openStatusStream(t, server, "/api/v1/me/events", owner.cookies)
	closed := limited.nextNamedEvent()
	if closed.Name != "closed" || !strings.Contains(closed.Data, "stream_limit") {
		t.Fatalf("expected the connection bound to be reported, got %#v", closed)
	}

	// Another account is unaffected by the first one's open subscriptions.
	other := newHTTPTestClient(t, server.Config.Handler)
	registerHTTPUser(t, other, "status-bound-other@example.com", "Other")
	allowed := openStatusStream(t, server, "/api/v1/me/events", other.cookies)
	allowed.nextStatus()
}

// TestStatusStreamReportsUnchangedStateOnlyOnce keeps the channel from becoming
// a poll with extra steps: a tick that finds no change sends nothing, so a page
// that is idle costs queries but no bandwidth.
func TestStatusStreamReportsUnchangedStateOnlyOnce(t *testing.T) {
	service, _, server := statusStreamServer(t)

	owner := newHTTPTestClient(t, server.Config.Handler)
	registerHTTPUser(t, owner, "status-quiet@example.com", "Owner")
	published := publishFileTransferWithQuota(t, service, owner, "status-quiet", 1, "")

	stream := openStatusStream(t, server, "/api/v1/shares/"+published.Share.Token+"/events", owner.cookies)
	if first := stream.nextTransferStatus(); first.TransferID != published.ID {
		t.Fatalf("expected the send, got %#v", first)
	}
	// Several intervals pass with nothing changing, and no event arrives.
	select {
	case event := <-stream.events:
		t.Fatalf("expected no event while the send is unchanged, got %#v", event)
	case <-time.After(10 * statusStreamInterval):
	}
}

// userID returns the signed-in account of a test client, which is what the
// service-side assertions need.
func (c *httpTestClient) userID() string {
	c.t.Helper()
	res := c.json(http.MethodGet, "/api/v1/me", "")
	assertStatus(c.t, res, http.StatusOK)
	var user app.UserView
	decodeResponse(c.t, res, &user)
	if user.ID == "" {
		c.t.Fatal("expected a signed-in account")
	}
	return user.ID
}
