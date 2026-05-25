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

## Scenario: Single-Image Docker Deployment

### 1. Scope / Trigger

- Trigger: Any change to `Dockerfile`, `.dockerignore`,
  `.github/workflows/docker-image.yml`, `compose.deploy.yaml`, static asset
  serving, Vite build output assumptions, or deployment documentation.

### 2. Signatures

- Local image build: `docker build -t pastebox:local .`
- Published image: `ghcr.io/cvinit/pastebox:<tag>`
- Container command: `/usr/local/bin/pastebox`
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
- Deployment docs must state the current persistence boundary when the app still
  uses the in-memory repository.

### 4. Validation & Error Matrix

- Missing embedded static directory -> build or startup failure.
- Static asset path rewritten to `/index.html` -> browser receives HTML for JS
  or CSS asset requests.
- `/api/...` unknown route -> JSON `404 not_found`, never frontend HTML.
- Docker build fails -> do not report image deployment readiness.
- In-memory repository used for real production data -> deployment docs must
  identify it as not production-ready.

### 5. Good/Base/Bad Cases

- Good: `docker build -t pastebox:local .` succeeds and the resulting container
  serves both `/api/v1/health` and frontend routes.
- Base: `go run ./cmd/pastebox` still serves the lightweight embedded fallback
  page when Vite assets have not been copied into `internal/httpserver/static`.
- Bad: Build the Vite app after `go build`; the generated files are not embedded
  in the binary.

### 6. Tests Required

- Handler tests must cover direct static file serving and frontend route
  fallback.
- Handler tests must assert missing asset-like paths return `404` and do not
  return index HTML.
- Run `make test` after static-serving changes.
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
