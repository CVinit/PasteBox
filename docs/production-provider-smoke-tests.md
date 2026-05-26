# PasteBox Production Provider Smoke-Test Runbook

Use this runbook for each public beta release candidate after deployment,
migrations, readiness checks, and worker startup have passed. Record sanitized
results in the release notes and evidence checklist. Do not store real secrets,
raw provider payloads, private object keys, card details, wallet private keys,
or user data in committed files.

## Scope

This runbook covers the provider evidence required by
`docs/production-launch-evidence-checklist.md`:

- Managed S3-compatible object storage upload, private read, and delete through
  the application path.
- SMTP delivery for verification, magic-link, reset, security, billing, and
  account-deletion messages.
- Google OAuth login and state-mismatch failure against the production OAuth
  application.
- Stripe production checkout URL creation, signed webhook replay, duplicate
  replay idempotency, and refund/cancellation lifecycle.
- Epusdt production checkout URL creation, signed success callback, and
  expired/canceled callback lifecycle.
- ClamAV clean-file scan and malicious-file block behavior.

## Preconditions

- `deploy/production.env` exists only on the server with mode `600`.
- `docker compose --env-file deploy/production.env -f compose.production.yaml
  --profile maintenance run --rm preflight` passed.
- `docker compose --env-file deploy/production.env -f compose.production.yaml
  --profile maintenance run --rm migrate` passed.
- `curl -fsS https://<domain>/readyz` and
  `curl -fsS https://<domain>/api/v1/ready` return `status=ready`.
- `docker compose --env-file deploy/production.env -f compose.production.yaml
  run --rm worker worker --once` completes without failed runnable jobs.
- Controlled test inboxes, test payment accounts, and a disposable test user
  are available. Do not use a real customer account.

## Evidence Rules

- Record timestamps, sanitized IDs, order IDs, webhook event IDs, message
  subjects, readiness snippets, and pass/fail status.
- Redact bearer tokens, cookies, CSRF tokens, OAuth codes, webhook signatures,
  object keys when they can identify private content, card data, private wallet
  data, raw SMTP headers, and raw provider payloads.
- If a provider dashboard is used, record the dashboard event ID and outcome,
  not screenshots containing secrets or personal data.

## Browser Session Setup

Use a disposable cookie jar for API smoke commands:

```sh
export PASTEBOX_ORIGIN=https://<domain>
export PASTEBOX_COOKIE_JAR=/tmp/pastebox-provider-smoke.cookies
export PASTEBOX_SMOKE_EMAIL=<controlled-test-inbox>
export PASTEBOX_SMOKE_PASSWORD=<strong-disposable-password>
rm -f "$PASTEBOX_COOKIE_JAR"
```

Fetch a CSRF token before browser-authenticated write requests:

```sh
curl -fsS -c "$PASTEBOX_COOKIE_JAR" "$PASTEBOX_ORIGIN/api/v1/csrf"
```

Use the returned `csrfToken` value as `X-CSRF-Token` for unsafe browser API
requests. If you use a browser instead of curl, verify the same behavior from
the browser network panel.

## Managed S3-Compatible Object Storage

1. Register or log in as the disposable test user.
2. Create a paste through the UI or `POST /api/v1/pastes/`.
3. Upload a small attachment through `POST /api/v1/pastes/<pasteID>/attachments`.
4. Download the owner attachment through
   `GET /api/v1/attachments/<attachmentID>/download`.
5. Delete the paste or attachment and run the worker once to process cleanup.
6. Attempt the old download URL again and confirm it is no longer downloadable.

Record:

- Paste ID and attachment ID.
- Upload result, owner download status, deletion/cleanup status, and final
  blocked download status.
- Object-storage provider request IDs if the provider exposes them.

## SMTP Delivery

Trigger and verify controlled test mailbox delivery for these subjects:

- `Verify your PasteBox email`
- `Your PasteBox magic link`
- `Reset your PasteBox password`
- `New PasteBox login`
- `PasteBox payment received`
- `PasteBox account deletion requested`

Steps:

1. Register the disposable user and confirm the verification email arrives.
2. Request a magic link and confirm the magic-link email arrives.
3. Request password reset and confirm the reset email arrives.
4. Log in from a fresh browser/session and confirm the security notification
   arrives.
5. Complete the Stripe or Epusdt paid-order smoke path and confirm the billing
   email arrives.
