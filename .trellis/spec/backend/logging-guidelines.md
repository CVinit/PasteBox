# Logging Guidelines

> How logging is done in this project.

---

## Overview

<!--
Document your project's logging conventions here.

Questions to answer:
- What logging library do you use?
- What are the log levels and when to use each?
- What should be logged?
- What should NOT be logged (PII, secrets)?
-->

(To be filled by the team)

---

## Log Levels

<!-- When to use each level: debug, info, warn, error -->

(To be filled by the team)

---

## Structured Logging

<!-- Log format, required fields -->

(To be filled by the team)

---

## What to Log

<!-- Important events to log -->

(To be filled by the team)

---

## What NOT to Log

<!-- Sensitive data, PII, secrets -->

(To be filled by the team)

## Scenario: Runtime Log Level Control

### 1. Scope / Trigger

- Trigger: Changes to process logging, `PASTEBOX_LOG_LEVEL`,
  `RuntimeConfig.logLevel`, admin runtime config APIs, or frontend controls
  that edit runtime config.

### 2. Signatures

- Env default: `PASTEBOX_LOG_LEVEL`
- Runtime JSON field: `RuntimeConfig.logLevel`
- Admin API: `GET/PATCH /api/v1/admin/runtime-config`
- Go helpers: `app.NormalizeRuntimeLogLevel`,
  `Service.RefreshRuntimeConfig`, and `slog.LevelVar` in `cmd/pastebox`.

### 3. Contracts

- Supported runtime levels are `debug`, `info`, `warn`, and `error`.
- `PASTEBOX_LOG_LEVEL` remains the startup default; persisted runtime config
  wins after the service loads it.
- API and worker loggers must use `slog.LevelVar`, not a fixed
  `slog.Level`, so the level can change without a container restart.
- Admin runtime config responses must include `logLevel`; PATCH may update it
  with the other runtime config groups.
- API process changes apply immediately through the service runtime-config
  hook. Worker processes poll persisted runtime config and apply changes with
  the same level parser.
- Debug logs must be structured and must not include secrets, share tokens,
  cookies, bearer/CSRF tokens, object storage credentials, full paste bodies,
  reset/magic/verification tokens, or user-provided file names.

### 4. Validation & Error Matrix

- `logLevel=debug|info|warn|error` -> save config and apply level.
- `logLevel=DEBUG` -> normalize to `debug`.
- `logLevel=warning` in stored config -> normalize to `warn`.
- `logLevel=trace` in admin PATCH -> `400 invalid_log_level`.
- Missing `logLevel` in old stored config -> fall back to
  `PASTEBOX_LOG_LEVEL` normalized through the runtime config default.

### 5. Good/Base/Bad Cases

- Good: Admin sets `debug`; the API emits debug request workflow logs without
  restart, and the worker follows after runtime config sync.
- Base: Startup with no persisted runtime config uses
  `PASTEBOX_LOG_LEVEL=info`.
- Bad: Store `trace`, silently accept it, or log raw share tokens while trying
  to make debug output more useful.

### 6. Tests Required

- App tests must cover log level normalization, invalid `invalid_log_level`,
  and runtime-config change hook behavior.
- Handler tests must cover `PATCH /api/v1/admin/runtime-config` accepting a
  valid log level and rejecting an invalid one.
- Command tests must cover `slog.LevelVar` application from runtime log level
  strings.
- Frontend changes must update `web/src/api.ts`, admin UI controls, and every
  supported locale, then run `make test-web`.
- Cross-layer changes must run `make test-api` and `make test-web`.

### 7. Wrong vs Correct

#### Wrong

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: cfg.LogLevel,
}))
```

#### Correct

```go
level := newLogLevelVar(cfg.LogLevel)
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: level,
}))
applyRuntimeLogLevel(runtimeCfg.LogLevel, level, logger)
```
