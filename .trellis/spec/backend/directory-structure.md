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
- Worker command: `pastebox worker [--once] [--batch-size <n>] [--poll-interval <duration>]`
- Compose one-shot worker check:
  `docker compose --env-file deploy/production.env -f compose.production.yaml run --rm worker worker --once`
- Migration commands: `pastebox migrate status` and `pastebox migrate up`
- Preflight command: `pastebox preflight production`
- Scanner constructor: `scanner.New(config.ScannerConfig) (scanner.Scanner, error)`
- Liveness endpoint: `GET /healthz`
- Readiness endpoint: `GET /readyz`
- API liveness endpoint: `GET /api/v1/health`
- API readiness endpoint: `GET /api/v1/ready`
- Production Compose file: `compose.production.yaml`
- Production env template: `deploy/production.env.example`
- Production monitoring files: `deploy/monitoring/prometheus.yml` and
  `deploy/monitoring/pastebox-alerts.yml`

### 3. Contracts

- `pastebox api` and bare `pastebox` start the HTTP server.
- `pastebox worker` is a supervised long-running process that polls the
  PostgreSQL-backed `jobs` table. `--once` processes one runnable batch and
  exits for deployment checks or maintenance.
- Production Compose uses `pastebox` as the image entrypoint and `worker` as
  the service command. When using `docker compose run` for one-shot checks,
  run arguments replace the service command, so the command must include both
  the service name and the application subcommand: `run --rm worker worker
  --once`. `run --rm worker --once` invokes `pastebox --once` and fails with
  `unknown command "--once"`.
- The worker currently handles `kind = 'cleanup'` jobs by calling the same
  service cleanup path used by admin cleanup, then marking the job
  `completed`, `pending` for retry with backoff, or `failed` after the retry
  budget is exhausted.
- The worker currently handles `kind = 'scan'` jobs by calling
  `Service.RunAttachmentScan` with the configured scanner. Scanner results must
  be one of `clean`, `scan_failed`, or `malicious`; invalid scanner verdicts are
  treated as scan failures and leave the job retryable through the worker retry
  policy.
- The worker currently handles `kind = 'billing_reconcile'` jobs by calling
  `Service.RunBillingReconciliation("")`, allowing stale pending billing orders
  to expire through the same retry/completion path as cleanup and scan jobs.
- Development may use `PASTEBOX_SCANNER_PROVIDER=heuristic`; production
  preflight must require `PASTEBOX_SCANNER_PROVIDER=clamav`,
  `PASTEBOX_CLAMAV_ADDR` as a valid `host:port`, and a positive
  `PASTEBOX_CLAMAV_TIMEOUT_SECONDS`.
- `pastebox migrate status` connects to `PASTEBOX_DATABASE_URL` and reports
  every embedded SQL migration as `pending`, `applied`, or `dirty`.
- `pastebox migrate up` applies embedded PostgreSQL migrations transactionally
  and records version, name, and checksum in `schema_migrations`.
- `pastebox preflight production` must require explicit production env vars,
  reject `CHANGE_ME` placeholder values, require `PASTEBOX_PUBLIC_URL` to use
  `https://`, reject `PASTEBOX_IMAGE=:latest`, require
  `PASTEBOX_S3_ENDPOINT` to be a non-local `https://` managed object-storage
  endpoint, require `PASTEBOX_RESTIC_REPOSITORY` to use an off-host
  `s3:https://` repository, and reject non-ClamAV production scanner settings.
- `GET /readyz` returns `{"status":"ready"}` once the process is ready for
  traffic.
- `GET /api/v1/ready` returns `app`, `env`, and `status`.
- Production deployment uses `compose.production.yaml`, a non-committed
  `deploy/production.env`, and a pinned `PASTEBOX_IMAGE` tag or digest.
- The optional `monitoring` profile runs Prometheus with committed scrape and
  alert-rule files. It must source the metrics bearer token from the
  `PASTEBOX_METRICS_TOKEN` Compose secret, not from committed YAML.
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
- `PASTEBOX_SCANNER_PROVIDER` is not `clamav` in production -> preflight exits
  1.
- `PASTEBOX_CLAMAV_ADDR` is missing or not `host:port` when provider is
  `clamav` -> preflight exits 1.
- `PASTEBOX_CLAMAV_TIMEOUT_SECONDS` is zero or negative -> preflight exits 1.
- `pastebox migrate status` or `up` cannot connect to PostgreSQL -> exits 1
  with a command-specific error.
- Stored migration checksum differs from the embedded SQL file -> exits 1 and
  reports a dirty/checksum mismatch.
- `pastebox worker --once` cannot list runnable jobs or update job state ->
  exits 1 with a worker-specific error.
- `docker compose run --rm worker --once` -> exits 2 with
  `unknown command "--once"` because it omits the application `worker`
  subcommand.
- Production monitoring profile fails to render -> config validation failure.
- Prometheus scrape or alert-rule config is syntactically invalid -> launch
  monitoring validation failure.
- `pastebox worker --help` exits 0 and prints worker usage.
- Unknown lifecycle subcommand -> exits 2 and prints usage.

### 5. Good/Base/Bad Cases

- Good: Adding a new production dependency updates `deploy/production.env.example`,
  `pastebox preflight production`, Compose wiring, runbooks, and tests together.
- Good: Adding a metrics alert updates `deploy/monitoring/pastebox-alerts.yml`,
  the deployment runbook, and the metrics spec together.
- Base: Worker support may start with one job kind, but it must use the durable
  `jobs` table and preserve retry state across process restarts.
- Bad: Editing an already-applied migration, silently ignoring checksum drift,
  using `latest` in the production runbook, or leaving `pastebox worker` as an
  idle process once runnable jobs exist.

### 6. Tests Required

- Command tests for migration connection errors, production preflight success,
  missing env, placeholder rejection, HTTPS enforcement, image pinning,
  managed object-storage enforcement, and off-host backup repository
  enforcement.
- Migration package tests for embedded migration ordering, checksums, and table
  coverage.
- Handler tests for `/readyz` and `/api/v1/ready` response shape.
- Worker runner tests for successful cleanup completion, successful scan
  completion, missing scanner retry/backoff, and unsupported job failure.
- Scanner package tests for provider validation, ClamAV timeout defaults,
  heuristic verdicts, ClamAV response parsing, and risk normalization.
- `docker compose --env-file deploy/production.env.example -f
  compose.production.yaml config` must render successfully with
  `PASTEBOX_ENV_FILE=./deploy/production.env.example`.
- `docker compose --env-file deploy/production.env.example -f
  compose.production.yaml --profile monitoring config` must render
  successfully with `PASTEBOX_ENV_FILE=./deploy/production.env.example`.
- Validate Prometheus syntax for `deploy/monitoring/prometheus.yml` and
  `deploy/monitoring/pastebox-alerts.yml` after alert or scrape changes.
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
