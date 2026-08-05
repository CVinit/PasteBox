# 扩写 S3Orchestrator + Cloudflare R2 + PasteBox Docker 部署教程

## Goal

把现有 `docs/s3-orchestrator-r2-pastebox-docker.zh-CN.md` 扩写成一份面向首次部署者的中文生产部署教程。读者应能从空白 Cloudflare R2 账号和一台 Linux 宿主机开始，完成多个 R2 bucket、S3Orchestrator、PasteBox GHCR 镜像、宿主机 Nginx 和 HTTPS 的配置，并通过端到端测试确认附件上传、下载和删除链路可用。

## Requirements

* 只修改现有中文教程以及本任务的 Trellis 材料，不修改应用代码、仓库 Compose 模板或真实生产配置。
* 默认环境为 Ubuntu 22.04/24.04 或 Debian 12、Docker Engine、Docker Compose v2、宿主机 Nginx、Cloudflare DNS。
* 明确两个应用在同一宿主机、同一 Docker Engine 中运行，但使用两个独立部署目录和两个独立 Compose project：`/opt/s3-orchestrator` 与 `/opt/pastebox`。
* S3Orchestrator 固定使用上游正式 tag `v0.62.28` 从源码构建本地镜像；不得用未固定的 `latest`。
* PasteBox 使用 GitHub Actions 自动发布的 `ghcr.io/cvinit/pastebox` 镜像，生产示例固定到 `sha-<commit>` 或 digest，并说明公开和私有 GHCR package 的拉取差异。
* Cloudflare R2 部分从开通 R2 开始，逐步说明创建 bucket、命名、位置/管辖区选择、默认私有状态、查询 Account ID、创建 R2 专用 API token、选择 `Object Read & Write`、限制到单个 bucket、一次性保存 Access Key ID 与 Secret Access Key。
* 推荐每个 R2 bucket 使用独立凭据，给出 Cloudflare 字段到 S3Orchestrator `BACKEND*_` 变量的逐项映射表。
* 解释 R2 S3 endpoint、`region=auto`、path-style，以及普通 jurisdiction、EU jurisdiction endpoint 的差异。
* 在部署 S3Orchestrator 前，使用 AWS CLI 分别验证每个真实 R2 backend 的 `HeadBucket`、上传、下载和删除。
* S3Orchestrator 使用独立 `compose.yaml`、`.env`、`config.yaml` 和持久化 `data/` 目录；端口只绑定 `127.0.0.1:19000`。
* 详细说明虚拟 bucket 凭据和 R2 backend 凭据是两组不同的凭据，PasteBox 只能使用虚拟 bucket 凭据。
* 解释 `routing_strategy`、`replication.factor`、`quota_bytes`、最大对象大小、SQLite 元数据的重要性，并提供适合多个 R2 bucket 聚合容量的示例。
* PasteBox 部署使用仓库的 `compose.production.yaml` 和 `deploy/production.env.example`，但只拉 GHCR 镜像，不在服务器构建 PasteBox。
* 创建本地 `compose.nginx-host.yaml` 覆盖文件，为 `api` 绑定 `127.0.0.1:18080`，并为 `api`、`worker`、`preflight` 添加 `s3o.example.com:host-gateway`。
* 明确使用宿主机 Nginx 时不要启动生产 Compose 中的 `caddy` 服务。
* PasteBox 配置必须使用 `PASTEBOX_S3_ENDPOINT=https://s3o.example.com`、S3Orchestrator 虚拟 bucket 名称和凭据、`PASTEBOX_S3_REGION=us-east-1`、`PASTEBOX_S3_USE_PATH_STYLE=true`。
* DNS 示例为 `pastebox.example.com` 走 Cloudflare Proxied，`s3o.example.com` 走 DNS only；解释这样做与 S3 SigV4、请求体、缓存、WAF 和上传限制的关系。
* 宿主机 Nginx 同时终止两个域名的 TLS，并把请求分别反代到 `127.0.0.1:18080` 与 `127.0.0.1:19000`；保留 Host 和请求体流式传输相关配置。
* 给出严格启动顺序：S3Orchestrator -> Nginx -> PasteBox 基础依赖 -> preflight -> migrate -> api/worker。
* 每组命令注明执行目录、目的、预期结果，以及失败时下一步查看什么。
* 验收覆盖：R2 backend 直连测试、S3Orchestrator 虚拟 S3 API 测试、PasteBox health/readiness、浏览器真实附件上传/下载/分享/删除、R2 dashboard 落盘确认。
* 运维章节覆盖日志、SQLite 元数据备份、配置和密钥备份、PostgreSQL 备份提示、升级、回滚、扩容、凭据轮换和故障排查。
* 所有示例仅使用占位符，不写入真实密钥；提醒 `.env` 文件设置 `600` 权限并避免进入 Git、终端历史和日志。

