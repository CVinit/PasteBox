# PasteBox Deployment

This document describes the deployable container path for the current PasteBox
MVP and the operational boundary that was verified in this repository.

## Current Readiness

The current build is immediately deployable as a single-container executable
MVP for demos, internal review, and low-risk evaluation. It serves the Go API and
the React/Vite frontend from the same container image.

It is not production-ready for a paid public SaaS yet. The current domain
service uses an in-memory repository and local in-process object abstraction.
That means users, sessions, pastes, attachments, shares, orders, audit logs, and
queue state are lost on process restart. PostgreSQL, Redis, S3, mail, Stripe,
Epusdt, ClamAV, and worker queues are modeled as typed seams/stubs but are not
live production integrations in this pass.

Use this deployment path when you need a runnable MVP. Do not use it for real
customer data until persistent adapters, migrations, real object storage,
payment webhooks, mail delivery, scanner workers, backups, and operational
monitoring are implemented.

## GitHub Actions Image Build

The repository includes `.github/workflows/docker-image.yml`.

On pushes to `main`, version tags matching `v*.*.*`, and manual
`workflow_dispatch` runs, the workflow builds the Docker image and publishes it
to GitHub Container Registry:

```text
ghcr.io/cvinit/pastebox:latest
ghcr.io/cvinit/pastebox:sha-<commit>
ghcr.io/cvinit/pastebox:<tag>
```

Pull requests build the image without pushing it.

The workflow uses `GITHUB_TOKEN` with `packages: write` to publish to GHCR. The
repository's Actions settings must allow workflows to write packages. If the
package already exists and is not connected to this repository, connect it in
GitHub Packages settings or grant this repository write access.

## Local Image Build

Build the same image locally:

```sh
docker build -t pastebox:local .
```

Run it:

```sh
docker run --rm -p 8080:8080 \
  -e PASTEBOX_APP_ENV=development \
  -e PASTEBOX_PUBLIC_URL=http://localhost:8080 \
  pastebox:local
```

Open:

```text
http://localhost:8080
```

For HTTP-only test deployments, PasteBox now omits the `Secure` cookie flag
when the browser reaches the app over plain HTTP. For HTTPS deployments behind a
reverse proxy, forward the original scheme with `X-Forwarded-Proto: https` so
session cookies are marked `Secure`.

## Docker Compose With GHCR Image

After the GitHub Actions workflow publishes the image, copy the deployment
compose file:

```sh
cp compose.deploy.yaml compose.yaml
```

Edit these values before starting:

```yaml
PASTEBOX_PUBLIC_URL: https://pastebox.example.com
PASTEBOX_BOOTSTRAP_ADMIN_EMAIL: admin@example.com
PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD: change-me-admin-password
```

Then run:

```sh
docker compose pull
docker compose up -d
docker compose logs -f pastebox
```

Check health:

```sh
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/api/v1/health
```

Expected response:

```json
{"status":"ok"}
```

and:

```json
{"app":"PasteBox","env":"production","status":"ok"}
```

## TLS and Reverse Proxy

For production mode, put PasteBox behind HTTPS. The `X-Forwarded-Proto: https`
header is part of the session-cookie contract: without it, PasteBox cannot tell
that the browser used HTTPS when TLS terminates at the proxy. Example Nginx
upstream:

```nginx
server {
    listen 443 ssl http2;
    server_name pastebox.example.com;

    client_max_body_size 5g;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
    }
}
```

The current API stores all state in memory, so do not run more than one PasteBox
container behind a load balancer until a shared persistent repository is added.

## Admin Bootstrap

For this in-memory MVP, the bootstrap admin is created at process startup from:

```sh
PASTEBOX_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD=change-me-admin-password
```

Change the password before exposing the service. Because data is in memory, the
same bootstrap admin is recreated after every restart.

## Upgrade Flow

To deploy a new image published by GitHub Actions:

```sh
docker compose pull pastebox
docker compose up -d pastebox
docker compose logs -f pastebox
```

Because this MVP is in-memory, every restart clears application state. Export
anything you need from the UI before restarting.

## Required Work Before Real Production

Before accepting real users or payments, implement and verify:

- PostgreSQL/sqlc persistence and migrations for all core entities.
- Durable object storage through S3-compatible storage.
- Redis-backed sessions, rate limits, and queues where appropriate.
- Real mail delivery for verification, magic links, reset, and billing notices.
- Real Stripe and Epusdt webhook validation and idempotent subscription/order
  reconciliation.
- Real ClamAV scanning workers and cleanup workers.
- Backups, restore runbooks, metrics, logs, alerts, and abuse monitoring.
- Secret management and removal of development-token responses from auth flows.
