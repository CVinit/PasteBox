# Quality Guidelines

> Code quality standards for frontend development.

---

## Overview

Frontend changes must preserve a usable PasteBox application screen, keep
TypeScript strict, and build with Vite.

---

## Forbidden Patterns

- Do not turn the first screen into a marketing-only landing page unless the
  active task explicitly requests a public product introduction page. When that
  exception applies, keep a stable product workspace route such as `/app`.
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
- Keep editable form controls visually distinct from card and page surfaces.
  If a wrapper and the real `input`/`textarea`/`select` are both styled, verify
  later reset selectors do not remove the control's border, fill, or focus ring.

---

## Testing Requirements

- Run `make test-web` before reporting frontend work complete.
- `make test-web` runs `tsc -b --pretty false` and `vite build`.
- Run full `make test` when the frontend consumes changed backend API fields.

## Scenario: Form Control Visual Boundaries

### 1. Scope / Trigger

- Trigger: Any frontend change that touches global form CSS, compose forms,
  side panels, auth forms, settings forms, or select/search wrappers.

### 2. Signatures

- Primary stylesheet: `web/src/styles.css`.
- Common controls: `.title-input`, `.composer textarea`, `.composer-controls
  input`, `.form-grid input`, `.form-grid select`, `.panel input`, `.panel
  select`, and `.panel textarea`.

### 3. Contracts

- Editable controls must have visible fill, border, and focus treatment against
  the current warm card surface.
- Wrapper-only controls, such as search boxes and select boxes, may keep the
  inner input/select transparent only when the wrapper itself provides the
  visible field boundary.
- Disabled controls must look non-interactive without disappearing into the
  panel background.

### 4. Validation & Error Matrix

- Light panel + editable input -> visible field boundary.
- Focused editable input -> stronger brand-colored border/ring.
- Disabled editable input -> muted non-interactive surface.
- Hidden file input inside drop zone -> remains hidden and does not gain visible
  field chrome.

### 5. Good/Base/Bad Cases

- Good: The compose title, body, tag input, edit textarea, share inputs, and
  settings language select all read as editable fields before focus.
- Base: Search and filter controls still preserve their wrapper-based styling.
- Bad: A later reset selector sets `border: 0` and `background: transparent` on
  real editable fields that need their own boundary.

### 6. Tests Required

- Run `make test-web` after changing form styles.
- Browser-check the compose, edit/share side panel, and settings form when the
  change is visually substantial.

### 7. Wrong vs Correct

#### Wrong

```css
.form-grid input {
  border: 1px solid var(--line);
  background: var(--panel);
}

.form-grid input {
  border: 0;
  background: transparent;
}
```

#### Correct

```css
.form-grid input {
  border: 1px solid var(--field-border);
  background: var(--field-bg);
  box-shadow: var(--field-shadow);
}
```

---

## Scenario: Admin Dashboard Responsive Layout

### 1. Scope / Trigger

- Trigger: Any frontend change that touches the admin dashboard panel, admin
  card layout, runtime controls, queues, orders, or admin-only form grids.

### 2. Signatures

- Primary component: admin view in `web/src/App.tsx`.
- Primary stylesheet: admin-scoped selectors in `web/src/styles.css`.
- Required layout classes: `.admin-panel`, `.admin-console`, `.admin-grid`,
  `.admin-section`, `.admin-section--wide`, `.admin-section--compact`, and
  `.admin-audit-section`.

### 3. Contracts

- Admin sections must use explicit semantic classes for grid span decisions;
  do not rely on `section:nth-child(...)` because adding or reordering admin
  sections silently breaks layout.
- Admin list cards that mix text, status badges, inputs, and action buttons
  must use admin-scoped grid constraints so content wraps instead of clipping.
- Admin form controls and action buttons must keep at least 44px touch/click
  height and visible focus/disabled states.
- The admin panel and `.admin-grid` must not make the page horizontally
  scrollable at 375px or desktop widths.

### 4. Validation & Error Matrix

- 375px admin view: `.admin-panel.scrollWidth <= .admin-panel.clientWidth` and
  `.admin-grid.scrollWidth <= .admin-grid.clientWidth`.
- Desktop admin view: same width assertions, with all admin sections visible.
- Mixed order card (`content + status + input + button`) -> wraps into stable
  rows rather than forcing a wide flex row.
- Long webhook, queue, email, or redemption-code text -> wraps or scrolls inside
  its own code block, not the whole page.

