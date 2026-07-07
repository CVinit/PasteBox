# PasteBox Docker + 宿主机 Nginx + Cloudflare 部署教程

本文按真实公网部署来写：PasteBox 用 Docker Compose 跑在服务器上，宿主机
Nginx 负责反代和 TLS 终止，Cloudflare 负责 DNS、CDN、WAF 和边缘 HTTPS。

示例域名统一写成 `pastebox.example.com`，部署时替换成你的真实域名。

## 部署结构

推荐结构：

```text
用户浏览器
  -> Cloudflare CDN / WAF
  -> 宿主机 Nginx 443
  -> 127.0.0.1:18080
  -> Docker Compose api:8080
  -> PostgreSQL / Redis / ClamAV / Worker
  -> 托管 S3 兼容对象存储
  -> SMTP / OAuth / Stripe / Epusdt
```

生产部署不要使用 `compose.deploy.yaml`。那个文件适合演示和内部走查，里面带
本地 MinIO、Mailpit 和开发便利开关。公网生产应使用：

- `compose.production.yaml`
- `deploy/production.env.example`
- 本文里的 `compose.nginx-host.yaml` 覆盖文件
- 宿主机 Nginx 配置
- Cloudflare Full (strict)

`compose.production.yaml` 默认带 Caddy 服务。因为这里改用宿主机 Nginx，所以
不要启动 `caddy` 服务，而是用 Compose override 把 API 只绑定到
`127.0.0.1:18080`。

## 服务器准备

建议最低配置：

- Ubuntu 22.04/24.04 或 Debian 12。
- 2 核 CPU、4 GB 内存起步；如果文件扫描和并发上传较多，建议 4 核 8 GB。
- 磁盘至少 50 GB；PostgreSQL、Docker 日志、本地备份 staging 都会占空间。
- 一个真实域名，例如 `pastebox.example.com`。
- Docker Engine 和 Docker Compose plugin。
- 宿主机 Nginx。

安装示例：

```sh
sudo apt update
sudo apt install -y ca-certificates curl gnupg nginx openssl git
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker "$USER"
newgrp docker
docker version
docker compose version
nginx -v
```

创建部署目录：

```sh
sudo mkdir -p /opt/pastebox
sudo chown "$USER:$USER" /opt/pastebox
cd /opt/pastebox
```

## 准备镜像

推荐使用 GitHub Actions 发布到 GHCR 的不可变镜像：

```text
ghcr.io/cvinit/pastebox:sha-<commit>
```

不要用 `latest` 做生产部署。`latest` 会移动，回滚和排查都不稳定。

如果 GHCR package 是私有的，服务器先登录：

```sh
echo '<github-token>' | docker login ghcr.io -u '<github-username>' --password-stdin
```

这个 token 至少需要读取 package 的权限。公开 package 可以跳过登录。

## 上传运行文件

服务器目录需要这些文件：

```text
compose.production.yaml
deploy/production.env.example
deploy/postgres/pg_hba.conf
deploy/backup/postgres-backup.sh
deploy/backup/postgres-basebackup.sh
deploy/backup/postgres-wal-check.sh
deploy/backup/postgres-restore-drill.sh
deploy/backup/postgres-pitr-restore-drill.sh
deploy/backup/restic-backup.sh
deploy/monitoring/textfile-metrics.sh
deploy/monitoring/prometheus.yml
deploy/monitoring/pastebox-alerts.yml
deploy/monitoring/blackbox.yml
```

最简单方式是在服务器拉取仓库：

```sh
cd /opt
git clone https://github.com/CVinit/PasteBox.git pastebox
cd /opt/pastebox
git checkout main
```

如果你只想发布一个固定版本，先在本地打 tag 或记录 commit，然后服务器上切到
对应 commit：

```sh
git fetch --all --tags
git checkout <release-commit>
```

## 创建 Nginx 覆盖 Compose 文件

在 `/opt/pastebox` 创建 `compose.nginx-host.yaml`：

```yaml
services:
  api:
    ports:
      - "127.0.0.1:${PASTEBOX_HOST_HTTP_PORT:-18080}:8080"
```

这个文件只做一件事：把容器内 `api:8080` 绑定到宿主机本地
`127.0.0.1:18080`。公网无法直接访问这个端口，只能通过 Nginx。

