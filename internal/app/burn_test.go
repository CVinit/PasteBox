package app

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// publishBurnTestTransfer creates and publishes a one-file transfer with an
// explicit claim quota and burn switch, and returns the published view plus the
// attachment id of its file.
func publishBurnTestTransfer(t *testing.T, svc *Service, userID string, key string, quota int, burn bool, expiresInSeconds int64) (TransferView, string) {
	t.Helper()
	created, err := svc.CreateTransferWithContext(context.Background(), userID, TransferInput{
		IdempotencyKey:   key,
		ExpiresInSeconds: expiresInSeconds,
		ClaimQuota:       quota,
		BurnAfterReading: burn,
		Items:            []TransferItemInput{transferItem("itm-1", "one.txt")},
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	_, attachment := uploadTestTransferItem(t, svc, userID, created.ID, "itm-1", "one.txt", []byte("one"))
	runCleanScan(t, svc, attachment.ID)
	published, err := svc.PublishTransferWithContext(context.Background(), userID, created.ID)
	if err != nil {
		t.Fatalf("publish transfer: %v", err)
	}
	if published.Share == nil || published.Share.Token == "" {
		t.Fatalf("expected a published share, got %#v", published)
	}
	return published, attachment.ID
}

func burnTestAttachment(t *testing.T, svc *Service, id string) Attachment {
	t.Helper()
	attachment := svc.attachmentsByID[id]
	if attachment == nil {
		t.Fatalf("attachment %s is missing", id)
	}
	return *attachment
}

func claimBurnTestTransfer(t *testing.T, svc *Service, token string, operationID string) TransferClaimResult {
	t.Helper()
	result, err := svc.ClaimTransferWithContext(context.Background(), token, "", "", operationID)
	if err != nil {
		t.Fatalf("claim transfer: %v", err)
	}
	return result
}

func completeBurnTestClaim(t *testing.T, svc *Service, token string, claim TransferClaimResult) {
	t.Helper()
	if _, err := svc.CompleteTransferClaimWithContext(context.Background(), token, claim.Claim.ID, claim.ClaimToken, ""); err != nil {
		t.Fatalf("complete claim: %v", err)
	}
}

func burnTestTransferView(t *testing.T, svc *Service, userID string, transferID string) TransferView {
	t.Helper()
	view, err := svc.GetTransferWithContext(context.Background(), userID, transferID)
	if err != nil {
		t.Fatalf("get transfer: %v", err)
	}
	return view
}

// TestBurnAfterReadingDefaultsOffAndNeverDestroysAnOrdinarySend pins the
// switch to off and proves an ordinary send keeps its content: the terminal
// state, the content and the cleanup queue are all untouched.
func TestBurnAfterReadingDefaultsOffAndNeverDestroysAnOrdinarySend(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "burn-off@example.com")

	published, _ := publishBurnTestTransfer(t, svc, user.User.ID, "burn-off", 1, false, 3600)
	if published.BurnAfterReading {
		t.Fatalf("expected the burn switch to default to off, got %#v", published)
	}
	claim := claimBurnTestTransfer(t, svc, published.Share.Token, "op-1")
	completeBurnTestClaim(t, svc, published.Share.Token, claim)

	// Even once every slot is spent and every session ended, an ordinary send
	// is not destroyed: it keeps its content until its own lifetime ends.
	if destroyed, err := svc.RunTransferBurnSweepWithContext(context.Background()); err != nil || destroyed != 0 {
		t.Fatalf("expected the sweep to leave an ordinary send alone, got %d %v", destroyed, err)
	}
	view := burnTestTransferView(t, svc, user.User.ID, published.ID)
	if view.Status != TransferStatusPublished || view.DestroyedAt != nil || view.CleanupStatus != "active" {
		t.Fatalf("expected an ordinary send to stay published with its content, got %#v", view)
	}
	if len(svc.cleanupJobs) != 0 {
		t.Fatalf("expected no cleanup job for an ordinary send, got %#v", svc.cleanupJobs)
	}
}

// TestBurnTransferIsDestroyedOnlyAfterEveryClaimEnds is the multi-recipient
// rule: ending one session never takes away what the remaining slots need, and
// the send is only destroyed once the last session has ended.
func TestBurnTransferIsDestroyedOnlyAfterEveryClaimEnds(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "burn-multi@example.com")

	published, attachmentID := publishBurnTestTransfer(t, svc, user.User.ID, "burn-multi", 2, true, 3600)
	if !published.BurnAfterReading {
		t.Fatalf("expected the burn switch to be stored, got %#v", published)
	}
	token := published.Share.Token

	// Opening the page is free and destroys nothing while a slot is left.
	_, _, access, err := svc.AccessShareWithTransferContext(context.Background(), token, "", "", "")
	if err != nil {
		t.Fatalf("open share: %v", err)
	}
	if access == nil || access.ClaimsRemaining != 2 || access.Claimed {
		t.Fatalf("expected an untouched claim state, got %#v", access)
	}
	if view := burnTestTransferView(t, svc, user.User.ID, published.ID); view.Status != TransferStatusPublished {
		t.Fatalf("expected opening the page to keep the send alive, got %#v", view)
	}

	// The first recipient claims, downloads and ends their session.
	first := claimBurnTestTransfer(t, svc, token, "op-1")
	if _, err := svc.OpenSharedAttachmentWithClaimOrAccessGrantContext(context.Background(), token, first.ClaimToken, false, attachmentID, ""); err != nil {
		t.Fatalf("download inside the first session: %v", err)
	}
	completeBurnTestClaim(t, svc, token, first)

	// The second slot was never claimed, so the content has to survive: a later
	// recipient still needs it.
	if view := burnTestTransferView(t, svc, user.User.ID, published.ID); view.Status != TransferStatusPublished || view.DestroyedAt != nil {
		t.Fatalf("expected the send to survive the first session, got %#v", view)
	}
	if _, _, access, err = svc.AccessShareWithTransferContext(context.Background(), token, "", "", ""); err != nil {
		t.Fatalf("open share after the first session: %v", err)
	} else if access.ClaimsRemaining != 1 {
		t.Fatalf("expected one slot to remain, got %#v", access)
	}

	// The second recipient claims, and the batch is still complete for them.
	second := claimBurnTestTransfer(t, svc, token, "op-2")
	download, err := svc.OpenSharedAttachmentWithClaimOrAccessGrantContext(context.Background(), token, second.ClaimToken, false, attachmentID, "")
	if err != nil {
		t.Fatalf("download inside the second session: %v", err)
	}
	_ = download.Body.Close()
	// The second session is still live, so the last slot ending has not happened
	// yet and the send must survive.
	if view := burnTestTransferView(t, svc, user.User.ID, published.ID); view.Status != TransferStatusPublished {
		t.Fatalf("expected the send to survive a live second session, got %#v", view)
	}

	completeBurnTestClaim(t, svc, token, second)

	// Every slot is spent and every session ended: the send is destroyed, the
	// link is dead and the content is queued for release.
	view := burnTestTransferView(t, svc, user.User.ID, published.ID)
	if view.Status != TransferStatusDestroyed || view.DestroyedAt == nil {
		t.Fatalf("expected the send to be destroyed, got %#v", view)
	}
	if view.DestroyReason != TransferDestroyReasonClaimsEnded {
		t.Fatalf("expected the claims to be named as the reason, got %q", view.DestroyReason)
	}
	if view.CleanupStatus != "pending_delete" {
		t.Fatalf("expected the content to be marked for deletion, got %q", view.CleanupStatus)
	}
	if view.Share == nil || view.Share.RevokedAt == nil {
		t.Fatalf("expected the share to be revoked with the send, got %#v", view.Share)
	}
	if len(svc.cleanupJobs) != 1 || svc.cleanupJobs[0].Kind != "cleanup" || svc.cleanupJobs[0].TargetID != published.PasteID {
		t.Fatalf("expected one cleanup job for the destroyed content, got %#v", svc.cleanupJobs)
	}

	// Every entry point refuses the destroyed send with its own terminal code.
	if _, _, _, err := svc.AccessShareWithTransferContext(context.Background(), token, "", "", ""); !hasAppCode(err, "transfer_destroyed") {
		t.Fatalf("expected the page to report the destroyed send, got %v", err)
	}
	if _, err := svc.OpenSharedAttachmentWithClaimOrAccessGrantContext(context.Background(), token, second.ClaimToken, false, attachmentID, ""); !hasAppCode(err, "transfer_destroyed") {
		t.Fatalf("expected a download to report the destroyed send, got %v", err)
	}
	if _, err := svc.ClaimTransferWithContext(context.Background(), token, "", "", "op-3"); !hasAppCode(err, "transfer_destroyed") {
		t.Fatalf("expected a claim to report the destroyed send, got %v", err)
	}
}

