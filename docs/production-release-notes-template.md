# PasteBox Production Release Notes Template

Copy this template for each public beta release candidate and store the filled
copy with the operator-controlled evidence archive. Do not commit real secrets,
raw provider payloads, private object keys, or user data.

Before accepting public beta traffic, validate the completed sanitized release
notes and completed evidence checklist:

```sh
make release-evidence \
  RELEASE_CHECKLIST=<completed-checklist.md> \
  RELEASE_NOTES=<completed-release-notes.md>
```

The Make target wraps `scripts/check-production-release-evidence.mjs`.

## Release Identity

- Release commit:
- Immutable image reference or digest:
- Production domain:
- Deployment window:
- Operator:
- Previous known-good image:
- Migration classification: no-migration / reversible / forward-compatible /
  non-reversible

## Repository Verification

- `make production-readiness` result:
- `make test` result:
- `make test-postgres` result:
- `make build` result:
- Web launch-surface smoke result:
- Release evidence validator self-test result:
- Docker image build or CI image result:
- Production Compose config results:
- Production preflight result:
- Monitoring config validation result:

## Deployment Evidence

- Server Compose config result:
- Image pull result:
- Migration status before deploy:
- Migration up result:
- Migration status after deploy:
- Running services:
- `/readyz` result:
- `/api/v1/ready` result:
- Scanner readiness evidence:
- Worker heartbeat evidence:
- `pastebox worker --once` evidence:

## Provider Smoke Tests

- Provider smoke-test runbook path: `docs/production-provider-smoke-tests.md`
- Managed S3-compatible upload/read/delete result:
- SMTP delivery result for registration-code, verification, reset, security, billing,
  and account-deletion messages:
- Google OAuth login result:
- Google OAuth state-mismatch result:
- Stripe checkout result:
- Stripe signed webhook replay and duplicate replay result:
- Stripe refund or cancellation lifecycle result:
- Epusdt checkout result:
- Epusdt signed success callback result:
- Epusdt expired or canceled callback result:
- ClamAV clean-file scan result:
- ClamAV malicious-file block result:

## Security And Browser Gates

- Secure session cookie evidence:
- CSRF double-submit evidence:
- Unsigned webhook rejection evidence:
- Secure response headers evidence:
- Credentialed CORS evidence:
- Rate-limit bucket evidence:
- Metrics bearer-token rejection/success evidence:
- Logs and metrics secret-redaction evidence:

## Backup, PITR, And Rollback

- Logical backup path and checksum:
- Logical restore drill result and duration:
- WAL archive freshness result:
- PITR base-backup manifest:
- PITR restore drill result and duration:
- Off-host restic snapshot ID:
- Reversible image rollback rehearsal result:
- Non-reversible migration restore rehearsal result, if applicable:

## Monitoring And Alerts

- Metrics scrape evidence:
- Caddy metrics scrape evidence:
- Host/textfile metrics evidence:
- HTTPS blackbox probe evidence:
- Alert rules loaded:
- Certificate renewal check:
- Host resource alerts:
- Backup/WAL/restore-drill alerts:
- Operator escalation targets:

## Legal, Support, And Product Operations

- Legal/support/status deep-link evidence:
- `/support` configured contact evidence:
- Billing refund/support link evidence:
- Settings deletion/export/privacy/support link evidence:
- Support/abuse report resolution evidence:
- Manual billing correction audit evidence:
- Account deletion/export audit evidence:
- Data retention and subprocessors review:
- Public status or announcement process:

## Known Residual Risks

- Risk:
- Owner:
- Mitigation:
- Deadline:
- Launch impact:

## Launch Decision

- Completed evidence checklist path:
- Skipped checklist items with justification:
- Release evidence validator result:
- Operator approval:
- Approval time:
- Public beta traffic accepted: yes / no