后续所有生产命令都带上两个 Compose 文件：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  <command>
```

## 配置生产环境变量

创建真实环境文件：

```sh
cp deploy/production.env.example deploy/production.env
chmod 600 deploy/production.env
```

编辑 `deploy/production.env`。至少确认这些关键项：

```sh
PASTEBOX_IMAGE=ghcr.io/cvinit/pastebox:sha-<commit>
PASTEBOX_DOMAIN=pastebox.example.com
PASTEBOX_PUBLIC_URL=https://pastebox.example.com
PASTEBOX_CORS_ALLOWED_ORIGINS=https://pastebox.example.com

PASTEBOX_APP_ENV=production
PASTEBOX_HTTP_ADDR=:8080
PASTEBOX_HTTP_READ_TIMEOUT_SECONDS=0
PASTEBOX_HTTP_WRITE_TIMEOUT_SECONDS=0
PASTEBOX_LOG_LEVEL=info

PASTEBOX_ADMIN_EMAIL=admin@example.com
PASTEBOX_SUPPORT_EMAIL=support@example.com
PASTEBOX_ABUSE_EMAIL=abuse@example.com

PASTEBOX_CSRF_SECRET=<long-random-secret>
PASTEBOX_METRICS_TOKEN=<long-random-token>

PASTEBOX_POSTGRES_PASSWORD=<long-random-password>
PASTEBOX_DATABASE_URL=postgres://pastebox:<same-password>@postgres:5432/pastebox?sslmode=disable

PASTEBOX_REDIS_ADDR=redis:6379
PASTEBOX_WORKER_ID=pastebox-worker
PASTEBOX_WORKER_HEARTBEAT_MAX_AGE_SECONDS=120

PASTEBOX_SCANNER_PROVIDER=clamav
PASTEBOX_CLAMAV_ADDR=clamav:3310

PASTEBOX_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD=<long-random-admin-password>
```

生成随机密钥：

```sh
openssl rand -base64 48
openssl rand -hex 32
```

### 对象存储

生产附件不要使用本地 MinIO。使用托管 S3 兼容服务，例如 Cloudflare R2、
AWS S3、Backblaze B2 S3、Wasabi、MinIO 集群等。

```sh
PASTEBOX_S3_ENDPOINT=https://<s3-endpoint>
PASTEBOX_S3_BUCKET=pastebox-prod
PASTEBOX_S3_REGION=us-east-1
PASTEBOX_S3_ACCESS_KEY=<object-storage-access-key>
PASTEBOX_S3_SECRET_KEY=<object-storage-secret-key>
PASTEBOX_S3_USE_PATH_STYLE=true
```

如果用 Cloudflare R2，endpoint 通常类似：

```text
https://<account-id>.r2.cloudflarestorage.com
```

### 邮件 SMTP

生产必须配置真实 SMTP，不能用 Mailpit：

```sh
PASTEBOX_MAILER_PROVIDER=smtp
PASTEBOX_SMTP_HOST=smtp.example.com
PASTEBOX_SMTP_PORT=587
PASTEBOX_SMTP_USERNAME=<smtp-user>
PASTEBOX_SMTP_PASSWORD=<smtp-password>
PASTEBOX_SMTP_FROM_EMAIL=no-reply@example.com
PASTEBOX_SMTP_FROM_NAME=PasteBox
PASTEBOX_SMTP_TLS_MODE=starttls
```

如果你的服务商要求 465 端口，通常用：

```sh
PASTEBOX_SMTP_PORT=465
PASTEBOX_SMTP_TLS_MODE=tls
```

### OAuth 回调

Google OAuth 回调地址：

```text
https://pastebox.example.com/api/v1/auth/google/callback
```

如果启用 GitHub OAuth，回调地址：

```text
https://pastebox.example.com/api/v1/auth/github/callback
```

环境变量按生产 OAuth 应用填写，不能用本地开发应用。

### 支付回调

Stripe webhook：

```text
https://pastebox.example.com/api/v1/billing/webhooks/stripe
```

Epusdt webhook：

```text
https://pastebox.example.com/api/v1/billing/webhooks/epusdt
```

生产首次上线如果启用支付，需要填好：

```sh
PASTEBOX_STRIPE_ENABLED=true
PASTEBOX_STRIPE_WEBHOOK_SECRET=whsec_...
PASTEBOX_STRIPE_CHECKOUT_URL_TEMPLATE=https://...

