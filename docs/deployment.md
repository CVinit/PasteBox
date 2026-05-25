# PasteBox Deployment

This document describes the deployable demo container path for the current
PasteBox MVP and the operational boundary that was verified in this repository.

## Current Readiness

The current build is deployable for demos, internal review, and low-risk
evaluation through the demo Compose stack. The API container serves both the Go
API and the embedded React/Vite frontend, while sibling containers provide
PostgreSQL, Redis, MinIO-compatible object storage, migrations, bucket
initialization, and the PasteBox worker.

Use this deployment path when you need a runnable MVP with durable demo state.
Do not use it as the public production launch stack: it uses demo defaults,
local MinIO, log mail, heuristic scanning by default, no TLS edge container, no
off-host backup flow, no production preflight, and no restore/PITR launch-gate
evidence.

For the confirmed production-launch baseline, use
`docs/production-deployment-runbook.md` and `compose.production.yaml` instead of
this demo deployment file. The production baseline adds HTTPS reverse proxy,
managed object-storage expectations, production preflight, readiness checks,
backup jobs, PITR/restore drills, rollback gates, and support/compliance
evidence.

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

Use `sha-*` tags or digests for deployments. `latest` is a moving convenience
tag for manual inspection and must not be used for the production launch
baseline.

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

Do not run the image by itself: `pastebox api` now expects PostgreSQL,
Redis-compatible infrastructure, and an S3-compatible bucket. For a local
container smoke test, start the demo Compose stack instead:

```sh
PASTEBOX_IMAGE=pastebox:local docker compose -f compose.deploy.yaml up -d
```

Open:

```text
http://localhost:8080
```

For HTTP-only test deployments, PasteBox now omits the `Secure` cookie flag
when the browser reaches the app over plain HTTP. For HTTPS deployments behind a
reverse proxy, forward the original scheme with `X-Forwarded-Proto: https` so
session cookies are marked `Secure`.

## Demo Docker Compose With GHCR Image

After the GitHub Actions workflow publishes the image, copy the demo deployment
Compose file:

```sh
cp compose.deploy.yaml compose.yaml
```

Create a `.env` file next to `compose.yaml` or export these variables before
starting. Use the immutable `sha-*` image tag from the workflow run, or a
registry digest. The demo file supplies local PostgreSQL, Redis, MinIO, a
migration job, a bucket-initialization job, and a worker:

```sh
PASTEBOX_IMAGE=ghcr.io/cvinit/pastebox:sha-<commit>
PASTEBOX_PUBLIC_URL=http://localhost:8080
PASTEBOX_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD=<long-random-password>
```

Then run:

```sh
docker compose pull
docker compose up -d
docker compose logs -f pastebox
```

For HTTPS demos behind your own reverse proxy, set
`PASTEBOX_PUBLIC_URL=https://pastebox.example.com` and forward
`X-Forwarded-Proto: https` to the `pastebox` service.

Check health:

```sh
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/api/v1/health
curl -fsS http://127.0.0.1:8080/api/v1/ready
```

Expected response:

```json
{"status":"ok"}
```

and:

```json
{"app":"PasteBox","env":"development","status":"ready","components":[{"name":"database","status":"ok"},{"name":"object_storage","status":"ok"},{"name":"redis","status":"ok"},{"name":"worker_queue","status":"ok"},{"name":"mail","status":"skipped","message":"smtp provider is not configured"}]}
```

and:

```json
{"app":"PasteBox","env":"development","status":"ok"}
```

and:

```json
{"app":"PasteBox","env":"development","status":"ready","components":[{"name":"database","status":"ok"},{"name":"object_storage","status":"ok"},{"name":"redis","status":"ok"},{"name":"worker_queue","status":"ok"},{"name":"mail","status":"skipped","message":"smtp provider is not configured"}]}
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

The demo Compose file is single-node. Do not run more than one PasteBox API
container behind a load balancer from this file; use the production runbook and
shared production services before horizontal scaling.

## Admin Bootstrap

For demo deployments, the bootstrap admin is created or updated at process
startup from:

```sh
PASTEBOX_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD=<long-random-password>
```

Change the password before exposing the service. The admin account is stored in
PostgreSQL and survives restarts in the demo stack.

## Upgrade Flow

To deploy a new image published by GitHub Actions:

```sh
grep '^PASTEBOX_IMAGE=' .env
# Edit .env and replace PASTEBOX_IMAGE with the new sha-* tag or digest.
docker compose pull pastebox
docker compose up -d pastebox
docker compose logs -f pastebox
```

The demo stack stores state in Docker volumes for PostgreSQL, Redis, and MinIO.
Back up or export anything you need before deleting those volumes.

## Required Work Before Real Production

Before accepting real users or payments, use the production runbook and verify:

- Pinned production image or digest, production preflight, and migration gate.
- Managed S3-compatible attachment storage and off-host backup storage.
- Real SMTP, Google OAuth, Stripe, Epusdt, and scanner credentials.
- Provider smoke tests for mail, OAuth, billing webhooks, Epusdt callbacks, and
  scanning.
- Backup integrity, logical restore drill, PITR restore drill, and rollback
  rehearsal evidence.
- Metrics, logs, alerts, certificate renewal checks, and abuse/support
  workflows.
- Secret handling, legal/support pages, data-retention matrix, and operator
  runbooks matching the deployed provider configuration.
