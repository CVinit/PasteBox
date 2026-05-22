# PasteBox

PasteBox is a cloud clipboard and temporary file transfer SaaS for international users. The MVP centers on private pastes with explicit expiration, controlled sharing links, membership quotas, S3-compatible object storage, Stripe subscriptions, and optional USDT fixed-duration membership orders.

This repository is the initial implementation skeleton. It keeps the product name as `PasteBox` and uses `pastebox` for the Go module, packages, and binary name.

## Stack

- Backend: Go, Chi, PostgreSQL, sqlc, Goose, Redis, Asynq, S3-compatible object storage, ClamAV.
- Frontend: React, TypeScript, Vite, Tailwind-ready structure.
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

## Verification

Run all local checks:

```sh
make test
```

Build both backend and frontend:

```sh
make build
```

## Current Skeleton

- `cmd/pastebox`: API binary entrypoint.
- `internal/config`: environment-driven runtime configuration.
- `internal/httpserver`: Chi router, health endpoint, API routes, and static fallback hook.
- `internal/plans`: configurable first-version plan limits used by the API and frontend.
- `web`: React/Vite application shell for the PasteBox product surface.
- `compose.yaml`: local development dependencies.

## Product Source

The evolving product requirements live in `.trellis/tasks/05-23-cloudpaste-product-prd/prd.md`.

