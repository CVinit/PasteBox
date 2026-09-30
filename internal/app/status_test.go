package app

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// statusViewFor is the credential-scoped status of a published send as a page
// that already holds the share's grant would read it.
func statusViewFor(t *testing.T, svc *Service, token string, claimToken string, accessGrant bool) TransferStatusView {
	t.Helper()
	view, err := svc.ShareStatusWithContext(context.Background(), token, claimToken, accessGrant)
	if err != nil {
		t.Fatalf("share status: %v", err)
	}
	return view
}

// findTransferRecord returns the snapshot row of one send. The snapshot is
// ordered newest first, so a test that asserts on several sends looks them up
// by identity instead of by position.
func findTransferRecord(t *testing.T, status AccountStatusView, transferID string) TransferRecordView {
	t.Helper()
	for _, record := range status.Transfers {
		if record.TransferID == transferID {
			return record
		}
	}
	t.Fatalf("send %s is missing from the snapshot %#v", transferID, status.Transfers)
	return TransferRecordView{}
}

// TestAccountStatusReportsSendsAndRecordMarkers pins the account snapshot to
// the sender's own rows: one entry per send with the claim counts the sender
// chose, and one change marker per record. It carries no content, so a
// subscription cannot become a way to read files.
func TestAccountStatusReportsSendsAndRecordMarkers(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "status-snapshot@example.com")

	published, _ := publishBurnTestTransfer(t, svc, user.User.ID, "status-snapshot", 2, false, 3600)

	status, err := svc.AccountStatusWithContext(context.Background(), user.User.ID)
	if err != nil {
		t.Fatalf("account status: %v", err)
	}
	if len(status.Transfers) != 1 {
		t.Fatalf("expected one send in the snapshot, got %#v", status.Transfers)
	}
	record := status.Transfers[0]
	if record.TransferID != published.ID {
		t.Fatalf("expected the published send, got %#v", record)
	}
	if record.State != TransferStatusStateClaimable || record.ClaimQuota != 2 || record.ClaimedCount != 0 || record.ClaimsRemaining != 2 {
		t.Fatalf("unexpected send state: %#v", record)
	}
	if record.ItemCount != 1 || record.Title == "" {
		t.Fatalf("expected the declared file and the record title, got %#v", record)
	}
	if record.ShareToken != published.Share.Token || record.PickupCode == "" || record.ShareURL == "" {
		t.Fatalf("expected the sender to see the link and pickup code, got %#v", record)
	}
	if record.CleanupStatus != "active" {
		t.Fatalf("expected an active backing record, got %q", record.CleanupStatus)
	}
	if len(status.Pastes) != 1 || status.Pastes[0].ID != published.PasteID || status.Pastes[0].UpdatedAt.IsZero() {
		t.Fatalf("expected one record marker, got %#v", status.Pastes)
	}

	// A snapshot is scoped to its account: another user's send is not in it.
	other := registerTestUser(t, svc, "status-snapshot-other@example.com")
	otherStatus, err := svc.AccountStatusWithContext(context.Background(), other.User.ID)
	if err != nil {
		t.Fatalf("other account status: %v", err)
	}
	if len(otherStatus.Transfers) != 0 || len(otherStatus.Pastes) != 0 {
		t.Fatalf("expected an unrelated account to see nothing, got %#v", otherStatus)
	}
}

