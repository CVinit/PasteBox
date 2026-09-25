# Verification

- Confirmed the clear-filter button's accessible name includes the active tag
  value in `web/src/App.tsx`.
- Confirmed `.tag-chip span` uses `min-width: 0`, `overflow: hidden`,
  `text-overflow: ellipsis`, and `white-space: nowrap` in `web/src/styles.css`.
- `make test-web` passed, including TypeScript typecheck and Vite production
  build.
- The existing untracked workspace files were not changed or staged.
