# PasteBox Production Deployment Runbook

This runbook implements Phase 0A of the production launch roadmap: a single US
VPS running Docker Compose with an API container, worker container, PostgreSQL,
Redis, HTTPS reverse proxy, and off-host backup flow.

The stack is still gated by later roadmap phases. `pastebox migrate up` applies
the PostgreSQL schema foundation, but mail delivery, billing, scanning, restore
drills, and compliance work still need to be completed before real user data or
paid traffic is allowed.

## Files

- `compose.production.yaml`: production Compose stack.
- `deploy/production.env.example`: production environment template.
- `deploy/caddy/Caddyfile`: HTTPS reverse proxy and certificate renewal.
- `deploy/backup/postgres-backup.sh`: logical PostgreSQL backup job.
- `deploy/backup/restic-backup.sh`: off-host backup push and integrity check.
- `docs/production-rollback-runbook.md`: image rollback and restore gates.
- `docs/production-secrets.md`: secret handling checklist.

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

5. Copy these repository files into `/opt/pastebox`:

   ```text
   compose.production.yaml
   deploy/production.env.example
   deploy/caddy/Caddyfile
   deploy/backup/postgres-backup.sh
   deploy/backup/restic-backup.sh
   ```

6. Create the real environment file:

   ```sh
   cp deploy/production.env.example deploy/production.env
   chmod 600 deploy/production.env
   ```

7. Edit `deploy/production.env` and replace every `CHANGE_ME` value. Set
   `PASTEBOX_IMAGE` to an immutable image reference:

   ```text
   ghcr.io/cvinit/pastebox:sha-<commit>
   ```

   A registry digest such as `ghcr.io/cvinit/pastebox@sha256:<digest>` is also
   valid. Do not deploy `latest`.

## Pre-Deploy Checks

Run these checks before starting or upgrading the stack:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml config
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm preflight
docker compose --env-file deploy/production.env -f compose.production.yaml pull
```

The production preflight fails if `PASTEBOX_IMAGE` is mutable, if
`PASTEBOX_PUBLIC_URL` is not HTTPS, if `PASTEBOX_CSRF_SECRET` is missing or
left at the development default, if Google OAuth client settings are missing, if
SMTP is not configured for TLS delivery, if `PASTEBOX_S3_ENDPOINT` points to a
local or HTTP object store, or if `PASTEBOX_RESTIC_REPOSITORY` is not an
off-host `s3:https://` repository. Use managed S3-compatible storage for
attachment objects and a separate off-host S3-compatible restic repository for
backups.

The Google OAuth app must include this authorized redirect URI:

```text
https://pastebox.example.com/api/v1/auth/google/callback
```

SMTP must use the confirmed enterprise mail service with
`PASTEBOX_MAILER_PROVIDER=smtp`, a production host, valid credentials, a
production sender address, and either `PASTEBOX_SMTP_TLS_MODE=starttls` or
`PASTEBOX_SMTP_TLS_MODE=tls`. Plain SMTP is rejected in production preflight.

To validate the committed template without creating a real secret file:

```sh
PASTEBOX_ENV_FILE=./deploy/production.env.example docker compose --env-file deploy/production.env.example -f compose.production.yaml config
```

Run the migration command before traffic switch:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm migrate
```

The command applies embedded SQL migrations and records applied versions in
`schema_migrations`. Treat any checksum mismatch or failed migration as a hard
release stop.

## Start Or Upgrade

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml up -d postgres redis
docker compose --env-file deploy/production.env -f compose.production.yaml up -d api worker caddy
docker compose --env-file deploy/production.env -f compose.production.yaml ps
```

Verify local readiness:

```sh
curl -fsS http://127.0.0.1/readyz
curl -fsS https://pastebox.example.com/readyz
curl -fsS https://pastebox.example.com/api/v1/ready
```

Expected responses:

```json
{"status":"ready"}
```

and:

```json
{"app":"PasteBox","env":"production","status":"ready"}
```

## Worker Supervision

The `worker` service runs `pastebox worker` under Docker Compose with
`restart: unless-stopped`. The worker polls the PostgreSQL-backed `jobs` table
and currently processes pending `cleanup` jobs that expire and delete content
through the same production service wiring as the API. Use the bounded one-shot
mode for deployment checks or maintenance:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml run --rm worker --once
```

Future phases will attach scan, deletion retry, notification retry, export, and
billing reconciliation jobs to this same durable worker runtime.

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

Run a logical PostgreSQL backup and push it off-host:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm postgres-backup
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm backup-push
```

Schedule both commands from the host with cron or a systemd timer at least
daily. The confirmed launch target requires 30-day retention, off-host backup
storage, RPO 15 minutes, and RTO 4 hours. Daily logical backups alone are not
enough for final launch; Phase 7 must add WAL/PITR or an equivalent
point-in-time recovery path before public beta.

## Launch Gate

Phase 0A is complete when a fresh VPS can follow this runbook, Compose uses a
pinned image tag or digest, readiness checks pass, backups can be pushed
off-host, and rollback has been rehearsed for a reversible migration.
