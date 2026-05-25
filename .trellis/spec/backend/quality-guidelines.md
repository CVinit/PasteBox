# Quality Guidelines

> Code quality standards for backend development.

---

## Overview

Backend changes must keep the Go service buildable and testable from a clean
checkout. Local commands use project-local caches so checks do not depend on
user-level Go cache permissions.

---

## Forbidden Patterns

- Do not hard-code product plan limits in HTTP handlers.
- Do not place core business rules directly in `cmd/pastebox/main.go`.
- Do not rely on Redis as a source of truth for plans, billing, paste metadata,
  object references, or final order state.
- Do not log secrets, magic links, reset tokens, OAuth tokens, object storage
  credentials, or full user-provided paste bodies.

---

## Required Patterns

- Use `gofmt` for Go files.
- Use `log/slog` structured logging for backend process logs.
- Keep process startup in `cmd/pastebox/main.go`; keep routing in
  `internal/httpserver`.
- Use `PASTEBOX_` env vars for runtime configuration and keep defaults in
  `internal/config`.
- Keep API JSON response contracts covered by handler tests.
- Re-run aggregate quota checks on mutating update paths, not only on create or
  upload paths.
- Daily upload quota preflights must count every byte that will be recorded in
  the UTC daily upload metric, including text bytes on paste creation and
  positive text deltas on paste updates.

## Scenario: Paste Quota Enforcement On Updates

### 1. Scope / Trigger

- Trigger: Any backend change that can alter paste text, attachment membership,
  active storage bytes, or paste lifetime.

### 2. Signatures

- Service: `UpdatePaste(userID string, id string, patch PastePatch)`
- API: `PATCH /api/v1/pastes/{pasteID}`

### 3. Contracts

- `text` changes must be checked against `Plan.SingleTextBytes`.
- The resulting text plus active attachment bytes must be checked against
  `Plan.SinglePasteBytes`.
- The resulting active account storage must be checked against
  `Plan.ActiveStorageBytes`.
- Expired, deleted, or taken-down pastes remain non-writable.

### 4. Validation & Error Matrix

- text bytes exceed `SingleTextBytes` -> `text_too_large`
- resulting paste bytes exceed `SinglePasteBytes` -> `paste_too_large`
- resulting active storage exceeds `ActiveStorageBytes` -> `storage_limit`
- target paste is not active and unexpired -> `paste_expired`

### 5. Good/Base/Bad Cases

- Good: Reducing text on a paste with large attachments succeeds.
- Base: Updating title, tags, pinned, or favorite preserves quota state.
- Bad: Growing text on a paste that already has attachments cannot bypass
  single-paste or account-storage limits.

### 6. Tests Required

- Add a regression test where a paste with attachments attempts to grow text
  beyond `SinglePasteBytes`.
- Assert the specific error code and also assert an in-limit update still
  succeeds.

### 7. Wrong vs Correct

#### Wrong

Only check the new text against `SingleTextBytes` in `UpdatePaste`.

#### Correct

Calculate the post-update paste and account storage totals before mutating the
stored paste, then reject quota violations with the same error codes used by
create/upload paths.

## Scenario: Daily Upload Quota Accounting

### 1. Scope / Trigger

- Trigger: Any backend change that creates text, uploads attachments, updates
  paste text, or records `DailyUploadBytes`.

### 2. Signatures

- Service: `CreatePaste(userID string, input PasteInput)`
- Service: `AddAttachment(userID string, pasteID string, fileName string,
  contentType string, content []byte)`
- Service: `UpdatePaste(userID string, id string, patch PastePatch)`

### 3. Contracts

- `CreatePaste` records non-empty paste text bytes in the UTC daily upload
  metric and must preflight those same bytes before mutation.
- `AddAttachment` records attachment content bytes and must preflight those
  bytes before storing the object reference.
- `UpdatePaste` records only positive text growth and must preflight that
  positive delta before mutation.
- The daily window key is based on `time.Now().UTC().Format("2006-01-02")`,
  not the user's local time zone.

### 4. Validation & Error Matrix

- create text bytes exceed remaining daily upload window ->
  `403 daily_upload_limit`
- attachment bytes exceed remaining daily upload window ->
  `403 daily_upload_limit`
- positive text update delta exceeds remaining daily upload window ->
  `403 daily_upload_limit`
- reducing text or metadata-only update -> no daily upload charge

### 5. Good/Base/Bad Cases

- Good: A free user with 5 bytes remaining can create one 5-byte text paste,
  then the next 1-byte text paste is rejected.
- Base: Uploading an attachment after text creation uses the same daily metric
  and cannot exceed the remaining window.
- Bad: Checking only `extraBytes` in a shared create helper lets text-only
  paste creation exceed the daily upload quota while still recording usage.

### 6. Tests Required

- Add a domain regression for text paste creation exhausting
  `Plan.DailyUploadBytes`.
