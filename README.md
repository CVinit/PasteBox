# PasteBox

PasteBox is a cloud clipboard and temporary file transfer SaaS for international users. The MVP centers on private pastes with explicit expiration, controlled sharing links, membership quotas, S3-compatible object storage, Stripe subscriptions, and optional USDT fixed-duration membership orders.

This repository contains the first executable single-node MVP. It keeps the product name as `PasteBox` and uses `pastebox` for the Go module, packages, and binary name.

## Stack

- Backend: Go and Chi. The executable runtime uses PostgreSQL for application
  state, S3-compatible object storage for attachments, Redis-compatible
  readiness/queue infrastructure, worker processes for background jobs, and
  provider seams for mail, scanning, OAuth, and billing.
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

3. Run the API. This target starts local dependencies, applies PostgreSQL
   migrations, ensures the MinIO `pastebox` bucket exists, and then starts the
   Go API:

   ```sh
   make api
   ```

4. Run the web app:

   ```sh
   make web
   ```

The API listens on `http://localhost:8080`. The Vite dev server listens on `http://localhost:5173` and proxies `/api` to the API.

Create or reset a local administrator explicitly after the database migrations
have run:

```sh
go run ./cmd/pastebox admin create \
  --email admin@example.com \
  --password '<dev-admin-password>'
```

Application and provider settings are managed from **Admin > Application
config**. The `.env` file only contains startup roots such as the database,
Redis, application environment, and the production configuration-encryption
key.

The development auth flows return dev tokens in JSON responses for registration codes, email verification, and password reset so the complete flow can be exercised without a live mail provider.

## Docker Image

The project includes a multi-stage `Dockerfile` that builds the React frontend,
embeds the Vite production assets into the Go binary, and serves the API and UI
from the API container.

Build locally:

```sh
docker build -t pastebox:local .
```

The API image expects PostgreSQL, Redis, and S3-compatible storage to be
available. For local development, use `make dev` and the root `compose.yaml`.

GitHub Actions publishes a moving convenience tag and immutable release
references:

```text
ghcr.io/cvinit/pastebox:latest
ghcr.io/cvinit/pastebox:sha-<commit>
ghcr.io/cvinit/pastebox:<tag>
```

Use the [PostgreSQL + Redis deployment tutorial](docs/postgresql-redis-deployment.zh-CN.md)
for the supported production deployment. Use immutable `sha-*` tags or image
digests; `latest` is only a convenience tag.

## Deployment Readiness

Production deployment is documented in
`docs/postgresql-redis-deployment.zh-CN.md`; the repository's other Compose
file, `compose.yaml`, is for local development only.

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

- `cmd/pastebox`: API, worker, migration, preflight, and admin CLI entrypoint.
- `internal/app`: domain service covering auth, users, pastes, attachments,
  shares, quota, billing/webhooks, admin, scanning, cleanup, reports, export,
  and deletion through store interfaces.
- `internal/config`: environment-driven runtime configuration.
- `internal/httpserver`: Chi router, health endpoint, API routes, and static fallback hook.
- `internal/plans`: configurable first-version plan limits used by the API and frontend.
- `internal/postgres`: migrations and PostgreSQL stores for auth, content,
  operational state, catalog, metrics, audit logs, jobs, and mail queue.
- `internal/objectstore`: S3-compatible attachment object storage.
- `web`: React/Vite application shell for the PasteBox product surface.
- `compose.yaml`: local development dependencies.

## Product Source

The source product requirements live in `.trellis/tasks/archive/2026-05/05-23-cloudpaste-product-prd/prd.md`. The active production launch task is `.trellis/tasks/05-24-pastebox-production-launch/prd.md`.
