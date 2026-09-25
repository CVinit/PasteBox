# 补齐 CI 覆盖率门槛并完成远程合并

## Goal

定位 GitHub PR #2 生产就绪检查的覆盖率缺口，只补充有实际行为断言的 Go 测试，使后端语句覆盖率达到 75% 门槛；推送测试修复，等待远程检查通过后将 PR #2 合并到 `main`。

## What I already know

- 当前分支：`feat/preflight-allow-latest-override`；PR #2 目标分支为 `main`。
- GitHub Actions `make production-readiness` 中的 `make test-coverage` 已运行全部测试，但统计覆盖率为 74.3%，低于 75%。
- 当前本地分支上的覆盖率脚本使用 PostgreSQL 集成测试，并将 `go test -coverpkg=pastebox/... -coverprofile=.cache/coverage/backend.out ./...` 作为门禁来源。
- 用户已确认补测试、重新检查并在通过后远程合并。
- 工作区还有未跟踪的本地文件；它们不属于本任务，不纳入提交。

## Assumptions

- 只改测试，不改生产逻辑或覆盖率门槛。
- 合并范围是当前 feature 分支的完整已提交历史，而不只是本轮的 Trellis 哈希清单提交。

## Requirements

- 分析覆盖率 profile，选择最有价值且未覆盖的现有行为路径。
- 新增最小、稳定、可重复运行的测试，覆盖有意义的分支而不是为了数字增加空断言。
- 验证 PostgreSQL 集成覆盖率命令达到至少 75%，并运行相关测试/生产就绪检查。
- 将测试补充提交并推送到 PR #2；远程门禁通过后从 GitHub 合并 PR #2 到 `main`。
- 保留所有未跟踪文件，不暂存或提交它们。

## Acceptance Criteria

- [ ] 新增的测试覆盖此前未覆盖的实际代码路径。
- [ ] `make test-coverage` 通过，汇总语句覆盖率不低于 75%。
- [ ] GitHub Actions PR 检查通过。
- [ ] PR #2 已在远程合并到 `main`，并核实远程提交 SHA。
- [ ] 未跟踪文件保持未提交。

## Definition of Done

- 相关 Go 测试、覆盖率门禁通过；可运行时生产就绪检查通过。
- 仅测试代码及必要的 Trellis 任务记录进入本轮提交。
- PR 合并状态和远程 `main` SHA 已核实。

## Out of Scope

- 改动生产逻辑、降低 75% 覆盖率门槛或绕过失败的 GitHub 检查。
- 纳入工作区已有未跟踪文件。
- 重整或重新提交 feature 分支上既有功能改动。

## Technical Notes

- 远程失败记录：GitHub Actions run `36082381458`，`Build and publish image / Run production readiness gate`。
- 门禁脚本：`scripts/check-postgres-integration.sh`；Make 目标：`make test-coverage`。
- 代码开发遵守 `.trellis/spec/backend/quality-guidelines.md`。
- 本地新增测试后，Go 1.27.1/PostgreSQL 17 的 Linux 容器覆盖率为 75.7%；macOS 本地 `make test-coverage` 为 75.8%。