- Keep attachment and update-path quota tests in `internal/app` when those
  paths change.
- Run full `make test` after changing quota enforcement because frontend quota
  displays consume the same API fields.

### 7. Wrong vs Correct

#### Wrong

Only check attachment bytes in the create helper:

```go
if quota.DailyUploadBytes+extraBytes > plan.DailyUploadBytes {
    return E(http.StatusForbidden, "daily_upload_limit", "daily upload traffic exceeds plan limit")
}
```

#### Correct

Check the exact bytes that will be recorded by the operation:

```go
if quota.DailyUploadBytes+textBytes+extraBytes > plan.DailyUploadBytes {
    return E(http.StatusForbidden, "daily_upload_limit", "daily upload traffic exceeds plan limit")
}
```

## Scenario: MVP HTTP Surface Contracts

### 1. Scope / Trigger

- Trigger: Any backend or frontend change that adds, renames, or changes a
  `/api/v1/...` route, JSON field, cookie contract, multipart upload path, or
  download path.

### 2. Signatures

- Auth: `POST /api/v1/auth/register`, `/login`, `/logout`, `/logout-all`,
  `/magic/start`, `/magic/finish`, `/password-reset/start`,
  `/password-reset/finish`.
- Current user: `GET/PATCH /api/v1/me`, deletion request/cancel/execute, and
  `GET /api/v1/me/export`.
- Pastes: `GET/POST /api/v1/pastes`, `GET/PATCH/DELETE
  /api/v1/pastes/{pasteID}`, `POST /extend`, `POST /attachments`, and
  `POST /shares`.
- Attachments and shares: owner download at
  `/api/v1/attachments/{attachmentID}/download`; share access at
  `/api/v1/shares/{token}/access`; shared download at
  `/api/v1/shares/{token}/attachments/{attachmentID}/download`.
- Billing/admin: `/api/v1/billing/prices`, `/billing/orders`, and the
  `/api/v1/admin/...` dashboard, list, mutation, queue, audit, cleanup, and
  manual payment routes.

### 3. Contracts

- Sessions use the `pastebox_session` HttpOnly cookie. `Secure` is omitted in
  development and in plain HTTP test deployments; HTTPS requests, including
  proxied requests with `X-Forwarded-Proto: https` or `Forwarded:
  proto=https`, must set `Secure`.
- Browser state-changing API requests under `/api/v1` must use the signed
  double-submit CSRF flow. `GET /api/v1/csrf` returns `{"csrfToken": "..."}` and
  sets the HttpOnly `pastebox_csrf` cookie; unsafe methods must send the token
  in `X-CSRF-Token`. Provider webhook routes are excluded from the browser CSRF
  gate and must be protected by provider-specific signature verification.
- All API and static responses must set app-level secure browser headers:
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  `Referrer-Policy: strict-origin-when-cross-origin`, a restrictive
  `Permissions-Policy`, and a same-origin Content Security Policy. The CSP may
  allow inline styles while the React UI still uses inline width styles.
- Browser API CORS is credentialed only for exact origins in
  `PASTEBOX_CORS_ALLOWED_ORIGINS`. Production preflight must reject wildcard,
  local, HTTP, path/query/fragment, and missing-public-origin allowlist entries.
- Production rate limits must stay enabled with positive limits for auth,
  browser write, upload, download, and provider webhook buckets.
- API errors use `{"error": "<code>", "message": "<human message>"}`.
- `GET /api/v1/plans` returns `plans` and `prices`; `GET
  /api/v1/billing/prices` returns the same catalog plus provider-enabled flags
  on prices.
- API response fields consumed as arrays by the frontend must encode empty
  collections as `[]`, never `null`. This includes top-level list fields such
  as `pastes`, `shares`, `orders`, admin queue arrays, and nested paste fields
  such as `tags` and `attachments`.
- Attachments are uploaded as multipart form field `file`; download responses
  must set `Content-Type`, `Content-Disposition`, `Content-Length`, and
  `X-Content-Type-Options: nosniff`.
- The frontend API client in `web/src/api.ts` must mirror backend response
  fields with explicit TypeScript types and use `credentials: "include"`.

### 4. Validation & Error Matrix

- Missing/invalid session -> `401 unauthenticated`.
- Missing/invalid CSRF token on browser unsafe methods -> `403 csrf_required`.
- Expired, deleted, or taken-down content -> `410 paste_expired` or
  `410 attachment_unavailable`.
- Bad JSON -> `400 invalid_json`; missing upload file -> `400 missing_file`.
- Share password mismatch -> `401 invalid_share_password`; login-required
  anonymous access -> `401 login_required`.
- Non-admin access to admin routes -> `403 admin_required`.

### 5. Good/Base/Bad Cases

- Good: Register through HTTP, receive the session cookie, create a paste,
  upload a file, create a share, and access the share anonymously with the
  expected JSON shapes.
