# Database Guidelines

> Database patterns and conventions for this project.

---

## Overview

PasteBox uses PostgreSQL as the production source of truth. Migrations are
embedded in the Go binary under `internal/postgres/migrations/` and applied by
the `pastebox migrate up` command.

---

## Query Patterns

- Use typed repository boundaries for production persistence work. Do not place
  SQL in HTTP handlers.
- Keep Redis out of source-of-truth paths for plans, billing, paste metadata,
  object references, and final order state.
- Use JSONB only where the application data is naturally variable, such as
  paste tags and audit/webhook metadata.

---

## Migrations

- Migration files live at `internal/postgres/migrations/<version>_<name>.sql`.
- Version numbers are positive, strictly ordered integers rendered as six
  digits, for example `000001_initial_schema.sql`.
- The migration runner records `version`, `name`, and `checksum` in
  `schema_migrations`.
- Applied migration files must not be edited. Add a new migration instead.
- `pastebox migrate status` reports `pending`, `applied`, or `dirty`.
- `pastebox migrate up` applies pending migrations in a transaction. A checksum
  mismatch is a hard failure.
- Local development reset uses the Makefile database targets; production reset
  requires an explicit restore/rollback runbook.

### Scenario: Initial PostgreSQL Schema

#### 1. Scope / Trigger

- Trigger: Any change that adds a durable source-of-truth table, changes a
  migration file, or changes `pastebox migrate status|up`.

#### 2. Signatures

- Command: `pastebox migrate status`
- Command: `pastebox migrate up`
- Package: `internal/postgres.LoadMigrations() ([]Migration, error)`
- Migration table: `schema_migrations(version, name, checksum, applied_at)`

#### 3. Contracts

- The initial schema includes users, sessions, auth tokens, login failures,
  pastes, attachments, object refs, shares, plans, prices, orders, webhook
  events, audit logs, reports, jobs, mails, and daily metrics.
- Plans and prices are seeded from the current product catalog in the initial
  migration.
- Migration checksums are SHA-256 hex strings generated from the embedded SQL
  file contents.
- `PASTEBOX_DATABASE_URL` is the only database connection input for migration
  commands.

#### 4. Validation & Error Matrix

- Invalid migration filename -> loader error.
- Duplicate migration version -> loader error.
- PostgreSQL connection failure -> command exits 1.
- Applied checksum differs from embedded SQL -> migration command exits 1.
- No pending migrations -> `pastebox migrate up` exits 0 and reports that the
  database is up to date.

#### 5. Good/Base/Bad Cases

- Good: Add `000002_add_feature_table.sql`, test that it loads in order, and
  run `pastebox migrate up` before deploying code that reads the new table.
- Base: Add repository code against tables already present in an applied
  migration.
- Bad: Modify `000001_initial_schema.sql` after it has been applied in any
  environment.

#### 6. Tests Required

- `internal/postgres` tests assert migration loading, ordering, checksum shape,
  and required table coverage.
- CLI tests assert invalid database URLs fail through `migrate status` and
  `migrate up`.
- Run `go test ./...` after migration or command changes.

#### 7. Wrong vs Correct

##### Wrong

```sh
# Editing an applied file changes its checksum and breaks deployment.
vim internal/postgres/migrations/000001_initial_schema.sql
```

##### Correct

```sh
cat > internal/postgres/migrations/000002_add_index.sql
go test ./...
pastebox migrate up
```

---

## Naming Conventions

- Tables and columns use snake_case.
- Primary keys use text IDs to preserve current MVP ID prefixes such as
  `usr_`, `pst_`, `att_`, and `ord_`.
- Timestamp columns use `timestamptz`.
- Byte counts use `bigint`.
- JSON payloads use `jsonb`.

---

## Common Mistakes

- Do not use Redis as final state for business entities.
- Do not edit an applied migration file; add a new migration.
- Do not report a production deployment as ready if `pastebox migrate up` has
  not run successfully against the target database.