### 5. Good/Base/Bad Cases

- Good: Runtime config and catalog sections use `.admin-section--wide`; compact
  status panels use `.admin-section--compact`; regular sections use
  `.admin-section`.
- Base: Existing admin actions still work and keep lucide icons where an action
  icon exists.
- Bad: Layout span rules depend on `nth-child`, or `.list-card` stays a single
  flex row for order cards with status, manual-payment input, and action button.

### 6. Tests Required

- Run `make test-web` after changing admin UI or admin CSS.
- Run full `make test` when synced embedded static assets or backend-adjacent
  admin behavior changes are part of the same slice.
- Browser-check the admin view at 375px and desktop width; assert no console
  errors and no admin panel/grid horizontal overflow.

### 7. Wrong vs Correct

#### Wrong

```css
.admin-panel .admin-grid section:first-child,
.admin-panel .admin-grid section:nth-child(4) {
  grid-column: span 8;
}
```

#### Correct

```tsx
<section className="admin-section admin-section--wide">
  ...
</section>
```

```css
.admin-panel .admin-section--wide {
  grid-column: span 8;
}
```

---

## Scenario: Billing Order Status Presentation

### 1. Scope / Trigger

- Trigger: Any frontend change that touches `Order.status`, billing order
  cards, admin order cards, admin billing reconciliation controls, or payment
  lifecycle wording.

### 2. Signatures

- Type: `Order.status` in `web/src/api.ts`
- User UI: billing order cards in `web/src/App.tsx`
- Admin UI: admin orders section in `web/src/App.tsx`
- Admin API client: `client.adminReconcileBilling()`

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
- Admin reconciliation controls must call the typed API client and refresh
  admin plus authenticated billing data after completion.

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

## Scenario: Attachment Scan Status Presentation

### 1. Scope / Trigger

- Trigger: Any frontend change that touches `Attachment.scanStatus`,
  attachment download rows, owner paste cards, share previews, or public share
  attachment links.

### 2. Signatures

- Type: `Attachment.scanStatus` and optional `Attachment.risk` in
  `web/src/api.ts`.
- UI helper: `attachmentScanDetail(attachment, locale, context)` in
  `web/src/App.tsx`.
- UI component: `AttachmentDownloadItem` in `web/src/App.tsx`.
- Download paths: `attachmentDownloadPath()` and
  `sharedAttachmentDownloadPath()` in `web/src/api.ts`.

### 3. Contracts

- Known scan statuses are `clean`, `pending`, `scan_failed`, and `malicious`;
  unknown provider strings must render safely with a neutral badge.
- Owner attachment rows must show a stable scan label, a short description, and
  any backend-provided risk before download. Owners may download `pending` and
  `scan_failed` files, but the UI must make the risk state explicit.
- Public share attachment rows must only render active download links for
  `clean` files. Pending, failed, malicious, and unknown scan states must render
  as blocked or non-downloadable rows rather than links that the backend will
  reject.
- `malicious` files must appear blocked in owner and public contexts, matching
  the backend global malicious-file gate.
- Scan status copy must be available for English and Chinese locales when the
  rest of the screen is localized.
- Backend-provided `Attachment.risk` must use the same locale as the scan
  status copy, including `Risk` for English, `风险` for Simplified Chinese,
  `風險` for Traditional Chinese, and `Riesgo` for Spanish.

### 4. Validation & Error Matrix

- `clean` -> success badge and active owner/public download link.
- `pending` -> warning badge; owner link active with risk copy; public link
  blocked.
- `scan_failed` -> warning badge; owner link active with caution copy; public
  link blocked until retry succeeds.
- `malicious` -> danger badge; owner and public links blocked.
- Unknown non-empty scan status -> neutral badge and blocked public link.

### 5. Good/Base/Bad Cases

- Good: A pending owner attachment says public sharing waits for a clean scan
  while preserving the owner download link.
- Base: A clean attachment still renders as a normal downloadable file with a
  clean badge.
- Bad: Rendering a raw `<a>` for every attachment, because public share links
  would invite users to click downloads that are known to fail the scan gate.

### 6. Tests Required

- Run `make test-web` after changing attachment scan presentation.
- Run full `make test` when the frontend consumes backend scan fields or when
  backend scan policy changes in the same slice.

### 7. Wrong vs Correct

#### Wrong

