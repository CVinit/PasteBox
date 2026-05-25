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
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

const (
	sessionCookieName          = "pastebox_session"
	googleOAuthStateCookieName = "pastebox_google_oauth_state"
	csrfCookieName             = "pastebox_csrf"
	csrfHeaderName             = "X-CSRF-Token"
)

var (
	googleOAuthAuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	googleOAuthTokenURL     = "https://oauth2.googleapis.com/token"
	googleOAuthJWKSURL      = "https://www.googleapis.com/oauth2/v3/certs"
	googleOAuthHTTPClient   = &http.Client{Timeout: 10 * time.Second}
)

//go:embed static
var staticFS embed.FS

type Server struct {
	cfg          config.Config
	logger       *slog.Logger
	app          *app.Service
	readiness    ReadinessChecker
	metricsMu    sync.Mutex
	httpRequests map[httpMetricKey]int64
}

type httpMetricKey struct {
	Method string
	Path   string
	Status int
}

type ReadinessChecker func(ctx context.Context) []ReadinessComponent

type ReadinessComponent struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type ReadinessReport struct {
	App        string               `json:"app"`
	Env        string               `json:"env"`
	Status     string               `json:"status"`
	Components []ReadinessComponent `json:"components"`
}

func New(cfg config.Config, logger *slog.Logger) http.Handler {
	return NewWithService(cfg, logger, app.New(cfg))
}

func NewWithService(cfg config.Config, logger *slog.Logger, service *app.Service) http.Handler {
	return NewWithServiceAndReadiness(cfg, logger, service, nil)
}

func NewWithServiceAndReadiness(cfg config.Config, logger *slog.Logger, service *app.Service, readiness ReadinessChecker) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if service == nil {
		service = app.New(cfg)
	}
	if readiness == nil {
		readiness = func(context.Context) []ReadinessComponent {
			return []ReadinessComponent{{Name: "application", Status: "ok"}}
		}
	}

	server := &Server{
		cfg:          cfg,
		logger:       logger,
		app:          service,
		readiness:    readiness,
		httpRequests: map[httpMetricKey]int64{},
	}
	return server.routes()
}

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(s.secureHeaders)
	r.Use(s.cors)
	r.Use(s.logRequests)

	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)
	r.Get("/metrics", s.metrics)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.csrfProtection)
		r.Get("/health", s.apiHealth)
		r.Get("/ready", s.apiReady)
		r.Get("/csrf", s.csrf)
		r.Get("/plans", s.planCatalog)

		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", s.register)
			r.Post("/login", s.login)
			r.Post("/logout", s.logout)
			r.Post("/logout-all", s.logoutAll)
			r.Get("/google/start", s.googleOAuthStart)
			r.Get("/google/callback", s.googleOAuthCallback)
			r.Post("/google", s.googleOAuth)
			r.Post("/email-verification/start", s.startEmailVerification)
			r.Post("/email-verification/finish", s.finishEmailVerification)
			r.Post("/magic/start", s.startMagic)
			r.Post("/magic/finish", s.finishMagic)
			r.Post("/password-reset/start", s.startPasswordReset)
			r.Post("/password-reset/finish", s.finishPasswordReset)
		})

		r.Get("/me", s.me)
		r.Patch("/me", s.updateMe)
		r.Post("/me/delete-request", s.requestAccountDeletion)
		r.Post("/me/delete-cancel", s.cancelAccountDeletion)
		r.Post("/me/delete-now", s.executeAccountDeletion)
		r.Get("/me/export", s.exportMe)

		r.Get("/quota", s.quota)

		r.Route("/pastes", func(r chi.Router) {
			r.Get("/", s.listPastes)
			r.Post("/", s.createPaste)
			r.Get("/{pasteID}", s.getPaste)
			r.Patch("/{pasteID}", s.updatePaste)
			r.Delete("/{pasteID}", s.deletePaste)
			r.Post("/{pasteID}/extend", s.extendPaste)
			r.Post("/{pasteID}/attachments", s.uploadAttachment)
			r.Post("/{pasteID}/shares", s.createShare)
		})

		r.Get("/attachments/{attachmentID}/download", s.downloadAttachment)

		r.Route("/shares", func(r chi.Router) {
			r.Get("/", s.listShares)
			r.Delete("/{shareID}", s.revokeShare)
			r.Post("/{token}/access", s.accessShare)
			r.Get("/{token}/attachments/{attachmentID}/download", s.downloadSharedAttachment)
		})

		r.Route("/billing", func(r chi.Router) {
			r.Get("/prices", s.prices)
			r.Get("/orders", s.listOrders)
			r.Post("/orders", s.createOrder)
			r.Post("/webhooks/{provider}", s.billingWebhook)
		})

		r.Post("/reports", s.report)

		r.Route("/admin", func(r chi.Router) {
			r.Get("/dashboard", s.adminDashboard)
			r.Get("/users", s.adminUsers)
			r.Patch("/users/{userID}/plan", s.adminSetUserPlan)
			r.Patch("/users/{userID}/freeze", s.adminFreezeUser)
			r.Get("/pastes", s.adminPastes)
			r.Post("/pastes/{pasteID}/takedown", s.adminTakedownPaste)
			r.Get("/attachments", s.adminAttachments)
			r.Patch("/attachments/{attachmentID}/freeze", s.adminFreezeAttachment)
			r.Post("/attachments/{attachmentID}/retry-scan", s.adminRetryScan)
			r.Get("/shares", s.adminShares)
			r.Post("/shares/{shareID}/revoke", s.adminRevokeShare)
			r.Get("/orders", s.adminOrders)
			r.Get("/webhook-events", s.adminWebhookEvents)
			r.Post("/webhook-events/{eventID}/replay", s.adminReplayWebhookEvent)
			r.Get("/audit-logs", s.adminAuditLogs)
			r.Get("/queues", s.adminQueues)
			r.Post("/reports/{reportID}/status", s.adminResolveReport)
			r.Post("/billing/reconcile", s.adminRunBillingReconciliation)
			r.Post("/cleanup/run", s.adminRunCleanup)
			r.Post("/orders/{orderID}/mark-paid", s.adminMarkOrderPaid)
		})
	})

	r.NotFound(s.staticFallback)

	return r
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	s.writeReadiness(w, r)
}

