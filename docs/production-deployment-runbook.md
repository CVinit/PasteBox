# PasteBox Production Deployment Runbook

This runbook implements Phase 0A of the production launch roadmap: a single US
VPS running Docker Compose with an API container, worker container, PostgreSQL,
Redis, HTTPS reverse proxy, and off-host backup flow.

The stack is still gated by later roadmap phases. `pastebox migrate up` is
intentionally a failing guard until Phase 1 adds real PostgreSQL migrations.
Do not put real user data or paid traffic on this stack until the launch
checklist in `docs/production-launch-roadmap.md` is complete.

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

To validate the committed template without creating a real secret file:

```sh
PASTEBOX_ENV_FILE=./deploy/production.env.example docker compose --env-file deploy/production.env.example -f compose.production.yaml config
```

Run the migration command before traffic switch once Phase 1 implements real
migrations:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm migrate
```

For the current Phase 0A baseline, this command fails by design because no
production schema exists yet.

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
`restart: unless-stopped`. In Phase 0A it idles and logs that durable queues are
not implemented yet. Phase 3 will attach scan, cleanup, deletion retry,
notification retry, export, and billing reconciliation jobs to this process.

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
