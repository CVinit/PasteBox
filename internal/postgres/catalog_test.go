package postgres

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
	"pastebox/internal/plans"
)

func TestCatalogStoreReadsMigratedPlanCatalog(t *testing.T) {
	databaseURL := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL to run PostgreSQL catalog integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := ApplyMigrations(ctx, databaseURL); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	catalog, err := NewCatalogStore(pool).Catalog(ctx)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}

	want := plans.DefaultCatalog()
	if !reflect.DeepEqual(catalog.Plans, want.Plans) {
		t.Fatalf("plans mismatch:\n got: %#v\nwant: %#v", catalog.Plans, want.Plans)
	}
	if !reflect.DeepEqual(catalog.Prices, want.Prices) {
		t.Fatalf("prices mismatch:\n got: %#v\nwant: %#v", catalog.Prices, want.Prices)
	}
}

func TestCatalogEntrySavePreservesOtherRowsAndRollsBackWithAudit(t *testing.T) {
	dsn := os.Getenv("PASTEBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set PASTEBOX_TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := ApplyMigrations(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	prefix := fmt.Sprintf("catalog_entry_%d_", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, table := range []string{"prices", "plans", "audit_logs"} {
			if _, err := pool.Exec(cleanupCtx, "DELETE FROM "+table+" WHERE id LIKE $1", prefix+"%"); err != nil {
				t.Errorf("cleanup %s: %v", table, err)
			}
		}
	})
	store := NewCatalogStore(pool)
	original, err := store.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan := original.Plans[0]
	plan.ID = prefix + "plan"
	price := original.Prices[0]
	price.ID, price.PlanID = prefix+"price", plan.ID
	audit := app.AuditLog{ID: prefix + "audit", Action: "catalog.test"}
	if err := store.SaveCatalogEntries(ctx, plans.Catalog{Plans: []plans.Plan{plan}, Prices: []plans.Price{price}}, audit); err != nil {
		t.Fatal(err)
	}
	before, err := store.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Plans) != len(original.Plans)+1 || len(before.Prices) != len(original.Prices)+1 {
		t.Fatal("partial save removed existing catalog rows")
	}
	plan.Name = "must roll back"
	price.AmountCents++
	// Reusing the audit ID fails after the upserts, so both must roll back.
	if err := store.SaveCatalogEntries(ctx, plans.Catalog{Plans: []plans.Plan{plan}, Prices: []plans.Price{price}}, audit); err == nil {
		t.Fatal("expected duplicate audit failure")
	}
	after, err := store.Catalog(ctx)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("audit failure did not roll back entries: %v", err)
	}
	audit.ID = prefix + "price_audit"
	if err := store.SaveCatalogEntries(ctx, plans.Catalog{Prices: []plans.Price{price}}, audit); err != nil {
		t.Fatal(err)
	}
	after, err = store.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range before.Prices {
		if before.Prices[i].ID == price.ID {
			before.Prices[i] = price
		}
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("price-only save changed unrelated catalog rows")
	}
}