// TestBurnTextTransferEndsOnlyAfterEverySlotIsSpent covers the text rule: the
// body stays readable for the slots that are left, and it goes once the last
// slot was spent and its window ended.
func TestBurnTextTransferEndsOnlyAfterEverySlotIsSpent(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "burn-text@example.com")

	created, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, TransferInput{
		IdempotencyKey:   "burn-text",
		ExpiresInSeconds: 3600,
		ClaimQuota:       2,
		BurnAfterReading: true,
		Text:             "burn body",
	})
	if err != nil {
		t.Fatalf("create text transfer: %v", err)
	}
	published, err := svc.PublishTransferWithContext(context.Background(), user.User.ID, created.ID)
	if err != nil {
		t.Fatalf("publish text transfer: %v", err)
	}
	token := published.Share.Token

	first := claimBurnTestTransfer(t, svc, token, "op-1")
	if first.Text != "burn body" || first.Claim.Kind != TransferClaimKindText {
		t.Fatalf("expected the text body on the first claim, got %#v", first)
	}

	// The first reader's window ends, but a slot is still unclaimed, so the
	// send survives: the later claim still needs the body.
	now = now.Add(TransferClaimTextWindow + time.Minute)
	if destroyed, err := svc.RunTransferBurnSweepWithContext(context.Background()); err != nil || destroyed != 0 {
		t.Fatalf("expected the send to survive while a slot is left, got %d %v", destroyed, err)
	}
	if _, _, _, err := svc.AccessShareWithTransferContext(context.Background(), token, "", "", ""); err != nil {
		t.Fatalf("expected the page to stay open while a slot is left: %v", err)
	}

	second := claimBurnTestTransfer(t, svc, token, "op-2")
	if second.Text != "burn body" {
		t.Fatalf("expected the last slot to still hand out the body, got %#v", second)
	}
	// The last slot is spent but its window is still open, so the body is not
	// destroyed under the recipient who just claimed it.
	if destroyed, err := svc.RunTransferBurnSweepWithContext(context.Background()); err != nil || destroyed != 0 {
		t.Fatalf("expected a live text window to hold the send open, got %d %v", destroyed, err)
	}

	now = now.Add(TransferClaimTextWindow + time.Minute)
	destroyed, err := svc.RunTransferBurnSweepWithContext(context.Background())
	if err != nil {
		t.Fatalf("burn sweep: %v", err)
	}
	if destroyed != 1 {
		t.Fatalf("expected the sweep to destroy one send, got %d", destroyed)
	}
	view := burnTestTransferView(t, svc, user.User.ID, published.ID)
	if view.Status != TransferStatusDestroyed || view.DestroyReason != TransferDestroyReasonClaimsEnded {
		t.Fatalf("expected the text send to be destroyed, got %#v", view)
	}
	if _, _, _, err := svc.AccessShareWithTransferContext(context.Background(), token, "", "", ""); !hasAppCode(err, "transfer_destroyed") {
		t.Fatalf("expected the destroyed text send to refuse the page, got %v", err)
	}
}

