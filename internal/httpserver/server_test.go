package httpserver

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestReadinessEndpoints(t *testing.T) {
	cfg := config.FromEnv()
	cfg.AppEnv = "production"
	handler := New(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)))

	readyz := httptest.NewRecorder()
	handler.ServeHTTP(readyz, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assertStatus(t, readyz, http.StatusOK)
	var rootBody ReadinessReport
	decodeResponse(t, readyz, &rootBody)
	if rootBody.Status != "ready" || len(rootBody.Components) == 0 {
		t.Fatalf("expected root readiness status, got %#v", rootBody)
	}

	apiReady := httptest.NewRecorder()
	handler.ServeHTTP(apiReady, httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil))
	assertStatus(t, apiReady, http.StatusOK)
	var apiBody ReadinessReport
	decodeResponse(t, apiReady, &apiBody)
	if apiBody.App != "PasteBox" || apiBody.Env != "production" || apiBody.Status != "ready" {
		t.Fatalf("unexpected api readiness body: %#v", apiBody)
	}
}

func TestReadinessEndpointReturnsUnavailableForFailedDependency(t *testing.T) {
	cfg := config.FromEnv()
	handler := NewWithServiceAndReadiness(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), app.New(cfg), func(context.Context) []ReadinessComponent {
		return []ReadinessComponent{{Name: "database", Status: "fail", Message: "down"}}
	})

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assertStatus(t, res, http.StatusServiceUnavailable)
	var body ReadinessReport
	decodeResponse(t, res, &body)
	if body.Status != "not_ready" || len(body.Components) != 1 || body.Components[0].Name != "database" {
		t.Fatalf("unexpected failed readiness body: %#v", body)
	}
}

func TestMetricsEndpointRequiresBearerToken(t *testing.T) {
	cfg := config.FromEnv()
	cfg.MetricsToken = "test-metrics-token"
	handler := New(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)))

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assertStatus(t, res, http.StatusUnauthorized)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	assertStatus(t, res, http.StatusUnauthorized)
}

func TestMetricsEndpointExposesReadinessHTTPAndOperationalGauges(t *testing.T) {
	cfg := config.FromEnv()
	cfg.AppEnv = "production"
	cfg.MetricsToken = "test-metrics-token"
	handler := NewWithServiceAndReadiness(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), app.New(cfg), func(context.Context) []ReadinessComponent {
		return []ReadinessComponent{
			{Name: "database", Status: "ok"},
			{Name: "mail", Status: "skipped"},
		}
	})

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	assertStatus(t, health, http.StatusOK)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer test-metrics-token")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	assertStatus(t, res, http.StatusOK)
	if contentType := res.Header().Get("Content-Type"); !strings.Contains(contentType, "text/plain") {
		t.Fatalf("expected prometheus text content type, got %q", contentType)
	}
	body := res.Body.String()
	for _, expected := range []string{
		`pastebox_info{app="PasteBox",env="production"} 1`,
		`pastebox_readiness_ready 1`,
		`pastebox_readiness_component_ready{name="database",status="ok"} 1`,
		`pastebox_http_requests_total{method="GET",path="/api/v1/health",status="200"} 1`,
		`pastebox_operational_metrics_available 1`,
		`pastebox_queue_depth{kind="scan",status="pending"} 0`,
		`pastebox_mail_failed_depth 0`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected metrics body to contain %q, got:\n%s", expected, body)
		}
	}
}

func TestRequestLogsAndMetricsUseSanitizedRoutePaths(t *testing.T) {
	cfg := config.FromEnv()
	cfg.MetricsToken = "test-metrics-token"
	var logs bytes.Buffer
	handler := New(cfg, slog.New(slog.NewTextHandler(&logs, nil)))
	client := newHTTPTestClient(t, handler)

	frontendToken := "frontend-secret-token-123"
	frontend := client.json(http.MethodGet, "/s/"+frontendToken, "")
	assertStatus(t, frontend, http.StatusOK)

	shareToken := "share-secret-token-456"
	shareAccess := client.json(http.MethodPost, "/api/v1/shares/"+shareToken+"/access", `{}`)
	assertStatus(t, shareAccess, http.StatusNotFound)

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsReq.Header.Set("Authorization", "Bearer "+cfg.MetricsToken)
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, metricsReq)
	assertStatus(t, metrics, http.StatusOK)

	logBody := logs.String()
	metricsBody := metrics.Body.String()
	for _, secret := range []string{frontendToken, shareToken} {
		if strings.Contains(logBody, secret) {
			t.Fatalf("request logs must not contain token %q: %s", secret, logBody)
		}
		if strings.Contains(metricsBody, secret) {
			t.Fatalf("request metrics must not contain token %q: %s", secret, metricsBody)
		}
	}
	for _, expected := range []string{`path=/{frontend}`, `path=/api/v1/shares/{token}/access`} {
		if !strings.Contains(logBody, expected) {
			t.Fatalf("expected sanitized log path %q in logs:\n%s", expected, logBody)
		}
	}
	for _, expected := range []string{`path="/{frontend}"`, `path="/api/v1/shares/{token}/access"`} {
		if !strings.Contains(metricsBody, expected) {
			t.Fatalf("expected sanitized metric path %q in metrics:\n%s", expected, metricsBody)
		}
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
			ID                string `json:"id"`
			TagsPerPasteLimit int    `json:"tagsPerPasteLimit"`
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
	if body.Plans[0].TagsPerPasteLimit != 0 || body.Plans[1].TagsPerPasteLimit != 5 || body.Plans[2].TagsPerPasteLimit != 20 {
		t.Fatalf("unexpected tag limits: %#v", body.Plans)
	}
}

func TestSupportContactsEndpointReturnsConfiguredPublicIntake(t *testing.T) {
	cfg := config.FromEnv()
	cfg.SupportEmail = "support@pastebox.example.com"
	cfg.AbuseEmail = "abuse@pastebox.example.com"
	handler := New(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)))

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/support/contacts", nil))
	assertStatus(t, res, http.StatusOK)

	var body PublicSupportContacts
	decodeResponse(t, res, &body)
	if body.SupportEmail != cfg.SupportEmail || body.AbuseEmail != cfg.AbuseEmail {
		t.Fatalf("expected configured support contacts, got %#v", body)
	}
}

func TestSecurityHeadersApplyToAPIAndStaticResponses(t *testing.T) {
	handler := New(config.FromEnv(), slog.New(slog.NewTextHandler(testWriter{t: t}, nil)))

	for _, path := range []string{"/api/v1/health", "/legal"} {
		t.Run(path, func(t *testing.T) {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
			assertStatus(t, res, http.StatusOK)

			for header, expected := range map[string]string{
				"X-Content-Type-Options": "nosniff",
				"X-Frame-Options":        "DENY",
				"Referrer-Policy":        "strict-origin-when-cross-origin",
				"Permissions-Policy":     "camera=(), microphone=(), geolocation=(), payment=()",
			} {
				if got := res.Header().Get(header); got != expected {
					t.Fatalf("expected %s=%q, got %q", header, expected, got)
				}
			}
			csp := res.Header().Get("Content-Security-Policy")
			for _, expected := range []string{
				"default-src 'self'",
				"script-src 'self' https://challenges.cloudflare.com",
				"frame-src https://challenges.cloudflare.com",
				"frame-ancestors 'none'",
				"object-src 'none'",
			} {
				if !strings.Contains(csp, expected) {
					t.Fatalf("expected CSP to contain %q, got %q", expected, csp)
				}
			}
		})
	}
}

func TestCORSAllowlistControlsCredentialedAPIOrigins(t *testing.T) {
	cfg := config.FromEnv()
	cfg.CORSAllowedOrigins = []string{"https://pastebox.example.com", "https://admin.pastebox.example.com"}
	handler := New(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)))

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	allowedReq.Header.Set("Origin", "https://pastebox.example.com")
	handler.ServeHTTP(allowed, allowedReq)
	assertStatus(t, allowed, http.StatusOK)
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != "https://pastebox.example.com" {
		t.Fatalf("expected allowed origin to be reflected, got %q", got)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected credentialed CORS support, got %q", got)
	}

	disallowed := httptest.NewRecorder()
	disallowedReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	disallowedReq.Header.Set("Origin", "https://evil.example.com")
	handler.ServeHTTP(disallowed, disallowedReq)
	assertStatus(t, disallowed, http.StatusOK)
	if got := disallowed.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected disallowed origin to receive no CORS access header, got %q", got)
	}

	preflight := httptest.NewRecorder()
	preflightReq := httptest.NewRequest(http.MethodOptions, "/api/v1/pastes", nil)
	preflightReq.Header.Set("Origin", "https://admin.pastebox.example.com")
	preflightReq.Header.Set("Access-Control-Request-Method", "POST")
	preflightReq.Header.Set("Access-Control-Request-Headers", "Content-Type, X-CSRF-Token")
	handler.ServeHTTP(preflight, preflightReq)
	assertStatus(t, preflight, http.StatusNoContent)
	if got := preflight.Header().Get("Access-Control-Allow-Origin"); got != "https://admin.pastebox.example.com" {
		t.Fatalf("expected preflight origin to be reflected, got %q", got)
	}
}

