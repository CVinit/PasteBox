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
- Daily quota counters use the `app.DailyMetricStore` boundary. The in-memory
  store remains the default until users and related source-of-truth entities are
  PostgreSQL-backed; PostgreSQL implementations must read/write
  `daily_metrics` through typed repository methods.
- Plan and price reads use the PostgreSQL `CatalogStore` boundary once runtime
  catalog wiring is switched to durable storage. The service-level catalog
  remains the single HTTP source for `/api/v1/plans` and billing price
  responses.
- Audit log persistence uses the PostgreSQL `AuditLogStore` boundary. Metadata
  is stored as JSONB and admin-facing listing is newest-first.
- User persistence uses the PostgreSQL `UserStore` boundary. Runtime auth must
  not switch to PostgreSQL users until sessions and auth tokens are switched
  with compatible repository semantics.
- Auth lifecycle state uses the PostgreSQL `SessionStore`, `AuthTokenStore`,
  and `LoginFailureStore` boundaries. These stores must move with user runtime
  wiring so restart behavior is coherent.
- Content metadata uses PostgreSQL `PasteStore`, `AttachmentStore`, and
  `ShareStore` boundaries. Attachment bytes are intentionally not stored in
  PostgreSQL; Phase 2 object storage owns byte persistence.
- Billing, support, worker, and notification state use PostgreSQL
  `OrderStore`, `WebhookEventStore`, `ReportStore`, `JobStore`, and
  `MailStore` boundaries.

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
- Do not treat a daily-metric repository error as zero usage. Quota checks must
  fail closed so a database outage cannot bypass upload/download limits.

## Scenario: Daily Metrics Repository Boundary

### 1. Scope / Trigger

- Trigger: Any change that reads or writes `DailyUploadBytes`,
  `DailyShareDownloadBytes`, or the `daily_metrics` table.

### 2. Signatures

- Interface: `app.DailyMetricStore`
- Read: `DailyMetric(ctx context.Context, userID string, kind string, day time.Time) (int64, error)`
- Write: `RecordDailyMetric(ctx context.Context, userID string, kind string, day time.Time, bytes int64) error`
- PostgreSQL constructor: `postgres.NewDailyMetricStore(pool *pgxpool.Pool)`
- Table key: `daily_metrics(user_id, metric_kind, metric_day)`

### 3. Contracts

- `kind` values currently used by the service are `upload` and
  `share_download`.
- The day bucket is UTC date based, matching the existing quota contract.
- Missing PostgreSQL rows read as zero bytes.
- Positive writes accumulate with an upsert:
  `bytes = daily_metrics.bytes + EXCLUDED.bytes`.
- Zero or negative writes are ignored.
- Service quota paths must propagate repository errors instead of converting
  them to zero.
- Runtime API wiring must not switch daily metrics to PostgreSQL before users
  are PostgreSQL-backed, because `daily_metrics.user_id` references `users(id)`.

### 4. Validation & Error Matrix

- Missing daily metric row -> return `0, nil`.
- Store read failure during quota check -> return the store error and block the
  mutation.
- Store write failure during upload/download accounting -> return the store
  error before applying related in-memory mutation.
- `bytes <= 0` on write -> no-op success.

### 5. Good/Base/Bad Cases

- Good: `CreatePaste` reads the quota, records positive text bytes, then stores
  the paste only after the metric write succeeds.
- Base: In-memory `DailyMetricStore` keeps local development and unit tests
  lightweight while repository code is introduced.
- Bad: Enable the PostgreSQL daily metrics store while users are still only
  in-memory; the foreign key will reject writes for missing users.

### 6. Tests Required

- App tests assert daily metric read failures block quota mutations.
- App tests assert daily metric write failures do not partially create pastes.
- PostgreSQL integration tests with `PASTEBOX_TEST_DATABASE_URL` assert missing
  rows read as zero, UTC-day accumulation works, and zero writes are ignored.
- Run full `make test` after changing daily metric quota paths because frontend
  quota displays consume these API fields.

### 7. Wrong vs Correct

#### Wrong

```go
upload, err := store.DailyMetric(ctx, userID, "upload", now)
if err != nil {
    upload = 0
}
```

#### Correct

```go
upload, err := store.DailyMetric(ctx, userID, "upload", now)
if err != nil {
    return QuotaView{}, err
}
```

## Scenario: Catalog And Audit Repository Boundaries

### 1. Scope / Trigger

- Trigger: Any change that reads or writes `plans`, `prices`, or `audit_logs`.

### 2. Signatures

- Catalog constructor: `postgres.NewCatalogStore(pool *pgxpool.Pool)`
- Catalog read: `Catalog(ctx context.Context) (plans.Catalog, error)`
- Audit constructor: `postgres.NewAuditLogStore(pool *pgxpool.Pool)`
- Audit write: `RecordAuditLog(ctx context.Context, log app.AuditLog) error`
- Audit read: `AuditLogs(ctx context.Context, limit int) ([]app.AuditLog, error)`
- Scoped audit read:
  `AuditLogsForActorOrTargets(ctx context.Context, actorID string, targets []string, limit int) ([]app.AuditLog, error)`
