package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"
	"time"

	"pastebox/internal/config"
)

func TestExpiredContentIsHiddenFromOwnerSearchDownloadAndShare(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "owner@example.com")
	paste := createTestPaste(t, svc, user.User.ID, PasteInput{Title: "expires", Text: "secret", ExpiresInSeconds: 60})
	attachment := addTestAttachment(t, svc, user.User.ID, paste.ID, "note.txt", []byte("attachment"))
	share := createTestShare(t, svc, user.User.ID, paste.ID, ShareInput{ExpiresInSeconds: 60})

	now = now.Add(61 * time.Second)

	if _, err := svc.GetPaste(user.User.ID, paste.ID); !hasAppStatus(err, http.StatusGone) {
		t.Fatalf("expected expired owner read to return 410, got %v", err)
	}
	items, err := svc.ListPastes(user.User.ID, ListOptions{Query: "secret"})
	if err != nil {
		t.Fatalf("list pastes: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected expired paste to be hidden from search, got %d item(s)", len(items))
	}
	if _, _, err := svc.DownloadAttachment(user.User.ID, attachment.ID); !hasAppStatus(err, http.StatusGone) {
		t.Fatalf("expected expired owner download to return 410, got %v", err)
	}
	if _, _, err := svc.AccessShare(share.Token, "", ""); !hasAppStatus(err, http.StatusGone) {
		t.Fatalf("expected expired share access to return 410, got %v", err)
	}
}

func TestShareVisitAndDownloadLimitsAreSeparate(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "owner@example.com")
	paste := createTestPaste(t, svc, user.User.ID, PasteInput{Title: "share", Text: "visible", ExpiresInSeconds: 3600})
	attachment := addTestAttachment(t, svc, user.User.ID, paste.ID, "file.txt", []byte("download"))
	share := createTestShare(t, svc, user.User.ID, paste.ID, ShareInput{
		Password:         "pw",
		MaxVisits:        1,
		MaxDownloads:     1,
		ExpiresInSeconds: 3600,
	})

	if _, _, err := svc.AccessShare(share.Token, "pw", ""); err != nil {
		t.Fatalf("first share access: %v", err)
	}
	if _, _, err := svc.AccessShare(share.Token, "pw", ""); !hasAppCode(err, "visit_limit_reached") {
		t.Fatalf("expected second access to hit visit limit, got %v", err)
	}
	if _, _, err := svc.DownloadSharedAttachment(share.Token, "pw", attachment.ID, ""); err != nil {
		t.Fatalf("download after visit limit should still use separate download limit: %v", err)
	}
	if _, _, err := svc.DownloadSharedAttachment(share.Token, "pw", attachment.ID, ""); !hasAppCode(err, "download_limit_reached") {
		t.Fatalf("expected second download to hit download limit, got %v", err)
	}
}

func TestLoginRequiredShareRejectsAnonymousAccess(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "owner@example.com")
	paste := createTestPaste(t, svc, user.User.ID, PasteInput{Title: "login share", Text: "visible", ExpiresInSeconds: 3600})
	share := createTestShare(t, svc, user.User.ID, paste.ID, ShareInput{LoginRequired: true, ExpiresInSeconds: 3600})

	if _, _, err := svc.AccessShare(share.Token, "", ""); !hasAppCode(err, "login_required") {
		t.Fatalf("expected anonymous access to require login, got %v", err)
	}
	if _, _, err := svc.AccessShare(share.Token, "", user.User.ID); err != nil {
		t.Fatalf("authenticated share access: %v", err)
	}
}