// TestAccountStatusFollowsClaimsRevocationAndDestruction covers the states a
// records list has to show: a claim moves the count, revoking the link ends it
// without destroying the content, and a burn send ends in its terminal state
// with the reason the burn policy recorded.
func TestAccountStatusFollowsClaimsRevocationAndDestruction(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "status-states@example.com")

	claimed, _ := publishBurnTestTransfer(t, svc, user.User.ID, "status-claimed", 1, false, 3600)
	claimBurnTestTransfer(t, svc, claimed.Share.Token, "op-status-claimed")

	status, err := svc.AccountStatusWithContext(context.Background(), user.User.ID)
	if err != nil {
		t.Fatalf("account status: %v", err)
	}
	claimedRecord := findTransferRecord(t, status, claimed.ID)
	if claimedRecord.ClaimedCount != 1 || claimedRecord.ClaimsRemaining != 0 {
		t.Fatalf("expected the claim to move the count, got %#v", claimedRecord)
	}
	if claimedRecord.State != TransferStatusStateExhausted {
		t.Fatalf("expected an exhausted send, got %#v", claimedRecord)
	}

	// Revoking ends the link but is not destruction: the send keeps its content
	// and reports the revoked state.
	if err := svc.RevokeShareWithContext(context.Background(), user.User.ID, claimed.Share.ID); err != nil {
		t.Fatalf("revoke share: %v", err)
	}
	status, err = svc.AccountStatusWithContext(context.Background(), user.User.ID)
	if err != nil {
		t.Fatalf("account status after revoke: %v", err)
	}
	revokedRecord := findTransferRecord(t, status, claimed.ID)
	if revokedRecord.State != TransferStatusStateRevoked {
		t.Fatalf("expected a revoked send, got %#v", revokedRecord)
	}
	if revokedRecord.DestroyedAt != nil {
		t.Fatalf("expected revoking not to destroy the send, got %#v", revokedRecord)
	}

	// A burn send ends with its last session and reports the reason.
	burning, _ := publishBurnTestTransfer(t, svc, user.User.ID, "status-burning", 1, true, 3600)
	claim := claimBurnTestTransfer(t, svc, burning.Share.Token, "op-status-burning")
	completeBurnTestClaim(t, svc, burning.Share.Token, claim)

	status, err = svc.AccountStatusWithContext(context.Background(), user.User.ID)
	if err != nil {
		t.Fatalf("account status after burn: %v", err)
	}
	record := findTransferRecord(t, status, burning.ID)
	if record.State != TransferStatusStateDestroyed || record.DestroyReason != TransferDestroyReasonClaimsEnded {
		t.Fatalf("expected a destroyed send with its reason, got %#v", record)
	}
	if record.CleanupStatus != "pending_delete" {
		t.Fatalf("expected the backing record to be queued for cleanup, got %q", record.CleanupStatus)
	}

	// The lifetime ending is reported as expired, not as destroyed.
	expiring, _ := publishBurnTestTransfer(t, svc, user.User.ID, "status-expiring", 1, false, 60)
	now = now.Add(2 * time.Minute)
	status, err = svc.AccountStatusWithContext(context.Background(), user.User.ID)
	if err != nil {
		t.Fatalf("account status after expiry: %v", err)
	}
	expiredRecord := findTransferRecord(t, status, expiring.ID)
	if expiredRecord.State != TransferStatusStateExpired {
		t.Fatalf("expected the send past its lifetime to be expired, got %#v", expiredRecord)
	}
}

// TestShareStatusReadNeverSpendsAClaim is the subscription guarantee: reading
// status — including every reconnect — reports the count without moving it, so
// watching a send can never consume the allowance a recipient still needs.
func TestShareStatusReadNeverSpendsAClaim(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "status-nonconsuming@example.com")
	published, _ := publishBurnTestTransfer(t, svc, user.User.ID, "status-nonconsuming", 2, false, 3600)

	share := svc.sharesByID[published.Share.ID]
	visitsBefore := share.VisitCount

	for attempt := 0; attempt < 5; attempt++ {
		view := statusViewFor(t, svc, published.Share.Token, "", true)
		if view.State != TransferStatusStateClaimable || view.ClaimsRemaining != 2 || view.ClaimedCount != 0 {
			t.Fatalf("expected the count to stay untouched, got %#v", view)
		}
	}
	if share.VisitCount != visitsBefore {
		t.Fatalf("expected status reads not to consume visits, got %d", share.VisitCount)
	}
	stored, err := svc.transferByIDLocked(context.Background(), published.ID)
	if err != nil {
		t.Fatalf("reload transfer: %v", err)
	}
	if stored.ClaimedCount != 0 {
		t.Fatalf("expected no claim to be spent, got %d", stored.ClaimedCount)
	}

	// A claimed session is reported as claimed, and reading it again is still
	// free.
	claim := claimBurnTestTransfer(t, svc, published.Share.Token, "op-status-nonconsuming")
	view := statusViewFor(t, svc, published.Share.Token, claim.ClaimToken, true)
	if !view.Claimed || view.ClaimID != claim.Claim.ID || view.ClaimsRemaining != 1 {
		t.Fatalf("expected the live claim to be reported, got %#v", view)
	}
	again := statusViewFor(t, svc, published.Share.Token, claim.ClaimToken, true)
	if again.ClaimedCount != 1 {
		t.Fatalf("expected the claimed session to stay free to read, got %#v", again)
	}
}

