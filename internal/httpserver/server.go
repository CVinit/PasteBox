package httpserver

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	cfg    config.Config
	logger *slog.Logger
	app    *app.Service
}

func New(cfg config.Config, logger *slog.Logger) http.Handler {
	return NewWithService(cfg, logger, app.New(cfg))
}

func NewWithService(cfg config.Config, logger *slog.Logger, service *app.Service) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if service == nil {
		service = app.New(cfg)
	}

	server := &Server{
		cfg:    cfg,
		logger: logger,
		app:    service,
	}
	return server.routes()
}

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(s.logRequests)

	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)

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

func (s *Server) readyz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ready",
	})
}

func (s *Server) apiHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"app":    s.cfg.AppName,
		"env":    s.cfg.AppEnv,
		"status": "ok",
	})
}

func (s *Server) apiReady(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"app":    s.cfg.AppName,
		"env":    s.cfg.AppEnv,
		"status": "ready",
	})
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
	var req struct {
		EventType      string         `json:"eventType"`
		OrderID        string         `json:"orderId"`
		TxID           string         `json:"txId"`
		IdempotencyKey string         `json:"idempotencyKey"`
		Metadata       map[string]any `json:"metadata"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	event, order, err := s.app.ProcessBillingWebhook(app.BillingWebhookInput{
		Provider:       chi.URLParam(r, "provider"),
		EventType:      req.EventType,
		OrderID:        req.OrderID,
		TxID:           req.TxID,
		IdempotencyKey: req.IdempotencyKey,
		Metadata:       req.Metadata,
	})
	if s.handleErr(w, err) {
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

		s.logger.Info(
			"http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
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

func (s *Server) googleOAuthConfigured() bool {
	return strings.TrimSpace(s.cfg.GoogleOAuth.ClientID) != "" &&
		strings.TrimSpace(s.cfg.GoogleOAuth.ClientSecret) != "" &&
		strings.TrimSpace(s.cfg.GoogleOAuth.RedirectURL) != ""
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