PASTEBOX_EPUSDT_ENABLED=true
PASTEBOX_EPUSDT_PID=<pid>
PASTEBOX_EPUSDT_SECRET_KEY=<secret>
PASTEBOX_EPUSDT_CHECKOUT_URL_TEMPLATE=https://...
PASTEBOX_EPUSDT_ADDRESS=<usdt-address>
PASTEBOX_EPUSDT_CHAIN=USDT-TRC20
```

### 备份存储

备份要用独立的 off-host S3 兼容存储，不要复用附件存储的 access key：

```sh
PASTEBOX_RESTIC_REPOSITORY=s3:https://<backup-s3-endpoint>/pastebox-backups
PASTEBOX_RESTIC_PASSWORD=<long-random-restic-password>
PASTEBOX_BACKUP_S3_ACCESS_KEY=<backup-access-key>
PASTEBOX_BACKUP_S3_SECRET_KEY=<backup-secret-key>
PASTEBOX_BACKUP_S3_REGION=us-east-1
```

## Cloudflare 配置

### DNS

在 Cloudflare DNS 添加：

```text
Type: A
Name: pastebox
Content: <server-ipv4>
Proxy status: Proxied
```

如果有 IPv6，再添加 AAAA。

### SSL/TLS

推荐：

- SSL/TLS encryption mode: `Full (strict)`
- Always Use HTTPS: 开启
- Automatic HTTPS Rewrites: 开启
- Minimum TLS Version: TLS 1.2 或更高
- HTTP/3: 可开启

不要用 `Flexible`。Flexible 会让 Cloudflare 到源站走 HTTP，容易造成 cookie、
回调 URL、重定向和安全判断问题。

### Origin Certificate

如果不想在源站申请公开 CA 证书，可以使用 Cloudflare Origin Certificate：

1. Cloudflare 控制台进入 `SSL/TLS -> Origin Server`。
2. 创建证书，Hostnames 填：

   ```text
   pastebox.example.com
   ```

3. 保存证书到服务器：

   ```sh
   sudo mkdir -p /etc/nginx/ssl/pastebox
   sudo nano /etc/nginx/ssl/pastebox/origin.pem
   sudo nano /etc/nginx/ssl/pastebox/origin.key
   sudo chmod 600 /etc/nginx/ssl/pastebox/origin.key
   ```

4. Nginx 使用这两个文件。

Cloudflare Origin Certificate 只被 Cloudflare 信任，不适合用户绕过
Cloudflare 直接访问源站。配合防火墙限制源站 443 只允许 Cloudflare IP 更稳。

### 缓存规则

不要开启全站 `Cache Everything`。PasteBox 有登录态、CSRF、分享访问、上传和下载。

推荐规则：

- Bypass cache:
  - `/api/*`
  - `/app*`
  - `/s/*`
  - `/login`
  - `/register`
  - `/password-reset*`
  - `/admin*`
- Cache static assets:
  - `/assets/*`
  - `/favicon.svg`
  - `/manifest.webmanifest`

上传大小受 Cloudflare 套餐限制。免费/Pro 常见单次请求上限是 100 MB。如果你
要支持更大的文件上传，需要升级 Cloudflare 套餐、改用 DNS only，或把上传做成
直传对象存储。

### WAF 和安全

建议开启：

- Bot Fight Mode 或 WAF 托管规则。
- Rate limiting，至少覆盖 `/api/v1/auth/*`、`/api/v1/guest/*`、
  `/api/v1/shares/*`。
- 防火墙只允许 Cloudflare IP 访问 80/443，SSH 只允许你的办公 IP。

更新 Cloudflare 真实 IP 配置：

```sh
sudo mkdir -p /etc/nginx/conf.d
{
  curl -fsS https://www.cloudflare.com/ips-v4 | sed 's/^/set_real_ip_from /; s/$/;/'
  curl -fsS https://www.cloudflare.com/ips-v6 | sed 's/^/set_real_ip_from /; s/$/;/'
  echo 'real_ip_header CF-Connecting-IP;'
} | sudo tee /etc/nginx/conf.d/cloudflare-real-ip.conf
sudo nginx -t
sudo systemctl reload nginx
```

建议把这段做成月度维护任务，因为 Cloudflare IP 段可能变化。

## 宿主机 Nginx 配置

创建 `/etc/nginx/sites-available/pastebox.conf`：

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}

upstream pastebox_api {
    server 127.0.0.1:18080;
    keepalive 32;
}

server {
    listen 80;
    listen [::]:80;
    server_name pastebox.example.com;

    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name pastebox.example.com;

    ssl_certificate /etc/nginx/ssl/pastebox/origin.pem;
    ssl_certificate_key /etc/nginx/ssl/pastebox/origin.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;

    client_max_body_size 100m;

    proxy_connect_timeout 30s;
    proxy_send_timeout 300s;
    proxy_read_timeout 300s;

    add_header X-Content-Type-Options nosniff always;
    add_header X-Frame-Options DENY always;
    add_header Referrer-Policy strict-origin-when-cross-origin always;
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains; preload" always;

    location / {
        proxy_pass http://pastebox_api;
        proxy_http_version 1.1;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Proto https;

        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        proxy_request_buffering off;
        proxy_buffering off;
    }
}
```

启用站点：

```sh
sudo ln -sf /etc/nginx/sites-available/pastebox.conf /etc/nginx/sites-enabled/pastebox.conf
sudo nginx -t
sudo systemctl reload nginx
```

`client_max_body_size` 不能超过 Cloudflare 当前套餐允许的上传上限，否则请求会先
在 Cloudflare 被拒。应用内部还会按套餐限制单文件大小。

## 防火墙

基础规则：

```sh
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
sudo ufw status
```

更稳的做法是只允许 Cloudflare IP 访问 80/443。这个规则较长，先确认你有
SSH 备用通道再做，避免把自己锁在外面。

## 预检

在本地 release checkout 先跑：

```sh
make production-readiness
node scripts/check-production-release-evidence.mjs --self-test
```

在服务器 `/opt/pastebox` 跑：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  config
```

生产 preflight：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm preflight
```

如果 preflight 报 `CHANGE_ME`、`example.com`、HTTP URL、本地 SMTP、本地对象存储、
弱密码、缺少 OAuth/支付配置等错误，不要绕过，按提示改真实配置。

## 首次启动

拉取镜像：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance \
  pull api worker preflight migrate
```

启动基础服务：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  up -d postgres redis clamav
```

执行迁移：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm migrate
```

启动应用和 worker：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  up -d api worker
```

不要启动 `caddy`：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  ps
```

确认 `api`、`worker`、`postgres`、`redis`、`clamav` 都在运行。

## 健康检查

宿主机本地：

```sh
curl -fsS http://127.0.0.1:18080/healthz
curl -fsS http://127.0.0.1:18080/readyz
curl -fsS http://127.0.0.1:18080/api/v1/health
curl -fsS http://127.0.0.1:18080/api/v1/ready
```

公网域名：

```sh
curl -fsS https://pastebox.example.com/healthz
curl -fsS https://pastebox.example.com/readyz
curl -fsS https://pastebox.example.com/api/v1/ready
```

生产 ready 预期类似：

```json
{"app":"PasteBox","env":"production","status":"ready","components":[{"name":"database","status":"ok"},{"name":"object_storage","status":"ok"},{"name":"redis","status":"ok"},{"name":"scanner","status":"ok"},{"name":"worker_queue","status":"ok"},{"name":"worker","status":"ok"},{"name":"mail","status":"ok"}]}
```

如果 `worker` 不是 `ok`，检查 worker 容器是否启动、`PASTEBOX_WORKER_ID` 是否
一致、Redis 是否正常。

## 首次登录

用 `PASTEBOX_BOOTSTRAP_ADMIN_EMAIL` 和
`PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD` 登录。

确认能登录后，建议轮换 bootstrap 密码，或者从 `deploy/production.env` 中移除
bootstrap 变量，避免以后重启时继续重置管理员密码。

## Provider smoke test

上线前至少做这些实测：

1. 注册验证码邮件能收到。
2. 邮箱验证邮件能收到。
3. 密码重置邮件能收到。
4. Google OAuth 能登录，错误 state 会失败。
5. GitHub OAuth 如果启用，也做同样测试。
6. Stripe 能创建生产 checkout，签名 webhook 能把订单置为 paid。
7. Epusdt 能创建生产支付链接，签名回调能处理成功、取消、过期。
8. 上传一个文件，ClamAV 扫描后状态变为 clean。
9. 创建分享链接，匿名打开和下载都正常。
10. 管理后台能查看用户、附件、分享、订单、队列和 webhook 事件。

详细证据记录参考：

- `docs/production-provider-smoke-tests.md`
- `docs/production-launch-evidence-checklist.md`
- `docs/production-release-notes-template.md`

## 备份和恢复演练

逻辑备份：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm postgres-backup
```

PITR base backup：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm postgres-basebackup
```

WAL 新鲜度检查：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm postgres-wal-check
```

逻辑恢复演练：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm postgres-restore-drill
```

PITR 恢复演练：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm postgres-pitr-drill
```

推送 off-host restic 备份：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm backup-push
```

上线前不要只证明“备份命令能跑”，还要证明“能恢复”。

## 日常运维

查看状态：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  ps
```

查看日志：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  logs --tail=200 api worker
```

进入数据库：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  exec postgres psql -U pastebox -d pastebox
```

访问 metrics：

```sh
curl -fsS -H "Authorization: Bearer <PASTEBOX_METRICS_TOKEN>" \
  https://pastebox.example.com/metrics
```

不要把 metrics token 放进公开截图、浏览器地址栏或第三方不可信平台。

## 升级

1. 本地合并代码，推送到 GitHub。
2. 等 GitHub Actions 镜像构建通过。
3. 记录新的 `ghcr.io/cvinit/pastebox:sha-<commit>`。
4. 服务器修改 `deploy/production.env` 里的 `PASTEBOX_IMAGE`。
5. 先跑 preflight。
6. 拉镜像、迁移、滚动重启 API 和 worker。

命令：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm preflight

docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance \
  pull api worker migrate

docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm migrate

docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  up -d api worker
```

升级后跑：

```sh
curl -fsS https://pastebox.example.com/readyz
curl -fsS https://pastebox.example.com/api/v1/ready
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  logs --tail=100 api worker
```

## 回滚

回滚前确认上一个镜像 tag/digest。然后：

```sh
grep '^PASTEBOX_IMAGE=' deploy/production.env
```

把 `PASTEBOX_IMAGE` 改回上一个版本，再跑：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm preflight

docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  pull api worker

docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  up -d api worker
```

如果新版本已经执行了不可逆数据库迁移，不要盲目回滚镜像，按
`docs/production-rollback-runbook.md` 做恢复判断。

## 常见问题

### 登录后马上掉线

检查：

- Cloudflare SSL 模式必须是 Full (strict)，不要 Flexible。
- Nginx 必须传 `X-Forwarded-Proto https`。
- `PASTEBOX_PUBLIC_URL` 必须是 `https://pastebox.example.com`。
- `PASTEBOX_CORS_ALLOWED_ORIGINS` 必须包含完全一致的 origin。

### 上传失败或大文件 413

检查：

- Nginx `client_max_body_size`。
- Cloudflare 套餐上传上限。
- PasteBox 套餐单文件限制。
- API 日志里的 quota 或 upload limit 错误。

### readyz object_storage 不通过

检查：

- `PASTEBOX_S3_ENDPOINT` 是否为 HTTPS。
- bucket 是否存在。
- access key 是否有读写权限。
- 如果是 R2，region/path style 是否符合服务商要求。

### readyz mail 不通过

检查：

- SMTP host、port、TLS 模式。
- 账号密码。
- 发件域名 SPF/DKIM/DMARC。
- 云服务器安全组是否允许访问 SMTP 端口。

### OAuth 回调失败

检查 OAuth 应用中的 callback URL 是否完全一致：

```text
https://pastebox.example.com/api/v1/auth/google/callback
https://pastebox.example.com/api/v1/auth/github/callback
```

### Stripe 或 Epusdt 回调失败

检查：

- 回调地址是否是 HTTPS 公网地址。
- Stripe `Stripe-Signature` 是否到达后端。
- Epusdt 签名密钥是否与 `PASTEBOX_EPUSDT_SECRET_KEY` 一致。
- Cloudflare/WAF 是否拦截 webhook。

### Cloudflare 显示 502/522

检查：

```sh
sudo nginx -t
sudo systemctl status nginx
curl -v http://127.0.0.1:18080/readyz
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  ps
```

如果本地 `127.0.0.1:18080` 不通，先修 Docker；如果本地通但公网不通，查 Nginx、
防火墙和 Cloudflare。

## 上线前检查清单

- [ ] `PASTEBOX_IMAGE` 是不可变 `sha-*` tag 或 digest。
- [ ] `deploy/production.env` 权限是 `600`，没有提交到 git。
- [ ] `PASTEBOX_PUBLIC_URL`、`PASTEBOX_DOMAIN`、CORS、OAuth callback 都是同一个生产域名。
- [ ] Cloudflare 是 Full (strict)，没有 Flexible。
- [ ] Nginx 只反代到 `127.0.0.1:18080`，容器端口没有直接暴露公网。
- [ ] `/readyz` 和 `/api/v1/ready` 返回 production ready。
- [ ] 注册、登录、邮箱验证、密码重置、OAuth、上传、分享、下载都通过浏览器实测。
- [ ] Stripe/Epusdt webhook 通过生产 provider smoke test。
- [ ] PostgreSQL 备份、逻辑恢复、PITR 恢复演练都通过。
- [ ] off-host restic 备份成功。
- [ ] 监控、告警、支持邮箱、滥用邮箱都能用。
- [ ] 完成 `docs/production-launch-evidence-checklist.md`。
