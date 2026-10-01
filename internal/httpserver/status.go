package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"pastebox/internal/app"
)

const (
	// statusStreamPingInterval keeps an idle subscription from being closed by
	// an intermediary that drops silent connections. A comment line is a valid
	// event-stream payload that carries no state, so a client never mistakes a
	// keepalive for an update.
	statusStreamPingInterval = 15 * time.Second
	// maxStatusStreamsPerScope bounds how many subscriptions one account or one
	// share may hold at once, so one page that reconnects in a loop cannot
	// consume the process.
	maxStatusStreamsPerScope = 4
	// maxStatusStreamsTotal bounds every open subscription in one process. A
	// subscription holds a goroutine and re-reads the store on every tick, so
	// the count has to be bounded independently of who is asking.
	maxStatusStreamsTotal = 256
	// statusStreamClosedLimit and statusStreamClosedUnauthorized are the reasons
	// a subscription ends by the server's decision, so the page can say what
	// happened instead of looking like it lost the network.
	statusStreamClosedLimit        = "stream_limit"
	statusStreamClosedUnauthorized = "unauthorized"
	// DefaultStatusSyncInterval is how often one open subscription re-reads the
	// authoritative store. It bounds the query cost of a subscription
	// independently of how much state the account has.
	DefaultStatusSyncInterval = 2 * time.Second
)

// statusStreamLimiter counts the subscriptions one process is serving, per
// scope and in total. It is the bound behind "连接数量有界": a subscription
// holds a goroutine for as long as the page is open, so the count cannot be
// left to the client.
type statusStreamLimiter struct {
	mu     sync.Mutex
	scopes map[string]int
	total  int
}

// acquire reserves one subscription slot. It reports false when the scope or
// the process is already at its bound.
func (l *statusStreamLimiter) acquire(scope string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.scopes == nil {
		l.scopes = map[string]int{}
	}
	if l.total >= maxStatusStreamsTotal {
		return false
	}
	if l.scopes[scope] >= maxStatusStreamsPerScope {
		return false
	}
	l.scopes[scope]++
	l.total++
	return true
}

// release gives one subscription slot back. A scope that drops to zero is
// removed, so a long-lived process does not accumulate one entry per share that
// was ever watched.
func (l *statusStreamLimiter) release(scope string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	remaining := l.scopes[scope] - 1
	if remaining <= 0 {
		delete(l.scopes, scope)
	} else {
		l.scopes[scope] = remaining
	}
	if l.total > 0 {
		l.total--
	}
}

// statusStreamScope names one subscription. Key is the credential-scoped
// identity the connection bound applies to; Kind is what may appear in a log,
// because a share token is a credential and must not be written out.
type statusStreamScope struct {
	Key  string
	Kind string
}

// statusReader returns the authoritative snapshot of one scope. The first read
// doubles as the authorization check: a subscription that may not be served is
// refused with the ordinary JSON error instead of an open stream.
type statusReader func(ctx context.Context) (any, error)

// streamAccountStatus serves the account-scoped status stream. It reports the
// signed-in account's sends and the change markers of their records, so a
// second device updates its records list, its claim counts and its editor
// without polling the content API.
func (s *Server) streamAccountStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	session, _ := r.Cookie(sessionCookieName) // requireUser already validated it.
	s.streamStatus(w, r, statusStreamScope{Key: "user:" + user.ID, Kind: "account"}, func(ctx context.Context) (any, error) {
		// A stream outlives individual requests. Recheck the original credential,
		// not just the account, so logout and expiry also end an open stream.
		current, err := s.app.UserForSessionWithContext(ctx, session.Value)
		if err != nil {
			return nil, err
		}
		if current.ID != user.ID {
			return nil, app.E(http.StatusUnauthorized, "unauthenticated", "login required")
		}
		return s.app.AccountStatusWithContext(ctx, user.ID, app.AccountStatusOptions{BeforeTransferID: r.URL.Query().Get("before"), ActivePasteID: r.URL.Query().Get("pasteId")})
	})
}

// streamShareStatus serves the credential-scoped status stream. It answers for
// the share the caller already opened, or for the share they hold a live claim
// on, and it never spends a claim slot, a visit or a download. The same channel
// serves the recipient's page and the sender's success page: publishing hands
// the sender the same page grant a recipient gets, so neither side needs a
// second protocol.
func (s *Server) streamShareStatus(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	claimToken := s.transferClaimToken(r)
	s.streamStatus(w, r, statusStreamScope{Key: "share:" + token, Kind: "share"}, func(ctx context.Context) (any, error) {
		// The page grant is re-checked on every tick, so a subscription cannot
		// outlive the grant it was opened with. The claim cookie is resolved by
		// the service, which is where claim validity lives.
		return s.app.ShareStatusWithContext(ctx, token, claimToken, s.validShareAccessCookie(r, token, s.optionalUserID(r)))
	})
}

