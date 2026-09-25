# Verification

- `go test ./cmd/pastebox/...` passed.
- Tests cover default rejection of `latest`, acceptance when
  `PASTEBOX_ALLOW_LATEST_IMAGE=true`, case-insensitive `true`, and rejection of
  other values.
- `docs/postgresql-redis-deployment.zh-CN.md` documents the explicit override
  and warns that it skips the pinned-image check, including non-`latest` tags.
- The production preflight applies the override only to the pinned-image check;
  `isPinnedImage` remains unchanged.
