# PasteBox Production Launch Design

Status: confirmed implementation source of truth; repo-local implementation is
in progress and verified by the production-readiness gate.

This document tracks the design needed to move PasteBox from the original
demo/MVP to a production service. The launch-scope decisions are resolved and
confirmed. Continue implementation from this document,
`docs/production-launch-roadmap.md`, and the current source tree. Do not treat
repo-local checks as live launch approval: public beta still requires
operator-owned VPS, provider, backup/PITR, rollback, monitoring, and support
evidence recorded through `docs/production-launch-evidence-checklist.md`.

## Original Demo Baseline

- At the start of launch planning, the application ran as a single Go API plus
  embedded React frontend image.
- User, session, paste, attachment, share, order, audit, and queue state were
  in memory.
- PostgreSQL, Redis/queues, S3-compatible storage, mail, payment providers,
  ClamAV, cleanup, and admin surfaces existed as seams or stubs.
- The original deployment shape was suitable for demos, internal review, and
  low-risk evaluation, not real customer data or paid public SaaS.

## Current Repo Implementation Checkpoint

As of 2026-05-26, the repository contains the launch-oriented implementation
foundation and `make production-readiness` proves the local release-candidate
gate. The current source tree includes:

- Production Docker Compose, Caddy, monitoring, backup/PITR maintenance jobs,
  production preflight, immutable image publishing gate, deployment and rollback
  runbooks, and an evidence checklist.
- PostgreSQL migrations and typed repository boundaries for users, sessions,
  auth tokens, OAuth identities, pastes, attachments, object references, shares,
  catalog, daily metrics, orders, webhook events, audit logs, reports, jobs,
  mail, and worker heartbeats.
- API and worker runtime wiring that uses PostgreSQL stores, S3-compatible
  object storage, SMTP mail queues, ClamAV scanning, worker heartbeats, and
  readiness components in production.
- Production auth/email/OAuth behavior: development token output is disabled in
  production mode, auth mails are queued, Google OAuth uses state/nonce and
  verified ID tokens, and account-linking/unlinking is audited.
- File scanning and abuse controls for pending, clean, failed, and malicious
  attachment states, including public-share clean-scan gates and owner-facing
  scan status presentation.
- Stripe and Epusdt launch readiness behavior for signed webhooks, idempotency,
  order lifecycle states, plan activation/revocation, reconciliation, admin
  manual corrections, replay support, and UI status presentation.
- Legal, support, retention, subprocessors, refund, abuse/DMCA, status,
  account deletion, and data export surfaces with a local web launch-surface
  smoke gate.

Remaining launch approval is external evidence, not more placeholder code:
real production secrets, production domain and OAuth app, SMTP delivery smoke
tests, managed object-storage smoke tests, Stripe/Epusdt provider smoke tests,
ClamAV smoke tests, backup/PITR restore drills, rollback rehearsal, alert
target setup, and operator approval must be captured for each release candidate.

## Launch Target

Decision: public beta with free base access and paid upgrades enabled.

Rationale: the first production launch should let users start for free while
also proving the real paid SaaS path. This keeps acquisition friction low, but
it means billing, refunds/cancellations, provider webhook verification,
idempotency, reconciliation, and payment support are launch-critical rather
than optional follow-up work.

## Production Architecture Direction

The target production architecture is a modular monolith plus workers:

- `pastebox-api`: stateless Go HTTP API and embedded frontend.
- `pastebox-worker`: background workers for scanning, cleanup, retry queues,
  billing reconciliation, and notification jobs.
- PostgreSQL: source of truth for users, sessions, pastes, attachment metadata,
  shares, plans, orders, audit logs, and job state.
- Redis-compatible cache/queue: rate limits, short-lived locks, queue fan-out,
  and optional session acceleration, but not source-of-truth state.
- S3-compatible object storage: private object bucket for attachment bytes,
  thumbnails/previews, and export artifacts.
- Existing SMTP/enterprise email service: verification, password reset, magic
  link, security notifications, billing notices, support messages, and
  operational messages.
- Scanner service: ClamAV-compatible scanner worker boundary.
- Billing providers: Stripe for card/subscription payments and Epusdt for USDT
  fixed-duration membership orders.