## Acceptance Criteria

* [x] 现有文档成为可从零照做的中文教程，不依赖读者先阅读其他 PasteBox 部署文档。
* [x] Cloudflare R2 建桶和凭据创建包含控制台菜单路径、每个关键字段的推荐值和安全说明。
* [x] 文档清楚展示两个独立 Compose project 在同一宿主机上的目录、端口和请求拓扑。
* [x] S3Orchestrator 的 `compose.yaml`、`.env`、`config.yaml` 示例可以互相对应，变量名没有缺失。
* [x] PasteBox 的镜像、环境变量和本地 Compose override 与当前仓库 `compose.production.yaml`、`deploy/production.env.example`、`.github/workflows/docker-image.yml` 一致。
* [x] 文档没有指导用户启动 `caddy`，没有让 S3Orchestrator 的 `9000` 端口直接暴露公网。
* [x] R2 backend 凭据、S3Orchestrator 虚拟 bucket 凭据、PasteBox S3 配置三者映射清楚。
* [x] 文档包含可复制的配置检查、启动、健康检查、S3 smoke、升级、回滚和备份命令。
* [x] 文档内所有命令块、文件路径、环境变量和标题经过静态自检。
* [x] 只改目标文档和本任务 Trellis 文件，不覆盖工作区现有未提交改动。

## Definition of Done

* 文档按确认的生产拓扑完成扩写。
* 官方 Cloudflare、GitHub 和 S3Orchestrator 资料链接保留在文档中。
* 运行 Compose 渲染或等价的配置语法检查；无法对真实 Cloudflare/R2/VPS 做在线部署时，明确说明未验证边界。
* 运行文档关键字、占位符、路径和敏感信息检查。
* 使用 `trellis-check` 做最终质量核对。

## Technical Approach

保留宿主机 Nginx + 真实 HTTPS S3 endpoint 的既有生产约束，但把原来将 S3Orchestrator 合并进 PasteBox Compose override 的写法改成两个独立 Compose project。PasteBox 容器通过 `extra_hosts` 把 `s3o.example.com` 解析到 Docker host gateway，再以正确域名和 TLS SNI 访问宿主机 Nginx；Nginx 转发到仅监听 `127.0.0.1:19000` 的 S3Orchestrator。

PasteBox 部署目录从仓库取得生产 Compose 和辅助文件，运行时使用 GitHub Actions 发布的固定 GHCR 镜像。这样既保留现有生产依赖和 preflight，又能让两个服务独立升级、回滚和备份。

## Decision (ADR-lite)

**Context**：PasteBox 生产 preflight 要求对象存储 endpoint 是真实 HTTPS 域名；用户又要求 S3Orchestrator 与 PasteBox 容器运行在同一宿主机。

**Decision**：使用两个独立 Compose project 和宿主机 Nginx。S3Orchestrator 的容器端口只绑定回环地址，PasteBox 通过 `s3o.example.com -> host-gateway -> Nginx -> 127.0.0.1:19000` 访问它。

**Consequences**：配置比容器直接访问 `http://s3-orchestrator:9000` 多一个域名和证书，但满足生产 preflight、TLS 和服务独立运维要求。Nginx、DNS、证书和 `extra_hosts` 任一配置错误都会影响 readiness，因此教程必须提供分层检查命令。

## Out of Scope

* 不代替用户购买或操作真实 Cloudflare 账号。
* 不写入、提交或验证用户的真实 R2、GitHub、PasteBox、SMTP、OAuth、支付密钥。
* 不修改 PasteBox 代码、生产 Compose 模板或 GitHub Actions。
* 不在本轮实际登录远程 VPS 或部署公网服务。
* 不承诺 Cloudflare 免费额度、套餐限制或上游版本长期不变；涉及当前状态的内容附官方链接和核对日期。

## Research References

* [`research/deployment-contracts.md`](research/deployment-contracts.md) - Cloudflare R2、S3Orchestrator、GHCR 和本仓库的当前部署约束。

## Technical Notes

* 目标文档：`docs/s3-orchestrator-r2-pastebox-docker.zh-CN.md`。
* PasteBox 生产 Compose：`compose.production.yaml`。
* PasteBox 环境变量模板：`deploy/production.env.example`。
* PasteBox 镜像工作流：`.github/workflows/docker-image.yml`。
* 相关生产教程：`docs/production-docker-nginx-cloudflare.zh-CN.md`、`docs/deployment.zh-CN.md`。
* 既有兼容性研究：`.trellis/tasks/06-09-s3-orchestrator-streaming-transfer/research/s3-orchestrator-compatibility.md`。
