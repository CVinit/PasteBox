package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Migration struct {
	Version  int64
	Name     string
	Filename string
	SQL      string
	Checksum string
}

type MigrationStatus struct {
	Migration Migration
	Applied   bool
	Dirty     bool
}

type AppliedMigration struct {
	Version  int64
	Name     string
	Checksum string
}

func LoadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	migrations := make([]Migration, 0, len(entries))
	seen := map[int64]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, name, err := parseMigrationFilename(entry.Name())
		if err != nil {
			return nil, err
		}
		if previous := seen[version]; previous != "" {
			return nil, fmt.Errorf("duplicate migration version %d in %s and %s", version, previous, entry.Name())
		}
		seen[version] = entry.Name()

		path := "migrations/" + entry.Name()
		content, err := migrationFS.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(content)
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     name,
			Filename: entry.Name(),
			SQL:      string(content),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	return migrations, nil
}

func parseMigrationFilename(filename string) (int64, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	versionText, name, ok := strings.Cut(base, "_")
	if !ok {
		return 0, "", fmt.Errorf("invalid migration filename %q: expected <version>_<name>.sql", filename)
	}
	version, err := strconv.ParseInt(versionText, 10, 64)
	if err != nil || version <= 0 {
		return 0, "", fmt.Errorf("invalid migration version in %q", filename)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, "", fmt.Errorf("invalid migration filename %q: name is required", filename)
	}
	return version, name, nil
}

func MigrationStatuses(ctx context.Context, databaseURL string) ([]MigrationStatus, error) {
	migrations, err := LoadMigrations()
	if err != nil {
		return nil, err
	}

	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	defer conn.Close(ctx)

	applied, err := appliedMigrations(ctx, conn)
	if err != nil {
		return nil, err
	}

	statuses := make([]MigrationStatus, 0, len(migrations))
	for _, migration := range migrations {
		appliedMigration, ok := applied[migration.Version]
		statuses = append(statuses, MigrationStatus{
			Migration: migration,
			Applied:   ok && appliedMigration.Checksum == migration.Checksum,
			Dirty:     ok && appliedMigration.Checksum != migration.Checksum,
		})
	}
	return statuses, nil
}

func ApplyMigrations(ctx context.Context, databaseURL string) ([]Migration, error) {
	migrations, err := LoadMigrations()
	if err != nil {
		return nil, err
	}

	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint PRIMARY KEY,
    name text NOT NULL,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`); err != nil {
		return nil, fmt.Errorf("ensure schema_migrations: %w", err)
	}

	applied, err := appliedMigrations(ctx, conn)
	if err != nil {
		return nil, err
	}

	appliedNow := []Migration{}
	for _, migration := range migrations {
		if existing, ok := applied[migration.Version]; ok {
			if existing.Checksum != migration.Checksum {
				return appliedNow, fmt.Errorf("migration %s checksum mismatch: database has %s, embedded file has %s", migration.Filename, existing.Checksum, migration.Checksum)
			}
			continue
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return appliedNow, fmt.Errorf("begin migration %s: %w", migration.Filename, err)
		}
		if _, err := tx.Exec(ctx, migration.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return appliedNow, fmt.Errorf("apply migration %s: %w", migration.Filename, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`, migration.Version, migration.Name, migration.Checksum); err != nil {
			_ = tx.Rollback(ctx)
			return appliedNow, fmt.Errorf("record migration %s: %w", migration.Filename, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return appliedNow, fmt.Errorf("commit migration %s: %w", migration.Filename, err)
		}
		appliedNow = append(appliedNow, migration)
	}

	return appliedNow, nil
}

func appliedMigrations(ctx context.Context, conn *pgx.Conn) (map[int64]AppliedMigration, error) {
	rows, err := conn.Query(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		if isUndefinedTable(err) {
			return map[int64]AppliedMigration{}, nil
		}
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int64]AppliedMigration{}
	for rows.Next() {
		var migration AppliedMigration
		if err := rows.Scan(&migration.Version, &migration.Name, &migration.Checksum); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[migration.Version] = migration
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	return applied, nil
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P01"
	}
	return false
}