- Base: Admin login can query dashboard/list endpoints and admin mutations write
  audit logs.
- Bad: A frontend field rename or route rename compiles only if the typed client
  and backend handler tests are updated together.

### 6. Tests Required

- Handler tests must cover representative auth, paste, upload, share, quota,
  admin mutation, queue, and audit response contracts.
- Handler tests must cover session cookie `Secure` behavior when auth routes
  run behind plain HTTP test deployments and HTTPS reverse proxies.
- Handler tests must cover CSRF token issuance, required `X-CSRF-Token` on
  unsafe browser routes, and webhook-route exclusion from browser CSRF.
- Handler tests must cover secure response headers on API and static responses,
  allowed credentialed API CORS, disallowed origins receiving no CORS access
  header, and allowed preflight returning `204`.
- Handler tests must assert empty list and nested collection fields serialize as
  JSON arrays (`[]`) rather than `null`, because React call sites use array
  methods such as `.find()` and `.join()`.
- Domain-heavy quota, expiry, dedupe, scan, cleanup, and billing behavior stays
  in `internal/app` tests.
- Cross-layer changes require both `make test-api` and `make test-web`; run full
  `make test` before reporting completion.

### 7. Wrong vs Correct

#### Wrong

Add a handler route and update only the React call site, relying on manual
browser clicks to notice shape drift.

Return a nil Go slice from an API list response:

```go
var out []PasteView
writeJSON(w, http.StatusOK, map[string]any{"pastes": out})
```

#### Correct

Keep the backend handler, typed `web/src/api.ts` client, and handler contract
tests in sync in the same change, then run the backend and web checks.

Initialize empty response collections before encoding:

```go
out := []PasteView{}
writeJSON(w, http.StatusOK, map[string]any{"pastes": out})
```

## Scenario: Production HTTP Rate Limits

### 1. Scope / Trigger

- Trigger: Any backend change that adds, removes, or changes browser auth,
  browser write, upload, download, provider webhook, or production preflight
  behavior.

### 2. Signatures

- Config: `config.RateLimitConfig`
- Middleware: `(*httpserver.Server).rateLimit(next http.Handler) http.Handler`
- Preflight: `pastebox preflight production`

### 3. Contracts

- Environment keys: `PASTEBOX_RATE_LIMIT_ENABLED`,
  `PASTEBOX_RATE_LIMIT_WINDOW_SECONDS`, `PASTEBOX_RATE_LIMIT_AUTH`,
  `PASTEBOX_RATE_LIMIT_WRITE`, `PASTEBOX_RATE_LIMIT_UPLOAD`,
  `PASTEBOX_RATE_LIMIT_DOWNLOAD`, and `PASTEBOX_RATE_LIMIT_WEBHOOK`.
- Production preflight must reject disabled rate limits and all non-positive
  windows or limits.
- HTTP rate-limit responses must use status `429`, JSON
  `{"error":"rate_limited","message":"too many requests"}`, and a
  `Retry-After` header.
- Buckets must cover IP addresses, and must also cover authenticated user IDs
  when a valid session cookie is present.
- The single-VPS baseline may use process-local buckets while the API runs as a
  single replica. Multiple API replicas require Redis-backed or otherwise
  shared counters before horizontal traffic scaling.

### 4. Validation & Error Matrix

- `PASTEBOX_RATE_LIMIT_ENABLED=false` in production preflight ->
  `PASTEBOX_RATE_LIMIT_ENABLED must be true in production`
- `PASTEBOX_RATE_LIMIT_WINDOW_SECONDS <= 0` ->
  `PASTEBOX_RATE_LIMIT_WINDOW_SECONDS must be positive`
- Any per-surface limit `<= 0` -> `<ENV_KEY> must be positive`
- Exceeded HTTP bucket -> `429 rate_limited`

### 5. Good/Base/Bad Cases

- Good: The second auth, browser write, upload, download, or webhook request in
  a one-request test window returns `429` with `Retry-After`.
- Base: Health, readiness, static frontend routes, CORS preflight, and ordinary
  safe reads are not blocked by write-surface limits.
- Bad: Disabling `PASTEBOX_RATE_LIMIT_ENABLED` in production preflight passes,
  or a multi-replica deployment uses per-process counters and allows each
  replica to reset the effective limit.

### 6. Tests Required

- Config tests must parse every `PASTEBOX_RATE_LIMIT_*` env key.
- CLI preflight tests must reject disabled and non-positive production rate
  limits.
- Handler tests must assert endpoint-specific `429 rate_limited` behavior and
  `Retry-After` for auth, write, upload, download, and webhook routes.
- Handler tests must assert rate limiting can be disabled for local/dev test
  configurations.

### 7. Wrong vs Correct

#### Wrong

