# PasteBox Production Deployment Runbook

This runbook implements Phase 0A of the production launch roadmap: a single US
VPS running Docker Compose with an API container, worker container, PostgreSQL,
Redis, HTTPS reverse proxy, and off-host backup flow.

The stack is still gated by operator-owned launch evidence. `pastebox migrate
up` applies the PostgreSQL schema foundation, and the worker can process
cleanup, scan, billing reconciliation, and queued mail jobs. WAL/PITR
maintenance services are present, but operators must still execute and record
provider smoke tests, restore drill evidence, PITR drill duration, rollback
rehearsal, and compliance/support readiness before real user data or paid
traffic is allowed.

## Files

- `compose.production.yaml`: production Compose stack.
- `deploy/production.env.example`: production environment template.
- `deploy/caddy/Caddyfile`: HTTPS reverse proxy and certificate renewal.
- `deploy/monitoring/prometheus.yml`: optional production Prometheus scrape
  configuration for the protected PasteBox metrics endpoint, Caddy metrics,
  host metrics, backup textfile metrics, and HTTPS probing.
- `deploy/monitoring/pastebox-alerts.yml`: baseline production alert rules for
  readiness, worker queues, mail backlog, support/abuse report backlog, host
  resources, certificate expiry, and backup/WAL freshness.
- `deploy/monitoring/blackbox.yml`: blackbox exporter probe configuration for
  HTTPS and certificate-expiry checks.
- `deploy/monitoring/textfile-metrics.sh`: helper used by backup and restore
  maintenance jobs to publish node-exporter textfile metrics.
- `deploy/postgres/pg_hba.conf`: PostgreSQL password-auth rules, including
  replication access for PITR base backups from maintenance containers.
- `deploy/backup/postgres-backup.sh`: logical PostgreSQL backup job.
- `deploy/backup/postgres-basebackup.sh`: PITR base backup job.
- `deploy/backup/postgres-wal-check.sh`: WAL archive freshness check.
- `deploy/backup/postgres-restore-drill.sh`: scratch database restore drill.
- `deploy/backup/postgres-pitr-restore-drill.sh`: scratch PITR restore drill.
- `deploy/backup/restic-backup.sh`: off-host backup push and integrity check.
- `scripts/check-production-readiness.sh`: local release-candidate verifier for
  Compose rendering, maintenance script syntax, monitoring config syntax,
  Caddy config syntax, tests, builds, and Docker image build.
- `.github/workflows/docker-image.yml`: CI image workflow; it runs the
  production-readiness gate before publishing immutable `sha-*` image tags.
- `docs/production-rollback-runbook.md`: image rollback and restore gates.
- `docs/production-secrets.md`: secret handling checklist.
- `docs/production-release-notes-template.md`: release-candidate notes template
  for image, migration, provider, backup/PITR, rollback, monitoring, support,
  residual-risk, and launch-decision evidence.
- `docs/production-provider-smoke-tests.md`: operator runbook for managed S3,
  SMTP, Google OAuth, Stripe, Epusdt, and ClamAV smoke-test evidence.
- `docs/production-support-operations-runbook.md`: legal, support, refund,
  abuse, data-rights, retention, and subprocessor workflows.
- `docs/production-launch-evidence-checklist.md`: release-candidate evidence
  checklist for accepting public beta traffic.
- `scripts/check-production-release-evidence.mjs`: operator-side validator for
  completed sanitized release notes and evidence checklist copies.

## Fresh VPS Provisioning

1. Create a US-region Linux VPS with enough disk for PostgreSQL, Docker logs,
   and at least one local backup staging copy.
2. Point the production DNS A/AAAA record at the VPS.
3. Install Docker Engine and the Docker Compose plugin.
4. Create the deployment directory:

   ```sh
   sudo mkdir -p /opt/pastebox
   sudo chown "$USER:$USER" /opt/pastebox
   cd /opt/pastebox
   ```

