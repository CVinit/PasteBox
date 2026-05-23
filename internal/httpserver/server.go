package httpserver

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"pastebox/internal/app"
	"pastebox/internal/config"
	"pastebox/internal/plans"
)

const sessionCookieName = "pastebox_session"

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

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.apiHealth)
		r.Get("/plans", s.planCatalog)

		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", s.register)
			r.Post("/login", s.login)
			r.Post("/logout", s.logout)
			r.Post("/logout-all", s.logoutAll)
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

func (s *Server) apiHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"app":    s.cfg.AppName,
		"env":    s.cfg.AppEnv,
		"status": "ok",
	})
}

func (s *Server) planCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, plans.DefaultCatalog())
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
	s.setSessionCookie(w, result.SessionID, result.ExpiresAt)
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
	s.setSessionCookie(w, result.SessionID, result.ExpiresAt)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) googleOAuth(w http.ResponseWriter, r *http.Request) {
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
	s.setSessionCookie(w, result.SessionID, result.ExpiresAt)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.app.Logout(cookie.Value)
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	s.app.LogoutAll(user.ID)
	s.clearSessionCookie(w)
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
	s.setSessionCookie(w, result.SessionID, result.ExpiresAt)
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
	s.clearSessionCookie(w)
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

func (s *Server) setSessionCookie(w http.ResponseWriter, value string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   s.cfg.AppEnv != "development",
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.AppEnv != "development",
		SameSite: http.SameSiteLaxMode,
	})
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
