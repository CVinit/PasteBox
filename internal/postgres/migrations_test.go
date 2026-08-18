package postgres

import (
	"fmt"
	"strings"
	"testing"
)

func TestLoadMigrationsIncludesInitialProductionSchema(t *testing.T) {
	migrations, err := LoadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("expected at least one migration")
	}

	first := migrations[0]
	if first.Version != 1 || first.Name != "initial_schema" || first.Filename != "000001_initial_schema.sql" {
		t.Fatalf("unexpected first migration: %#v", first)
	}
	if len(first.Checksum) != 64 {
		t.Fatalf("expected sha256 hex checksum, got %q", first.Checksum)
	}

	requiredTables := []string{
		"schema_migrations",
		"plans",
		"prices",
		"users",
		"sessions",
		"auth_tokens",
		"login_failures",
		"pastes",
		"attachments",
		"object_refs",
		"shares",
		"daily_metrics",
		"orders",
		"webhook_events",
		"audit_logs",
		"reports",
		"jobs",
		"mails",
	}
	for _, table := range requiredTables {
		want := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s", table)
		if !strings.Contains(first.SQL, want) {
			t.Fatalf("expected initial schema to create %s", table)
		}
	}
	for _, seed := range []string{
		"price_plus_monthly",
		"price_plus_yearly",
		"price_pro_monthly",
		"price_pro_yearly",
	} {
		if !strings.Contains(first.SQL, seed) {
			t.Fatalf("expected initial schema to seed %s", seed)
		}
	}
}

func TestLoadMigrationsAreStrictlyOrdered(t *testing.T) {
	migrations, err := LoadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	for i := 1; i < len(migrations); i++ {
		if migrations[i].Version <= migrations[i-1].Version {
			t.Fatalf("migrations are not strictly ordered: %#v", migrations)
		}
	}
}

func TestLoadMigrationsIncludesOAuthIdentities(t *testing.T) {
	migrations, err := LoadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	var migration Migration
	for _, item := range migrations {
		if item.Version == 3 {
			migration = item
			break
		}
	}
	if migration.Name != "oauth_identities" || migration.Filename != "000003_oauth_identities.sql" {
		t.Fatalf("expected oauth identity migration, got %#v", migration)
	}
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS oauth_identities",
		"PRIMARY KEY (provider, subject)",
		"UNIQUE (user_id, provider)",
	} {
		if !strings.Contains(migration.SQL, expected) {
			t.Fatalf("expected oauth migration to contain %q", expected)
		}
	}
}

func TestLoadMigrationsIncludesWorkerHeartbeats(t *testing.T) {
	migrations, err := LoadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	var migration Migration
	for _, item := range migrations {
		if item.Version == 4 {
			migration = item
			break
		}
	}
	if migration.Name != "worker_heartbeats" || migration.Filename != "000004_worker_heartbeats.sql" {
		t.Fatalf("expected worker heartbeat migration, got %#v", migration)
	}
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS worker_heartbeats",
		"worker_id text PRIMARY KEY",
		"last_seen_at timestamptz NOT NULL",
		"worker_heartbeats_last_seen_at_idx",
	} {
		if !strings.Contains(migration.SQL, expected) {
			t.Fatalf("expected worker heartbeat migration to contain %q", expected)
		}
	}
}

func TestLoadMigrationsIncludesAdminRuntimeControls(t *testing.T) {
	migrations, err := LoadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	var migration Migration
	for _, item := range migrations {
		if item.Version == 5 {
			migration = item
			break
		}
	}
	if migration.Name != "admin_runtime_controls" || migration.Filename != "000005_admin_runtime_controls.sql" {
		t.Fatalf("expected admin runtime controls migration, got %#v", migration)
	}
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS system_configs",
		"CREATE TABLE IF NOT EXISTS redemption_batches",
		"CREATE TABLE IF NOT EXISTS redemption_codes",
		"CREATE TABLE IF NOT EXISTS redemption_records",
		"CREATE TABLE IF NOT EXISTS alert_events",
	} {
		if !strings.Contains(migration.SQL, expected) {
			t.Fatalf("expected admin runtime controls migration to contain %q", expected)
		}
	}
}

func TestLoadMigrationsIncludesPlanTagLimits(t *testing.T) {
	migrations, err := LoadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	var migration Migration
	for _, item := range migrations {
		if item.Version == 7 {
			migration = item
			break
		}
	}
	if migration.Name != "plan_tag_limits" || migration.Filename != "000007_plan_tag_limits.sql" {
		t.Fatalf("expected plan tag limits migration, got %#v", migration)
	}
	for _, expected := range []string{
		"ADD COLUMN IF NOT EXISTS tags_per_paste_limit",
		"WHERE id = 'plus'",
		"WHERE id = 'pro'",
	} {
		if !strings.Contains(migration.SQL, expected) {
			t.Fatalf("expected plan tag limits migration to contain %q", expected)
		}
	}
}

func TestLoadMigrationsIncludesQueueLeases(t *testing.T) {
	migrations, err := LoadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	var migration Migration
	for _, item := range migrations {
		if item.Version == 9 {
			migration = item
			break
		}
	}
	if migration.Name != "queue_leases" || migration.Filename != "000009_queue_leases.sql" {
		t.Fatalf("expected queue lease migration, got %#v", migration)
	}
	for _, expected := range []string{
		"ALTER TABLE jobs",
		"ALTER TABLE mails",
		"claimed_by text NOT NULL",
		"lease_expires_at timestamptz",
		"jobs_runnable_lease_idx",
		"mails_runnable_lease_idx",
	} {
		if !strings.Contains(migration.SQL, expected) {
			t.Fatalf("expected queue lease migration to contain %q", expected)
		}
	}
}
