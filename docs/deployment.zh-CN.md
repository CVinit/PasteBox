# PasteBox 中文部署文档

本文说明当前 PasteBox MVP 的演示部署范围、GitHub Actions 自动构建镜像流程，以及使用 GHCR 镜像通过 Docker Compose 部署的方法。

## 当前可用边界

当前版本可以通过演示 Compose 栈部署用于演示、内部评审、功能走查和低风险试用。API 容器提供 Go API 和内置 React/Vite 前端，旁路容器提供 PostgreSQL、Redis、MinIO 兼容对象存储、数据库迁移、bucket 初始化和 PasteBox worker。

该演示部署不是公网生产上线栈，原因如下：

- 使用演示默认值、本地 PostgreSQL volume、本地 Redis、本地 MinIO 和 log mail。
- 默认使用 heuristic 扫描；如需 ClamAV 可通过环境变量启用并确保服务可达。
- 不包含 HTTPS edge、production preflight、off-host backup、恢复演练、PITR 证据和生产告警。
- 开发认证流程可在 JSON 响应中返回邮箱验证、magic link 和密码重置 token，便于演示，但不应暴露给真实公网用户。

如果要执行已确认的生产上线 Phase 0A 基线，请使用
`docs/production-deployment-runbook.md` 和 `compose.production.yaml`，不要使用本
文的演示部署文件。生产基线包含 API/worker、PostgreSQL、Redis、HTTPS
反向代理、production preflight、readiness 检查、备份任务和回滚 gate；但在
承载真实公网用户或付费业务前，仍必须完成生产密钥、托管对象存储、真实
SMTP/OAuth/支付/扫描凭据、备份恢复演练、回滚演练、监控告警和支持合规工作流验证。

## 镜像构建与发布

仓库包含 `.github/workflows/docker-image.yml`。当代码推送到 `main`、推送 `v*.*.*` 版本标签，或手动运行 `workflow_dispatch` 时，GitHub Actions 会构建多架构 Docker 镜像并推送到 GitHub Container Registry：

```text
ghcr.io/cvinit/pastebox:latest
ghcr.io/cvinit/pastebox:sha-<commit>
ghcr.io/cvinit/pastebox:<tag>
```

部署时应使用 `sha-*` 标签或 digest。`latest` 只是便于人工检查的移动标签，不能作为生产上线基线的部署镜像。

Pull Request 只构建镜像，不推送镜像。

如果目标机器拉取镜像失败，请检查：

- GitHub Actions 是否已成功完成。
- 仓库 Actions 权限是否允许写入 packages。
- GHCR package 是否公开；如果是私有 package，先执行 `docker login ghcr.io`。

## 服务器要求

最低演示环境：

- Linux 服务器或支持 Docker 的主机。
- Docker Engine 和 Docker Compose plugin。
- 可访问 `ghcr.io`。
- 一个可用域名和 HTTPS 反向代理，推荐用于非本机访问。

示例安装检查：

```sh
docker version
docker compose version
```

## Docker Compose 部署

在服务器上创建目录：

```sh
mkdir -p /opt/pastebox
cd /opt/pastebox
```

可以复制仓库中的演示部署模板。该模板会启动 API、worker、PostgreSQL、
Redis、MinIO、迁移任务和 bucket 初始化任务：

```sh
cp compose.deploy.yaml compose.yaml
```

在 `compose.yaml` 同目录创建 `.env`，或先导出这些变量。镜像必须使用 GitHub Actions 产出的不可变 `sha-*` 标签、registry digest，或本地构建的 `pastebox:local`：

```sh
PASTEBOX_IMAGE=ghcr.io/cvinit/pastebox:sha-<commit>
PASTEBOX_PUBLIC_URL=http://localhost:8080
PASTEBOX_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD=<long-random-password>
```

启动：

```sh
docker compose pull
docker compose up -d
docker compose logs -f pastebox
```

如果通过自己的 HTTPS 反向代理做演示，把 `PASTEBOX_PUBLIC_URL` 设置为
`https://pastebox.example.com`，并把 `X-Forwarded-Proto: https` 转发给
`pastebox` 服务。

本机验证：

```sh
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/api/v1/health
curl -fsS http://127.0.0.1:8080/api/v1/ready
```

预期返回：

```json
{"status":"ok"}
```

以及：

```json
{"app":"PasteBox","env":"development","status":"ready","components":[{"name":"database","status":"ok"},{"name":"object_storage","status":"ok"},{"name":"redis","status":"ok"},{"name":"scanner","status":"skipped","message":"clamav scanner is not configured"},{"name":"worker_queue","status":"ok"},{"name":"worker","status":"skipped","message":"worker heartbeat is required in production"},{"name":"mail","status":"skipped","message":"smtp provider is not configured"}]}
```

