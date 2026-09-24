package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"pastebox/internal/plans"
)

type entryCatalogStore struct {
	memoryCatalogStore
	entries plans.Catalog
	err     error
}

func (s *entryCatalogStore) SaveCatalogEntries(_ context.Context, entries plans.Catalog, _ AuditLog) error {
	s.entries = cloneCatalog(entries)
	return s.err
}

func TestCatalogEntryUpdatePreservesUnrelatedEntriesAndPublishesAfterCommit(t *testing.T) {
	now := time.Now()
	store := &entryCatalogStore{memoryCatalogStore: memoryCatalogStore{catalog: plans.DefaultCatalog()}}
	svc := newTestServiceWithStorage(t, &now, Stores{Catalog: store})
	admin := seedAdminTestUser(t, svc, "entry-admin@example.com")
	before := svc.PlanCatalog()
	plan := before.Plans[0]
	plan.Name = "Updated free plan"
	updated, err := svc.AdminUpdateCatalogEntries(context.Background(), admin.ID, AdminPlanUpdate{Plans: []plans.Plan{plan}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Plans[0].Name != plan.Name || !reflect.DeepEqual(updated.Plans[1:], before.Plans[1:]) || !reflect.DeepEqual(updated.Prices, before.Prices) {
		t.Fatalf("partial update changed unrelated entries: %#v", updated)
	}
	if len(store.entries.Plans) != 1 || len(store.entries.Prices) != 0 {
		t.Fatalf("writer received unrelated entries: %#v", store.entries)
	}
	store.err = errors.New("audit write failed")
	price := before.Prices[0]
	price.AmountCents++
	if _, err := svc.AdminUpdateCatalogEntries(context.Background(), admin.ID, AdminPlanUpdate{Prices: []plans.Price{price}}); !errors.Is(err, store.err) {
		t.Fatalf("expected transaction error, got %v", err)
	}
	if !reflect.DeepEqual(svc.PlanCatalog(), updated) {
		t.Fatal("failed transaction published catalog changes")
	}
	if len(store.entries.Prices) != 1 || len(store.entries.Plans) != 0 {
		t.Fatalf("price writer received unrelated entries: %#v", store.entries)
	}
}

func TestCatalogEntryValidationLeavesCatalogUntouched(t *testing.T) {
	now := time.Now()
	svc := newTestService(t, &now)
	admin := seedAdminTestUser(t, svc, "entry-validation@example.com")
	before := svc.PlanCatalog()
	plan, price := before.Plans[0], before.Prices[0]
	badPlan := plan
	badPlan.ActivePasteLimit = -1
	badPrice := price
	badPrice.PlanID = "missing"
	cases := []struct {
		name   string
		update AdminPlanUpdate
		code   string
	}{
		{"empty", AdminPlanUpdate{}, "catalog_entries_required"},
		{"unknown plan", AdminPlanUpdate{Plans: []plans.Plan{{ID: "missing"}}}, "plan_not_found"},
		{"unknown price", AdminPlanUpdate{Prices: []plans.Price{{ID: "missing"}}}, "price_not_found"},
		{"duplicate plan", AdminPlanUpdate{Plans: []plans.Plan{plan, plan}}, "duplicate_plan"},
		{"duplicate price", AdminPlanUpdate{Prices: []plans.Price{price, price}}, "duplicate_price"},
		{"invalid quota", AdminPlanUpdate{Plans: []plans.Plan{badPlan}}, "invalid_plan_limits"},
		{"invalid price plan", AdminPlanUpdate{Prices: []plans.Price{badPrice}}, "invalid_price_plan"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.AdminUpdateCatalogEntries(context.Background(), admin.ID, tc.update); !hasAppCode(err, tc.code) {
				t.Fatalf("wanted %s, got %v", tc.code, err)
			}
			if !reflect.DeepEqual(svc.PlanCatalog(), before) {
				t.Fatal("rejected update changed catalog")
			}
		})
	}
}

func TestManagedConfigFieldSelectionPreservesOtherGroupsAndSecrets(t *testing.T) {
	now := time.Now()
	svc := newTestService(t, &now)
	admin := seedAdminTestUser(t, svc, "managed-fields@example.com")
	before, err := svc.AdminManagedConfig(admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected := before.Config
	selected.Turnstile.SiteKey = "fixture-site"
	selected.Site.AppName = "Unsubmitted draft"
	secret := "fixture-secret"
	updated, err := svc.AdminUpdateManagedConfig(admin.ID, ManagedConfigUpdate{
		Fields: []string{"turnstile"}, Config: selected,
		Secrets: ManagedSecretPatch{TurnstileSecretKey: &secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Config.Site.AppName != before.Config.Site.AppName || updated.Config.Turnstile.SiteKey != "fixture-site" || !updated.Secrets.TurnstileSecretKey {
		t.Fatalf("field selection lost data: %#v", updated)
	}
	selected.Site.AppName = "Saved site"
	updated, err = svc.AdminUpdateManagedConfig(admin.ID, ManagedConfigUpdate{Fields: []string{"site"}, Config: selected})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Config.Turnstile.SiteKey != "fixture-site" || svc.EffectiveConfig().Turnstile.SecretKey != secret {
		t.Fatal("unrelated group save cleared Turnstile config")
	}
	if _, err := svc.AdminUpdateManagedConfig(admin.ID, ManagedConfigUpdate{Fields: []string{"site", "unknown"}, Config: before.Config}); !hasAppCode(err, "invalid_managed_config_field") {
		t.Fatalf("unknown field should fail: %v", err)
	}
	current, err := svc.AdminManagedConfig(admin.ID)
	if err != nil || !reflect.DeepEqual(current, updated) {
		t.Fatalf("rejected field selection changed config: %v", err)
	}
}
