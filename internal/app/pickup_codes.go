package app

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	// PickupCodeLength is the number of characters a recipient types.
	PickupCodeLength = 6
	// PickupCodeAlphabet excludes the characters people mix up when a code is
	// read aloud or retyped: 0/O, 1/I and L.
	PickupCodeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	// pickupCodeByteLimit is the largest random byte that maps onto the alphabet
	// without favouring its head; larger bytes are rejected and redrawn.
	pickupCodeByteLimit = byte(256 - (256 % len(PickupCodeAlphabet)))
	// maxPickupCodeAttempts bounds how often publishing retries allocation
	// after a uniqueness conflict before giving up.
	maxPickupCodeAttempts = 8
	// pickupAttemptLimit bounds well-formed pickup-code guesses per client and
	// window. It is a fixed security budget rather than a product knob: the
	// window follows the runtime rate-limit window, the limit does not.
	pickupAttemptLimit = 10
	// pickupAttemptWindow applies when runtime rate limits carry no window.
	pickupAttemptWindow = time.Minute
	// memoryPickupAttemptPruneThreshold keeps the process-local fallback
	// counter from growing with every client that ever guessed.
	memoryPickupAttemptPruneThreshold = 1024
)

// ErrSharePickupCodeExists reports that a freshly generated pickup code is
// already taken. It also satisfies ErrStoreConflict, so callers that only care
// about the conflict still match it.
var ErrSharePickupCodeExists = errors.Join(errors.New("share pickup code already exists"), ErrStoreConflict)

// PickupResolution is what a pickup-code lookup hands back: the share token the
// recipient then opens exactly like a shared link, so password, login and
// expiry rules stay on the single existing access path.
type PickupResolution struct {
	Token string `json:"token"`
	URL   string `json:"url"`
	// ShareID identifies the resolved share for logging. It is not part of the
	// response: the recipient already holds the token.
	ShareID string `json:"-"`
}

// newPickupCode draws a code from the crypto random source. Bytes outside the
// largest multiple of the alphabet size are rejected, so every character is
// equally likely instead of favouring the head of the alphabet.
func newPickupCode() (string, error) {
	code := make([]byte, 0, PickupCodeLength)
	buffer := make([]byte, 32)
	for len(code) < PickupCodeLength {
		if _, err := rand.Read(buffer); err != nil {
			return "", err
		}
		for _, value := range buffer {
			if value >= pickupCodeByteLimit {
				continue
			}
			code = append(code, PickupCodeAlphabet[int(value)%len(PickupCodeAlphabet)])
			if len(code) == PickupCodeLength {
				break
			}
		}
	}
	return string(code), nil
}

// normalizePickupCode accepts what people actually type: mixed case, stray
// spaces, and hyphen-separated groups.
func normalizePickupCode(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	cleaned := strings.Map(func(char rune) rune {
		switch char {
		case ' ', '\t', '\n', '\r', '-', '_':
			return -1
		}
		return char
	}, trimmed)
	return strings.ToUpper(cleaned)
}

func validPickupCode(code string) bool {
	if len(code) != PickupCodeLength {
		return false
	}
	for _, char := range code {
		if !strings.ContainsRune(PickupCodeAlphabet, char) {
			return false
		}
	}
	return true
}

// pickupNotFoundError is the single answer for unknown, expired and revoked
// codes. Telling them apart would let a guesser learn which codes exist.
func pickupNotFoundError() *Error {
	return E(http.StatusNotFound, "pickup_not_found", "pickup code is invalid or expired")
}

// ResolvePickupCodeWithContext turns a 6-character pickup code into the share
// token it belongs to. The lookup never grants sender controls, and it does not
// open the share: the recipient still passes the normal share access rules.
func (s *Service) ResolvePickupCodeWithContext(ctx context.Context, code string, clientKey string) (PickupResolution, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized := normalizePickupCode(code)
	if !validPickupCode(normalized) {
		return PickupResolution{}, pickupNotFoundError()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	window := s.pickupAttemptWindowLocked()
	now := s.now().UTC()
	key := "pickup:" + clientKey
	if err := s.checkPickupBudgetLocked(ctx, key, window, now); err != nil {
		return PickupResolution{}, err
	}
	resolution, err := s.resolvePickupLocked(ctx, normalized)
	if err != nil {
		// Only a failed guess spends the budget. A working code must not use up
		// the allowance of everyone else behind the same address.
		if isAppStatus(err, http.StatusNotFound) {
			if recordErr := s.recordPickupFailureLocked(ctx, key, window, now); recordErr != nil {
				return PickupResolution{}, recordErr
			}
		}
		return PickupResolution{}, err
	}
	return resolution, nil
}

// resolvePickupLocked maps a well-formed code onto its share. Every failure it
// returns is the same not-found error, so a guesser cannot tell which codes
// exist, have expired, or were revoked.
func (s *Service) resolvePickupLocked(ctx context.Context, code string) (PickupResolution, error) {
	share, err := s.shareByPickupCodeLocked(ctx, code)
	if err != nil {
		return PickupResolution{}, err
	}
	now := s.now().UTC()
	if share.RevokedAt != nil || !share.ExpiresAt.After(now) {
		return PickupResolution{}, pickupNotFoundError()
	}
	paste, err := s.pasteByIDLocked(ctx, share.PasteID)
	if err != nil {
		if isAppStatus(err, http.StatusNotFound) {
			return PickupResolution{}, pickupNotFoundError()
		}
		return PickupResolution{}, err
	}
	if !s.isPasteVisibleLocked(paste) {
		return PickupResolution{}, pickupNotFoundError()
	}
	return PickupResolution{Token: share.Token, URL: s.shareURLLocked(share.Token), ShareID: share.ID}, nil
}

func (s *Service) shareByPickupCodeLocked(ctx context.Context, code string) (*Share, error) {
	if s.content.Shares != nil {
		lookup, ok := s.content.Shares.(PickupCodeShareStore)
		if !ok {
			return nil, pickupNotFoundError()
		}
		s.mu.Unlock()
		loaded, err := lookup.ShareByPickupCode(ctx, code)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, pickupNotFoundError()
			}
			return nil, err
		}
		return s.cacheShareLocked(loaded), nil
	}
	share := s.sharesByID[s.shareIDByPickupCode[code]]
	if share == nil {
		return nil, pickupNotFoundError()
	}
	return share, nil
}