- Service catalog accessor: `app.Service.PlanCatalog() plans.Catalog`
- Account export service: `app.Service.ExportUser(userID string) (map[string]any, error)`

### 3. Contracts

- `/api/v1/plans` must read from the service catalog accessor, not directly
  from `plans.DefaultCatalog()`, so HTTP plans and billing prices share one
  catalog source.
- PostgreSQL catalog ordering is stable: `free`, `plus`, `pro`, followed by
  unknown plan IDs alphabetically.
- PostgreSQL price ordering is stable by plan order, then `monthly`, `yearly`,
  then unknown periods alphabetically by ID.
- The initial migration seeds the same launch catalog as `plans.DefaultCatalog()`
  until the runtime catalog is fully PostgreSQL-backed.
- Audit metadata must be JSON-serializable and stored in `audit_logs.metadata`
  as JSONB. Nil metadata is stored and returned as an empty object.
- Audit listing defaults to a bounded newest-first result when the caller passes
  a non-positive limit.
- Scoped audit reads are used by account export and must return logs where the
  exported user is the actor or where the log target is one of the exported
  user-owned resources. The resource target set includes the user ID, paste IDs,
  attachment IDs, share IDs, order IDs, report IDs, and webhook event IDs that
  belong to the export.
- Account export must include user, pastes, shares, orders, reports, billing
  webhook events for exported orders, scoped audit logs, and `exportedAt`.
  Export-scoped collections must be initialized as empty arrays and sorted
  newest-first where the service exposes ordering.
- Account export must record an `account.export` audit log with the exported
  user as both actor and target before returning the scoped audit log snapshot,
  so export requests are themselves included in the auditable data-rights trail.
- Account export must not leak unrelated users' audit entries just because an
  admin actor created them. Admin-created entries are exportable only when their
  `target` is part of the exported user's target set.

### 4. Validation & Error Matrix

- Catalog query failure -> return an error; do not silently fall back to default
  catalog in repository code.
- Audit metadata cannot be marshaled -> return an error before inserting.
- Audit insert failure -> return an error with audit context.
- Audit metadata cannot be decoded on read -> return an error; do not drop
  metadata.
- Scoped audit query failure -> account export returns the store error instead
  of silently omitting audit logs.
- Empty scoped target list -> return only actor-owned audit logs, bounded by
  limit.

### 5. Good/Base/Bad Cases

- Good: Add a new price row through a migration and update both
  `plans.DefaultCatalog()` and the catalog integration test while the runtime
  service is still hybrid.
- Base: Keep the in-memory catalog as runtime default while other source-of-
  truth tables are still in-memory, but expose it only through
  `Service.PlanCatalog()`.
- Bad: Return `plans.DefaultCatalog()` directly from an HTTP handler or store
  audit metadata as an opaque string outside JSONB.
- Good: Exporting a paid user includes their orders, billing webhook events,
  abuse reports, and only audit logs tied to the user or those exported
  resources.
- Bad: Export all newest audit logs and filter them in the browser or include
  unrelated admin actions performed on other users.

### 6. Tests Required

- PostgreSQL catalog integration tests with `PASTEBOX_TEST_DATABASE_URL` assert
  migrated `plans` and `prices` match the launch catalog contract.
- PostgreSQL audit log integration tests assert JSONB metadata round-trips and
  listing order is newest-first.
- App export tests assert orders, reports, webhook events, and scoped audit logs
  are included, `account.export` is audited, and unrelated user audit logs are
  excluded.
- Handler tests for `/api/v1/plans` must continue to cover response shape.
- Run full `make test` after changing catalog, billing price, or audit-log API
  contracts.

### 7. Wrong vs Correct

#### Wrong

```go
func (s *Server) planCatalog(w http.ResponseWriter, _ *http.Request) {
    writeJSON(w, http.StatusOK, plans.DefaultCatalog())
}
```

#### Correct

```go
func (s *Server) planCatalog(w http.ResponseWriter, _ *http.Request) {
    writeJSON(w, http.StatusOK, s.app.PlanCatalog())
}
```

#### Wrong

```go
logs, _ := s.audit.AuditLogs(ctx, 1000)
export["auditLogs"] = logs
```

#### Correct

```go
targets := exportAuditTargets(userID, pastes, shares, orders, reports, events)
logs, err := s.audit.AuditLogsForActorOrTargets(ctx, userID, targets, 1000)
if err != nil {
    return nil, err
}
export["auditLogs"] = logs
```

## Scenario: Production PostgreSQL WAL/PITR Maintenance

### 1. Scope / Trigger

- Trigger: Any change that alters `compose.production.yaml`, production
  PostgreSQL authentication, WAL archiving, backup scripts, restore drills, or
  production preflight environment validation.

### 2. Signatures

- Compose service: `postgres`
- Compose maintenance services: `postgres-wal-check`, `postgres-basebackup`,
  `postgres-pitr-drill`, `backup-push`