以及：

```json
{"app":"PasteBox","env":"development","status":"ok"}
```

以及：

```json
{"app":"PasteBox","env":"development","status":"ready","components":[{"name":"database","status":"ok"},{"name":"object_storage","status":"ok"},{"name":"redis","status":"ok"},{"name":"scanner","status":"skipped","message":"clamav scanner is not configured"},{"name":"worker_queue","status":"ok"},{"name":"worker","status":"skipped","message":"worker heartbeat is required in production"},{"name":"mail","status":"skipped","message":"smtp provider is not configured"}]}
```

浏览器打开：

```text
https://pastebox.example.com
```

## HTTPS 反向代理示例

生产模式下浏览器访问应使用 HTTPS。PasteBox 会根据请求是否来自 HTTPS 来决定
session cookie 是否带 `Secure` 标记；如果 TLS 在反向代理终止，必须转发
`X-Forwarded-Proto: https`，否则后端无法判断浏览器原始访问协议。Nginx 示例：

```nginx
server {
    listen 443 ssl http2;
    server_name pastebox.example.com;

    client_max_body_size 5g;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
    }
}
```

如果只在 HTTP 测试环境演示，可以直接通过普通 HTTP 访问；此时 PasteBox 会省略
`Secure` cookie 标记，避免浏览器丢弃登录态。不要把这种 HTTP 模式暴露给真实公网用户。

如果希望明确使用开发环境文案和调试行为，可以临时设置：

```yaml
PASTEBOX_APP_ENV: development
PASTEBOX_PUBLIC_URL: http://localhost:8080
```

真实生产环境仍应使用 HTTPS，并保留上方 `X-Forwarded-Proto: https` 代理头。

## 管理员账号

演示部署会在进程启动时根据以下环境变量创建或更新管理员账号：

```sh
PASTEBOX_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD=<long-random-password>
```

管理员账号保存在 PostgreSQL 中，演示栈正常重启后会保留。

## 升级

当 GitHub Actions 发布新镜像后，在服务器执行：

```sh
cd /opt/pastebox
grep '^PASTEBOX_IMAGE=' .env
# 编辑 .env，把 PASTEBOX_IMAGE 替换为新的 sha-* 标签或 digest。
docker compose pull pastebox
docker compose up -d pastebox
docker compose logs -f pastebox
```

演示栈的数据保存在 Docker volumes 中。删除 volumes 前请先备份或从 UI 导出需要保留的数据。

## 故障排查

查看容器状态：

```sh
docker compose ps
docker compose logs --tail=200 pastebox
```

确认镜像是否能拉取：

```sh
docker pull ghcr.io/cvinit/pastebox:sha-<commit>
```

确认端口监听：

```sh
curl -v http://127.0.0.1:8080/healthz
```

常见问题：

- `docker pull` 返回权限错误：GHCR package 可能是私有，需要 `docker login ghcr.io`，或在 GitHub Packages 中公开该 package。
- 浏览器登录后立即丢失登录态：确认访问协议和代理头是否一致。HTTPS 反向代理必须传递 `X-Forwarded-Proto: https`；HTTP 测试环境会自动使用非 `Secure` cookie。
- `readyz` 显示 object storage 失败：确认 `minio-init` 已成功创建 bucket，或重新执行 `docker compose run --rm minio-init`。
- `readyz` 显示 database 失败：确认 `migrate` 任务成功完成，或查看 `docker compose logs migrate postgres`。
- 支付、邮件、病毒扫描没有真实外部效果：演示默认使用 log mail、禁用支付渠道、heuristic 扫描；生产前必须配置真实提供商并验证。

## 进入真实生产前必须补齐

在承载真实用户或付费业务前，必须改用生产 runbook 并验证：

- 固定镜像 tag/digest、production preflight 和迁移 gate。
- 托管 S3 兼容附件存储和 off-host backup 存储。
- 真实 SMTP、Google OAuth、Stripe、Epusdt 和扫描服务凭据。
- 邮件、OAuth、支付 webhook、Epusdt callback 和扫描的 provider smoke test。
- 备份完整性、逻辑恢复演练、PITR 恢复演练和回滚演练证据。
- 指标、日志、告警、证书续期检查和滥用/支持工作流。
- 密钥处理、法律/支持页面、数据保留矩阵和操作 runbook 与实际 provider 配置一致。