func (s *Service) pickupAttemptWindowLocked() time.Duration {
	window := time.Duration(s.runtimeConfig.RateLimits.WindowSeconds) * time.Second
	if window <= 0 {
		window = pickupAttemptWindow
	}
	return window
}

// checkPickupBudgetLocked refuses the lookup once a client has spent its guess
// budget, so a spent key can neither keep probing nor learn that a code was
// right. Counting is the store's job, deciding is this layer's: the budget
// stays here so no store can silently widen how many guesses a client gets.
func (s *Service) checkPickupBudgetLocked(ctx context.Context, key string, window time.Duration, now time.Time) error {
	attempt, err := s.pickupAttemptStateLocked(ctx, key, window, now)
	if err != nil {
		return err
	}
	if attempt.Count < pickupAttemptLimit {
		return nil
	}
	return pickupRateLimitedError(attempt.Start.Add(window).Sub(now))
}

func (s *Service) pickupAttemptStateLocked(ctx context.Context, key string, window time.Duration, now time.Time) (PickupAttemptWindow, error) {
	if s.content.PickupAttempts == nil {
		return memoryPickupAttemptState(s.pickupAttempts, key, window, now), nil
	}
	s.mu.Unlock()
	attempt, err := s.content.PickupAttempts.PickupAttemptCount(ctx, key, window, now)
	s.mu.Lock()
	if err != nil {
		return PickupAttemptWindow{}, err
	}
	return attempt, nil
}

func (s *Service) recordPickupFailureLocked(ctx context.Context, key string, window time.Duration, now time.Time) error {
	if s.content.PickupAttempts == nil {
		recordMemoryPickupFailure(s.pickupAttempts, key, window, now)
		return nil
	}
	s.mu.Unlock()
	_, err := s.content.PickupAttempts.RecordPickupFailure(ctx, key, window, now)
	s.mu.Lock()
	return err
}

func pickupRateLimitedError(remaining time.Duration) *Error {
	err := E(http.StatusTooManyRequests, "pickup_rate_limited", "too many pickup code attempts")
	err.RetryAfterSeconds = pickupRetryAfterSeconds(remaining)
	return err
}

func pickupRetryAfterSeconds(remaining time.Duration) int {
	if remaining <= 0 {
		return 1
	}
	return max(int((remaining+time.Second-1)/time.Second), 1)
}

// memoryPickupAttempt is the process-local fallback used when no durable store
// is configured. Production wiring passes the PostgreSQL store so several API
// instances share one budget.
type memoryPickupAttempt struct {
	windowStart time.Time
	count       int
}

func memoryPickupAttemptState(attempts map[string]memoryPickupAttempt, key string, window time.Duration, now time.Time) PickupAttemptWindow {
	attempt := attempts[key]
	if attempt.windowStart.IsZero() || !attempt.windowStart.Add(window).After(now) {
		return PickupAttemptWindow{Start: now}
	}
	return PickupAttemptWindow{Start: attempt.windowStart, Count: attempt.count}
}

func recordMemoryPickupFailure(attempts map[string]memoryPickupAttempt, key string, window time.Duration, now time.Time) {
	if len(attempts) >= memoryPickupAttemptPruneThreshold {
		for entry, attempt := range attempts {
			if !attempt.windowStart.Add(window).After(now) {
				delete(attempts, entry)
			}
		}
	}
	attempt := attempts[key]
	if attempt.windowStart.IsZero() || !attempt.windowStart.Add(window).After(now) {
		attempt = memoryPickupAttempt{windowStart: now}
	}
	attempt.count++
	attempts[key] = attempt
}
