package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNewPickupCodeUsesUnambiguousAlphabet(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 512; i++ {
		code, err := newPickupCode()
		if err != nil {
			t.Fatalf("generate pickup code: %v", err)
		}
		if len(code) != PickupCodeLength {
			t.Fatalf("expected %d characters, got %q", PickupCodeLength, code)
		}
		for _, char := range code {
			if !strings.ContainsRune(PickupCodeAlphabet, char) {
				t.Fatalf("code %q uses character %q outside the pickup alphabet", code, char)
			}
		}
		seen[code] = true
	}
	if len(seen) < 500 {
		t.Fatalf("expected mostly distinct codes, got %d unique codes out of 512", len(seen))
	}
	for _, confusable := range []rune{'0', 'O', '1', 'I', 'L'} {
		if strings.ContainsRune(PickupCodeAlphabet, confusable) {
			t.Fatalf("pickup alphabet must not contain confusable character %q", confusable)
		}
	}
}

func TestNormalizePickupCodeAcceptsLowercaseAndSeparators(t *testing.T) {
	cases := map[string]string{
		"abc234":   "ABC234",
		" abc234 ": "ABC234",
		"ABC-234":  "ABC234",
		"ab c 234": "ABC234",
		"a-b-c-2":  "ABC2",
		"":         "",
	}
	for input, want := range cases {
		if got := normalizePickupCode(input); got != want {
			t.Fatalf("normalizePickupCode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestValidPickupCodeRequiresLengthAndAlphabet(t *testing.T) {
	valid := []string{"ABC234", "234567", "ZYXWVU"}
	for _, code := range valid {
		if !validPickupCode(code) {
			t.Fatalf("expected %q to be a valid pickup code", code)
		}
	}
	invalid := []string{"", "ABC23", "ABC2345", "ABC230", "ABCO34", "ABC2 4"}
	for _, code := range invalid {
		if validPickupCode(code) {
			t.Fatalf("expected %q to be rejected", code)
		}
	}
}

// conflictingShareStore rejects the first pickup codes as already taken, so the
// publish path can be shown to retry with a fresh code instead of failing a
// send whose files already landed.
type conflictingShareStore struct {
	failures int
	attempts []string
	shares   map[string]Share
}

func (s *conflictingShareStore) CreateShare(_ context.Context, share Share) error {
	s.attempts = append(s.attempts, share.PickupCode)
	if s.failures > 0 {
		s.failures--
		return ErrSharePickupCodeExists
	}
	s.store(share)
	return nil
}

func (s *conflictingShareStore) store(share Share) {
	if s.shares == nil {
		s.shares = map[string]Share{}
	}
	s.shares[share.ID] = share
}

func (s *conflictingShareStore) ShareByID(_ context.Context, id string) (Share, error) {
	share, ok := s.shares[id]
	if !ok {
		return Share{}, ErrStoreNotFound
	}
	return share, nil
}

func (s *conflictingShareStore) ShareByTokenHash(_ context.Context, tokenHash string) (Share, error) {
	for _, share := range s.shares {
		if share.TokenHash == tokenHash {
			return share, nil
		}
	}
	return Share{}, ErrStoreNotFound
}

func (s *conflictingShareStore) ListShares(context.Context) ([]Share, error) {
	return nil, nil
}

func (s *conflictingShareStore) ListSharesByUser(context.Context, string) ([]Share, error) {
	return nil, nil
}

func (s *conflictingShareStore) UpdateShare(_ context.Context, share Share) error {
	s.store(share)
	return nil
}

func TestPublishTransferRetriesPickupCodeCollision(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	store := &conflictingShareStore{failures: 2}
	svc := newTestServiceWithStorage(t, &now, Stores{Content: ContentStores{Shares: store}})
	user := registerTestUser(t, svc, "pickup-collision@example.com")

	transfer, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, transferInput(transferItem("itm-1", "a.txt")))
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	uploadTestTransferItem(t, svc, user.User.ID, transfer.ID, "itm-1", "a.txt", []byte("collision-body"))

	published, err := svc.PublishTransferWithContext(context.Background(), user.User.ID, transfer.ID)
	if err != nil {
		t.Fatalf("publish transfer: %v", err)
	}
	if len(store.attempts) != 3 {
		t.Fatalf("expected two conflicts and one success, got %d attempts", len(store.attempts))
	}
	seen := map[string]bool{}
	for _, code := range store.attempts {
		if len(code) != PickupCodeLength {
			t.Fatalf("expected every attempt to carry a %d-character code, got %q", PickupCodeLength, code)
		}
		if seen[code] {
			t.Fatalf("expected a fresh code per attempt, got %q twice", code)
		}
		seen[code] = true
	}
	if published.Status != TransferStatusPublished || published.Share == nil {
		t.Fatalf("unexpected published transfer: %#v", published)
	}
	if published.PickupCode != store.attempts[len(store.attempts)-1] {
		t.Fatalf("expected the stored code %q, got %q", store.attempts[len(store.attempts)-1], published.PickupCode)
	}
}
