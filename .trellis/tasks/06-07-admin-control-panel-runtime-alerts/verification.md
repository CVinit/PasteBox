# Verification

- Service, HTTP, PostgreSQL, and frontend contracts are covered by `TestRuntimeControlStoresPersistConfigRedemptionsAndAlerts`, `TestRuntimeConfigAuditFailureRollsBackConfigAndSecrets`, `TestAdminRuntimeGuestRedemptionAndAlertHTTPContracts`, `TestRedemptionHTTPRejectsInvalidAndRestrictedCodes`, `TestRedemptionCodesValidateBatchRulesAndUserLimits`, `TestRedemptionBatchesEnforceAllowedEmailsAndTotalLimit`, `TestRuntimeAlertsSendRecordFailuresAndRespectCooldown`, and `TestTurnstileVerifierSiteverifyResponses`.
- Manual work item coverage includes failed, malicious, and frozen attachments in `TestAdminManualWorkItemsIncludeFailedMails`; guest upload configuration checks include text and attachment preflight behavior.
- `make test` passed. PostgreSQL-backed `make test-coverage` passed at 76.2% total statement coverage.
- Browser fixture checks exercised admin views at desktop and mobile sizes. These are UI/API-fixture checks, not a live deployment verification.
