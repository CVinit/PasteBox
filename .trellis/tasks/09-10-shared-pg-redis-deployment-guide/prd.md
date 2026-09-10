# shared-pg-redis-deployment-guide

## Goal

重写一份手把手部署教程：PostgreSQL 和 Redis 拆成两个独立 Compose project 部署、
供其他项目复用；PasteBox 分别接入两个容器网络实现内部互通；ClamAV 等非公共容器
仍随 PasteBox 项目启动；边缘层为宿主机 Nginx 反代（无 Cloudflare，acme 签发
Let's Encrypt）；对象存储对接已独立部署完成的 s3-orchestrator。

## Requirements

### 仓库改动（支撑新教程）

1. 新增 PG-only 共享模板 `compose.shared-postgres.yaml` + env 示例
   `deploy/shared-postgres.env.example`：独立 project（如 `shared-postgres`），
   网络 `shared-postgres-net`，卷 `shared-postgres-data`、`shared-postgres-backups`，
   发布 `127.0.0.1:5432`，保留 wal_level=replica / archive_mode=on / pg_hba.conf
   挂载（模板需随教程复制到 `/opt/shared-postgres/`，pg_hba.conf 一并复制）。
2. 新增 Redis-only 共享模板 `compose.shared-redis.yaml` + env 示例
   `deploy/shared-redis.env.example`：独立 project（如 `shared-redis`），
   网络 `shared-redis-net`，卷 `shared-redis-data`，发布 `127.0.0.1:6379`。
3. 新增 split 模式 override `compose.external-split-services.yaml`（命名可调整）：
   api/worker/migrate/preflight 加入 `shared-postgres-net` + `shared-redis-net`
   两个外部网络；内置 postgres/redis/backup-volume-init 归入
   `integrated-infra` profile；备份类任务 PGHOST 指向 `shared-postgres`，
   卷对齐 `shared-postgres-backups`。
4. `deploy/pastebox-deploy.sh` 新增第三种模式（如 `shared-split`，命名可调整）：
   init 依次启动两个共享 project、等待 PG 就绪、创建 pastebox role/database；
   infra-* 命令同时管理两个 project。保留现有 `shared`、`integrated` 模式不动
   （兼容旧模式）。
5. 新增 `deploy/production.split.env.example`（或复用现有 shared 示例改名），
   DSN 指向 `shared-postgres:5432` / `shared-redis:6379`。

### 教程文档

- 新增 `docs/shared-pg-redis-deployment.zh-CN.md`（名字可微调），手把手式：
  每步含可复制命令和预期输出。
- 起点：全新服务器从零部署（Ubuntu/Debian VPS + Docker + Nginx）。
- 结构覆盖：
  - 架构总览（三个 PasteBox 相关 project + s3o project 的职责与请求链路、
    端口/网络表、其他项目如何接入复用的简要说明）。
  - 服务器准备（Docker、Nginx、目录规划）。
  - 部署共享 PostgreSQL（含 pg_hba.conf、env、启动、健康验证）。
  - 部署共享 Redis（env、启动、健康验证）。
  - PasteBox 部署：镜像固定、env、preflight-root、up、admin 创建。
  - 宿主机 Nginx + acme（Let's Encrypt）反代 pastebox 域名；s3o 已有 Nginx
    配置沿用，extra_hosts host-gateway 访问 s3o。
  - 后台应用配置（对象存储指向 s3o 虚拟 bucket 等）+ 完整 preflight + readyz。
  - 完整备份章节：PG 逻辑备份、WAL 归档、basebackup、restic off-host、
    恢复演练（基于现有 maintenance profile 脚本）。
  - 日常运维：status/logs/down/upgrade/infra-* 命令。
  - FAQ/常见问题排查。
- 与旧文档互链：`docs/deployment.zh-CN.md`、s3o 教程、生产教程头部加指引。

## Acceptance Criteria

- [x] `docker compose config` 渲染通过：两个新共享模板 + split 模式组合
      （production + external-split + nginx-host override）。
- [x] `pastebox-deploy.sh` 三种模式 help/语法正确（`sh -n`）；shared 与
      integrated 模式行为不变。
- [x] 教程中每条命令与仓库实际文件名/变量名一致，可复制执行。
- [x] 教程覆盖 AC：从零 init 共享服务（两个 project）→ PasteBox up → Nginx
      反代 → 后台配置 s3o → preflight → readyz 通过。
- [x] 现有 Makefile/CI 对 compose 渲染与脚本语法的校验继续通过（必要时把新
      文件加入校验清单）。
- [ ] trellis-check 子代理复核通过（进行中，第三次派出）。

## Definition of Done

- 文档与仓库实际 compose/脚本/env 示例一致。
- `make` 相关校验（compose 渲染、脚本语法）通过。
- 旧模式（shared 单 project、integrated）不受影响。

## Technical Approach

- 复用现有 shared 模式的全部机制（外部网络别名 `shared-postgres`/`shared-redis`、
  外部备份卷、maintenance 备份容器改 PGHOST），只把「一个 project 一个网络」
  拆成「两个 project 两个网络」。
- 网络名：`shared-postgres-net`、`shared-redis-net`（服务别名仍为
  `shared-postgres`、`shared-redis`，PasteBox 侧 DSN 不变）。
- 教程中共享服务模板从仓库复制到 `/opt/shared-postgres/`、`/opt/shared-redis/`，
  与 PasteBox 仓库目录解耦；`git pull` 升级 PasteBox 不影响共享 project。

## Decision (ADR-lite)

**Context**: PG/Redis 需独立部署供多项目复用；存在旧的单 project shared 模式。
**Decision**: 拆两个独立 Compose project + 各自网络；仓库提供模板并新增
`shared-split` 部署模式；保留旧 shared/integrated 模式兼容；边缘层用宿主机
Nginx + acme（无 Cloudflare）；教程为新增独立文档，含完整备份章节；起点为全新
服务器。
**Consequences**: 部署脚本和 compose 文件数量增加；需维护三种模式；换来 PG 与
Redis 生命周期完全独立（可独立升级/重启/备份）和其他项目接入更清晰。

## Out of Scope

- s3-orchestrator 本身的部署教程（已独立部署完成，只对接）。
- Cloudflare DNS/TLS/WAF 章节（本教程用 acme + 直接 DNS）。
- 监控告警（monitoring profile）章节。
- 其他项目复用的专门大章节（只在架构总览简要说明接入方式）。
- 演示栈（compose.deploy.yaml）说明。
- 从现有 shared-infra 单 project 迁移到双 project 的迁移指南（教程假设全新
  服务器）。

## Technical Notes

- 已检查文件：compose.shared-services.yaml、compose.external-services.yaml、
  compose.production.yaml、compose.nginx-host.example.yaml、
  deploy/pastebox-deploy.sh、deploy/*.env.example、
  docs/s3-orchestrator-r2-pastebox-docker.zh-CN.md、
  docs/production-docker-nginx-cloudflare.zh-CN.md、docs/deployment.zh-CN.md。
- 备份容器（postgres-backup 等）通过 PGHOST=shared-postgres + 外部卷
  shared-postgres-backups 工作，split 模式沿用同一机制。
- Makefile/CI 现有 compose 渲染校验需确认覆盖新文件（实现时检查 Makefile）。