func (s *Server) apiHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"app":    s.cfg.AppName,
		"env":    s.cfg.AppEnv,
		"status": "ok",
	})
}

func (s *Server) apiReady(w http.ResponseWriter, r *http.Request) {
	s.writeReadiness(w, r)
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if !s.validMetricsToken(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="pastebox metrics"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "metrics_unauthorized", "message": "metrics token is missing or invalid"})
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(s.prometheusMetrics(r.Context())))
}

func (s *Server) validMetricsToken(r *http.Request) bool {
	expected := strings.TrimSpace(s.cfg.MetricsToken)
	if expected == "" {
		return false
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(expected)) == 1
}

func (s *Server) writeReadiness(w http.ResponseWriter, r *http.Request) {
	components := s.readiness(r.Context())
	status := "ready"
	for _, component := range components {
		if component.Status != "ok" && component.Status != "skipped" {
			status = "not_ready"
			break
		}
	}
	code := http.StatusOK
	if status != "ready" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, ReadinessReport{App: s.cfg.AppName, Env: s.cfg.AppEnv, Status: status, Components: components})
}

func (s *Server) prometheusMetrics(ctx context.Context) string {
	var b strings.Builder
	components := s.readiness(ctx)
	ready := 1
	for _, component := range components {
		if component.Status != "ok" && component.Status != "skipped" {
			ready = 0
			break
		}
	}

	writeMetricHelp(&b, "pastebox_info", "PasteBox process information.")
	writeMetricType(&b, "pastebox_info", "gauge")
	writeMetric(&b, "pastebox_info", map[string]string{"app": s.cfg.AppName, "env": s.cfg.AppEnv}, 1)
	writeMetricHelp(&b, "pastebox_readiness_ready", "Overall readiness state, 1 when every component is ok or skipped.")
	writeMetricType(&b, "pastebox_readiness_ready", "gauge")
	writeMetric(&b, "pastebox_readiness_ready", nil, float64(ready))
	writeMetricHelp(&b, "pastebox_readiness_component_ready", "Readiness component state, 1 when the component is ok or skipped.")
	writeMetricType(&b, "pastebox_readiness_component_ready", "gauge")
	for _, component := range components {
		componentReady := 0
		if component.Status == "ok" || component.Status == "skipped" {
			componentReady = 1
		}
		writeMetric(&b, "pastebox_readiness_component_ready", map[string]string{
			"name":   component.Name,
			"status": component.Status,
		}, float64(componentReady))
	}

	writeMetricHelp(&b, "pastebox_http_requests_total", "HTTP requests handled by method, route pattern, and status code.")
	writeMetricType(&b, "pastebox_http_requests_total", "counter")
	for _, sample := range s.httpRequestSamples() {
		writeMetric(&b, "pastebox_http_requests_total", map[string]string{
			"method": sample.key.Method,
			"path":   sample.key.Path,
			"status": strconv.Itoa(sample.key.Status),
		}, float64(sample.count))
	}

	ops, err := s.app.OperationalMetrics()
	writeMetricHelp(&b, "pastebox_operational_metrics_available", "Whether aggregate operational metrics could be loaded.")
	writeMetricType(&b, "pastebox_operational_metrics_available", "gauge")
	if err != nil {
		writeMetric(&b, "pastebox_operational_metrics_available", nil, 0)
		return b.String()
	}
	writeMetric(&b, "pastebox_operational_metrics_available", nil, 1)
	writeOperationalMetrics(&b, ops)
	return b.String()
}

type httpRequestSample struct {
	key   httpMetricKey
	count int64
}

func (s *Server) httpRequestSamples() []httpRequestSample {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	samples := make([]httpRequestSample, 0, len(s.httpRequests))
	for key, count := range s.httpRequests {
		samples = append(samples, httpRequestSample{key: key, count: count})
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].key.Method != samples[j].key.Method {
			return samples[i].key.Method < samples[j].key.Method
		}
		if samples[i].key.Path != samples[j].key.Path {
			return samples[i].key.Path < samples[j].key.Path
		}
		return samples[i].key.Status < samples[j].key.Status
	})
	return samples
}