5. Copy these runtime files into `/opt/pastebox` on the server:

   ```text
   compose.production.yaml
   deploy/production.env.example
   deploy/caddy/Caddyfile
   deploy/monitoring/prometheus.yml
   deploy/monitoring/pastebox-alerts.yml
   deploy/monitoring/blackbox.yml
   deploy/monitoring/textfile-metrics.sh
   deploy/postgres/pg_hba.conf
   deploy/backup/postgres-backup.sh
   deploy/backup/postgres-basebackup.sh
   deploy/backup/postgres-wal-check.sh
   deploy/backup/restic-backup.sh
   deploy/backup/postgres-restore-drill.sh
   deploy/backup/postgres-pitr-restore-drill.sh
   ```

6. Keep a release checkout or operator-controlled evidence workspace for the
   repo-local verification scripts and release evidence artifacts. It must
   contain at least these files when validating completed evidence:

   ```text
   docs/production-launch-evidence-checklist.md
   docs/production-release-notes-template.md
   docs/production-provider-smoke-tests.md
   docs/production-support-operations-runbook.md
   docs/production-rollback-runbook.md
   scripts/check-production-release-evidence.mjs
   ```

   Do not store real secrets, raw provider payloads, private object keys, or
   user data in the repository checkout. Store completed sanitized release
   evidence in the operator evidence archive.

7. Create the real environment file:

   ```sh
   cp deploy/production.env.example deploy/production.env
   chmod 600 deploy/production.env
   ```

8. Edit `deploy/production.env` and replace every `CHANGE_ME` value. Set
   `PASTEBOX_IMAGE` to an immutable image reference:

   ```text
   ghcr.io/cvinit/pastebox:sha-<commit>
   ```

   A registry digest such as `ghcr.io/cvinit/pastebox@sha256:<digest>` is also
   valid. Do not deploy `latest`.

## Pre-Deploy Checks

Run the repo-local release-candidate gate from the exact release checkout or an
equivalent operator-owned CI job before starting or upgrading the stack:

```sh
make production-readiness
node scripts/check-production-release-evidence.mjs --self-test
```

Then run these checks from `/opt/pastebox` on the server:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml config
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm preflight
docker compose --env-file deploy/production.env -f compose.production.yaml pull
```

`make production-readiness` uses the committed `deploy/production.env.example`
by default and proves the repo-local release candidate gates: production
Compose rendering for the default, monitoring, and maintenance profiles, shell
syntax for maintenance scripts, Prometheus/blackbox/Caddy config syntax through
container images, `pastebox preflight production` against a synthetic
production-safe environment derived from `deploy/production.env.example`,
`make test`, the web launch-surface smoke check for legal/support/status
routes and support/billing/settings links, the release evidence template check,
the release evidence validator self-test, the provider smoke-test runbook
check, PostgreSQL-backed integration tests in an ephemeral container,
`make build`, and a local Docker image build. To run the same verifier against
a server-specific env file without committing it, set
`PASTEBOX_PRODUCTION_ENV_FILE=deploy/production.env`. To skip the local image
build only when CI has already built the exact release image, set
`PASTEBOX_SKIP_DOCKER_BUILD=true`.

The GitHub Actions image workflow runs the same production-readiness gate before
the Buildx publish step. A production image tag is not acceptable launch
evidence unless that workflow passed for the exact release commit or an
equivalent operator-owned CI job recorded the same checks.

The production preflight fails if `PASTEBOX_IMAGE` is mutable, if
`PASTEBOX_PUBLIC_URL` is not a root HTTPS production origin, if
`PASTEBOX_DOMAIN` is not a production hostname matching the
`PASTEBOX_PUBLIC_URL` host, if `PASTEBOX_ADMIN_EMAIL`,
`PASTEBOX_SUPPORT_EMAIL`, or `PASTEBOX_ABUSE_EMAIL` is missing, local, or not a
plain production email address, if `PASTEBOX_CSRF_SECRET` is missing or left at
the development default, if `PASTEBOX_METRICS_TOKEN` is missing or too short,
if `PASTEBOX_CORS_ALLOWED_ORIGINS` is missing, wildcarded, local, HTTP, or does
not include the exact `PASTEBOX_PUBLIC_URL` origin, if production rate limits
are disabled or non-positive, if Google OAuth client settings are missing, if
SMTP is not configured for TLS delivery, if the bootstrap admin password is
short or placeholder-like, if `PASTEBOX_S3_ENDPOINT` points to a local or HTTP
object store, if `PASTEBOX_RESTIC_REPOSITORY` is not an off-host `s3:https://`
repository, or if backup S3 credentials reuse the attachment object-storage
credentials. Use managed S3-compatible storage for attachment objects and a
separate off-host S3-compatible restic repository for backups.
Preflight also rejects `CHANGE_ME` placeholders and documentation-only hostnames
such as `example.com`, `.example.com`, `.test`, `.invalid`, localhost,
single-label internal hostnames, and IP literals for production-facing URLs,
email domains, CORS origins, OAuth redirects, SMTP, object storage, backup
repositories, and payment checkout templates. The committed
`deploy/production.env.example` is only a key inventory; copy it to the server
as `deploy/production.env` and replace every placeholder with operator-owned
real values before running preflight.
`PASTEBOX_WAL_ARCHIVE_TIMEOUT_SECONDS` and
`PASTEBOX_WAL_ARCHIVE_MAX_AGE_SECONDS` must both be positive and no more than
`900`; `PASTEBOX_WAL_ARCHIVE_WAIT_SECONDS` must also be positive and no more
than `900` so the local PostgreSQL topology can support the 15-minute RPO
target.

