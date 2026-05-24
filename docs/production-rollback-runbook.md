# PasteBox Production Rollback Runbook

This runbook defines the rollback gate for the single-VPS Docker Compose
baseline. Use it before public traffic and before every production upgrade.

## Required Inputs

- Current image reference from `deploy/production.env`.
- Previous known-good image reference.
- Migration classification: reversible, forward-compatible, or non-reversible.
- Latest successful off-host backup snapshot ID.

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
- The backup snapshot ID is recorded in the release notes.
- A restore drill for the same backup class has already met the RTO 4-hour
  target.
- The operator has an approved maintenance window and user-facing status
  message.

If rollback requires data restore, stop app traffic first:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml stop api worker
```

Restore from the approved backup snapshot according to the Phase 7 restore
runbook, then restart app services and verify readiness. Until Phase 7 adds and
tests PITR/WAL restore, non-reversible migrations are not allowed for public
beta.
