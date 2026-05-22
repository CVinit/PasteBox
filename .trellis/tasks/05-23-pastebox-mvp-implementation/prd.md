# Implement PasteBox MVP

## Goal

Implement the PasteBox MVP from the archived product PRD as a working single-node application. The first implementation must provide usable end-to-end product flows through the Go API and React frontend while preserving the production-oriented boundaries for database, object storage, billing, scanning, cleanup, and admin operations.

## Source Contract

- Product PRD: `.trellis/tasks/archive/2026-05/05-23-cloudpaste-product-prd/prd.md`
- Product name: `PasteBox`
- Code/module/binary name: `pastebox`

## Scope

- Authentication: email/password registration and login, dev magic link flow, password reset, HttpOnly cookie sessions, logout, logout all.
- Users: profile, display name, plan state, account deletion request/cancel/execute state.
- Pastes: create, list, view, update metadata, extend, delete, search, filter, tags, pinned/favorite flags, expiration enforcement.
- Attachments: upload through backend, MIME sniffing, sha256, image preview metadata, same-user dedupe, download auth, basic file risk checks.
- Sharing: anonymous share links, optional password, login-required flag, visit/download counters and limits, share expiry, revoke.
- Quotas: plan limits for active paste count, active storage, text size, file size, paste size, attachment count, retention, UTC daily upload/download windows.
- Billing: plan/price catalog, Stripe/Epusdt order/webhook models and API stubs, local member state source of truth.
- Admin: user/paste/attachment/share/order/audit views; plan override/freeze/takedown/retry operations with audit logs.
- Cleanup/scanning: expiration cleanup, pending delete/retry queues, scan status transitions, ClamAV-compatible scanner abstraction with dev stub.
- Export/deletion: JSON export of metadata/text and account deletion cooldown workflow.
- Frontend: responsive PWA-like app covering login, inbox, paste creation, upload, search/filter, sharing, admin, billing, export/delete settings.

## Implementation Strategy

- Use an in-memory repository plus local file/object abstraction for the first complete executable MVP.
- Keep module seams aligned with the PRD (`auth`, `users`, `pastes`, `attachments`, `shares`, `quota`, `billing`, `admin`, `scanner`, `cleanup`, `mailer`).
- Preserve `/api/v1/...` JSON contracts.
- Use secure defaults where possible: HttpOnly cookies, token hashing, no inline HTML/SVG rendering, escaped text in frontend, scan gating for non-owner downloads.
- External production integrations are represented as typed adapters/stubs and auditable state rather than live Stripe/Epusdt/ClamAV/S3 calls in this pass.

## Acceptance Criteria

- [ ] `make test` passes.
- [ ] API supports registration/login/session, paste CRUD, uploads/downloads, share links, quotas, admin operations, export/delete, and billing/scanner/cleanup stubs.
- [ ] Frontend builds and exercises the main user workflows against the API.
- [ ] Expired/deleted content is not readable through owner, search, download, or share flows.
- [ ] Share visit/download limits are enforced separately.
- [ ] Plan quotas are enforced server-side.
- [ ] Admin operations write audit logs.
- [ ] Trellis specs updated for new API/domain contracts.

## Out of Scope For This Pass

- Production PostgreSQL/sqlc migrations and live Redis/Asynq execution.
- Live Stripe, Epusdt, Resend, S3, or ClamAV network integration.
- Native mobile apps, browser extensions, CLI, teams, E2EE, and public third-party API guarantees.

