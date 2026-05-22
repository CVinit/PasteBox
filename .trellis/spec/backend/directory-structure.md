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
  `PASTEBOX_S3_ACCESS_KEY`, `PASTEBOX_S3_SECRET_KEY`,
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