func writeOperationalMetrics(b *strings.Builder, ops app.OperationalMetrics) {
	writeMetricHelp(b, "pastebox_users_total", "Total known PasteBox users.")
	writeMetricType(b, "pastebox_users_total", "gauge")
	writeMetric(b, "pastebox_users_total", nil, float64(ops.UserCount))
	writeMetricHelp(b, "pastebox_active_pastes", "Currently visible active pastes.")
	writeMetricType(b, "pastebox_active_pastes", "gauge")
	writeMetric(b, "pastebox_active_pastes", nil, float64(ops.ActivePastes))
	writeMetricHelp(b, "pastebox_active_storage_bytes", "Currently active paste and attachment bytes.")
	writeMetricType(b, "pastebox_active_storage_bytes", "gauge")
	writeMetric(b, "pastebox_active_storage_bytes", nil, float64(ops.ActiveStorageBytes))
	writeMetricHelp(b, "pastebox_reports_open", "Open abuse or support reports.")
	writeMetricType(b, "pastebox_reports_open", "gauge")
	writeMetric(b, "pastebox_reports_open", nil, float64(ops.ReportsOpen))
	writeMetricHelp(b, "pastebox_queue_depth", "Operational queue depth by kind and status.")
	writeMetricType(b, "pastebox_queue_depth", "gauge")
	writeMetric(b, "pastebox_queue_depth", map[string]string{"kind": "cleanup", "status": "pending"}, float64(ops.CleanupQueueDepth))
	writeMetric(b, "pastebox_queue_depth", map[string]string{"kind": "scan", "status": "pending"}, float64(ops.ScanQueueDepth))
	writeMetric(b, "pastebox_queue_depth", map[string]string{"kind": "scan", "status": "failed"}, float64(ops.ScanFailureDepth))
	writeMetric(b, "pastebox_queue_depth", map[string]string{"kind": "all", "status": "failed"}, float64(ops.FailedJobDepth))
	writeMetricHelp(b, "pastebox_mail_queue_depth", "Queued outbound mails waiting for delivery.")
	writeMetricType(b, "pastebox_mail_queue_depth", "gauge")
	writeMetric(b, "pastebox_mail_queue_depth", nil, float64(ops.MailQueueDepth))
	writeMetricHelp(b, "pastebox_webhook_events_total", "Stored provider webhook events.")
	writeMetricType(b, "pastebox_webhook_events_total", "gauge")
	writeMetric(b, "pastebox_webhook_events_total", nil, float64(ops.WebhookEvents))
	writeMetricHelp(b, "pastebox_billing_orders_total", "Stored billing orders by lifecycle status.")
	writeMetricType(b, "pastebox_billing_orders_total", "gauge")
	statuses := make([]string, 0, len(ops.OrdersByStatus))
	for status := range ops.OrdersByStatus {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	for _, status := range statuses {
		writeMetric(b, "pastebox_billing_orders_total", map[string]string{"status": status}, float64(ops.OrdersByStatus[status]))
	}
}

func writeMetricHelp(b *strings.Builder, name string, help string) {
	b.WriteString("# HELP ")
	b.WriteString(name)
	b.WriteByte(' ')
	b.WriteString(help)
	b.WriteByte('\n')
}

func writeMetricType(b *strings.Builder, name string, metricType string) {
	b.WriteString("# TYPE ")
	b.WriteString(name)
	b.WriteByte(' ')
	b.WriteString(metricType)
	b.WriteByte('\n')
}

func writeMetric(b *strings.Builder, name string, labels map[string]string, value float64) {
	b.WriteString(name)
	if len(labels) > 0 {
		keys := make([]string, 0, len(labels))
		for key := range labels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(key)
			b.WriteByte('=')
			b.WriteString(strconv.Quote(labels[key]))
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
	b.WriteByte('\n')
}

func (s *Server) planCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.app.PlanCatalog())
}

