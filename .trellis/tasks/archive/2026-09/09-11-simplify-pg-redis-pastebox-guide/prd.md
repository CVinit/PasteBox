# docs: simplify PostgreSQL Redis PasteBox deployment guide

## Goal

调整 `docs/shared-pg-redis-deployment.zh-CN.md` 的用户可见命名和启动说明，让部署方案直接使用 `postgresql`、`redis`、`pastebox` 三个名称，不刻意区分 `shared`/`split`。PostgreSQL 和 Redis 由操作者手动启动，PasteBox 同时提供直接执行 Compose 命令和执行部署脚本两种启动方式。

## What I already know

- 用户已确认修改方案。
- 当前文档大量使用 `shared-*`、`split` 和 `/opt/shared-*` 命名。
- 仓库现有独立服务模板文件是 `compose.shared-postgres.yaml`、`compose.shared-redis.yaml`，部署脚本是 `deploy/pastebox-deploy.sh`。
- 独立服务模板内部仍使用 `SHARED_*` 环境变量和 `shared-postgres` / `shared-redis` 网络别名；这些真实配置名必须保留，避免文档命令失效。
- Compose project 名称可通过 `-p postgresql`、`-p redis`、`-p pastebox` 指定。

## Requirements

- [ ] 文档中的用户可见目录、Compose project、职责说明和示例命令统一使用 `postgresql`、`redis`、`pastebox` 命名。
- [ ] 删除或弱化对 `shared`/`split` 方案的刻意区分；保留必要的真实文件名、变量名和网络别名说明。
- [ ] PostgreSQL 和 Redis 的启动步骤使用手动 `docker compose` 命令，并包含 `-p postgresql` / `-p redis`。
- [ ] PasteBox 启动步骤同时提供直接 `docker compose` 命令和 `./deploy/pastebox-deploy.sh up` 脚本方式。
- [ ] 后续初始化、运维、备份和故障排查命令与新的命名及启动边界一致。

## Acceptance Criteria

- [ ] 目标文档不存在会误导用户执行的 `shared-split` 启动流程或“脚本自动启动 PostgreSQL/Redis”的描述。
- [ ] 文档保留仓库实际存在的模板文件名和环境变量名，命令路径可从当前仓库复制。
- [ ] `rg` 核对命名、启动方式和脚本引用后无明显矛盾。

## Out of Scope

- 不修改 Compose 文件、部署脚本、环境变量名、网络别名或其他代码。
- 不改动工作区中与本任务无关的既有修改。
- 不执行真实 Docker 部署。

## Technical Notes

- 目标文件：`docs/shared-pg-redis-deployment.zh-CN.md`
- 相关真实文件：`compose.shared-postgres.yaml`、`compose.shared-redis.yaml`、`compose.external-split-services.yaml`、`deploy/pastebox-deploy.sh`