// TestBurnSweepDestroysExpiredSends covers the condition a worker has to
// enforce on its own: a lifetime that ran out with slots left. Revoking the
// link is deliberately not a destroy condition, and the test pins that too.
func TestBurnSweepDestroysExpiredSends(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "burn-expiry@example.com")

	// Three slots, one claimed: the send is not fully claimed, so only its own
	// lifetime can end it.
	expiring, _ := publishBurnTestTransfer(t, svc, user.User.ID, "burn-expiry", 3, true, 600)
	claimBurnTestTransfer(t, svc, expiring.Share.Token, "op-1")
	now = now.Add(601 * time.Second)
	destroyed, err := svc.RunTransferBurnSweepWithContext(context.Background())
	if err != nil {
		t.Fatalf("burn sweep: %v", err)
	}
	if destroyed != 1 {
		t.Fatalf("expected the sweep to destroy the expired send, got %d", destroyed)
	}
	view := burnTestTransferView(t, svc, user.User.ID, expiring.ID)
	if view.Status != TransferStatusDestroyed || view.DestroyReason != TransferDestroyReasonExpired {
		t.Fatalf("expected an expiry reason, got %#v", view)
	}
	if _, _, _, err := svc.AccessShareWithTransferContext(context.Background(), expiring.Share.Token, "", "", ""); !hasAppCode(err, "transfer_destroyed") {
		t.Fatalf("expected the expired send to report its terminal state, got %v", err)
	}

	// Revoking the link is an access change, not a destroy condition: the
	// approved conditions are the claims ending and the lifetime ending, so a
	// revoked burn send keeps its content until the lifetime would have ended.
	revoked, attachmentID := publishBurnTestTransfer(t, svc, user.User.ID, "burn-revoke", 2, true, 3600)
	live := claimBurnTestTransfer(t, svc, revoked.Share.Token, "op-2")
	if err := svc.RevokeShareWithContext(context.Background(), user.User.ID, revoked.Share.ID); err != nil {
		t.Fatalf("revoke share: %v", err)
	}
	view = burnTestTransferView(t, svc, user.User.ID, revoked.ID)
	if view.Status != TransferStatusPublished || view.DestroyedAt != nil || view.CleanupStatus != "active" {
		t.Fatalf("expected a revoked burn send to keep its content, got %#v", view)
	}
	// A session that was still open cannot outlive the revocation.
	if _, err := svc.OpenSharedAttachmentWithClaimOrAccessGrantContext(context.Background(), revoked.Share.Token, live.ClaimToken, false, attachmentID, ""); !hasAppStatus(err, http.StatusGone) {
		t.Fatalf("expected the live session to stop with the revocation, got %v", err)
	}
	if _, _, _, err := svc.AccessShareWithTransferContext(context.Background(), revoked.Share.Token, "", "", ""); !hasAppStatus(err, http.StatusGone) {
		t.Fatalf("expected the revoked page to be refused, got %v", err)
	}
	// The sweep only ends it once the lifetime is over, exactly like any other
	// send that was not fully claimed.
	now = revoked.ExpiresAt.Add(time.Minute)
	if _, err := svc.RunTransferBurnSweepWithContext(context.Background()); err != nil {
		t.Fatalf("burn sweep after the lifetime: %v", err)
	}
	view = burnTestTransferView(t, svc, user.User.ID, revoked.ID)
	if view.Status != TransferStatusDestroyed || view.DestroyReason != TransferDestroyReasonExpired {
		t.Fatalf("expected the lifetime to end the revoked send, got %#v", view)
	}
}

