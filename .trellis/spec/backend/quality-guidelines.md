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
  credentials, share tokens, frontend token route values, or full
  user-provided paste bodies.

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
- Production preflight must reject reserved/documentation-only hostnames such
  as `example.com`, `.example.com`, `.test`, `.invalid`, localhost, IP
  literals, and single-label internal names for public URLs, CORS origins,
  contact emails, SMTP sender/host, OAuth redirects, object storage, backup
  repositories, and payment checkout templates.
- HTTP request logs and Prometheus route labels must use sanitized route
  patterns, not raw URL paths.

## Scenario: Sanitized HTTP Observability Paths

### 1. Scope / Trigger

- Trigger: Any backend change that touches request logging, HTTP metrics,
  routing, frontend fallback behavior, share URLs, auth token URLs, or any path
  containing user-controlled IDs or secrets.

### 2. Signatures

- Middleware: `Server.logRequests(next http.Handler) http.Handler`
- Helper: `requestRoutePath(r *http.Request) string`
- Metrics recorder: `Server.recordHTTPRequest(method string, routePath string, status int)`
- Metrics output: `pastebox_http_requests_total{method,path,status}`

### 3. Contracts

- Known chi routes must log and emit the chi route pattern, for example
  `/api/v1/shares/{token}/access`, not the concrete token value.
- Unknown API paths must collapse to `/api/{unmatched}`.
- Frontend SPA deep links without file extensions must collapse to
  `/{frontend}`.
- Static asset paths or unknown dotted paths must collapse to `/{asset}`.
- Logs may include HTTP method, sanitized path, status, byte count, duration,
  and request ID. They must not include query strings, request bodies, cookies,
  bearer tokens, CSRF tokens, share tokens, magic/reset/verification tokens, or
  full user-provided paste content.

### 4. Validation & Error Matrix

- Raw share token appears in request log or metric label -> security bug.
- Raw frontend token route such as `/s/<token>` appears in request log or
  metric label -> security bug.
- Unknown API path appears verbatim in request log or metric label -> security
  bug; use `/api/{unmatched}`.
- Static missing asset path appears with the exact filename -> acceptable only
  when it is collapsed to `/{asset}`.

### 5. Good/Base/Bad Cases

- Good: `POST /api/v1/shares/secret-token/access` logs
  `/api/v1/shares/{token}/access` and metrics use the same pattern.
- Base: `GET /legal/privacy` logs `/{frontend}` because the SPA route is served
  through fallback.
- Bad: `GET /s/dev-token` logs `/s/dev-token` or creates a metric label with
  that value.

### 6. Tests Required

- Add or keep handler tests that send token-like frontend and API paths, then
  assert request logs and `/metrics` output do not contain the concrete token
  values.
- Assert expected sanitized path labels are present for both logs and metrics.
- Run `go test ./internal/httpserver` after changing route logging or HTTP
  metric label behavior.

### 7. Wrong vs Correct

#### Wrong

```go
s.logger.Info("http request", "path", r.URL.Path)
s.httpRequests[httpMetricKey{Method: r.Method, Path: r.URL.Path, Status: status}]++
```

#### Correct

```go
routePath := requestRoutePath(r)
s.logger.Info("http request", "path", routePath)
s.recordHTTPRequest(r.Method, routePath, status)
```

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
- Share access grant cookie: `pastebox_share_access`, scoped to
  `/api/v1/shares/{token}/attachments`, HttpOnly, SameSite=Lax, and signed
  against the share token plus grant payload.
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
  such as `tags` and `attachments`. Admin provider status arrays such as
  `requiredEnv` and `missingEnv` must follow the same contract because the
  admin UI renders them with array methods.
- Attachments are uploaded as multipart form field `file`; download responses
  must set `Content-Type`, `Content-Disposition`, `Content-Length`, and
  `X-Content-Type-Options: nosniff`.
- Shared attachment downloads must not accept share passwords in URL query
  strings. A successful `POST /api/v1/shares/{token}/access` sets a short-lived
  signed `pastebox_share_access` cookie; the follow-up shared attachment
  `GET` uses that cookie and a clean URL. Login-required share grants must bind
  to the authenticated viewer; anonymous password-only grants may omit
  `viewerId`.
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
- Missing, expired, wrong-token, wrong-viewer, or tampered shared download
  grant cookie -> `401 share_access_required`.
- `GET /api/v1/shares/{token}/attachments/{attachmentID}/download?password=...`
  -> `401 share_access_required`; the query password is ignored.
- Non-admin access to admin routes -> `403 admin_required`.

### 5. Good/Base/Bad Cases

- Good: Register through HTTP, receive the session cookie, create a paste,
  upload a file, create a share, and access the share anonymously with the
  expected JSON shapes.
- Good: After successful share access, the browser receives an HttpOnly scoped
  grant cookie and downloads a clean shared attachment URL without query
  secrets.
- Base: Admin login can query dashboard/list endpoints and admin mutations write
  audit logs.
- Bad: A frontend field rename or route rename compiles only if the typed client
  and backend handler tests are updated together.