func TestPlanQuotasAreEnforcedServerSide(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "quota@example.com")

	largeText := strings.Repeat("x", int(svc.catalog.Plans[0].SingleTextBytes)+1)
	if _, err := svc.CreatePaste(user.User.ID, PasteInput{Text: largeText, ExpiresInSeconds: 60}); !hasAppCode(err, "text_too_large") {
		t.Fatalf("expected text size quota error, got %v", err)
	}

	for i := 0; i < svc.catalog.Plans[0].ActivePasteLimit; i++ {
		createTestPaste(t, svc, user.User.ID, PasteInput{Title: "paste", Text: "ok", ExpiresInSeconds: 60})
	}
	if _, err := svc.CreatePaste(user.User.ID, PasteInput{Text: "one too many", ExpiresInSeconds: 60}); !hasAppCode(err, "active_paste_limit") {
		t.Fatalf("expected active paste quota error, got %v", err)
	}

	now = now.Add(61 * time.Second)
	if _, err := svc.CreatePaste(user.User.ID, PasteInput{Text: "allowed after expiry", ExpiresInSeconds: 60}); err != nil {
		t.Fatalf("expired content should release logical quota: %v", err)
	}
}

func TestTextCreateCountsAgainstDailyUploadQuota(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	svc.catalog.Plans[0].DailyUploadBytes = 5
	svc.catalog.Plans[0].SingleTextBytes = 64
	svc.catalog.Plans[0].SinglePasteBytes = 64
	svc.catalog.Plans[0].ActiveStorageBytes = 64
	user := registerTestUser(t, svc, "daily-text@example.com")

	if _, err := svc.CreatePaste(user.User.ID, PasteInput{Text: "12345", ExpiresInSeconds: 60}); err != nil {
		t.Fatalf("expected first text paste within daily upload quota: %v", err)
	}
	if _, err := svc.CreatePaste(user.User.ID, PasteInput{Text: "x", ExpiresInSeconds: 60}); !hasAppCode(err, "daily_upload_limit") {
		t.Fatalf("expected second text paste to hit daily upload quota, got %v", err)
	}
}

func TestDailyMetricReadFailureBlocksQuotaMutations(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	storeErr := errors.New("daily metric read failed")
	svc := newTestServiceWithDailyMetrics(t, &now, failingDailyMetricStore{readErr: storeErr})
	user := registerTestUser(t, svc, "metric-read@example.com")

	if _, err := svc.CreatePaste(user.User.ID, PasteInput{Text: "blocked", ExpiresInSeconds: 60}); !errors.Is(err, storeErr) {
		t.Fatalf("expected daily metric read error, got %v", err)
	}
	pastes, err := svc.ListPastes(user.User.ID, ListOptions{})
	if err != nil {
		t.Fatalf("list pastes after failed create: %v", err)
	}
	if len(pastes) != 0 {
		t.Fatalf("failed quota read must not create paste, got %#v", pastes)
	}
}

func TestDailyMetricWriteFailureDoesNotPartiallyCreatePaste(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	storeErr := errors.New("daily metric write failed")
	svc := newTestServiceWithDailyMetrics(t, &now, failingDailyMetricStore{writeErr: storeErr})
	user := registerTestUser(t, svc, "metric-write@example.com")

	if _, err := svc.CreatePaste(user.User.ID, PasteInput{Text: "blocked", ExpiresInSeconds: 60}); !errors.Is(err, storeErr) {
		t.Fatalf("expected daily metric write error, got %v", err)
	}
	pastes, err := svc.ListPastes(user.User.ID, ListOptions{})
	if err != nil {
		t.Fatalf("list pastes after failed create: %v", err)
	}
	if len(pastes) != 0 {
		t.Fatalf("failed metric write must not create paste, got %#v", pastes)
	}
}

