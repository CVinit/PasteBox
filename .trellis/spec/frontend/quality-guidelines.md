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

## Code Review Checklist

- Does the UI still present a functional PasteBox workspace as the first
  viewport?
- Are button labels and icons readable on mobile widths?
- Are backend response fields typed instead of inferred as `any`?
- Does the app behave acceptably when the API is not running locally?