Only rate-limit login failures inside the auth service and leave uploads,
downloads, browser writes, and payment webhooks without HTTP-layer buckets.

#### Correct

Classify HTTP routes explicitly in middleware, apply endpoint-specific buckets,
return the standard `rate_limited` JSON contract, and make production preflight
fail if the baseline is disabled or invalid.

## Scenario: Single-Image Docker Deployment

### 1. Scope / Trigger

- Trigger: Any change to `Dockerfile`, `.dockerignore`,
  `.github/workflows/docker-image.yml`, `compose.deploy.yaml`, static asset
  serving, Vite build output assumptions, or deployment documentation.

### 2. Signatures

- Local image build: `docker build -t pastebox:local .`
- Published image: `ghcr.io/cvinit/pastebox:<tag>`
- Container command: `/usr/local/bin/pastebox api|worker|migrate up`
- Health endpoint: `GET /healthz`
- Static frontend fallback: non-API paths should serve embedded Vite assets or
  fall back to `/index.html`.

### 3. Contracts

- The Docker build must run `npm ci` and `npm run build` under `web/`.
- The Docker build must copy `web/dist/` into
  `internal/httpserver/static/` before compiling the Go binary, because Go
  `embed` captures files at compile time.
- Existing static files such as `/assets/...` and `/manifest.webmanifest` must
  be served directly; unknown frontend routes such as `/s/<token>` must fall
  back to `index.html`.
- Missing asset-like paths with file extensions, such as
  `/assets/missing.js`, must return `404` rather than `index.html`.
- The GitHub Actions workflow publishes to GHCR on `main`, version tags, and
  manual dispatch; pull requests build without pushing.
- The API image must not be documented as a standalone runnable service. Runtime
  startup requires PostgreSQL, Redis-compatible readiness/queue infrastructure,
  and an S3-compatible bucket.
- Demo Compose must run migrations and initialize the object bucket before
  starting API and worker containers.
- Production Compose remains separate from demo Compose and must keep production
  preflight, HTTPS, backup, restore, and rollback gates.

### 4. Validation & Error Matrix

- Missing embedded static directory -> build or startup failure.
- Static asset path rewritten to `/index.html` -> browser receives HTML for JS
  or CSS asset requests.
- `/api/...` unknown route -> JSON `404 not_found`, never frontend HTML.
- Docker build fails -> do not report image deployment readiness.
- API container started without migrated PostgreSQL schema -> startup or
  readiness failure.
- API container started without the configured S3 bucket -> object storage
  readiness failure.
- Demo Compose used for real production data -> deployment docs must identify it
  as demo-only and direct operators to the production runbook.

### 5. Good/Base/Bad Cases

- Good: `PASTEBOX_IMAGE=pastebox:local docker compose -f compose.deploy.yaml up
  -d` starts PostgreSQL, Redis, MinIO, migration, bucket init, API, and worker
  services.
- Base: `go run ./cmd/pastebox` uses the default local PostgreSQL, Redis, and
  MinIO settings after `make dev` and `make db-migrate`.
- Bad: Build the Vite app after `go build`; the generated files are not embedded
  in the binary.
- Bad: Document `docker run pastebox:local` as a complete runtime; it omits
  required database and object-storage dependencies.

### 6. Tests Required

- Handler tests must cover direct static file serving and frontend route
  fallback.
- Handler tests must assert missing asset-like paths return `404` and do not
  return index HTML.
- Run `make test` after static-serving changes.
- Run `docker compose -f compose.deploy.yaml config` after demo Compose changes.
- Run `docker compose -f compose.production.yaml config` after production
  Compose changes.
- Run `docker build -t pastebox:local .` after Dockerfile or workflow changes
  whenever the local Docker daemon is available.

### 7. Wrong vs Correct

#### Wrong

Compile Go before copying Vite assets:

```dockerfile
RUN go build -o /out/pastebox ./cmd/pastebox
COPY --from=web-builder /src/web/dist/ ./internal/httpserver/static/
```

#### Correct

Copy assets before the Go compile step:

```dockerfile
COPY --from=web-builder /src/web/dist/ ./internal/httpserver/static/
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/pastebox ./cmd/pastebox
```

## Scenario: Production Readiness Checks

### 1. Scope / Trigger

- Trigger: Any change to `/readyz`, `/api/v1/ready`, production service wiring,
  object storage setup, Redis config, SMTP config, or worker queue wiring.

### 2. Signatures

- Root readiness: `GET /readyz`
- API readiness: `GET /api/v1/ready`
- Handler factory:
  `httpserver.NewWithServiceAndReadiness(cfg, logger, service, checker)`
- Production entrypoint: `pastebox api`

### 3. Contracts

- Readiness responses use
  `{app, env, status, components:[{name,status,message?}]}`.
- `status = ready` returns HTTP 200 only when every component is `ok` or
  `skipped`.
