# Plan PasteBox Production Launch

## Goal

Move PasteBox from the original demo/MVP state to a production-ready service by
resolving launch-scope uncertainties one decision at a time, producing
repo-backed launch design/roadmap artifacts, and continuing implementation until
repo-local gates plus operator-owned release evidence prove the public beta
launch gate.

## What I already know

- The user wants Codex goal-mode work to continue until the production launch
  objective is complete.
- At the start of launch planning, the demo could be deployed for internal
  review but was not ready for real customer data, paid SaaS, or long-running
  production operation.
- The original baseline kept users, sessions, pastes, attachments, shares,
  orders, audit logs, and queues in memory, with PostgreSQL, Redis, S3, mail,
  Stripe, Epusdt, ClamAV, and worker paths as seams/stubs.
- As of 2026-05-26, the current source tree has production Compose/default,
  monitoring, maintenance, Caddy, backup/PITR, rollback, preflight, immutable
  image, PostgreSQL, object-storage, worker, scanner, OAuth, mail, billing,
  legal/support, and readiness-gate slices implemented and locally verified.
- `make production-readiness` now covers Compose rendering, maintenance script
  syntax, monitoring config syntax, Caddy config syntax, production preflight,
  backend/frontend tests, web launch-surface smoke checks, PostgreSQL
  integration tests, project build, and Docker image build.
- Public beta launch approval still requires operator-owned external evidence:
  production secrets/domain, OAuth app, SMTP delivery, managed object storage,
  Stripe/Epusdt provider smoke tests, ClamAV smoke tests, backup/PITR restore
  drills, rollback rehearsal, monitoring/alert targets, support readiness, and
  release approval recorded through `docs/production-launch-evidence-checklist.md`.

## Source Context

- `README.md`
- `docs/deployment.zh-CN.md`
- `docs/deployment.md`
- `.trellis/spec/backend/quality-guidelines.md`
- Archived product PRD:
  `.trellis/tasks/archive/2026-05/05-23-cloudpaste-product-prd/prd.md`

## Launch Gap Areas To Confirm

1. Data persistence and migrations.
2. Object storage and download security.
3. Redis, sessions, rate limits, queues, and workers.
4. Auth, email, OAuth, and token handling.
5. Billing and payment lifecycle.
6. File scanning and abuse/safety controls.
7. Operations, observability, backup, and recovery.
8. Security hardening and anti-abuse controls.
9. Production deployment topology and release process.
10. Legal, compliance, support, and product operations.

## Open Questions

- None for planning. The user confirmed the design document and roadmap as the
  implementation source of truth, but asked not to start implementation in this
  session.

## Requirements

- Confirm every launch gap area before implementation starts.
- When asking the user a decision question, stop and wait for the user's reply
  before continuing. Do not continue by applying provisional defaults after a
  question has been asked.
- Persist each decision in the PRD or final design document.
- Produce a production launch design document in the repository.
- Produce an implementation roadmap with phases, ordering, migration strategy,
  test gates, rollback strategy, and launch checklist.
- Continue development under the active Codex goal until the launch objective is
  complete or explicitly paused/blocked by user decision.

## Acceptance Criteria

- [x] All 10 launch gap areas have explicit decisions.
- [x] Production launch design document is saved in the repository.
- [x] Implementation roadmap is saved in the repository.
- [x] Trellis task can be activated with enough context to implement.
- [x] Follow-up implementation phases can be executed and verified locally.
- [ ] Operator-owned external release evidence is complete for a public beta
  release candidate.

## Definition of Done

- Design document committed.
- Implementation roadmap committed.
- Backend/frontend/code-spec updates made where launch contracts are decided.
- `make test` remains green after any code changes.
- User confirms the launch design is the source of truth for implementation.

## Out of Scope For This Planning Pass

- Reopening already-confirmed launch-scope decisions without new evidence.
- Choosing vendors without user confirmation when the choice affects cost,
  compliance, or operations.
- Treating demo-only stubs as production-ready.

## Decision Log

### 1. Launch Target

- Decision: public beta with free base access and paid upgrades enabled.
- Reason: it allows real users and real operational signals without requiring
  every user to pay on day one, while still validating the paid SaaS path at
  launch.
