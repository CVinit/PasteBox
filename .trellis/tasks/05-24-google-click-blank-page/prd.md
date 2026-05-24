# Fix Google click blank page in test environment

## Goal

Fix the test-environment failure where clicking the Google auth button leaves
the app in a broken authenticated-looking state and refresh does not preserve a
usable session.

## What I already know

- User report: in the test environment, clicking Google makes the page content
  disappear, and refreshing does not recover it.
- The frontend Google button calls `POST /api/v1/auth/google` through
  `web/src/api.ts`.
- The backend Google OAuth stub creates or updates a verified user and sets the
  `pastebox_session` HttpOnly cookie.
- Local Vite development on `http://localhost:5173` works after clicking Google.
- Local production Docker image on `http://localhost:18081` works because
  localhost can accept the cookie path in this browser.
- Local production Docker image through a plain HTTP LAN address reproduces the
  session breakage: Google returns success, then authenticated follow-up
  requests to `/quota`, `/pastes`, `/shares`, and `/billing/orders` return 401.

## Assumptions

- The test environment is accessed over plain HTTP or through a proxy that does
  not present the original HTTPS scheme to the app.
- The frontend should not enter a misleading authenticated state unless the
  backend session cookie is usable by follow-up API requests.

## Requirements

- Session cookie `Secure` behavior must support HTTP-only test environments
  without breaking HTTPS production deployments.
- HTTPS deployments behind a reverse proxy should still mark session cookies
  `Secure` when the forwarded scheme is HTTPS.
- Login/register/Google/magic auth responses should continue to set an
  HttpOnly `pastebox_session` cookie.
- The fix must be covered by backend tests.
- Deployment docs must explain the test-environment cookie behavior.

## Acceptance Criteria

- [x] Clicking Google in a plain HTTP test deployment results in a persisted
  usable session, not an authenticated-looking state with 401 follow-up calls.
- [x] Refresh after Google auth restores the signed-in workspace in that test
  deployment.
- [x] HTTPS/proxied production requests still receive `Secure` session cookies.
- [x] `make test` passes.

## Definition of Done

- Tests added or updated for cookie behavior.
- Backend/frontend checks pass.
- Docs/spec updated if cookie contract changes.
- Trellis finish flow can archive the task after commit.

## Out of Scope

- Implementing real Google OAuth network integration.
- Replacing the current in-memory repository.
- Changing unrelated auth flows beyond shared session-cookie behavior.

## Technical Notes

- Relevant backend files: `internal/config/config.go`,
  `internal/httpserver/server.go`, `internal/httpserver/server_test.go`.
- Relevant frontend files: `web/src/App.tsx`, `web/src/api.ts`.
- Relevant docs/specs: `docs/deployment.md`, `docs/deployment.zh-CN.md`,
  `.trellis/spec/backend/quality-guidelines.md`.
