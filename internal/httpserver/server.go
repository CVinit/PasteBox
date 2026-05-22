package httpserver

import (
	"embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"pastebox/internal/config"
	"pastebox/internal/plans"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	cfg    config.Config
	logger *slog.Logger
	router http.Handler
}

func New(cfg config.Config, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	server := &Server{
		cfg:    cfg,
		logger: logger,
	}
	server.router = server.routes()
	return server.router
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

func (s *Server) staticFallback(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}

	http.FileServer(http.FS(staticFS)).ServeHTTP(w, r)
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}
