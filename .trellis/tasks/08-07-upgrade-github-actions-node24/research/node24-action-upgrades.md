# GitHub Actions Node.js 24 upgrade research

核对日期：2026-08-07。

## GitHub runtime timeline

GitHub 官方公告说明：

* Node.js 20 已在 2026 年 4 月结束生命周期。
* GitHub Actions runner 从 2026 年 6 月 16 日开始默认使用 Node.js 24。
* GitHub 计划在 2026 年秋季从 runner 移除 Node.js 20。
* GitHub 对 workflow 用户的建议是升级到使用 Node.js 24 的最新 Action 版本，而不是依赖临时的 Node.js 20 兼容开关。

来源：<https://github.blog/changelog/2025-09-19-deprecation-of-node-20-on-github-actions-runners/>

## Official version mapping

当前 workflow 与官方仓库示例的版本对应如下：

| Action | 当前版本 | 官方当前主版本 | 处理 |
| --- | --- | --- | --- |
| `actions/checkout` | `v4` | `v7` | 升级 |
| `actions/setup-go` | `v5` | `v6` | 升级 |
| `actions/setup-node` | `v4` | `v7` | 升级 |
| `docker/setup-qemu-action` | `v3` | `v4` | 升级 |
| `docker/setup-buildx-action` | `v3` | `v4` | 升级 |
| `docker/login-action` | `v3` | `v4` | 升级 |
| `docker/metadata-action` | `v5` | `v6` | 升级 |
| `docker/build-push-action` | `v7` | `v7` | 保留 |

官方仓库：

* <https://github.com/actions/checkout>
* <https://github.com/actions/setup-go>
* <https://github.com/actions/setup-node>
* <https://github.com/docker/setup-qemu-action>
* <https://github.com/docker/setup-buildx-action>
* <https://github.com/docker/login-action>
* <https://github.com/docker/metadata-action>
* <https://github.com/docker/build-push-action>

## Compatibility notes

* `actions/setup-node` 从 v5 起将 Action 内部运行时升级到 Node.js 24，要求 runner `v2.327.1` 或更高；GitHub 托管 runner 已满足该要求。
* `setup-node@v6` 会自动检测 npm 缓存，`v7` 移除了无效 `NODE_AUTH_TOKEN` 回退。当前 workflow 已显式设置 `cache: npm`，且没有 `registry-url`，因此这些变化不影响现有配置。
* `docker/metadata-action@v6` 明确把默认内部运行时升级到 Node.js 24，并要求 runner `v2.327.1` 或更高。
* 最近的 GitHub 托管 runner 已把旧 Action 强制运行在 Node.js 24 上并成功完成 workflow，说明 runner 环境满足新版 Action 的最低要求；但只有升级后的真实远端构建才能证明所有主版本组合兼容。

## Local verification

2026-08-07 完成以下检查：

* 全局搜索确认旧 Action 主版本引用已经清零，目标 8 个 Action 版本与 PRD 映射一致。
* Ruby YAML AST 解析成功；本机没有安装 `actionlint`。
* `git diff --check` 通过。
* `make production-readiness` 完整通过，包括 Go 测试、前端类型检查与构建、零漏洞依赖审计、PostgreSQL 集成测试和本地 Docker 镜像构建。

相关说明：

* <https://github.com/actions/setup-node/blob/main/README.md>
* <https://github.com/docker/metadata-action/releases/tag/v6.0.0>