- Script: `deploy/backup/postgres-wal-check.sh`
- Script: `deploy/backup/postgres-basebackup.sh`
- Script: `deploy/backup/postgres-pitr-restore-drill.sh`
- Config file: `deploy/postgres/pg_hba.conf`
- Preflight command: `pastebox preflight production`
- Environment keys:
  `PASTEBOX_WAL_ARCHIVE_TIMEOUT_SECONDS`,
  `PASTEBOX_WAL_ARCHIVE_MAX_AGE_SECONDS`,
  `PASTEBOX_WAL_ARCHIVE_WAIT_SECONDS`,
  `PASTEBOX_PITR_RECOVERY_WAIT_SECONDS`

### 3. Contracts

- Production PostgreSQL must run with `wal_level=replica`,
  `archive_mode=on`, and
  `archive_timeout=$PASTEBOX_WAL_ARCHIVE_TIMEOUT_SECONDS`.
- The production PostgreSQL container must mount
  `deploy/postgres/pg_hba.conf` and set `hba_file` to that path so maintenance
  containers can run password-authenticated `pg_basebackup`.
- `deploy/postgres/pg_hba.conf` must include both application access and
  `host replication all samenet scram-sha-256` for Compose-network
  maintenance containers.
- WAL archive freshness checks must inspect the newest archived WAL segment in
  `/backups/wal` and `pg_stat_archiver` recent failures. Do not require the
  exact filename returned by `pg_switch_wal()` to appear; PostgreSQL can return
  the next segment while archiving completes the previous segment or a `.backup`
  marker.
- Base-backup jobs must run `pg_verifybackup --wal-directory=/backups/wal`
  before creating the tarball, checksum, and manifest.
- PITR drills must verify the base-backup checksum, run `pg_verifybackup`, start
  an isolated PostgreSQL instance, wait until `pg_is_in_recovery()` returns
  false, then assert `schema_migrations` is readable.
- WAL timeout, max-age, and wait values must be positive integers and no more
  than `900` seconds to preserve the confirmed 15-minute RPO target.

### 4. Validation & Error Matrix

- Missing WAL env key in production preflight -> preflight failure.
- Non-positive or non-integer WAL timeout/max-age/wait -> preflight or script
  exit `2`.
- WAL timeout/max-age/wait greater than `900` -> preflight or script exit `2`.
- Missing replication rule in `pg_hba.conf` -> `pg_basebackup` fails with
  `no pg_hba.conf entry for replication connection`.
- Recent `pg_stat_archiver` failure newer than the last successful archive ->
  WAL check/base-backup failure.
- No fresh archived WAL segment within the configured wait window -> WAL
  check/base-backup failure.
- `pg_verifybackup` failure -> base-backup/PITR drill failure before producing
  or trusting artifacts.
- PITR recovery does not promote within
  `PASTEBOX_PITR_RECOVERY_WAIT_SECONDS` -> PITR drill failure.

### 5. Good/Base/Bad Cases

- Good: An isolated Compose project starts `postgres`, runs
  `postgres-wal-check`, runs `postgres-basebackup`, and then runs
  `postgres-pitr-drill`; the drill prints `schema_migrations=<count>` and
  `in_recovery=f`.
- Base: `docker compose --profile maintenance config` renders all maintenance
  services with the committed example env when `PASTEBOX_ENV_FILE` points at
  `deploy/production.env.example`.
- Bad: Waiting for `/backups/wal/$(pg_switch_wal)` can fail even while WAL
  archiving is healthy, because the archived file may be the completed previous
  segment or a `.backup` marker.

### 6. Tests Required

- Run `sh -n` for every changed backup script.
- Run `docker compose --profile maintenance -f compose.production.yaml
  --env-file deploy/production.env.example config` with
  `PASTEBOX_ENV_FILE=./deploy/production.env.example`.
- Run `go test ./cmd/pastebox` after changing production preflight env
  validation.
- Run full `make test` before committing production backup/preflight changes.
- For WAL/PITR script changes, run an isolated smoke project that starts
  production PostgreSQL, seeds a minimal `schema_migrations` table, then runs
  `postgres-wal-check`, `postgres-basebackup`, and `postgres-pitr-drill`.
  Clean up the smoke project and volumes afterward.

### 7. Wrong vs Correct

#### Wrong

```sh
wal_file="$(psql --command="SELECT pg_walfile_name(pg_switch_wal());")"
test -f "/backups/wal/$wal_file"
```

#### Correct

```sh
psql --command="SELECT pg_switch_wal();" >/dev/null
latest_wal="$(ls -1t /backups/wal | grep -E '^[0-9A-F]{24}$' | head -n 1)"
psql --command="SELECT last_failed_wal FROM pg_stat_archiver WHERE last_failed_time > last_archived_time;"
```

#### Wrong

```yaml
postgres:
  image: postgres:17-alpine
  command: ["postgres", "-c", "archive_mode=on"]
```

#### Correct

```yaml
postgres:
  volumes:
    - ./deploy/postgres/pg_hba.conf:/etc/postgresql/pg_hba.conf:ro
  command:
    - "postgres"
    - "-c"
    - "archive_mode=on"
    - "-c"
    - "hba_file=/etc/postgresql/pg_hba.conf"
```

## Scenario: User Repository Boundary

### 1. Scope / Trigger