// TestBurnStopsAnOpenDownloadWhenItsSessionEnds covers the in-flight stream:
// the bytes already sent are not recalled, but a download that is still open
// stops at the next checkpoint instead of finishing on a stale decision.
func TestBurnStopsAnOpenDownloadWhenItsSessionEnds(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "burn-stream@example.com")

	// Two slots, so ending the first session does not destroy the send: this is
	// the plain "session ended" case.
	published, attachmentID := publishBurnTestTransfer(t, svc, user.User.ID, "burn-stream", 2, true, 3600)
	token := published.Share.Token
	first := claimBurnTestTransfer(t, svc, token, "op-1")
	download, err := svc.OpenSharedAttachmentWithClaimOrAccessGrantContext(context.Background(), token, first.ClaimToken, false, attachmentID, "")
	if err != nil {
		t.Fatalf("open download: %v", err)
	}
	defer download.Body.Close()
	probe := make([]byte, 1)
	if _, err := download.Body.Read(probe); err != nil {
		t.Fatalf("read the first byte of the stream: %v", err)
	}
	completeBurnTestClaim(t, svc, token, first)
	now = now.Add(TransferStreamRecheckInterval + time.Second)
	if _, err := download.Body.Read(probe); !hasAppCode(err, "claim_ended") {
		t.Fatalf("expected the open stream to stop when the session ended, got %v", err)
	}
	// Once stopped it stays stopped, even if the same body is read again.
	if _, err := download.Body.Read(probe); !hasAppCode(err, "claim_ended") {
		t.Fatalf("expected the stopped stream to keep refusing reads, got %v", err)
	}

	// The same guard stops a stream when the link is taken away while the
	// session that opened it is still live, and the bytes already handed over
	// are not recalled.
	revoked, revokedAttachment := publishBurnTestTransfer(t, svc, user.User.ID, "burn-stream-revoked", 2, true, 3600)
	live := claimBurnTestTransfer(t, svc, revoked.Share.Token, "op-2")
	stream, err := svc.OpenSharedAttachmentWithClaimOrAccessGrantContext(context.Background(), revoked.Share.Token, live.ClaimToken, false, revokedAttachment, "")
	if err != nil {
		t.Fatalf("open revoked download: %v", err)
	}
	defer stream.Body.Close()
	if _, err := stream.Body.Read(probe); err != nil {
		t.Fatalf("read the first byte of the revoked stream: %v", err)
	}
	if err := svc.RevokeShareWithContext(context.Background(), user.User.ID, revoked.Share.ID); err != nil {
		t.Fatalf("revoke share: %v", err)
	}
	now = now.Add(TransferStreamRecheckInterval + time.Second)
	if _, err := stream.Body.Read(probe); !hasAppStatus(err, http.StatusGone) {
		t.Fatalf("expected the open stream to stop when the send was destroyed, got %v", err)
	}
}