func TestRateLimitAppliesEndpointSpecificBuckets(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*config.Config)
		newRequest func() *http.Request
	}{
		{
			name: "auth",
			configure: func(cfg *config.Config) {
				cfg.RateLimit.AuthLimit = 1
			},
			newRequest: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"nobody@example.com","password":"wrong"}`))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
		},
		{
			name: "write",
			configure: func(cfg *config.Config) {
				cfg.RateLimit.WriteLimit = 1
			},
			newRequest: func() *http.Request {
				req := httptest.NewRequest(http.MethodPatch, "/api/v1/me", strings.NewReader(`{"displayName":"New Name"}`))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
		},
		{
			name: "upload",
			configure: func(cfg *config.Config) {
				cfg.RateLimit.UploadLimit = 1
			},
			newRequest: func() *http.Request {
				var body bytes.Buffer
				writer := multipart.NewWriter(&body)
				part, err := writer.CreateFormFile("file", "test.txt")
				if err != nil {
					t.Fatalf("create multipart field: %v", err)
				}
				if _, err := part.Write([]byte("hello")); err != nil {
					t.Fatalf("write multipart field: %v", err)
				}
				if err := writer.Close(); err != nil {
					t.Fatalf("close multipart writer: %v", err)
				}
				req := httptest.NewRequest(http.MethodPost, "/api/v1/pastes/paste-id/attachments", &body)
				req.Header.Set("Content-Type", writer.FormDataContentType())
				return req
			},
		},
		{
			name: "download",
			configure: func(cfg *config.Config) {
				cfg.RateLimit.DownloadLimit = 1
			},
			newRequest: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/v1/attachments/att-id/download", nil)
			},
		},
		{
			name: "webhook",
			configure: func(cfg *config.Config) {
				cfg.RateLimit.WebhookLimit = 1
			},
			newRequest: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/billing/webhooks/stripe", strings.NewReader(`{}`))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := rateLimitTestConfig()
			tt.configure(&cfg)
			handler := New(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)))

			first := tt.newRequest()
			first.RemoteAddr = "203.0.113.10:1234"
			firstRes := httptest.NewRecorder()
			handler.ServeHTTP(firstRes, first)
			if firstRes.Code == http.StatusTooManyRequests {
				t.Fatalf("first request should reach downstream handler, got %d: %s", firstRes.Code, firstRes.Body.String())
			}

			second := tt.newRequest()
			second.RemoteAddr = "203.0.113.10:1234"
			secondRes := httptest.NewRecorder()
			handler.ServeHTTP(secondRes, second)
			assertRateLimited(t, secondRes)
		})
	}
}

func TestRateLimitCanBeDisabledForLocalDevelopment(t *testing.T) {
	cfg := rateLimitTestConfig()
	cfg.RateLimit.Enabled = false
	cfg.RateLimit.AuthLimit = 1
	handler := New(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"nobody@example.com","password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.20:1234"
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code == http.StatusTooManyRequests {
			t.Fatalf("rate limit should be disabled, got %d: %s", res.Code, res.Body.String())
		}
	}
}

func TestRuntimeRateLimitConfigAppliesImmediately(t *testing.T) {
	cfg := rateLimitTestConfig()
	service := app.New(cfg)
	admin, err := service.SeedAdmin("rate-admin@example.com", "password123")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	runtimeCfg, err := service.AdminRuntimeConfig(admin.ID)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runtimeCfg.RateLimits.Enabled = true
	runtimeCfg.RateLimits.WindowSeconds = 60
	runtimeCfg.RateLimits.LoginLimit = 1
	if _, err := service.AdminUpdateRuntimeConfig(admin.ID, app.RuntimeConfigPatch{RateLimits: runtimeRateLimitConfigPatch(runtimeCfg.RateLimits)}); err != nil {
		t.Fatalf("update rate limits: %v", err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"nobody@example.com","password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.25:1234"
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if i == 0 && res.Code == http.StatusTooManyRequests {
			t.Fatalf("first request should reach handler, got %d: %s", res.Code, res.Body.String())
		}
		if i == 1 {
			assertRateLimited(t, res)
		}
	}
}

func TestStaticFallbackServesAssetsAndFrontendRoutes(t *testing.T) {
	handler := New(config.FromEnv(), slog.New(slog.NewTextHandler(testWriter{t: t}, nil)))

	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	assertStatus(t, index, http.StatusOK)
	if !strings.Contains(index.Body.String(), "PasteBox") {
		t.Fatalf("expected embedded index to contain PasteBox, got %q", index.Body.String())
	}

	manifest := httptest.NewRecorder()
	handler.ServeHTTP(manifest, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))
	assertStatus(t, manifest, http.StatusOK)
	if !strings.Contains(manifest.Body.String(), "PasteBox") {
		t.Fatalf("expected embedded manifest to be served directly, got %q", manifest.Body.String())
	}

	favicon := httptest.NewRecorder()
	handler.ServeHTTP(favicon, httptest.NewRequest(http.MethodGet, "/favicon.svg", nil))
	assertStatus(t, favicon, http.StatusOK)
	if !strings.Contains(favicon.Body.String(), "<svg") {
		t.Fatalf("expected embedded favicon to be served directly, got %q", favicon.Body.String())
	}

	for _, path := range []string{
		"/s/dev-token",
		"/legal",
		"/legal/terms",
		"/legal/privacy",
		"/legal/refund",
		"/legal/abuse",
		"/legal/cookies",
		"/legal/account-deletion",
		"/legal/data-export",
		"/legal/data-retention",
		"/legal/subprocessors",
		"/support",
		"/status",
	} {
		t.Run(path, func(t *testing.T) {
			frontendRoute := httptest.NewRecorder()
			handler.ServeHTTP(frontendRoute, httptest.NewRequest(http.MethodGet, path, nil))
			assertStatus(t, frontendRoute, http.StatusOK)
			if !strings.Contains(frontendRoute.Body.String(), "PasteBox") {
				t.Fatalf("expected frontend route fallback to serve index, got %q", frontendRoute.Body.String())
			}
		})
	}

	missingAsset := httptest.NewRecorder()
	handler.ServeHTTP(missingAsset, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	assertStatus(t, missingAsset, http.StatusNotFound)
	if strings.Contains(missingAsset.Body.String(), "PasteBox") {
		t.Fatalf("expected missing asset to return 404 instead of index HTML, got %q", missingAsset.Body.String())
	}
}

func TestSessionCookieSecureFollowsProductionRequestScheme(t *testing.T) {
	cfg := config.FromEnv()
	cfg.AppEnv = "production"
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	service := app.New(cfg)
	admin, err := service.SeedAdmin("cookie-admin@example.com", "password123")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	runtimeCfg, err := service.AdminRuntimeConfig(admin.ID)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runtimeCfg.Registration.RequireEmailVerification = false
	if _, err := service.AdminUpdateRuntimeConfig(admin.ID, app.RuntimeConfigPatch{Registration: registrationConfigPatch(runtimeCfg.Registration)}); err != nil {
		t.Fatalf("disable registration verification for cookie test: %v", err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)

	plain := httptest.NewRecorder()
	plainReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/register",
		strings.NewReader(`{"email":"plain@example.com","password":"password123","displayName":"Plain"}`),
	)
	plainReq.Header.Set("Content-Type", "application/json")
	addCSRFToken(t, handler, plainReq)
	handler.ServeHTTP(plain, plainReq)
	assertStatus(t, plain, http.StatusCreated)
	plainCookie := sessionCookieFromResponse(t, plain)
	if plainCookie.Secure {
		t.Fatalf("expected plain HTTP test cookie to omit Secure, got %#v", plainCookie)
	}

	proxied := httptest.NewRecorder()
	proxiedReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/register",
		strings.NewReader(`{"email":"proxied@example.com","password":"password123","displayName":"Proxied"}`),
	)
	proxiedReq.Header.Set("Content-Type", "application/json")
	proxiedReq.Header.Set("X-Forwarded-Proto", "https")
	addCSRFToken(t, handler, proxiedReq)
	handler.ServeHTTP(proxied, proxiedReq)
	assertStatus(t, proxied, http.StatusCreated)
	proxiedCookie := sessionCookieFromResponse(t, proxied)
	if !proxiedCookie.Secure {
		t.Fatalf("expected HTTPS proxy cookie to set Secure, got %#v", proxiedCookie)
	}

	forwarded := httptest.NewRecorder()
	forwardedReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/register",
		strings.NewReader(`{"email":"forwarded@example.com","password":"password123","displayName":"Forwarded"}`),
	)
	forwardedReq.Header.Set("Content-Type", "application/json")
	forwardedReq.Header.Set("Forwarded", `for=192.0.2.10; proto="https"; host=pastebox.example.com`)
	addCSRFToken(t, handler, forwardedReq)
	handler.ServeHTTP(forwarded, forwardedReq)
	assertStatus(t, forwarded, http.StatusCreated)
	forwardedCookie := sessionCookieFromResponse(t, forwarded)
	if !forwardedCookie.Secure {
		t.Fatalf("expected standard Forwarded HTTPS cookie to set Secure, got %#v", forwardedCookie)
	}
}

func TestProductionAuthResponsesDoNotExposeDevTokens(t *testing.T) {
	cfg := config.FromEnv()
	cfg.AppEnv = "production"
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	cfg.MetricsToken = "production-auth-metrics-token"
	service := app.New(cfg)
	admin, err := service.SeedAdmin("prod-auth-admin@example.com", "password123")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)
	initialMailDepth := mailQueueDepthMetric(t, handler, cfg.MetricsToken)

	client := newHTTPTestClient(t, handler)
	startRegistrationVerify := client.json(http.MethodPost, "/api/v1/auth/registration/email-verification/start", `{"email":"prod-auth-user@example.com"}`)
	assertStatus(t, startRegistrationVerify, http.StatusOK)
	assertNoDevTokenFields(t, startRegistrationVerify)

	runtimeCfg, err := service.AdminRuntimeConfig(admin.ID)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runtimeCfg.Registration.RequireEmailVerification = false
	if _, err := service.AdminUpdateRuntimeConfig(admin.ID, app.RuntimeConfigPatch{Registration: registrationConfigPatch(runtimeCfg.Registration)}); err != nil {
		t.Fatalf("disable registration verification for production auth response test: %v", err)
	}
	register := client.json(http.MethodPost, "/api/v1/auth/register", `{"email":"prod-auth-user@example.com","password":"password123","displayName":"Prod Auth"}`)
	assertStatus(t, register, http.StatusCreated)
	assertNoDevTokenFields(t, register)

	startVerify := client.json(http.MethodPost, "/api/v1/auth/email-verification/start", "")
	assertStatus(t, startVerify, http.StatusOK)
	assertNoDevTokenFields(t, startVerify)

	passwordReset := client.json(http.MethodPost, "/api/v1/auth/password-reset/start", `{"email":"prod-auth-admin@example.com"}`)
	assertStatus(t, passwordReset, http.StatusOK)
	assertNoDevTokenFields(t, passwordReset)

	if got := mailQueueDepthMetric(t, handler, cfg.MetricsToken); got < initialMailDepth+3 {
		t.Fatalf("expected production auth flows to queue delivery emails, depth before=%d after=%d", initialMailDepth, got)
	}
}

func TestCSRFTokenProtectsUnsafeBrowserRoutes(t *testing.T) {
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.Stripe.WebhookSecret = "whsec_test_csrf_exclusion"
	service := app.New(cfg)
	admin, err := service.SeedAdmin("csrf-admin@example.com", "password123")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	runtimeCfg, err := service.AdminRuntimeConfig(admin.ID)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runtimeCfg.Registration.RequireEmailVerification = false
	if _, err := service.AdminUpdateRuntimeConfig(admin.ID, app.RuntimeConfigPatch{Registration: registrationConfigPatch(runtimeCfg.Registration)}); err != nil {
		t.Fatalf("disable registration verification for csrf test: %v", err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)

	missing := httptest.NewRecorder()
	missingReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/register",
		strings.NewReader(`{"email":"csrf-missing@example.com","password":"password123","displayName":"Missing"}`),
	)
	missingReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(missing, missingReq)
	assertStatus(t, missing, http.StatusForbidden)
	var missingBody map[string]string
	decodeResponse(t, missing, &missingBody)
	if missingBody["error"] != "csrf_required" {
		t.Fatalf("expected csrf_required error, got %#v", missingBody)
	}

	protected := httptest.NewRecorder()
	protectedReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/register",
		strings.NewReader(`{"email":"csrf-ok@example.com","password":"password123","displayName":"Protected"}`),
	)
	protectedReq.Header.Set("Content-Type", "application/json")
	addCSRFToken(t, handler, protectedReq)
	handler.ServeHTTP(protected, protectedReq)
	assertStatus(t, protected, http.StatusCreated)

	webhook := httptest.NewRecorder()
	webhookReq := httptest.NewRequest(http.MethodPost, "/api/v1/billing/webhooks/stripe", strings.NewReader(`{}`))
	webhookReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(webhook, webhookReq)
	assertStatus(t, webhook, http.StatusBadRequest)
	var webhookBody map[string]string
	decodeResponse(t, webhook, &webhookBody)
	if webhookBody["error"] != "invalid_webhook_signature" {
		t.Fatalf("provider webhook route must reach signature validation instead of browser CSRF gate: %#v", webhookBody)
	}
}

func TestGoogleOAuthRedirectFlowCreatesSession(t *testing.T) {
	cfg := config.FromEnv()
	cfg.AppEnv = "production"
	cfg.GoogleOAuth.ClientID = "google-client-id"
	cfg.GoogleOAuth.ClientSecret = "google-client-secret"
	cfg.GoogleOAuth.RedirectURL = "https://pastebox.example.com/api/v1/auth/google/callback"
	service := app.New(cfg)
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test google key: %v", err)
	}
	const keyID = "google-test-key"
	var wantedNonce string
	testIDToken := func() string {
		return signGoogleTestIDToken(t, privateKey, keyID, map[string]any{
			"iss":            "https://accounts.google.com",
			"aud":            cfg.GoogleOAuth.ClientID,
			"sub":            "google-subject-1",
			"email":          "oauth-redirect@example.com",
			"email_verified": true,
			"name":           "OAuth Redirect",
			"nonce":          wantedNonce,
			"exp":            time.Now().UTC().Add(5 * time.Minute).Unix(),
		})
	}
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			if r.Form.Get("client_id") != cfg.GoogleOAuth.ClientID ||
				r.Form.Get("client_secret") != cfg.GoogleOAuth.ClientSecret ||
				r.Form.Get("redirect_uri") != cfg.GoogleOAuth.RedirectURL ||
				r.Form.Get("code") != "google-code" {
				t.Fatalf("unexpected token form: %#v", r.Form)
			}
			writeJSON(w, http.StatusOK, map[string]string{"id_token": testIDToken()})
		case "/certs":
			writeJSON(w, http.StatusOK, map[string]any{
				"keys": []map[string]string{
					googleTestJWK(keyID, &privateKey.PublicKey),
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer google.Close()
	oldTokenURL := googleOAuthTokenURL
	oldJWKSURL := googleOAuthJWKSURL
	oldHTTPClient := googleOAuthHTTPClient
	googleOAuthTokenURL = google.URL + "/token"
	googleOAuthJWKSURL = google.URL + "/certs"
	googleOAuthHTTPClient = google.Client()
	t.Cleanup(func() {
		googleOAuthTokenURL = oldTokenURL
		googleOAuthJWKSURL = oldJWKSURL
		googleOAuthHTTPClient = oldHTTPClient
	})

	start := httptest.NewRecorder()
	startReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/start?returnTo=%2Fbilling&language=zh-TW", nil)
	handler.ServeHTTP(start, startReq)
	assertStatus(t, start, http.StatusSeeOther)
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse google redirect location: %v", err)
	}
	if location.Host != "accounts.google.com" || location.Query().Get("client_id") != cfg.GoogleOAuth.ClientID {
		t.Fatalf("unexpected google redirect: %s", location.String())
	}
	state := location.Query().Get("state")
	if state == "" {
		t.Fatalf("expected state in redirect: %s", location.String())
	}
	oauthCookie := cookieFromResponse(t, start, googleOAuthStateCookieName)
	if !oauthCookie.HttpOnly || oauthCookie.Value == "" {
		t.Fatalf("expected signed HttpOnly state cookie, got %#v", oauthCookie)
	}
	stateReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback", nil)
	stateReq.AddCookie(oauthCookie)
	decodedState, err := (&Server{cfg: cfg}).googleOAuthStateFromRequest(stateReq)
	if err != nil {
		t.Fatalf("decode signed oauth state: %v", err)
	}
	if decodedState.State != state || decodedState.ReturnTo != "/billing" || decodedState.Language != "zh-TW" {
		t.Fatalf("unexpected signed oauth state: %#v", decodedState)
	}
	wantedNonce = decodedState.Nonce

	callback := httptest.NewRecorder()
	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback?code=google-code&state="+url.QueryEscape(state), nil)
	callbackReq.AddCookie(oauthCookie)
	handler.ServeHTTP(callback, callbackReq)
	assertStatus(t, callback, http.StatusSeeOther)
	if got := callback.Header().Get("Location"); got != "/billing" {
		t.Fatalf("expected return redirect to /billing, got %q", got)
	}
	sessionCookie := sessionCookieFromResponse(t, callback)
	if sessionCookie.Value == "" {
		t.Fatalf("expected session cookie after callback: %#v", sessionCookie)
	}
	clearedState := cookieFromResponse(t, callback, googleOAuthStateCookieName)
	if clearedState.MaxAge >= 0 {
		t.Fatalf("expected state cookie to be cleared, got %#v", clearedState)
	}

	me := httptest.NewRecorder()
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	meReq.AddCookie(sessionCookie)
	handler.ServeHTTP(me, meReq)
	assertStatus(t, me, http.StatusOK)
	var user app.UserView
	decodeResponse(t, me, &user)
	if user.Email != "oauth-redirect@example.com" || !user.EmailVerified || user.DisplayName != "OAuth Redirect" || user.Language != "zh-TW" {
		t.Fatalf("unexpected oauth user: %#v", user)
	}
}

func TestGitHubOAuthRedirectFlowCreatesSession(t *testing.T) {
	cfg := config.FromEnv()
	cfg.AppEnv = "production"
	cfg.GitHubOAuth.ClientID = "github-client-id"
	cfg.GitHubOAuth.ClientSecret = "github-client-secret"
	cfg.GitHubOAuth.RedirectURL = "https://pastebox.example.com/api/v1/auth/github/callback"
	service := app.New(cfg)
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)

	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse github token form: %v", err)
			}
			if r.Form.Get("client_id") != cfg.GitHubOAuth.ClientID ||
				r.Form.Get("client_secret") != cfg.GitHubOAuth.ClientSecret ||
				r.Form.Get("redirect_uri") != cfg.GitHubOAuth.RedirectURL ||
				r.Form.Get("code") != "github-code" {
				t.Fatalf("unexpected github token form: %#v", r.Form)
			}
			writeJSON(w, http.StatusOK, map[string]string{"access_token": "github-access-token"})
		case "/user":
			if r.Header.Get("Authorization") != "Bearer github-access-token" {
				t.Fatalf("expected bearer token, got %q", r.Header.Get("Authorization"))
			}
			writeJSON(w, http.StatusOK, githubUserResponse{ID: 12345, Login: "octo", Name: "GitHub Redirect"})
		case "/emails":
			writeJSON(w, http.StatusOK, []githubEmailResponse{
				{Email: "secondary@example.com", Verified: true},
				{Email: "github-redirect@example.com", Primary: true, Verified: true},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer github.Close()
	oldTokenURL := githubOAuthTokenURL
	oldUserURL := githubOAuthUserURL
	oldEmailsURL := githubOAuthEmailsURL
	oldHTTPClient := githubOAuthHTTPClient
	githubOAuthTokenURL = github.URL + "/token"
	githubOAuthUserURL = github.URL + "/user"
	githubOAuthEmailsURL = github.URL + "/emails"
	githubOAuthHTTPClient = github.Client()
	t.Cleanup(func() {
		githubOAuthTokenURL = oldTokenURL
		githubOAuthUserURL = oldUserURL
		githubOAuthEmailsURL = oldEmailsURL
		githubOAuthHTTPClient = oldHTTPClient
	})

	start := httptest.NewRecorder()
	startReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/start?returnTo=%2Fapp&locale=es", nil)
	handler.ServeHTTP(start, startReq)
	assertStatus(t, start, http.StatusSeeOther)
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse github redirect location: %v", err)
	}
	if location.Host != "github.com" || location.Query().Get("client_id") != cfg.GitHubOAuth.ClientID {
		t.Fatalf("unexpected github redirect: %s", location.String())
	}
	state := location.Query().Get("state")
	if state == "" {
		t.Fatalf("expected state in redirect: %s", location.String())
	}
	oauthCookie := cookieFromResponse(t, start, githubOAuthStateCookieName)
	if !oauthCookie.HttpOnly || oauthCookie.Value == "" {
		t.Fatalf("expected signed HttpOnly github state cookie, got %#v", oauthCookie)
	}
	stateReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback", nil)
	stateReq.AddCookie(oauthCookie)
	decodedState, err := (&Server{cfg: cfg}).oauthStateFromRequest(stateReq, githubOAuthStateCookieName, cfg.GitHubOAuth.ClientSecret)
	if err != nil {
		t.Fatalf("decode signed github oauth state: %v", err)
	}
	if decodedState.State != state || decodedState.ReturnTo != "/app" || decodedState.Language != "es" {
		t.Fatalf("unexpected signed github oauth state: %#v", decodedState)
	}

	callback := httptest.NewRecorder()
	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback?code=github-code&state="+url.QueryEscape(state), nil)
	callbackReq.AddCookie(oauthCookie)
	handler.ServeHTTP(callback, callbackReq)
	assertStatus(t, callback, http.StatusSeeOther)
	if got := callback.Header().Get("Location"); got != "/app" {
		t.Fatalf("expected return redirect to /app, got %q", got)
	}
	sessionCookie := sessionCookieFromResponse(t, callback)
	clearedState := cookieFromResponse(t, callback, githubOAuthStateCookieName)
	if sessionCookie.Value == "" || clearedState.MaxAge >= 0 {
		t.Fatalf("expected session and cleared github state cookies, session=%#v state=%#v", sessionCookie, clearedState)
	}

	me := httptest.NewRecorder()
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	meReq.AddCookie(sessionCookie)
	handler.ServeHTTP(me, meReq)
	assertStatus(t, me, http.StatusOK)
	var user app.UserView
	decodeResponse(t, me, &user)
	if user.Email != "github-redirect@example.com" || !user.EmailVerified || user.DisplayName != "GitHub Redirect" || user.Language != "es" {
		t.Fatalf("unexpected github oauth user: %#v", user)
	}
	if got := user.OAuthProviders; len(got) != 1 || got[0] != "github" {
		t.Fatalf("expected github provider, got %#v", got)
	}
}

func TestGoogleOAuthCallbackRejectsStateMismatch(t *testing.T) {
	cfg := config.FromEnv()
	cfg.AppEnv = "production"
	cfg.GoogleOAuth.ClientID = "google-client-id"
	cfg.GoogleOAuth.ClientSecret = "google-client-secret"
	cfg.GoogleOAuth.RedirectURL = "https://pastebox.example.com/api/v1/auth/google/callback"
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), app.New(cfg))

	start := httptest.NewRecorder()
	startReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/start?returnTo=%2Fbilling", nil)
	handler.ServeHTTP(start, startReq)
	assertStatus(t, start, http.StatusSeeOther)
	oauthCookie := cookieFromResponse(t, start, googleOAuthStateCookieName)

	callback := httptest.NewRecorder()
	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback?code=google-code&state=wrong-state", nil)
	callbackReq.AddCookie(oauthCookie)
	handler.ServeHTTP(callback, callbackReq)
	assertStatus(t, callback, http.StatusSeeOther)
	if got := callback.Header().Get("Location"); got != "/?authError=invalid_google_state" {
		t.Fatalf("expected invalid state redirect, got %q", got)
	}
	clearedState := cookieFromResponse(t, callback, googleOAuthStateCookieName)
	if clearedState.MaxAge >= 0 {
		t.Fatalf("expected state cookie to be cleared, got %#v", clearedState)
	}
	for _, cookie := range callback.Result().Cookies() {
		if cookie.Name == sessionCookieName && cookie.Value != "" && cookie.MaxAge >= 0 {
			t.Fatalf("state mismatch must not create a session cookie, got %#v", cookie)
		}
	}
}

func TestAuthPasteUploadShareAndQuotaHTTPContracts(t *testing.T) {
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	service := app.New(cfg)
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)
	client := newHTTPTestClient(t, handler)

	register := registerHTTPUser(t, client, "owner@example.com", "Owner")
	sessionCookie := register.Result().Cookies()[0]
	if sessionCookie.Name != sessionCookieName || !sessionCookie.HttpOnly {
		t.Fatalf("expected HttpOnly session cookie, got %#v", sessionCookie)
	}
	var authBody struct {
		User             app.UserView `json:"user"`
		SessionExpiresAt string       `json:"sessionExpiresAt"`
	}
	decodeResponse(t, register, &authBody)
	if authBody.User.Email != "owner@example.com" || authBody.User.PlanID != "free" || !authBody.User.EmailVerified || authBody.SessionExpiresAt == "" {
		t.Fatalf("unexpected register body: %#v", authBody)
	}

	quota := client.json(http.MethodGet, "/api/v1/quota", "")
	assertStatus(t, quota, http.StatusOK)
	var quotaBody app.QuotaView
	decodeResponse(t, quota, &quotaBody)
	if quotaBody.Plan.ID != "free" || quotaBody.ActivePasteCount != 0 {
		t.Fatalf("unexpected quota body: %#v", quotaBody)
	}

	for _, item := range []struct {
		path string
		key  string
	}{
		{path: "/api/v1/pastes", key: "pastes"},
		{path: "/api/v1/shares", key: "shares"},
		{path: "/api/v1/billing/orders", key: "orders"},
	} {
		emptyList := client.json(http.MethodGet, item.path, "")
		assertStatus(t, emptyList, http.StatusOK)
		var body map[string]json.RawMessage
		decodeResponse(t, emptyList, &body)
		if got := strings.TrimSpace(string(body[item.key])); got != "[]" {
			t.Fatalf("expected %s to return an empty JSON array, got %s", item.path, got)
		}
	}

	createPaste := client.json(http.MethodPost, "/api/v1/pastes", `{"title":"Contract","text":"hello","tags":[],"pinned":true,"favorite":false,"expiresInSeconds":3600}`)
	assertStatus(t, createPaste, http.StatusCreated)
	var paste app.PasteView
	decodeResponse(t, createPaste, &paste)
	if paste.ID == "" || paste.Title != "Contract" || paste.Text != "hello" || len(paste.Tags) != 0 {
		t.Fatalf("unexpected paste body: %#v", paste)
	}

	createFileOnlyPaste := client.json(http.MethodPost, "/api/v1/pastes", `{"title":"File only","text":"","tags":[],"expiresInSeconds":3600}`)
	assertStatus(t, createFileOnlyPaste, http.StatusCreated)
	var fileOnlyRaw map[string]json.RawMessage
	decodeResponse(t, createFileOnlyPaste, &fileOnlyRaw)
	for _, key := range []string{"tags", "attachments"} {
		if got := strings.TrimSpace(string(fileOnlyRaw[key])); got != "[]" {
			t.Fatalf("expected created paste field %s to return an empty JSON array, got %s", key, got)
		}
	}

	upload := client.multipart("/api/v1/pastes/"+paste.ID+"/attachments", "file", "note.txt", []byte("attachment"))
	assertStatus(t, upload, http.StatusCreated)
	var attachment app.AttachmentView
	decodeResponse(t, upload, &attachment)
	wantSHA := sha256.Sum256([]byte("attachment"))
	if attachment.ID == "" || attachment.PasteID != paste.ID || attachment.Size != int64(len("attachment")) || attachment.SHA256 != hex.EncodeToString(wantSHA[:]) {
		t.Fatalf("unexpected attachment body: %#v", attachment)
	}
	if err := service.RunAttachmentScan(staticHTTPScanner{result: app.ScanResult{Status: "clean"}}, attachment.ID); err != nil {
		t.Fatalf("run clean scan: %v", err)
	}

	createShare := client.json(http.MethodPost, "/api/v1/pastes/"+paste.ID+"/shares", `{"password":"pw","loginRequired":false,"maxVisits":2,"maxDownloads":1,"expiresInSeconds":1800}`)
	assertStatus(t, createShare, http.StatusCreated)
	var share app.ShareView
	decodeResponse(t, createShare, &share)
	if share.ID == "" || share.Token == "" || share.URL == "" || !share.HasPassword || share.MaxVisits != 2 || share.MaxDownloads != 1 {
		t.Fatalf("unexpected share body: %#v", share)
	}

	leakyDownload := newHTTPTestClient(t, handler).json(http.MethodGet, "/api/v1/shares/"+share.Token+"/attachments/"+attachment.ID+"/download?password=pw", "")
	assertStatus(t, leakyDownload, http.StatusUnauthorized)
	var leakyDownloadBody map[string]string
	decodeResponse(t, leakyDownload, &leakyDownloadBody)
	if leakyDownloadBody["error"] != "share_access_required" {
		t.Fatalf("expected query password to be ignored, got %#v", leakyDownloadBody)
	}

	anonymous := newHTTPTestClient(t, handler)
	malformedAccess := anonymous.json(http.MethodPost, "/api/v1/shares/"+share.Token+"/access", `{"password":`)
	assertStatus(t, malformedAccess, http.StatusBadRequest)
	var malformedAccessBody map[string]string
	decodeResponse(t, malformedAccess, &malformedAccessBody)
	if malformedAccessBody["error"] != "invalid_json" {
		t.Fatalf("expected malformed share access body to be rejected, got %#v", malformedAccessBody)
	}
	unknownFieldAccess := anonymous.json(http.MethodPost, "/api/v1/shares/"+share.Token+"/access", `{"password":"pw","extra":true}`)
	assertStatus(t, unknownFieldAccess, http.StatusBadRequest)
	var unknownFieldAccessBody map[string]string
	decodeResponse(t, unknownFieldAccess, &unknownFieldAccessBody)
	if unknownFieldAccessBody["error"] != "invalid_json" {
		t.Fatalf("expected unknown share access field to be rejected, got %#v", unknownFieldAccessBody)
	}
	oversizedAccess := anonymous.json(http.MethodPost, "/api/v1/shares/"+share.Token+"/access", strings.Repeat("x", int(shareAccessBodyLimitBytes)+1))
	assertStatus(t, oversizedAccess, http.StatusRequestEntityTooLarge)
	var oversizedAccessBody map[string]string
	decodeResponse(t, oversizedAccess, &oversizedAccessBody)
	if oversizedAccessBody["error"] != "request_body_too_large" {
		t.Fatalf("expected oversized share access body to be rejected, got %#v", oversizedAccessBody)
	}
	accessShare := anonymous.json(http.MethodPost, "/api/v1/shares/"+share.Token+"/access", `{"password":"pw"}`)
	assertStatus(t, accessShare, http.StatusOK)
	grantCookie := cookieFromResponse(t, accessShare, shareAccessCookieName)
	if grantCookie.Value == "" || !grantCookie.HttpOnly || grantCookie.Path != shareAccessCookiePath(share.Token) {
		t.Fatalf("expected scoped HttpOnly share access cookie, got %#v", grantCookie)
	}
	var shareAccess struct {
		Paste app.PasteView `json:"paste"`
		Share app.ShareView `json:"share"`
	}
	decodeResponse(t, accessShare, &shareAccess)
	if shareAccess.Paste.ID != paste.ID || shareAccess.Share.VisitCount != 1 {
		t.Fatalf("unexpected share access body: %#v", shareAccess)
	}

	download := anonymous.json(http.MethodGet, "/api/v1/shares/"+share.Token+"/attachments/"+attachment.ID+"/download", "")
	assertStatus(t, download, http.StatusOK)
	if got := download.Body.String(); got != "attachment" {
		t.Fatalf("expected shared attachment download body, got %q", got)
	}
}

func TestAdminHTTPContractsWriteAuditLogs(t *testing.T) {
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	service := app.New(cfg)
	if _, err := service.SeedAdmin("admin@example.com", "password123"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)

	owner := newHTTPTestClient(t, handler)
	register := registerHTTPUser(t, owner, "owner-admin@example.com", "Owner")
	var ownerAuth struct {
		User app.UserView `json:"user"`
	}
	decodeResponse(t, register, &ownerAuth)
	if !ownerAuth.User.EmailVerified {
		t.Fatalf("expected owner to be verified after registration, got %#v", ownerAuth.User)
	}

	createPaste := owner.json(http.MethodPost, "/api/v1/pastes", `{"title":"Admin target","text":"binary","tags":[],"expiresInSeconds":3600}`)
	assertStatus(t, createPaste, http.StatusCreated)
	var paste app.PasteView
	decodeResponse(t, createPaste, &paste)

	upload := owner.multipart("/api/v1/pastes/"+paste.ID+"/attachments", "file", "tool.exe", []byte("binary"))
	assertStatus(t, upload, http.StatusCreated)
	var attachment app.AttachmentView
	decodeResponse(t, upload, &attachment)
	if attachment.ScanStatus != "pending" {
		t.Fatalf("expected upload to queue pending scan, got %#v", attachment)
	}

	admin := newHTTPTestClient(t, handler)
	login := admin.json(http.MethodPost, "/api/v1/auth/login", `{"email":"admin@example.com","password":"password123"}`)
	assertStatus(t, login, http.StatusOK)

	dashboard := admin.json(http.MethodGet, "/api/v1/admin/dashboard", "")
	assertStatus(t, dashboard, http.StatusOK)
	var dashboardBody map[string]any
	decodeResponse(t, dashboard, &dashboardBody)
	if dashboardBody["scanQueueDepth"] == float64(0) {
		t.Fatalf("expected scan queue depth in dashboard, got %#v", dashboardBody)
	}

	queues := admin.json(http.MethodGet, "/api/v1/admin/queues", "")
	assertStatus(t, queues, http.StatusOK)
	var queuesBody map[string]json.RawMessage
	decodeResponse(t, queues, &queuesBody)
	for _, key := range []string{"cleanupFailures", "reports"} {
		if got := strings.TrimSpace(string(queuesBody[key])); got != "[]" {
			t.Fatalf("expected admin queue field %s to return an array, got %s", key, got)
		}
	}
	if got := strings.TrimSpace(string(queuesBody["scanJobs"])); got == "" || got == "null" {
		t.Fatalf("expected admin scanJobs field to return an array, got %s", got)
	}

	missingPlanReason := admin.json(http.MethodPatch, "/api/v1/admin/users/"+ownerAuth.User.ID+"/plan", `{"planId":"plus"}`)
	assertStatus(t, missingPlanReason, http.StatusBadRequest)

	setPlanReason := "SUP-456 trial upgrade"
	setPlan := admin.json(http.MethodPatch, "/api/v1/admin/users/"+ownerAuth.User.ID+"/plan", `{"planId":"plus","reason":"`+setPlanReason+`","ticketId":"SUP-456"}`)
	assertStatus(t, setPlan, http.StatusOK)
	var updatedUser app.UserView
	decodeResponse(t, setPlan, &updatedUser)
	if updatedUser.PlanID != "plus" {
		t.Fatalf("expected plus plan, got %#v", updatedUser)
	}

	orderRes := owner.json(http.MethodPost, "/api/v1/billing/orders", `{"provider":"epusdt","planId":"plus","period":"monthly"}`)
	assertStatus(t, orderRes, http.StatusCreated)
	var order app.Order
	decodeResponse(t, orderRes, &order)

	missingReason := admin.json(http.MethodPost, "/api/v1/admin/orders/"+order.ID+"/mark-paid", `{"txId":"tx-missing-reason"}`)
	assertStatus(t, missingReason, http.StatusBadRequest)

	reconcile := admin.json(http.MethodPost, "/api/v1/admin/billing/reconcile", "")
	assertStatus(t, reconcile, http.StatusOK)
	var reconcileBody map[string]int
	decodeResponse(t, reconcile, &reconcileBody)
	if reconcileBody["checkedOrders"] == 0 || reconcileBody["pendingOrders"] == 0 {
		t.Fatalf("expected billing reconciliation counts, got %#v", reconcileBody)
	}

	manualReason := "SUP-789 verified stuck Epusdt payment"
	markPaid := admin.json(http.MethodPost, "/api/v1/admin/orders/"+order.ID+"/mark-paid", `{"txId":"tx-http-manual","reason":"`+manualReason+`"}`)
	assertStatus(t, markPaid, http.StatusOK)
	var paidOrder app.Order
	decodeResponse(t, markPaid, &paidOrder)
	if paidOrder.Status != "paid" || paidOrder.TxID != "tx-http-manual" {
		t.Fatalf("expected manually paid order, got %#v", paidOrder)
	}

	retryScan := admin.json(http.MethodPost, "/api/v1/admin/attachments/"+attachment.ID+"/retry-scan", "")
	assertStatus(t, retryScan, http.StatusOK)
	var retriedAttachment app.AttachmentView
	decodeResponse(t, retryScan, &retriedAttachment)
	if retriedAttachment.ScanStatus != "pending" {
		t.Fatalf("expected retried scan to requeue pending scan, got %#v", retriedAttachment)
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
	if !containsAuditMetadata(auditBody.AuditLogs, "admin.user_plan_set", ownerAuth.User.ID, "reason", setPlanReason) ||
		!containsAuditMetadata(auditBody.AuditLogs, "admin.user_plan_set", ownerAuth.User.ID, "ticketId", "SUP-456") ||
		!containsAuditMetadata(auditBody.AuditLogs, "admin.user_plan_set", ownerAuth.User.ID, "newPlanId", "plus") {
		t.Fatalf("expected admin plan audit metadata, got %#v", auditBody.AuditLogs)
	}
	if !containsAuditMetadata(auditBody.AuditLogs, "billing.order_paid", order.ID, "reason", manualReason) {
		t.Fatalf("expected manual payment reason audit metadata, got %#v", auditBody.AuditLogs)
	}
}

func TestAdminRuntimeGuestRedemptionAndAlertHTTPContracts(t *testing.T) {
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.DevAuthTokens = true
	service := app.New(cfg)
	alertSender := &fakeHTTPAlertSender{}
	service.SetAlertSender(alertSender)
	if _, err := service.SeedAdmin("runtime-admin@example.com", "password123"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)

	user := newHTTPTestClient(t, handler)
	registerHTTPUser(t, user, "runtime-user@example.com", "Runtime User")
	nonAdminRuntime := user.json(http.MethodGet, "/api/v1/admin/runtime-panel", "")
	assertStatus(t, nonAdminRuntime, http.StatusForbidden)
	var nonAdminErr map[string]string
	decodeResponse(t, nonAdminRuntime, &nonAdminErr)
	if nonAdminErr["error"] != "admin_required" {
		t.Fatalf("expected admin_required, got %#v", nonAdminErr)
	}

	admin := newHTTPTestClient(t, handler)
	login := admin.json(http.MethodPost, "/api/v1/auth/login", `{"email":"runtime-admin@example.com","password":"password123"}`)
	assertStatus(t, login, http.StatusOK)

	updateRuntime := admin.json(http.MethodPatch, "/api/v1/admin/runtime-config", `{"guestUploads":{"enabled":true,"requireTurnstile":false},"alerts":{"enabled":true,"telegramEnabled":true,"cooldownSeconds":60,"cpuPercentThreshold":90,"memoryPercentThreshold":90,"diskPercentThreshold":90,"scanFailureDepthThreshold":10,"failedJobDepthThreshold":10,"mailFailedDepthThreshold":10,"reportsOpenThreshold":10}}`)
	assertStatus(t, updateRuntime, http.StatusOK)
	var runtimeCfg app.RuntimeConfig
	decodeResponse(t, updateRuntime, &runtimeCfg)
	if !runtimeCfg.GuestUploads.Enabled || runtimeCfg.GuestUploads.RequireTurnstile {
		t.Fatalf("expected guest uploads enabled without turnstile, got %#v", runtimeCfg.GuestUploads)
	}
	if bytes.Contains(updateRuntime.Body.Bytes(), []byte(`"missingEnv":null`)) {
		t.Fatalf("admin runtime config must encode empty missingEnv arrays as []: %s", updateRuntime.Body.String())
	}

	catalog := service.PlanCatalog()
	if len(catalog.Plans) == 0 {
		t.Fatal("expected plan catalog")
	}
	catalog.Plans[0].ActivePasteLimit = 7
	catalogBody, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	updateCatalog := admin.json(http.MethodPatch, "/api/v1/admin/catalog", string(catalogBody))
	assertStatus(t, updateCatalog, http.StatusOK)
	publicPlans := admin.json(http.MethodGet, "/api/v1/plans", "")
	assertStatus(t, publicPlans, http.StatusOK)
	var publicCatalog struct {
		Plans []struct {
			ID                string `json:"id"`
			ActivePasteLimit  int    `json:"activePasteLimit"`
			TagsPerPasteLimit int    `json:"tagsPerPasteLimit"`
		} `json:"plans"`
		GuestUploads app.GuestUploadConfig `json:"guestUploads"`
	}
	decodeResponse(t, publicPlans, &publicCatalog)
	if len(publicCatalog.Plans) == 0 || publicCatalog.Plans[0].ActivePasteLimit != 7 || publicCatalog.Plans[0].TagsPerPasteLimit != catalog.Plans[0].TagsPerPasteLimit {
		t.Fatalf("expected public plans to use updated catalog, got %#v", publicCatalog.Plans)
	}
	if !publicCatalog.GuestUploads.Enabled || publicCatalog.GuestUploads.SingleTextBytes == 0 {
		t.Fatalf("expected public guest upload limits, got %#v", publicCatalog.GuestUploads)
	}

	createBatch := admin.json(http.MethodPost, "/api/v1/admin/redemption-batches", `{"planId":"plus","durationDays":30,"quantity":1,"note":"HTTP contract"}`)
	assertStatus(t, createBatch, http.StatusCreated)
	var batch app.RedemptionBatchView
	decodeResponse(t, createBatch, &batch)
	if len(batch.Codes) != 1 || batch.Codes[0].Code == "" {
		t.Fatalf("expected generated redemption code, got %#v", batch)
	}
	redeem := user.json(http.MethodPost, "/api/v1/redemptions/redeem", `{"code":"`+batch.Codes[0].Code+`"}`)
	assertStatus(t, redeem, http.StatusOK)
	var redeemed app.UserView
	decodeResponse(t, redeem, &redeemed)
	if redeemed.PlanID != "plus" {
		t.Fatalf("expected redeemed plus plan, got %#v", redeemed)
	}

	guest := newHTTPTestClient(t, handler)
	guestPaste := guest.json(http.MethodPost, "/api/v1/guest/pastes", `{"title":"Guest","text":"hello","tags":[],"expiresInSeconds":600}`)
	assertStatus(t, guestPaste, http.StatusCreated)
	var guestBody struct {
		GuestToken string        `json:"guestToken"`
		Paste      app.PasteView `json:"paste"`
	}
	decodeResponse(t, guestPaste, &guestBody)
	if guestBody.GuestToken == "" || guestBody.Paste.ID == "" {
		t.Fatalf("expected guest token and paste, got %#v", guestBody)
	}
	guestUpload := guest.multipartWithFields("/api/v1/guest/pastes/"+guestBody.Paste.ID+"/attachments", "file", "guest.txt", []byte("guest file"), map[string]string{"guestToken": guestBody.GuestToken})
	assertStatus(t, guestUpload, http.StatusCreated)
	var guestAttachment app.AttachmentView
	decodeResponse(t, guestUpload, &guestAttachment)
	if guestAttachment.PasteID != guestBody.Paste.ID {
		t.Fatalf("expected guest attachment on paste, got %#v", guestAttachment)
	}
	guestShare := guest.json(http.MethodPost, "/api/v1/guest/pastes/"+guestBody.Paste.ID+"/shares", `{"guestToken":"`+guestBody.GuestToken+`","expiresInSeconds":600}`)
	assertStatus(t, guestShare, http.StatusCreated)
	var guestShareBody app.ShareView
	decodeResponse(t, guestShare, &guestShareBody)
	if guestShareBody.Token == "" || !strings.Contains(guestShareBody.URL, "/s/") {
		t.Fatalf("expected guest share link, got %#v", guestShareBody)
	}
	guestShareAccess := guest.json(http.MethodPost, "/api/v1/shares/"+guestShareBody.Token+"/access", `{}`)
	assertStatus(t, guestShareAccess, http.StatusOK)

	freeze := admin.json(http.MethodPatch, "/api/v1/admin/attachments/"+guestAttachment.ID+"/freeze", `{"frozen":true}`)
	assertStatus(t, freeze, http.StatusOK)
	workItems := admin.json(http.MethodGet, "/api/v1/admin/manual-work-items", "")
	assertStatus(t, workItems, http.StatusOK)
	var workBody struct {
		Items []app.ManualWorkItem `json:"items"`
	}
	decodeResponse(t, workItems, &workBody)
	if len(workBody.Items) == 0 {
		t.Fatalf("expected frozen attachment in manual work items, got %#v", workBody)
	}

	panel := admin.json(http.MethodGet, "/api/v1/admin/runtime-panel", "")
	assertStatus(t, panel, http.StatusOK)
	var panelBody app.RuntimePanel
	decodeResponse(t, panel, &panelBody)
	if panelBody.Resources.CollectedAt.IsZero() || !panelBody.Config.GuestUploads.Enabled {
		t.Fatalf("unexpected runtime panel body: %#v", panelBody)
	}

	provider := admin.json(http.MethodPost, "/api/v1/admin/providers/telegram/test", "")
	assertStatus(t, provider, http.StatusOK)
	var providerCfg app.RuntimeConfig
	decodeResponse(t, provider, &providerCfg)
	if providerCfg.ProviderStatus.Telegram.LastTestStatus != "missing_telegram_config" {
		t.Fatalf("expected missing telegram status, got %#v", providerCfg.ProviderStatus.Telegram)
	}
	alert := admin.json(http.MethodPost, "/api/v1/admin/alerts/test", `{"message":"HTTP alert"}`)
	assertStatus(t, alert, http.StatusOK)
	var alertEvent app.AlertEvent
	decodeResponse(t, alert, &alertEvent)
	if alertEvent.Status != "sent" || alertSender.calls != 1 {
		t.Fatalf("expected sent alert event, got event=%#v calls=%d", alertEvent, alertSender.calls)
	}
	alerts := admin.json(http.MethodGet, "/api/v1/admin/alerts", "")
	assertStatus(t, alerts, http.StatusOK)
	var alertsBody struct {
		Alerts []app.AlertEvent `json:"alerts"`
	}
	decodeResponse(t, alerts, &alertsBody)
	if len(alertsBody.Alerts) == 0 {
		t.Fatalf("expected alert history, got %#v", alertsBody)
	}
	auditLogs := admin.json(http.MethodGet, "/api/v1/admin/audit-logs", "")
	assertStatus(t, auditLogs, http.StatusOK)
	var auditBody struct {
		AuditLogs []app.AuditLog `json:"auditLogs"`
	}
	decodeResponse(t, auditLogs, &auditBody)
	for _, action := range []string{
		"admin.runtime_config_update",
		"admin.catalog_update",
		"admin.redemption_batch_create",
		"admin.provider_test",
		"admin.alert_test",
	} {
		if !containsAuditAction(auditBody.AuditLogs, action) {
			t.Fatalf("expected audit action %s, got %#v", action, auditBody.AuditLogs)
		}
	}
}

func TestOAuthWebhookReplayAndReportHTTPContracts(t *testing.T) {
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.Stripe.WebhookSecret = "whsec_test_http_contract"
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
	if got := oauthBody.User.OAuthProviders; len(got) != 1 || got[0] != "google" {
		t.Fatalf("expected google oauth provider in user response, got %#v", got)
	}

	unlink := user.json(http.MethodDelete, "/api/v1/me/oauth/google", "")
	assertStatus(t, unlink, http.StatusOK)
	var unlinked app.UserView
	decodeResponse(t, unlink, &unlinked)
	if len(unlinked.OAuthProviders) != 0 {
		t.Fatalf("expected google provider to be unlinked, got %#v", unlinked.OAuthProviders)
	}

	orderRes := user.json(http.MethodPost, "/api/v1/billing/orders", `{"provider":"stripe","planId":"plus","period":"monthly"}`)
	assertStatus(t, orderRes, http.StatusCreated)
	var order app.Order
	decodeResponse(t, orderRes, &order)

	webhookBodyRaw := stripeWebhookBody(t, "evt_http_1", "checkout.session.completed", order.ID, "tx-http")
	webhookReq := httptest.NewRequest(http.MethodPost, "/api/v1/billing/webhooks/stripe", strings.NewReader(webhookBodyRaw))
	webhookReq.Header.Set("Content-Type", "application/json")
	webhookReq.Header.Set("Stripe-Signature", stripeSignatureHeader(webhookBodyRaw, cfg.Stripe.WebhookSecret, time.Now()))
	webhook := user.do(webhookReq)
	assertStatus(t, webhook, http.StatusOK)
	var webhookBody struct {
		WebhookEvent app.WebhookEvent `json:"webhookEvent"`
		Order        *app.Order       `json:"order"`
	}
	decodeResponse(t, webhook, &webhookBody)
	if webhookBody.Order == nil || webhookBody.Order.Status != "paid" || webhookBody.WebhookEvent.IdempotencyKey != "evt_http_1" {
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

func TestBillingWebhookRequiresProviderSignatures(t *testing.T) {
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.Stripe.WebhookSecret = "whsec_test_signature"
	service := app.New(cfg)
	disableRegistrationEmailVerification(t, service)
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)
	user := newHTTPTestClient(t, handler)

	registerHTTPUser(t, user, "stripe-sig@example.com", "Stripe Sig")
	orderRes := user.json(http.MethodPost, "/api/v1/billing/orders", `{"provider":"stripe","planId":"plus","period":"monthly"}`)
	assertStatus(t, orderRes, http.StatusCreated)
	var order app.Order
	decodeResponse(t, orderRes, &order)

	raw := stripeWebhookBody(t, "evt_sig_1", "checkout.session.completed", order.ID, "pi_sig_1")
	missing := user.json(http.MethodPost, "/api/v1/billing/webhooks/stripe", raw)
	assertStatus(t, missing, http.StatusBadRequest)
	var missingBody map[string]string
	decodeResponse(t, missing, &missingBody)
	if missingBody["error"] != "invalid_webhook_signature" {
		t.Fatalf("expected signature error, got %#v", missingBody)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/billing/webhooks/stripe", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", stripeSignatureHeader(raw, cfg.Stripe.WebhookSecret, time.Now()))
	ok := user.do(req)
	assertStatus(t, ok, http.StatusOK)

	replayReq := httptest.NewRequest(http.MethodPost, "/api/v1/billing/webhooks/stripe", strings.NewReader(raw))
	replayReq.Header.Set("Content-Type", "application/json")
	replayReq.Header.Set("Stripe-Signature", stripeSignatureHeader(raw, cfg.Stripe.WebhookSecret, time.Now()))
	replay := user.do(replayReq)
	assertStatus(t, replay, http.StatusOK)
	var replayBody struct {
		WebhookEvent app.WebhookEvent `json:"webhookEvent"`
		Order        *app.Order       `json:"order"`
	}
	decodeResponse(t, replay, &replayBody)
	if replayBody.WebhookEvent.IdempotencyKey != "evt_sig_1" || replayBody.Order == nil || replayBody.Order.Status != "paid" {
		t.Fatalf("expected idempotent signed webhook replay, got %#v", replayBody)
	}

	refundRaw := stripeWebhookBody(t, "evt_sig_refund_1", "charge.refunded", order.ID, "pi_sig_1")
	refundReq := httptest.NewRequest(http.MethodPost, "/api/v1/billing/webhooks/stripe", strings.NewReader(refundRaw))
	refundReq.Header.Set("Content-Type", "application/json")
	refundReq.Header.Set("Stripe-Signature", stripeSignatureHeader(refundRaw, cfg.Stripe.WebhookSecret, time.Now()))
	refund := user.do(refundReq)
	assertStatus(t, refund, http.StatusOK)
	var refundBody struct {
		WebhookEvent app.WebhookEvent `json:"webhookEvent"`
		Order        *app.Order       `json:"order"`
	}
	decodeResponse(t, refund, &refundBody)
	if refundBody.WebhookEvent.IdempotencyKey != "evt_sig_refund_1" || refundBody.Order == nil || refundBody.Order.Status != "refunded" {
		t.Fatalf("expected signed Stripe refund to mark order refunded, got %#v", refundBody)
	}
}

func TestEpusdtWebhookSignatureAndPlainOKResponse(t *testing.T) {
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	cfg.Epusdt.PID = "1000"
	cfg.Epusdt.SecretKey = "epusdt-test-secret"
	service := app.New(cfg)
	disableRegistrationEmailVerification(t, service)
	adminUser, err := service.SeedAdmin("epusdt-admin@example.com", "password123")
	if err != nil {
		t.Fatalf("seed epusdt admin: %v", err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)
	user := newHTTPTestClient(t, handler)

	registerHTTPUser(t, user, "epusdt-sig@example.com", "Epusdt Sig")
	orderRes := user.json(http.MethodPost, "/api/v1/billing/orders", `{"provider":"epusdt","planId":"plus","period":"monthly"}`)
	assertStatus(t, orderRes, http.StatusCreated)
	var order app.Order
	decodeResponse(t, orderRes, &order)

	payload := map[string]any{
		"pid":           cfg.Epusdt.PID,
		"order_id":      order.ID,
		"trade_id":      "trade_epusdt_1",
		"txid":          "tx_epusdt_1",
		"status":        "success",
		"amount":        json.Number("19.00"),
		"actual_amount": json.Number("19.0001"),
	}
	payload["signature"] = epusdtSignature(payload, cfg.Epusdt.SecretKey)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal epusdt payload: %v", err)
	}
	res := user.json(http.MethodPost, "/api/v1/billing/webhooks/epusdt", string(raw))
	assertStatus(t, res, http.StatusOK)
	if strings.TrimSpace(res.Body.String()) != "ok" {
		t.Fatalf("expected plain ok response, got %q", res.Body.String())
	}

	paidOrder := requireServiceOrderStatus(t, service, order.UserID, order.ID, "paid")
	if paidOrder.TxID != "tx_epusdt_1" {
		t.Fatalf("expected signed epusdt webhook to store tx id, got %#v", paidOrder)
	}
	webhookEvents, err := service.AdminWebhookEvents(adminUser.ID)
	if err != nil {
		t.Fatalf("list epusdt webhook events: %v", err)
	}
	var paidEvent *app.WebhookEvent
	for i := range webhookEvents {
		if webhookEvents[i].IdempotencyKey == "trade_epusdt_1" {
			paidEvent = &webhookEvents[i]
			break
		}
	}
	if paidEvent == nil {
		t.Fatalf("expected paid epusdt webhook event in %#v", webhookEvents)
	}
	if _, ok := paidEvent.Metadata["raw"]; ok {
		t.Fatalf("epusdt webhook metadata must not persist raw payload: %#v", paidEvent.Metadata)
	}
	if _, ok := paidEvent.Metadata["signature"]; ok {
		t.Fatalf("epusdt webhook metadata must not persist signature: %#v", paidEvent.Metadata)
	}
	if paidEvent.Metadata["tradeId"] != "trade_epusdt_1" || paidEvent.Metadata["txId"] != "tx_epusdt_1" {
		t.Fatalf("expected sanitized epusdt identifiers in metadata, got %#v", paidEvent.Metadata)
	}

	canceledPayload := map[string]any{
		"pid":           cfg.Epusdt.PID,
		"order_id":      order.ID,
		"trade_id":      "trade_epusdt_canceled_1",
		"status":        "canceled",
		"amount":        json.Number("19.00"),
		"actual_amount": json.Number("0"),
	}
	canceledPayload["signature"] = epusdtSignature(canceledPayload, cfg.Epusdt.SecretKey)
	canceledRaw, err := json.Marshal(canceledPayload)
	if err != nil {
		t.Fatalf("marshal canceled epusdt payload: %v", err)
	}
	canceledCallback := user.json(http.MethodPost, "/api/v1/billing/webhooks/epusdt", string(canceledRaw))
	assertStatus(t, canceledCallback, http.StatusOK)
	if strings.TrimSpace(canceledCallback.Body.String()) != "ok" {
		t.Fatalf("expected plain ok response for canceled callback, got %q", canceledCallback.Body.String())
	}
	requireServiceOrderStatus(t, service, order.UserID, order.ID, "canceled")
	me := user.json(http.MethodGet, "/api/v1/me", "")
	assertStatus(t, me, http.StatusOK)
	var currentUser app.UserView
	decodeResponse(t, me, &currentUser)
	if currentUser.PlanID != "free" || currentUser.PlanExpiresAt != nil {
		t.Fatalf("expected signed epusdt cancellation to revoke matching plan, got %#v", currentUser)
	}

	expiredRes := user.json(http.MethodPost, "/api/v1/billing/orders", `{"provider":"epusdt","planId":"plus","period":"monthly"}`)
	assertStatus(t, expiredRes, http.StatusCreated)
	var expiredOrder app.Order
	decodeResponse(t, expiredRes, &expiredOrder)
	expiredPayload := map[string]any{
		"pid":           cfg.Epusdt.PID,
		"order_id":      expiredOrder.ID,
		"trade_id":      "trade_epusdt_expired_1",
		"status":        "expired",
		"amount":        json.Number("19.00"),
		"actual_amount": json.Number("0"),
	}
	expiredPayload["signature"] = epusdtSignature(expiredPayload, cfg.Epusdt.SecretKey)
	expiredRaw, err := json.Marshal(expiredPayload)
	if err != nil {
		t.Fatalf("marshal expired epusdt payload: %v", err)
	}
	expiredCallback := user.json(http.MethodPost, "/api/v1/billing/webhooks/epusdt", string(expiredRaw))
	assertStatus(t, expiredCallback, http.StatusOK)
	if strings.TrimSpace(expiredCallback.Body.String()) != "ok" {
		t.Fatalf("expected plain ok response for expired callback, got %q", expiredCallback.Body.String())
	}
	requireServiceOrderStatus(t, service, expiredOrder.UserID, expiredOrder.ID, "expired")
}

type httpTestClient struct {
	t         *testing.T
	handler   http.Handler
	cookies   map[string]*http.Cookie
	csrfToken string
}

type fakeHTTPAlertSender struct {
	calls int
}

func (s *fakeHTTPAlertSender) SendAlert(_ context.Context, _ string, _ bool) error {
	s.calls++
	return nil
}

type staticHTTPScanner struct {
	result app.ScanResult
}

func (s staticHTTPScanner) Scan(_ context.Context, _ string, _ string, _ []byte) (app.ScanResult, error) {
	return s.result, nil
}

func newHTTPTestClient(t *testing.T, handler http.Handler) *httpTestClient {
	t.Helper()
	return &httpTestClient{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
}

func rateLimitTestConfig() config.Config {
	cfg := config.FromEnv()
	cfg.RateLimit = config.RateLimitConfig{
		Enabled:       true,
		WindowSeconds: 60,
		AuthLimit:     1000,
		WriteLimit:    1000,
		UploadLimit:   1000,
		DownloadLimit: 1000,
		WebhookLimit:  1000,
	}
	return cfg
}

func assertRateLimited(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	assertStatus(t, res, http.StatusTooManyRequests)
	if got := res.Header().Get("Retry-After"); got == "" {
		t.Fatalf("expected Retry-After header on rate limited response")
	}
	var body map[string]string
	decodeResponse(t, res, &body)
	if body["error"] != "rate_limited" || body["message"] == "" {
		t.Fatalf("expected rate_limited error body, got %#v", body)
	}
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
	return c.multipartWithFields(path, fieldName, fileName, content, nil)
}

func (c *httpTestClient) multipartWithFields(path string, fieldName string, fileName string, content []byte, fields map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			c.t.Fatalf("write multipart field %s: %v", key, err)
		}
	}
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
	if requiresCSRF(req) && req.Header.Get(csrfHeaderName) == "" {
		c.ensureCSRF()
		req.Header.Set(csrfHeaderName, c.csrfToken)
	}
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

func (c *httpTestClient) ensureCSRF() {
	c.t.Helper()
	if c.csrfToken != "" {
		return
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/csrf", nil)
	req.Header.Set("Accept", "application/json")
	res := c.doWithoutCSRF(req)
	assertStatus(c.t, res, http.StatusOK)
	var body struct {
		CSRFToken string `json:"csrfToken"`
	}
	decodeResponse(c.t, res, &body)
	if body.CSRFToken == "" {
		c.t.Fatalf("expected csrf token response, got %#v", body)
	}
	c.csrfToken = body.CSRFToken
}

func registerHTTPUser(t *testing.T, client *httpTestClient, email string, displayName string) *httptest.ResponseRecorder {
	t.Helper()
	start := client.json(http.MethodPost, "/api/v1/auth/registration/email-verification/start", `{"email":"`+email+`"}`)
	assertStatus(t, start, http.StatusOK)
	var startBody struct {
		DevToken string `json:"devToken"`
	}
	decodeResponse(t, start, &startBody)
	registerBody := `{"email":"` + email + `","password":"password123","displayName":"` + displayName + `"}`
	if startBody.DevToken != "" {
		registerBody = `{"email":"` + email + `","password":"password123","displayName":"` + displayName + `","emailVerificationCode":"` + startBody.DevToken + `"}`
	}
	register := client.json(http.MethodPost, "/api/v1/auth/register", registerBody)
	assertStatus(t, register, http.StatusCreated)
	return register
}

func disableRegistrationEmailVerification(t *testing.T, service *app.Service) {
	t.Helper()
	admin, err := service.SeedAdmin("test-admin@example.com", "password123")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	runtimeCfg, err := service.AdminRuntimeConfig(admin.ID)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runtimeCfg.Registration.RequireEmailVerification = false
	if _, err := service.AdminUpdateRuntimeConfig(admin.ID, app.RuntimeConfigPatch{Registration: registrationConfigPatch(runtimeCfg.Registration)}); err != nil {
		t.Fatalf("disable registration verification: %v", err)
	}
}

func (c *httpTestClient) doWithoutCSRF(req *http.Request) *httptest.ResponseRecorder {
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

func assertNoDevTokenFields(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]json.RawMessage
	decodeResponse(t, res, &body)
	for _, key := range []string{"devEmailVerificationToken", "devToken"} {
		if _, ok := body[key]; ok {
			t.Fatalf("production response must not expose %s: %s", key, res.Body.String())
		}
	}
}

func mailQueueDepthMetric(t *testing.T, handler http.Handler, token string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	assertStatus(t, res, http.StatusOK)
	for _, line := range strings.Split(res.Body.String(), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 || fields[0] != "pastebox_mail_queue_depth" {
			continue
		}
		value, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("parse mail queue metric %q: %v", line, err)
		}
		return value
	}
	t.Fatalf("expected pastebox_mail_queue_depth metric in:\n%s", res.Body.String())
	return 0
}

func sessionCookieFromResponse(t *testing.T, res *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	return cookieFromResponse(t, res, sessionCookieName)
}

func addCSRFToken(t *testing.T, handler http.Handler, req *http.Request) {
	t.Helper()
	tokenReq := httptest.NewRequest(http.MethodGet, "/api/v1/csrf", nil)
	if req.Header.Get("X-Forwarded-Proto") != "" {
		tokenReq.Header.Set("X-Forwarded-Proto", req.Header.Get("X-Forwarded-Proto"))
	}
	if req.Header.Get("Forwarded") != "" {
		tokenReq.Header.Set("Forwarded", req.Header.Get("Forwarded"))
	}
	tokenRes := httptest.NewRecorder()
	handler.ServeHTTP(tokenRes, tokenReq)
	assertStatus(t, tokenRes, http.StatusOK)
	var body struct {
		CSRFToken string `json:"csrfToken"`
	}
	decodeResponse(t, tokenRes, &body)
	if body.CSRFToken == "" {
		t.Fatalf("expected csrf token response, got %#v", body)
	}
	req.Header.Set(csrfHeaderName, body.CSRFToken)
	req.AddCookie(cookieFromResponse(t, tokenRes, csrfCookieName))
}

func cookieFromResponse(t *testing.T, res *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("expected %s cookie in response headers: %v", name, res.Result().Header.Values("Set-Cookie"))
	return nil
}

func requireServiceOrderStatus(t *testing.T, service *app.Service, userID string, orderID string, status string) app.Order {
	t.Helper()
	orders, err := service.ListOrders(userID)
	if err != nil {
		t.Fatalf("list orders: %v", err)
	}
	for _, order := range orders {
		if order.ID == orderID {
			if order.Status != status {
				t.Fatalf("expected order %s status %q, got %#v", orderID, status, order)
			}
			return order
		}
	}
	t.Fatalf("expected order %s in %#v", orderID, orders)
	return app.Order{}
}

func stripeWebhookBody(t *testing.T, eventID string, eventType string, orderID string, txID string) string {
	t.Helper()
	payload := map[string]any{
		"id":   eventID,
		"type": eventType,
		"data": map[string]any{
			"object": map[string]any{
				"id":                  txID,
				"client_reference_id": orderID,
				"payment_intent":      txID,
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal stripe payload: %v", err)
	}
	return string(raw)
}

func stripeSignatureHeader(raw string, secret string, timestamp time.Time) string {
	ts := strconv.FormatInt(timestamp.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(ts))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write([]byte(raw))
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func epusdtSignature(payload map[string]any, secret string) string {
	keys := make([]string, 0, len(payload))
	for key, value := range payload {
		if key == "signature" {
			continue
		}
		rendered := fmt.Sprint(value)
		if strings.TrimSpace(rendered) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+fmt.Sprint(payload[key]))
	}
	sum := md5.Sum([]byte(strings.Join(parts, "&") + secret))
	return hex.EncodeToString(sum[:])
}

func signGoogleTestIDToken(t *testing.T, key *rsa.PrivateKey, keyID string, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "kid": keyID, "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal test google id token header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal test google id token claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign test google id token: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func googleTestJWK(keyID string, key *rsa.PublicKey) map[string]string {
	return map[string]string{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": keyID,
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
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

func containsAuditMetadata(logs []app.AuditLog, action string, target string, key string, value any) bool {
	for _, log := range logs {
		if log.Action == action && log.Target == target && log.Metadata[key] == value {
			return true
		}
	}
	return false
}

func ptr[T any](value T) *T {
	return &value
}

func registrationConfigPatch(cfg app.RegistrationConfig) *app.RegistrationConfigPatch {
	return &app.RegistrationConfigPatch{
		AllowedDomains:           ptr(append([]string{}, cfg.AllowedDomains...)),
		RequireEmailVerification: ptr(cfg.RequireEmailVerification),
		RequireTurnstile:         ptr(cfg.RequireTurnstile),
		TurnstileSiteKey:         ptr(cfg.TurnstileSiteKey),
	}
}

func runtimeRateLimitConfigPatch(cfg app.RuntimeRateLimitConfig) *app.RuntimeRateLimitConfigPatch {
	return &app.RuntimeRateLimitConfigPatch{
		Enabled:                ptr(cfg.Enabled),
		WindowSeconds:          ptr(cfg.WindowSeconds),
		EmailVerificationLimit: ptr(cfg.EmailVerificationLimit),
		RegisterLimit:          ptr(cfg.RegisterLimit),
		LoginLimit:             ptr(cfg.LoginLimit),
		WriteLimit:             ptr(cfg.WriteLimit),
		UploadLimit:            ptr(cfg.UploadLimit),
		ShareCreateLimit:       ptr(cfg.ShareCreateLimit),
		ShareAccessLimit:       ptr(cfg.ShareAccessLimit),
		DownloadLimit:          ptr(cfg.DownloadLimit),
		WebhookLimit:           ptr(cfg.WebhookLimit),
	}
}

type testWriter struct {
	t *testing.T
}

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	return len(p), nil
}
