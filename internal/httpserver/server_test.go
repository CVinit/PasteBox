package httpserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

func TestHealthEndpoint(t *testing.T) {
	handler := New(config.FromEnv(), slog.New(slog.NewTextHandler(testWriter{t: t}, nil)))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", res.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body["app"] != "PasteBox" {
		t.Fatalf("expected PasteBox app name, got %q", body["app"])
	}
}

func TestPlanCatalogEndpoint(t *testing.T) {
	handler := New(config.FromEnv(), slog.New(slog.NewTextHandler(testWriter{t: t}, nil)))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/plans", nil)
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", res.Code)
	}

	var body struct {
		Plans []struct {
			ID string `json:"id"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode plan catalog: %v", err)
	}
	if len(body.Plans) != 3 {
		t.Fatalf("expected 3 plans, got %d", len(body.Plans))
	}
	if body.Plans[0].ID != "free" {
		t.Fatalf("expected free plan first, got %q", body.Plans[0].ID)
	}
}

func TestAuthPasteUploadShareAndQuotaHTTPContracts(t *testing.T) {
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), app.New(cfg))
	client := newHTTPTestClient(t, handler)

	register := client.json(http.MethodPost, "/api/v1/auth/register", `{"email":"owner@example.com","password":"password123","displayName":"Owner"}`)
	assertStatus(t, register, http.StatusCreated)
	sessionCookie := register.Result().Cookies()[0]
	if sessionCookie.Name != sessionCookieName || !sessionCookie.HttpOnly {
		t.Fatalf("expected HttpOnly session cookie, got %#v", sessionCookie)
	}
	var authBody struct {
		User                      app.UserView `json:"user"`
		SessionExpiresAt          string       `json:"sessionExpiresAt"`
		DevEmailVerificationToken string       `json:"devEmailVerificationToken"`
	}
	decodeResponse(t, register, &authBody)
	if authBody.User.Email != "owner@example.com" || authBody.User.PlanID != "free" || authBody.User.EmailVerified || authBody.SessionExpiresAt == "" || authBody.DevEmailVerificationToken == "" {
		t.Fatalf("unexpected register body: %#v", authBody)
	}

	verify := client.json(http.MethodPost, "/api/v1/auth/email-verification/finish", `{"token":"`+authBody.DevEmailVerificationToken+`"}`)
	assertStatus(t, verify, http.StatusOK)
	var verifiedUser app.UserView
	decodeResponse(t, verify, &verifiedUser)
	if !verifiedUser.EmailVerified {
		t.Fatalf("expected verified user, got %#v", verifiedUser)
	}

	quota := client.json(http.MethodGet, "/api/v1/quota", "")
	assertStatus(t, quota, http.StatusOK)
	var quotaBody app.QuotaView
	decodeResponse(t, quota, &quotaBody)
	if quotaBody.Plan.ID != "free" || quotaBody.ActivePasteCount != 0 {
		t.Fatalf("unexpected quota body: %#v", quotaBody)
	}

	createPaste := client.json(http.MethodPost, "/api/v1/pastes", `{"title":"Contract","text":"hello","tags":["alpha"],"pinned":true,"favorite":false,"expiresInSeconds":3600}`)
	assertStatus(t, createPaste, http.StatusCreated)
	var paste app.PasteView
	decodeResponse(t, createPaste, &paste)
	if paste.ID == "" || paste.Title != "Contract" || paste.Text != "hello" || len(paste.Tags) != 1 || paste.Tags[0] != "alpha" {
		t.Fatalf("unexpected paste body: %#v", paste)
	}

	upload := client.multipart("/api/v1/pastes/"+paste.ID+"/attachments", "file", "note.txt", []byte("attachment"))
	assertStatus(t, upload, http.StatusCreated)
	var attachment app.AttachmentView
	decodeResponse(t, upload, &attachment)
	wantSHA := sha256.Sum256([]byte("attachment"))
	if attachment.ID == "" || attachment.PasteID != paste.ID || attachment.Size != int64(len("attachment")) || attachment.SHA256 != hex.EncodeToString(wantSHA[:]) {
		t.Fatalf("unexpected attachment body: %#v", attachment)
	}

	createShare := client.json(http.MethodPost, "/api/v1/pastes/"+paste.ID+"/shares", `{"password":"pw","loginRequired":false,"maxVisits":2,"maxDownloads":1,"expiresInSeconds":1800}`)
	assertStatus(t, createShare, http.StatusCreated)
	var share app.ShareView
	decodeResponse(t, createShare, &share)
	if share.ID == "" || share.Token == "" || share.URL == "" || !share.HasPassword || share.MaxVisits != 2 || share.MaxDownloads != 1 {
		t.Fatalf("unexpected share body: %#v", share)
	}

	anonymous := newHTTPTestClient(t, handler)
	accessShare := anonymous.json(http.MethodPost, "/api/v1/shares/"+share.Token+"/access", `{"password":"pw"}`)
	assertStatus(t, accessShare, http.StatusOK)
	var shareAccess struct {
		Paste app.PasteView `json:"paste"`
		Share app.ShareView `json:"share"`
	}
	decodeResponse(t, accessShare, &shareAccess)
	if shareAccess.Paste.ID != paste.ID || shareAccess.Share.VisitCount != 1 {
		t.Fatalf("unexpected share access body: %#v", shareAccess)
	}
}

func TestAdminHTTPContractsWriteAuditLogs(t *testing.T) {
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	service := app.New(cfg)
	if _, err := service.SeedAdmin("admin@example.com", "password123"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)

	owner := newHTTPTestClient(t, handler)
	register := owner.json(http.MethodPost, "/api/v1/auth/register", `{"email":"owner-admin@example.com","password":"password123","displayName":"Owner"}`)
	assertStatus(t, register, http.StatusCreated)
	var ownerAuth struct {
		User                      app.UserView `json:"user"`
		DevEmailVerificationToken string       `json:"devEmailVerificationToken"`
	}
	decodeResponse(t, register, &ownerAuth)
	verify := owner.json(http.MethodPost, "/api/v1/auth/email-verification/finish", `{"token":"`+ownerAuth.DevEmailVerificationToken+`"}`)
	assertStatus(t, verify, http.StatusOK)

	createPaste := owner.json(http.MethodPost, "/api/v1/pastes", `{"title":"Admin target","text":"binary","tags":[],"expiresInSeconds":3600}`)
	assertStatus(t, createPaste, http.StatusCreated)
	var paste app.PasteView
	decodeResponse(t, createPaste, &paste)

	upload := owner.multipart("/api/v1/pastes/"+paste.ID+"/attachments", "file", "tool.exe", []byte("binary"))
	assertStatus(t, upload, http.StatusCreated)
	var attachment app.AttachmentView
	decodeResponse(t, upload, &attachment)
	if attachment.ScanStatus != "scan_failed" {
		t.Fatalf("expected executable upload to create scan failure, got %#v", attachment)
	}

	admin := newHTTPTestClient(t, handler)
	login := admin.json(http.MethodPost, "/api/v1/auth/login", `{"email":"admin@example.com","password":"password123"}`)
	assertStatus(t, login, http.StatusOK)

	dashboard := admin.json(http.MethodGet, "/api/v1/admin/dashboard", "")
	assertStatus(t, dashboard, http.StatusOK)
	var dashboardBody map[string]any
	decodeResponse(t, dashboard, &dashboardBody)
	if dashboardBody["scanFailureQueueDepth"] == float64(0) {
		t.Fatalf("expected scan failure queue depth in dashboard, got %#v", dashboardBody)
	}

	setPlan := admin.json(http.MethodPatch, "/api/v1/admin/users/"+ownerAuth.User.ID+"/plan", `{"planId":"plus"}`)
	assertStatus(t, setPlan, http.StatusOK)
	var updatedUser app.UserView
	decodeResponse(t, setPlan, &updatedUser)
	if updatedUser.PlanID != "plus" {
		t.Fatalf("expected plus plan, got %#v", updatedUser)
	}

	retryScan := admin.json(http.MethodPost, "/api/v1/admin/attachments/"+attachment.ID+"/retry-scan", "")
	assertStatus(t, retryScan, http.StatusOK)
	var retriedAttachment app.AttachmentView
	decodeResponse(t, retryScan, &retriedAttachment)
	if retriedAttachment.ScanStatus != "clean" {
		t.Fatalf("expected retried scan to be clean, got %#v", retriedAttachment)
	}

	auditLogs := admin.json(http.MethodGet, "/api/v1/admin/audit-logs", "")
	assertStatus(t, auditLogs, http.StatusOK)
	var auditBody struct {
		AuditLogs []app.AuditLog `json:"auditLogs"`
	}
	decodeResponse(t, auditLogs, &auditBody)
	if !containsAuditAction(auditBody.AuditLogs, "admin.user_plan_set") || !containsAuditAction(auditBody.AuditLogs, "admin.scan_retry") {
		t.Fatalf("expected admin audit actions, got %#v", auditBody.AuditLogs)
	}
}

func TestOAuthWebhookReplayAndReportHTTPContracts(t *testing.T) {
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	service := app.New(cfg)
	if _, err := service.SeedAdmin("admin2@example.com", "password123"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)

	user := newHTTPTestClient(t, handler)
	oauth := user.json(http.MethodPost, "/api/v1/auth/google", `{"email":"oauth@example.com","displayName":"OAuth User","googleSubject":"google-subject"}`)
	assertStatus(t, oauth, http.StatusOK)
	var oauthBody struct {
		User app.UserView `json:"user"`
	}
	decodeResponse(t, oauth, &oauthBody)
	if !oauthBody.User.EmailVerified || oauthBody.User.DisplayName != "OAuth User" {
		t.Fatalf("unexpected oauth body: %#v", oauthBody)
	}

	orderRes := user.json(http.MethodPost, "/api/v1/billing/orders", `{"provider":"stripe","planId":"plus","period":"monthly"}`)
	assertStatus(t, orderRes, http.StatusCreated)
	var order app.Order
	decodeResponse(t, orderRes, &order)

	webhook := user.json(http.MethodPost, "/api/v1/billing/webhooks/stripe", `{"eventType":"checkout.session.completed","orderId":"`+order.ID+`","txId":"tx-http","idempotencyKey":"stripe-http-1"}`)
	assertStatus(t, webhook, http.StatusOK)
	var webhookBody struct {
		WebhookEvent app.WebhookEvent `json:"webhookEvent"`
		Order        *app.Order       `json:"order"`
	}
	decodeResponse(t, webhook, &webhookBody)
	if webhookBody.Order == nil || webhookBody.Order.Status != "paid" || webhookBody.WebhookEvent.IdempotencyKey != "stripe-http-1" {
		t.Fatalf("unexpected webhook body: %#v", webhookBody)
	}

	report := user.json(http.MethodPost, "/api/v1/reports", `{"target":"share:abc","reason":"abuse"}`)
	assertStatus(t, report, http.StatusCreated)
	var reportBody app.Report
	decodeResponse(t, report, &reportBody)

	admin := newHTTPTestClient(t, handler)
	login := admin.json(http.MethodPost, "/api/v1/auth/login", `{"email":"admin2@example.com","password":"password123"}`)
	assertStatus(t, login, http.StatusOK)

	replay := admin.json(http.MethodPost, "/api/v1/admin/webhook-events/"+webhookBody.WebhookEvent.ID+"/replay", "")
	assertStatus(t, replay, http.StatusOK)
	var replayEvent app.WebhookEvent
	decodeResponse(t, replay, &replayEvent)
	if replayEvent.ID == "" {
		t.Fatalf("expected replay event, got %#v", replayEvent)
	}

	resolve := admin.json(http.MethodPost, "/api/v1/admin/reports/"+reportBody.ID+"/status", `{"status":"resolved"}`)
	assertStatus(t, resolve, http.StatusOK)
	var resolvedReport app.Report
	decodeResponse(t, resolve, &resolvedReport)
	if resolvedReport.Status != "resolved" {
		t.Fatalf("expected resolved report, got %#v", resolvedReport)
	}
}

type httpTestClient struct {
	t       *testing.T
	handler http.Handler
	cookies map[string]*http.Cookie
}

func newHTTPTestClient(t *testing.T, handler http.Handler) *httpTestClient {
	t.Helper()
	return &httpTestClient{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
}

func (c *httpTestClient) json(method string, path string, body string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	return c.do(req)
}

func (c *httpTestClient) multipart(path string, fieldName string, fileName string, content []byte) *httptest.ResponseRecorder {
	c.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(fieldName, fileName)
	if err != nil {
		c.t.Fatalf("create multipart field: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		c.t.Fatalf("write multipart field: %v", err)
	}
	if err := writer.Close(); err != nil {
		c.t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	return c.do(req)
}

func (c *httpTestClient) do(req *http.Request) *httptest.ResponseRecorder {
	c.t.Helper()
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}
	res := httptest.NewRecorder()
	c.handler.ServeHTTP(res, req)
	for _, cookie := range res.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
			continue
		}
		c.cookies[cookie.Name] = cookie
	}
	return res
}

func assertStatus(t *testing.T, res *httptest.ResponseRecorder, status int) {
	t.Helper()
	if res.Code != status {
		t.Fatalf("expected HTTP %d, got %d: %s", status, res.Code, res.Body.String())
	}
}

func decodeResponse(t *testing.T, res *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(res.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response body %q: %v", res.Body.String(), err)
	}
}

func containsAuditAction(logs []app.AuditLog, action string) bool {
	for _, log := range logs {
		if log.Action == action {
			return true
		}
	}
	return false
}

type testWriter struct {
	t *testing.T
}

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	return len(p), nil
}
