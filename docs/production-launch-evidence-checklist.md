# PasteBox Production Launch Evidence Checklist

This checklist is the operator-owned evidence record for accepting real public
beta traffic. The repository now contains the production runtime, deployment
runbooks, legal/support surfaces, and local verification gates, but final launch
approval still requires live-provider and VPS evidence that cannot be proven
from source code alone.

Create one completed copy of this file and one completed copy of
`docs/production-release-notes-template.md` per production release candidate.
Use `docs/production-provider-smoke-tests.md` for the provider evidence steps.
Store both completed artifacts with release notes or an operator-controlled
evidence archive. Before accepting public beta traffic, run
`make release-evidence RELEASE_CHECKLIST=<completed-checklist.md> RELEASE_NOTES=<completed-release-notes.md>`
against the sanitized completed copies. The validator rejects unchecked,
placeholder, missing, unapproved, or release-identity-mismatched evidence across
the checklist and release notes. Do not commit real secrets, raw provider
payloads, private object keys, or user data.

## Release Identity

- [ ] Release commit:
- [ ] Immutable image reference or digest:
- [ ] Production domain:
- [ ] Deployment window:
- [ ] Operator:
- [ ] Previous known-good image:
- [ ] Migration classification: no-migration / reversible /
  forward-compatible / non-reversible

## Repository Verification

- [ ] `make production-readiness` passed for the release commit.
- [ ] `make test` passed for the release commit.
- [ ] `make test-postgres` passed for the release commit, proving migrations
  and PostgreSQL-backed auth, content, billing/support, audit, mail, jobs, and
  restart-persistence integration tests against a real PostgreSQL server.
- [ ] `make build` passed for the release commit.
- [ ] `node scripts/check-web-launch-surfaces.mjs` passed after the production
  web bundle was built, proving committed legal/support/status routes and
  support/billing/settings links are present in the built frontend.
- [ ] `node scripts/check-release-evidence-template.mjs` passed, proving the
  release-notes template still covers image, migration, provider smoke,
  backup/PITR, rollback, monitoring, support, residual-risk, and launch-decision
  evidence.
- [ ] `node scripts/check-production-release-evidence.mjs --self-test` passed,
  proving the release-evidence validator accepts completed template-shaped
  evidence and rejects unchecked, missing, placeholder, or unapproved evidence.
- [ ] `node scripts/check-provider-smoke-runbook.mjs` passed, proving the live
  provider smoke-test runbook still covers managed S3, SMTP, Google OAuth,
  Stripe, Epusdt, and ClamAV evidence.
- [ ] `docker build -t pastebox:<release> .` passed, or CI produced the image
  for the exact release commit.
- [ ] `PASTEBOX_ENV_FILE=./deploy/production.env.example docker compose
  --env-file deploy/production.env.example -f compose.production.yaml config`
  renders with the committed template.
- [ ] `PASTEBOX_ENV_FILE=./deploy/production.env.example docker compose
  --env-file deploy/production.env.example -f compose.production.yaml
  --profile monitoring config` renders with the committed template.
- [ ] `PASTEBOX_ENV_FILE=./deploy/production.env.example docker compose
  --env-file deploy/production.env.example -f compose.production.yaml
  --profile maintenance config` renders with the committed template.
- [ ] Any changed backup scripts passed `sh -n`.
- [ ] `scripts/check-production-preflight.sh` passed, proving the committed
  production env template can be mapped to a complete production-shaped
  preflight environment.
- [ ] Any changed Prometheus config or rules passed `promtool` or equivalent
  syntax validation.

## Production Environment

- [ ] `deploy/production.env` exists only on the server and has mode `600`.
- [ ] `PASTEBOX_IMAGE` uses the release `sha-*` tag or image digest, never
  `latest`.
- [ ] `PASTEBOX_PUBLIC_URL` is the HTTPS production URL.
- [ ] `PASTEBOX_CORS_ALLOWED_ORIGINS` includes only exact production origins.
- [ ] `PASTEBOX_CSRF_SECRET` and `PASTEBOX_METRICS_TOKEN` are unique production
  values.
- [ ] `PASTEBOX_SUPPORT_EMAIL` and `PASTEBOX_ABUSE_EMAIL` route to monitored
  operator inboxes.
- [ ] Object-storage credentials and backup-storage credentials are separate
  where the provider supports separate keys.
- [ ] `pastebox preflight production` passed through the production Compose
  maintenance service.

## Deployment Evidence

- [ ] `docker compose --env-file deploy/production.env -f
  compose.production.yaml config` passed on the server.
- [ ] `docker compose --env-file deploy/production.env -f
  compose.production.yaml pull` pulled the pinned image.
- [ ] `pastebox migrate status` showed only expected pending/applied
  migrations before deploy.
- [ ] `pastebox migrate up` completed successfully.
- [ ] `pastebox migrate status` showed no dirty migration after deploy.
- [ ] `api`, `worker`, `postgres`, `redis`, `clamav`, and `caddy` services are
  running.
- [ ] `curl -fsS https://<domain>/readyz` returned `status=ready`.
- [ ] `curl -fsS https://<domain>/api/v1/ready` returned `status=ready`.
- [ ] Readiness includes `scanner` with `status=ok`, proving ClamAV TCP
  reachability from the API container.
- [ ] Readiness includes `worker` with `status=ok`, proving a fresh worker
  heartbeat within `PASTEBOX_WORKER_HEARTBEAT_MAX_AGE_SECONDS`.