- Bad: Putting `password=<share password>` in a shared attachment download URL,
  because URLs leak through logs, history, referrers, and diagnostics.

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
- Handler tests must assert shared attachment downloads reject password query
  strings, set a scoped HttpOnly `pastebox_share_access` cookie on successful
  share access, and then allow clean shared download URLs.
- Domain-heavy quota, expiry, dedupe, scan, cleanup, and billing behavior stays
  in `internal/app` tests.
- Cross-layer changes require both `make test-api` and `make test-web`; run full
  `make test` before reporting completion.

### 7. Wrong vs Correct

#### Wrong

Add a handler route and update only the React call site, relying on manual
browser clicks to notice shape drift.

Put share passwords in shared attachment URLs:

```go
password := r.URL.Query().Get("password")
download, err := s.app.OpenSharedAttachment(token, password, attachmentID, viewerID)
```

Return a nil Go slice from an API list response:

```go
var out []PasteView
writeJSON(w, http.StatusOK, map[string]any{"pastes": out})
```

#### Correct

Keep the backend handler, typed `web/src/api.ts` client, and handler contract
tests in sync in the same change, then run the backend and web checks.

Issue a signed, scoped access grant on share access and require it for the
download:

```go
s.setShareAccessCookie(w, r, token, viewerID)
download, err := s.app.OpenSharedAttachmentWithAccessGrant(token, attachmentID, viewerID)
```

Initialize empty response collections before encoding:

```go
out := []PasteView{}
writeJSON(w, http.StatusOK, map[string]any{"pastes": out})
```

## Scenario: Plan-Scoped Paste Tag Limits

### 1. Scope / Trigger

- Trigger: Any backend or frontend change that touches paste `tags`, plan
  catalog fields, `/api/v1/plans`, `GET /api/v1/pastes?tag=...`, paste
  create/update handlers, or PostgreSQL `plans`/`pastes` metadata.

### 2. Signatures

- Plan field: `plans.Plan.TagsPerPasteLimit int` serialized as
  `tagsPerPasteLimit`.
- Database column: `plans.tags_per_paste_limit integer NOT NULL DEFAULT 0`.
- Service create/update: `CreatePaste(userID string, input PasteInput)` and
  `UpdatePaste(userID string, id string, patch PastePatch)`.
- Owner list filter: `ListPastes(userID string, opts ListOptions)` with
  `ListOptions.Tag`, exposed as `GET /api/v1/pastes?tag=<tag>`.

### 3. Contracts

- Default launch catalog tag limits are `free=0`, `plus=5`, and `pro=20`, all
  counted per paste/content item, not per account.
- Tags must be normalized once through the service convention: comma-split,
  trim, lower-case, de-duplicate, and sort before storage or comparison.
- Create and tag-changing update paths must reject normalized tag counts above
  the current plan's `TagsPerPasteLimit`.
- Existing tags remain stored, listed, and searchable after plan expiry or
  downgrade. If the existing tag set exceeds the current plan limit, tag edits
  are read-only, but non-tag paste edits may still succeed.
- `/api/v1/plans` and `/api/v1/billing/prices` must expose
  `tagsPerPasteLimit` so the frontend does not duplicate plan limits.

### 4. Validation & Error Matrix

- Free user creates or updates a paste with any tag -> `403 tag_limit`.
- Plus user saves more than 5 tags -> `403 tag_limit`.
- Pro user saves more than 20 tags -> `403 tag_limit`.
- Downgraded user keeps unchanged over-limit tags -> allowed.
- Downgraded user changes an over-limit tag set -> `403 tag_limit`.
- `GET /api/v1/pastes?tag=<existing>` must still find downgraded pastes with
  retained tags.

### 5. Good/Base/Bad Cases

- Good: A Pro paste with 20 tags remains searchable after the user is moved to
  Plus, while attempts to replace those tags are rejected.
- Base: A Plus user can create and edit a paste with up to 5 normalized tags.
- Bad: The frontend hard-codes `plus=5` and `pro=20` while the backend catalog
  becomes configurable through admin catalog editing.

### 6. Tests Required

- `internal/plans` tests assert the default catalog tag limits.
- `internal/app` tests cover create/update `tag_limit` failures and downgraded
  read-only-but-searchable retained tags.
- Handler tests assert plan catalog responses include `tagsPerPasteLimit`.
- PostgreSQL migration/catalog tests assert `plans.tags_per_paste_limit` is
  added and read/written through `CatalogStore`.
- Run full `make test` because this contract spans database, service, HTTP,
  typed frontend API fields, and UI behavior.

### 7. Wrong vs Correct

#### Wrong

```go
paste.Tags = normalizeTags(input.Tags)
return s.createPasteLocked(paste)
```

#### Correct

```go
tags := normalizeTags(input.Tags)
if err := ensureTagsWithinPlan(plan, paste.Tags, tags); err != nil {
    return PasteView{}, err
}
paste.Tags = tags
```

## Scenario: S3-Compatible Attachment Streaming

### 1. Scope / Trigger