The first production launch also requires billing to be enabled with real
provider callback and checkout settings: `PASTEBOX_STRIPE_ENABLED=true`,
`PASTEBOX_STRIPE_WEBHOOK_SECRET=whsec_...`,
`PASTEBOX_STRIPE_CHECKOUT_URL_TEMPLATE=https://...`,
`PASTEBOX_EPUSDT_ENABLED=true`, `PASTEBOX_EPUSDT_PID`,
`PASTEBOX_EPUSDT_SECRET_KEY`, `PASTEBOX_EPUSDT_CHECKOUT_URL_TEMPLATE=https://...`,
`PASTEBOX_EPUSDT_ADDRESS`, and `PASTEBOX_EPUSDT_CHAIN`. Provider webhook routes
are excluded from browser CSRF but reject unsigned or incorrectly signed
callbacks. Production order creation fails closed instead of returning
development checkout URLs or test USDT addresses when these checkout settings
are missing or invalid.

The Google OAuth app must include the production authorized redirect URI for
the real `PASTEBOX_PUBLIC_URL` host:

```text
https://<production-domain>/api/v1/auth/google/callback
```

SMTP must use the confirmed enterprise mail service with
`PASTEBOX_MAILER_PROVIDER=smtp`, a production host, valid credentials, a
production sender address, and either `PASTEBOX_SMTP_TLS_MODE=starttls` or
`PASTEBOX_SMTP_TLS_MODE=tls`. Plain SMTP is rejected in production preflight.
Scanning must use `PASTEBOX_SCANNER_PROVIDER=clamav` with a reachable
`PASTEBOX_CLAMAV_ADDR` such as `clamav:3310`.

PasteBox adds app-level browser hardening headers on API and static responses:
`X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`,
`Permissions-Policy`, and a same-origin Content Security Policy. Keep the Caddy
headers as the edge defense layer, but do not rely on Caddy as the only source
of secure headers. API CORS is credentialed only for exact origins listed in
`PASTEBOX_CORS_ALLOWED_ORIGINS`; do not use wildcard origins with browser
cookies.

Production HTTP rate limits are enabled by default through
`PASTEBOX_RATE_LIMIT_ENABLED=true`. The process-local fixed-window limiter
covers auth, browser write, upload, download, and provider webhook surfaces by
IP, and by user ID when a valid session cookie is present. Keep all
`PASTEBOX_RATE_LIMIT_*` limits positive for production. The current single-VPS
Compose baseline runs one API replica; if multiple API replicas are introduced,
replace the process-local buckets with Redis-backed counters before scaling
traffic horizontally.

To validate the committed template without creating a real secret file:

