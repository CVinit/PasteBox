package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"pastebox/internal/config"
)

func TestPricingVisibilityPersistsAndPreservesMembership(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	auditStore := newMemoryAuditLogStore()
	store := &memoryRuntimeConfigStore{auditStore: auditStore}
	svc := newTestServiceWithStorage(t, &now, Stores{RuntimeConfigs: store, AuditLogs: auditStore})
	admin := seedAdminTestUser(t, svc, "visibility-admin@example.com")
	expires := now.Add(30 * 24 * time.Hour)
	if _, err := svc.AdminSetUserPlanWithContext(context.Background(), admin.ID, admin.ID, "plus", &expires, "test membership", ""); err != nil {
		t.Fatal(err)
	}
	catalog := svc.PlanCatalog()
	quota, err := svc.QuotaWithContext(context.Background(), admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !svc.Prices().PricingVisible {
		t.Fatal("pricing must remain visible by default")
	}
	if _, err := svc.AdminUpdateRuntimeConfig(admin.ID, RuntimeConfigPatch{PricingHidden: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if svc.Prices().PricingVisible || !store.cfg.PricingHidden {
		t.Fatal("pricing hidden state did not reach public data and persistence")
	}
	after, err := svc.QuotaWithContext(context.Background(), admin.ID)
	if err != nil || !reflect.DeepEqual(quota, after) || !reflect.DeepEqual(catalog, svc.PlanCatalog()) {
		t.Fatalf("hiding pricing changed membership or catalog: quota=%#v err=%v", after, err)
	}
	if _, err := svc.AdminUpdateRuntimeConfig(admin.ID, RuntimeConfigPatch{LogLevel: ptr("warn")}); err != nil || svc.Prices().PricingVisible {
		t.Fatalf("unrelated partial update reset visibility: %v", err)
	}
	restarted := newTestServiceWithStorage(t, &now, Stores{RuntimeConfigs: store})
	if restarted.Prices().PricingVisible {
		t.Fatal("pricing hidden state was lost after restart")
	}
	store.atomicErr = errors.New("save failed")
	if _, err := svc.AdminUpdateRuntimeConfig(admin.ID, RuntimeConfigPatch{PricingHidden: ptr(false)}); err == nil || svc.Prices().PricingVisible || !store.cfg.PricingHidden {
		t.Fatal("failed save must retain the previous visibility")
	}
	store.atomicErr = nil
	if _, err := svc.AdminUpdateRuntimeConfig(admin.ID, RuntimeConfigPatch{PricingHidden: ptr(false)}); err != nil || !svc.Prices().PricingVisible || store.cfg.PricingHidden {
		t.Fatalf("explicit false must restore pricing: %v", err)
	}
	var legacy RuntimeConfig
	if err := json.Unmarshal([]byte(`{"id":"default","logLevel":"info"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if normalizeRuntimeConfig(legacy, config.FromEnv()).PricingHidden {
		t.Fatal("old runtime JSON must not hide pricing")
	}
}

func TestAdminUserSearchFiltersBeforePagination(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	admin := seedAdminTestUser(t, svc, "search-admin@example.com")
	for i, user := range []User{
		{ID: "usr_search_old", Email: "alice-old@example.com", DisplayName: "Lookup Name"},
		{ID: "usr_search_new", Email: "alice-new@example.com", DisplayName: "100% Alice"},
		{ID: "usr_unrelated", Email: "other@example.com", DisplayName: "Other"},
	} {
		user.Role, user.PlanID, user.EmailVerified = "user", "free", true
		user.CreatedAt = now.Add(time.Duration(i) * time.Minute)
		svc.usersByID[user.ID] = &user
	}
	for _, tc := range []struct {
		query  string
		offset int
		want   string
	}{
		{" ALICE ", 0, "usr_search_new"},
		{" ALICE ", 1, "usr_search_old"},
		{"Lookup NAME", 0, "usr_search_old"},
		{"usr_search_old", 0, "usr_search_old"},
		{"%", 0, "usr_search_new"},
		{"does-not-exist", 0, ""},
		{"alice", 2, ""},
	} {
		rows, err := svc.AdminUsersWithContext(context.Background(), admin.ID, ListOptions{Query: tc.query, Limit: 1, Offset: tc.offset})
		if err != nil {
			t.Fatal(err)
		}
		if tc.want == "" {
			if len(rows) != 0 {
				t.Fatalf("query %q returned unexpected rows: %#v", tc.query, rows)
			}
		} else if len(rows) != 1 || rows[0].ID != tc.want {
			t.Fatalf("query %q offset %d: got %#v, want %q", tc.query, tc.offset, rows, tc.want)
		}
	}
	if _, err := svc.AdminUsersWithContext(context.Background(), "usr_unrelated", ListOptions{Query: "alice"}); !hasAppStatus(err, http.StatusForbidden) {
		t.Fatalf("search bypassed admin permissions: %v", err)
	}
}

func TestAdminAttachmentListIncludesParentContentState(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	svc := newTestService(t, &now)
	admin := seedAdminTestUser(t, svc, "file-admin@example.com")
	owner := registerTestUser(t, svc, "file-owner@example.com")
	paste := createTestPaste(t, svc, owner.User.ID, PasteInput{Title: "File parent", Text: "file", ExpiresInSeconds: 3600})
	file := addTestAttachment(t, svc, owner.User.ID, paste.ID, "review.txt", []byte("review"))
	for _, status := range []string{"active", "taken_down"} {
		if status == "taken_down" {
			if err := svc.AdminTakedownPaste(admin.ID, paste.ID); err != nil {
				t.Fatal(err)
			}
		}
		rows, err := svc.AdminAttachments(admin.ID, "review.txt")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].ID != file.ID || rows[0].PasteTitle != "File parent" || rows[0].PasteStatus != status || rows[0].Status != file.Status {
			t.Fatalf("parent status %q: unexpected file view %#v", status, rows)
		}
	}
}