- Impact: production-readiness still requires persistence, object storage,
  email, security, backups, monitoring, abuse handling, and a complete
  Stripe/Epusdt payment lifecycle before public beta.
- Status: confirmed by user.

### 2. Data Region And Provider Family

- Decision: US single-region VPS/cloud host plus managed
  S3-compatible object storage.
- Reason: public free beta needs production-like durability and observability
  without over-optimizing for multi-region complexity or high managed-service
  cost on day one.
- Baseline topology: one US-region host runs API, worker, PostgreSQL, and
  Redis-compatible cache/queue; attachment bytes live in managed object
  storage.
- Impact: this keeps deployment simple while still forcing the codebase to
  become stateless at the API layer and durable at the storage layer. Later
  phases can split PostgreSQL/Redis into managed services without changing
  product contracts.
- Status: confirmed by user.

### 3. Payment Provider Scope

- Decision: Stripe and Epusdt both enabled for the first production launch.
- Reason: the service should support both international card subscription
  payments and USDT fixed-duration membership orders from the first paid public
  launch.
- Contracts required: provider webhook signature verification, idempotency keys,
  duplicate event replay safety, order state reconciliation, plan activation,
  expiry handling, refund/cancel handling for Stripe, manual admin payment
  controls, and audit logs.
- Impact: public launch cannot proceed with billing stubs. Billing moves from a
  later optional phase into the launch-critical path.
- Status: confirmed by user.

### 4. Email Provider Scope

- Decision: reuse the user's existing SMTP/enterprise email service for the
  first production launch.
- Reason: this avoids introducing a new mail vendor during beta while still
  allowing production auth, security, billing, and support email flows to stop
  exposing development tokens in API responses.
- Contracts required: configurable SMTP host/port/TLS/auth, production sender
  address, sender-domain DNS readiness, retryable mail jobs, templated
  verification/magic-link/reset/security/billing/support emails, bounce/error
  logging, and development-only token output behind explicit development mode.
- Impact: launch still requires the sender domain and SMTP credentials to be
  operational before production mode is enabled. The mail adapter should remain
  provider-neutral so Resend or SES can replace SMTP later without changing
  product flows.
- Status: confirmed by user.

### 5. Google OAuth Scope

- Decision: enable real Google OAuth for the first production launch.
- Reason: Google sign-in is part of the launch UX rather than a post-launch
  enhancement, so the production identity path must be completed before public
  beta.
- Contracts required: production Google OAuth application, approved redirect
  URI, configured client ID/secret, secure state/nonce handling, account
  linking by verified email, explicit unlink behavior, session issuance through
  the same production auth pipeline as email login, audit logging, and tests
  for login, account linking conflicts, callback failures, and CSRF/state
  mismatch.
- Impact: launch cannot rely on demo OAuth stubs or placeholder credentials.
  OAuth setup, callback-domain validation, and account-linking behavior become
  part of the auth productionization phase and launch checklist.
- Status: confirmed by user.

### 6. File Scanning And Download Policy

- Decision: use a balanced scanning policy for first production launch.
- Public shared downloads require a clean scan result. Pending, unscanned,
  failed-scan, and malicious files are not downloadable through public share
  links.
- Private owner downloads may proceed before scanning is complete unless the
  file is known malicious. The UI must show scan status and risk messaging for
  pending or failed scans before owner download.
- Known malicious files are blocked globally for owner downloads, public share
  downloads, previews, exports, and future share creation.
- Contracts required: persisted scan status, scanner worker, retryable failed
  scan jobs, quarantine/block state, owner-facing status UI, public-download
  authorization gate, admin triage actions, audit logs, and tests covering
  pending, clean, failed, malicious, owner-download, and public-share paths.
- Impact: public sharing is safety-gated while owner access remains usable
  during scanner delays. The launch checklist must include scanner health,
  queue monitoring, and an admin path for failed or malicious scan triage.
- Status: confirmed by user.

### 7. Availability, Backup, And Recovery Target

- Decision: use the standard production target for first launch.
- Availability target: 99.9% monthly availability for the public service.
- Backup retention: 30 days.
- Backup mechanics: daily full logical backups plus WAL/PITR or equivalent
  point-in-time recovery, with backup artifacts stored off-host in durable
  object storage.
