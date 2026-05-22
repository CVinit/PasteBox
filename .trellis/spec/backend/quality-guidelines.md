# Quality Guidelines

> Code quality standards for backend development.

---

## Overview

Backend changes must keep the Go service buildable and testable from a clean
checkout. Local commands use project-local caches so checks do not depend on
user-level Go cache permissions.

---

## Forbidden Patterns

- Do not hard-code product plan limits in HTTP handlers.
- Do not place core business rules directly in `cmd/pastebox/main.go`.
- Do not rely on Redis as a source of truth for plans, billing, paste metadata,
  object references, or final order state.
- Do not log secrets, magic links, reset tokens, OAuth tokens, object storage
  credentials, or full user-provided paste bodies.

---

## Required Patterns

- Use `gofmt` for Go files.
- Use `log/slog` structured logging for backend process logs.
- Keep process startup in `cmd/pastebox/main.go`; keep routing in
  `internal/httpserver`.
- Use `PASTEBOX_` env vars for runtime configuration and keep defaults in
  `internal/config`.
- Keep API JSON response contracts covered by handler tests.

---

## Testing Requirements

- Run `make test` before reporting completion.
- Backend-only verification: `make test-api`.
- Add unit tests for new config parsing, domain calculations, quota limits, and
  handler response contracts.
- For cross-layer API response changes, run both `make test-api` and
  `make test-web`.

---

## Code Review Checklist

- Does the change preserve the `/api/v1/...` API namespace?
- Are product-facing names `PasteBox` and code/module names `pastebox`?
- Are new env keys documented in `.env.example` and the relevant spec?
- Are limits/config values sourced from a domain package or config path instead
  of being duplicated in transport code?
- Did `make test` pass with project-local caches?
