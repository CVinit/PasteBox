# Review Commit And Demo Deploy

## Goal

Review the current PasteBox project state, commit the reviewed work, and redeploy the demo environment so the current app can be exercised through the demo Docker Compose stack.

## What I Already Know

* User wants this order: review the project first, then commit, then deploy to the demo environment.
* The repo currently has uncommitted changes, including deployment files and docs.
* Demo deployment uses `compose.deploy.yaml`.
* Production deployment uses `compose.production.yaml` and is out of scope for this task.

## Requirements

* Review the current working tree before committing.
* Run relevant local verification that is practical in this environment.
* Prepare a commit plan that separates recognized task changes from unrelated or unrecognized dirty files.
* Commit only after user confirms the commit plan.
* Deploy to the demo environment using the demo Compose stack.
* Verify the demo service health after deployment.

## Acceptance Criteria

* [ ] Review findings are reported before commit.
* [ ] Relevant tests/checks/builds are run or skipped with a clear reason.
* [ ] Commit plan is confirmed before `git commit`.
* [ ] Demo Compose deployment is running.
* [ ] `/readyz` and `/api/v1/ready` are checked after deployment.

## Out of Scope

* Production deployment.
* Changing product behavior unless review finds a blocker that must be fixed before deployment.
* Force-reverting user or prior-session changes.

## Technical Notes

* Use `compose.deploy.yaml` for the demo stack.
* Use `docs/deployment.zh-CN.md` and deployment runbooks only as references.
* Keep real production secrets out of the repo.
