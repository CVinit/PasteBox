# Fix GitHub Docker build dependency audit failure

## Goal

修复 GitHub Actions `Docker image` workflow 在生产 readiness 门禁中的前端依赖审计失败，使自动镜像构建和发布恢复正常，同时保留现有安全门禁强度。

## Requirements

* 根因以失败 run `31017623652` 和 `31155649701` 的日志为准。
* 只通过 npm 支持的锁文件更新修复受影响的传递依赖。
* 不删除、跳过或降低 `npm audit --audit-level=high`。
* 不在本任务升级 Vite 或 React 主版本。
* 不修改应用代码、GitHub Actions workflow 或其他业务依赖声明，除非锁文件更新无法解决失败。
* 保留工作区内所有无关未提交改动，不暂存、不覆盖。
* 修复后提交并推送到 `origin/main`，持续监控新触发的 Docker workflow 到完成。

## Acceptance Criteria

* [x] `web/package-lock.json` 不再解析到受影响的 `postcss <=8.5.22`。
* [x] `npm --prefix web audit --audit-level=high` 退出码为 0。
* [x] `npm --prefix web run typecheck` 和 `npm --prefix web run build` 通过。
* [x] `make production-readiness` 通过。
* [ ] 提交仅包含锁文件和本任务 Trellis 材料，不包含其他工作区改动。
* [ ] 修复提交推送到 `origin/main`。
* [ ] 修复提交触发的 GitHub `Docker image` workflow 构建并发布镜像成功。

## Definition of Done

* 本地依赖审计、类型检查、构建和生产 readiness 全部通过。
* 远端 Docker workflow conclusion 为 `success`。
* 记录最终解析版本和验证证据。
* 任务完成后按 Trellis 流程归档。

## Technical Approach

运行 `npm audit fix --package-lock-only`，让 npm 在现有依赖范围内重算安全版本。根据 dry-run，预计把 `postcss` 更新到 `8.5.26`、`nanoid` 更新到 `3.3.17`，并把 `esbuild` 调整到不在当前公告影响范围内的 `0.27.2`。随后使用 `npm ci` 按新锁文件重建依赖并执行完整门禁。

## Decision (ADR-lite)

**Context**：当前 Vite 7 依赖范围允许安全的传递依赖版本，CI 失败由新安全公告和旧锁文件组合触发，不是应用代码或 workflow 错误。

**Decision**：只更新锁文件，保留高危审计门禁。

**Consequences**：改动最小且可由 `npm ci` 稳定复现；未来升级 Vite 时 npm 可能重新选择其他传递依赖版本，仍需依靠生产 readiness 持续检查。

## Out of Scope

* Vite 8、`@vitejs/plugin-react` 6 或其他前端主版本升级。
* 修改安全审计阈值或忽略漏洞。
* 修复与本次两个 npm advisory 无关的功能问题。
* 提交工作区中其他任务的文件。

## Research References

* [`research/github-actions-audit-failure.md`](research/github-actions-audit-failure.md) - 远端失败日志、依赖链和 npm dry-run 结果。

## Technical Notes

* Workflow：`.github/workflows/docker-image.yml`。
* Readiness 脚本：`scripts/check-production-readiness.sh`。
* 前端依赖：`web/package.json`、`web/package-lock.json`。
