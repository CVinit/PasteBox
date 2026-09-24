package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"pastebox/internal/app"
	"pastebox/internal/config"
	"testing"
)

func reviewHTTPClients(t *testing.T) (*app.Service, *httpTestClient, *httpTestClient) {
	t.Helper()
	cfg := config.FromEnv()
	cfg.RateLimit.Enabled = false
	cfg.DevAuthTokens = true
	cfg.BootstrapAdminEmail = ""
	cfg.BootstrapAdminPassword = ""
	service := app.New(cfg)
	if _, err := service.SeedAdmin("review-admin@example.com", "password123"); err != nil {
		t.Fatal(err)
	}
	handler := NewWithService(cfg, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)), service)
	user := newHTTPTestClient(t, handler)
	registerHTTPUser(t, user, "review-user@example.com", "Review")
	admin := newHTTPTestClient(t, handler)
	assertStatus(t, admin.json("POST", "/api/v1/auth/login", `{"email":"review-admin@example.com","password":"password123"}`), 200)
	return service, user, admin
}

func TestHTTPAccountAndSessionLifecycle(t *testing.T) {
	_, user, _ := reviewHTTPClients(t)
	res := user.json("PATCH", "/api/v1/me", `{"displayName":"Updated","language":"zh-CN"}`)
	assertStatus(t, res, 200)
	var profile app.UserView
	decodeResponse(t, res, &profile)
	if profile.DisplayName != "Updated" || profile.Language != "zh-CN" {
		t.Fatalf("profile not saved: %#v", profile)
	}
	assertStatus(t, user.json("GET", "/api/v1/me/export", ""), 200)
	reset := user.json("POST", "/api/v1/auth/password-reset/start", `{"email":"review-user@example.com"}`)
	assertStatus(t, reset, 200)
	var token struct {
		DevToken string `json:"devToken"`
	}
	decodeResponse(t, reset, &token)
	if token.DevToken == "" {
		t.Fatal("missing reset test token")
	}
	assertStatus(t, user.json("POST", "/api/v1/auth/password-reset/finish", `{"token":"`+token.DevToken+`","password":"updated-password"}`), 200)
	assertStatus(t, user.json("GET", "/api/v1/me", ""), 401)
	assertStatus(t, user.json("POST", "/api/v1/auth/password-reset/finish", `{"token":"`+token.DevToken+`","password":"another-password"}`), 401)
	assertStatus(t, user.json("POST", "/api/v1/auth/login", `{"email":"review-user@example.com","password":"updated-password"}`), 200)
	assertStatus(t, user.json("POST", "/api/v1/me/delete-request", `{}`), 200)
	assertStatus(t, user.json("POST", "/api/v1/me/delete-cancel", `{}`), 200)
	assertStatus(t, user.json("POST", "/api/v1/auth/logout-all", `{}`), 200)
	assertStatus(t, user.json("GET", "/api/v1/me", ""), 401)
	assertStatus(t, user.json("POST", "/api/v1/auth/login", `{"email":"review-user@example.com","password":"updated-password"}`), 200)
	assertStatus(t, user.json("POST", "/api/v1/auth/logout", `{}`), 200)
	assertStatus(t, user.json("GET", "/api/v1/me", ""), 401)
}

func TestHTTPContentLifecycleAndAdminPagination(t *testing.T) {
	_, user, admin := reviewHTTPClients(t)
	created := user.json("POST", "/api/v1/pastes", `{"title":"Lifecycle","text":"first","expiresInSeconds":600}`)
	assertStatus(t, created, 201)
	var paste app.PasteView
	decodeResponse(t, created, &paste)
	path := "/api/v1/pastes/" + paste.ID
	assertStatus(t, user.json("GET", path, ""), 200)
	changed := user.json("PATCH", path, `{"title":"Changed","text":"second","favorite":true}`)
	assertStatus(t, changed, 200)
	decodeResponse(t, changed, &paste)
	if paste.Title != "Changed" || paste.Text != "second" || !paste.Favorite {
		t.Fatalf("update lost: %#v", paste)
	}
	assertStatus(t, user.json("POST", path+"/extend", `{"expiresInSeconds":1200}`), 200)
	shareRes := user.json("POST", path+"/shares", `{"password":"test-password"}`)
	assertStatus(t, shareRes, 201)
	var share app.ShareView
	decodeResponse(t, shareRes, &share)
	for _, route := range []string{"users", "pastes", "attachments", "shares", "orders", "webhook-events", "runtime-config", "managed-config", "redemption-batches"} {
		t.Run(route, func(t *testing.T) {
			assertStatus(t, user.json("GET", "/api/v1/admin/"+route, ""), 403)
			res := admin.json("GET", "/api/v1/admin/"+route+"?limit=1&offset=0", "")
			assertStatus(t, res, 200)
			if !json.Valid(res.Body.Bytes()) {
				t.Fatalf("invalid admin JSON: %s", res.Body)
			}
		})
	}
	var page struct {
		Pastes []app.PasteView `json:"pastes"`
	}
	decodeResponse(t, admin.json("GET", "/api/v1/admin/pastes?limit=1", ""), &page)
	if len(page.Pastes) != 1 || page.Pastes[0].ID != paste.ID {
		t.Fatalf("unexpected page: %#v", page)
	}
	assertStatus(t, admin.json("POST", "/api/v1/admin/shares/"+share.ID+"/revoke", `{}`), 200)
	assertStatus(t, user.json("DELETE", "/api/v1/shares/"+share.ID, ""), 200)
	assertStatus(t, admin.json("POST", "/api/v1/admin/pastes/"+paste.ID+"/takedown", `{}`), 200)
	assertStatus(t, user.json("GET", path, ""), 410)
	second := user.json("POST", "/api/v1/pastes", `{"text":"delete me"}`)
	assertStatus(t, second, 201)
	decodeResponse(t, second, &paste)
	assertStatus(t, user.json("DELETE", "/api/v1/pastes/"+paste.ID, ""), 200)
	assertStatus(t, admin.json("POST", "/api/v1/admin/cleanup/run", `{}`), 200)
	assertStatus(t, user.json("GET", "/api/v1/pastes/"+paste.ID, ""), 410)
	assertStatus(t, user.json("GET", "/api/v1/billing/prices", ""), 200)
}