func TestEmailVerificationRequiredBeforePasswordLoginAndWrites(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	result, err := svc.Register(context.Background(), RegisterInput{
		Email:       "verify@example.com",
		Password:    "password123",
		DisplayName: "Verify",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if result.User.EmailVerified {
		t.Fatalf("new email/password account should start unverified")
	}
	if result.DevEmailVerificationToken == "" {
		t.Fatalf("expected development verification token")
	}
	if _, err := svc.Login(context.Background(), "verify@example.com", "password123"); !hasAppCode(err, "email_not_verified") {
		t.Fatalf("expected password login to require verification, got %v", err)
	}
	if _, err := svc.CreatePaste(result.User.ID, PasteInput{Text: "blocked", ExpiresInSeconds: 60}); !hasAppCode(err, "email_not_verified") {
		t.Fatalf("expected write to require verification, got %v", err)
	}
	verified, err := svc.FinishEmailVerification(result.DevEmailVerificationToken)
	if err != nil {
		t.Fatalf("finish verification: %v", err)
	}
	if !verified.EmailVerified {
		t.Fatalf("expected user to become verified")
	}
	if _, err := svc.Login(context.Background(), "verify@example.com", "password123"); err != nil {
		t.Fatalf("verified password login: %v", err)
	}
	if _, err := svc.CreatePaste(result.User.ID, PasteInput{Text: "allowed", ExpiresInSeconds: 60}); err != nil {
		t.Fatalf("verified write: %v", err)
	}
}

func TestStoreBackedAuthStateSurvivesServiceRestart(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	stores := newMemoryAuthStores()
	svc := newTestServiceWithAuthStores(t, &now, stores.authStores())

	registered, err := svc.Register(context.Background(), RegisterInput{
		Email:       "durable@example.com",
		Password:    "password123",
		DisplayName: "Durable",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if registered.DevEmailVerificationToken == "" || registered.SessionID == "" {
		t.Fatalf("expected register to issue session and verification token, got %#v", registered)
	}

	restarted := newTestServiceWithAuthStores(t, &now, stores.authStores())
	sessionUser, err := restarted.UserForSession(registered.SessionID)
	if err != nil {
		t.Fatalf("session should survive a fresh service instance: %v", err)
	}
	if sessionUser.ID != registered.User.ID || sessionUser.Email != "durable@example.com" {
		t.Fatalf("unexpected session user after restart: %#v", sessionUser)
	}

	verified, err := restarted.FinishEmailVerification(registered.DevEmailVerificationToken)
	if err != nil {
		t.Fatalf("email verification token should survive restart: %v", err)
	}
	if !verified.EmailVerified {
		t.Fatalf("expected verified user after restart, got %#v", verified)
	}

	restartedAgain := newTestServiceWithAuthStores(t, &now, stores.authStores())
	loggedIn, err := restartedAgain.Login(context.Background(), "durable@example.com", "password123")
	if err != nil {
		t.Fatalf("verified user should be loginable after another restart: %v", err)
	}
	if loggedIn.User.ID != registered.User.ID || loggedIn.SessionID == "" {
		t.Fatalf("unexpected login after restart: %#v", loggedIn)
	}

	restartedAgain.Logout(loggedIn.SessionID)
	afterLogout := newTestServiceWithAuthStores(t, &now, stores.authStores())
	if _, err := afterLogout.UserForSession(loggedIn.SessionID); !hasAppCode(err, "unauthenticated") {
		t.Fatalf("logout should revoke persisted session, got %v", err)
	}
}

func TestGoogleOAuthCreatesVerifiedAccount(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	result, err := svc.GoogleOAuth(context.Background(), "google@example.com", "Google User", "google-sub-1")
	if err != nil {
		t.Fatalf("google oauth: %v", err)
	}
	if !result.User.EmailVerified || result.User.DisplayName != "Google User" {
		t.Fatalf("unexpected oauth user: %#v", result.User)
	}
	if _, err := svc.CreatePaste(result.User.ID, PasteInput{Text: "oauth", ExpiresInSeconds: 60}); err != nil {
		t.Fatalf("oauth user should be able to write: %v", err)
	}
}

func TestPasteUpdateCannotBypassSinglePasteQuota(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	svc.catalog.Plans[0].SinglePasteBytes = 12
	svc.catalog.Plans[0].SingleFileBytes = 12
	svc.catalog.Plans[0].ActiveStorageBytes = 64
	user := registerTestUser(t, svc, "quota-edit@example.com")
	paste := createTestPaste(t, svc, user.User.ID, PasteInput{Text: "ok", ExpiresInSeconds: 3600})
	attachmentSize := svc.catalog.Plans[0].SinglePasteBytes - int64(len([]byte(paste.Text))) - 1
	addTestAttachment(t, svc, user.User.ID, paste.ID, "payload.bin", make([]byte, attachmentSize))

	tooLargeText := strings.Repeat("x", 4)
	if _, err := svc.UpdatePaste(user.User.ID, paste.ID, PastePatch{Text: &tooLargeText}); !hasAppCode(err, "paste_too_large") {
		t.Fatalf("expected update to enforce total paste quota, got %v", err)
	}

	reducedText := "fit"
	if _, err := svc.UpdatePaste(user.User.ID, paste.ID, PastePatch{Text: &reducedText}); err != nil {
		t.Fatalf("expected update within total paste quota to succeed: %v", err)
	}
}

func TestAttachmentDedupeReusesSameUserObjectUntilLastReferenceDeleted(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "dedupe@example.com")
	firstPaste := createTestPaste(t, svc, user.User.ID, PasteInput{Text: "first", ExpiresInSeconds: 3600})
	secondPaste := createTestPaste(t, svc, user.User.ID, PasteInput{Text: "second", ExpiresInSeconds: 3600})
	content := []byte("same payload")

	firstAttachment := addTestAttachment(t, svc, user.User.ID, firstPaste.ID, "one.txt", content)
	secondAttachment := addTestAttachment(t, svc, user.User.ID, secondPaste.ID, "two.txt", content)
	firstStored := svc.attachmentsByID[firstAttachment.ID]
	secondStored := svc.attachmentsByID[secondAttachment.ID]
	if firstStored.ObjectKey != secondStored.ObjectKey {
		t.Fatalf("expected same-user duplicate content to reuse object key, got %q and %q", firstStored.ObjectKey, secondStored.ObjectKey)
	}
	if refs := svc.objectRefs[firstStored.ObjectKey]; refs != 2 {
		t.Fatalf("expected two object references, got %d", refs)
	}
	if len(svc.objects) != 1 {
		t.Fatalf("expected one physical object, got %d", len(svc.objects))
	}

	if err := svc.DeletePaste(user.User.ID, firstPaste.ID); err != nil {
		t.Fatalf("delete first paste: %v", err)
	}
	if _, err := svc.RunCleanup(""); err != nil {
		t.Fatalf("cleanup first paste: %v", err)
	}
	if refs := svc.objectRefs[firstStored.ObjectKey]; refs != 1 {
		t.Fatalf("expected one object reference after first cleanup, got %d", refs)
	}
	if _, ok := svc.objects[firstStored.ObjectKey]; !ok {
		t.Fatalf("expected object to remain while second attachment references it")
	}
	if _, downloaded, err := svc.DownloadAttachment(user.User.ID, secondAttachment.ID); err != nil || string(downloaded) != string(content) {
		t.Fatalf("expected second attachment to remain downloadable, got content=%q err=%v", string(downloaded), err)
	}

	if err := svc.DeletePaste(user.User.ID, secondPaste.ID); err != nil {
		t.Fatalf("delete second paste: %v", err)
	}
	if _, err := svc.RunCleanup(""); err != nil {
		t.Fatalf("cleanup second paste: %v", err)
	}
	if _, ok := svc.objects[firstStored.ObjectKey]; ok {
		t.Fatalf("expected object to be deleted after last reference cleanup")
	}
}

func TestImageAttachmentExposesPreviewMetadata(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	user := registerTestUser(t, svc, "image@example.com")
	paste := createTestPaste(t, svc, user.User.ID, PasteInput{Text: "image", ExpiresInSeconds: 3600})
	content := pngFixture(t, 2, 3)

	attachment := addTestAttachment(t, svc, user.User.ID, paste.ID, "preview.png", content)
	if attachment.ContentType != "image/png" {
		t.Fatalf("expected MIME sniffing to detect image/png, got %q", attachment.ContentType)
	}
	if attachment.ImagePreview == nil {
		t.Fatalf("expected image preview metadata")
	}
	if attachment.ImagePreview.Width != 2 || attachment.ImagePreview.Height != 3 {
		t.Fatalf("expected 2x3 image preview, got %#v", attachment.ImagePreview)
	}
}

func TestScanFailedAttachmentIsOwnerDownloadableButNotPublicUntilRetry(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	owner := registerTestUser(t, svc, "owner@example.com")
	admin := seedAdminTestUser(t, svc, "admin@example.com")
	paste := createTestPaste(t, svc, owner.User.ID, PasteInput{Title: "scan", Text: "file", ExpiresInSeconds: 3600})
	attachment := addTestAttachment(t, svc, owner.User.ID, paste.ID, "tool.exe", []byte("binary"))
	share := createTestShare(t, svc, owner.User.ID, paste.ID, ShareInput{ExpiresInSeconds: 3600})

	if attachment.ScanStatus != "scan_failed" {
		t.Fatalf("expected executable stub scan failure, got %q", attachment.ScanStatus)
	}
	if _, _, err := svc.DownloadAttachment(owner.User.ID, attachment.ID); err != nil {
		t.Fatalf("owner should be able to download scan-failed attachment: %v", err)
	}
	if _, _, err := svc.DownloadSharedAttachment(share.Token, "", attachment.ID, ""); !hasAppCode(err, "scan_not_clean") {
		t.Fatalf("expected public download to require clean scan, got %v", err)
	}
	if _, err := svc.AdminRetryScan(admin.ID, attachment.ID); err != nil {
		t.Fatalf("admin retry scan: %v", err)
	}
	if _, _, err := svc.DownloadSharedAttachment(share.Token, "", attachment.ID, ""); err != nil {
		t.Fatalf("public download after retry scan: %v", err)
	}
}

func TestAdminOperationsWriteAuditLogsAndExposeQueues(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	admin := seedAdminTestUser(t, svc, "admin@example.com")
	owner := registerTestUser(t, svc, "owner@example.com")
	paste := createTestPaste(t, svc, owner.User.ID, PasteInput{Title: "admin", Text: "file", ExpiresInSeconds: 3600})
	attachment := addTestAttachment(t, svc, owner.User.ID, paste.ID, "tool.exe", []byte("binary"))
	share := createTestShare(t, svc, owner.User.ID, paste.ID, ShareInput{ExpiresInSeconds: 3600})
	order, err := svc.CreateOrder(owner.User.ID, "stripe", "plus", "monthly")
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	if _, err := svc.AdminSetUserPlan(admin.ID, owner.User.ID, "pro", nil); err != nil {
		t.Fatalf("admin set plan: %v", err)
	}
	if _, err := svc.AdminFreezeUser(admin.ID, owner.User.ID, true); err != nil {
		t.Fatalf("admin freeze user: %v", err)
	}
	if err := svc.AdminTakedownPaste(admin.ID, paste.ID); err != nil {
		t.Fatalf("admin takedown paste: %v", err)
	}
	if _, err := svc.AdminFreezeAttachment(admin.ID, attachment.ID, true); err != nil {
		t.Fatalf("admin freeze attachment: %v", err)
	}
	if _, err := svc.AdminRevokeShare(admin.ID, share.ID); err != nil {
		t.Fatalf("admin revoke share: %v", err)
	}
	if _, err := svc.MarkOrderPaid(admin.ID, order.ID, "tx-test"); err != nil {
		t.Fatalf("mark order paid: %v", err)
	}

	logs, err := svc.AdminAuditLogs(admin.ID)
	if err != nil {
		t.Fatalf("admin audit logs: %v", err)
	}
	assertAuditAction(t, logs, "admin.user_plan_set")
	assertAuditAction(t, logs, "admin.user_freeze")
	assertAuditAction(t, logs, "admin.paste_takedown")
	assertAuditAction(t, logs, "admin.attachment_freeze")
	assertAuditAction(t, logs, "admin.share_revoke")
	assertAuditAction(t, logs, "billing.order_paid")

	queues, err := svc.AdminQueues(admin.ID)
	if err != nil {
		t.Fatalf("admin queues: %v", err)
	}
	scanFailures, ok := queues["scanFailures"].([]*QueueItem)
	if !ok || len(scanFailures) == 0 {
		t.Fatalf("expected scan failure queue to expose executable attachment, got %#v", queues["scanFailures"])
	}
	events, err := svc.AdminWebhookEvents(admin.ID)
	if err != nil {
		t.Fatalf("admin webhook events: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected order create and payment webhook events, got %d", len(events))
	}
}

func TestBillingWebhookReplayAndReportResolution(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	admin := seedAdminTestUser(t, svc, "admin@example.com")
	owner := registerTestUser(t, svc, "owner@example.com")
	order, err := svc.CreateOrder(owner.User.ID, "stripe", "plus", "monthly")
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	event, paidOrder, err := svc.ProcessBillingWebhook(BillingWebhookInput{
		Provider:       "stripe",
		EventType:      "checkout.session.completed",
		OrderID:        order.ID,
		TxID:           "tx-webhook",
		IdempotencyKey: "stripe-event-1",
	})
	if err != nil {
		t.Fatalf("process webhook: %v", err)
	}
	if paidOrder == nil || paidOrder.Status != "paid" || event.IdempotencyKey != "stripe-event-1" {
		t.Fatalf("unexpected webhook result: event=%#v order=%#v", event, paidOrder)
	}
	replayed, err := svc.ReplayWebhookEvent(admin.ID, event.ID)
	if err != nil {
		t.Fatalf("replay webhook: %v", err)
	}
	if replayed.EventType == "" {
		t.Fatalf("expected replay event, got %#v", replayed)
	}

	report, err := svc.Report(owner.User.ID, "share:abc", "abuse")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	resolved, err := svc.AdminResolveReport(admin.ID, report.ID, "resolved")
	if err != nil {
		t.Fatalf("resolve report: %v", err)
	}
	if resolved.Status != "resolved" {
		t.Fatalf("expected resolved report, got %#v", resolved)
	}
	logs, err := svc.AdminAuditLogs(admin.ID)
	if err != nil {
		t.Fatalf("audit logs: %v", err)
	}
	assertAuditAction(t, logs, "admin.webhook_replay")
	assertAuditAction(t, logs, "admin.report_status")
}

func TestAccountDeletionRevokesSharesAndSessions(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	auth := registerTestUser(t, svc, "delete@example.com")
	paste := createTestPaste(t, svc, auth.User.ID, PasteInput{Text: "delete me", ExpiresInSeconds: 3600})
	share := createTestShare(t, svc, auth.User.ID, paste.ID, ShareInput{ExpiresInSeconds: 3600})

	if err := svc.ExecuteAccountDeletion(auth.User.ID); err != nil {
		t.Fatalf("execute account deletion: %v", err)
	}
	if _, err := svc.UserForSession(auth.SessionID); !hasAppCode(err, "unauthenticated") {
		t.Fatalf("expected deletion to revoke sessions, got %v", err)
	}
	if _, _, err := svc.AccessShare(share.Token, "", ""); !hasAppStatus(err, http.StatusGone) {
		t.Fatalf("expected deletion to revoke shares, got %v", err)
	}
	if _, err := svc.GetPaste(auth.User.ID, paste.ID); !hasAppStatus(err, http.StatusUnauthorized) {
		t.Fatalf("expected deleted account owner read to be blocked, got %v", err)
	}
}

func newTestService(t *testing.T, now *time.Time) *Service {
	t.Helper()
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	svc := New(cfg)
	svc.now = func() time.Time { return *now }
	return svc
}

func newTestServiceWithDailyMetrics(t *testing.T, now *time.Time, store DailyMetricStore) *Service {
	t.Helper()
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	svc := NewWithDailyMetricStore(cfg, store)
	svc.now = func() time.Time { return *now }
	return svc
}

func newTestServiceWithAuthStores(t *testing.T, now *time.Time, authStores AuthStores) *Service {
	t.Helper()
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	svc := NewWithStores(cfg, authStores, nil)
	svc.now = func() time.Time { return *now }
	return svc
}

type failingDailyMetricStore struct {
	readErr  error
	writeErr error
}

func (s failingDailyMetricStore) DailyMetric(_ context.Context, _ string, _ string, _ time.Time) (int64, error) {
	if s.readErr != nil {
		return 0, s.readErr
	}
	return 0, nil
}

func (s failingDailyMetricStore) RecordDailyMetric(_ context.Context, _ string, _ string, _ time.Time, _ int64) error {
	return s.writeErr
}

func registerTestUser(t *testing.T, svc *Service, email string) AuthResult {
	t.Helper()
	result, err := svc.Register(context.Background(), RegisterInput{
		Email:       email,
		Password:    "password123",
		DisplayName: email,
	})
	if err != nil {
		t.Fatalf("register %s: %v", email, err)
	}
	if result.DevEmailVerificationToken != "" {
		verified, err := svc.FinishEmailVerification(result.DevEmailVerificationToken)
		if err != nil {
			t.Fatalf("verify %s: %v", email, err)
		}
		result.User = verified
	}
	return result
}

func seedAdminTestUser(t *testing.T, svc *Service, email string) UserView {
	t.Helper()
	admin, err := svc.SeedAdmin(email, "password123")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return admin
}

func createTestPaste(t *testing.T, svc *Service, userID string, input PasteInput) PasteView {
	t.Helper()
	paste, err := svc.CreatePaste(userID, input)
	if err != nil {
		t.Fatalf("create paste: %v", err)
	}
	return paste
}

func addTestAttachment(t *testing.T, svc *Service, userID string, pasteID string, fileName string, content []byte) AttachmentView {
	t.Helper()
	attachment, err := svc.AddAttachment(userID, pasteID, fileName, "application/octet-stream", content)
	if err != nil {
		t.Fatalf("add attachment: %v", err)
	}
	return attachment
}

func createTestShare(t *testing.T, svc *Service, userID string, pasteID string, input ShareInput) ShareView {
	t.Helper()
	share, err := svc.CreateShare(userID, pasteID, input)
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	return share
}

func pngFixture(t *testing.T, width int, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 15, G: 118, B: 110, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func hasAppCode(err error, code string) bool {
	var appErr *Error
	return errors.As(err, &appErr) && appErr.Code == code
}

func hasAppStatus(err error, status int) bool {
	var appErr *Error
	return errors.As(err, &appErr) && appErr.Status == status
}

func assertAuditAction(t *testing.T, logs []AuditLog, action string) {
	t.Helper()
	for _, log := range logs {
		if log.Action == action {
			return
		}
	}
	t.Fatalf("expected audit action %q in %#v", action, logs)
}

type memoryAuthStores struct {
	usersByID     map[string]User
	userIDByEmail map[string]string
	sessions      map[string]Session
	tokens        map[string]AuthToken
	loginFailures map[string]LoginFailure
}

func newMemoryAuthStores() *memoryAuthStores {
	return &memoryAuthStores{
		usersByID:     map[string]User{},
		userIDByEmail: map[string]string{},
		sessions:      map[string]Session{},
		tokens:        map[string]AuthToken{},
		loginFailures: map[string]LoginFailure{},
	}
}

func (s *memoryAuthStores) authStores() AuthStores {
	return AuthStores{
		Users:         s,
		Sessions:      s,
		Tokens:        s,
		LoginFailures: s,
	}
}

func (s *memoryAuthStores) CreateUser(_ context.Context, user User) error {
	if _, ok := s.usersByID[user.ID]; ok {
		return ErrStoreConflict
	}
	if _, ok := s.userIDByEmail[user.Email]; ok {
		return ErrStoreConflict
	}
	s.usersByID[user.ID] = user
	s.userIDByEmail[user.Email] = user.ID
	return nil
}

func (s *memoryAuthStores) UserByID(_ context.Context, id string) (User, error) {
	user, ok := s.usersByID[id]
	if !ok {
		return User{}, ErrStoreNotFound
	}
	return user, nil
}

func (s *memoryAuthStores) UserByEmail(_ context.Context, email string) (User, error) {
	userID := s.userIDByEmail[normalizeEmail(email)]
	if userID == "" {
		return User{}, ErrStoreNotFound
	}
	return s.UserByID(context.Background(), userID)
}

func (s *memoryAuthStores) UpdateUser(_ context.Context, user User) error {
	previous, ok := s.usersByID[user.ID]
	if !ok {
		return ErrStoreNotFound
	}
	if previous.Email != user.Email {
		if existing := s.userIDByEmail[user.Email]; existing != "" && existing != user.ID {
			return ErrStoreConflict
		}
		delete(s.userIDByEmail, previous.Email)
	}
	s.usersByID[user.ID] = user
	s.userIDByEmail[user.Email] = user.ID
	return nil
}

func (s *memoryAuthStores) CreateSession(_ context.Context, session Session) error {
	s.sessions[session.ID] = session
	return nil
}

func (s *memoryAuthStores) SessionByID(_ context.Context, id string) (Session, error) {
	session, ok := s.sessions[id]
	if !ok {
		return Session{}, ErrStoreNotFound
	}
	return session, nil
}

func (s *memoryAuthStores) RevokeSession(_ context.Context, id string, revokedAt time.Time) error {
	session, ok := s.sessions[id]
	if !ok {
		return ErrStoreNotFound
	}
	session.RevokedAt = &revokedAt
	s.sessions[id] = session
	return nil
}

func (s *memoryAuthStores) RevokeUserSessions(_ context.Context, userID string, revokedAt time.Time) (int64, error) {
	var count int64
	for id, session := range s.sessions {
		if session.UserID != userID || session.RevokedAt != nil {
			continue
		}
		session.RevokedAt = &revokedAt
		s.sessions[id] = session
		count++
	}
	return count, nil
}

func (s *memoryAuthStores) CreateAuthToken(_ context.Context, kind string, token AuthToken) error {
	s.tokens[kind+"\x00"+token.Hash] = token
	return nil
}

func (s *memoryAuthStores) AuthToken(_ context.Context, kind string, hash string) (AuthToken, error) {
	token, ok := s.tokens[kind+"\x00"+hash]
	if !ok {
		return AuthToken{}, ErrStoreNotFound
	}
	return token, nil
}

func (s *memoryAuthStores) MarkAuthTokenUsed(_ context.Context, kind string, hash string, usedAt time.Time) error {
	key := kind + "\x00" + hash
	token, ok := s.tokens[key]
	if !ok {
		return ErrStoreNotFound
	}
	token.UsedAt = &usedAt
	s.tokens[key] = token
	return nil
}

func (s *memoryAuthStores) LoginFailure(_ context.Context, email string) (LoginFailure, error) {
	failure, ok := s.loginFailures[email]
	if !ok {
		return LoginFailure{}, ErrStoreNotFound
	}
	return failure, nil
}

func (s *memoryAuthStores) SaveLoginFailure(_ context.Context, email string, failure LoginFailure) error {
	s.loginFailures[email] = failure
	return nil
}

func (s *memoryAuthStores) DeleteLoginFailure(_ context.Context, email string) error {
	delete(s.loginFailures, email)
	return nil
}
