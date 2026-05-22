# Directory Structure

> How frontend code is organized in this project.

---

## Overview

The frontend is a React + TypeScript + Vite app under `web/`. It is the first
screen of the PasteBox product, not a marketing-only landing page. The Vite dev
server proxies `/api` to the Go API.

---

## Directory Layout

```
web/
├── index.html
├── package.json
├── vite.config.ts
└── src/
    ├── api.ts          # API response types and fetch helpers
    ├── App.tsx         # current product workspace shell
    ├── main.tsx        # React root mounting
    └── styles.css      # app-level styles until a component system is added
```

---

## Module Organization

- Keep API response types and fetch helpers in `src/api.ts` until the file grows
  large enough to split by feature.
- New product areas should move toward `src/features/<feature>/...` once there
  are multiple components, hooks, or types for that area.
- Keep app-wide layout in `App.tsx` only while the shell is small; extract
  repeated controls into components before duplicating them.

---

## Naming Conventions

- Product-facing text uses `PasteBox`.
- TypeScript component files use PascalCase, for example `App.tsx`.
- Shared non-component helpers use lower camelCase exports from lower-case
  files, for example `api.ts`.
- Backend JSON fields are represented as camelCase TypeScript fields.

---

## Scenario: Plan Catalog API Consumption

### 1. Scope / Trigger

- Trigger: the frontend consumes the backend `/api/v1/plans` response and shows
  plan limits in the application shell.

### 2. Signatures

- Fetch helper: `fetchPlanCatalog(): Promise<PlanCatalog>`
- Backend endpoint: `GET /api/v1/plans`
- Vite proxy: `/api` -> `http://localhost:8080`

### 3. Contracts

- `PlanCatalog` is `{ plans: Plan[] }`.
- `Plan` fields must match the backend camelCase JSON contract exactly.
- The app may use a static fallback catalog only for local development when the
  API is unavailable; backend remains the source of truth.

### 4. Validation & Error Matrix

- API 2xx with valid JSON -> render returned plan list.
- API non-2xx -> render local fallback catalog.
- Network error -> render local fallback catalog.
- Field rename in backend without TypeScript update -> `make test-web` should
  fail once the frontend references the changed field.

### 5. Good/Base/Bad Cases

- Good: add a backend field, update `Plan` type, render it, and run
  `make test`.
- Base: add frontend-only display formatting without changing the API contract.
- Bad: change frontend fallback values but forget to update `internal/plans`.

### 6. Tests Required

- Run `make test-web` for every frontend change.
- Run full `make test` for API type or response contract changes.

### 7. Wrong vs Correct

#### Wrong

```ts
const freeStorage = '500 MB';
```

#### Correct

```ts
const freeStorage = formatBytes(plan.activeStorageBytes);
```

---

## Examples

- `web/src/api.ts`: typed API boundary and local fallback.
- `web/src/App.tsx`: current application shell and plan rendering.
