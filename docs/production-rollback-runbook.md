# PasteBox Production Rollback Runbook

This runbook defines the rollback gate for the single-VPS Docker Compose
baseline. Use it before public traffic and before every production upgrade.

## Required Inputs

- Current image reference from `deploy/production.env`.
- Previous known-good image reference.
- Migration classification: reversible, forward-compatible, or non-reversible.
- Latest successful off-host backup snapshot ID.
- Latest PITR base-backup manifest and WAL archive freshness evidence.

## Reversible Or No-Migration Rollback

1. Save the current image reference:

   ```sh
   grep '^PASTEBOX_IMAGE=' deploy/production.env
   ```

2. Replace `PASTEBOX_IMAGE` in `deploy/production.env` with the previous
   known-good `sha-*` tag or digest.
3. Validate the Compose plan:

   ```sh
   docker compose --env-file deploy/production.env -f compose.production.yaml config
   docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm preflight
   ```

4. Pull and restart only the application services:

   ```sh
   docker compose --env-file deploy/production.env -f compose.production.yaml pull api worker
   docker compose --env-file deploy/production.env -f compose.production.yaml up -d api worker
   ```

5. Verify:

   ```sh
   curl -fsS https://pastebox.example.com/readyz
   curl -fsS https://pastebox.example.com/api/v1/ready
   docker compose --env-file deploy/production.env -f compose.production.yaml logs --tail=200 api worker
   ```

## Non-Reversible Migration Gate

Do not run a non-reversible production migration unless all of these are true:

- A fresh logical backup and off-host push completed successfully.
- A fresh WAL archive freshness check and PITR base backup completed
  successfully.
- The logical backup path, PITR base-backup manifest, latest WAL file, and
  off-host snapshot ID are recorded in the release notes.
- Logical restore and PITR restore drills for the same backup class have already
  met the RTO 4-hour target.
- The operator has an approved maintenance window and user-facing status
  message.

If rollback requires data restore, stop app traffic first:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml stop api worker
```

For logical rollback, restore from the approved backup snapshot only after the
same backup class has passed the scratch restore drill:

```sh
PASTEBOX_RESTORE_SOURCE=/backups/postgres/pastebox-YYYYMMDDTHHMMSSZ.sql.gz \
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm postgres-restore-drill
```

For point-in-time recovery, prove the exact base-backup class and WAL archive in
an isolated temporary instance first:

```sh
PASTEBOX_PITR_SOURCE_BASE=/backups/basebackups/pastebox-base-YYYYMMDDTHHMMSSZ.tar.gz \
PASTEBOX_PITR_TARGET_TIME="YYYY-MM-DD HH:MM:SS+00" \
docker compose --env-file deploy/production.env -f compose.production.yaml --profile maintenance run --rm postgres-pitr-drill
```

Only after the drill succeeds, restore into the production database during the
approved maintenance window, restart app services, and verify readiness. Record
the drill duration, target time, base backup, latest WAL file, and off-host
snapshot ID in the incident notes.