func (s *Server) csrf(w http.ResponseWriter, r *http.Request) {
	token, signed, err := s.newCSRFToken()
	if err != nil {
		s.handleErr(w, err)
		return
	}
	s.setCSRFCookie(w, r, signed)
	writeJSON(w, http.StatusOK, map[string]string{"csrfToken": token})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"displayName"`
		Language    string `json:"language"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	result, err := s.app.Register(r.Context(), app.RegisterInput{
		Email:       req.Email,
		Password:    req.Password,
		DisplayName: req.DisplayName,
		Language:    req.Language,
	})
	if s.handleErr(w, err) {
		return
	}
	s.setSessionCookie(w, r, result.SessionID, result.ExpiresAt)
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	result, err := s.app.Login(r.Context(), req.Email, req.Password)
	if s.handleErr(w, err) {
		return
	}
	s.setSessionCookie(w, r, result.SessionID, result.ExpiresAt)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) googleOAuth(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AppEnv == "production" {
		s.handleErr(w, app.E(http.StatusNotFound, "not_found", "use the Google OAuth redirect flow"))
		return
	}
	var req struct {
		Email         string `json:"email"`
		DisplayName   string `json:"displayName"`
		GoogleSubject string `json:"googleSubject"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	result, err := s.app.GoogleOAuth(r.Context(), req.Email, req.DisplayName, req.GoogleSubject)
	if s.handleErr(w, err) {
		return
	}
	s.setSessionCookie(w, r, result.SessionID, result.ExpiresAt)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) googleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if !s.googleOAuthConfigured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "google_oauth_not_configured", "message": "Google OAuth is not configured"})
		return
	}
	state, err := randomURLToken(32)
	if err != nil {
		s.handleErr(w, err)
		return
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		s.handleErr(w, err)
		return
	}
	returnTo := sanitizeOAuthReturnTo(r.URL.Query().Get("returnTo"))
	cookieValue, err := s.signGoogleOAuthState(googleOAuthState{
		State:    state,
		Nonce:    nonce,
		ReturnTo: returnTo,
		IssuedAt: time.Now().UTC().Unix(),
	})
	if err != nil {
		s.handleErr(w, err)
		return
	}
	s.setGoogleOAuthStateCookie(w, r, cookieValue, 10*time.Minute)

	params := url.Values{}
	params.Set("client_id", s.cfg.GoogleOAuth.ClientID)
	params.Set("redirect_uri", s.cfg.GoogleOAuth.RedirectURL)
	params.Set("response_type", "code")
	params.Set("scope", "openid email profile")
	params.Set("state", state)
	params.Set("nonce", nonce)
	params.Set("prompt", "select_account")
	http.Redirect(w, r, googleOAuthAuthorizeURL+"?"+params.Encode(), http.StatusSeeOther)
}

func (s *Server) googleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !s.googleOAuthConfigured() {
		s.redirectGoogleOAuthError(w, r, "google_oauth_not_configured")
		return
	}
	if providerErr := strings.TrimSpace(r.URL.Query().Get("error")); providerErr != "" {
		s.redirectGoogleOAuthError(w, r, "google_"+providerErr)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		s.redirectGoogleOAuthError(w, r, "invalid_google_callback")
		return
	}
	statePayload, err := s.googleOAuthStateFromRequest(r)
	if err != nil || subtle.ConstantTimeCompare([]byte(statePayload.State), []byte(state)) != 1 {
		s.redirectGoogleOAuthError(w, r, "invalid_google_state")
		return
	}
	s.clearGoogleOAuthStateCookie(w, r)

	idToken, err := exchangeGoogleOAuthCode(r.Context(), s.cfg.GoogleOAuth, code)
	if err != nil {
		s.redirectGoogleOAuthError(w, r, "google_token_exchange_failed")
		return
	}
	identity, err := verifyGoogleIDToken(r.Context(), s.cfg.GoogleOAuth.ClientID, statePayload.Nonce, idToken)
	if err != nil {
		s.redirectGoogleOAuthError(w, r, "google_identity_failed")
		return
	}
	result, err := s.app.GoogleOAuth(r.Context(), identity.Email, identity.Name, identity.Subject)
	if err != nil {
		s.redirectGoogleOAuthError(w, r, "google_account_failed")
		return
	}
	s.setSessionCookie(w, r, result.SessionID, result.ExpiresAt)
	http.Redirect(w, r, statePayload.ReturnTo, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.app.Logout(cookie.Value)
	}
	s.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	s.app.LogoutAll(user.ID)
	s.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) startEmailVerification(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	resp, err := s.app.StartEmailVerification(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) finishEmailVerification(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	user, err := s.app.FinishEmailVerification(req.Token)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) startMagic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	resp, err := s.app.StartMagicLink(r.Context(), req.Email)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) finishMagic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	result, err := s.app.ConsumeMagicLink(r.Context(), req.Token)
	if s.handleErr(w, err) {
		return
	}
	s.setSessionCookie(w, r, result.SessionID, result.ExpiresAt)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) startPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	resp, err := s.app.StartPasswordReset(r.Context(), req.Email)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) finishPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	err := s.app.FinishPasswordReset(r.Context(), req.Token, req.Password)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		DisplayName string `json:"displayName"`
		Language    string `json:"language"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	updated, err := s.app.UpdateProfile(user.ID, req.DisplayName, req.Language)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) requestAccountDeletion(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	updated, err := s.app.RequestAccountDeletion(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) cancelAccountDeletion(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	updated, err := s.app.CancelAccountDeletion(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) executeAccountDeletion(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	err := s.app.ExecuteAccountDeletion(user.ID)
	if s.handleErr(w, err) {
		return
	}
	s.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) exportMe(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	payload, err := s.app.ExportUser(user.ID)
	if s.handleErr(w, err) {
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="pastebox-export.json"`)
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) quota(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	quota, err := s.app.Quota(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, quota)
}

func (s *Server) listPastes(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	items, err := s.app.ListPastes(user.ID, app.ListOptions{
		Query:  r.URL.Query().Get("query"),
		Filter: r.URL.Query().Get("filter"),
		Tag:    r.URL.Query().Get("tag"),
	})
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pastes": items})
}

func (s *Server) createPaste(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req pasteRequest
	if !s.decode(w, r, &req) {
		return
	}
	paste, err := s.app.CreatePaste(user.ID, app.PasteInput{
		Title:            req.Title,
		Text:             req.Text,
		Tags:             req.Tags,
		Pinned:           req.Pinned,
		Favorite:         req.Favorite,
		ExpiresInSeconds: req.ExpiresInSeconds,
	})
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, paste)
}

func (s *Server) getPaste(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	paste, err := s.app.GetPaste(user.ID, chi.URLParam(r, "pasteID"))
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, paste)
}

func (s *Server) updatePaste(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req pastePatchRequest
	if !s.decode(w, r, &req) {
		return
	}
	paste, err := s.app.UpdatePaste(user.ID, chi.URLParam(r, "pasteID"), app.PastePatch{
		Title:    req.Title,
		Text:     req.Text,
		Tags:     req.Tags,
		HasTags:  req.Tags != nil,
		Pinned:   req.Pinned,
		Favorite: req.Favorite,
	})
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, paste)
}

func (s *Server) deletePaste(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if s.handleErr(w, s.app.DeletePaste(user.ID, chi.URLParam(r, "pasteID"))) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) extendPaste(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		ExpiresInSeconds int64 `json:"expiresInSeconds"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	paste, err := s.app.ExtendPaste(user.ID, chi.URLParam(r, "pasteID"), req.ExpiresInSeconds)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, paste)
}

func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_multipart", "message": "invalid multipart form"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_file", "message": "file is required"})
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 5<<30))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read_failed", "message": "failed to read file"})
		return
	}
	attachment, err := s.app.AddAttachment(user.ID, chi.URLParam(r, "pasteID"), header.Filename, contentType(header), content)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, attachment)
}

func (s *Server) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	attachment, content, err := s.app.DownloadAttachment(user.ID, chi.URLParam(r, "attachmentID"))
	if s.handleErr(w, err) {
		return
	}
	s.writeDownload(w, attachment, content)
}

func (s *Server) createShare(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Password         string `json:"password"`
		LoginRequired    bool   `json:"loginRequired"`
		MaxVisits        int    `json:"maxVisits"`
		MaxDownloads     int    `json:"maxDownloads"`
		ExpiresInSeconds int64  `json:"expiresInSeconds"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	share, err := s.app.CreateShare(user.ID, chi.URLParam(r, "pasteID"), app.ShareInput{
		Password:         req.Password,
		LoginRequired:    req.LoginRequired,
		MaxVisits:        req.MaxVisits,
		MaxDownloads:     req.MaxDownloads,
		ExpiresInSeconds: req.ExpiresInSeconds,
	})
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, share)
}

func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	shares, err := s.app.ListShares(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": shares})
}

func (s *Server) revokeShare(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if s.handleErr(w, s.app.RevokeShare(user.ID, chi.URLParam(r, "shareID"))) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) accessShare(w http.ResponseWriter, r *http.Request) {
	viewerID := s.optionalUserID(r)
	var req struct {
		Password string `json:"password"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	paste, share, err := s.app.AccessShare(chi.URLParam(r, "token"), req.Password, viewerID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paste": paste, "share": share})
}

func (s *Server) downloadSharedAttachment(w http.ResponseWriter, r *http.Request) {
	viewerID := s.optionalUserID(r)
	password := r.URL.Query().Get("password")
	attachment, content, err := s.app.DownloadSharedAttachment(chi.URLParam(r, "token"), password, chi.URLParam(r, "attachmentID"), viewerID)
	if s.handleErr(w, err) {
		return
	}
	s.writeDownload(w, attachment, content)
}

func (s *Server) prices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.app.Prices())
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Provider string `json:"provider"`
		PlanID   string `json:"planId"`
		Period   string `json:"period"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	order, err := s.app.CreateOrder(user.ID, req.Provider, req.PlanID, req.Period)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, order)
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	orders, err := s.app.ListOrders(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

func (s *Server) billingWebhook(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "provider")))
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_webhook", "message": "failed to read webhook body"})
		return
	}

	input, err := s.verifiedBillingWebhookInput(provider, raw, r.Header)
	if s.handleErr(w, err) {
		return
	}
	event, order, err := s.app.ProcessBillingWebhook(input)
	if s.handleErr(w, err) {
		return
	}
	if provider == "epusdt" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhookEvent": event, "order": order})
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target string `json:"target"`
		Reason string `json:"reason"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	reporterID := s.optionalUserID(r)
	report, err := s.app.Report(reporterID, req.Target, req.Reason)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, report)
}