- Any component status other than `ok` or `skipped` returns HTTP 503 with
  `status = not_ready`.
- Production API startup injects dependency checks for PostgreSQL, S3/object
  storage, Redis TCP reachability, worker job-table access, and SMTP TCP
  reachability when SMTP is configured.
- Development/test handlers that do not inject a checker use a default
  in-process `application: ok` component so local tests do not require external
  services.

### 4. Validation & Error Matrix

- Dependency check succeeds -> component `ok`.
- SMTP provider is not `smtp` -> mail component `skipped`.
- Dependency check fails or times out -> component `fail`, HTTP 503.
- S3 bucket cannot be checked -> object storage component `fail`, HTTP 503.

### 5. Good/Base/Bad Cases

- Good: Production `/readyz` proves database, object storage, Redis, SMTP, and
  worker queue access before Compose marks API healthy.
- Base: Unit tests can inject a failing checker and assert 503 without opening
  real sockets.
- Bad: A static `{"status":"ready"}` response while PostgreSQL or object
  storage is unreachable.

### 6. Tests Required

- Handler tests assert ready and failed readiness response shapes and status
  codes.
- Command/package tests compile the production readiness checker with the real
  PostgreSQL pool and S3 health interface.
- Run full `make test` after changing readiness contracts because deployment
  docs and Compose health checks consume them.

### 7. Wrong vs Correct

#### Wrong

```go
writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
```

#### Correct

```go
writeJSON(w, statusCode, ReadinessReport{
    Status: componentsStatus(components),
    Components: components,
})
```

## Scenario: Protected Production Metrics

### 1. Scope / Trigger

- Trigger: Any change to `/metrics`, HTTP request middleware, readiness
  reporting, admin/operational queue data, production preflight, or monitoring
  runbooks.

### 2. Signatures

- API: `GET /metrics`
- Config: `PASTEBOX_METRICS_TOKEN`
- Preflight: `pastebox preflight production`
- Service: `app.Service.OperationalMetrics()`

### 3. Contracts

- `/metrics` returns Prometheus text format only when `Authorization: Bearer
  <PASTEBOX_METRICS_TOKEN>` matches the configured token.
- Production preflight requires `PASTEBOX_METRICS_TOKEN` to be explicitly set
  and at least 32 characters long.
- Metrics must avoid user PII, secrets, magic links, reset tokens, OAuth tokens,
  webhook payloads, paste bodies, and object-storage credentials.
- HTTP request counters use route patterns, methods, and status codes; do not
  add raw URLs or user-supplied IDs as labels.
- Readiness component gauges mirror `/readyz` semantics: `ok` and `skipped`
  count as ready, every other status counts as not ready.
- Operational gauges are aggregate counts only: active pastes/storage, open
  reports, queue depths, mail backlog, webhook event count, and order counts by
  lifecycle status.

### 4. Validation & Error Matrix

- Missing or wrong bearer token -> `401 metrics_unauthorized`.
- Missing production token -> production preflight fails.
- Readiness check fails -> metrics endpoint still returns text with readiness
  gauges set to `0`.
- Operational metric loading fails -> emit `pastebox_operational_metrics_available
  0` without exposing the internal error text.

### 5. Good/Base/Bad Cases

- Good: Monitoring scrapes `/metrics` over HTTPS with the bearer token and
  alerts on readiness, failed jobs, queue lag, and mail backlog.
- Base: Development can leave the metrics token empty; `/metrics` remains
  unauthorized until a token is configured.
- Bad: Exposing `/metrics` publicly without a token or labeling HTTP metrics
  with raw share tokens, paste IDs, emails, or object keys.

### 6. Tests Required

- Handler tests must cover unauthorized and authorized `/metrics` access.
- Handler tests must assert representative Prometheus lines for readiness,
  HTTP request counters, and operational gauges.
- Command tests must cover production preflight rejecting missing or short
  metrics tokens.
- Run full `make test` after changing metrics because middleware, preflight,
  and deployment docs consume the same contract.

### 7. Wrong vs Correct

#### Wrong

```go
writeMetric("pastebox_http_requests_total", map[string]string{"path": r.URL.Path})
```

#### Correct

```go
writeMetric("pastebox_http_requests_total", map[string]string{"path": chi.RouteContext(r.Context()).RoutePattern()})
```

## Scenario: Backup Restore Drill

### 1. Scope / Trigger

- Trigger: Any change to production backup scripts, restore-drill scripts,
  Compose maintenance services, backup docs, or rollback runbooks.

### 2. Signatures

- Backup service:
  `docker compose --profile maintenance run --rm postgres-backup`
- Off-host push service:
  `docker compose --profile maintenance run --rm backup-push`
- Restore drill service:
  `docker compose --profile maintenance run --rm postgres-restore-drill`
- Script: `deploy/backup/postgres-restore-drill.sh`

### 3. Contracts

