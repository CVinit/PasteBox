# Fix Frontend Tag Accessibility Review Findings

## Goal

Apply the two accepted frontend review fixes for tag filtering: expose the active tag value in the clear-filter button's accessible name, and make long tag chip text truncate reliably.

## What I Already Know

- The current tag chip buttons already expose `aria-pressed` and toggle the active filter off when clicked again.
- The clear-filter pill uses `aria-label={t("clearTagFilter")}`, which hides the visible tag value from assistive technology.
- `.tag-chip span` has `overflow` and `text-overflow` but lacks `min-width: 0` and `white-space: nowrap`.

## Requirements

- The clear tag-filter button must include the active tag value in its accessible name.
- Long tag labels inside tag chips must not expand or spill out of the chip.
- Keep changes scoped to `web/src/App.tsx` and `web/src/styles.css`.

## Acceptance Criteria

- [x] Clear-filter button renders an accessible name that includes both the clear action and current tag.
- [x] `.tag-chip span` supports reliable ellipsis truncation in flex layout.
- [x] Frontend typecheck/build passes.

## Definition of Done

- `make test-web` passes, or any inability to run it is reported with a concrete reason.
- No unrelated dirty files are staged or committed.

## Out of Scope

- Changing tag filter product behavior beyond the accepted review fixes.
- Memoizing `parseTagInput`.
- Backend tag limit logic.

## Technical Notes

- Target lines from review: `web/src/App.tsx` clear filter pill and `web/src/styles.css` `.tag-chip span`.
- Verification: `make test-web` passed on 2026-06-22.
- Commit note: these fixes sit on top of the current uncommitted tag-control WIP, so they should be committed together with that WIP or split manually by the owner of that change.