- Trigger: Any change that reads, creates, or updates rows in the `users`
  table, or prepares runtime auth to use PostgreSQL users.

### 2. Signatures

- Constructor: `postgres.NewUserStore(pool *pgxpool.Pool)`
- Create: `CreateUser(ctx context.Context, user app.User) error`
- Read by ID: `UserByID(ctx context.Context, id string) (app.User, error)`
- Read by email: `UserByEmail(ctx context.Context, email string) (app.User, error)`
- Update: `UpdateUser(ctx context.Context, user app.User) error`
- Account deletion request:
  `app.Service.RequestAccountDeletion(userID string) (app.UserView, error)`
- Account deletion cancel:
  `app.Service.CancelAccountDeletion(userID string) (app.UserView, error)`
- Account deletion execute: `app.Service.ExecuteAccountDeletion(userID string) error`
- Errors: `postgres.ErrUserNotFound`, `postgres.ErrUserEmailExists`

### 3. Contracts

- `UserStore` round-trips every current `app.User` persistence field:
  `id`, `email`, `display_name`, `language`, `password_hash`, `role`,
  `email_verified`, `plan_id`, `plan_expires_at`, `frozen`, `created_at`,
  `updated_at`, `delete_requested_at`, `delete_scheduled_at`, and
  `deleted_at`.
- Nullable timestamp columns map to nil `*time.Time` values in `app.User`.
- Duplicate emails return `ErrUserEmailExists` so auth registration can map the
  database error to the existing `email_exists` API code once runtime is
  switched.
- Missing users return `ErrUserNotFound` from both read and update paths.
- Runtime auth must not use PostgreSQL users while sessions, auth tokens, and
  login-failure records are still only in memory, because a restart would
  otherwise preserve users but lose the surrounding auth lifecycle state.
- Account deletion lifecycle changes must be auditable:
  `account.deletion_requested` after scheduling deletion,
  `account.deletion_canceled` after canceling a pending deletion, and
  `account.deleted` after user state, owned active pastes, shares, and sessions
  have been updated. These audit logs use the account user ID as both actor and
  target so data-rights operations remain exportable and visible to admins.
- `account.deletion_requested` metadata includes `scheduledAt`.
  `account.deleted` metadata includes `pasteCount` and `shareCount` for the
  number of active owned resources moved or revoked during execution.

### 4. Validation & Error Matrix

- `users.email` unique violation -> `ErrUserEmailExists`.
- Missing row on `UserByID` / `UserByEmail` -> `ErrUserNotFound`.
- `UpdateUser` affects zero rows -> `ErrUserNotFound`.
- Other PostgreSQL failures -> wrapped repository error with operation context.
- Account deletion audit write failure -> return the audit error; do not report
  deletion lifecycle completion without an audit trail.

### 5. Good/Base/Bad Cases

- Good: Registering a user through a future PostgreSQL-backed auth path creates
  the user, email verification token, login failure state, and session in one
  coherent durable flow.
- Good: Requesting, canceling, and executing account deletion leaves
  `account.deletion_requested`, `account.deletion_canceled`, and
  `account.deleted` audit logs tied to the account user.
- Base: Introduce `UserStore` with live integration tests while keeping runtime
  auth in memory until the dependent repositories are ready.
- Bad: Persist users to PostgreSQL but keep sessions and auth tokens only in
  memory in production mode; users survive restart while login/session recovery
  behavior silently changes.
- Bad: Mark a user deleted and revoke sessions without writing
  `account.deleted`; support cannot prove when the data-rights action ran.

### 6. Tests Required

- PostgreSQL integration tests with `PASTEBOX_TEST_DATABASE_URL` assert create,
  read by ID, read by email, update, duplicate-email error mapping, nullable
  timestamp handling, and missing-user error mapping.
- When runtime auth is switched, handler/domain tests must prove register,
  login, email verification, magic link, password reset, session lookup,
  logout, logout-all, account deletion, and admin bootstrap survive process
  restart with PostgreSQL-backed repositories.
- App tests assert account deletion request, cancel, and execute paths write
  the three account deletion audit actions.
- PostgreSQL-backed service integration tests assert deletion request and
  execution audit logs survive restart with durable audit storage.
- Run full `make test` after changing user repository or auth runtime wiring.

### 7. Wrong vs Correct

#### Wrong

```go
if err := userStore.CreateUser(ctx, user); err != nil {
    return AuthResult{}, err
}
```

#### Correct

```go
if err := userStore.CreateUser(ctx, user); errors.Is(err, postgres.ErrUserEmailExists) {
    return AuthResult{}, E(http.StatusConflict, "email_exists", "email is already registered")
} else if err != nil {
    return AuthResult{}, err
}
```

## Scenario: Auth State Repository Boundaries

### 1. Scope / Trigger

- Trigger: Any change that reads or writes `sessions`, `auth_tokens`, or
  `login_failures`, or prepares runtime auth to use PostgreSQL-backed users.

### 2. Signatures