```sh
PASTEBOX_ENV_FILE=./deploy/production.env.example docker compose --env-file deploy/production.env.example -f compose.production.yaml config
PASTEBOX_ENV_FILE=./deploy/production.env.example docker compose --env-file deploy/production.env.example -f compose.production.yaml --profile monitoring config
```

Run the migration command before traffic switch:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm migrate
```

The command applies embedded SQL migrations and records applied versions in
`schema_migrations`. Treat any checksum mismatch or failed migration as a hard
release stop.

## Bootstrap Admin

Set `PASTEBOX_BOOTSTRAP_ADMIN_EMAIL` and `PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD`
in `deploy/production.env` before the first API start. The API process creates
or updates that account at startup, marks it verified, grants the `admin` role,
and replaces the password with the configured value. Startup fails instead of
silently continuing if the bootstrap email or password is invalid.

After the first successful admin login, rotate the bootstrap password or remove
the bootstrap variables from the real production environment so routine
restarts do not keep resetting the administrator password.

## Start Or Upgrade

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml up -d postgres redis
docker compose --env-file deploy/production.env -f compose.production.yaml up -d api worker caddy
docker compose --env-file deploy/production.env -f compose.production.yaml ps
```

Verify local readiness:

```sh
curl -fsS http://127.0.0.1/readyz
curl -fsS https://<production-domain>/readyz
curl -fsS https://<production-domain>/api/v1/ready
```

Expected responses:

```json
{"app":"PasteBox","env":"production","status":"ready","components":[{"name":"database","status":"ok"},{"name":"object_storage","status":"ok"},{"name":"redis","status":"ok"},{"name":"scanner","status":"ok"},{"name":"worker_queue","status":"ok"},{"name":"worker","status":"ok"},{"name":"mail","status":"ok"}]}
```

## Metrics And Alerting

The API exposes Prometheus text metrics at `/metrics`. The endpoint is not
browser-authenticated and must be scraped with the production metrics bearer
token:

```sh
curl -fsS -H "Authorization: Bearer $PASTEBOX_METRICS_TOKEN" https://<production-domain>/metrics
```