- Trigger: Any backend change that touches attachment upload handlers,
  attachment download handlers, object-storage adapters, S3-compatible gateway
  configuration, or attachment byte transfer between HTTP, service, and object
  storage.

### 2. Signatures

- Upload APIs:
  `POST /api/v1/pastes/{pasteID}/attachments` and
  `POST /api/v1/guest/pastes/{pasteID}/attachments`.
- Download APIs:
  `GET /api/v1/attachments/{attachmentID}/download` and
  `GET /api/v1/shares/{token}/attachments/{attachmentID}/download`.
- App helper:
  `PrepareAttachmentUpload(fileName, contentType string, body io.Reader)`.
- Service upload methods:
  `AddPreparedAttachment(userID, pasteID string, upload *PreparedAttachmentUpload)`
  and `AddPreparedGuestAttachment(token, pasteID string, upload *PreparedAttachmentUpload, turnstileToken, remoteIP string)`.
- Service download methods:
  `OpenAttachment(userID, attachmentID string)` and
  `OpenSharedAttachment(token, password, attachmentID, viewerUserID string)`.
- Optional object-store extension:
  `StreamingObjectStore.PutObjectStream` and `StreamingObjectStore.OpenObject`.
- S3 env keys:
  `PASTEBOX_S3_ENDPOINT`, `PASTEBOX_S3_BUCKET`, `PASTEBOX_S3_REGION`,
  `PASTEBOX_S3_ACCESS_KEY`, `PASTEBOX_S3_SECRET_KEY`,
  `PASTEBOX_S3_USE_PATH_STYLE`.

### 3. Contracts

- HTTP attachment upload handlers must read multipart bodies with
  `MultipartReader` or an equivalent streaming parser. They must not use
  `ParseMultipartForm`, `FormFile`, or file-part `io.ReadAll` as the main
  upload path.
- Upload preparation may spool to a temporary file to calculate size, SHA-256,
  content type, and image dimensions before storage. The temporary file must be
  closed and removed after the service call.
- S3-compatible stores should implement `StreamingObjectStore`. When available,
  service upload uses `PutObjectStream` and service download uses `OpenObject`.
- Legacy `[]byte` object-store methods may remain for unit tests, scanner
  paths, and compatibility, but HTTP upload/download must use the prepared
  upload and stream download methods.
- HTTP handlers must pass `r.Context()` into prepared attachment service calls
  so client disconnects and request cancellations can stop object-store writes.
- Service upload paths must not hold the global service mutex while streaming
  bytes to S3-compatible storage. Do preflight validation under the service
  mutex, write the object outside it, then reacquire the mutex and revalidate
  before creating attachment metadata.
- Object writes and metadata commits must remain atomic for rollback purposes:
  if uploads can happen concurrently, protect the object write plus metadata
  commit/rollback with a dedicated object-write guard so a failed upload cannot
  delete another in-flight upload of the same content-addressed object key.
- Download responses must preserve the existing attachment response headers:
  `Content-Type`, `Content-Disposition`, `Content-Length` when known, and
  `X-Content-Type-Options: nosniff`.
- s3-orchestrator and similar path-style gateways must be configured with the
  virtual bucket name, its credentials, a valid region such as `us-east-1`, and
  `PASTEBOX_S3_USE_PATH_STYLE=true`.
- Production preflight still requires `PASTEBOX_S3_ENDPOINT` to be a real
  HTTPS domain; local HTTP s3-orchestrator endpoints are for development and
  tests only.

### 4. Validation & Error Matrix

- Missing multipart file -> `400 missing_file`.
- Invalid multipart body -> `400 invalid_multipart`.
- Upload stream read failure -> `400 read_failed`.
- Upload exceeds the hard stream cap -> `413 file_too_large`.
- Upload exceeds plan single-file limit -> `413 file_too_large`.
- Upload exceeds plan paste/account quota -> existing quota error code.
- Object missing while opening a download -> `410 attachment_unavailable`.
- Request context canceled during object write -> attachment metadata is not
  created and daily upload quota is not consumed.
- Wrong s3-orchestrator virtual bucket for credentials -> readiness
  `HeadBucket` or object operation fails with S3 access/bucket error.

### 5. Good/Base/Bad Cases

- Good: The HTTP handler streams the multipart file into
  `PrepareAttachmentUpload`, then the service streams that temp file into S3
  through `PutObjectStream`.
- Good: The download handler calls `OpenAttachment`/`OpenSharedAttachment` and
  copies the returned body with `io.Copy`.
- Base: Tests without a streaming object store fall back to the legacy in-memory
  object store for small payloads.
- Bad: Reintroducing `io.ReadAll(file)` in upload handlers or `io.ReadAll` on
  S3 response bodies in HTTP download handlers.

### 6. Tests Required

- Object-store tests must cover `PutObjectStream`, `OpenObject`, `DeleteObject`,
  and `Health` against a fake S3-compatible server.
- Keep an env-gated external S3-compatible endpoint test for real gateway
  smoke checks. It should be skipped unless endpoint, bucket, and credentials
  env vars are set.
