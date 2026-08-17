# Current configuration architecture

## Existing configuration sources

- `internal/config/config.go` reads site, security, infrastructure, provider, billing, and bootstrap-admin values from environment variables into one immutable `config.Config`.
- `cmd/pastebox/main.go` creates the PostgreSQL pool, S3 store, API server, mail sender, scanner, and worker from that startup snapshot.
- `internal/httpserver/server.go` keeps a copy of the startup config for support contacts, CORS, CSRF, metrics, OAuth, and webhook validation.
- `internal/app/app.go` keeps another startup config copy for public URLs, Turnstile, payment details, mail content, and provider behavior.

## Existing database-managed configuration

- Migration `000005_admin_runtime_controls.sql` created `system_configs`.
- `internal/postgres/runtime_controls.go` persists one JSON runtime document with id `default`.
- `internal/app/runtime_control.go` already supports database-backed log level, guest upload policy, registration rules, rate limits, plan identifiers, provider status, and alerts.
- The API and Worker refresh the runtime document periodically, but only log level and existing business limits are truly dynamic.

## Existing admin surface

- `/api/v1/admin/runtime-config` reads and patches the runtime document.
- `/api/v1/admin/providers/{provider}/test` only tests values from the startup environment.
- `web/src/App.tsx` renders provider status and missing environment-variable names, but does not accept provider credentials.

## Constraints for the implementation

- PostgreSQL connection and the decryption root key must exist before database-managed configuration can be loaded.
- Prometheus consumes the metrics token outside the application, so that token remains deployment-managed.
- Caddy, PostgreSQL, backups, and image selection are Compose concerns and cannot safely depend on the application database.
- S3 is currently required while constructing the service; a configurable wrapper or dependency swap is needed so a fresh deployment can reach the admin page before S3 is configured.
- Worker mail and scanner clients are currently created once; they need concurrency-safe runtime replacement or per-job lookup.
- HTTP server handlers currently read an immutable config copy; they need a safe effective-config accessor.
- Secret persistence must be structurally separate from API response types to prevent accidental serialization.