Do not put `PASTEBOX_METRICS_TOKEN` in dashboards, public URLs, or shared
screenshots. The optional in-stack Prometheus profile reads this token as a
Compose secret from `PASTEBOX_METRICS_TOKEN`, renders the configured
`PASTEBOX_PUBLIC_URL` into the blackbox scrape target, and mounts committed
scrape, probe, and alert-rule files:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml --profile monitoring up -d prometheus
docker compose --env-file deploy/production.env -f compose.production.yaml --profile monitoring exec prometheus promtool check config /etc/prometheus/prometheus.yml
docker compose --env-file deploy/production.env -f compose.production.yaml --profile monitoring exec blackbox /bin/blackbox_exporter --version
```

Keep Prometheus private to the Compose network or a protected operator network;
the production Compose file intentionally exposes port `9090` only to sibling
containers. The monitoring profile also runs Caddy internal metrics,
node-exporter with a textfile collector, and blackbox-exporter for the
production HTTPS URL. If you use an external monitoring provider instead,
import `deploy/monitoring/pastebox-alerts.yml` or equivalent rules, keep the
same authorization header for `/metrics`, and provide equivalent host,
certificate, backup, WAL, and restore-drill signals.

The committed baseline alert rules cover these launch gates:

- `PasteBoxMetricsScrapeDown` for failed `/metrics` scraping.
- `PasteBoxCaddyMetricsScrapeDown` for failed Caddy metrics scraping.
- `PasteBoxNodeExporterDown` for failed host/textfile metrics scraping.
- `PasteBoxHttpsProbeDown` for failed public HTTPS probing.
- `PasteBoxCertificateExpiresSoon` for certificates expiring within 14 days.
- `PasteBoxReadinessDown` for overall dependency readiness failures.
- `PasteBoxReadinessComponentDown` for database, object storage, Redis,
  scanner, worker queue, worker heartbeat, or mail readiness failures.
- `PasteBoxOperationalMetricsUnavailable` when aggregate operational metrics
  cannot be loaded.
- `PasteBoxFailedWorkerJobs` for failed durable worker jobs.
- `PasteBoxScannerBacklog` for scan queue lag.
- `PasteBoxMailBacklog` for mail delivery backlog.
- `PasteBoxMailFailures` for outbound mail that exhausted delivery retries.
- `PasteBoxOpenReportsBacklog` for unresolved abuse/support report load.
- `PasteBoxHostDiskPressure`, `PasteBoxHostMemoryPressure`, and
  `PasteBoxHostCpuPressure` for VPS resource pressure.
- `PasteBoxLogicalBackupStale`, `PasteBoxWalArchiveStale`,
  `PasteBoxBaseBackupStale`, `PasteBoxRestoreDrillStale`,
  `PasteBoxPitrDrillStale`, `PasteBoxPitrDrillRtoExceeded`, and
  `PasteBoxOffHostBackupStale` for backup, RPO, restore-drill, RTO, and
  off-host backup evidence freshness.

The backup and restore maintenance jobs update node-exporter textfile metrics
only after successful runs. A missing textfile metric is treated as stale by the
baseline rules. Prometheus rules cannot prove backup evidence by themselves:
release notes must still record fresh logical backup, WAL freshness check,
base-backup manifest, restore-drill evidence, PITR drill evidence, and
off-host backup push evidence before public beta traffic is accepted.

## Worker Supervision

The `worker` service runs `pastebox worker` under Docker Compose with
`restart: unless-stopped`. The worker polls the PostgreSQL-backed `jobs` table
and currently processes pending `cleanup`, `scan`, and `billing_reconcile` jobs
through the same production service wiring as the API. Scan jobs use the
configured ClamAV scanner and update attachment scan state to `clean`,
`scan_failed`, or `malicious`. The same worker also drains queued mail through
the configured SMTP sender and preserves retry/failure state in PostgreSQL.
The worker service disables the image-level `/readyz` healthcheck because the
worker process does not serve HTTP.
Each worker loop records a PostgreSQL-backed heartbeat under
`PASTEBOX_WORKER_ID`; production readiness fails when the latest heartbeat is
missing or older than `PASTEBOX_WORKER_HEARTBEAT_MAX_AGE_SECONDS`.
`PasteBoxReadinessComponentDown{name="worker"}` means the worker container is
stopped, cannot reach PostgreSQL, or has not completed a poll within the launch
threshold.
`PasteBoxMailFailures` should be triaged from the admin Queues panel by checking
recipient, subject, attempts, last error, and SMTP/provider status before
restarting the worker or changing mail credentials. Use the bounded one-shot
mode for deployment checks or maintenance:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml run --rm worker worker --once
```

Future worker extensions should use the same durable `jobs` table or `mails`
queue pattern so retry state survives process restarts.

## HTTPS And Certificate Renewal

Caddy terminates HTTPS for `PASTEBOX_DOMAIN`, forwards
`X-Forwarded-Proto: https`, and renews certificates automatically. Keep ports
80 and 443 reachable from the public internet so HTTP-01/HTTPS validation and
renewal continue to work.

Check renewal health:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml logs --tail=200 caddy
docker compose --env-file deploy/production.env -f compose.production.yaml exec caddy caddy list-certificates
```

## Backups

PostgreSQL runs with `archive_mode=on`, `wal_level=replica`, and
`archive_timeout=$PASTEBOX_WAL_ARCHIVE_TIMEOUT_SECONDS`. Archived WAL segments
are staged under `/backups/wal` in the shared backup volume and must be pushed
off-host by `backup-push`.

Run a logical PostgreSQL backup:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm postgres-backup
```

Run a restore drill against the latest local logical backup:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm postgres-restore-drill
```

To drill a specific backup file, set `PASTEBOX_RESTORE_SOURCE` to the path under
the backup volume, for example `/backups/postgres/pastebox-20260525T120000Z.sql.gz`.
The drill verifies the `.sha256`, restores into
`PASTEBOX_RESTORE_DRILL_DATABASE` (default `pastebox_restore_drill`), checks the
`schema_migrations` table, drops the scratch database unless
`PASTEBOX_KEEP_RESTORE_DRILL_DB=true`, prints `duration_seconds`, and updates
the `pastebox_restore_drill_*` textfile metrics. Record that duration and the
backup path in release notes.

Check WAL archive freshness. Schedule this at least every 15 minutes:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm postgres-wal-check
```

