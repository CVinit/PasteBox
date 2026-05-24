# PasteBox Production Secrets

Production secrets live only on the server and in provider dashboards. They must
not be committed to the repository.

## Files

- Commit: `deploy/production.env.example`
- Do not commit: `deploy/production.env`

The real `deploy/production.env` file should be owned by the deploy user and
mode `600`.

## Required Secret Classes

- PostgreSQL password and `PASTEBOX_DATABASE_URL`.
- Managed S3-compatible object storage access key and secret key.
- Restic repository password and backup object-storage credentials.
- Bootstrap admin password.
- CSRF signing secret.
- SMTP credentials.
- Stripe webhook/signing secrets and API keys once Phase 6 enables Stripe.
- Epusdt API credentials and callback secrets once Phase 6 enables Epusdt.
- Google OAuth client secret once Phase 4 enables production OAuth.

## Handling Rules

- Use `PASTEBOX_` environment variables for application runtime config.
- Use pinned image tags or digests in `PASTEBOX_IMAGE`; never deploy `latest`.
- Rotate a secret immediately if it appears in logs, shell history, screenshots,
  chat, issue trackers, or committed files.
- Keep object-storage credentials for attachment data separate from backup
  credentials where the provider supports separate keys.
- Do not place raw secrets in Caddyfile, Compose files, README snippets, or
  runbooks.
