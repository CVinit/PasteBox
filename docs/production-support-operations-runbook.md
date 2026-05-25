# PasteBox Support Operations Runbook

This runbook defines the Phase 8 launch gate for support, legal, compliance,
refund, abuse, deletion, export, retention, and subprocessor operations. Public
beta must not start until these workflows are reachable from the product UI and
operators can process each request without direct database edits.

## Public Surfaces

The production web bundle must expose these unauthenticated routes through the
single-page app fallback:

- `/legal`
- `/legal/terms`
- `/legal/privacy`
- `/legal/refund`
- `/legal/abuse`
- `/legal/cookies`
- `/legal/account-deletion`
- `/legal/data-export`
- `/legal/data-retention`
- `/legal/subprocessors`
- `/support`
- `/status`

The authenticated app must keep footer/legal navigation visible from the signed
out screen and signed in workspace, add billing support links from Billing, and
add account deletion/export/privacy/support links from Settings.

## Intake Channels

Use the in-app report flow for abuse tied to a paste, share, or attachment. Use
the Support page for account, billing, privacy, DPA, GDPR/data-subject, and
provider-change requests. Operators must record every action in existing audit,
report, account, order, webhook, or lifecycle records.

The Support page reads its public intake addresses from
`GET /api/v1/support/contacts`. Production preflight requires
`PASTEBOX_SUPPORT_EMAIL` and `PASTEBOX_ABUSE_EMAIL` to be explicit production
email addresses, and the values in `deploy/production.env` must route to
operator-monitored inboxes before public beta traffic is accepted.

Do not request or store raw secrets, card numbers, private keys, seed phrases,
or identity documents in PasteBox tickets unless a future verified support
system with restricted retention is added.

## SLA Targets

- Abuse or malware report: acknowledge within 24 hours; urgent malicious public
  share links should be triaged as soon as the operator is available.
- DMCA/takedown notice: acknowledge within 2 business days and preserve the
  report evidence.
- Refund or payment support: acknowledge within 2 business days and reconcile
  against order, webhook, provider, and audit state before manual correction.
- GDPR/data-subject request: acknowledge within 7 days and complete within 30
  days unless local law requires a different deadline.
- DPA request: acknowledge within 7 days and route to the account owner or
  business contact before sharing contractual material.
- Account deletion/export support path: acknowledge within 7 days after
  ownership verification.

## Refund And Payment Disputes

Required evidence:

- Account email and user ID.
- Order ID, provider, amount, currency, period, and creation time.
- Latest order status, provider transaction ID when available, and webhook event
  history.
- Any admin correction action and the operator identity.

Workflow:

1. Locate the order in the admin Billing view.
2. Run billing reconciliation before making a manual correction.
3. For Stripe, rely on signed provider webhooks for refund and cancellation
   lifecycle changes when possible.
4. For Epusdt, compare the order ID, transaction evidence, expiry state, and
   any stuck-payment report before using manual mark-paid controls.
5. Record the decision in audit logs and reply through the support channel.

Manual paid-plan changes must include a reason tied to the support request.

## Abuse And DMCA Takedowns

Required evidence:

- PasteBox URL, paste ID, share token, attachment ID, or user ID.
- Report category and narrative.
- Reporter contact.
- Evidence needed to verify malware, phishing, copyright, illegal content,
  exposed secrets, spam, or harassment.

Workflow:

1. Open the admin Reports and high-risk attachment views.
2. Freeze or revoke public access first when the content is plausibly harmful.
3. Use scanner retry for failed scan jobs before deciding whether a clean file
   is safe to restore.
4. Use takedown actions for violating pastes and revoke shares that expose the
   target content.
5. Preserve report and audit records for later review.
6. Mark the report resolved or dismissed with the final action.

Known malicious files remain blocked for owner downloads, public share access,
previews, exports, and future share creation.

## Account Deletion Requests

Normal path:

1. The user signs in.
2. The user opens Settings and requests account deletion.
3. PasteBox schedules deletion and sends the configured account-deletion email.
4. The user may cancel before execution or execute when eligible.

Support path:

1. Verify control of the account email before acting.
2. Check for open billing, abuse, or legal holds.
3. Use the account lifecycle controls or documented service path rather than
   direct database edits.
4. Confirm completion or reason for delay in the support record.

Deletion must preserve audit and billing records required for security, dispute,
abuse, and compliance review while removing user-controlled content according to
the data-retention matrix.

## Data Export And Data-Subject Requests

Normal path:

1. The user signs in.
2. The user opens Settings.
3. The user downloads the authenticated JSON export from `/api/v1/me/export`.

Support path:

1. Verify account ownership through the account email.
2. Identify whether the request is a product export, GDPR/data-subject request,
   DPA request, or legal escalation.
3. Use the authenticated export path when possible.
4. For broader requests, collect relevant account, paste metadata, share,
   order, webhook, report, audit, deletion, and support records.
5. Record the export date, operator, delivery path, and verification method.

Do not send exports to unverified addresses.

## Data Retention Matrix

- Active account profile, sessions, pastes, attachments, shares, orders,
  reports, jobs, webhook events, and audit logs: retained while the account and
  related objects are active.
- Expired pastes and shares: removed by cleanup jobs and object lifecycle
  cleanup after expiry and retry completion.
- Deleted-account content: removed through account deletion workflows unless a
  legal, billing, security, or abuse hold applies.
- Billing and webhook records: retained as needed for disputes, fraud, tax,
  provider reconciliation, and audit history.
- Abuse, DMCA, and security reports: retained as needed for enforcement and
  repeat-abuse analysis.
- Backups: 30-day retention with daily logical backups plus WAL/PITR evidence
  stored off host.
- Operational logs and metrics: retained according to the monitoring provider
  configuration and must not include raw secrets.

If retention behavior changes, update `/legal/data-retention`, this runbook, and
the deployment launch checklist in the same release.

## Subprocessor Changes

The confirmed launch architecture uses these provider categories:

- US single-region VPS/cloud host.
- Managed S3-compatible object storage for attachments.
- Off-host S3-compatible backup storage.
- Existing SMTP/enterprise email service.
- Stripe.
- Epusdt.
- Google OAuth.

Before adding, removing, or replacing a provider:

1. Update `/legal/subprocessors`.
2. Update `/legal/privacy` and `/legal/data-retention` if data handling changes.
3. Update `deploy/production.env.example` and `docs/production-secrets.md` when
   new credentials or secret rotation rules are needed.
4. Run preflight and readiness checks for the new provider.
5. Record the change in release notes before public beta traffic uses it.

## Legal Page Updates

Legal/support page updates are release-gated product changes. For each update:

1. Identify the reason: provider change, retention change, payment workflow,
   abuse workflow, legal requirement, or product behavior change.
2. Update the public page content in the web app.
3. Update this runbook when operator behavior changes.
4. Run `make test-web` and focused backend route fallback tests.
5. Record the updated page date and release commit.

## Launch Gate Evidence

Phase 8 is complete only when all of these are true:

- Public legal/support/status routes are reachable through the production web
  bundle and through direct deep links.
- `/support` renders the configured `PASTEBOX_SUPPORT_EMAIL` and
  `PASTEBOX_ABUSE_EMAIL` intake addresses from `/api/v1/support/contacts`.
- Billing and Settings link to refund, support, privacy, deletion, and export
  paths.
- This runbook covers payment disputes, account deletion, data exports,
  takedowns, legal page updates, and subprocessor changes.
- The data-retention matrix and subprocessor list match the configured
  production architecture.
- Operators can triage support, abuse, refund, GDPR/data-subject, and DPA
  requests with an audit trail and without direct database edits.
