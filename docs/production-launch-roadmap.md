# PasteBox Production Launch Roadmap

Status: confirmed as implementation source of truth; implementation deferred to
a future session by user request.

This roadmap turns the production launch design into executable work phases. It
contains the confirmed launch decisions. Implementation should start from this
roadmap and `docs/production-launch-design.md` in a future session when the user
explicitly starts goal-mode implementation.

## Phase 0: Confirm Launch Decisions

Goal: lock the confirmed production launch scope before implementation.

Work:

- Keep launch target, region/provider, storage provider, queue provider, email
  provider, OAuth scope, scan policy, SLO, deployment topology, legal pages, and
  support channel aligned with `docs/production-launch-design.md`.
- Update `.trellis/spec/` with any executable contracts that affect code.

Exit gate:

- All decision matrix rows are confirmed or explicitly derived from confirmed
  launch requirements.
- User confirms the design is the implementation source of truth.

## Phase 0A: Single-VPS Production Baseline

Goal: establish the confirmed deployment and release foundation.

Work:

- Add production Docker Compose files for API, worker, PostgreSQL, Redis,
  reverse proxy/TLS, backup jobs, and monitoring/logging sidecars where needed.
- Add `.env` template and secret-management documentation for production-only
  credentials.
- Build immutable images and deploy by pinned image tag or digest.
- Add migration command, pre-deploy checks, health/readiness checks, worker
  supervision, reverse-proxy HTTPS config, certificate renewal checks, and
  rollback runbook.
- Store object data and backup artifacts off-host.

Exit gate:

- A fresh US VPS can be provisioned from the runbook.
- Deployment uses pinned image tags/digests and passes health/readiness checks.
- Rollback to the previous image is documented and tested for a reversible
  migration.
- Non-reversible migrations have an explicit backup/restore procedure before
  they are allowed in production.

## Phase 1: PostgreSQL Source Of Truth

Goal: eliminate in-memory source-of-truth state.

Work:

- Add migrations for users, sessions, auth tokens, pastes, attachments, shares,
  plans, prices, orders, webhook events, audit logs, reports, and jobs.
- Add sqlc query layer or an equivalent typed repository boundary.
- Implement repository interface parity with the current in-memory service.
- Add local dev database reset/seed workflow.
- Add integration tests for auth, paste CRUD, share, quota, billing stub,
  audit, export, and account deletion against PostgreSQL.

Exit gate:

- Restarting the app preserves users, sessions, pastes, shares, orders, and
  audit logs.
- `make test` passes with PostgreSQL-backed tests.

## Phase 2: S3-Compatible Object Storage

Goal: move file bytes out of process memory and app host disk.

Work:

- Add S3-compatible storage adapter with private bucket operations.
- Store attachment object keys, hashes, sizes, MIME metadata, image previews,
  and ref-count state in PostgreSQL.
- Implement upload rollback when metadata or object writes fail.
- Implement deletion queue and object lifecycle cleanup.
- Ensure owner and shared download authorization checks run before object reads.

Exit gate:

- Uploaded files survive app restart.
- Deleted or expired content is not downloadable.
- Object cleanup is retryable and audited.

## Phase 3: Worker Runtime And Queues

Goal: move background work out of request handlers.

Work:

- Add `pastebox worker` or equivalent process mode.
- Add queue tables and/or Redis-backed queue adapter.
- Implement workers for scan, cleanup, deletion retry, notification retry,
  export generation, and billing reconciliation.
- Add idempotency keys and retry/backoff semantics.

Exit gate:

- Worker can be restarted without losing jobs.
- Failed jobs are visible in admin queues and retryable.

## Phase 4: Auth, Email, And OAuth Productionization

Goal: remove development token exposure and make account flows production-safe.

Work:

- Add mail provider adapter and templates.
- Use the user's existing SMTP/enterprise email service for first launch while
  keeping the adapter provider-neutral for later Resend/SES migration.
- Send verification, magic-link, reset, security, and admin notification emails.
- Hide dev tokens outside explicit development mode.
- Add real Google OAuth for first beta, including OAuth app configuration,
  redirect-domain validation, secure state/nonce handling, account
  linking/unlinking, callback failure handling, and audit logs.
- Add session/device management and account recovery tests.

Exit gate:

- Production mode never returns auth tokens in JSON.
- New users can verify email and recover accounts through email.
- SMTP sender domain, credentials, TLS settings, and production sender address
  are configured and pass a delivery smoke test.
- Google OAuth login, account-linking conflicts, unlink behavior, callback
  failures, and CSRF/state mismatch are covered by tests before production
  OAuth is enabled.

## Phase 5: Safety, Abuse, And Scanning

Goal: reduce abuse risk for public uploads and share links.

Work:

