# Polish localized compose form UX

## Goal

Improve the localized PasteBox workspace UI based on screenshot review: make the
new-paste content textarea visually obvious, remove visible Simplified/Traditional
Chinese mixed wording in the Traditional Chinese compose/workspace surface, and
make the public/footer legal links align vertically instead of wrapping into a
ragged horizontal cluster.

## What I already know

- The user flagged that the content input area in the new private paste form is
  not obvious enough.
- The user asked whether Simplified Chinese and Traditional Chinese are mixed.
- The screenshot shows the Traditional Chinese UI still using English `paste`
  in labels such as `新增私有 paste` and `為這個 paste 命名`.
- The user flagged the footer/legal links as visually messy and asked to arrange
  them vertically.
- `web/src/App.tsx` contains the locale copy table, compose form, and
  `PublicFooter` component.
- `web/src/styles.css` contains the compose textarea and `.public-footer`
  layout styles.

## Assumptions

- Keep the product name `PasteBox` unchanged.
- Treat `paste` as product/domain copy that should be localized in Chinese UI
  labels where it appears as a generic content item.
- Preserve the existing visual language; make targeted polish rather than a full
  redesign.

## Open Questions

- None blocking. The screenshots and code provide enough direction for this
  small UX correction.

## Requirements

- The new-paste content textarea must have a visible field boundary, sufficient
  padding, and a clear focus state.
- Traditional Chinese copy visible in the workspace/compose flow should not mix
  Simplified Chinese terms or generic English `paste` labels where a natural
  Traditional Chinese term is available.
- Public footer links must render as a vertical, regular list on auth/public
  surfaces, with compact spacing that still works in the sidebar/auth panel.
- Keep public legal document body text unchanged unless it is part of the footer
  chrome or workspace copy already being localized.
- Preserve responsive behavior on desktop and mobile.

## Acceptance Criteria

- [x] The compose content textarea is visually identifiable as an input box in
  the logged-in workspace.
- [x] Traditional Chinese compose/workspace labels no longer show generic
  English `paste` wording in the screenshot areas.
- [x] Footer links render in a vertical, consistently aligned stack.
- [x] `make test-web` passes.
- [x] `node scripts/check-web-launch-surfaces.mjs` passes.

## Definition of Done

- Frontend code and styles are updated.
- Relevant frontend checks pass.
- Browser inspection confirms the target surfaces render correctly.
- Changes are committed through Trellis Phase 3.4.

## Out of Scope

- Full legal body translation.
- Backend/API behavior changes.
- A full redesign of the workspace, auth, or public legal pages.

## Technical Notes

- Relevant files inspected:
  - `web/src/App.tsx`
  - `web/src/styles.css`
  - `.trellis/spec/frontend/index.md`
  - `.trellis/spec/guides/index.md`
- The frontend quality spec requires localized launch copy and `make test-web`
  after frontend locale/copy changes.