- Session constructor: `postgres.NewSessionStore(pool *pgxpool.Pool)`
- Session create: `CreateSession(ctx context.Context, session app.Session) error`
- Session read: `SessionByID(ctx context.Context, id string) (app.Session, error)`
- Session revoke: `RevokeSession(ctx context.Context, id string, revokedAt time.Time) error`
- User-session revoke: `RevokeUserSessions(ctx context.Context, userID string, revokedAt time.Time) (int64, error)`
- Token constructor: `postgres.NewAuthTokenStore(pool *pgxpool.Pool)`
- Token create: `CreateAuthToken(ctx context.Context, kind string, token app.AuthToken) error`
- Token read: `AuthToken(ctx context.Context, kind string, hash string) (app.AuthToken, error)`
- Token consume marker: `MarkAuthTokenUsed(ctx context.Context, kind string, hash string, usedAt time.Time) error`
- Login-failure constructor: `postgres.NewLoginFailureStore(pool *pgxpool.Pool)`
- Login-failure read: `LoginFailure(ctx context.Context, email string) (app.LoginFailure, error)`
- Login-failure save: `SaveLoginFailure(ctx context.Context, email string, failure app.LoginFailure) error`
- Login-failure delete: `DeleteLoginFailure(ctx context.Context, email string) error`
- Errors: `ErrSessionNotFound`, `ErrAuthTokenNotFound`,
  `ErrLoginFailureNotFound`

### 3. Contracts

- Sessions round-trip `id`, `user_id`, `created_at`, `expires_at`, and nullable
  `revoked_at`.
- Auth tokens are looked up by both `kind` and `hash`; a valid hash under the
  wrong kind must be treated as missing.
- Auth token `used_at` is nullable and is the durable consume marker for email
  verification, magic link, and password reset flows.
- Login failures upsert by normalized email, preserving `count`,
  `window_start`, and nullable `locked_until`.
- Deleting a missing login-failure row is a no-op success.
- Runtime auth must switch users, sessions, auth tokens, and login failures
  together so registration, login, verification, password reset, magic link,
  logout, and account deletion survive process restart consistently.

### 4. Validation & Error Matrix

- Missing session on read or direct revoke -> `ErrSessionNotFound`.
- Missing auth token on read or consume marker update -> `ErrAuthTokenNotFound`.
- Wrong token kind -> `ErrAuthTokenNotFound`.
- Missing login-failure row on read -> `ErrLoginFailureNotFound`.
- Other PostgreSQL failures -> wrapped repository error with operation context.

### 5. Good/Base/Bad Cases

- Good: A future PostgreSQL-backed auth runtime creates the user, verification
  token, login failure state, and session through repository boundaries and
  proves the flow survives process restart.
- Base: Introduce repository boundaries with live integration tests while
  runtime auth remains in-memory until all dependent stores are ready.
- Bad: Persist sessions but leave auth tokens in memory; password-reset and
  magic-link flows would still fail across restarts.

### 6. Tests Required

- PostgreSQL integration tests with `PASTEBOX_TEST_DATABASE_URL` assert session
  create/read/revoke/user-revoke, auth token create/read/wrong-kind miss/used
  marker, and login-failure save/read/delete behavior.
- Runtime auth switch tests must prove restart persistence for register,
  login, email verification, magic link, password reset, session lookup,
  logout, logout-all, account deletion, and bootstrap admin.
- Run full `make test` after changing auth-state repository or runtime wiring.

### 7. Wrong vs Correct

#### Wrong

```go
token, err := tokenStore.AuthToken(ctx, "email_verification", hash)
if err != nil {
    token = app.AuthToken{}
}
```

#### Correct

```go
token, err := tokenStore.AuthToken(ctx, "email_verification", hash)
if errors.Is(err, postgres.ErrAuthTokenNotFound) {
    return AuthResult{}, E(http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
} else if err != nil {
    return AuthResult{}, err
}
```

## Scenario: Bootstrap Admin Runtime

### 1. Scope / Trigger

- Trigger: Any change that touches production administrator bootstrap,
  `PASTEBOX_BOOTSTRAP_ADMIN_*` environment handling, auth runtime startup, or
  `app.Service.SeedAdmin`.

### 2. Signatures

- Config: `PASTEBOX_BOOTSTRAP_ADMIN_EMAIL`
- Config: `PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD`
- Service: `app.Service.SeedAdmin(email string, password string) (app.UserView, error)`
- Startup: `app.NewWithStorage(ctx, cfg, stores)`
- CLI helper: `pastebox admin create --email <email> --password <password>`

### 3. Contracts

- Startup calls `SeedAdmin` only when both bootstrap env vars are set.
- Bootstrap email is normalized before lookup or creation.
- `SeedAdmin` is create-or-update: it creates the account when missing and
  updates the existing account with the same normalized email when present.
- Bootstrap always marks the account email verified, grants `role = admin`,
  unfreezes the account, clears pending deletion fields, replaces the password
  hash with the configured password, and clears login-failure state for that
  email.
- Startup must fail if bootstrap validation or persistence fails; it must not
  silently continue with no usable administrator.
- `pastebox admin create` is only a bootstrap env helper. It must not claim to
  have written the production database unless it actually starts a production
  store-backed service.

### 4. Validation & Error Matrix