- HTTP upload/download contract tests must continue to cover user uploads,
  guest uploads, owner downloads, and shared downloads.
- App-level tests must prove a blocked or slow object-store upload does not
  block unrelated service reads such as quota/list calls, and that metadata and
  daily quota are still rolled back on object/ref/metadata failures.
- Run `make test` after changing upload/download handlers or object-store
  adapters.

### 7. Wrong vs Correct

#### Wrong

```go
file, header, _ := r.FormFile("file")
content, _ := io.ReadAll(file)
attachment, err := s.app.AddAttachment(user.ID, pasteID, header.Filename, contentType(header), content)
```

#### Correct

```go
upload, fields, err := readAttachmentMultipart(r)
defer upload.Close()
attachment, err := s.app.AddPreparedGuestAttachment(fields["guestToken"], pasteID, upload, fields["turnstileToken"], clientIP(r))
```

#### Wrong

```go
s.mu.Lock()
defer s.mu.Unlock()
err := streaming.PutObjectStream(context.Background(), key, reader, size, contentType)
```

#### Correct

```go
objectKey, err := s.preflightPreparedAttachment(userID, pasteID, upload)
s.objectWriteMu.Lock()
defer s.objectWriteMu.Unlock()
stored, err := s.storePreparedObject(ctx, objectKey, upload)
attachment, err := s.finalizePreparedAttachment(userID, pasteID, upload, stored)
```

#### Wrong

```go
content, err := s.app.DownloadAttachment(user.ID, attachmentID)
w.Write(content)
```

#### Correct

```go
download, err := s.app.OpenAttachment(user.ID, attachmentID)
defer download.Body.Close()
_, _ = io.Copy(w, download.Body)
```

## Scenario: OAuth Deployment Env Wiring

### 1. Scope / Trigger

- Trigger: Any backend or deployment change that adds, renames, or changes
  OAuth providers, OAuth callback routes, OAuth env keys, or local/demo
  Compose deployment templates.

### 2. Signatures

- Google start/callback routes:
  `GET /api/v1/auth/google/start` and
  `GET /api/v1/auth/google/callback`.
- GitHub start/callback routes:
  `GET /api/v1/auth/github/start` and
  `GET /api/v1/auth/github/callback`.
- Start route query contract: `returnTo` plus optional `language`, `locale`,
  `lang`, or `hl`.
- Config fields: `Config.GoogleOAuth` and `Config.GitHubOAuth`.
- Demo deployment file: `compose.deploy.yaml`.
- Production env template: `deploy/production.env.example`.

### 3. Contracts

- Google OAuth uses `PASTEBOX_GOOGLE_OAUTH_CLIENT_ID`,
  `PASTEBOX_GOOGLE_OAUTH_CLIENT_SECRET`, and
  `PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL`.
- GitHub OAuth uses `PASTEBOX_GITHUB_OAUTH_CLIENT_ID`,
  `PASTEBOX_GITHUB_OAUTH_CLIENT_SECRET`, and
  `PASTEBOX_GITHUB_OAUTH_REDIRECT_URL`.
- Default redirect URLs must derive from `PASTEBOX_PUBLIC_URL` and end with
  `/api/v1/auth/<provider>/callback`.
- OAuth start routes must persist the normalized UI language in the signed
  OAuth state. Callback handlers must pass it to the service so newly-created
  OAuth users get the same language as the request that started OAuth.
- Compose deployment templates that run the API or worker must pass through
  the OAuth env keys so local/demo deployments do not silently disable a
  configured provider.
- Frontend login UI may add OAuth providers, but must not remove existing
  supported provider entrypoints unless the backend routes and docs are also
  intentionally removed.

### 4. Validation & Error Matrix

- Provider client ID/secret missing in runtime config ->
  `503 <provider>_oauth_not_configured`.
- Provider env keys set on the host but omitted from Compose service
  environment -> deployment bug; the OAuth start route behaves unconfigured.
- Wrong redirect URL in env -> provider callback fails outside PasteBox.
- Unsupported OAuth callback state -> redirect to the app with provider-scoped
  error query state, without creating a session.
- Missing language query/header -> new OAuth accounts default to `en`.
- `zh-Hant`, `zh-TW`, `zh-HK`, or `zh-MO` -> user language `zh-TW`;
  `zh`, `zh-CN`, or `zh-SG` -> `zh-CN`; `es*` -> `es`; `en*` -> `en`.

### 5. Good/Base/Bad Cases

- Good: `docker compose config` shows both Google and GitHub OAuth env keys on
  API, worker, and migration services when the host env is set.
- Base: With local fake client IDs and secrets, `GET
  /api/v1/auth/google/start?returnTo=/app&language=zh-CN` and
  `/github/start?returnTo=/app&locale=es` both return `303` to their provider
  authorization URL and preserve language in signed state.
- Bad: Adding GitHub OAuth to `internal/config` while leaving
  `compose.deploy.yaml` unaware of the new env keys.

### 6. Tests Required

- Config tests must assert both provider env parsing and default redirect URL
  derivation from `PASTEBOX_PUBLIC_URL`.