- Add production security hardening for uploads, downloads, shares, reports,
  and admin/support actions.
- Add secure cookies, CSRF protection, secure headers, CORS allowlist, secret
  redaction, and production-only disabling of development token output.
- Add scanner worker integration.
- Persist pending, clean, failed, and malicious scan states for attachments.
- Enforce the confirmed download policy: public shared downloads/previews/exports
  require a clean scan; owner downloads may proceed while pending or failed with
  explicit scan-status UI; known malicious files are blocked globally.
- Add upload/download rate limits.
- Add report triage workflow, takedown actions, and audit trails.
- Add admin visibility for high-risk files, users, shares, and failed scans.

Exit gate:

- Production browser auth flows have CSRF protection, secure cookies, secure
  headers, CORS allowlist, and no development token output.
- Public shared downloads, previews, and exports obey scan policy.
- Owner downloads show scan status/risk messaging for pending and failed scans.
- Known malicious files are blocked for owner downloads, public access, previews,
  exports, and future share creation.
- Abuse reports can be triaged without direct database access.

## Phase 6: Billing Launch Readiness

Goal: make Stripe and Epusdt safe to enable for the first production launch.

Work:

- Implement Stripe/Epusdt signature verification, idempotency, order lifecycle,
  plan activation, expiry, refund/cancel handling, and reconciliation jobs.
- Add admin manual payment controls and audit logs.
- Add UI states for payment pending, paid, expired, canceled, refunded, and
  provider failure cases.
- Add support/admin workflows for duplicate webhook replay, stuck Epusdt
  payments, and manual correction.

Exit gate:

- Test-mode provider webhooks can be replayed safely.
- Duplicate webhook delivery does not double-activate plans.
- Stripe refund/cancel and Epusdt paid/expired cases are covered by tests.

## Phase 7: Operations And Launch Gate

Goal: make the service operable.

Work:

- Add readiness/health checks for database, object storage, Redis, mail, and
  worker status.
- Add structured logs, metrics, error reporting, backup jobs, restore drill,
  rollback procedure, and deployment runbook.
- Implement the confirmed SLO target: 99.9% monthly availability, 30-day backup
  retention, daily full logical backups, WAL/PITR or equivalent point-in-time
  recovery, off-host backup storage, RPO 15 minutes, and RTO 4 hours.
- Add backup integrity checks and alerts for failed backups, WAL lag, disk
  pressure, database health, queue lag, worker health, object-storage failures,
  mail failures, and certificate expiry.
- Add legal pages, privacy policy, terms, support contact, abuse contact, and
  data retention policy.

Exit gate:

- Restore drill succeeds from backup and proves RPO 15 minutes / RTO 4 hours.
- Local PostgreSQL remains acceptable only if the restore drill meets the
  confirmed target; otherwise the launch gate requires managed PostgreSQL or a
  standby-backed topology.
- Launch checklist is complete.
- Public beta deployment can be rolled back without data loss.

## Phase 8: Legal, Compliance, And Support Operations

Goal: make public beta supportable for real users, paid plans, uploads, and data
requests.

Work:

- Add public Terms, Privacy Policy, Refund Policy, Abuse/DMCA,
  Cookie Notice/Consent, status/announcement page, support contact page, account
  deletion instructions, and data export instructions.
- Add footer/legal navigation, billing support/refund links, account
  deletion/export entry points, and cookie consent where non-essential cookies
  are used.
- Add support email intake, abuse/DMCA intake, refund request handling,
  GDPR/data-subject request workflow, DPA request path, data retention matrix,
  subprocessors list, and support/admin audit logging.
- Add runbooks for payment disputes, account deletion, data export requests,
  takedown requests, legal page updates, and subprocessors changes.

Exit gate:

- Legal/support pages are reachable from the public UI.
- Account deletion and data export request paths are documented and testable.
- Refund, abuse/DMCA, GDPR/data-subject, and support intake workflows are
  documented with owner, SLA target, and audit trail.
- Data retention matrix and subprocessors list match the production architecture
  and configured providers.

## Cross-Phase Launch Checklist

- `make test` passes after each phase that changes code.
- Integration tests cover PostgreSQL persistence, S3 storage, Redis/queue
  behavior, worker idempotency, auth/email/OAuth, scan gates, Stripe/Epusdt,
  admin/support actions, and legal/support links.
- Production secrets are not committed and are documented through environment
  templates only.
- Restore drill proves RPO 15 minutes and RTO 4 hours.
- Deployment rollback and non-reversible migration restore procedures are
  rehearsed before public beta.
- The final public beta deployment uses pinned image tags or digests.
- `docs/production-launch-evidence-checklist.md` is completed for the release
  candidate with provider smoke tests, backup/PITR evidence, rollback evidence,
  monitoring evidence, and legal/support readiness.
