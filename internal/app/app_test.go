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
	"pastebox/internal/plans"
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
	runCleanScan(t, svc, attachment.ID)
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
	admin := seedAdminTestUser(t, svc, "durable-admin@example.com")

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
	users, err := restarted.AdminUsers(admin.ID)
	if err != nil {
		t.Fatalf("admin users should list persisted auth users after restart: %v", err)
	}
	if !hasUserEmail(users, "durable-admin@example.com") || !hasUserEmail(users, "durable@example.com") {
		t.Fatalf("expected admin listing to include persisted users after restart, got %#v", users)
	}
	dashboard, err := restarted.AdminDashboard(admin.ID)
	if err != nil {
		t.Fatalf("admin dashboard should count persisted users after restart: %v", err)
	}
	if dashboard["users"] != 2 {
		t.Fatalf("expected persisted user count after restart, got %#v", dashboard["users"])
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

func TestStoreBackedCatalogAndAuditLogsSurviveServiceRestart(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	stores := newMemoryAuthStores()
	catalogStore := memoryCatalogStore{catalog: plans.Catalog{
		Plans: []plans.Plan{{
			ID:                       "free",
			Name:                     "Free From Store",
			ActivePasteLimit:         3,
			ActiveStorageBytes:       1024,
			SingleTextBytes:          128,
			SingleFileBytes:          256,
			SinglePasteBytes:         512,
			AttachmentsPerPasteLimit: 2,
			MaxRetentionSeconds:      3600,
			DailyUploadBytes:         1024,
			DailyShareDownloadBytes:  2048,
		}},
		Prices: []plans.Price{},
	}}
	auditStore := newMemoryAuditLogStore()

	svc := newTestServiceWithStorage(t, &now, Stores{
		Auth:         stores.authStores(),
		Catalog:      catalogStore,
		AuditLogs:    auditStore,
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	admin := seedAdminTestUser(t, svc, "admin-catalog-audit@example.com")
	owner := registerTestUser(t, svc, "owner-catalog-audit@example.com")

	catalog := svc.PlanCatalog()
	if len(catalog.Plans) != 1 || catalog.Plans[0].Name != "Free From Store" {
		t.Fatalf("expected service catalog to come from store, got %#v", catalog)
	}
	if _, err := svc.AdminFreezeUser(admin.ID, owner.User.ID, true); err != nil {
		t.Fatalf("admin freeze user: %v", err)
	}

	restarted := newTestServiceWithStorage(t, &now, Stores{
		Auth:         stores.authStores(),
		Catalog:      catalogStore,
		AuditLogs:    auditStore,
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	logs, err := restarted.AdminAuditLogs(admin.ID)
	if err != nil {
		t.Fatalf("read persisted audit logs: %v", err)
	}
	assertAuditAction(t, logs, "admin.user_freeze")
}

func TestStoreBackedContentMetadataSurvivesServiceRestart(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	authStores := newMemoryAuthStores()
	contentStores := newMemoryContentStores()

	svc := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	owner := registerTestUser(t, svc, "content-durable@example.com")
	paste := createTestPaste(t, svc, owner.User.ID, PasteInput{
		Title:            "durable paste",
		Text:             "metadata survives",
		Tags:             []string{"durable"},
		ExpiresInSeconds: 3600,
	})
	attachment := addTestAttachment(t, svc, owner.User.ID, paste.ID, "metadata.txt", []byte("metadata only"))
	share := createTestShare(t, svc, owner.User.ID, paste.ID, ShareInput{
		Password:         "pw",
		ExpiresInSeconds: 1800,
	})
	if _, _, err := svc.AccessShare(share.Token, "wrong", ""); !hasAppCode(err, "invalid_share_password") {
		t.Fatalf("expected invalid share password before restart, got %v", err)
	}

	restarted := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	pastes, err := restarted.ListPastes(owner.User.ID, ListOptions{Query: "metadata"})
	if err != nil {
		t.Fatalf("list store-backed pastes after restart: %v", err)
	}
	if len(pastes) != 1 || pastes[0].ID != paste.ID || len(pastes[0].Attachments) != 1 || pastes[0].Attachments[0].ID != attachment.ID {
		t.Fatalf("expected restarted service to load paste and attachment metadata, got %#v", pastes)
	}
	shares, err := restarted.ListShares(owner.User.ID)
	if err != nil {
		t.Fatalf("list store-backed shares after restart: %v", err)
	}
	if len(shares) != 1 || shares[0].ID != share.ID {
		t.Fatalf("expected restarted service to load share metadata, got %#v", shares)
	}
	persistedShare, err := contentStores.ShareByID(context.Background(), share.ID)
	if err != nil {
		t.Fatalf("read persisted share metadata: %v", err)
	}
	if persistedShare.LastAccessFailure == nil {
		t.Fatalf("expected invalid access metadata to persist, got %#v", persistedShare)
	}

	revokedAt := now.Add(time.Minute)
	now = revokedAt
	if err := restarted.RevokeShare(owner.User.ID, share.ID); err != nil {
		t.Fatalf("revoke store-backed share after restart: %v", err)
	}
	restartedAgain := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	shares, err = restartedAgain.ListShares(owner.User.ID)
	if err != nil {
		t.Fatalf("list shares after revoke restart: %v", err)
	}
	if len(shares) != 1 || shares[0].RevokedAt == nil {
		t.Fatalf("expected share revocation to persist, got %#v", shares)
	}
}

func TestObjectStoreBackedAttachmentsSurviveServiceRestartAndCleanup(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	authStores := newMemoryAuthStores()
	contentStores := newMemoryContentStores()
	objectStore := newMemoryObjectStore()

	svc := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		Objects:      objectStore,
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	owner := registerTestUser(t, svc, "object-durable@example.com")
	firstPaste := createTestPaste(t, svc, owner.User.ID, PasteInput{Title: "first", Text: "one", ExpiresInSeconds: 3600})
	secondPaste := createTestPaste(t, svc, owner.User.ID, PasteInput{Title: "second", Text: "two", ExpiresInSeconds: 3600})
	content := []byte("durable object bytes")
	firstAttachment := addTestAttachment(t, svc, owner.User.ID, firstPaste.ID, "first.txt", content)
	secondAttachment := addTestAttachment(t, svc, owner.User.ID, secondPaste.ID, "second.txt", content)
	firstStored := svc.attachmentsByID[firstAttachment.ID]
	secondStored := svc.attachmentsByID[secondAttachment.ID]
	if firstStored.ObjectKey != secondStored.ObjectKey {
		t.Fatalf("expected duplicate content to share object key, got %q and %q", firstStored.ObjectKey, secondStored.ObjectKey)
	}
	if len(svc.objects) != 0 {
		t.Fatalf("injected object store should keep bytes out of service memory, got %d fallback objects", len(svc.objects))
	}

	restarted := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		Objects:      objectStore,
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	if refs := restarted.objectRefs[firstStored.ObjectKey]; refs != 2 {
		t.Fatalf("expected object references to rebuild from metadata after restart, got %d", refs)
	}
	if _, downloaded, err := restarted.DownloadAttachment(owner.User.ID, firstAttachment.ID); err != nil || string(downloaded) != string(content) {
		t.Fatalf("expected object-store attachment download after restart, got content=%q err=%v", string(downloaded), err)
	}

	if err := restarted.DeletePaste(owner.User.ID, firstPaste.ID); err != nil {
		t.Fatalf("delete first paste after restart: %v", err)
	}
	if _, err := restarted.RunCleanup(""); err != nil {
		t.Fatalf("cleanup first paste after restart: %v", err)
	}
	if refs := restarted.objectRefs[firstStored.ObjectKey]; refs != 1 {
		t.Fatalf("expected one object reference after first cleanup, got %d", refs)
	}
	if !objectStore.has(firstStored.ObjectKey) {
		t.Fatalf("expected shared object to remain while second attachment references it")
	}

	if err := restarted.DeletePaste(owner.User.ID, secondPaste.ID); err != nil {
		t.Fatalf("delete second paste after restart: %v", err)
	}
	if _, err := restarted.RunCleanup(""); err != nil {
		t.Fatalf("cleanup second paste after restart: %v", err)
	}
	if objectStore.has(firstStored.ObjectKey) {
		t.Fatalf("expected object store content to be deleted after last reference cleanup")
	}
}

func TestObjectStoreWriteFailureDoesNotCreateAttachmentMetadata(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	objectErr := errors.New("object write failed")
	authStores := newMemoryAuthStores()
	contentStores := newMemoryContentStores()
	objectStore := newMemoryObjectStore()
	objectStore.putErr = objectErr

	svc := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		Objects:      objectStore,
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	owner := registerTestUser(t, svc, "object-failure@example.com")
	paste := createTestPaste(t, svc, owner.User.ID, PasteInput{Title: "failure", Text: "base", ExpiresInSeconds: 3600})

	if _, err := svc.AddAttachment(owner.User.ID, paste.ID, "broken.txt", "text/plain", []byte("broken")); !errors.Is(err, objectErr) {
		t.Fatalf("expected object write error, got %v", err)
	}
	attachments, err := contentStores.ListAttachmentsByPaste(context.Background(), paste.ID)
	if err != nil {
		t.Fatalf("list attachments after object failure: %v", err)
	}
	if len(attachments) != 0 {
		t.Fatalf("failed object write must not persist attachment metadata, got %#v", attachments)
	}
	if quota, err := svc.Quota(owner.User.ID); err != nil || quota.DailyUploadBytes != int64(len("base")) {
		t.Fatalf("failed object write must not consume attachment daily quota, quota=%#v err=%v", quota, err)
	}
}

func TestAttachmentMetadataFailureRollsBackStoredObject(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	updateErr := errors.New("paste metadata unavailable")
	authStores := newMemoryAuthStores()
	contentStores := newMemoryContentStores()
	objectStore := newMemoryObjectStore()
	dailyMetrics := newMemoryDailyMetricStore()

	svc := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		Objects:      objectStore,
		DailyMetrics: dailyMetrics,
	})
	owner := registerTestUser(t, svc, "object-rollback@example.com")
	paste := createTestPaste(t, svc, owner.User.ID, PasteInput{Title: "rollback", Text: "base", ExpiresInSeconds: 3600})
	contentStores.updatePasteErr = updateErr

	if _, err := svc.AddAttachment(owner.User.ID, paste.ID, "rollback.txt", "text/plain", []byte("rollback")); !errors.Is(err, updateErr) {
		t.Fatalf("expected paste update error, got %v", err)
	}
	attachments, err := contentStores.ListAttachmentsByPaste(context.Background(), paste.ID)
	if err != nil {
		t.Fatalf("list attachments after rollback: %v", err)
	}
	if len(attachments) != 0 {
		t.Fatalf("metadata failure must roll back attachment row, got %#v", attachments)
	}
	if len(objectStore.objects) != 0 || len(svc.objectRefs) != 0 {
		t.Fatalf("metadata failure must roll back object storage, objects=%#v refs=%#v", objectStore.objects, svc.objectRefs)
	}
	if got, err := dailyMetrics.DailyMetric(context.Background(), owner.User.ID, "upload", now); err != nil || got != int64(len("base")) {
		t.Fatalf("metadata failure must not consume attachment daily quota, got %d err=%v", got, err)
	}
}

func TestAttachmentQuotaWriteFailureRollsBackMetadataObjectAndQueue(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	quotaErr := errors.New("daily metric unavailable")
	authStores := newMemoryAuthStores()
	contentStores := newMemoryContentStores()
	operationalStores := newMemoryOperationalStores()
	objectStore := newMemoryObjectStore()
	dailyMetrics := &failingAfterFirstMetricStore{delegate: newMemoryDailyMetricStore(), writeErr: quotaErr}

	svc := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		Operational:  operationalStores.operationalStores(),
		Objects:      objectStore,
		DailyMetrics: dailyMetrics,
	})
	owner := registerTestUser(t, svc, "quota-rollback@example.com")
	admin := seedAdminTestUser(t, svc, "quota-rollback-admin@example.com")
	paste := createTestPaste(t, svc, owner.User.ID, PasteInput{Title: "rollback", Text: "base", ExpiresInSeconds: 3600})

	if _, err := svc.AddAttachment(owner.User.ID, paste.ID, "blocked.exe", "application/octet-stream", []byte("rollback")); !errors.Is(err, quotaErr) {
		t.Fatalf("expected daily metric error, got %v", err)
	}
	attachments, err := contentStores.ListAttachmentsByPaste(context.Background(), paste.ID)
	if err != nil {
		t.Fatalf("list attachments after rollback: %v", err)
	}
	if len(attachments) != 0 {
		t.Fatalf("quota failure must roll back attachment row, got %#v", attachments)
	}
	if len(objectStore.objects) != 0 || len(svc.objectRefs) != 0 {
		t.Fatalf("quota failure must roll back object storage, objects=%#v refs=%#v", objectStore.objects, svc.objectRefs)
	}
	queues, err := svc.AdminQueues(admin.ID)
	if err != nil {
		t.Fatalf("admin queues after rollback: %v", err)
	}
	scanFailures, ok := queues["scanFailures"].([]*QueueItem)
	if !ok || len(scanFailures) != 0 {
		t.Fatalf("quota failure must roll back scan failure queue, got %#v", queues["scanFailures"])
	}
	scanJobs, ok := queues["scanJobs"].([]*QueueItem)
	if !ok || len(scanJobs) != 0 {
		t.Fatalf("quota failure must roll back scan job queue, got %#v", queues["scanJobs"])
	}
	if got, err := dailyMetrics.delegate.DailyMetric(context.Background(), owner.User.ID, "upload", now); err != nil || got != int64(len("base")) {
		t.Fatalf("quota failure must not consume attachment daily quota, got %d err=%v", got, err)
	}
}

func TestDeletePasteSchedulesDurableCleanupJob(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	authStores := newMemoryAuthStores()
	contentStores := newMemoryContentStores()
	operationalStores := newMemoryOperationalStores()

	svc := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		Operational:  operationalStores.operationalStores(),
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	admin := seedAdminTestUser(t, svc, "admin-cleanup-job@example.com")
	owner := registerTestUser(t, svc, "owner-cleanup-job@example.com")
	paste := createTestPaste(t, svc, owner.User.ID, PasteInput{Title: "cleanup", Text: "delete", ExpiresInSeconds: 3600})
	addTestAttachment(t, svc, owner.User.ID, paste.ID, "delete.txt", []byte("delete"))

	if err := svc.DeletePaste(owner.User.ID, paste.ID); err != nil {
		t.Fatalf("delete paste: %v", err)
	}

	queues, err := svc.AdminQueues(admin.ID)
	if err != nil {
		t.Fatalf("admin queues: %v", err)
	}
	cleanupJobs, ok := queues["cleanupJobs"].([]*QueueItem)
	if !ok || len(cleanupJobs) != 1 {
		t.Fatalf("expected cleanup job queue item, got %#v", queues["cleanupJobs"])
	}
	if cleanupJobs[0].Kind != "cleanup" || cleanupJobs[0].TargetID != paste.ID || cleanupJobs[0].Status != "pending" || !cleanupJobs[0].RunAfter.Equal(now) {
		t.Fatalf("unexpected cleanup job: %#v", cleanupJobs[0])
	}

	restarted := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		Operational:  operationalStores.operationalStores(),
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	queues, err = restarted.AdminQueues(admin.ID)
	if err != nil {
		t.Fatalf("admin queues after restart: %v", err)
	}
	cleanupJobs, ok = queues["cleanupJobs"].([]*QueueItem)
	if !ok || len(cleanupJobs) != 1 || cleanupJobs[0].TargetID != paste.ID {
		t.Fatalf("expected cleanup job to survive restart, got %#v", queues["cleanupJobs"])
	}
}

func TestStoreBackedOperationalStateSurvivesServiceRestart(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	authStores := newMemoryAuthStores()
	contentStores := newMemoryContentStores()
	operationalStores := newMemoryOperationalStores()
	auditStore := newMemoryAuditLogStore()

	svc := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		Operational:  operationalStores.operationalStores(),
		AuditLogs:    auditStore,
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	admin := seedAdminTestUser(t, svc, "admin-operational@example.com")
	owner := registerTestUser(t, svc, "owner-operational@example.com")
	paste := createTestPaste(t, svc, owner.User.ID, PasteInput{Title: "ops", Text: "state", ExpiresInSeconds: 3600})
	attachment := addTestAttachment(t, svc, owner.User.ID, paste.ID, "tool.exe", []byte("binary"))
	order, err := svc.CreateOrder(owner.User.ID, "stripe", "plus", "monthly")
	if err != nil {
		t.Fatalf("create store-backed order: %v", err)
	}
	if _, _, err := svc.ProcessBillingWebhook(BillingWebhookInput{
		Provider:       "stripe",
		EventType:      "payment.failed",
		OrderID:        order.ID,
		IdempotencyKey: "stripe-failed-operational",
	}); err != nil {
		t.Fatalf("process failed webhook: %v", err)
	}
	report, err := svc.Report(owner.User.ID, "share:ops", "abuse")
	if err != nil {
		t.Fatalf("create store-backed report: %v", err)
	}
	if _, err := svc.AdminResolveReport(admin.ID, report.ID, "dismissed"); err != nil {
		t.Fatalf("resolve store-backed report: %v", err)
	}

	restarted := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		Operational:  operationalStores.operationalStores(),
		AuditLogs:    auditStore,
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	orders, err := restarted.ListOrders(owner.User.ID)
	if err != nil {
		t.Fatalf("list store-backed orders after restart: %v", err)
	}
	if len(orders) != 1 || orders[0].ID != order.ID || orders[0].Status != "needs_review" {
		t.Fatalf("expected order status to survive restart, got %#v", orders)
	}
	events, err := restarted.AdminWebhookEvents(admin.ID)
	if err != nil {
		t.Fatalf("list store-backed webhook events after restart: %v", err)
	}
	if len(events) < 2 || !hasWebhookEvent(events, "stripe-failed-operational") {
		t.Fatalf("expected webhook events to survive restart, got %#v", events)
	}
	queues, err := restarted.AdminQueues(admin.ID)
	if err != nil {
		t.Fatalf("list store-backed queues after restart: %v", err)
	}
	reports, ok := queues["reports"].([]*Report)
	if !ok || len(reports) != 1 || reports[0].Status != "dismissed" {
		t.Fatalf("expected resolved report to survive restart, got %#v", queues["reports"])
	}
	scanFailures, ok := queues["scanFailures"].([]*QueueItem)
	if !ok || len(scanFailures) != 0 {
		t.Fatalf("expected no scan failure before worker scan, got %#v", queues["scanFailures"])
	}
	scanJobs, ok := queues["scanJobs"].([]*QueueItem)
	if !ok || len(scanJobs) != 1 || scanJobs[0].TargetID != attachment.ID {
		t.Fatalf("expected scan job to survive restart, got %#v", queues["scanJobs"])
	}
	mails, err := operationalStores.QueuedMails(context.Background(), 100)
	if err != nil {
		t.Fatalf("list queued mails: %v", err)
	}
	if len(mails) == 0 {
		t.Fatalf("expected auth and billing emails to be queued in operational store")
	}

	if _, err := restarted.AdminRetryScan(admin.ID, attachment.ID); err != nil {
		t.Fatalf("retry store-backed scan: %v", err)
	}
	restartedAgain := newTestServiceWithStorage(t, &now, Stores{
		Auth:         authStores.authStores(),
		Content:      contentStores.contentStores(),
		Operational:  operationalStores.operationalStores(),
		AuditLogs:    auditStore,
		DailyMetrics: newMemoryDailyMetricStore(),
	})
	queues, err = restartedAgain.AdminQueues(admin.ID)
	if err != nil {
		t.Fatalf("list queues after retry restart: %v", err)
	}
	scanFailures, ok = queues["scanFailures"].([]*QueueItem)
	if !ok || len(scanFailures) != 0 {
		t.Fatalf("expected scan retry to delete persisted queue item, got %#v", queues["scanFailures"])
	}
	scanJobs, ok = queues["scanJobs"].([]*QueueItem)
	if !ok || len(scanJobs) != 1 || scanJobs[0].TargetID != attachment.ID {
		t.Fatalf("expected scan retry to queue persisted scan job, got %#v", queues["scanJobs"])
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

	if attachment.ScanStatus != "pending" {
		t.Fatalf("expected executable upload to start pending scan, got %q", attachment.ScanStatus)
	}
	if _, _, err := svc.DownloadAttachment(owner.User.ID, attachment.ID); err != nil {
		t.Fatalf("owner should be able to download pending attachment: %v", err)
	}
	if _, _, err := svc.DownloadSharedAttachment(share.Token, "", attachment.ID, ""); !hasAppCode(err, "scan_not_clean") {
		t.Fatalf("expected public download to require clean scan while pending, got %v", err)
	}
	if err := svc.RunAttachmentScan(staticScanner{result: ScanResult{Status: "scan_failed", Risk: "scanner_timeout"}}, attachment.ID); err != nil {
		t.Fatalf("run failed scan: %v", err)
	}
	if _, _, err := svc.DownloadAttachment(owner.User.ID, attachment.ID); err != nil {
		t.Fatalf("owner should be able to download scan-failed attachment: %v", err)
	}
	if _, _, err := svc.DownloadSharedAttachment(share.Token, "", attachment.ID, ""); !hasAppCode(err, "scan_not_clean") {
		t.Fatalf("expected public download to require clean scan after failed scan, got %v", err)
	}
	if _, err := svc.AdminRetryScan(admin.ID, attachment.ID); err != nil {
		t.Fatalf("admin retry scan: %v", err)
	}
	if err := svc.RunAttachmentScan(staticScanner{result: ScanResult{Status: "clean"}}, attachment.ID); err != nil {
		t.Fatalf("run clean retry scan: %v", err)
	}
	if _, _, err := svc.DownloadSharedAttachment(share.Token, "", attachment.ID, ""); err != nil {
		t.Fatalf("public download after retry scan: %v", err)
	}
}

func TestMaliciousScanBlocksOwnerAndPublicDownloads(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	owner := registerTestUser(t, svc, "malware-owner@example.com")
	paste := createTestPaste(t, svc, owner.User.ID, PasteInput{Title: "malware", Text: "file", ExpiresInSeconds: 3600})
	attachment := addTestAttachment(t, svc, owner.User.ID, paste.ID, "payload.bin", []byte("binary"))
	share := createTestShare(t, svc, owner.User.ID, paste.ID, ShareInput{ExpiresInSeconds: 3600})

	if err := svc.RunAttachmentScan(staticScanner{result: ScanResult{Status: "malicious", Risk: "eicar_test_file"}}, attachment.ID); err != nil {
		t.Fatalf("run malicious scan: %v", err)
	}
	if _, _, err := svc.DownloadAttachment(owner.User.ID, attachment.ID); !hasAppCode(err, "malicious_file") {
		t.Fatalf("expected owner download to block malicious file, got %v", err)
	}
	if _, _, err := svc.DownloadSharedAttachment(share.Token, "", attachment.ID, ""); !hasAppCode(err, "scan_not_clean") {
		t.Fatalf("expected public download to block malicious file through clean-scan gate, got %v", err)
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
	_, ok := queues["scanFailures"].([]*QueueItem)
	if !ok {
		t.Fatalf("expected scan failure queue in admin queues, got %#v", queues)
	}
	scanJobs, ok := queues["scanJobs"].([]*QueueItem)
	if !ok || len(scanJobs) == 0 {
		t.Fatalf("expected scan job queue to expose pending executable attachment, got %#v", queues["scanJobs"])
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
	cfg.DevAuthTokens = true
	svc := New(cfg)
	svc.now = func() time.Time { return *now }
	return svc
}

func newTestServiceWithDailyMetrics(t *testing.T, now *time.Time, store DailyMetricStore) *Service {
	t.Helper()
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	svc := NewWithDailyMetricStore(cfg, store)
	svc.now = func() time.Time { return *now }
	return svc
}

func newTestServiceWithAuthStores(t *testing.T, now *time.Time, authStores AuthStores) *Service {
	t.Helper()
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	svc := NewWithStores(cfg, authStores, nil)
	svc.now = func() time.Time { return *now }
	return svc
}

func newTestServiceWithStorage(t *testing.T, now *time.Time, stores Stores) *Service {
	t.Helper()
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	svc, err := NewWithStorage(context.Background(), cfg, stores)
	if err != nil {
		t.Fatalf("new service with storage: %v", err)
	}
	svc.now = func() time.Time { return *now }
	return svc
}

type memoryCatalogStore struct {
	catalog plans.Catalog
}

func (s memoryCatalogStore) Catalog(_ context.Context) (plans.Catalog, error) {
	return cloneCatalog(s.catalog), nil
}

type memoryAuditLogStore struct {
	logs []AuditLog
}

func newMemoryAuditLogStore() *memoryAuditLogStore {
	return &memoryAuditLogStore{logs: []AuditLog{}}
}

func (s *memoryAuditLogStore) RecordAuditLog(_ context.Context, log AuditLog) error {
	s.logs = append(s.logs, log)
	return nil
}

func (s *memoryAuditLogStore) AuditLogs(_ context.Context, limit int) ([]AuditLog, error) {
	if limit <= 0 || limit > len(s.logs) {
		limit = len(s.logs)
	}
	out := make([]AuditLog, 0, limit)
	for i := len(s.logs) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.logs[i])
	}
	return out, nil
}

type memoryContentStores struct {
	pastes         map[string]Paste
	attachments    map[string]Attachment
	shares         map[string]Share
	shareTokens    map[string]string
	updatePasteErr error
}

func newMemoryContentStores() *memoryContentStores {
	return &memoryContentStores{
		pastes:      map[string]Paste{},
		attachments: map[string]Attachment{},
		shares:      map[string]Share{},
		shareTokens: map[string]string{},
	}
}

func (s *memoryContentStores) contentStores() ContentStores {
	return ContentStores{
		Pastes:      s,
		Attachments: s,
		Shares:      s,
	}
}

func (s *memoryContentStores) CreatePaste(_ context.Context, paste Paste) error {
	if _, ok := s.pastes[paste.ID]; ok {
		return ErrStoreConflict
	}
	s.pastes[paste.ID] = clonePasteForStore(paste)
	return nil
}

func (s *memoryContentStores) PasteByID(_ context.Context, id string) (Paste, error) {
	paste, ok := s.pastes[id]
	if !ok {
		return Paste{}, ErrStoreNotFound
	}
	return clonePasteForStore(paste), nil
}

func (s *memoryContentStores) ListPastes(_ context.Context) ([]Paste, error) {
	out := make([]Paste, 0, len(s.pastes))
	for _, paste := range s.pastes {
		out = append(out, clonePasteForStore(paste))
	}
	return out, nil
}

func (s *memoryContentStores) ListPastesByUser(_ context.Context, userID string) ([]Paste, error) {
	out := []Paste{}
	for _, paste := range s.pastes {
		if paste.UserID == userID {
			out = append(out, clonePasteForStore(paste))
		}
	}
	return out, nil
}

func (s *memoryContentStores) UpdatePaste(_ context.Context, paste Paste) error {
	if s.updatePasteErr != nil {
		return s.updatePasteErr
	}
	if _, ok := s.pastes[paste.ID]; !ok {
		return ErrStoreNotFound
	}
	s.pastes[paste.ID] = clonePasteForStore(paste)
	return nil
}

func (s *memoryContentStores) CreateAttachment(_ context.Context, attachment Attachment) error {
	if _, ok := s.attachments[attachment.ID]; ok {
		return ErrStoreConflict
	}
	s.attachments[attachment.ID] = cloneAttachmentForStore(attachment)
	return nil
}

func (s *memoryContentStores) AttachmentByID(_ context.Context, id string) (Attachment, error) {
	attachment, ok := s.attachments[id]
	if !ok {
		return Attachment{}, ErrStoreNotFound
	}
	return cloneAttachmentForStore(attachment), nil
}

func (s *memoryContentStores) ListAttachments(_ context.Context) ([]Attachment, error) {
	out := make([]Attachment, 0, len(s.attachments))
	for _, attachment := range s.attachments {
		out = append(out, cloneAttachmentForStore(attachment))
	}
	return out, nil
}

func (s *memoryContentStores) ListAttachmentsByPaste(_ context.Context, pasteID string) ([]Attachment, error) {
	out := []Attachment{}
	for _, attachment := range s.attachments {
		if attachment.PasteID == pasteID {
			out = append(out, cloneAttachmentForStore(attachment))
		}
	}
	return out, nil
}

func (s *memoryContentStores) UpdateAttachment(_ context.Context, attachment Attachment) error {
	if _, ok := s.attachments[attachment.ID]; !ok {
		return ErrStoreNotFound
	}
	s.attachments[attachment.ID] = cloneAttachmentForStore(attachment)
	return nil
}

func (s *memoryContentStores) DeleteAttachment(_ context.Context, id string) error {
	if _, ok := s.attachments[id]; !ok {
		return ErrStoreNotFound
	}
	delete(s.attachments, id)
	return nil
}

func (s *memoryContentStores) CreateShare(_ context.Context, share Share) error {
	if _, ok := s.shares[share.ID]; ok {
		return ErrStoreConflict
	}
	if _, ok := s.shareTokens[share.TokenHash]; ok {
		return ErrStoreConflict
	}
	s.shares[share.ID] = share
	s.shareTokens[share.TokenHash] = share.ID
	return nil
}

func (s *memoryContentStores) ShareByID(_ context.Context, id string) (Share, error) {
	share, ok := s.shares[id]
	if !ok {
		return Share{}, ErrStoreNotFound
	}
	return share, nil
}

func (s *memoryContentStores) ShareByTokenHash(_ context.Context, tokenHash string) (Share, error) {
	shareID := s.shareTokens[tokenHash]
	if shareID == "" {
		return Share{}, ErrStoreNotFound
	}
	return s.ShareByID(context.Background(), shareID)
}

func (s *memoryContentStores) ListShares(_ context.Context) ([]Share, error) {
	out := make([]Share, 0, len(s.shares))
	for _, share := range s.shares {
		out = append(out, share)
	}
	return out, nil
}

func (s *memoryContentStores) ListSharesByUser(_ context.Context, userID string) ([]Share, error) {
	out := []Share{}
	for _, share := range s.shares {
		if share.UserID == userID {
			out = append(out, share)
		}
	}
	return out, nil
}

func (s *memoryContentStores) UpdateShare(_ context.Context, share Share) error {
	if _, ok := s.shares[share.ID]; !ok {
		return ErrStoreNotFound
	}
	s.shares[share.ID] = share
	s.shareTokens[share.TokenHash] = share.ID
	return nil
}

func clonePasteForStore(paste Paste) Paste {
	cloned := paste
	cloned.Tags = append([]string(nil), paste.Tags...)
	cloned.AttachmentIDs = append([]string(nil), paste.AttachmentIDs...)
	return cloned
}

func cloneAttachmentForStore(attachment Attachment) Attachment {
	cloned := attachment
	cloned.Content = append([]byte(nil), attachment.Content...)
	return cloned
}

type memoryObjectStore struct {
	objects map[string][]byte
	putErr  error
	getErr  error
	delErr  error
}

func newMemoryObjectStore() *memoryObjectStore {
	return &memoryObjectStore{objects: map[string][]byte{}}
}

func (s *memoryObjectStore) PutObject(_ context.Context, key string, content []byte, _ string) error {
	if s.putErr != nil {
		return s.putErr
	}
	s.objects[key] = append([]byte(nil), content...)
	return nil
}

func (s *memoryObjectStore) GetObject(_ context.Context, key string) ([]byte, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	content, ok := s.objects[key]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return append([]byte(nil), content...), nil
}

func (s *memoryObjectStore) DeleteObject(_ context.Context, key string) error {
	if s.delErr != nil {
		return s.delErr
	}
	if _, ok := s.objects[key]; !ok {
		return ErrObjectNotFound
	}
	delete(s.objects, key)
	return nil
}

func (s *memoryObjectStore) has(key string) bool {
	_, ok := s.objects[key]
	return ok
}

type memoryOperationalStores struct {
	orders             map[string]Order
	webhookEvents      map[string]WebhookEvent
	webhookEventsByKey map[string]string
	reports            map[string]Report
	queues             map[string]QueueItem
	mails              map[string]Mail
}

func newMemoryOperationalStores() *memoryOperationalStores {
	return &memoryOperationalStores{
		orders:             map[string]Order{},
		webhookEvents:      map[string]WebhookEvent{},
		webhookEventsByKey: map[string]string{},
		reports:            map[string]Report{},
		queues:             map[string]QueueItem{},
		mails:              map[string]Mail{},
	}
}

func (s *memoryOperationalStores) operationalStores() OperationalStores {
	return OperationalStores{
		Orders:        s,
		WebhookEvents: s,
		Reports:       s,
		Queues:        s,
		Mails:         s,
	}
}

func (s *memoryOperationalStores) CreateOrder(_ context.Context, order Order) error {
	if _, ok := s.orders[order.ID]; ok {
		return ErrStoreConflict
	}
	s.orders[order.ID] = order
	return nil
}

func (s *memoryOperationalStores) OrderByID(_ context.Context, id string) (Order, error) {
	order, ok := s.orders[id]
	if !ok {
		return Order{}, ErrStoreNotFound
	}
	return order, nil
}

func (s *memoryOperationalStores) ListOrders(_ context.Context) ([]Order, error) {
	out := make([]Order, 0, len(s.orders))
	for _, order := range s.orders {
		out = append(out, order)
	}
	return out, nil
}

func (s *memoryOperationalStores) ListOrdersByUser(_ context.Context, userID string) ([]Order, error) {
	out := []Order{}
	for _, order := range s.orders {
		if order.UserID == userID {
			out = append(out, order)
		}
	}
	return out, nil
}

func (s *memoryOperationalStores) UpdateOrder(_ context.Context, order Order) error {
	if _, ok := s.orders[order.ID]; !ok {
		return ErrStoreNotFound
	}
	s.orders[order.ID] = order
	return nil
}

func (s *memoryOperationalStores) CreateWebhookEvent(_ context.Context, event WebhookEvent) error {
	if _, ok := s.webhookEvents[event.ID]; ok {
		return ErrStoreConflict
	}
	if _, ok := s.webhookEventsByKey[event.IdempotencyKey]; ok {
		return ErrStoreConflict
	}
	event.Metadata = cloneMetadata(event.Metadata)
	s.webhookEvents[event.ID] = event
	s.webhookEventsByKey[event.IdempotencyKey] = event.ID
	return nil
}

func (s *memoryOperationalStores) WebhookEventByID(_ context.Context, id string) (WebhookEvent, error) {
	event, ok := s.webhookEvents[id]
	if !ok {
		return WebhookEvent{}, ErrStoreNotFound
	}
	event.Metadata = cloneMetadata(event.Metadata)
	return event, nil
}

func (s *memoryOperationalStores) WebhookEventByIdempotencyKey(_ context.Context, idempotencyKey string) (WebhookEvent, error) {
	id := s.webhookEventsByKey[idempotencyKey]
	if id == "" {
		return WebhookEvent{}, ErrStoreNotFound
	}
	return s.WebhookEventByID(context.Background(), id)
}

func (s *memoryOperationalStores) ListWebhookEvents(_ context.Context) ([]WebhookEvent, error) {
	out := make([]WebhookEvent, 0, len(s.webhookEvents))
	for _, event := range s.webhookEvents {
		event.Metadata = cloneMetadata(event.Metadata)
		out = append(out, event)
	}
	return out, nil
}

func (s *memoryOperationalStores) UpdateWebhookEventProcessed(_ context.Context, id string, processed bool) error {
	event, ok := s.webhookEvents[id]
	if !ok {
		return ErrStoreNotFound
	}
	event.Processed = processed
	s.webhookEvents[id] = event
	return nil
}

func (s *memoryOperationalStores) CreateReport(_ context.Context, report Report) error {
	if _, ok := s.reports[report.ID]; ok {
		return ErrStoreConflict
	}
	s.reports[report.ID] = report
	return nil
}

func (s *memoryOperationalStores) ReportByID(_ context.Context, id string) (Report, error) {
	report, ok := s.reports[id]
	if !ok {
		return Report{}, ErrStoreNotFound
	}
	return report, nil
}

func (s *memoryOperationalStores) ListReports(_ context.Context) ([]Report, error) {
	out := make([]Report, 0, len(s.reports))
	for _, report := range s.reports {
		out = append(out, report)
	}
	return out, nil
}

func (s *memoryOperationalStores) UpdateReportStatus(_ context.Context, id string, status string) error {
	report, ok := s.reports[id]
	if !ok {
		return ErrStoreNotFound
	}
	report.Status = status
	s.reports[id] = report
	return nil
}

func (s *memoryOperationalStores) CreateQueueItem(_ context.Context, item QueueItem) error {
	if _, ok := s.queues[item.ID]; ok {
		return ErrStoreConflict
	}
	s.queues[item.ID] = item
	return nil
}

func (s *memoryOperationalStores) ListQueueItemsByKind(_ context.Context, kind string) ([]QueueItem, error) {
	out := []QueueItem{}
	for _, item := range s.queues {
		if item.Kind == kind {
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *memoryOperationalStores) ListQueueItemsByStatus(_ context.Context, status string, limit int) ([]QueueItem, error) {
	out := []QueueItem{}
	for _, item := range s.queues {
		if item.Status == status {
			out = append(out, item)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *memoryOperationalStores) DeleteQueueItemsByKindTarget(_ context.Context, kind string, targetID string) error {
	for id, item := range s.queues {
		if item.Kind == kind && item.TargetID == targetID {
			delete(s.queues, id)
		}
	}
	return nil
}

func (s *memoryOperationalStores) QueueMail(_ context.Context, mail Mail) error {
	if _, ok := s.mails[mail.ID]; ok {
		return ErrStoreConflict
	}
	s.mails[mail.ID] = mail
	return nil
}

func (s *memoryOperationalStores) QueuedMails(_ context.Context, limit int) ([]Mail, error) {
	out := make([]Mail, 0, len(s.mails))
	for _, mail := range s.mails {
		out = append(out, mail)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
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

type failingAfterFirstMetricStore struct {
	delegate *memoryDailyMetricStore
	writeErr error
	writes   int
}

func (s *failingAfterFirstMetricStore) DailyMetric(ctx context.Context, userID string, kind string, day time.Time) (int64, error) {
	return s.delegate.DailyMetric(ctx, userID, kind, day)
}

func (s *failingAfterFirstMetricStore) RecordDailyMetric(ctx context.Context, userID string, kind string, day time.Time, bytes int64) error {
	s.writes++
	if s.writes > 1 {
		return s.writeErr
	}
	return s.delegate.RecordDailyMetric(ctx, userID, kind, day, bytes)
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

func runCleanScan(t *testing.T, svc *Service, attachmentID string) {
	t.Helper()
	if err := svc.RunAttachmentScan(staticScanner{result: ScanResult{Status: "clean"}}, attachmentID); err != nil {
		t.Fatalf("run clean scan: %v", err)
	}
}

type staticScanner struct {
	result ScanResult
	err    error
}

func (s staticScanner) Scan(_ context.Context, _ string, _ string, _ []byte) (ScanResult, error) {
	return s.result, s.err
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

func hasWebhookEvent(events []WebhookEvent, idempotencyKey string) bool {
	for _, event := range events {
		if event.IdempotencyKey == idempotencyKey {
			return true
		}
	}
	return false
}

func hasUserEmail(users []UserView, email string) bool {
	for _, user := range users {
		if user.Email == email {
			return true
		}
	}
	return false
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

func (s *memoryAuthStores) ListUsers(_ context.Context) ([]User, error) {
	users := make([]User, 0, len(s.usersByID))
	for _, user := range s.usersByID {
		users = append(users, user)
	}
	return users, nil
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
