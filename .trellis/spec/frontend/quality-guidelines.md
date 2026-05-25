# Quality Guidelines

> Code quality standards for frontend development.

---

## Overview

Frontend changes must preserve a usable PasteBox application screen, keep
TypeScript strict, and build with Vite.

---

## Forbidden Patterns

- Do not turn the first screen into a marketing-only landing page.
- Do not duplicate backend plan limits in UI code except for the documented
  local development fallback in `src/api.ts`.
- Do not use `any` for backend API response types.
- Do not add visible instructional text explaining UI mechanics unless it is a
  real product label or placeholder.

---

## Required Patterns

- Use TypeScript strict mode.
- Use lucide-react icons for recognizable UI actions when an icon exists.
- Keep interactive controls stable across responsive layouts with explicit
  grid/flex constraints.
- Keep API data formatting in helper functions instead of inline string
  concatenation spread across components.

---

## Testing Requirements

- Run `make test-web` before reporting frontend work complete.
- `make test-web` runs `tsc -b --pretty false` and `vite build`.
- Run full `make test` when the frontend consumes changed backend API fields.

---

## Scenario: Billing Order Status Presentation

### 1. Scope / Trigger

- Trigger: Any frontend change that touches `Order.status`, billing order
  cards, admin order cards, or payment lifecycle wording.

### 2. Signatures

- Type: `Order.status` in `web/src/api.ts`
- User UI: billing order cards in `web/src/App.tsx`
- Admin UI: admin orders section in `web/src/App.tsx`

### 3. Contracts

- Known statuses are `pending`, `paid`, `failed`, `expired`, `canceled`,
  `refunded`, and `needs_review`; the API type may still accept provider
  extension strings.
- Billing and admin order cards must not display raw status text alone. They
  must show a stable label, a short lifecycle description, and a visual badge
  tone.
- Status copy must be available for English and Chinese locales when the rest
  of the screen is localized.
- Unknown provider status strings must render safely with a neutral badge
  instead of crashing or hiding the order.

### 4. Validation & Error Matrix

- Known status -> localized label and expected badge tone.
- Unknown non-empty status -> raw status label, neutral provider-status
  description.
- Empty status -> neutral `Unknown` label.

### 5. Good/Base/Bad Cases

- Good: A refunded order shows a refunded badge and revocation-oriented
  description in both user billing and admin orders.
- Base: A paid order still shows as active/paid and the admin mark-paid button
  remains disabled.
- Bad: Rendering `{order.status}` as the only user-facing state, because
  provider failure, expiry, cancel, and refund states are easy to miss.

### 6. Tests Required

- Run `make test-web` after changing status presentation.
- Run full `make test` when backend lifecycle statuses or API fields changed in
  the same slice.

### 7. Wrong vs Correct

#### Wrong

```tsx
<strong>
  {order.planId} · {order.status}
</strong>
```

#### Correct

```tsx
const status = orderStatusDetail(order.status, locale);
<span className={`order-status order-status--${status.tone}`}>
  {status.label}
</span>
```

---

## Code Review Checklist

- Does the UI still present a functional PasteBox workspace as the first
  viewport?
- Are button labels and icons readable on mobile widths?
- Are backend response fields typed instead of inferred as `any`?
- Does the app behave acceptably when the API is not running locally?
