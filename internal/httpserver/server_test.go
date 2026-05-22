package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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

type testWriter struct {
	t *testing.T
}

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	return len(p), nil
}