func (s *Server) adminDashboard(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	payload, err := s.app.AdminDashboard(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	users, err := s.app.AdminUsers(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) adminSetUserPlan(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		PlanID    string     `json:"planId"`
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	updated, err := s.app.AdminSetUserPlan(user.ID, chi.URLParam(r, "userID"), req.PlanID, req.ExpiresAt)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) adminFreezeUser(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Frozen bool `json:"frozen"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	updated, err := s.app.AdminFreezeUser(user.ID, chi.URLParam(r, "userID"), req.Frozen)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) adminPastes(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	pastes, err := s.app.AdminPastes(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pastes": pastes})
}

func (s *Server) adminTakedownPaste(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if s.handleErr(w, s.app.AdminTakedownPaste(user.ID, chi.URLParam(r, "pasteID"))) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "taken_down"})
}

func (s *Server) adminAttachments(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	attachments, err := s.app.AdminAttachments(user.ID, r.URL.Query().Get("query"))
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attachments": attachments})
}

func (s *Server) adminFreezeAttachment(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Frozen bool `json:"frozen"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	attachment, err := s.app.AdminFreezeAttachment(user.ID, chi.URLParam(r, "attachmentID"), req.Frozen)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, attachment)
}

func (s *Server) adminRetryScan(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	attachment, err := s.app.AdminRetryScan(user.ID, chi.URLParam(r, "attachmentID"))
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, attachment)
}

func (s *Server) adminShares(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	shares, err := s.app.AdminShares(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": shares})
}

func (s *Server) adminRevokeShare(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	share, err := s.app.AdminRevokeShare(user.ID, chi.URLParam(r, "shareID"))
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, share)
}