- Handler tests must cover OAuth start redirects and callback state handling
  for each supported provider, including language persistence into the created
  user.
- Deployment changes must run `docker compose -f compose.deploy.yaml config`
  with representative OAuth env values and assert the rendered services carry
  those values.

### 7. Wrong vs Correct

#### Wrong

Add a provider only to `internal/config`:

```go
GitHubOAuth: OAuthConfig{ClientID: envString("PASTEBOX_GITHUB_OAUTH_CLIENT_ID", "")}
```

#### Correct

Wire the provider through config, routes, frontend entrypoints, tests, and
deployment env templates:

```yaml
PASTEBOX_GITHUB_OAUTH_CLIENT_ID: ${PASTEBOX_GITHUB_OAUTH_CLIENT_ID:-}
PASTEBOX_GITHUB_OAUTH_CLIENT_SECRET: ${PASTEBOX_GITHUB_OAUTH_CLIENT_SECRET:-}
```

## Scenario: Attachment Scan Sharing Gates

### 1. Scope / Trigger

- Trigger: Any backend change that touches attachment scan states, owner
  downloads, shared attachment downloads, share creation, scanner workers, or
  scan retry behavior.

### 2. Signatures

- Service share creation:
  `CreateShare(userID string, pasteID string, input ShareInput) (ShareView, error)`
- Owner download:
  `DownloadAttachment(userID string, attachmentID string) (AttachmentView, []byte, error)`
- Shared download:
  `DownloadSharedAttachment(token string, password string, attachmentID string, viewerUserID string) (AttachmentView, []byte, error)`
- Scanner result application:
  `RunAttachmentScan(scanner Scanner, attachmentID string) error`
- Scan states: `pending`, `clean`, `scan_failed`, and `malicious`.

### 3. Contracts

- Owner downloads may proceed for `pending` and `scan_failed` attachments, but
  must reject `malicious` attachments with `403 malicious_file`.
- Shared attachment downloads require `scanStatus == "clean"`. Pending,
  failed, malicious, and unknown scan states must reject with
  `403 scan_not_clean`.
- New share creation must reject active pastes that contain any active
  `malicious` attachment with `403 malicious_file`.
- Existing shares remain auditable and revocable after a file is later marked
  malicious, but shared downloads through those shares remain blocked by the
  clean-scan gate.
- Retrying scans must not auto-retry `malicious` attachments.

### 4. Validation & Error Matrix

- Owner downloads `pending` attachment -> allowed.
- Owner downloads `scan_failed` attachment -> allowed.
- Owner downloads `malicious` attachment -> `403 malicious_file`.
- Public/shared downloads any non-`clean` attachment -> `403 scan_not_clean`.
- Create share for paste containing active `malicious` attachment ->
  `403 malicious_file`.
- Admin retry scan for `malicious` attachment -> `403 malicious_file`.

### 5. Good/Base/Bad Cases

- Good: A paste with a malicious attachment cannot receive new public share
  links, and an old share cannot download the malicious file.
- Base: A paste with a pending executable upload can still be shared, but the
  public file download waits until the scanner marks the attachment clean.
- Bad: Blocking only shared downloads but allowing new shares on known
  malicious files, because the product would still advertise a public link for
  content known to be blocked globally.

### 6. Tests Required

- Domain tests in `internal/app` must cover pending owner downloads,
  scan-failed owner downloads, public clean-scan gates, malicious owner
  download rejection, malicious shared download rejection, and malicious share
  creation rejection.
- Run full `make test` after changing scan gates because frontend attachment
  status presentation consumes the same API fields.

### 7. Wrong vs Correct

#### Wrong

```go
share := &Share{PasteID: paste.ID, UserID: userID}
return s.createShareLocked(share)
```

#### Correct