- Restore drill uses the latest `/backups/postgres/pastebox-*.sql.gz` unless
  `PASTEBOX_RESTORE_SOURCE` is set.
- The `.sha256` file must exist and pass before restore begins.
- The drill restores into `PASTEBOX_RESTORE_DRILL_DATABASE`, never directly into
  the production database.
- Drill database names are restricted to ASCII letters, digits, `_`, and `-`.
- The drill checks `schema_migrations`, drops the scratch database by default,
  and prints `duration_seconds` for RTO evidence.
- Operators may set `PASTEBOX_KEEP_RESTORE_DRILL_DB=true` only when they intend
  to inspect the scratch database manually.

### 4. Validation & Error Matrix

- No backup found -> script exits non-zero.
- Missing checksum -> script exits non-zero.
- Invalid drill database name -> script exits 2.
- Restore or schema check fails -> script exits non-zero and leaves evidence in
  command output.

### 5. Good/Base/Bad Cases

- Good: Restore the latest logical backup into a scratch DB, record duration,
  and then push backup artifacts off-host.
- Base: Drill a specific backup by setting `PASTEBOX_RESTORE_SOURCE`.
- Bad: Restore a production backup over the live database before proving it in a
  scratch database.

### 6. Tests Required

- Run `sh -n` for all backup shell scripts after editing them.
- Run full `make test` after Compose/runbook changes because deployment docs and
  preflight assumptions depend on the maintenance profile.
- A real launch gate still requires executing the restore drill against a real
  backup and recording the duration; static tests do not prove RTO.

### 7. Wrong vs Correct

#### Wrong

```sh
gunzip -c "$backup" | psql "$PGDATABASE"
```

#### Correct

```sh
sha256sum -c "$backup.sha256"
createdb "$PASTEBOX_RESTORE_DRILL_DATABASE"
gunzip -c "$backup" | psql "$PASTEBOX_RESTORE_DRILL_DATABASE"
```

## Scenario: Provider Billing Webhooks

### 1. Scope / Trigger

- Trigger: Any change that touches `/api/v1/billing/webhooks/{provider}`,
  Stripe/Epusdt billing configuration, billing order activation, or frontend
  billing/admin controls.

### 2. Signatures

- API: `POST /api/v1/billing/webhooks/stripe`
- API: `POST /api/v1/billing/webhooks/epusdt`
- Service: `app.Service.ProcessBillingWebhook(input app.BillingWebhookInput)`
- Config: `PASTEBOX_STRIPE_WEBHOOK_SECRET`,
  `PASTEBOX_EPUSDT_PID`, `PASTEBOX_EPUSDT_SECRET_KEY`
- Production preflight: `pastebox preflight production`

### 3. Contracts

- Provider webhook routes are excluded from browser CSRF because they are
  server-to-server callbacks, not browser state-changing UI requests.
- Stripe webhooks must be verified against the raw request body and
  `Stripe-Signature` header using `PASTEBOX_STRIPE_WEBHOOK_SECRET`.
- Stripe signed events map to `BillingWebhookInput` using the top-level event
  `id` as the idempotency key, event `type` as the event type, and
  `data.object.client_reference_id` or `data.object.metadata.orderId` as the
  PasteBox order id.
- Epusdt GMPay callbacks are JSON POSTs signed with merchant `secret_key`; the
  verifier keeps non-empty fields except `signature`, sorts by ASCII key order,
  joins as `key=value&...`, appends the secret, and compares lowercase MD5.
- Valid Epusdt callbacks return plain text `ok`, not JSON.
- Signed lifecycle events preserve explicit order states: successful payment
  events mark orders `paid`; failed payment events mark pending orders
  `failed`; expiry events mark pending orders `expired`; cancel events mark
  orders `canceled`; refund events mark orders `refunded`.
- Failed and expired events must not downgrade an already `paid` order.
  Canceled or refunded paid orders revoke the active user plan only when the
  user's current plan still matches the order plan; pending canceled/refunded
  orders must not revoke unrelated active plans.
- Epusdt status values must map distinctly: `success`, `succeeded`, `paid`,
  `completed`, and `1` -> `epusdt.payment.succeeded`; `expired` and `timeout`
  -> `epusdt.payment.expired`; `canceled` and `cancelled` ->
  `epusdt.payment.canceled`; `failed` -> `epusdt.payment.failed`.
- Every actual lifecycle transition writes a `billing.order_<status>` audit log
  with `planId`, `provider`, `previousStatus`, and `planRevoked` metadata.
- Browser and admin UI clients must not post synthetic provider webhooks.
  Support operations use admin manual payment controls and webhook replay
  endpoints instead.

### 4. Validation & Error Matrix

