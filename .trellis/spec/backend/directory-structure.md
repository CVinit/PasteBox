# Directory Structure

> How backend code is organized in this project.

---

## Overview

The backend is a Go modular monolith. The module name is `pastebox`, and the
runtime binary is `pastebox`.

HTTP entrypoints stay thin: routing and JSON response handling live under
`internal/httpserver`; product logic belongs in focused `internal/<domain>`
packages such as `plans`, `auth`, `pastes`, `attachments`, `shares`, `quota`,
`billing`, `scanner`, and `cleanup`.

---

## Directory Layout

```
cmd/pastebox/
├── main.go              # process startup, logger, graceful shutdown
internal/
├── config/              # env-driven runtime config
├── httpserver/          # Chi router, API handlers, static fallback
│   └── static/          # embedded API fallback page
└── plans/               # membership plan catalog and limits
```

---

## Module Organization

Create one `internal/<domain>` package per bounded product area. Keep transport
concerns in `internal/httpserver`; do not put database, quota, billing, or file
storage decisions directly in HTTP handlers once those packages exist.

Domain packages should expose explicit Go types and service functions. Handlers
should compose those packages and translate errors into HTTP responses.

---

## Naming Conventions

- Repository, Go module, package names, and binary names use lower-case
  `pastebox`.
- Product-facing text uses `PasteBox`.
- Environment variables use the `PASTEBOX_` prefix.
- API paths live under `/api/v1/...` unless the PRD explicitly promotes a
  stable public API.

---

## Scenario: Initial API and Runtime Skeleton

### 1. Scope / Trigger

- Trigger: the first executable backend skeleton introduced process startup,
  environment configuration, API responses, and the first cross-layer
  membership plan contract consumed by the frontend.

### 2. Signatures

- Command: `go run ./cmd/pastebox`
- Build: `go build -o bin/pastebox ./cmd/pastebox`
- Health endpoint: `GET /healthz`
- API health endpoint: `GET /api/v1/health`
- Plan catalog endpoint: `GET /api/v1/plans`

### 3. Contracts

- `GET /healthz` returns JSON: `{"status":"ok"}`.
- `GET /api/v1/health` returns JSON with `app`, `env`, and `status`.
- `GET /api/v1/plans` returns `{ "plans": Plan[] }`.
- `Plan` fields are camelCase JSON fields:
  `id`, `name`, `activePasteLimit`, `activeStorageBytes`,
  `singleTextBytes`, `singleFileBytes`, `singlePasteBytes`,
  `attachmentsPerPasteLimit`, `maxRetentionSeconds`, `dailyUploadBytes`,
  `dailyShareDownloadBytes`.
- Runtime env keys: `PASTEBOX_APP_NAME`, `PASTEBOX_APP_ENV`,
  `PASTEBOX_HTTP_ADDR`, `PASTEBOX_PUBLIC_URL`, `PASTEBOX_DATABASE_URL`,
  `PASTEBOX_REDIS_ADDR`, `PASTEBOX_S3_ENDPOINT`, `PASTEBOX_S3_BUCKET`,
  `PASTEBOX_S3_REGION`, `PASTEBOX_S3_ACCESS_KEY`, `PASTEBOX_S3_SECRET_KEY`,
  `PASTEBOX_S3_USE_PATH_STYLE`, `PASTEBOX_MAILER_PROVIDER`,
  `PASTEBOX_STRIPE_ENABLED`, `PASTEBOX_EPUSDT_ENABLED`.

### 4. Validation & Error Matrix

- Unknown `/api/...` route -> HTTP 404 JSON `{ "error": "not_found" }`.
- Missing env key -> use the development-safe default from
  `internal/config`.
- Invalid boolean env key -> use the configured fallback rather than failing
  process startup.
- Invalid log level env key -> use `info`.

### 5. Good/Base/Bad Cases

- Good: adding a new API route under `/api/v1` with a typed response and a
  handler test.
- Base: adding a new domain package under `internal/<domain>` and wiring it
  through `internal/httpserver`.
- Bad: hard-coding plan limits in a handler or duplicating response field names
  only in the frontend.

### 6. Tests Required

- Config defaults and env parsing tests in `internal/config`.
- Handler response tests for each public API route in `internal/httpserver`.
- Domain package tests for catalog, quota, or billing constants before they are
  exposed through API handlers.

### 7. Wrong vs Correct

#### Wrong

```go
func (s *Server) planCatalog(w http.ResponseWriter, _ *http.Request) {
    writeJSON(w, http.StatusOK, map[string]any{"freeLimit": 20})
}
```

#### Correct

```go
func (s *Server) planCatalog(w http.ResponseWriter, _ *http.Request) {
    writeJSON(w, http.StatusOK, plans.DefaultCatalog())
}
```

---