Create a PITR base backup. Schedule this at least daily:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm postgres-basebackup
```

The base-backup job verifies the backup with `pg_verifybackup` against the WAL
archive, then writes `/backups/basebackups/pastebox-base-*.tar.gz`, `*.sha256`,
and `*.manifest`, and updates the `pastebox_basebackup_*` textfile metrics.
The manifest records the latest archived WAL file, the WAL age, backup
duration, size, `pg_verifybackup=passed`, `rpo_target_seconds=900`, and
`rto_target_seconds=14400`.

Run a scratch PITR restore drill against the latest local base backup and WAL
archive:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm postgres-pitr-drill
```

To drill a specific base backup or target time, set
`PASTEBOX_PITR_SOURCE_BASE` and `PASTEBOX_PITR_TARGET_TIME`, for example:

```sh
PASTEBOX_PITR_SOURCE_BASE=/backups/basebackups/pastebox-base-20260525T120000Z.tar.gz \
PASTEBOX_PITR_TARGET_TIME="2026-05-25 12:10:00+00" \
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm postgres-pitr-drill
```

The PITR drill verifies the base-backup checksum, runs `pg_verifybackup` against
the WAL archive, replays `/backups/wal` into an isolated temporary PostgreSQL
data directory, waits for recovery to promote, checks `schema_migrations`,
prints `duration_seconds`, and removes the temporary data directory unless
`PASTEBOX_KEEP_PITR_DRILL_DIR=true`. Successful runs update
`pastebox_pitr_drill_*` textfile metrics. Tune
`PASTEBOX_PITR_RECOVERY_WAIT_SECONDS` only when a larger backup needs more local
replay time; record the base backup path, target time, duration, latest WAL age,
and whether the run met the 4-hour RTO target.

Push all local backup artifacts off-host after logical backup, WAL check, and
base backup jobs:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm backup-push
```

The off-host backup job prints `snapshot_id=<id>` and writes a manifest under
`/backups/restic/pastebox-restic-*.manifest` containing the restic snapshot ID,
duration, `read_data_subset=1/20`, and `integrity_check=passed`. Record that
snapshot ID in release notes and in the production launch evidence checklist.

Successful logical backup, WAL freshness, base backup, restore drill, PITR
drill, and off-host push jobs write Prometheus textfile metrics into the shared
`pastebox-node-textfile` volume. The monitoring profile exposes those metrics
through node-exporter so missing or stale maintenance evidence can page before
the launch RPO/RTO target is violated.

Schedule logical backups, base backups, and off-host pushes at least daily.
Schedule WAL freshness checks at least every 15 minutes. The confirmed launch
target requires 30-day retention, off-host backup storage, RPO 15 minutes, and
RTO 4 hours; public beta requires successful logical restore and PITR restore
drill evidence before real traffic.

## Launch Gate

Phase 0A is complete when a fresh VPS can follow this runbook, Compose uses a
pinned image tag or digest, readiness checks pass, logical and PITR backups can
be pushed off-host, the monitoring profile or external equivalent has the
baseline alert rules loaded, PITR drill duration is recorded, and rollback has
been rehearsed for a reversible migration.

The public beta launch gate additionally requires
`docs/production-support-operations-runbook.md` to match the deployed public
legal/support pages, configured subprocessors, data-retention behavior, and
support/admin audit workflows. Complete
`docs/production-launch-evidence-checklist.md` and
`docs/production-release-notes-template.md`, using
`docs/production-provider-smoke-tests.md` for live provider evidence, for each
release candidate before accepting public beta traffic. Run
`node scripts/check-production-release-evidence.mjs --checklist <completed-checklist.md> --release-notes <completed-release-notes.md>`
against the completed sanitized copies before operator approval.
