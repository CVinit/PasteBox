# S3Orchestrator 使用 GitHub 构建镜像

## Goal

把 `docs/s3-orchestrator-r2-pastebox-docker.zh-CN.md` 中 S3Orchestrator 的服务器本地构建流程改为直接使用上游 GitHub Container Registry 已构建镜像，减少部署和升级步骤，方便及时获取新版本。

## Requirements

- 使用固定版本镜像 `ghcr.io/afreidah/s3-orchestrator:v0.62.28`，不使用 `latest`。
- 删除服务器上的上游源码目录、克隆源码、校验源码 commit 和本地 Docker 构建步骤。
- Compose 通过 `.env` 中的 `S3O_IMAGE` 引用镜像，默认示例为固定版本。
- 首次部署使用 `docker compose pull`，并保留配置校验、健康检查和固定版本检查。
- 升级流程改为更新 `S3O_IMAGE`、拉取镜像、验证配置并重建服务。
- 回滚流程改为恢复旧 `S3O_IMAGE`，同时保留数据库兼容性提醒。
- 同步修改目录结构、执行顺序、检查清单及所有相关文字，避免残留本地构建说明。
- PasteBox 原有 GHCR 镜像部署方式保持不变。

## Acceptance Criteria

- [x] 目标文档不再包含 S3Orchestrator 的 `source/`、`build:`、`local/s3-orchestrator` 或 `docker compose build` 部署指令。
- [x] 文档中的 Compose 示例使用 `image: ${S3O_IMAGE:?set S3O_IMAGE to a pinned GHCR tag or digest}`。
- [x] 首次部署、升级和回滚命令前后一致，可通过文档静态检查。
- [x] S3Orchestrator 固定版本仍为 `v0.62.28`，且明确不建议生产使用 `latest`。

## Out of Scope

- 修改应用代码、仓库 Compose 文件或 GitHub Actions。
- 自动跟随 `latest`。
- 改动 PasteBox 的镜像发布流程。

## Technical Notes

- 已通过上游 GitHub Release 确认 `v0.62.28` 是正式版本。
- 已通过 `docker manifest inspect ghcr.io/afreidah/s3-orchestrator:v0.62.28` 确认镜像存在，并支持 Linux amd64 和 arm64。
- 本任务只修改目标中文部署文档及对应 Trellis 任务记录。
