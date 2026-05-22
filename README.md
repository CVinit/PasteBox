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
