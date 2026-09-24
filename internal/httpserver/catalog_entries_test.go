package httpserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"testing"

	"pastebox/internal/app"
	"pastebox/internal/config"
	"pastebox/internal/plans"
)

func TestCatalogEntryHTTPContract(t *testing.T) {
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword = "", ""
	cfg.DevAuthTokens = true
	svc := app.New(cfg)
	if _, err := svc.SeedAdmin("catalog-admin@example.com", "password123"); err != nil {
		t.Fatal(err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), svc)
	admin := newHTTPTestClient(t, handler)
	path := "/api/v1/admin/catalog/entries"
	assertStatus(t, admin.json(http.MethodPatch, path, `{}`), http.StatusUnauthorized)
	registerHTTPUser(t, admin, "catalog-user@example.com", "Catalog user")
	assertStatus(t, admin.json(http.MethodPatch, path, `{}`), http.StatusForbidden)
	assertStatus(t, admin.json(http.MethodPost, "/api/v1/auth/login", `{"email":"catalog-admin@example.com","password":"password123"}`), http.StatusOK)
	before := svc.PlanCatalog()
	price := before.Prices[0]
	price.AmountCents++
	body, err := json.Marshal(app.AdminPlanUpdate{Prices: []plans.Price{price}})
	if err != nil {
		t.Fatal(err)
	}
	response := admin.json(http.MethodPatch, path, string(body))
	assertStatus(t, response, http.StatusOK)
	var updated plans.Catalog
	decodeResponse(t, response, &updated)
	if updated.Prices[0].AmountCents != price.AmountCents || !reflect.DeepEqual(updated.Prices[1:], before.Prices[1:]) || !reflect.DeepEqual(updated.Plans, before.Plans) {
		t.Fatalf("partial HTTP save changed other entries: %#v", updated)
	}
	assertStatus(t, admin.json(http.MethodPatch, path, `{"prices":[],"unexpected":true}`), http.StatusBadRequest)
	assertStatus(t, admin.json(http.MethodPatch, path, `{"prices":[{"id":"missing"}]}`), http.StatusNotFound)
}
