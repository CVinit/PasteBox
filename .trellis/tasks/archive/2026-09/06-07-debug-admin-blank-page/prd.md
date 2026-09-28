# debug admin blank page

## Goal

Fix the local admin control panel deployment so an admin can open `/app`, log in, click the admin navigation, and see the control panel instead of a blank page.

## What I Already Know

- The user reports the admin page opens blank.
- The local API should run at `http://127.0.0.1:8080`.
- The admin login to verify is `1@1.com` with password `12345678`.
- Browser MCP inspection is explicitly allowed for checking page errors.

## Assumptions

- This is a local deployment/debug task, not a product scope change.
- Fixes may include rebuilding frontend assets, syncing embedded static assets, restarting the local API, or making a small code fix if browser/runtime errors reveal one.
- Keep changes minimal and do not redesign the admin UI.

## Requirements

- Reproduce the blank admin page in a browser.
- Inspect browser console and network errors.
- Fix the root cause with the smallest safe change.
- Rebuild or redeploy local static assets if stale assets are the issue.
- Restart the local API with the requested bootstrap admin credentials.
- Verify admin login and the control panel in a browser.

## Acceptance Criteria

- [x] `http://127.0.0.1:8080/readyz` returns ready.
- [x] Admin login with `1@1.com` / `12345678` succeeds.
- [x] Clicking the admin navigation renders the control panel.
- [x] Browser console has no blocking runtime error for the admin panel.
- [x] Relevant build/test command is run if code or bundled assets change.

## Out of Scope

- Changing production credentials.
- Reworking the admin panel design.
- Adding new admin features beyond fixing the blank page.

## Technical Notes

- Frontend source is under `web/src`.
- Built frontend assets are served from `internal/httpserver/static`.
- Backend admin APIs are under `/api/v1/admin/*`; the user-facing route is `/app`.