- [ ] `pastebox worker --once` completed without failed runnable jobs.

## Provider Smoke Tests

- [ ] Provider smoke tests followed `docs/production-provider-smoke-tests.md`
  and sanitized evidence was copied into the release notes.
- [ ] Managed S3-compatible object storage accepted upload, private read, and
  delete through the application path.
- [ ] SMTP delivered verification, magic-link, reset, security, billing, and
  account-deletion messages to controlled test mailboxes.
- [ ] Google OAuth login completed against the production OAuth application and
  authorized redirect URI.
- [ ] Google OAuth state mismatch failed without creating a session.
- [ ] Stripe order creation returned a production checkout URL, not a
  development `/dev/checkout` URL.
- [ ] Stripe signed webhook replay succeeded and duplicate replay was
  idempotent.
- [ ] Stripe refund or cancellation test-mode webhook reached the expected
  lifecycle state.
- [ ] Epusdt order creation returned the configured production checkout URL,
  chain, and receiving address, not the development test address.
- [ ] Epusdt signed success callback activated the matching order.
- [ ] Epusdt expired or canceled callback reached the expected lifecycle state.
- [ ] ClamAV scan marked a known clean file `clean`.
- [ ] ClamAV or test scanner marked a known malicious test file `malicious` and
  public and owner downloads were blocked.

## Security And Browser Gates

- [ ] Session cookies are `Secure` on HTTPS requests.
- [ ] Unsafe browser API requests require the double-submit CSRF token.
- [ ] Provider webhook routes reject unsigned callbacks and are excluded from
  browser CSRF.
- [ ] API and static responses include secure browser headers.
- [ ] Credentialed CORS works only for configured production origins.
- [ ] Auth, browser write, upload, download, and webhook rate-limit buckets are
  enabled with positive limits.
- [ ] `/metrics` rejects missing/wrong bearer tokens.
- [ ] `/metrics` succeeds with the production metrics token.
- [ ] Logs and metrics do not contain secrets, paste bodies, OAuth tokens,
  webhook secrets, or object-storage credentials.

## Backup, PITR, And Rollback

- [ ] Logical backup completed and produced a checksum.
- [ ] Logical restore drill completed from the latest backup.
- [ ] WAL archive freshness check completed within the 15-minute RPO target.
- [ ] PITR base backup completed and `pg_verifybackup=passed` is recorded in
  the manifest.
- [ ] PITR restore drill completed and read `schema_migrations`.
- [ ] PITR drill duration was recorded and is within the 4-hour RTO target.
- [ ] Off-host backup push completed, `backup-push` printed
  `snapshot_id=<id>`, and `/backups/restic/pastebox-restic-*.manifest`
  recorded the same snapshot ID with `integrity_check=passed`.
- [ ] Reversible image rollback was rehearsed with the previous known-good
  image.
- [ ] Non-reversible migration restore procedure was rehearsed when applicable.

## Monitoring And Alerts

- [ ] Prometheus or external monitoring scrapes `/metrics` with the bearer
  token, plus Caddy metrics, host metrics, backup textfile metrics, and HTTPS
  blackbox probe metrics.
- [ ] Alert rules cover scrape failure, readiness, scanner readiness, failed
  jobs, scanner backlog, mail backlog, failed outbound mail, open support/abuse
  report backlog, stale backup/WAL evidence, restore-drill freshness, RTO
  breach, host resources, and certificate expiry.
- [ ] Certificate renewal status was checked through Caddy logs or equivalent
  provider tooling.
- [ ] Disk, CPU, memory, PostgreSQL health, WAL lag, backup failure, queue lag,
  object-storage failure, mail failure, and certificate-expiry alerts are
  enabled.
- [ ] Operator escalation targets for alerts are configured.

## Legal, Support, And Product Operations

- [ ] `/legal`, `/legal/terms`, `/legal/privacy`, `/legal/refund`,
  `/legal/abuse`, `/legal/cookies`, `/legal/account-deletion`,
  `/legal/data-export`, `/legal/data-retention`, `/legal/subprocessors`,
  `/support`, and `/status` are reachable by direct deep link.
- [ ] `/support` renders the configured support and abuse addresses from
  `GET /api/v1/support/contacts`.
- [ ] Billing links point users to refund and support instructions.
- [ ] Settings links expose account deletion, data export, privacy, and support
  instructions.
- [ ] Operators can resolve support/abuse reports without direct database
  edits.
- [ ] Manual billing corrections require a support reason and produce audit
  logs.
- [ ] Account deletion request, cancellation, execution, and export requests
  produce audit logs.
- [ ] Data retention matrix and subprocessor list match the deployed providers.
- [ ] Public status or announcement process is ready for incidents and
  maintenance notices.

## Launch Decision

- [ ] All required evidence above is complete.
- [ ] Any skipped item is explicitly justified with owner, risk, and deadline.
- [ ] Release notes use `docs/production-release-notes-template.md` and include
  image, migration class, backup/PITR evidence, rollback evidence, provider
  smoke-test results, and known residual risks.
- [ ] `make release-evidence RELEASE_CHECKLIST=<completed-checklist.md>
  RELEASE_NOTES=<completed-release-notes.md>` passed against the sanitized
  completed release-candidate evidence files.
- [ ] Operator approved public beta traffic.