## Decision Matrix

| Area | Decision | Status |
| --- | --- | --- |
| Launch target | Public beta with free base access and paid upgrades | Confirmed |
| Data region/provider | US single-region VPS/cloud host | Confirmed |
| PostgreSQL topology | Local PostgreSQL on beta host first, managed-ready adapter boundary | Confirmed |
| Object storage provider | Managed S3-compatible object storage | Confirmed |
| Redis/queue provider | Local Redis-compatible service on beta host first | Confirmed |
| Email provider | Existing SMTP/enterprise email service | Confirmed |
| Google OAuth | Enabled for first production launch | Confirmed |
| Payment providers | Stripe + Epusdt enabled at first production launch | Confirmed |
| File scanning policy | Public shares require clean scan; private owner downloads may proceed unless malicious | Confirmed |
| SLO/backup retention | 99.9% monthly availability; 30-day PITR; RPO 15m; RTO 4h | Confirmed |
| Deployment topology | Single US VPS with Docker Compose | Confirmed |
| Security/anti-abuse baseline | Production secure cookies, CSRF, rate limits, audit logs, webhook/OAuth validation | Derived |
| Legal/support docs | Enhanced compliance package | Confirmed |

## Security And Abuse Baseline

- Production mode must use secure cookies, explicit SameSite behavior, CSRF
  protection on browser state-changing flows, secure response headers, a CORS
  allowlist, and secret redaction in logs.
- Development auth token output must be disabled outside explicit development
  mode.
- Auth tokens, reset tokens, magic links, sessions, OAuth state/nonce values,
  webhook idempotency keys, and admin credentials must be persisted and stored
  with appropriate hashing or one-way verification where applicable.
- Rate limits must cover login, signup, verification, magic links, password
  reset, OAuth callback abuse, upload, download, share creation, payment
  callback surfaces, and report/support forms.
- Audit logs must cover auth, billing, admin, support, abuse, deletion, export,
  OAuth linking/unlinking, and payment correction events.
- Admin and support actions must require explicit authorization boundaries.

## Legal, Compliance, And Support Baseline

- Public pages: Terms, Privacy Policy, Refund Policy, Abuse/DMCA,
  Cookie Notice/Consent, status/announcement page, support contact, account
  deletion instructions, and data export instructions.
- Operational workflows: support email intake, abuse/DMCA intake, refund
  handling, GDPR/data-subject request handling, DPA request path, data retention
  matrix, subprocessors list, payment dispute runbook, account deletion runbook,
  data export runbook, and takedown runbook.
- Product surfaces: footer/legal navigation, billing refund/support links,
  account deletion/export entry points, cookie consent where non-essential
  cookies are used, and public status/announcement link.

## Implementation Phases

### Phase 0: Production Deployment Baseline

- Add production Docker Compose files for API, worker, PostgreSQL, Redis,
  reverse proxy/TLS, backup jobs, and monitoring sidecars where needed.
- Build immutable images, publish to the registry, and deploy by pinned image
  tag or digest rather than mutable local builds.
- Add environment templates, secret management instructions, reverse-proxy HTTPS
  config, health/readiness probes, worker supervision, migration commands,
  certificate renewal checks, and a deployment runbook.
- Store attachment bytes and backup artifacts off-host. The VPS may run
  PostgreSQL and Redis locally for beta only if backup/restore, WAL/PITR, and
  monitoring prove the confirmed SLO/RPO/RTO target.

### Phase 1: Durable Data Foundation

- Add PostgreSQL/sqlc schema and migrations.
- Move users, sessions, auth tokens, pastes, shares, plans, orders, audit logs,
  and queue records out of memory.
- Add migration runner, local database reset workflow, and integration tests.
- Beta default: run PostgreSQL on the same US-region host with volume-backed
  storage, daily logical backups, and restore drills. Keep DSN/env wiring
  compatible with later managed PostgreSQL migration.

### Phase 2: Durable Object Storage

- Add S3-compatible object adapter for private attachment storage.
- Implement upload rollback, reference-counted deletion, download auth checks,
  preview metadata, and lifecycle cleanup.