- Unknown provider -> `400 invalid_provider`.
- Missing provider webhook secret -> `503 webhook_not_configured`.
- Missing or invalid Stripe signature -> `400 invalid_webhook_signature`.
- Missing or invalid Epusdt signature -> `400 invalid_webhook_signature`.
- Epusdt `pid` mismatch -> `400 invalid_webhook`.
- Bad JSON body after signature validation -> `400 invalid_json`.
- Duplicate signed provider event idempotency key -> return the existing
  webhook event and order without double-activating the plan.
- Signed Stripe `charge.refunded` or `refund.created` for a paid order ->
  order `refunded` and active matching plan revoked.
- Signed Stripe subscription cancel/deletion or Epusdt canceled callback for a
  paid order -> order `canceled` and active matching plan revoked.
- Signed failed or expired callback for a pending order -> order `failed` or
  `expired`; same callback for an already paid order records the webhook event
  but leaves the order `paid`.

### 5. Good/Base/Bad Cases

- Good: A signed Stripe `checkout.session.completed` event marks the matching
  order paid once, and duplicate delivery returns the existing webhook event.
- Good: A signed Stripe refund/cancel callback moves a paid order to
  `refunded` or `canceled`, revokes the matching active plan, and writes an
  audit log for the transition.
- Good: A signed Epusdt `success` callback marks the matching order paid and
  responds with exactly `ok`.
- Good: A signed Epusdt `expired` callback marks a pending order `expired` and
  still responds with exactly `ok`.
- Base: Unsigned provider callbacks bypass CSRF but fail at provider signature
  validation.
- Bad: Posting old development JSON such as `eventType`, `orderId`, and
  `idempotencyKey` directly to the provider webhook route from the frontend.
- Bad: Collapsing Epusdt `expired`, `canceled`, and `failed` into generic
  `payment.failed`, because it loses lifecycle state and breaks operator
  reconciliation.

### 6. Tests Required

- HTTP tests assert CSRF exclusion reaches signature validation, not
  `csrf_required`.
- HTTP tests assert unsigned Stripe callbacks fail with
  `invalid_webhook_signature`.
- HTTP tests assert signed Stripe callbacks are idempotent and activate an
  order once.
- HTTP tests assert signed Stripe refund callbacks mark paid orders refunded.
- HTTP tests assert signed Epusdt callbacks return plain `ok` and persist the
  paid order state.
- HTTP tests assert signed Epusdt expired callbacks return plain `ok` and
  persist `expired`.
- Domain tests assert Stripe failed, refunded, canceled, and Epusdt expired
  lifecycle transitions, including paid-order plan revocation and audit logs.
- CLI preflight tests assert production env includes Stripe and Epusdt callback
  credentials.
- Frontend typecheck/build must pass after removing or changing any webhook
  client controls.

### 7. Wrong vs Correct

#### Wrong

```go
// Browser/admin clients must not fabricate provider webhook payloads.
client.processBillingWebhook("stripe", map[string]string{
    "eventType": "checkout.session.completed",
    "orderId": orderID,
})
```

#### Correct

```go
// Provider route validates raw provider callbacks; support tools use admin
// manual payment controls or replay already-recorded webhook events.
req.Header.Set("Stripe-Signature", signedHeader)
handler.ServeHTTP(res, req)
```

#### Wrong

```go
case "expired", "timeout", "canceled", "cancelled", "failed":
    return "payment.failed"
```

#### Correct

```go
case "expired", "timeout":
    return "epusdt.payment.expired"
case "canceled", "cancelled":
    return "epusdt.payment.canceled"
case "failed":
    return "epusdt.payment.failed"
```

---

## Scenario: Billing Reconciliation

### 1. Scope / Trigger

- Trigger: Any change that touches pending order expiry, stuck provider
  payments, admin billing correction controls, or worker billing jobs.

### 2. Signatures

- Service: `app.Service.RunBillingReconciliation(actorID string)`
- API: `POST /api/v1/admin/billing/reconcile`
- Worker job kind: `billing_reconcile`

### 3. Contracts

- Reconciliation checks all cached/persisted orders and returns stable numeric
  counts: `checkedOrders`, `pendingOrders`, and `expiredOrders`.
- Pending orders with `expiresAt <= now` move to `expired`.
- Paid, failed, canceled, refunded, and non-expired pending orders must not be
  changed by reconciliation.
- Admin-triggered reconciliation requires an admin session. Worker-triggered
  reconciliation passes an empty actor and audits as `system:billing_reconcile`.
- Every order expired by reconciliation writes `billing.order_expired` audit
  metadata with `source = billing_reconcile`, `previousStatus`, `provider`,
  `planId`, and `planRevoked = false`.

### 4. Validation & Error Matrix

- Non-admin API caller -> `403 admin_required`.
- No stale pending orders -> `200` with `expiredOrders = 0`.
- Stale pending order -> `200`, order status `expired`, count incremented.
- Unsupported worker job kind -> normal worker retry/failure policy; do not
  silently treat it as reconciliation.

