# 同步 S3Orchestrator 部署文档与后台配置

## Goal

让 `docs/s3-orchestrator-r2-pastebox-docker.zh-CN.md` 与当前 PasteBox 部署和管理员后台配置机制一致，避免继续要求部署者把托管配置写入 `.env`。

## What I already know

* `deploy/production.env.example` 已只保留生产启动、数据库、可信代理和备份所需变量。
* SMTP、OAuth、S3、扫描器、支付、站点信息等应用配置已迁移到管理员后台。
* 文档已有一组未提交的 S3Orchestrator GHCR 镜像改动，必须完整保留。
* 用户已确认按当前项目实现同步整份文档。

## Requirements

1. 生产 `.env` 示例严格对齐 `deploy/production.env.example`。
2. 补充 `PASTEBOX_CONFIG_ENCRYPTION_KEY` 和 `PASTEBOX_TRUSTED_PROXY_CIDRS` 的生成、含义与部署注意事项。
3. 将 SMTP、OAuth、S3、扫描器、支付、站点信息改为首次启动后在管理员后台配置。
4. 同步生产预检、启动顺序、S3 接入、密钥轮换、故障排查和最终检查清单。
5. 保留文档现有的 S3Orchestrator GHCR 固定版本镜像改动。

## Acceptance Criteria

* [x] 文档不再要求把管理员托管配置写入 `deploy/production.env`。
* [x] 文档中的长期环境变量与 `deploy/production.env.example` 一致。
* [x] S3Orchestrator 的 endpoint、bucket、region、path-style 和访问密钥明确从管理员后台设置。
* [x] 生产启动顺序能处理“先启动基础服务，再登录后台补齐配置，再做就绪与 smoke 检查”。
* [x] 全文没有遗留的旧配置操作指引。
* [x] `git diff --check -- docs/s3-orchestrator-r2-pastebox-docker.zh-CN.md` 通过。

## Definition of Done

* 文档完成局部补丁式同步。
* 使用全文变量检索核对所有旧指引。
* 不覆盖任务开始前的 GHCR 文档改动或其他无关脏文件。

## Out of Scope

* 不修改应用代码、Compose、环境模板或其他部署文档。
* 不改动 S3Orchestrator 本身的部署架构和现有 GHCR 版本选择。

## Technical Notes

* 目标文档：`docs/s3-orchestrator-r2-pastebox-docker.zh-CN.md`
* 配置基准：`deploy/production.env.example`
* 管理端 API：`GET/PUT /api/v1/admin/managed-config`
