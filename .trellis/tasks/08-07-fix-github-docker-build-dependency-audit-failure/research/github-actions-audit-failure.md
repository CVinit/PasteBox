# GitHub Actions dependency audit failure

核对日期：2026-08-07。

## Remote evidence

* Run `31017623652` at commit `87a3b82` failed in `Run production readiness gate`.
* Run `31155649701` at commit `eec7c58` failed in the same step.
* Go tests, TypeScript typecheck and Vite build passed before the audit failure.
* `npm audit --audit-level=high` reported `postcss <=8.5.22` as high severity and exited non-zero, so the image build/push step was skipped.
* The same audit also reported `esbuild >=0.27.3 <0.28.1` as low severity.

## Local dependency evidence

Current resolved chain:

```text
vite@7.3.5
  -> postcss@8.5.15
  -> esbuild@0.27.7
```

`npm audit fix --dry-run` proposed:

```text
postcss 8.5.15 -> 8.5.26
nanoid 3.3.12 -> 3.3.17
esbuild 0.27.7 -> 0.27.2
```

All proposed versions stay inside Vite 7's declared dependency ranges. A Vite 8 upgrade is unnecessary for this incident and would expand the regression surface.

## Selected remediation

Use npm's lockfile-only audit fix, rebuild `node_modules` with `npm ci`, then run the same audit and readiness commands used by CI. Do not suppress advisories or weaken the audit level.

## Local verification

Resolved versions after the lockfile-only fix:

```text
postcss 8.5.26
nanoid 3.3.17
esbuild 0.27.2
```

The following checks passed on 2026-08-07:

* Clean `npm ci` installation from the updated lockfile.
* `npm audit --audit-level=high` with zero vulnerabilities.
* `make test-web` including TypeScript and Vite production build.
* `make production-readiness`, including Go tests, PostgreSQL integration tests, deployment configuration checks, project build, and local Docker image build.