### 5. Good/Base/Bad Cases

- Good: A stuck Epusdt order whose checkout window elapsed is expired by the
  admin reconcile endpoint or worker job.
- Base: A fresh pending order is counted as pending but remains pending.
- Bad: Expire paid orders only because their original checkout `expiresAt` is
  in the past.

### 6. Tests Required

- Domain tests assert stale pending orders expire, fresh pending orders remain
  pending, paid orders remain paid, non-admin callers are rejected, and audit
  logs are written.
- HTTP tests assert the admin reconciliation route returns numeric counts.
- Worker tests assert `billing_reconcile` jobs call the service and complete
  through the same retry/completion path as cleanup and scan jobs.
- Run full `make test` after changing reconciliation because it spans service,
  HTTP, worker, and frontend admin controls.

### 7. Wrong vs Correct

#### Wrong

```go
if order.ExpiresAt != nil && !order.ExpiresAt.After(now) {
    order.Status = "expired"
}
```

#### Correct

```go
if order.Status == "pending" && order.ExpiresAt != nil && !order.ExpiresAt.After(now) {
    expireOrderWithAudit(order)
}
```

---

## Scenario: Public Support Contact Contract

### 1. Scope / Trigger

- Trigger: Any change that touches public support, abuse, DMCA, refund,
  privacy, DPA, GDPR/data-subject, or status/legal launch surfaces.

### 2. Signatures

- Config: `config.Config.SupportEmail` and `config.Config.AbuseEmail`
- Env keys: `PASTEBOX_SUPPORT_EMAIL` and `PASTEBOX_ABUSE_EMAIL`
- Preflight command: `pastebox preflight production`
- API: `GET /api/v1/support/contacts`
- Response type: `httpserver.PublicSupportContacts`
- Frontend type: `web/src/api.ts` `SupportContacts`
- UI surface: `/support` public page in `web/src/App.tsx`

### 3. Contracts

- `GET /api/v1/support/contacts` is public, safe, and returns
  `{"supportEmail":"...","abuseEmail":"..."}` from runtime config.
- `PASTEBOX_SUPPORT_EMAIL` is the monitored intake address for account,
  billing, privacy, DPA, and GDPR/data-subject requests.
- `PASTEBOX_ABUSE_EMAIL` is the monitored intake address for abuse, malware,
  DMCA, and urgent takedown requests.
- Production preflight must require both env keys to be explicit plain email
  addresses on production domains.
- The `/support` page must render both addresses as `mailto:` links using the
  typed frontend API client, not hard-coded copy.
- `deploy/production.env.example`, `.env.example`, production runbooks, and the
  support operations runbook must stay in sync with these env keys.

### 4. Validation & Error Matrix

- Missing contact env key -> production preflight exits `1` and lists the key.
- `CHANGE_ME` placeholder -> production preflight exits `1`.
- Invalid email syntax -> production preflight exits `1`.
- Display-name address such as `Support <support@example.com>` -> production
  preflight exits `1`; only the plain address is accepted.
- Local or non-production domain such as `support@localhost` -> production
  preflight exits `1`.
- API request succeeds -> `200` with both configured address fields.

### 5. Good/Base/Bad Cases

- Good: `support@pastebox.example.com` and `abuse@pastebox.example.com` pass
  preflight, are returned by the API, and render on `/support`.
- Base: Development defaults may use local addresses, but production preflight
  must reject them.
- Bad: Legal pages tell users to "contact support" without a reachable address,
  or the frontend hard-codes a stale support inbox.

### 6. Tests Required

- Config tests assert both env keys parse into `config.Config`.
- CLI tests assert production preflight passes with real public addresses and
  rejects invalid, display-name, local, missing, and placeholder values.
- Handler tests assert `GET /api/v1/support/contacts` returns the configured
  values.
- Frontend changes must pass `make test-web`; cross-layer changes must pass
  full `make test`.

### 7. Wrong vs Correct

#### Wrong

```tsx
<p>Contact support for billing, privacy, or abuse requests.</p>
```

#### Correct

```tsx
const contacts = await client.supportContacts();
<a href={`mailto:${contacts.supportEmail}`}>{contacts.supportEmail}</a>
```

---

## Testing Requirements

- Run `make test` before reporting completion.
- Backend-only verification: `make test-api`.
- Add unit tests for new config parsing, domain calculations, quota limits, and
  handler response contracts.
- For cross-layer API response changes, run both `make test-api` and
  `make test-web`.

---

## Code Review Checklist

- Does the change preserve the `/api/v1/...` API namespace?
- Are product-facing names `PasteBox` and code/module names `pastebox`?
- Are new env keys documented in `.env.example` and the relevant spec?
- Are limits/config values sourced from a domain package or config path instead
  of being duplicated in transport code?
- Did `make test` pass with project-local caches?
