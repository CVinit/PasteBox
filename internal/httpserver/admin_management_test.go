package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"pastebox/internal/app"
	"pastebox/internal/config"
)

func TestAdminPricingVisibilityAndUserSearchHTTP(t *testing.T) {
	cfg := config.FromEnv()
	cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword = "", ""
	cfg.DevAuthTokens = true
	svc := app.New(cfg)
	if _, err := svc.SeedAdmin("management-admin@example.com", "preview-admin-password"); err != nil {
		t.Fatal(err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), svc)
	anonymous := newHTTPTestClient(t, handler)
	assertStatus(t, anonymous.json(http.MethodPatch, "/api/v1/admin/runtime-config", `{"pricingHidden":true}`), http.StatusUnauthorized)
	user := newHTTPTestClient(t, handler)
	registerHTTPUser(t, user, "searchable@example.com", "Lookup Person")
	assertStatus(t, user.json(http.MethodPatch, "/api/v1/admin/runtime-config", `{"pricingHidden":true}`), http.StatusForbidden)
	assertStatus(t, user.json(http.MethodGet, "/api/v1/admin/users?query=searchable", ""), http.StatusForbidden)
	admin := newHTTPTestClient(t, handler)
	assertStatus(t, admin.json(http.MethodPost, "/api/v1/auth/login", `{"email":"management-admin@example.com","password":"preview-admin-password"}`), http.StatusOK)
	for _, tc := range []struct {
		body    string
		visible bool
	}{
		{`{}`, true}, {`{"pricingHidden":true}`, false}, {`{"logLevel":"warn"}`, false}, {`{"pricingHidden":false}`, true},
	} {
		assertStatus(t, admin.json(http.MethodPatch, "/api/v1/admin/runtime-config", tc.body), http.StatusOK)
		for _, path := range []string{"/api/v1/plans", "/api/v1/billing/prices"} {
			response := anonymous.json(http.MethodGet, path, "")
			assertStatus(t, response, http.StatusOK)
			var catalog struct {
				PricingVisible *bool `json:"pricingVisible"`
				Plans          []any `json:"plans"`
			}
			decodeResponse(t, response, &catalog)
			if catalog.PricingVisible == nil || *catalog.PricingVisible != tc.visible || len(catalog.Plans) == 0 {
				t.Fatalf("public catalog %s after %s: %#v", path, tc.body, catalog)
			}
		}
	}

	create := user.json(http.MethodPost, "/api/v1/pastes", `{"title":"Managed file","text":"review","expiresInSeconds":3600}`)
	assertStatus(t, create, http.StatusCreated)
	var paste app.PasteView
	decodeResponse(t, create, &paste)
	assertStatus(t, user.multipart("/api/v1/pastes/"+paste.ID+"/attachments", "file", "review.txt", []byte("review")), http.StatusCreated)
	assertStatus(t, admin.json(http.MethodPost, "/api/v1/admin/pastes/"+paste.ID+"/takedown", ""), http.StatusOK)
	filesResponse := admin.json(http.MethodGet, "/api/v1/admin/attachments?query=review.txt", "")
	assertStatus(t, filesResponse, http.StatusOK)
	var files struct {
		Attachments []app.AdminAttachmentView `json:"attachments"`
	}
	decodeResponse(t, filesResponse, &files)
	if len(files.Attachments) != 1 || files.Attachments[0].PasteStatus != "taken_down" || files.Attachments[0].PasteTitle != paste.Title {
		t.Fatalf("admin files lost parent content state: %#v", files)
	}

	for _, query := range []string{"SEARCHABLE", "Lookup", "usr_"} {
		response := admin.json(http.MethodGet, "/api/v1/admin/users?limit=1&offset=0&query="+query, "")
		assertStatus(t, response, http.StatusOK)
		var body struct {
			Users []app.UserView `json:"users"`
		}
		decodeResponse(t, response, &body)
		if len(body.Users) != 1 {
			t.Fatalf("query %q did not reach service search: %#v", query, body)
		}
	}
}