- Invalid bootstrap email -> `400 invalid_email` and startup fails.
- Bootstrap password shorter than eight characters -> `400 weak_password` and
  startup fails.
- Duplicate existing email -> update the existing user instead of returning
  `email_exists`.
- Stored login-failure lock for the bootstrap email -> deleted during
  bootstrap so the configured administrator can log in immediately.
- Repository create/update/login-failure delete failure -> startup fails with
  the wrapped store error.

### 5. Good/Base/Bad Cases

- Good: A first production start creates a verified admin, and a later restart
  with the same email rotates the admin password without changing the user ID.
- Good: A locked bootstrap admin can log in after password rotation because
  the login-failure state was cleared.
- Base: The local helper prints normalized env values and never echoes the raw
  password.
- Bad: Calling `Register` from `SeedAdmin`, because an existing user would make
  bootstrap non-idempotent and startup could fail after a password rotation.
- Bad: Ignoring `SeedAdmin` errors during startup, because production would
  appear healthy while no administrator can reach launch-gate workflows.

### 6. Tests Required

- App tests assert `SeedAdmin` creates a normalized verified admin.
- App tests assert calling `SeedAdmin` again with the same email preserves the
  user ID, replaces the password, keeps the admin role, clears login failures,
  and survives a fresh service instance with store-backed auth.
- App tests assert invalid bootstrap config makes `NewWithStorage` return an
  error instead of silently continuing.
- CLI tests assert `pastebox admin create` prints env guidance and does not
  echo the supplied password.
- Run full `make test` after changing bootstrap auth behavior.

### 7. Wrong vs Correct

#### Wrong

```go
_, _ = svc.SeedAdmin(cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword)
```

#### Correct

```go
if _, err := svc.SeedAdmin(cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword); err != nil {
    return nil, fmt.Errorf("bootstrap admin: %w", err)
}
```

#### Wrong

```go
result, err := svc.Register(ctx, RegisterInput{Email: email, Password: password})
```

#### Correct

```go
user, err := svc.userByEmailLocked(normalizeEmail(email))
if err == nil {
    user.PasswordHash = newHash
    user.Role = "admin"
    return svc.updateUserLocked(user)
}
```

## Scenario: Content Metadata Repository Boundaries

### 1. Scope / Trigger

- Trigger: Any change that reads or writes `pastes`, `attachments`,
  `object_refs`, or `shares`.

### 2. Signatures

- Paste constructor: `postgres.NewPasteStore(pool *pgxpool.Pool)`
- Paste create: `CreatePaste(ctx context.Context, paste app.Paste) error`
- Paste read: `PasteByID(ctx context.Context, id string) (app.Paste, error)`
- Paste list: `ListPastesByUser(ctx context.Context, userID string) ([]app.Paste, error)`
- Paste update: `UpdatePaste(ctx context.Context, paste app.Paste) error`
- Attachment constructor: `postgres.NewAttachmentStore(pool *pgxpool.Pool)`
- Attachment create: `CreateAttachment(ctx context.Context, attachment app.Attachment) error`
- Attachment read: `AttachmentByID(ctx context.Context, id string) (app.Attachment, error)`
- Attachment list: `ListAttachmentsByPaste(ctx context.Context, pasteID string) ([]app.Attachment, error)`
- Attachment update: `UpdateAttachment(ctx context.Context, attachment app.Attachment) error`
- Object ref upsert: `UpsertObjectRef(ctx context.Context, ref postgres.ObjectRef) error`
- Object ref read: `ObjectRef(ctx context.Context, objectKey string) (postgres.ObjectRef, error)`
- Share constructor: `postgres.NewShareStore(pool *pgxpool.Pool)`
- Share create: `CreateShare(ctx context.Context, share app.Share) error`
- Share read: `ShareByID(ctx context.Context, id string) (app.Share, error)`
- Share token lookup: `ShareByTokenHash(ctx context.Context, tokenHash string) (app.Share, error)`
- Share list: `ListSharesByUser(ctx context.Context, userID string) ([]app.Share, error)`
- Share update: `UpdateShare(ctx context.Context, share app.Share) error`
- Errors: `ErrPasteNotFound`, `ErrAttachmentNotFound`, `ErrObjectRefNotFound`,
  `ErrShareNotFound`, `ErrShareTokenExists`

### 3. Contracts

- Paste tags are stored in `pastes.tags` as JSONB and must round-trip nil tags
  as an empty slice so API responses can keep returning `[]`, not `null`.
- Paste repository methods persist metadata and text body only. They do not
  compute quota, scan aggregation, share counts, or view fields.
- Attachment repository methods persist metadata, scan state, object key,
  preview dimensions, and download count. `app.Attachment.Content` is not
  persisted in PostgreSQL and must remain nil after repository reads.
- `object_refs` stores object-key reference counts and object metadata only.
  Actual object bytes belong to the S3-compatible object storage phase.
- Shares store `token_hash` for lookup and `token_ciphertext` through the
  current `app.Share.Token` field. Runtime share URLs must not depend on
  plaintext tokens being recoverable from PostgreSQL unless encryption is
  explicitly implemented.