func TestHTTPAdminRedemptionAndFreezeLifecycle(t *testing.T) {
	_, user, admin := reviewHTTPClients(t)
	var account app.UserView
	decodeResponse(t, user.json("GET", "/api/v1/me", ""), &account)
	path := "/api/v1/admin/users/" + account.ID + "/freeze"
	assertStatus(t, admin.json("PATCH", path, `{"frozen":true}`), 200)
	assertStatus(t, user.json("GET", "/api/v1/me", ""), 403)
	assertStatus(t, admin.json("PATCH", path, `{"frozen":false}`), 200)
	assertStatus(t, user.json("GET", "/api/v1/me", ""), 200)
	result := admin.json("POST", "/api/v1/admin/redemption-batches", `{"planId":"plus","durationDays":30,"quantity":1,"maxTotalRedemptions":1,"maxRedemptionsPerUser":1}`)
	assertStatus(t, result, 201)
	var batch app.RedemptionBatchView
	decodeResponse(t, result, &batch)
	assertStatus(t, admin.json("PATCH", "/api/v1/admin/redemption-batches/"+batch.ID, `{"disabled":true,"note":"paused"}`), 200)
	assertStatus(t, user.json("POST", "/api/v1/redemptions/redeem", `{"code":"`+batch.Codes[0].Code+`"}`), 403)
	assertStatus(t, admin.json("PATCH", "/api/v1/admin/redemption-batches/"+batch.ID, `{"disabled":false,"note":"enabled"}`), 200)
	assertStatus(t, user.json("POST", "/api/v1/redemptions/redeem", `{"code":"`+batch.Codes[0].Code+`"}`), 200)
	var after app.UserView
	decodeResponse(t, user.json("GET", "/api/v1/me", ""), &after)
	if after.PlanID != "plus" {
		t.Fatalf("plan not redeemed: %#v", after)
	}
}

func TestHTTPChangedRoutesRequireSessionAndStrictJSON(t *testing.T) {
	_, user, admin := reviewHTTPClients(t)
	anonymous := newHTTPTestClient(t, user.handler)
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/v1/me/export"}, {"PATCH", "/api/v1/me"}, {"POST", "/api/v1/me/delete-now"},
		{"GET", "/api/v1/pastes/missing"}, {"PATCH", "/api/v1/pastes/missing"}, {"DELETE", "/api/v1/pastes/missing"}, {"POST", "/api/v1/pastes/missing/extend"}, {"DELETE", "/api/v1/shares/missing"},
		{"GET", "/api/v1/admin/users"}, {"GET", "/api/v1/admin/pastes"}, {"GET", "/api/v1/admin/attachments"}, {"GET", "/api/v1/admin/shares"}, {"GET", "/api/v1/admin/runtime-config"}, {"GET", "/api/v1/admin/managed-config"}, {"GET", "/api/v1/admin/orders"}, {"GET", "/api/v1/admin/webhook-events"},
	} {
		assertStatus(t, anonymous.json(route.method, route.path, `{}`), http.StatusUnauthorized)
	}
	for _, route := range []struct{ method, path string }{
		{"PATCH", "/api/v1/me"}, {"PATCH", "/api/v1/pastes/missing"}, {"POST", "/api/v1/pastes/missing/extend"}, {"PUT", "/api/v1/admin/managed-config"}, {"PATCH", "/api/v1/admin/runtime-config"}, {"PATCH", "/api/v1/admin/users/missing/freeze"}, {"POST", "/api/v1/admin/redemption-batches"}, {"PATCH", "/api/v1/admin/redemption-batches/missing"},
	} {
		assertStatus(t, admin.json(route.method, route.path, `{} {}`), http.StatusBadRequest)
	}
}
