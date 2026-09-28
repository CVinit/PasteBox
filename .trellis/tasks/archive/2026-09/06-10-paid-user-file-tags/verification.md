# Verification

- `TestPaidPlanTagLimits`, `TestPasteTagLimitsFollowCurrentPlan`, and `TestDowngradedPasteTagsRemainSearchableButReadOnly` cover defaults, create/edit limits, and downgrade behavior.
- HTTP plan/catalog contract checks expose `tagsPerPasteLimit`; PostgreSQL migration and catalog tests cover persistence. The UI includes disabled free-plan input, editable limits, existing Tag chips, and click-to-filter behavior.
- `make test` passed Go tests, web typecheck, and Vite build.
