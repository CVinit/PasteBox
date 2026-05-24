# PasteBox

PasteBox is a cloud clipboard and temporary file transfer SaaS for international users. The MVP centers on private pastes with explicit expiration, controlled sharing links, membership quotas, S3-compatible object storage, Stripe subscriptions, and optional USDT fixed-duration membership orders.

This repository contains the first executable single-node MVP. It keeps the product name as `PasteBox` and uses `pastebox` for the Go module, packages, and binary name.

## Stack

- Backend: Go and Chi. The current MVP runs with an in-memory repository and local object abstraction while preserving typed seams for PostgreSQL, Redis/queues, S3-compatible storage, billing, scanning, cleanup, mail, and admin operations.
- Frontend: React, TypeScript, Vite.
- Local services: Docker Compose with PostgreSQL, Redis, MinIO, ClamAV, and Mailpit.

## Quick Start

1. Copy the sample environment:

   ```sh
   cp .env.example .env
   ```

2. Start local dependencies:

   ```sh
   make dev
   ```

3. Run the API:

   ```sh
   make api
   ```

4. Run the web app:

   ```sh
   make web
   ```

The API listens on `http://localhost:8080`. The Vite dev server listens on `http://localhost:5173` and proxies `/api` to the API.

Optional bootstrap admin credentials can be set in `.env`:

```sh
PASTEBOX_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD=change-me-admin-password
```

The development auth flows return dev tokens in JSON responses for email verification, magic link, and password reset so the complete flow can be exercised without a live mail provider.

## Docker Image

The project includes a multi-stage `Dockerfile` that builds the React frontend,
embeds the Vite production assets into the Go binary, and serves the API and UI
from one container.

Build locally:

```sh
docker build -t pastebox:local .
```

Run locally:

```sh
docker run --rm -p 8080:8080 \
  -e PASTEBOX_APP_ENV=development \
  -e PASTEBOX_PUBLIC_URL=http://localhost:8080 \
  pastebox:local
```

Open `http://localhost:8080`.

GitHub Actions publishes the image to:

```text
ghcr.io/cvinit/pastebox:latest
```

See [docs/deployment.md](docs/deployment.md) for the GHCR image workflow,
Docker Compose deployment, reverse proxy notes, and current production-readiness
boundary.

中文部署说明见 [docs/deployment.zh-CN.md](docs/deployment.zh-CN.md)，其中包含
GitHub Actions 自动构建镜像后的 Docker Compose 部署方式。

The production launch baseline now lives in
[docs/production-deployment-runbook.md](docs/production-deployment-runbook.md)
with `compose.production.yaml`, `deploy/production.env.example`, HTTPS reverse
proxy config, backup jobs, production preflight, readiness checks, and rollback
runbook. It is the Phase 0A deployment foundation; later phases still need to
complete durable PostgreSQL, object storage, workers, mail, OAuth, billing,
scanning, operations, and compliance before public beta.

## Deployment Readiness

The current MVP can be deployed immediately for demos, internal review, and
low-risk evaluation. It is not ready for real customer data or paid public SaaS
operation because the repository and object store are in-memory in this pass.
The Phase 0A production deployment baseline is present, but production use is
still gated by the remaining roadmap phases. Persistent PostgreSQL/sqlc,
S3-compatible storage, real mail, payment webhook verification, scanner
workers, cleanup workers, restore drills, and monitoring must be implemented
before public beta.

## Verification

Run all local checks:

```sh
make test
```

Build both backend and frontend:

```sh
make build
```

## Current MVP

- `cmd/pastebox`: API binary entrypoint.
- `internal/app`: in-memory domain service covering auth, users, pastes, attachments, shares, quota, billing/webhooks, admin, scanning, cleanup, reports, export, and deletion.
- `internal/config`: environment-driven runtime configuration.
- `internal/httpserver`: Chi router, health endpoint, API routes, and static fallback hook.
- `internal/plans`: configurable first-version plan limits used by the API and frontend.
- `web`: React/Vite application shell for the PasteBox product surface.
- `compose.yaml`: local development dependencies.

## Product Source

The source product requirements live in `.trellis/tasks/archive/2026-05/05-23-cloudpaste-product-prd/prd.md`. The active implementation task is `.trellis/tasks/05-23-pastebox-mvp-implementation/prd.md`.