- Duplicate `shares.token_hash` returns `ErrShareTokenExists`.

### 4. Validation & Error Matrix

- Missing paste on read/update -> `ErrPasteNotFound`.
- Missing attachment on read/update -> `ErrAttachmentNotFound`.
- Missing object ref on read -> `ErrObjectRefNotFound`.
- Missing share on read/update -> `ErrShareNotFound`.
- Duplicate share token hash -> `ErrShareTokenExists`.
- Invalid paste tag JSON in the database -> repository read error; do not
  silently drop tags.
- Other PostgreSQL failures -> wrapped repository error with operation context.

### 5. Good/Base/Bad Cases

- Good: A future PostgreSQL-backed paste flow writes paste metadata, attachment
  metadata, object refs, and share rows while object bytes are written to the
  object storage adapter with rollback semantics.
- Base: Introduce metadata repositories with live integration tests before
  switching runtime storage.
- Bad: Persist attachment metadata in PostgreSQL but continue treating
  `Attachment.Content` as durable; bytes would still disappear on restart.

### 6. Tests Required

- PostgreSQL integration tests with `PASTEBOX_TEST_DATABASE_URL` assert paste
  create/read/list/update with JSONB tags, attachment create/read/list/update
  without content bytes, object ref upsert/read, share create/read by token
  hash/list/update, duplicate share token mapping, and missing-row errors.
- Runtime storage switch tests must prove app restart preserves paste metadata,
  attachment metadata, object references, and shares; Phase 2 tests must also
  prove attachment bytes survive through object storage.
- Run full `make test` after changing content metadata repositories or runtime
  storage wiring.

### 7. Wrong vs Correct

#### Wrong

```go
attachment, err := attachmentStore.AttachmentByID(ctx, id)
content := attachment.Content
```

#### Correct

```go
attachment, err := attachmentStore.AttachmentByID(ctx, id)
content, err := objectStore.Get(ctx, attachment.ObjectKey)
```

## Scenario: Operational State Repository Boundaries

### 1. Scope / Trigger

- Trigger: Any change that reads or writes `orders`, `webhook_events`,
  `reports`, `jobs`, or `mails`.

### 2. Signatures

- Runtime service wiring: `app.Stores{Operational: app.OperationalStores{...}}`
- Runtime operational boundary: `app.OperationalStores`
- Runtime order interface: `app.OrderStore`
- Runtime webhook interface: `app.WebhookEventStore`
- Runtime report interface: `app.ReportStore`
- Runtime queue interface: `app.QueueStore`
- Runtime mail interface: `app.MailStore`
- Order constructor: `postgres.NewOrderStore(pool *pgxpool.Pool)`
- Order create: `CreateOrder(ctx context.Context, order app.Order) error`
- Order read: `OrderByID(ctx context.Context, id string) (app.Order, error)`
- Order list all: `ListOrders(ctx context.Context) ([]app.Order, error)`
- Order list: `ListOrdersByUser(ctx context.Context, userID string) ([]app.Order, error)`
- Order update: `UpdateOrder(ctx context.Context, order app.Order) error`
- Webhook constructor: `postgres.NewWebhookEventStore(pool *pgxpool.Pool)`
- Webhook create: `CreateWebhookEvent(ctx context.Context, event app.WebhookEvent) error`
- Webhook read by ID: `WebhookEventByID(ctx context.Context, id string) (app.WebhookEvent, error)`
- Webhook idempotency lookup: `WebhookEventByIdempotencyKey(ctx context.Context, idempotencyKey string) (app.WebhookEvent, error)`
- Webhook list all: `ListWebhookEvents(ctx context.Context) ([]app.WebhookEvent, error)`
- Webhook processed marker: `UpdateWebhookEventProcessed(ctx context.Context, id string, processed bool) error`
- Report constructor: `postgres.NewReportStore(pool *pgxpool.Pool)`
- Report create/read/list/status update: `CreateReport`, `ReportByID`,
  `ListReports`, `UpdateReportStatus`
- Job constructor: `postgres.NewJobStore(pool *pgxpool.Pool)`
- Job create/read/list/update: `CreateJob`, `JobByID`, `ListRunnableJobs`,
  `UpdateJob`
- Queue compatibility create/list/delete: `CreateQueueItem`,
  `ListQueueItemsByKind`, `ListQueueItemsByStatus`,
  `DeleteQueueItemsByKindTarget`
- Mail constructor: `postgres.NewMailStore(pool *pgxpool.Pool)`
- Mail create/read/list/update: `CreateMail`, `MailByID`, `ListQueuedMail`,
  `ListRunnableMail`, `UpdateMail`
- Mail runtime queue compatibility: `QueueMail`, `QueuedMails`
- Errors: `ErrOrderNotFound`, `ErrWebhookEventNotFound`,
  `ErrWebhookEventExists`, `ErrReportNotFound`, `ErrJobNotFound`,
  `ErrMailNotFound`

### 3. Contracts

- Orders round-trip current billing lifecycle fields including nullable
  `expires_at` and `paid_at`.
