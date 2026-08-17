# PasteBox Production Secrets

Production root secrets live only on the server. Application/provider secrets
are entered through the administrator console and encrypted in PostgreSQL. No
secret may be committed to the repository.

## Files

- Commit: `deploy/production.env.example`
- Do not commit: `deploy/production.env`

The real `deploy/production.env` file should be owned by the deploy user and
mode `600`.

## Startup Roots In The Environment

- PostgreSQL password and `PASTEBOX_DATABASE_URL`.
- Redis address.
- Configuration-encryption key (`PASTEBOX_CONFIG_ENCRYPTION_KEY`), encoded as
  Base64 for exactly 32 bytes.
- Restic repository password and backup object-storage credentials.
- Metrics bearer token (`PASTEBOX_METRICS_TOKEN`).
- Immutable image reference, production domain, and administrator contact.

These values are required before PostgreSQL and the application can start or
before encrypted configuration can be read. The CSRF secret is derived from the
configuration-encryption key unless a legacy deployment still supplies an
explicit value.

## Admin-Managed Secrets

- Managed S3-compatible object-storage access key and secret key.
- SMTP credentials.
- Google and GitHub OAuth client secrets.
- Turnstile secret.
- Telegram bot token.
- Stripe webhook signing secret.
- Epusdt merchant id and callback secret.

Enter these values in **Admin > Application config**. The API returns only a
configured/not-configured status, never plaintext. Leaving a secret input blank
keeps the current value; the clear action removes it.

## Handling Rules

- Back up `PASTEBOX_CONFIG_ENCRYPTION_KEY` separately from PostgreSQL. Database
  restores require the same key. Do not rotate it until a supported
  decrypt-and-re-encrypt procedure is available.
- Use pinned image tags or digests in `PASTEBOX_IMAGE`; never deploy `latest`.
- Rotate a secret immediately if it appears in logs, shell history, screenshots,
  chat, issue trackers, or committed files.
- Keep object-storage credentials for attachment data separate from backup
  credentials where the provider supports separate keys.
- Do not place raw secrets in Caddyfile, Compose files, README snippets, or
  runbooks.
