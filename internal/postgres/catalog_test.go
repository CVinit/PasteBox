package postgres

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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
