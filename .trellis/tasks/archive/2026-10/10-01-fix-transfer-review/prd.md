# Fix Issue 6–12 review findings

## Goal

Fix the six distinct findings from the two-axis review of Issues #6–#12 at
`d4cb26b`, with regression tests before each fix and narrow, compatible changes.
The user approved the proposed next step with “继续下一步” and “继续”.

## Requirements

1. Account SSE must stop when its original session is revoked or expires,
   including logout-all and password reset. Preserve share/claim authorization.
2. A remote text refresh must not overwrite typing or a selection change that
   happened while the request was pending. Explicit reload remains available.
3. Cancelled upload runs must not mutate a newer queue, including late create,
   upload, publish and failure responses.
4. Concurrent uploads of one manifest item across API instances must bind one
   attachment, without leaked active attachments or object references.
5. Pickup-code failure admission is atomic across instances, remains ten failed
   lookups per window, and does not charge successful lookups.
6. Account status reads must use bounded result batches and batched claim state,
   without silently removing access to older records or missing remote edits.

## Acceptance Criteria

- [x] Real HTTP SSE tests cover logout, logout-all and session expiry.
- [x] Delayed frontend responses preserve new typing and current selection.
- [x] Queue cancellation/restart tests verify all old responses are ignored.
- [x] Two PostgreSQL-backed services racing an item create one visible attachment.
- [x] Concurrent clients at nine failures admit only one further failed lookup;
      successful lookups do not consume the failure budget.
- [x] Status payload/query work stays bounded as historical rows grow, with
      coverage for older edited records and published/burned state changes.
- [x] Relevant tests, Go race/vet, PostgreSQL integration, frontend checks and
      build pass; browser evidence is recorded separately from offline tests.

## Technical Approach

- Reuse existing session validation and SSE `closed/unauthorized` signaling.
- Guard async frontend commits by current draft/selection and run generation.
- Put shared concurrency decisions at existing PostgreSQL repository boundaries;
  never hold `Service.mu` over durable I/O or confuse local locks with DB locks.
- Bound account status batches, aggregate live claims in SQL, and preserve
  navigable history and the active editor's change marker.
- Reuse the current object-reference and cleanup transaction machinery.

## Decision / Scope

This is a bug-fix task, not a new product design. Keep the approved anonymous
claim, burn-after-reading, four-locale and existing sharing semantics. Future
protocol changes, distributed broadcasts and broader UI refactors are excluded.
Offline/network failure, cross-instance races and stale async callbacks are
in scope because they directly trigger the reviewed bugs.

## Out of Scope

- No commits, push, deployment, new GitHub Issues, or changes to existing Issues.
- Do not include or overwrite the pre-existing dirty AGENTS/docs/tooling files.
- No historical attachment data repair without separate approval.
- Do not extend page-grant lifetime or add anonymous shared rooms.

## References

- GitHub `CVinit/PasteBox` Issues #6–#12 (body, comments and labels read in review).
- `../09-29-file-transfer-first/glossary.md` and ADR 001–005.
- `.trellis/spec/backend/{quality-guidelines,database-guidelines}.md`.
- `.trellis/spec/frontend/quality-guidelines.md`.

## Baseline Evidence

Before fixes: Go unit/HTTP tests, PostgreSQL integration, relevant `-race`,
TypeScript no-emit and `go vet` pass. In-memory probes against actual frontend
source reproduce late-create queue pollution and remote-refresh draft loss.
Existing tests therefore do not cover all reviewed cases.

## Verification / Handoff

All six fix categories and the scoped checks are complete. See `verification.md`
for failed-before/passed-after evidence, browser checks, and remaining operational
boundaries. No commit, push, deployment, or historical-data repair was performed.