6. Request account deletion in Settings and confirm the deletion email arrives.
7. Run `docker compose --env-file deploy/production.env -f
   compose.production.yaml run --rm worker worker --once` after each queued mail
   batch when needed.

Record message subjects, recipient test inbox, delivery times, and worker
summary counts. Do not record full message bodies or token URLs.

## Google OAuth

1. Confirm the production OAuth app has this authorized redirect URI:

   ```text
   https://<domain>/api/v1/auth/google/callback
   ```

2. Open `https://<domain>/api/v1/auth/google/start?returnTo=%2F%3Fview%3Dsettings`
   in a browser and complete Google login with a controlled account.
3. Confirm PasteBox creates or links the account, sets a session cookie, and
   redirects to Settings.
4. Start a second OAuth flow, then manually change the callback `state`
   parameter before completing the callback. Confirm it redirects with
   `authError=invalid_google_state` and does not create a new session.
5. In Settings, unlink Google if the test account should be reused.

Record the OAuth client ID suffix, redirect URI, controlled account identifier,
successful login time, state-mismatch result, and whether any session was
created on mismatch.

## Stripe

1. Ensure `PASTEBOX_STRIPE_ENABLED=true`,
   `PASTEBOX_STRIPE_WEBHOOK_SECRET=whsec_...`, and
   `PASTEBOX_STRIPE_CHECKOUT_URL_TEMPLATE=https://...` are set in
   `deploy/production.env`.
2. Create a Stripe order from Billing. Confirm the returned checkout URL uses
   the configured production checkout host and does not contain `/dev/checkout`.
3. Complete or simulate the provider checkout using the operator-owned Stripe
   test/live process approved for the release candidate.
4. Replay the signed Stripe event to
   `POST /api/v1/billing/webhooks/stripe` through Stripe's dashboard, Stripe
   CLI, or the provider delivery UI.
5. Replay the same event a second time and confirm the order is not
   double-activated.
6. Send a refund or cancellation event for the same test order and confirm the
   order reaches `refunded` or `canceled` and plan access is revoked when
   applicable.

Record the PasteBox order ID, sanitized Stripe event IDs, order status after
first replay, order status after duplicate replay, refund/cancellation status,
and relevant audit/webhook event IDs.

## Epusdt

1. Ensure `PASTEBOX_EPUSDT_ENABLED=true`, `PASTEBOX_EPUSDT_PID`,
   `PASTEBOX_EPUSDT_SECRET_KEY`, `PASTEBOX_EPUSDT_CHECKOUT_URL_TEMPLATE`,
   `PASTEBOX_EPUSDT_ADDRESS`, and `PASTEBOX_EPUSDT_CHAIN` are set in
   `deploy/production.env`.
2. Create an Epusdt order from Billing. Confirm the returned checkout URL uses
   the configured production checkout host and that the response includes the
   configured receiving address and chain, not development test values.
3. Use the operator-owned Epusdt callback mechanism to send a signed success
   callback for the matching order. Confirm the endpoint returns plain `ok`,
   the order becomes `paid`, and plan access is active.
4. Create a second Epusdt order and send a signed `expired` or `canceled`
   callback. Confirm the endpoint returns plain `ok` and the order reaches the
   expected lifecycle state without activating paid access.
5. If a callback cannot be generated from the provider UI, stop and record the
   blocker. Do not forge production evidence manually.

Record the PasteBox order IDs, sanitized trade IDs, callback status, final order
statuses, payment address/chain confirmation, and webhook/audit event IDs.

## ClamAV

1. Confirm readiness includes `scanner` with `status=ok`.
2. Upload a known clean text file and run the worker once. Confirm the
   attachment scan status becomes `clean`.
3. Upload the EICAR test string as a disposable file and run the worker once.
   Confirm the attachment scan status becomes `malicious`.
4. Confirm owner download, public share download, previews, exports, and future
   share creation are blocked for the malicious attachment.

Record attachment IDs, scan statuses, worker summary counts, and blocked
download/share results. Do not upload real malware.

## Completion

Provider smoke testing is complete only when every item in the Provider Smoke
Tests section of `docs/production-launch-evidence-checklist.md` has a matching
sanitized note in the release notes. Any skipped provider item must have an
owner, risk, deadline, and launch impact in the Known Residual Risks section.