// TestShareStatusRequiresAPageGrantOrALiveClaim pins the access rule: status
// reads are limited to a caller that already holds a credential for that share,
// so the channel is not a way to discover shares or to watch somebody else's
// send.
func TestShareStatusRequiresAPageGrantOrALiveClaim(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "status-auth@example.com")
	published, _ := publishBurnTestTransfer(t, svc, user.User.ID, "status-auth", 2, false, 3600)
	other, _ := publishBurnTestTransfer(t, svc, user.User.ID, "status-auth-other", 2, false, 3600)

	if _, err := svc.ShareStatusWithContext(context.Background(), published.Share.Token, "", false); !isAppStatus(err, http.StatusUnauthorized) {
		t.Fatalf("expected a status read without a credential to be refused, got %v", err)
	}

	// A claim belongs to the share it was made against, so it never authorizes
	// another send's status.
	claim := claimBurnTestTransfer(t, svc, other.Share.Token, "op-status-auth-other")
	if _, err := svc.ShareStatusWithContext(context.Background(), published.Share.Token, claim.ClaimToken, false); !isAppStatus(err, http.StatusUnauthorized) {
		t.Fatalf("expected a foreign claim to be refused, got %v", err)
	}
	// The same claim is accepted for its own share.
	if _, err := svc.ShareStatusWithContext(context.Background(), other.Share.Token, claim.ClaimToken, false); err != nil {
		t.Fatalf("expected the claim to authorize its own share, got %v", err)
	}

	// A claim that has ended is no longer a credential.
	completeBurnTestClaim(t, svc, other.Share.Token, claim)
	if _, err := svc.ShareStatusWithContext(context.Background(), other.Share.Token, claim.ClaimToken, false); !isAppStatus(err, http.StatusUnauthorized) {
		t.Fatalf("expected an ended claim to be refused, got %v", err)
	}

	// A legacy share has no send state to report.
	legacyPaste, err := svc.CreatePasteWithContext(context.Background(), user.User.ID, PasteInput{
		Title:            "legacy",
		Text:             "body",
		ExpiresInSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("create legacy record: %v", err)
	}
	legacy := createTestShare(t, svc, user.User.ID, legacyPaste.ID, ShareInput{ExpiresInSeconds: 3600})
	if _, err := svc.ShareStatusWithContext(context.Background(), legacy.Token, "", true); !isAppStatus(err, http.StatusBadRequest) {
		t.Fatalf("expected a legacy share to report no send status, got %v", err)
	}
}

// TestShareStatusReportsDestroyedInsteadOfRefusingIt is the terminal-state
// contract with the burn policy: a page that is already open is told the send
// ended and why, instead of being handed an error it cannot render.
func TestShareStatusReportsDestroyedInsteadOfRefusingIt(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "status-destroyed@example.com")
	published, _ := publishBurnTestTransfer(t, svc, user.User.ID, "status-destroyed", 1, true, 3600)

	live := statusViewFor(t, svc, published.Share.Token, "", true)
	if live.State != TransferStatusStateClaimable || !live.BurnAfterReading {
		t.Fatalf("expected a live burn send, got %#v", live)
	}

	claim := claimBurnTestTransfer(t, svc, published.Share.Token, "op-status-destroyed")
	completeBurnTestClaim(t, svc, published.Share.Token, claim)

	destroyed := statusViewFor(t, svc, published.Share.Token, "", true)
	if destroyed.State != TransferStatusStateDestroyed {
		t.Fatalf("expected the terminal state, got %#v", destroyed)
	}
	if destroyed.DestroyReason != TransferDestroyReasonClaimsEnded || destroyed.DestroyedAt == nil {
		t.Fatalf("expected the reason and time the burn recorded, got %#v", destroyed)
	}
	if destroyed.CleanupStatus != "pending_delete" {
		t.Fatalf("expected the cleanup boundary, got %q", destroyed.CleanupStatus)
	}

	// The same terminal state is what the sender's records list reports, so the
	// two sides cannot disagree about what ended the send.
	status, err := svc.AccountStatusWithContext(context.Background(), user.User.ID)
	if err != nil {
		t.Fatalf("account status: %v", err)
	}
	record := findTransferRecord(t, status, published.ID)
	if record.State != destroyed.State || record.DestroyReason != destroyed.DestroyReason {
		t.Fatalf("expected both sides to share the terminal state, got %#v and %#v", record, destroyed)
	}
}

// TestShareStatusExpiryIsReportedBeforeAnythingIsRefused keeps the expiry
// boundary honest: a page watching a send past its lifetime is told it expired
// rather than being left to guess from a failed request.
func TestShareStatusExpiryIsReportedBeforeAnythingIsRefused(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "status-expiry@example.com")
	published, _ := publishBurnTestTransfer(t, svc, user.User.ID, "status-expiry", 1, false, 60)

	now = now.Add(2 * time.Minute)
	view := statusViewFor(t, svc, published.Share.Token, "", true)
	if view.State != TransferStatusStateExpired {
		t.Fatalf("expected the expired state, got %#v", view)
	}
	if view.DestroyedAt != nil {
		t.Fatalf("expected an ordinary send to expire without a terminal destroy state, got %#v", view)
	}
}