```go
for _, attachment := range s.attachmentsForPasteLocked(paste) {
    if attachment.Status == "active" && attachment.ScanStatus == "malicious" {
        return ShareView{}, E(http.StatusForbidden, "malicious_file", "known malicious files cannot be shared")
    }
}
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
  object storage setup, Redis config, scanner config, SMTP config, or worker
  queue wiring.

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
  storage, Redis TCP reachability, ClamAV TCP reachability when the scanner
  provider is `clamav`, worker job-table access, worker heartbeat freshness,
  and SMTP TCP reachability when SMTP is configured.
- Development/test handlers that do not inject a checker use a default
  in-process `application: ok` component so local tests do not require external
  services.

### 4. Validation & Error Matrix

- Dependency check succeeds -> component `ok`.
- `PASTEBOX_SCANNER_PROVIDER=clamav` and `PASTEBOX_CLAMAV_ADDR` is reachable ->
  scanner component `ok`.
- Scanner provider is not `clamav` in production -> scanner component `fail`.
- Scanner provider is not `clamav` outside production -> scanner component
  `skipped`.
- SMTP provider is not `smtp` -> mail component `skipped`.
- Dependency check fails or times out -> component `fail`, HTTP 503.
- S3 bucket cannot be checked -> object storage component `fail`, HTTP 503.

### 5. Good/Base/Bad Cases

- Good: Production `/readyz` proves database, object storage, Redis, ClamAV,
  SMTP, worker queue access, and worker heartbeat freshness before Compose
  marks API healthy.
- Base: Unit tests can inject a failing checker and assert 503 without opening
  real sockets.
- Bad: A static `{"status":"ready"}` response while PostgreSQL or object
  storage is unreachable.

### 6. Tests Required

- Handler tests assert ready and failed readiness response shapes and status
  codes.
- Command/package tests compile the production readiness checker with the real
  PostgreSQL pool and S3 health interface.
- Command/package tests assert scanner readiness fails in production when
  ClamAV is not configured, skips outside production, and succeeds against a
  reachable ClamAV TCP address.
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
  reporting, admin/operational queue data, production preflight, production
  Prometheus files, or monitoring runbooks.

### 2. Signatures

- API: `GET /metrics`
- Config: `PASTEBOX_METRICS_TOKEN`
- Preflight: `pastebox preflight production`
- Service: `app.Service.OperationalMetrics()`
- Compose profile: `monitoring` service `prometheus`
- Scrape config: `deploy/monitoring/prometheus.yml`
- Alert rules: `deploy/monitoring/pastebox-alerts.yml`

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
- The optional production Prometheus profile must scrape `api:8080/metrics`
  with `authorization.credentials_file = /run/secrets/pastebox_metrics_token`.
  The secret is sourced from `PASTEBOX_METRICS_TOKEN`; do not write the token
  into committed YAML.
- Baseline alert rules must cover scrape availability, overall readiness,
  component readiness, operational metric loading, failed jobs, scanner backlog,
  mail backlog, and open abuse/support report backlog.

### 4. Validation & Error Matrix

- Missing or wrong bearer token -> `401 metrics_unauthorized`.
- Missing production token -> production preflight fails.
- Readiness check fails -> metrics endpoint still returns text with readiness
  gauges set to `0`.
- Operational metric loading fails -> emit `pastebox_operational_metrics_available
  0` without exposing the internal error text.
- Compose monitoring profile cannot render -> deployment config validation
  fails.
- Prometheus config or alert rules are syntactically invalid -> monitoring
  validation fails before launch.

### 5. Good/Base/Bad Cases

- Good: Monitoring scrapes `/metrics` over HTTPS with the bearer token and
  alerts on readiness, failed jobs, queue lag, mail backlog, and unresolved
  support/abuse reports.
- Base: Development can leave the metrics token empty; `/metrics` remains
  unauthorized until a token is configured.
- Bad: Exposing `/metrics` publicly without a token or labeling HTTP metrics
  with raw share tokens, paste IDs, emails, or object keys. Also bad: putting
  `PASTEBOX_METRICS_TOKEN` directly into committed Prometheus config.

### 6. Tests Required

- Handler tests must cover unauthorized and authorized `/metrics` access.
- Handler tests must assert representative Prometheus lines for readiness,
  HTTP request counters, and operational gauges.
- Command tests must cover production preflight rejecting missing or short
  metrics tokens.
- Deployment checks must render `docker compose --profile monitoring config`
  against `deploy/production.env.example`.
- Prometheus checks must validate `deploy/monitoring/prometheus.yml` and
  `deploy/monitoring/pastebox-alerts.yml` with `promtool` or an equivalent
  syntax checker.
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

#### Wrong

```yaml
authorization:
  credentials: "CHANGE_ME_LONG_RANDOM_METRICS_TOKEN"
```

#### Correct

```yaml
authorization:
  credentials_file: /run/secrets/pastebox_metrics_token
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
- Epusdt webhook event metadata must be allowlisted. It may include normalized
  amount and provider identifiers such as `tradeId`/`txId`, but must not store
  the raw provider payload or the provider `signature`, because webhook events
  are visible to admins and included in user data exports for related orders.
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
- Signed Epusdt callback with extra provider fields -> process normally, but
  persisted metadata excludes `raw` and `signature`.
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
  responds with exactly `ok`; stored metadata includes sanitized identifiers
  but not raw payload or signature.
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
  paid order state, with webhook metadata excluding raw payload and signature.
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
- Display-name address such as `Support <support@pastebox.app>` -> production
  preflight exits `1`; only the plain address is accepted.
- Local or non-production domain such as `support@localhost` -> production
  preflight exits `1`.
- Reserved/documentation domains such as `support@pastebox.example.com` ->
  production preflight exits `1`.
- API request succeeds -> `200` with both configured address fields.

### 5. Good/Base/Bad Cases

- Good: `support@pastebox.app` and `abuse@pastebox.app` pass
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

## Scenario: Manual Billing Correction Audit Contract

### 1. Scope / Trigger

- Trigger: Any change that touches admin billing correction controls, manual
  order state changes, support/refund workflows, or `MarkOrderPaid`.

### 2. Signatures