## Examples

- `internal/config`: env parsing stays isolated and tested.
- `internal/plans`: PRD membership limits live in a domain package, not in the
  handler or frontend only.

## Scenario: Production Deployment Lifecycle Baseline

### 1. Scope / Trigger

- Trigger: Any backend or deployment change that affects production process
  startup, readiness checks, migration commands, worker supervision, pinned
  image rollout, or production preflight.

### 2. Signatures

- API command: `pastebox` or `pastebox api`
- Worker command: `pastebox worker`
- Migration commands: `pastebox migrate status` and `pastebox migrate up`
- Preflight command: `pastebox preflight production`
- Liveness endpoint: `GET /healthz`
- Readiness endpoint: `GET /readyz`
- API liveness endpoint: `GET /api/v1/health`
- API readiness endpoint: `GET /api/v1/ready`
- Production Compose file: `compose.production.yaml`
- Production env template: `deploy/production.env.example`

### 3. Contracts

- `pastebox api` and bare `pastebox` start the HTTP server.
- `pastebox worker` is a supervised long-running process. Until durable queues
  are implemented, it may idle, but it must not pretend to process production
  jobs.
- `pastebox migrate status` connects to `PASTEBOX_DATABASE_URL` and reports
  every embedded SQL migration as `pending`, `applied`, or `dirty`.
- `pastebox migrate up` applies embedded PostgreSQL migrations transactionally
  and records version, name, and checksum in `schema_migrations`.
- `pastebox preflight production` must require explicit production env vars,
  reject `CHANGE_ME` placeholder values, require `PASTEBOX_PUBLIC_URL` to use
  `https://`, reject `PASTEBOX_IMAGE=:latest`, require
  `PASTEBOX_S3_ENDPOINT` to be a non-local `https://` managed object-storage
  endpoint, and require `PASTEBOX_RESTIC_REPOSITORY` to use an off-host
  `s3:https://` repository.
- `GET /readyz` returns `{"status":"ready"}` once the process is ready for
  traffic.
- `GET /api/v1/ready` returns `app`, `env`, and `status`.
- Production deployment uses `compose.production.yaml`, a non-committed
  `deploy/production.env`, and a pinned `PASTEBOX_IMAGE` tag or digest.
- The committed env template may contain placeholders; the real production env
  file must not be committed.

### 4. Validation & Error Matrix

- Missing required production env -> preflight exits 1 and lists missing keys.
- Placeholder `CHANGE_ME` remains -> preflight exits 1 and lists affected keys.
- `PASTEBOX_PUBLIC_URL` uses HTTP -> preflight exits 1.
- `PASTEBOX_IMAGE` is missing or `:latest` -> preflight exits 1.
- `PASTEBOX_S3_ENDPOINT` is HTTP, invalid, or local -> preflight exits 1.
- `PASTEBOX_RESTIC_REPOSITORY` is local, invalid, or not `s3:https://` ->
  preflight exits 1.
- `pastebox migrate status` or `up` cannot connect to PostgreSQL -> exits 1
  with a command-specific error.
- Stored migration checksum differs from the embedded SQL file -> exits 1 and
  reports a dirty/checksum mismatch.
- Unknown lifecycle subcommand -> exits 2 and prints usage.

### 5. Good/Base/Bad Cases

- Good: Adding a new production dependency updates `deploy/production.env.example`,
  `pastebox preflight production`, Compose wiring, runbooks, and tests together.
- Base: Readiness endpoints stay simple while the app still uses in-memory
  adapters; later phases can expand readiness to check PostgreSQL, Redis,
  object storage, mail, and worker health.
- Bad: Editing an already-applied migration, silently ignoring checksum drift,
  or using `latest` in the production runbook.

### 6. Tests Required

- Command tests for migration connection errors, production preflight success,
  missing env, placeholder rejection, HTTPS enforcement, image pinning,
  managed object-storage enforcement, and off-host backup repository
  enforcement.
- Migration package tests for embedded migration ordering, checksums, and table
  coverage.
- Handler tests for `/readyz` and `/api/v1/ready` response shape.
- `docker compose --env-file deploy/production.env.example -f
  compose.production.yaml config` must render successfully with
  `PASTEBOX_ENV_FILE=./deploy/production.env.example`.
- Run full `make test` after changing production lifecycle commands or HTTP
  readiness endpoints.

### 7. Wrong vs Correct

#### Wrong

```sh
PASTEBOX_IMAGE=ghcr.io/cvinit/pastebox:latest
pastebox migrate up
# deploys a mutable image and risks schema drift
```

#### Correct

```sh
PASTEBOX_IMAGE=ghcr.io/cvinit/pastebox:sha-abc123
pastebox preflight production
pastebox migrate up
# applies embedded SQL migrations or fails loudly
```