- Recovery targets: RPO 15 minutes and RTO 4 hours.
- Contracts required: automated backup jobs, WAL archiving or equivalent PITR,
  restore drill before launch, restore runbook, backup integrity checks, alerting
  for failed backups/WAL lag/disk pressure/database health, and documented
  incident response thresholds.
- Impact: first launch may still use local PostgreSQL on the beta host only if
  backups are stored off-host, recovery is rehearsed, and monitoring proves the
  99.9%/RPO/RTO targets are supportable. If restore drills fail to meet RTO,
  PostgreSQL must move to a managed or standby-backed topology before launch.
- Status: confirmed by user.

### 8. Deployment And Release Topology

- Decision: first production launch uses a single US VPS with Docker Compose.
- Runtime topology: API container, worker container, PostgreSQL container or
  local service, Redis-compatible cache/queue container or local service, reverse
  proxy/TLS entrypoint, managed S3-compatible object storage, and off-host
  backups.
- Release process: build immutable images, publish to the registry, deploy by
  pulling a pinned image tag/digest, run migrations before traffic switch,
  verify health/readiness checks, and keep a rollback path to the previous image
  plus database restore procedure.
- Contracts required: production Compose files, environment template, secret
  management instructions, reverse-proxy/HTTPS config, health/readiness probes,
  worker supervision, migration command, backup/WAL archiving jobs, restore
  drill, log/metric/error collection, certificate renewal checks, disk/CPU/memory
  alerts, and a deployment runbook.
- Impact: this minimizes beta complexity and cost but does not provide host-level
  high availability. The launch gate must explicitly prove backup/restore and
  rollback behavior. The code and data adapters must remain compatible with
  later migration to managed PostgreSQL/Redis or a managed container platform.
- Status: confirmed by user.

### 9. Security Hardening And Anti-Abuse Baseline

- Decision: apply a production security baseline derived from the confirmed
  public beta, paid billing, OAuth, upload, share, and single-VPS topology.
- Contracts required: production-only secure cookies, CSRF protection on browser
  state-changing flows, OAuth state/nonce validation, webhook signature
  validation, password/session/token hashing, development token output disabled
  outside explicit development mode, per-IP and per-account rate limits,
  upload/download/share abuse limits, audit logging for auth/billing/admin
  actions, secure response headers, CORS allowlist, secret redaction in logs,
  administrator authorization boundaries, and an abuse report/takedown workflow.
- Impact: these are not optional polish items. They gate public beta because the
  service accepts uploaded files, public share links, paid orders, and third-party
  identity callbacks.
- Status: derived from confirmed launch requirements.

### 10. Legal, Compliance, Support, And Product Operations

- Decision: use the enhanced compliance package for first production launch.
- Required public pages: Terms, Privacy Policy, Refund Policy, Abuse/DMCA,
  Cookie Notice/Consent, status/announcement page, support contact page, account
  deletion instructions, and data export instructions.
- Required operational workflows: support email intake, abuse/DMCA intake,
  refund request handling, GDPR/data-subject request workflow, DPA request path,
  data retention matrix, subprocessors list, support/admin audit logging, and
  response runbooks for payment disputes, account deletion, export requests, and
  takedown requests.
- Impact: public launch cannot ship with only placeholder legal pages. The app
  must expose enough UI/docs for users to understand data handling, payments,
  refund/abuse paths, cookies, account deletion, and export rights. Admin/support
  workflows must exist before paid public beta.
- Status: confirmed by user.

## Technical Notes

- The first blocking decision is the launch target, because it controls the
  required level of billing, legal, support, compliance, SLO, and abuse
  response work.
- All 10 launch gap areas now have explicit decisions or derived launch
  contracts.
- User confirmed `docs/production-launch-design.md` and
  `docs/production-launch-roadmap.md` as the implementation source of truth.
- The task is active and implementation is in progress. Future continuations
  should load `trellis-continue`, verify git status, keep the task activated,
  and continue from the next source-verifiable roadmap/evidence gap.
- Do not mark the public beta launch objective complete from source checks
  alone. Completion requires the release-candidate evidence checklist to be
  satisfied with live VPS/provider/backup/rollback/monitoring/support evidence.