- Service: `MarkOrderPaid(actorID string, orderID string, txID string, reason string)`
- API: `POST /api/v1/admin/orders/{orderID}/mark-paid`
- Request JSON: `{"txId":"<provider-or-manual-reference>","reason":"<support reason>"}`
- Frontend client: `client.adminMarkOrderPaid(id, txId, reason)`
- Service:
  `AdminSetUserPlan(actorID, userID, planID, expiresAt, reason, ticketID)`
- API: `PATCH /api/v1/admin/users/{userID}/plan`
- Request JSON:
  `{"planId":"plus","expiresAt":"<optional RFC3339>","reason":"<support reason>","ticketId":"<optional support ticket>"}`

### 3. Contracts

- Manual payment correction is admin-only at the service boundary, not only in
  the HTTP router.
- `reason` is required and must identify the support ticket, refund/payment
  investigation, or correction rationale.
- `reason` must be trimmed, non-empty, and no longer than 500 characters.
- Manual correction audit metadata must include `manual: true`, `reason`,
  `planId`, `provider`, and `txId` when a transaction reference exists.
- Webhook-driven payment activation remains provider-driven and must not require
  the manual `reason` field.
- Frontend admin order cards must collect the reason before enabling the manual
  paid action.
- Manual admin plan changes require either `reason` or `ticketId` and must
  audit `oldPlanId`, `newPlanId`, `oldExpiresAt`, `newExpiresAt`, and any
  supplied support reason/ticket.

### 4. Validation & Error Matrix

- Non-admin actor -> `403 admin_required`.
- Blank or whitespace-only `reason` -> `400 manual_reason_required`.
- `reason` longer than 500 characters -> `400 manual_reason_too_long`.
- Admin plan change with neither `reason` nor `ticketId` ->
  `400 admin_plan_reason_required`.
- Existing paid order with manual replay metadata -> idempotent success with no
  duplicate plan activation.
- Provider webhook payment success -> no manual reason required.

### 5. Good/Base/Bad Cases

- Good: Admin marks a stuck Epusdt order paid with reason
  `SUP-123 verified stuck Epusdt transfer`; audit logs preserve that reason.
- Good: Admin changes a user from `free` to `plus` with ticket `SUP-456`;
  audit logs preserve old/new plan and the ticket.
- Base: Stripe webhook marks an order paid using signed provider metadata and
  does not need an operator reason.
- Bad: UI generates `manual-<timestamp>` and calls mark-paid without an
  operator reason, leaving no support trail.

### 6. Tests Required

- Service tests must assert non-admin manual mark-paid fails with
  `admin_required`.
- Service and handler tests must assert blank reason fails with
  `manual_reason_required`.
- Service or handler tests must assert successful manual correction stores the
  reason in `billing.order_paid` audit metadata.
- Service/handler tests must assert admin plan changes reject missing
  reason/ticket and store old/new plan plus support reason/ticket in audit
  metadata.
- Frontend changes must pass `make test-web`; cross-layer changes must pass
  full `make test`.

### 7. Wrong vs Correct

#### Wrong

```go
order, err := svc.MarkOrderPaid(admin.ID, order.ID, "manual-123")
```

#### Correct

```go
order, err := svc.MarkOrderPaid(admin.ID, order.ID, "manual-123", "SUP-123 verified stuck payment")
```

## Scenario: Production Release Evidence Validator

### 1. Scope / Trigger

- Trigger: Any change to `scripts/check-production-release-evidence.mjs`,
  `docs/production-launch-evidence-checklist.md`,
  `docs/production-release-notes-template.md`, `Makefile` `release-evidence`,
  or the public beta launch decision contract.

### 2. Signatures

- Command: `make release-evidence RELEASE_CHECKLIST=<completed-checklist.md>
  RELEASE_NOTES=<completed-release-notes.md>`
- Script: `node scripts/check-production-release-evidence.mjs --checklist
  <completed-checklist.md> --release-notes <completed-release-notes.md>`
- Self-test: `node scripts/check-production-release-evidence.mjs --self-test`

### 3. Contracts

- Completed evidence must include every required checklist item from
  `docs/production-launch-evidence-checklist.md`, and every checkbox must be
  checked.
- Completed release notes must include every field from
  `docs/production-release-notes-template.md`.
- Release identity fields must match exactly across the checklist and release
  notes after whitespace normalization: `Release commit`, `Immutable image
  reference or digest`, `Production domain`, `Deployment window`, `Operator`,
  `Previous known-good image`, and `Migration classification`.
- `Migration classification` must be one of `no-migration`, `reversible`,
  `forward-compatible`, or `non-reversible` in both completed artifacts.
- `Immutable image reference or digest` and `Previous known-good image` must
  use a `sha-*` tag or registry digest, mirroring production preflight's pinned
  `PASTEBOX_IMAGE` rule.
- The release notes `Completed evidence checklist path` must match the
  `--checklist` path passed to the validator, accepting equivalent absolute,
  working-directory-relative, or repo-root-relative path forms.
- The launch decision must record `Release evidence validator result` beginning
  with `passed`, `Operator approval` beginning with `approved`, and `Public beta
  traffic accepted` exactly `yes`.