- Webhook events store `metadata` as JSONB and enforce unique
  `idempotency_key`; duplicate delivery must map to `ErrWebhookEventExists`
  at repository level.
- Reports may be anonymous. Empty `app.Report.UserID` is stored as SQL NULL and
  read back as an empty string.
- Report intake must write a `support.report_created` audit log after the
  report row is created. Authenticated reporters use their user ID as actor;
  anonymous reporters use actor `anonymous`. The audit target is the report ID,
  and metadata includes `reportedTarget` and `anonymous`.
- Jobs are the durable retry boundary for workers. `ListRunnableJobs` returns
  pending jobs with `run_after <= now`, ordered by `run_after`, `created_at`,
  and `id`.
- `ListQueueItemsByStatus` returns durable job rows by status for admin
  visibility. Failed worker jobs must be visible without direct database
  access.
- Runtime scan and cleanup queue compatibility uses the `jobs` table through
  `app.QueueStore`, not in-memory queue slices, once operational stores are
  configured.
- Deleting a paste schedules a `kind = 'cleanup'`, `status = 'pending'` job.
  Admin queue responses expose pending cleanup jobs separately from historical
  cleanup failures.
- `pastebox worker` processes pending cleanup, scan, and billing reconciliation
  jobs and writes `completed`, retryable `pending`, or terminal `failed` status
  back to the same `jobs` table.
- Mails are the durable retry boundary for SMTP delivery. `ListQueuedMail`
  returns only `status = 'queued'` rows in oldest-first order. Worker delivery
  uses `ListRunnableMail`, which returns queued rows with `run_after <= now`
  ordered by `run_after`, `created_at`, and `id`.
- Runtime billing, support, worker, queue, and mail code must not treat
  in-memory maps or slices as production source of truth once these repositories
  are wired.
- `cmd/pastebox` API startup must wire PostgreSQL operational stores together
  with PostgreSQL auth/content/catalog/audit stores. Partial operational wiring
  is not production-safe because webhook idempotency, order state, reports,
  queue items, and mail retries must survive the same restart boundary.

### 4. Validation & Error Matrix

- Missing order on read/update -> `ErrOrderNotFound`.
- Duplicate webhook idempotency key -> `ErrWebhookEventExists`.
- Missing webhook event on read/update -> `ErrWebhookEventNotFound`.
- Missing report on read/update -> `ErrReportNotFound`.
- Report audit write failure -> return the audit error; do not report support
  or abuse intake success without an audit trail.
- Missing job on read/update -> `ErrJobNotFound`.
- Missing mail on read/update -> `ErrMailNotFound`.
- Unsupported worker job kind -> job attempt increments; status remains
  `pending` with backoff until max attempts, then becomes `failed`.
- Invalid webhook metadata JSON in the database -> repository read error; do
  not silently drop metadata.
- Other PostgreSQL failures -> wrapped repository error with operation context.

### 5. Good/Base/Bad Cases

- Good: A paste deletion creates a pending cleanup job; a restarted worker
  consumes it and persists completion or retry state.
- Good: Creating an abuse report stores the report and writes
  `support.report_created` before admin triage writes `admin.report_status`.
- Base: Introduce one worker job kind at a time, using the same durable jobs
  table and retry semantics.
- Bad: Process provider webhooks only in memory; replay after restart would not
  see the original idempotency key and could double-activate a plan.

### 6. Tests Required

- PostgreSQL integration tests with `PASTEBOX_TEST_DATABASE_URL` assert order
  create/read/list/update, webhook JSONB metadata and duplicate idempotency
  mapping, report create/read/status update, runnable job filtering/update, and
  queued mail filtering/update.
- Runtime switch tests must prove billing order state, webhook idempotency,
  reports, jobs, and mail queues survive process restart.
- App tests assert report creation writes `support.report_created` and admin
  triage writes `admin.report_status`.
- Runtime switch tests must also prove scan queue items are deleted from the
  durable queue when admin retry succeeds.
- Runtime switch tests must prove delete-paste cleanup jobs are durable across
  restart and admin queues distinguish `cleanupJobs`, `cleanupFailures`,
  `scanFailures`, and `failedJobs`.
- Worker tests must prove cleanup, scan, and billing reconciliation job
  completion, retry/backoff, and terminal failure for unsupported job kinds.
- Run full `make test` after changing operational repositories or runtime
  wiring.

### 7. Wrong vs Correct

#### Wrong

```go
if _, ok := s.webhookEventKeys[idempotencyKey]; ok {
    return existingEvent, order, nil
}
```

#### Correct

```go
event, err := webhookStore.WebhookEventByIdempotencyKey(ctx, idempotencyKey)
if err == nil {
    return event, order, nil
}
if !errors.Is(err, postgres.ErrWebhookEventNotFound) {
    return WebhookEvent{}, nil, err
}
```

#### Wrong

```go
// A separate worker completed this job, but the API still serves stale startup
// cache state.
return map[string]any{"cleanupJobs": s.cleanupJobs}, nil
```

#### Correct

```go
if err := s.refreshQueueCachesLocked(ctx); err != nil {
    return nil, err
}
return map[string]any{"cleanupJobs": s.cleanupJobs}, nil
```