// failingDecrementRefStore is a content-addressed object reference store whose
// decrement always fails, which is how the test makes the physical cleanup fail
// after the terminal state was already committed.
type failingDecrementRefStore struct {
	// ObjectRefStore carries the plain reference methods; only the atomic pair
	// the cleanup path uses is implemented here.
	ObjectRefStore
	refs map[string]int
}

func (f *failingDecrementRefStore) UpsertObjectRef(context.Context, ObjectRef) error { return nil }

func (f *failingDecrementRefStore) DeleteObjectRef(context.Context, string) error { return nil }

func (f *failingDecrementRefStore) IncrementObjectRef(_ context.Context, ref ObjectRef) (ObjectRef, error) {
	if f.refs == nil {
		f.refs = map[string]int{}
	}
	f.refs[ref.ObjectKey]++
	ref.RefCount = f.refs[ref.ObjectKey]
	return ref, nil
}

func (f *failingDecrementRefStore) DecrementObjectRef(_ context.Context, _ string) (ObjectRef, bool, error) {
	return ObjectRef{}, false, errors.New("object store unavailable")
}

// TestBurnCleanupFailureKeepsTheSendRefusedAndRetryable covers the failure
// boundary: the terminal state is committed before any byte is touched, so a
// cleanup that fails cannot bring the send back, and the queued job is what
// retries the physical release.
func TestBurnCleanupFailureKeepsTheSendRefusedAndRetryable(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	svc := newTestServiceWithStorage(t, &now, Stores{Content: ContentStores{ObjectRefs: &failingDecrementRefStore{}}})
	user := registerTestUser(t, svc, "burn-failure@example.com")

	published, attachmentID := publishBurnTestTransfer(t, svc, user.User.ID, "burn-failure", 1, true, 3600)
	claim := claimBurnTestTransfer(t, svc, published.Share.Token, "op-1")
	completeBurnTestClaim(t, svc, published.Share.Token, claim)

	view := burnTestTransferView(t, svc, user.User.ID, published.ID)
	if view.Status != TransferStatusDestroyed || view.CleanupStatus != "pending_delete" {
		t.Fatalf("expected the terminal state to be committed before the bytes are touched, got %#v", view)
	}
	if len(svc.cleanupJobs) != 1 {
		t.Fatalf("expected the cleanup to be queued, got %#v", svc.cleanupJobs)
	}

	if _, err := svc.RunCleanup(""); err == nil {
		t.Fatalf("expected the physical cleanup to fail")
	}

	// A failed cleanup does not restore access: the committed terminal state is
	// what refuses every entry point.
	if _, _, _, err := svc.AccessShareWithTransferContext(context.Background(), published.Share.Token, "", "", ""); !hasAppCode(err, "transfer_destroyed") {
		t.Fatalf("expected the page to stay refused, got %v", err)
	}
	if _, err := svc.OpenSharedAttachmentWithClaimOrAccessGrantContext(context.Background(), published.Share.Token, claim.ClaimToken, false, attachmentID, ""); !hasAppCode(err, "transfer_destroyed") {
		t.Fatalf("expected the download to stay refused, got %v", err)
	}
	view = burnTestTransferView(t, svc, user.User.ID, published.ID)
	if view.Status != TransferStatusDestroyed || view.DestroyedAt == nil {
		t.Fatalf("expected the send to stay destroyed, got %#v", view)
	}
	// The job is still queued, so the worker retries the release instead of the
	// content being forgotten.
	if len(svc.cleanupJobs) != 1 || svc.cleanupJobs[0].Status != "pending" {
		t.Fatalf("expected the cleanup job to stay queued for a retry, got %#v", svc.cleanupJobs)
	}
}