func (s *Server) adminOrders(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	orders, err := s.app.AdminOrders(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

func (s *Server) adminWebhookEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	events, err := s.app.AdminWebhookEvents(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhookEvents": events})
}

func (s *Server) adminReplayWebhookEvent(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	event, err := s.app.ReplayWebhookEvent(user.ID, chi.URLParam(r, "eventID"))
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) adminAuditLogs(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	logs, err := s.app.AdminAuditLogs(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"auditLogs": logs})
}

func (s *Server) adminQueues(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	queues, err := s.app.AdminQueues(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, queues)
}

func (s *Server) adminResolveReport(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	report, err := s.app.AdminResolveReport(user.ID, chi.URLParam(r, "reportID"), req.Status)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) adminRunCleanup(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	result, err := s.app.RunCleanup(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) adminRunBillingReconciliation(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	result, err := s.app.RunBillingReconciliation(user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) adminMarkOrderPaid(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		TxID string `json:"txId"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	order, err := s.app.MarkOrderPaid(user.ID, chi.URLParam(r, "orderID"), req.TxID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) staticFallback(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "static_unavailable"})
		return
	}

	filePath := strings.TrimPrefix(r.URL.Path, "/")
	if filePath == "" {
		filePath = "index.html"
	}
	if info, err := fs.Stat(sub, filePath); err == nil && !info.IsDir() {
		http.FileServer(http.FS(sub)).ServeHTTP(w, r)
		return
	}
	if strings.Contains(filePath, ".") {
		http.NotFound(w, r)
		return
	}

	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "static_unavailable"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(index)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		s.recordHTTPRequest(r, status)

		s.logger.Info(
			"http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}

func (s *Server) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		header.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if s.corsOriginAllowed(origin) {
			header := w.Header()
			header.Add("Vary", "Origin")
			header.Set("Access-Control-Allow-Origin", origin)
			header.Set("Access-Control-Allow-Credentials", "true")
			header.Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token")
			header.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			header.Set("Access-Control-Max-Age", "600")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) corsOriginAllowed(origin string) bool {
	for _, allowed := range s.cfg.CORSAllowedOrigins {
		if strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

func (s *Server) recordHTTPRequest(r *http.Request, status int) {
	routePath := chi.RouteContext(r.Context()).RoutePattern()
	if routePath == "" {
		routePath = r.URL.Path
	}
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	s.httpRequests[httpMetricKey{Method: r.Method, Path: routePath, Status: status}]++
}

func (s *Server) csrfProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requiresCSRF(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !s.validCSRF(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf_required", "message": "CSRF token is missing or invalid"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requiresCSRF(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return !strings.HasPrefix(r.URL.Path, "/api/v1/billing/webhooks/")
}

type pasteRequest struct {
	Title            string   `json:"title"`
	Text             string   `json:"text"`
	Tags             []string `json:"tags"`
	Pinned           bool     `json:"pinned"`
	Favorite         bool     `json:"favorite"`
	ExpiresInSeconds int64    `json:"expiresInSeconds"`
}

type pastePatchRequest struct {
	Title    *string  `json:"title"`
	Text     *string  `json:"text"`
	Tags     []string `json:"tags"`
	Pinned   *bool    `json:"pinned"`
	Favorite *bool    `json:"favorite"`
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json", "message": "request body is invalid"})
		return false
	}
	return true
}

func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (app.UserView, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated", "message": "login required"})
		return app.UserView{}, false
	}
	user, err := s.app.UserForSession(cookie.Value)
	if s.handleErr(w, err) {
		return app.UserView{}, false
	}
	return user, true
}

func (s *Server) optionalUserID(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return ""
	}
	user, err := s.app.UserForSession(cookie.Value)
	if err != nil {
		return ""
	}
	return user.ID
}

type googleOAuthState struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	ReturnTo string `json:"returnTo"`
	IssuedAt int64  `json:"issuedAt"`
}

type googleTokenResponse struct {
	IDToken string `json:"id_token"`
	Error   string `json:"error"`
}

type googleIdentity struct {
	Subject string
	Email   string
	Name    string
}

type googleIDTokenHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

type googleIDTokenClaims struct {
	Issuer        string `json:"iss"`
	Audience      string `json:"aud"`
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified any    `json:"email_verified"`
	Name          string `json:"name"`
	Nonce         string `json:"nonce"`
	ExpiresAt     int64  `json:"exp"`
	NotBefore     int64  `json:"nbf"`
}

type googleJWKS struct {
	Keys []googleJWK `json:"keys"`
}

type googleJWK struct {
	KeyType   string `json:"kty"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
}

type stripeWebhookPayload struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object map[string]any `json:"object"`
	} `json:"data"`
}

type epusdtWebhookPayload struct {
	PID          string         `json:"pid"`
	OrderID      string         `json:"order_id"`
	TradeID      string         `json:"trade_id"`
	TxID         string         `json:"txid"`
	Status       string         `json:"status"`
	Signature    string         `json:"signature"`
	Raw          map[string]any `json:"-"`
	Amount       any            `json:"amount,omitempty"`
	ActualAmount any            `json:"actual_amount,omitempty"`
}

func (s *Server) googleOAuthConfigured() bool {
	return strings.TrimSpace(s.cfg.GoogleOAuth.ClientID) != "" &&
		strings.TrimSpace(s.cfg.GoogleOAuth.ClientSecret) != "" &&
		strings.TrimSpace(s.cfg.GoogleOAuth.RedirectURL) != ""
}

func (s *Server) verifiedBillingWebhookInput(provider string, raw []byte, header http.Header) (app.BillingWebhookInput, error) {
	switch provider {
	case "stripe":
		return s.verifiedStripeWebhookInput(raw, header.Get("Stripe-Signature"))
	case "epusdt":
		return s.verifiedEpusdtWebhookInput(raw)
	default:
		return app.BillingWebhookInput{}, app.E(http.StatusBadRequest, "invalid_provider", "provider must be stripe or epusdt")
	}
}

func (s *Server) verifiedStripeWebhookInput(raw []byte, signatureHeader string) (app.BillingWebhookInput, error) {
	if strings.TrimSpace(s.cfg.Stripe.WebhookSecret) == "" {
		return app.BillingWebhookInput{}, app.E(http.StatusServiceUnavailable, "webhook_not_configured", "Stripe webhook secret is not configured")
	}
	if !validStripeSignature(raw, signatureHeader, s.cfg.Stripe.WebhookSecret, time.Now().UTC()) {
		return app.BillingWebhookInput{}, app.E(http.StatusBadRequest, "invalid_webhook_signature", "Stripe webhook signature is invalid")
	}
	var payload stripeWebhookPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return app.BillingWebhookInput{}, app.E(http.StatusBadRequest, "invalid_json", "request body is invalid")
	}
	orderID := stringFromObject(payload.Data.Object, "client_reference_id")
	if orderID == "" {
		orderID = stringFromObject(payload.Data.Object, "metadata.orderId")
	}
	txID := stringFromObject(payload.Data.Object, "payment_intent")
	if txID == "" {
		txID = stringFromObject(payload.Data.Object, "id")
	}
	return app.BillingWebhookInput{
		Provider:       "stripe",
		EventType:      strings.TrimSpace(payload.Type),
		OrderID:        orderID,
		TxID:           txID,
		IdempotencyKey: firstNonEmpty(payload.ID, stringFromObject(payload.Data.Object, "id")),
		Metadata: map[string]any{
			"stripeObject": payload.Data.Object,
		},
	}, nil
}

func (s *Server) verifiedEpusdtWebhookInput(raw []byte) (app.BillingWebhookInput, error) {
	if strings.TrimSpace(s.cfg.Epusdt.SecretKey) == "" {
		return app.BillingWebhookInput{}, app.E(http.StatusServiceUnavailable, "webhook_not_configured", "Epusdt webhook secret is not configured")
	}
	var payload epusdtWebhookPayload
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload.Raw); err != nil {
		return app.BillingWebhookInput{}, app.E(http.StatusBadRequest, "invalid_json", "request body is invalid")
	}
	payload.PID = stringFromObject(payload.Raw, "pid")
	payload.OrderID = stringFromObject(payload.Raw, "order_id")
	payload.TradeID = stringFromObject(payload.Raw, "trade_id")
	payload.TxID = stringFromObject(payload.Raw, "txid")
	payload.Status = stringFromObject(payload.Raw, "status")
	payload.Signature = stringFromObject(payload.Raw, "signature")
	payload.Amount = payload.Raw["amount"]
	payload.ActualAmount = payload.Raw["actual_amount"]

	if strings.TrimSpace(s.cfg.Epusdt.PID) != "" && payload.PID != strings.TrimSpace(s.cfg.Epusdt.PID) {
		return app.BillingWebhookInput{}, app.E(http.StatusBadRequest, "invalid_webhook", "Epusdt merchant id is invalid")
	}
	if !validEpusdtSignature(payload.Raw, s.cfg.Epusdt.SecretKey) {
		return app.BillingWebhookInput{}, app.E(http.StatusBadRequest, "invalid_webhook_signature", "Epusdt webhook signature is invalid")
	}
	return app.BillingWebhookInput{
		Provider:       "epusdt",
		EventType:      epusdtEventType(payload.Status),
		OrderID:        payload.OrderID,
		TxID:           firstNonEmpty(payload.TxID, payload.TradeID),
		IdempotencyKey: firstNonEmpty(payload.TradeID, payload.TxID, payload.OrderID),
		Metadata: map[string]any{
			"amount":       payload.Amount,
			"actualAmount": payload.ActualAmount,
			"raw":          payload.Raw,
		},
	}, nil
}