```tsx
<a href={sharedAttachmentDownloadPath(token, attachment.id, password)}>
  {attachment.fileName}
</a>
```

#### Correct

```tsx
const scan = attachmentScanDetail(attachment, locale, "public");
return scan.canDownload ? (
  <a href={sharedAttachmentDownloadPath(token, attachment.id, password)}>
    <span className={`scan-badge scan-badge--${scan.tone}`}>{scan.label}</span>
  </a>
) : (
  <span aria-disabled="true">{scan.description}</span>
);
```

---

## Scenario: Frontend Locale Preference And Copy Coverage

### 1. Scope / Trigger

- Trigger: Any frontend change that adds user-visible copy, changes the
  language selector, alters registration/profile payloads, or renders anonymous
  public pages/shares.

### 2. Signatures

- Type: `Locale = "en" | "zh-CN" | "zh-TW" | "es"` in `web/src/App.tsx`.
- Helper: `localeFor(language?: string)`.
- User setting: `client.updateMe({displayName, language})`.
- Registration payload:
  `client.register({email, password, displayName, language})`.
- Launch check: `node scripts/check-web-launch-surfaces.mjs`.

### 3. Contracts

- The supported launch locales are English, Simplified Chinese, Traditional
  Chinese, and Spanish.
- Browser language fallback maps `zh-CN`, `zh-SG`, and bare `zh` to
  Simplified Chinese; `zh-TW`, `zh-HK`, `zh-MO`, and `zh-Hant` to Traditional
  Chinese; `es-*` to Spanish; all other values to English.
- Authenticated UI language follows `user.language`; anonymous auth, public
  share, and public legal/support chrome follow browser locale.
- New registrations must send the current locale in the `language` field so
  first-session users keep their selected language after account creation.
- `copyFor(locale)` must keep English fallback behavior so missing optional copy
  renders a safe label instead of crashing.
- Status helpers such as `orderStatusDetail` and `attachmentScanDetail` must
  localize known statuses and safely describe unknown provider/scanner strings.
- Chinese UI chrome should avoid generic English domain words like `paste` when
  a natural Simplified/Traditional Chinese term is available. Keep product
  names such as `PasteBox` unchanged.
- Public footer/legal links must render as an explicit navigation list with
  stable alignment instead of relying on arbitrary flex wrapping.

### 4. Validation & Error Matrix

- Unsupported browser language -> English UI fallback.
- Legacy stored language `zh` -> Simplified Chinese UI.
- Traditional Chinese browser or stored language -> Traditional Chinese UI.
- Spanish browser or stored language -> Spanish UI.
- Missing copy key -> English fallback, then key name as last resort.
- Unknown order or scan status -> neutral provider/scanner status copy; no
  crash or hidden action.

### 5. Good/Base/Bad Cases

- Good: A Spanish browser lands on the auth screen, sees Spanish primary copy,
  registers, and the registration request persists `language: "es"`.
- Good: A user can switch between English, Simplified Chinese, Traditional
  Chinese, and Spanish from Settings without changing backend schemas.
- Base: Public legal document body can remain the existing launch source text,
  but public navigation, support contact chrome, and footer labels must follow
  locale.
- Bad: Add a new visible button label only in English while the rest of the
  screen is localized.
- Bad: Store bare `zh` from the new selector; use `zh-CN` or `zh-TW` instead.
- Bad: Render Traditional Chinese workspace chrome such as `新增私有 paste` or
  `為這個 paste 命名`; use localized object wording such as `新增私有內容`.
- Bad: Let public footer links wrap into a ragged horizontal cluster on narrow
  sidebars or auth panels; use a consistent vertical list.

### 6. Tests Required

- Run `make test-web` after locale or copy changes.
- Run `node scripts/check-web-launch-surfaces.mjs` after building so the
  production bundle is checked for supported locale selectors and multilingual
  launch copy.
- Run full `make test` when backend language payload handling changes.

### 7. Wrong vs Correct

#### Wrong

```tsx
<option value="zh">中文</option>
client.register(auth)
```

#### Correct

```tsx
<option value="zh-CN">简体中文</option>
<option value="zh-TW">繁體中文</option>
client.register({ ...auth, language: locale })
```

---

## Code Review Checklist

- Does the UI still present a functional PasteBox workspace as the first
  viewport?
- Are button labels and icons readable on mobile widths?
- Are backend response fields typed instead of inferred as `any`?
- Does the app behave acceptably when the API is not running locally?