// streamStatus runs one server-sent event subscription: an immediate
// authoritative snapshot, then a bounded re-read that only pushes when the
// snapshot changed. The store is the only source of truth, so two API instances
// report the same state and a reconnect cannot miss an event that mattered.
func (s *Server) streamStatus(w http.ResponseWriter, r *http.Request, scope statusStreamScope, read statusReader) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming_unsupported", "message": "this server cannot stream status"})
		return
	}
	if !s.statusStreams.acquire(scope.Key) {
		// The refusal travels as an event rather than as a status code, because
		// the browser cannot see the code of a failed event stream. It is a
		// capacity answer, not a lost connection, and the page says so.
		s.refuseStatusStream(w, flusher, statusStreamClosedLimit)
		return
	}
	defer s.statusStreams.release(scope.Key)

	ctx := r.Context()
	// The first read is also the authorization check, so a caller that may not
	// watch this scope gets a plain refusal rather than an open stream.
	payload, err := read(ctx)
	if err != nil {
		s.handleErr(w, err)
		return
	}
	last, err := json.Marshal(payload)
	if err != nil {
		s.handleErr(w, err)
		return
	}

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	// A buffering reverse proxy would hold every push until the next one, which
	// turns a live channel into a laggy one. The header is advisory; the client
	// still recovers on its own if a proxy ignores it.
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := writeStatusEvent(w, "status", last); err != nil {
		return
	}
	flusher.Flush()

	s.logger.Debug("status stream opened", "scope", scope.Kind)
	defer s.logger.Debug("status stream closed", "scope", scope.Kind)

	tick := time.NewTicker(s.statusSyncIntervalOrDefault())
	defer tick.Stop()
	ping := time.NewTicker(statusStreamPingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.streamShutdown:
			// The process is stopping. Ending here lets it drain instead of
			// waiting out its shutdown timeout on a subscription that would
			// otherwise live until the page closes.
			return
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-tick.C:
			payload, err := read(ctx)
			if err != nil {
				if statusStreamEnded(err) {
					// The credential lapsed or the account went away: the
					// stream says so instead of looking like a network failure.
					_ = writeStatusEvent(w, "closed", closedStatusPayload(statusStreamClosedUnauthorized))
					flusher.Flush()
					return
				}
				// A transient store failure only skips a tick, so a short
				// outage does not disconnect every open page.
				s.logger.Warn("status stream read failed", "scope", scope.Kind, "error", err)
				continue
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				s.logger.Warn("status stream encode failed", "scope", scope.Kind, "error", err)
				continue
			}
			if bytes.Equal(encoded, last) {
				continue
			}
			last = encoded
			if err := writeStatusEvent(w, "status", last); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// statusSyncIntervalOrDefault is the re-read interval this server runs with. A
// server built without one — a literal struct in a test — still gets the
// bounded production interval instead of a busy loop.
func (s *Server) statusSyncIntervalOrDefault() time.Duration {
	if s.statusSyncInterval <= 0 {
		return DefaultStatusSyncInterval
	}
	return s.statusSyncInterval
}

// writeStatusEvent writes one named event. The payload is JSON, which never
// contains a raw newline, so a single data line is always a complete event.
func writeStatusEvent(w io.Writer, event string, payload []byte) error {
	if _, err := io.WriteString(w, "event: "+event+"\ndata: "); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n\n")
	return err
}

// statusStreamEnded reports whether an error means this subscription can never
// be served again, as opposed to a transient failure worth retrying. A scope
// that has nothing to report — a share without a send behind it — is included:
// retrying it would only repeat the same answer.
func statusStreamEnded(err error) bool {
	var appErr *app.Error
	if !errors.As(err, &appErr) {
		return false
	}
	switch appErr.Status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return true
	}
	return false
}

// refuseStatusStream answers a subscription the server will not serve with the
// event a streaming client can actually read.
func (s *Server) refuseStatusStream(w http.ResponseWriter, flusher http.Flusher, reason string) {
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_ = writeStatusEvent(w, "closed", closedStatusPayload(reason))
	flusher.Flush()
}

func closedStatusPayload(reason string) []byte {
	payload, err := json.Marshal(map[string]string{"reason": reason})
	if err != nil {
		return []byte(`{"reason":"unknown"}`)
	}
	return payload
}