- Beta default: store attachment bytes in managed S3-compatible object storage
  rather than the application host disk.

### Phase 3: Auth And Mail Productionization

- Replace response-exposed dev tokens with real email delivery.
- Add SMTP mail adapter with configurable host/port/TLS/auth, production sender
  address, sender-domain readiness checks, retryable mail jobs, template
  rendering, and mail delivery/error audit logs.
- Add production Google OAuth with a real OAuth application, approved redirect
  URI, configured client ID/secret, secure state/nonce handling, verified-email
  account linking, explicit unlink behavior, audit logging, and failure-path
  tests.
- Preserve development-only token output behind explicit development mode.

### Phase 4: Workers, Queues, And Safety Jobs

- Add worker process mode.
- Add Redis-backed or PostgreSQL-backed queue execution depending on provider
  decision.
- Move scanning, cleanup, retries, notification, and billing reconciliation into
  workers.
- Beta default: run Redis-compatible queue/cache on the same host while keeping
  it non-authoritative and replaceable by a managed service.

### Phase 4A: File Scanning And Download Safety

- Persist scan status for each attachment: pending, clean, failed, and
  malicious.
- Require a clean scan result before public share downloads, public previews,
  and public export access.
- Allow private owner downloads while a file is pending or failed, but only with
  explicit scan-status UI and risk messaging.
- Block known malicious files globally for owner downloads, public shares,
  previews, exports, and future share creation.
- Add retryable scanner jobs, quarantine/block state, admin triage actions,
  audit logs, and tests for owner/public download gates.

### Phase 5: Billing Launch Readiness

- Enable Stripe and Epusdt for first production launch.
- Implement provider signature verification, idempotent reconciliation, order
  lifecycle, plan activation, plan expiry, Stripe refund/cancel handling,
  Epusdt transaction matching, webhook replay safety, and audit logs before
  enabling paid plans.

### Phase 6: Operations And Launch Gates

- Add structured logs, metrics, error reporting, health/readiness probes,
  backups, restore drill, deployment rollback, abuse runbook, legal pages, and
  support channels.
- Add single-VPS rollback behavior: previous image tag/digest, migration
  compatibility check, rollback runbook, and database restore procedure for
  non-reversible migrations.
- Enforce the launch SLO target: 99.9% monthly availability, 30-day backup
  retention, daily full logical backups plus WAL/PITR or equivalent
  point-in-time recovery, off-host backup storage, RPO 15 minutes, and RTO 4
  hours.
- Add backup integrity checks, failed-backup alerts, WAL lag alerts, disk
  pressure alerts, database health alerts, restore runbook, and a successful
  pre-launch restore drill. Local PostgreSQL on the beta host is acceptable only
  if the restore drill proves the target; otherwise PostgreSQL must move to a
  managed or standby-backed topology before launch.

### Phase 7: Legal, Support, And Compliance Launch Readiness

- Add Terms, Privacy Policy, Refund Policy, Abuse/DMCA, Cookie Notice/Consent,
  status/announcement page, support contact, account deletion instructions, and
  data export instructions.
- Add support email intake, abuse/DMCA intake, refund handling, GDPR/data-subject
  request workflow, DPA request path, data retention matrix, subprocessors list,
  and support/admin audit logging.
- Add runbooks for payment disputes, account deletion, data export requests,
  takedown requests, legal page updates, and subprocessors changes.

## Launch Gate Summary

Public beta can launch only after:

- PostgreSQL is the source of truth and app restart preserves users, sessions,
  pastes, shares, orders, audit logs, and jobs.
- Attachments are stored in managed S3-compatible object storage.
- SMTP email, Google OAuth, Stripe, and Epusdt run against production providers
  with signature/state/idempotency protections.
- Public file sharing obeys the confirmed scan policy.
- Single-VPS Docker Compose deployment, backups, WAL/PITR, health checks,
  observability, restore drill, rollback runbook, and legal/support surfaces are
  verified.

## Open Decisions

No launch-scope decision remains open. Continue implementation and verification
from the roadmap until repo-local gates plus operator-owned release evidence
prove the public beta launch gate.
