# Document S3 orchestrator R2 Docker deployment

## Goal

把已经确认的部署教程落盘成中文 Markdown 文档，覆盖 s3-orchestrator 容器聚合多个 Cloudflare R2、本项目 Docker 容器通过宿主机 Nginx 与 Cloudflare CDN 部署并对接 s3-orchestrator 的完整流程。

## What I already know

* 用户已经确认只新建一份教程文档，不改代码、不改 Compose 文件。
* PasteBox 域名走 Cloudflare CDN，宿主机 Nginx 反代 PasteBox 容器。
* s3-orchestrator 也使用 Docker 部署，后端接多个 Cloudflare R2。
* PasteBox 生产 preflight 要求 `PASTEBOX_S3_ENDPOINT` 使用真实 HTTPS 域名，不能直接配置本地 HTTP endpoint。
* 现有生产部署文档已有 `compose.nginx-host.yaml`、Cloudflare Full strict、Nginx 反代、对象存储和健康检查口径。

## Requirements

* 新增中文教程文档，路径为 `docs/s3-orchestrator-r2-pastebox-docker.zh-CN.md`。
* 文档包含域名规划、Cloudflare DNS/CDN 设置、R2 bucket 和密钥准备。
* 文档包含 s3-orchestrator 本地 Docker 构建、配置文件、多个 R2 backend、虚拟 bucket、SQLite 元数据目录和备份提醒。
* 文档包含 PasteBox `deploy/production.env` 对接 s3-orchestrator 的关键环境变量。
* 文档包含宿主机 Nginx 同时反代 `pastebox` 和 `s3o` 的配置要点。
* 文档包含启动顺序、preflight、readiness、S3 API、UI 上传下载删除验证。
* 文档不落真实密钥，不要求直接改生产环境。

## Acceptance Criteria

* [x] `docs/s3-orchestrator-r2-pastebox-docker.zh-CN.md` 存在且为中文 Markdown。
* [x] 文档包含 `compose.nginx-s3o.yaml` 示例。
* [x] 文档包含 `deploy/s3-orchestrator/config.yaml` 示例。
* [x] 文档包含 `deploy/s3-orchestrator.env` 示例。
* [x] 文档包含 `PASTEBOX_S3_ENDPOINT=https://s3o.example.com` 对接示例。
* [x] 文档包含验证和风险点。

## Definition of Done

* 文档已写入仓库。
* 对新增文档做最小自检，确认关键段落存在。
* 不修改代码和真实部署密钥。

## Technical Approach

新增单独教程文档，不触碰现有生产部署文件。文档复用现有 Nginx + Cloudflare 部署口径，并补充 s3-orchestrator 容器、本地构建、多个 R2 backend、PasteBox 对接和验证步骤。

## Out of Scope

* 不实际部署服务。
* 不修改 `compose.production.yaml`、`deploy/production.env.example` 或 Nginx 现有文档。
* 不提交真实 R2、PasteBox、Cloudflare、SMTP、OAuth、支付或备份密钥。

## Technical Notes

* 现有参考：`docs/production-docker-nginx-cloudflare.zh-CN.md`。
* 现有生产环境模板：`deploy/production.env.example`。
* PasteBox S3 配置关键项：`PASTEBOX_S3_ENDPOINT`、`PASTEBOX_S3_BUCKET`、`PASTEBOX_S3_REGION`、`PASTEBOX_S3_ACCESS_KEY`、`PASTEBOX_S3_SECRET_KEY`、`PASTEBOX_S3_USE_PATH_STYLE`。
* s3-orchestrator 生产 endpoint 给 PasteBox 时必须是 HTTPS 域名；容器内部通过 `extra_hosts` 把 `s3o.example.com` 指向宿主机 Nginx，既满足 TLS 证书域名，也避免容器 DNS 回环不稳定。