func validStripeSignature(raw []byte, signatureHeader string, secret string, now time.Time) bool {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return false
	}
	values := map[string][]string{}
	for _, part := range strings.Split(signatureHeader, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		values[key] = append(values[key], value)
	}
	timestamp := ""
	if ts := values["t"]; len(ts) > 0 {
		timestamp = ts[0]
	}
	if timestamp == "" || len(values["v1"]) == 0 {
		return false
	}
	parsed, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	signedAt := time.Unix(parsed, 0)
	if now.Sub(signedAt) > 5*time.Minute || signedAt.Sub(now) > 5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(raw)
	want := hex.EncodeToString(mac.Sum(nil))
	for _, candidate := range values["v1"] {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(want)) == 1 {
			return true
		}
	}
	return false
}

func validEpusdtSignature(values map[string]any, secret string) bool {
	signature := stringFromObject(values, "signature")
	if signature == "" || strings.TrimSpace(secret) == "" {
		return false
	}
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if key == "signature" || stringFromAny(value) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+stringFromAny(values[key]))
	}
	base := strings.Join(parts, "&") + strings.TrimSpace(secret)
	sum := md5.Sum([]byte(base))
	want := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(signature)), []byte(want)) == 1
}

func epusdtEventType(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded", "paid", "completed", "1":
		return "epusdt.payment.succeeded"
	case "expired", "timeout":
		return "epusdt.payment.expired"
	case "canceled", "cancelled":
		return "epusdt.payment.canceled"
	case "failed":
		return "epusdt.payment.failed"
	default:
		normalized := strings.ToLower(strings.TrimSpace(status))
		if normalized == "" {
			return ""
		}
		return "epusdt." + normalized
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringFromObject(object map[string]any, key string) string {
	if object == nil {
		return ""
	}
	if strings.Contains(key, ".") {
		current := any(object)
		for _, part := range strings.Split(key, ".") {
			mapped, ok := current.(map[string]any)
			if !ok {
				return ""
			}
			current = mapped[part]
		}
		return stringFromAny(current)
	}
	return stringFromAny(object[key])
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func randomURLToken(bytesN int) (string, error) {
	buf := make([]byte, bytesN)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *Server) signGoogleOAuthState(state googleOAuthState) (string, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("marshal oauth state: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(s.cfg.GoogleOAuth.ClientSecret))
	_, _ = mac.Write([]byte(encodedPayload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + signature, nil
}

func (s *Server) googleOAuthStateFromRequest(r *http.Request) (googleOAuthState, error) {
	cookie, err := r.Cookie(googleOAuthStateCookieName)
	if err != nil || cookie.Value == "" {
		return googleOAuthState{}, fmt.Errorf("missing oauth state cookie")
	}
	payload, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok || payload == "" || signature == "" {
		return googleOAuthState{}, fmt.Errorf("invalid oauth state cookie")
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.GoogleOAuth.ClientSecret))
	_, _ = mac.Write([]byte(payload))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(signature), []byte(want)) != 1 {
		return googleOAuthState{}, fmt.Errorf("invalid oauth state signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return googleOAuthState{}, fmt.Errorf("decode oauth state: %w", err)
	}
	var state googleOAuthState
	if err := json.Unmarshal(raw, &state); err != nil {
		return googleOAuthState{}, fmt.Errorf("unmarshal oauth state: %w", err)
	}
	if state.State == "" || state.Nonce == "" || state.ReturnTo == "" {
		return googleOAuthState{}, fmt.Errorf("incomplete oauth state")
	}
	if time.Since(time.Unix(state.IssuedAt, 0)) > 10*time.Minute {
		return googleOAuthState{}, fmt.Errorf("expired oauth state")
	}
	return state, nil
}

func sanitizeOAuthReturnTo(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "/"
	}
	if strings.HasPrefix(value, "//") {
		return "/"
	}
	if parsed, err := url.Parse(value); err != nil || parsed.IsAbs() || !strings.HasPrefix(parsed.Path, "/") {
		return "/"
	}
	return value
}

func (s *Server) setGoogleOAuthStateCookie(w http.ResponseWriter, r *http.Request, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     googleOAuthStateCookieName,
		Value:    value,
		Path:     "/api/v1/auth/google",
		Expires:  time.Now().UTC().Add(ttl),
		HttpOnly: true,
		Secure:   s.secureSessionCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) newCSRFToken() (string, string, error) {
	token, err := randomURLToken(32)
	if err != nil {
		return "", "", err
	}
	return token, s.signCSRFToken(token), nil
}

func (s *Server) signCSRFToken(token string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.CSRFSecret))
	_, _ = mac.Write([]byte(token))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return token + "." + signature
}

func (s *Server) validCSRF(r *http.Request) bool {
	headerToken := strings.TrimSpace(r.Header.Get(csrfHeaderName))
	if headerToken == "" {
		return false
	}
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	token, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok || token == "" || signature == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(headerToken), []byte(token)) != 1 {
		return false
	}
	want := s.signCSRFToken(token)
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(want)) == 1
}

func (s *Server) setCSRFCookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    value,
		Path:     "/",
		Expires:  time.Now().UTC().Add(12 * time.Hour),
		HttpOnly: true,
		Secure:   s.secureSessionCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearGoogleOAuthStateCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     googleOAuthStateCookieName,
		Value:    "",
		Path:     "/api/v1/auth/google",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureSessionCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) redirectGoogleOAuthError(w http.ResponseWriter, r *http.Request, code string) {
	s.clearGoogleOAuthStateCookie(w, r)
	http.Redirect(w, r, "/?authError="+url.QueryEscape(code), http.StatusSeeOther)
}

