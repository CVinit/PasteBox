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

## Scenario: Admin Runtime Control API Consumption

### 1. Scope / Trigger

- Trigger: The frontend consumes or edits admin runtime config, plan catalog,
  redemption batches, manual work items, provider status, runtime panel, or
  alert history.

### 2. Signatures

- Typed client file: `web/src/api.ts`
- Admin UI shell: `web/src/App.tsx`
- Endpoints:
  `GET/PATCH /api/v1/admin/runtime-config`,
  `GET /api/v1/admin/runtime-panel`,
  `GET /api/v1/admin/manual-work-items`,
  `PATCH /api/v1/admin/catalog`,
  `POST /api/v1/admin/providers/{provider}/test`,
  `GET/POST/PATCH /api/v1/admin/redemption-batches`,
  `GET /api/v1/admin/alerts`,
  `POST /api/v1/admin/alerts/test`, and
  `POST /api/v1/redemptions/redeem`.

### 3. Contracts

- `RuntimeConfig`, `RuntimePanel`, `ManualWorkItem`, `RedemptionBatch`,
  `RedemptionCode`, and `AlertEvent` in `api.ts` must mirror backend JSON
  fields exactly, including camelCase `OperationalMetrics` fields.
- Admin runtime edits are staged in React state and saved through
  `client.adminUpdateRuntimeConfig({ guestUploads, alerts })`; numeric inputs
  must send numbers, not formatted byte strings.
- Admin catalog edits must expose all backend plan limit fields and price
  fields that operators can change: visibility, purchase switch, period,
  amount cents, currency, storage limits, single item limits, retention,
  attachment count, daily upload, and daily share download.
- Provider status UI must never render provider secrets. It may display
  configured/missing env state, provider name, non-sensitive fields, and last
  test status.
- New visible admin copy must be added to every supported locale in
  `copyFor(locale)`.

### 4. Validation & Error Matrix

- Non-admin admin endpoint response -> backend returns `403 admin_required`;
  frontend should surface the API error instead of assuming admin data exists.
- Missing runtime config response -> render disabled/null-safe controls until
  admin data loads.
- Backend field rename without `api.ts` update -> `make test-web` typecheck or
  build should fail when the field is referenced.
- Secret-bearing response field -> frontend contract violation; do not add it
  to `api.ts` or render it.

### 5. Good/Base/Bad Cases

- Good: Add a new alert threshold by adding the Go JSON field, `api.ts` type,
  localized label, admin form control, handler test, and `make test-web`.
- Base: Render runtime resource values with `formatBytes` while keeping the
  raw editable threshold inputs numeric.
- Bad: Add only a backend admin route and call it through an untyped
  `api<Record<string, any>>`, or expose only one quota field while hiding the
  rest of the editable plan/guest limit contract.

### 6. Tests Required

- Run `make test-web` for every admin UI or typed client change.
- Run full `make test` when backend admin JSON fields, routes, or validation
  behavior change.
- Handler tests must cover representative admin runtime/catalog/redemption/
  alert contracts and non-admin rejection.

### 7. Wrong vs Correct

#### Wrong

```ts
const panel = await api<any>("/admin/runtime-panel");
```

#### Correct

```ts
const panel = await client.adminRuntimePanel();
panel.operational.mailFailedDepth;
```

---

## Examples

- `web/src/api.ts`: typed API boundary and local fallback.
- `web/src/App.tsx`: current application shell and plan rendering.