- Completed evidence must reject placeholder values and common raw-secret
  patterns such as Stripe keys, Stripe webhook secrets, GitHub tokens, AWS
  access keys, bearer tokens, JWTs, and private key blocks.

### 4. Validation & Error Matrix

- Unchecked checklist item -> release evidence check fails.
- Missing checklist item or release-notes field -> release evidence check
  fails.
- Placeholder field value -> release evidence check fails.
- Invalid migration classification -> release evidence check fails.
- Mutable image reference such as `:latest` or `:v1.2.3` -> release evidence
  check fails.
- Checklist/release-notes release identity mismatch -> release evidence check
  fails.
- `Completed evidence checklist path` does not refer to `--checklist` ->
  release evidence check fails.
- Validator result not `passed...` -> release evidence check fails.
- Operator approval not `approved...` -> release evidence check fails.
- `Public beta traffic accepted` not `yes` -> release evidence check fails.
- Raw secret-like value in either file -> release evidence check fails.

### 5. Good/Base/Bad Cases

- Good: A sanitized release candidate checklist and release notes name the same
  commit, pinned image, domain, operator, previous image, and migration class,
  include no secrets, record the actual checklist path, record validator
  `passed`, operator `approved`, and set public beta traffic to `yes`.
- Base: Template files remain unchecked and placeholder-filled in the repo; they
  are validated only by template/self-test checks, not as completed evidence.
- Bad: A completed checklist for commit `abc1234` and release notes for commit
  `def5678` pass independently, or a release note stores `whsec_...` as proof.

### 6. Tests Required

- Keep `--self-test` cases for success plus unchecked checklist, missing
  checklist item, empty/placeholder field, invalid migration classification in
  both artifacts, mutable image references, missing release-notes field,
  unapproved launch, failed validator result, pending operator approval,
  raw-secret rejection in both files, release identity mismatch, and mismatched
  completed-checklist path.
- Run `node scripts/check-production-release-evidence.mjs --self-test` after
  changing the validator.
- Run `node scripts/check-release-evidence-template.mjs` after changing
  evidence templates or release decision fields.
- Run full `make production-readiness` before committing launch-gate changes.

### 7. Wrong vs Correct

#### Wrong

```js
validateChecklist(checklist, checklistTemplate);
validateReleaseNotes(releaseNotes, releaseNotesTemplate);
```

#### Correct

```js
validateChecklist(checklist, checklistTemplate);
validateReleaseNotes(releaseNotes, releaseNotesTemplate);
validateEvidenceConsistency(checklist, releaseNotes);
validateChecklistPathReference(options.checklist, releaseNotes);
```

## Scenario: GitHub Actions JavaScript Runtime Migration

### 1. Scope / Trigger

- Trigger: Any change prompted by a GitHub-hosted runner deprecation warning,
  an Action's bundled Node.js runtime reaching end of life, or a major-version
  upgrade in `.github/workflows/`.

### 2. Signatures

- Workflow: `.github/workflows/docker-image.yml`
- Action reference: `uses: <owner>/<action>@<supported-major>`
- Local release gate: `make production-readiness`

### 3. Contracts

- Resolve supported major versions from each Action's official repository or
  release notes at implementation time; do not preserve stale version numbers
  in this spec.
- Keep workflow triggers, permissions, conditions, inputs, image tags,
  platforms, and cache settings unchanged unless the major-version release
  notes require a reviewed migration.
- Do not use `ACTIONS_ALLOW_USE_UNSECURE_NODE_VERSION` to extend an end-of-life
  runtime. Upgrade the affected Actions instead.
- A GitHub-hosted run is the final compatibility check because local project
  tests cannot execute the hosted Action bundles or publish to GHCR.

### 4. Validation & Error Matrix

- Old runtime warning remains in the latest run -> migration is incomplete.
- YAML or `actionlint` failure -> reject the workflow change before push.
- Production readiness failure -> fix the project regression before push.
- Hosted image build or GHCR push failure -> inspect the upgraded Action step;
  do not mark the migration complete.

### 5. Good/Base/Bad Cases

- Good: Every warned Action is upgraded from official release guidance,
  `make production-readiness` passes, and the hosted multi-platform publish
  completes without the runtime warning.
- Base: An Action already uses the current supported major and remains
  unchanged.
- Bad: Set an insecure-runtime opt-out flag or remove the warning from logs
  without upgrading the Action references.

### 6. Tests Required

- Search all `.github/workflows/` files for every affected Action before and
  after editing so no stale occurrence remains.
- Parse the workflow YAML and run `actionlint` when it is available.
- Run `make production-readiness`.
- Push the isolated workflow change, then require the latest `Docker image`
  workflow to pass its readiness, multi-platform build, and GHCR push steps
  without the original runtime warning.

### 7. Wrong vs Correct

#### Wrong

```yaml
env:
  ACTIONS_ALLOW_USE_UNSECURE_NODE_VERSION: "true"
```

#### Correct

```yaml
steps:
  - uses: actions/checkout@<current-supported-major>
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
