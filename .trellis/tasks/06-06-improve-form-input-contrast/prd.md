# Redesign public, auth, and clipboard screens

## Goal

Refactor the PasteBox frontend toward the compact online-clipboard style from
`https://online-clipboard.online/online-clipboard/`, add a product introduction
page with top-right login/register actions inspired by `https://pasteapp.io/`,
and rebuild the login/register experience so account creation feels like a
dedicated product screen rather than a raw utility form.

## What I already know

* The existing app already has a functional authenticated workspace, public
  legal/support pages, and share-access screens.
* The default unauthenticated route currently opens a combined auth panel, so it
  does not satisfy the requested product introduction page.
* Inputs, textareas, and selects can blend into light cards unless global form
  styling keeps explicit field fill, borders, and focus rings.
* The requested change is frontend UX and deployment work, not a backend API
  behavior change.

## Requirements

* Make `/` a product introduction page for signed-out visitors with a visible
  top navigation and right-aligned Login/Register buttons.
* Keep signed-in users able to reach the real PasteBox workspace without losing
  existing paste, share, billing, settings, admin, and legal flows.
* Add separate `/login` and `/register` auth routes with a more intentional
  two-column product/auth layout.
* Style the app toward the reference online-clipboard language: compact white
  cards, light gray background, purple accent actions, segmented controls,
  clearly bounded text areas and inputs, subtle shadows, and responsive layout.
* Keep editable form controls visually distinct from card and page surfaces on
  compose, edit/share panels, settings, and auth screens.
* Keep focus states accessible and visibly stronger than default states.
* Keep disabled controls visibly non-interactive.
* Avoid changing form logic or backend API behavior.
* After tests pass, commit the changes, remove the currently running PasteBox
  demo container, and redeploy the updated app.
* Reduce oversized marketing/auth/workspace headline and stat text so the
  screens look balanced rather than dominated by large copy.
* Move public/legal navigation links to a centered bottom footer with grouped
  link columns instead of a vertical sidebar-style stack.
* Move "logout all" out of the sidebar and into Settings with copy that makes
  clear it signs out all device sessions.
* Fix language-switch status feedback so the top status text uses the same
  target language as the page after saving the profile language.

## Acceptance Criteria

* [ ] `/` shows a product introduction page when signed out.
* [ ] The product page has top-right Login and Register buttons.
* [ ] `/login` shows a login-focused page without requiring display name.
* [ ] `/register` shows a registration-focused page with display name.
* [ ] The authenticated workspace visually aligns with the online-clipboard
      reference while preserving existing workflows.
* [ ] Inputs and textareas are visibly distinct from card backgrounds on the compose card.
* [ ] Edit and share panel fields have clear boundaries against their card surface.
* [ ] Settings profile input and language select remain visibly editable.
* [ ] Focus state remains obvious for keyboard users.
* [ ] Frontend build/type-check passes.
* [ ] Browser smoke checks cover landing, login, register, and workspace routes.
* [ ] Git commit exists for the verified change.
* [ ] Running PasteBox app container is removed and redeployed with the new build.
* [ ] Oversized hero/card text is visibly smaller on desktop and mobile.
* [ ] Footer links are grouped and centered at the bottom of public and app pages.
* [ ] Sidebar shows single-device logout only; Settings shows the all-device
      session logout action and description.
* [ ] Repeated language changes do not leave the status pill in the previous
      language after save.

## Out of Scope

* Changing backend profile or paste APIs.
* Adding anonymous paste storage or new public API flows.
* Rebuilding billing/provider behavior.

## Technical Notes

* Likely files: `web/src/App.tsx`, `web/src/styles.css`, and Trellis task/spec
  documentation.
* Prefer route detection from `window.location.pathname` to stay consistent with
  the current single-file React shell.
* Keep the static fallback routes compatible with the Go/Vite deployment model.
