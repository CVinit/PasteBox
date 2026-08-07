# Upgrade GitHub Actions to Node 24 runtimes

## Goal

升级 Docker 镜像 workflow 中仍以 Node.js 20 为内部运行时的第三方 Action，消除 GitHub Actions 的弃用警告，并降低 GitHub 在 2026 年秋季移除 Node.js 20 后发生兼容性故障的风险。

## Requirements

* 只修改 `.github/workflows/docker-image.yml` 中受警告影响的 Action 主版本。
* 使用各 Action 官方仓库当前推荐、支持 Node.js 24 的主版本：
  * `actions/checkout@v4` 更新为 `actions/checkout@v7`。
  * `actions/setup-go@v5` 更新为 `actions/setup-go@v6`。
  * `actions/setup-node@v4` 更新为 `actions/setup-node@v7`。
  * `docker/setup-qemu-action@v3` 更新为 `docker/setup-qemu-action@v4`。
  * `docker/setup-buildx-action@v3` 更新为 `docker/setup-buildx-action@v4`。
  * `docker/login-action@v3` 更新为 `docker/login-action@v4`。
  * `docker/metadata-action@v5` 更新为 `docker/metadata-action@v6`。
* 保留已经是当前版本的 `docker/build-push-action@v7`。
* 保留所有触发条件、权限、输入参数、镜像标签规则、平台列表和缓存配置。
* 保留工作区中所有无关未提交文件，不暂存、不覆盖。

## Acceptance Criteria

* [x] workflow 中不再引用本次识别出的 Node.js 20 Action 主版本。
* [x] 版本升级以外的 workflow 内容没有变化。
* [x] workflow YAML 可解析；若本机有 `actionlint`，其检查通过。
* [x] `make production-readiness` 通过。
* [x] 只提交 workflow 和本任务 Trellis 材料，不包含其他工作区改动。
* [x] 提交推送到 `origin/main`。
* [x] 最新提交触发的 `Docker image` workflow 完整成功，且不再出现 Node.js 20 弃用警告。

## Definition of Done

* 本地语法检查和生产 readiness 门禁通过。
* 远端多架构镜像构建及 GHCR 推送成功。
* GitHub Actions 日志不再报告本次 Node.js 20 Action 警告。
* 任务按 Trellis 流程归档并记录开发日志。

## Technical Approach

对 `.github/workflows/docker-image.yml` 做逐行版本标签替换，不调整 Action 的 `with`、`if`、权限或执行顺序。先进行本地 YAML/项目门禁检查，再提交推送，以 GitHub 托管 runner 的完整 workflow 结果作为最终兼容性证明。

## Decision (ADR-lite)

**Context**：GitHub 已开始把旧 Node.js 20 Action 强制运行在 Node.js 24 上，并计划在 2026 年秋季移除 Node.js 20。当前构建成功，但官方要求 workflow 用户升级到支持 Node.js 24 的 Action 版本。

**Decision**：直接升级到各官方仓库当前推荐主版本，而不是设置 `ACTIONS_ALLOW_USE_UNSECURE_NODE_VERSION` 继续使用 Node.js 20。

**Consequences**：可以消除弃用警告并保持在官方支持路径上；主版本升级可能包含行为变化，因此必须通过现有 production readiness 和远端真实镜像发布验证。

## Out of Scope

* 修改 Node.js 业务构建版本 `node-version: 26`。
* 修改 Dockerfile、应用代码、依赖锁文件或镜像标签策略。
* 把 Action 从主版本标签改成提交 SHA 固定。
* 更新 Trellis CLI 或处理其他活跃任务。

## Research References

* [`research/node24-action-upgrades.md`](research/node24-action-upgrades.md) - GitHub Node.js 20 退役时间线、官方版本映射和兼容性注意事项。

## Technical Notes

* 仓库搜索确认目标版本只存在于 `.github/workflows/docker-image.yml`。
* `setup-node@v7` 的额外破坏性变化与当前 workflow 无冲突：项目没有设置 `registry-url` 或依赖虚假的 `NODE_AUTH_TOKEN` 回退，并且已经显式配置 npm 缓存。
* GitHub 托管的 `ubuntu-latest` runner 已在最近成功构建中强制使用 Node.js 24，满足新版 Action 对 runner 的要求。
