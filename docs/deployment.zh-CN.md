# PasteBox 中文部署文档

本文说明当前 PasteBox MVP 的可部署范围、GitHub Actions 自动构建镜像流程，以及使用 GHCR 镜像通过 Docker Compose 部署的方法。

## 当前可用边界

当前版本可以立即部署用于演示、内部评审、功能走查和低风险试用。容器会在一个进程内同时提供 Go API 和 React/Vite 前端，用户可以通过浏览器完成注册、登录、创建 paste、上传附件、生成分享链接、查看账单桩、使用管理后台和执行清理等 MVP 流程。

当前版本不适合承载真实客户数据、付费公网 SaaS 或长期生产运行，原因如下：

- 用户、会话、paste、附件、分享、订单、审计日志和队列状态都保存在内存中，容器重启后会丢失。
- PostgreSQL、Redis、S3、真实邮件、Stripe、Epusdt、ClamAV 和异步 worker 目前是配置或接口边界，不是生产级真实集成。
- 开发认证流程会在 JSON 响应中返回邮箱验证、magic link 和密码重置 token，便于演示，但不应暴露给真实公网用户。
- Billing webhook 是本地桩流程，不包含真实支付平台签名验证。

## 镜像构建与发布

仓库包含 `.github/workflows/docker-image.yml`。当代码推送到 `main`、推送 `v*.*.*` 版本标签，或手动运行 `workflow_dispatch` 时，GitHub Actions 会构建多架构 Docker 镜像并推送到 GitHub Container Registry：

```text
ghcr.io/cvinit/pastebox:latest
ghcr.io/cvinit/pastebox:sha-<commit>
ghcr.io/cvinit/pastebox:<tag>
```

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

创建 `compose.yaml`：

```yaml
services:
  pastebox:
    image: ghcr.io/cvinit/pastebox:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      PASTEBOX_APP_ENV: production
      PASTEBOX_APP_NAME: PasteBox
      PASTEBOX_HTTP_ADDR: :8080
      PASTEBOX_PUBLIC_URL: https://pastebox.example.com
      PASTEBOX_MAILER_PROVIDER: log
      PASTEBOX_STRIPE_ENABLED: "false"
      PASTEBOX_EPUSDT_ENABLED: "false"
      PASTEBOX_BOOTSTRAP_ADMIN_EMAIL: admin@example.com
      PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD: change-me-admin-password
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
```

必须修改：

- `PASTEBOX_PUBLIC_URL`：改成你的真实 HTTPS 访问地址。
- `PASTEBOX_BOOTSTRAP_ADMIN_EMAIL`：改成你的管理员邮箱。
- `PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD`：改成强密码。

启动：

```sh
docker compose pull
docker compose up -d
docker compose logs -f pastebox
```

本机验证：

```sh
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/api/v1/health
```

预期返回：

```json
{"status":"ok"}
```

以及：

```json
{"app":"PasteBox","env":"production","status":"ok"}
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

当前内存版会在进程启动时根据以下环境变量创建管理员账号：

```sh
PASTEBOX_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD=change-me-admin-password
```

因为数据保存在内存中，容器每次重启后都会重新初始化数据和管理员账号。

## 升级

当 GitHub Actions 发布新镜像后，在服务器执行：

```sh
cd /opt/pastebox
docker compose pull pastebox
docker compose up -d pastebox
docker compose logs -f pastebox
```

注意：当前 MVP 重启会清空所有应用数据。升级前如果需要保留演示数据，请先在 UI 中导出。

## 故障排查

查看容器状态：

```sh
docker compose ps
docker compose logs --tail=200 pastebox
```

确认镜像是否能拉取：

```sh
docker pull ghcr.io/cvinit/pastebox:latest
```

确认端口监听：

```sh
curl -v http://127.0.0.1:8080/healthz
```

常见问题：

- `docker pull` 返回权限错误：GHCR package 可能是私有，需要 `docker login ghcr.io`，或在 GitHub Packages 中公开该 package。
- 浏览器登录后立即丢失登录态：确认访问协议和代理头是否一致。HTTPS 反向代理必须传递 `X-Forwarded-Proto: https`；HTTP 测试环境会自动使用非 `Secure` cookie。
- 重启后数据消失：这是当前内存 MVP 的已知边界，不是持久化部署形态。
- 支付、邮件、病毒扫描没有真实外部效果：当前是 stub，不是生产集成。

## 进入真实生产前必须补齐

在承载真实用户或付费业务前，至少需要完成并重新验证：

- PostgreSQL/sqlc 持久化和迁移。
- S3 兼容对象存储适配器和私有 bucket 下载链路。
- Redis-backed 会话、限流和队列。
- 真实邮件发送，且移除响应中的开发 token。
- Stripe/Epusdt webhook 签名验证、幂等账单对账和订阅生命周期处理。
- ClamAV worker、清理 worker、失败重试和监控告警。
- 备份、恢复、日志、指标、错误追踪和滥用处置 runbook。