func exchangeGoogleOAuthCode(ctx context.Context, cfg config.GoogleOAuthConfig, code string) (string, error) {
	body := url.Values{}
	body.Set("client_id", cfg.ClientID)
	body.Set("client_secret", cfg.ClientSecret)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", cfg.RedirectURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleOAuthTokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return "", fmt.Errorf("create google token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	res, err := googleOAuthHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange google code: %w", err)
	}
	defer res.Body.Close()

	var token googleTokenResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&token); err != nil {
		return "", fmt.Errorf("decode google token response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || token.Error != "" || token.IDToken == "" {
		return "", fmt.Errorf("google token exchange failed")
	}
	return token.IDToken, nil
}

func verifyGoogleIDToken(ctx context.Context, clientID string, nonce string, idToken string) (googleIdentity, error) {
	header, claims, signingInput, signature, err := parseGoogleIDToken(idToken)
	if err != nil {
		return googleIdentity{}, err
	}
	if header.Algorithm != "RS256" || header.KeyID == "" {
		return googleIdentity{}, fmt.Errorf("google id token uses unsupported signing header")
	}
	key, err := fetchGoogleJWKSKey(ctx, header.KeyID)
	if err != nil {
		return googleIdentity{}, err
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return googleIdentity{}, fmt.Errorf("google id token signature mismatch")
	}
	now := time.Now().UTC().Unix()
	if claims.ExpiresAt <= now || (claims.NotBefore != 0 && claims.NotBefore > now) {
		return googleIdentity{}, fmt.Errorf("google id token is outside its valid time window")
	}
	if claims.Issuer != "accounts.google.com" && claims.Issuer != "https://accounts.google.com" {
		return googleIdentity{}, fmt.Errorf("google id token issuer mismatch")
	}
	if claims.Audience != clientID || claims.Subject == "" || claims.Email == "" {
		return googleIdentity{}, fmt.Errorf("google id token claims mismatch")
	}
	if claims.Nonce != nonce {
		return googleIdentity{}, fmt.Errorf("google nonce mismatch")
	}
	if !googleEmailVerified(claims.EmailVerified) {
		return googleIdentity{}, fmt.Errorf("google email is not verified")
	}
	return googleIdentity{Subject: claims.Subject, Email: claims.Email, Name: claims.Name}, nil
}

func parseGoogleIDToken(token string) (googleIDTokenHeader, googleIDTokenClaims, string, []byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return googleIDTokenHeader{}, googleIDTokenClaims{}, "", nil, fmt.Errorf("invalid google id token")
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return googleIDTokenHeader{}, googleIDTokenClaims{}, "", nil, fmt.Errorf("decode google id token header: %w", err)
	}
	var header googleIDTokenHeader
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return googleIDTokenHeader{}, googleIDTokenClaims{}, "", nil, fmt.Errorf("unmarshal google id token header: %w", err)
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return googleIDTokenHeader{}, googleIDTokenClaims{}, "", nil, fmt.Errorf("decode google id token claims: %w", err)
	}
	var claims googleIDTokenClaims
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		return googleIDTokenHeader{}, googleIDTokenClaims{}, "", nil, fmt.Errorf("unmarshal google id token claims: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return googleIDTokenHeader{}, googleIDTokenClaims{}, "", nil, fmt.Errorf("decode google id token signature: %w", err)
	}
	return header, claims, parts[0] + "." + parts[1], signature, nil
}

func fetchGoogleJWKSKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleOAuthJWKSURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create google jwks request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	res, err := googleOAuthHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch google jwks: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("google jwks fetch failed")
	}
	var jwks googleJWKS
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("decode google jwks: %w", err)
	}
	for _, key := range jwks.Keys {
		if key.KeyID != kid {
			continue
		}
		publicKey, err := googleJWKPublicKey(key)
		if err != nil {
			return nil, err
		}
		return publicKey, nil
	}
	return nil, fmt.Errorf("google jwks key not found")
}

func googleJWKPublicKey(key googleJWK) (*rsa.PublicKey, error) {
	if key.KeyType != "RSA" || (key.Use != "" && key.Use != "sig") || (key.Algorithm != "" && key.Algorithm != "RS256") {
		return nil, fmt.Errorf("google jwks key is not an RSA signing key")
	}
	modulus, err := base64.RawURLEncoding.DecodeString(key.Modulus)
	if err != nil {
		return nil, fmt.Errorf("decode google jwks modulus: %w", err)
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(key.Exponent)
	if err != nil {
		return nil, fmt.Errorf("decode google jwks exponent: %w", err)
	}
	exponent := new(big.Int).SetBytes(exponentBytes).Int64()
	if exponent <= 1 || exponent > int64(^uint(0)>>1) {
		return nil, fmt.Errorf("google jwks exponent is invalid")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: int(exponent)}, nil
}

func googleEmailVerified(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "true"
	default:
		return false
	}
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, value string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   s.secureSessionCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureSessionCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) secureSessionCookie(r *http.Request) bool {
	if s.cfg.AppEnv == "development" {
		return false
	}
	return requestIsHTTPS(r)
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if proto := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])); proto == "https" {
		return true
	}
	for _, entry := range strings.Split(r.Header.Get("Forwarded"), ",") {
		for _, part := range strings.Split(entry, ";") {
			key, value, ok := strings.Cut(part, "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "proto") {
				continue
			}
			if strings.EqualFold(strings.Trim(strings.TrimSpace(value), `"`), "https") {
				return true
			}
		}
	}
	return false
}

func (s *Server) handleErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status, payload := app.ErrorResponse(err)
	writeJSON(w, status, payload)
	return true
}

func (s *Server) writeDownload(w http.ResponseWriter, attachment app.AttachmentView, content []byte) {
	contentType := attachment.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeHeaderValue(attachment.FileName)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func contentType(header *multipart.FileHeader) string {
	if values := header.Header.Values("Content-Type"); len(values) > 0 {
		return values[0]
	}
	return "application/octet-stream"
}

func sanitizeHeaderValue(value string) string {
	value = strings.ReplaceAll(value, `"`, "")
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	return value
}