// TestBurnCleanupReleasesOnlyItsOwnContentAndProtectsSharedObjects covers the
// physical side: the burned record's bytes go, a record that still references
// the same object keeps it, and unrelated content is untouched.
func TestBurnCleanupReleasesOnlyItsOwnContentAndProtectsSharedObjects(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "burn-objects@example.com")

	// One ordinary record and one burn send share the same bytes, which
	// content-addressed storage turns into one object with two references.
	shared := []byte("shared-bytes")
	keeper := createTestPaste(t, svc, user.User.ID, PasteInput{Title: "keeper", Text: "keep", ExpiresInSeconds: 3600})
	keeperAttachment := addTestAttachment(t, svc, user.User.ID, keeper.ID, "keep.txt", shared)

	created, err := svc.CreateTransferWithContext(context.Background(), user.User.ID, TransferInput{
		IdempotencyKey:   "burn-objects",
		ExpiresInSeconds: 3600,
		ClaimQuota:       1,
		BurnAfterReading: true,
		Items:            []TransferItemInput{transferItem("itm-1", "burn.txt")},
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	_, burnedAttachment := uploadTestTransferItem(t, svc, user.User.ID, created.ID, "itm-1", "burn.txt", shared)
	runCleanScan(t, svc, burnedAttachment.ID)
	published, err := svc.PublishTransferWithContext(context.Background(), user.User.ID, created.ID)
	if err != nil {
		t.Fatalf("publish transfer: %v", err)
	}
	if burnTestAttachment(t, svc, keeperAttachment.ID).ObjectKey != burnTestAttachment(t, svc, burnedAttachment.ID).ObjectKey {
		t.Fatalf("expected the two records to share one object, got %q and %q",
			burnTestAttachment(t, svc, keeperAttachment.ID).ObjectKey, burnTestAttachment(t, svc, burnedAttachment.ID).ObjectKey)
	}

	claim := claimBurnTestTransfer(t, svc, published.Share.Token, "op-1")
	completeBurnTestClaim(t, svc, published.Share.Token, claim)
	if _, err := svc.RunCleanup(""); err != nil {
		t.Fatalf("run cleanup: %v", err)
	}

	// The record that still references the object keeps working.
	if _, content, err := svc.DownloadAttachment(user.User.ID, keeperAttachment.ID); err != nil {
		t.Fatalf("expected the shared object to survive for the other record: %v", err)
	} else if string(content) != string(shared) {
		t.Fatalf("expected the shared bytes to stay intact, got %q", content)
	}
	// The burned record's own attachment is gone.
	view := burnTestTransferView(t, svc, user.User.ID, published.ID)
	if view.CleanupStatus != "deleted" {
		t.Fatalf("expected the burned content to be fully cleaned up, got %q", view.CleanupStatus)
	}
	if _, _, err := svc.DownloadAttachment(user.User.ID, burnedAttachment.ID); err == nil {
		t.Fatalf("expected the burned attachment to be gone")
	}

	// A burn send whose object nobody else references loses the bytes.
	solo, soloAttachment := publishBurnTestTransfer(t, svc, user.User.ID, "burn-solo", 1, true, 3600)
	soloClaim := claimBurnTestTransfer(t, svc, solo.Share.Token, "op-2")
	completeBurnTestClaim(t, svc, solo.Share.Token, soloClaim)
	if _, err := svc.RunCleanup(""); err != nil {
		t.Fatalf("run cleanup: %v", err)
	}
	snapshot := burnTestAttachment(t, svc, soloAttachment)
	if snapshot.Status != "deleted" {
		t.Fatalf("expected the solo attachment to be deleted, got %q", snapshot.Status)
	}
	if _, ok := svc.objects[snapshot.ObjectKey]; ok {
		t.Fatalf("expected the unreferenced object to be removed, got %#v", svc.objects)
	}
	if len(svc.cleanupFailures) != 0 {
		t.Fatalf("expected no cleanup failure for a destroyed send, got %#v", svc.cleanupFailures)
	}
}
