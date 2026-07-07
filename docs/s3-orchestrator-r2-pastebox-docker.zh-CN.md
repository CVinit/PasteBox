# s3-orchestrator 聚合 Cloudflare R2 并对接 PasteBox Docker 部署教程

本文适用于这个部署形态：

```text
用户浏览器
  -> Cloudflare CDN / WAF
  -> 宿主机 Nginx
  -> PasteBox api 容器
  -> s3-orchestrator HTTPS endpoint
  -> 多个 Cloudflare R2 bucket
```

PasteBox 域名走 Cloudflare CDN，宿主机 Nginx 反代 PasteBox 容器。s3-orchestrator 也用 Docker 部署，但建议对象存储域名不要走 Cloudflare CDN，而是 DNS only 直连源站 Nginx，再由 Nginx 反代到 s3-orchestrator 容器。

## 域名规划

下面用示例域名说明，部署时换成你的真实域名：

```text
pastebox.example.com  -> Cloudflare Proxied -> Nginx -> 127.0.0.1:18080 -> PasteBox api
s3o.example.com       -> DNS only           -> Nginx -> 127.0.0.1:19000 -> s3-orchestrator
```

`pastebox.example.com` 可以走 Cloudflare CDN，因为它面对浏览器用户。

`s3o.example.com` 建议 DNS only，因为它是 S3 API endpoint。Cloudflare CDN、WAF、缓存和上传大小限制容易干扰 S3 签名、上传和长连接。PasteBox 会在服务端访问 `https://s3o.example.com`，不需要用户浏览器直接访问它。

## 前置条件

- 一台 Linux VPS，已安装 Docker Engine、Docker Compose plugin、Nginx、Git。
- PasteBox 已按生产部署方式放在 `/opt/pastebox`。
- Cloudflare 托管你的域名。
- 已有或准备创建多个 Cloudflare R2 bucket。
- PasteBox 镜像使用固定 tag 或 digest，例如 `ghcr.io/cvinit/pastebox:sha-<commit>`，不要用 `latest`。

## Cloudflare DNS 和 SSL

### DNS

在 Cloudflare DNS 里配置：

```text
Type: A
Name: pastebox
Content: <server-ipv4>
Proxy status: Proxied

Type: A
Name: s3o
Content: <server-ipv4>
Proxy status: DNS only
```

如果服务器有 IPv6，也可以加对应 AAAA。

### PasteBox 域名

Cloudflare SSL/TLS 推荐：

```text
SSL/TLS encryption mode: Full (strict)
Always Use HTTPS: On
Automatic HTTPS Rewrites: On
Minimum TLS Version: TLS 1.2 或更高
```

不要用 `Flexible`。Flexible 会让 Cloudflare 到源站走 HTTP，容易导致 cookie、重定向、OAuth 回调和安全判断出问题。

PasteBox 源站证书可以用 Cloudflare Origin Certificate。这个证书只需要覆盖：

```text
pastebox.example.com
```

### s3-orchestrator 域名

`s3o.example.com` 推荐 DNS only，并使用 Let's Encrypt 或其他公开可信证书：

```sh
sudo certbot --nginx -d s3o.example.com
```

如果你不想让 `s3o.example.com` 对公网开放，可以用 DNS challenge 申请证书，然后在 Nginx 里按 Docker 网段和管理员 IP 做访问限制。

## Cloudflare R2 准备

创建多个 R2 bucket，例如：

```text
pastebox-r2-a
pastebox-r2-b
pastebox-r2-c
```

每个 R2 bucket 建议单独创建 S3 API Token，只给对应 bucket 的对象读写权限。R2 的 S3 endpoint 通常是：

```text
https://<account_id>.r2.cloudflarestorage.com
```

R2 给 s3-orchestrator 当后端时，region 使用：

```text
auto
```

s3-orchestrator 对 PasteBox 暴露虚拟 bucket 时，PasteBox 侧 region 使用：

```text
us-east-1
```

## 部署目录

以下命令默认在服务器 `/opt/pastebox` 执行：

```sh
cd /opt/pastebox
mkdir -p deploy/s3-orchestrator/data
mkdir -p vendor
```

s3-orchestrator 官方发布包有二进制和 deb 包，但这里要求容器部署。为避免依赖不确定的公共镜像，推荐在服务器上固定 tag 拉源码并本地构建 Docker 镜像：

```sh
git clone --branch v0.62.28 --depth 1 https://github.com/afreidah/s3-orchestrator.git vendor/s3-orchestrator
```

s3-orchestrator 容器默认用户是 `10001`，要让它能写 SQLite 元数据：

```sh
sudo chown -R 10001:10001 deploy/s3-orchestrator/data
```

## 创建 Compose 覆盖文件

创建 `/opt/pastebox/compose.nginx-s3o.yaml`：

```yaml
services:
  api:
    ports:
      - "127.0.0.1:${PASTEBOX_HOST_HTTP_PORT:-18080}:8080"
    extra_hosts:
      - "${S3O_DOMAIN:?set S3O_DOMAIN}:host-gateway"
    depends_on:
      s3-orchestrator:
        condition: service_started

  worker:
    extra_hosts:
      - "${S3O_DOMAIN:?set S3O_DOMAIN}:host-gateway"
    depends_on:
      s3-orchestrator:
        condition: service_started

  preflight:
    extra_hosts:
      - "${S3O_DOMAIN:?set S3O_DOMAIN}:host-gateway"

  s3-orchestrator:
    build:
      context: ./vendor/s3-orchestrator
      args:
        VERSION: v0.62.28
    image: local/s3-orchestrator:v0.62.28
    restart: unless-stopped
    command: ["-config", "/etc/s3-orchestrator/config.yaml"]
    env_file:
      - ./deploy/s3-orchestrator.env
    ports:
      - "127.0.0.1:${S3O_HOST_HTTP_PORT:-19000}:9000"
    volumes:
      - ./deploy/s3-orchestrator/config.yaml:/etc/s3-orchestrator/config.yaml:ro
      - ./deploy/s3-orchestrator/data:/var/lib/s3-orchestrator
    logging:
      driver: json-file
      options:
        max-size: "20m"
        max-file: "5"
```

这里有两个关键点：

- PasteBox api、worker、preflight 通过 `extra_hosts` 把 `s3o.example.com` 指到宿主机 Docker gateway，然后走宿主机 Nginx 的 HTTPS。这样既满足 PasteBox 生产 preflight 的真实 HTTPS 域名要求，也避免容器内 DNS 回环不稳定。
- s3-orchestrator 容器只绑定宿主机 `127.0.0.1:19000`，公网不能直接访问容器端口，只能通过 Nginx。

后续所有生产命令都带两个 Compose 文件：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-s3o.yaml \
  <command>
```

不要启动 `caddy`，因为这里使用宿主机 Nginx。

## 配置 s3-orchestrator

创建 `/opt/pastebox/deploy/s3-orchestrator/config.yaml`：

```yaml
server:
  listen_addr: ":9000"
  max_object_size: 5368709120

database:
  driver: sqlite
  path: /var/lib/s3-orchestrator/data.db

buckets:
  - name: pastebox-files
    credentials:
      - access_key_id: ${BUCKET_ACCESS_KEY}
        secret_access_key: ${BUCKET_SECRET_KEY}

backends:
  - name: ${BACKEND1_NAME}
    endpoint: ${BACKEND1_ENDPOINT}
    region: ${BACKEND1_REGION}
    bucket: ${BACKEND1_BUCKET}
    access_key_id: ${BACKEND1_ACCESS_KEY}
    secret_access_key: ${BACKEND1_SECRET_KEY}
    force_path_style: true
    quota_bytes: 9663676416

  - name: ${BACKEND2_NAME}
    endpoint: ${BACKEND2_ENDPOINT}
    region: ${BACKEND2_REGION}
    bucket: ${BACKEND2_BUCKET}
    access_key_id: ${BACKEND2_ACCESS_KEY}
    secret_access_key: ${BACKEND2_SECRET_KEY}
    force_path_style: true
    quota_bytes: 9663676416

  - name: ${BACKEND3_NAME}
    endpoint: ${BACKEND3_ENDPOINT}
    region: ${BACKEND3_REGION}
    bucket: ${BACKEND3_BUCKET}
    access_key_id: ${BACKEND3_ACCESS_KEY}
    secret_access_key: ${BACKEND3_SECRET_KEY}
    force_path_style: true
    quota_bytes: 9663676416

routing_strategy: "spread"

replication:
  factor: 1
  worker_interval: "5m"
  batch_size: 50

backend_circuit_breaker:
  enabled: true
  failure_threshold: 5
  open_timeout: "5m"

telemetry:
  metrics:
    enabled: true
    path: /metrics
```

`replication.factor=1` 表示聚合容量优先，一个对象只放到一个 R2 后端。想要多副本容灾可以改成 `2`，但容量和写请求都会翻倍。

`quota_bytes` 建议略低于 R2 免费层 10GB，例如上面的 `9663676416` 约等于 9GiB，留一点空间给误差和元数据。

创建 `/opt/pastebox/deploy/s3-orchestrator.env`：

```sh
BACKEND1_NAME=r2-a
BACKEND1_ENDPOINT=https://<account_id_1>.r2.cloudflarestorage.com
BACKEND1_REGION=auto
BACKEND1_BUCKET=pastebox-r2-a
BACKEND1_ACCESS_KEY=<r2-key-a>
BACKEND1_SECRET_KEY=<r2-secret-a>

BACKEND2_NAME=r2-b
BACKEND2_ENDPOINT=https://<account_id_2>.r2.cloudflarestorage.com
BACKEND2_REGION=auto
BACKEND2_BUCKET=pastebox-r2-b
BACKEND2_ACCESS_KEY=<r2-key-b>
BACKEND2_SECRET_KEY=<r2-secret-b>

BACKEND3_NAME=r2-c
BACKEND3_ENDPOINT=https://<account_id_3>.r2.cloudflarestorage.com
BACKEND3_REGION=auto
BACKEND3_BUCKET=pastebox-r2-c
BACKEND3_ACCESS_KEY=<r2-key-c>
BACKEND3_SECRET_KEY=<r2-secret-c>

BUCKET_ACCESS_KEY=<pastebox-virtual-access-key>
BUCKET_SECRET_KEY=<pastebox-virtual-secret-key>
```

设置权限：

```sh
chmod 600 deploy/s3-orchestrator.env
```

不要把 `deploy/s3-orchestrator.env` 提交到 Git。

生成虚拟 bucket 凭据可以用：

```sh
openssl rand -hex 20
openssl rand -base64 40
```

## 配置 PasteBox 环境变量

编辑 `/opt/pastebox/deploy/production.env`，对象存储相关项改成：

```sh
PASTEBOX_HOST_HTTP_PORT=18080

S3O_DOMAIN=s3o.example.com
S3O_HOST_HTTP_PORT=19000

PASTEBOX_S3_ENDPOINT=https://s3o.example.com
PASTEBOX_S3_BUCKET=pastebox-files
PASTEBOX_S3_REGION=us-east-1
PASTEBOX_S3_ACCESS_KEY=<pastebox-virtual-access-key>
PASTEBOX_S3_SECRET_KEY=<pastebox-virtual-secret-key>
PASTEBOX_S3_USE_PATH_STYLE=true
```

注意：

- `PASTEBOX_S3_BUCKET` 是 s3-orchestrator 的虚拟 bucket 名，不是 R2 bucket 名。
- `PASTEBOX_S3_ACCESS_KEY` 和 `PASTEBOX_S3_SECRET_KEY` 是 s3-orchestrator 的虚拟 bucket 凭据，不是 R2 后端凭据。
- 生产 preflight 会拒绝 HTTP、本地地址和占位符，所以 `PASTEBOX_S3_ENDPOINT` 必须是类似 `https://s3o.example.com` 的真实 HTTPS 域名。
- 备份用的 `PASTEBOX_BACKUP_S3_ACCESS_KEY`、`PASTEBOX_BACKUP_S3_SECRET_KEY` 不能复用这里的对象存储凭据。

## 配置 Nginx

### Cloudflare 真实 IP

PasteBox 域名走 Cloudflare Proxied，建议让 Nginx 识别真实访问 IP：

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

Cloudflare IP 段可能变化，建议定期刷新。

### PasteBox 和 s3-orchestrator 站点

创建或更新 `/etc/nginx/sites-available/pastebox.conf`。下面示例同时放了 PasteBox 和 s3-orchestrator：

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}

upstream pastebox_api {
    server 127.0.0.1:18080;
    keepalive 32;
}

upstream s3_orchestrator {
    server 127.0.0.1:19000;
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

server {
    listen 80;
    listen [::]:80;
    server_name s3o.example.com;

    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name s3o.example.com;

    ssl_certificate /etc/letsencrypt/live/s3o.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/s3o.example.com/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;

    client_max_body_size 5g;

    proxy_connect_timeout 30s;
    proxy_send_timeout 600s;
    proxy_read_timeout 600s;
    proxy_request_buffering off;
    proxy_buffering off;

    location / {
        proxy_pass http://s3_orchestrator;
        proxy_http_version 1.1;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header Connection "";
    }
}
```

启用配置：

```sh
sudo ln -sf /etc/nginx/sites-available/pastebox.conf /etc/nginx/sites-enabled/pastebox.conf
sudo nginx -t
sudo systemctl reload nginx
```

如果要收紧 `s3o.example.com` 的公网访问，可以在 `s3o.example.com` 的 `location /` 里加 allowlist。先确认 Docker 网段后再加，例如：

```nginx
allow 172.16.0.0/12;
allow 10.0.0.0/8;
allow 192.168.0.0/16;
allow <your-admin-ip>/32;
deny all;
```

如果启用 allowlist，外部 AWS CLI 测试也必须来自允许的管理员 IP。

## Cloudflare 缓存和上传限制

不要对 PasteBox 开全站 `Cache Everything`。

推荐 Cloudflare Cache Rules：

```text
Bypass cache:
  /api/*
  /app*
  /s/*
  /login
  /register
  /password-reset*
  /admin*

Cache static assets:
  /assets/*
  /favicon.svg
  /manifest.webmanifest
```

PasteBox 域名走 Cloudflare Proxied 时，上传大小受 Cloudflare 套餐限制。免费和 Pro 套餐常见单次请求上限是 100MB。Nginx 的 `client_max_body_size` 设得更大也绕不过 Cloudflare 边缘限制。

如果要支持更大的文件上传，有三个方向：

- 升级 Cloudflare 套餐。
- 上传域名改 DNS only。
- 后续改造为浏览器直传对象存储。

当前本文按服务端代理上传下载处理。

## 启动顺序

先检查 Compose 渲染：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-s3o.yaml \
  config
```

构建 s3-orchestrator 镜像：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-s3o.yaml \
  build s3-orchestrator
```

启动基础服务和 s3-orchestrator：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-s3o.yaml \
  up -d postgres redis clamav s3-orchestrator
```

确认 Nginx 配置通过：

```sh
sudo nginx -t
sudo systemctl reload nginx
```

检查 s3-orchestrator：

```sh
curl -fsS http://127.0.0.1:19000/health/ready
curl -fsS https://s3o.example.com/health/ready
```

跑 PasteBox 生产 preflight：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-s3o.yaml \
  --profile maintenance run --rm preflight
```

执行数据库迁移：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-s3o.yaml \
  --profile maintenance run --rm migrate
```

启动 PasteBox：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-s3o.yaml \
  up -d api worker
```

确认没有启动 `caddy`：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-s3o.yaml \
  ps
```

## 验证 s3-orchestrator S3 API

安装 AWS CLI 后，用虚拟 bucket 凭据测试。这里测试的是 s3-orchestrator 暴露给 PasteBox 的虚拟 bucket，不是 R2 后端 bucket。

```sh
export AWS_ACCESS_KEY_ID=<pastebox-virtual-access-key>
export AWS_SECRET_ACCESS_KEY=<pastebox-virtual-secret-key>
export AWS_DEFAULT_REGION=us-east-1
export S3O_ENDPOINT=https://s3o.example.com
export S3O_BUCKET=pastebox-files

aws --endpoint-url "$S3O_ENDPOINT" \
  s3api head-bucket --bucket "$S3O_BUCKET" --region us-east-1
```

上传、下载、删除一个测试对象：

```sh
printf 'pastebox s3 orchestrator smoke\n' > /tmp/pastebox-s3o-smoke.txt

aws --endpoint-url "$S3O_ENDPOINT" \
  s3api put-object \
  --bucket "$S3O_BUCKET" \
  --key smoke/pastebox-s3o-smoke.txt \
  --body /tmp/pastebox-s3o-smoke.txt \
  --content-type text/plain \
  --region us-east-1

aws --endpoint-url "$S3O_ENDPOINT" \
  s3api get-object \
  --bucket "$S3O_BUCKET" \
  --key smoke/pastebox-s3o-smoke.txt \
  /tmp/pastebox-s3o-smoke.out \
  --region us-east-1

diff -u /tmp/pastebox-s3o-smoke.txt /tmp/pastebox-s3o-smoke.out

aws --endpoint-url "$S3O_ENDPOINT" \
  s3api delete-object \
  --bucket "$S3O_BUCKET" \
  --key smoke/pastebox-s3o-smoke.txt \
  --region us-east-1
```

如果这里失败，先不要启动 PasteBox 业务流量。优先修 s3-orchestrator、R2 凭据、Nginx 和域名。

## 验证 PasteBox

宿主机本地检查：

```sh
curl -fsS http://127.0.0.1:18080/healthz
curl -fsS http://127.0.0.1:18080/readyz
curl -fsS http://127.0.0.1:18080/api/v1/health
```

公网检查：

```sh
curl -fsS https://pastebox.example.com/healthz
curl -fsS https://pastebox.example.com/readyz
curl -fsS https://pastebox.example.com/api/v1/ready
```

`readyz` 或 `/api/v1/ready` 里 object storage 通过，说明 PasteBox 的 `HeadBucket` 已经打通到 s3-orchestrator。

最后做真实业务 smoke：

1. 注册或登录一个测试用户。
2. 创建 paste。
3. 上传一个小附件。
4. 下载附件，确认内容一致。
5. 创建分享链接，通过分享下载附件。
6. 删除 paste 或附件。
7. 跑一次 worker cleanup。
8. 确认旧下载链接不可用。
9. 到 Cloudflare R2 dashboard 里确认对象落到了某个后端 bucket。

## 日志排查

查看 s3-orchestrator 日志：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-s3o.yaml \
  logs -f s3-orchestrator
```

查看 PasteBox API 日志：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-s3o.yaml \
  logs -f api
```

查看 Nginx 日志：

```sh
sudo tail -f /var/log/nginx/access.log /var/log/nginx/error.log
```

正常情况下，s3-orchestrator 日志里应该能看到 `HeadBucket`、`PutObject`、`GetObject`、`DeleteObject` 这类请求。

## 常见问题

### PasteBox preflight 报对象存储 endpoint 不合法

检查：

- `PASTEBOX_S3_ENDPOINT` 必须是 `https://s3o.example.com` 这种真实 HTTPS 域名。
- 不要写 `http://s3-orchestrator:9000`。
- 不要写 `http://127.0.0.1:19000`。
- 不要留 `CHANGE_ME` 或 `example.com`。

### readyz object storage 不通过

检查：

- `PASTEBOX_S3_BUCKET` 是否等于 s3-orchestrator 的虚拟 bucket：`pastebox-files`。
- `PASTEBOX_S3_ACCESS_KEY` / `PASTEBOX_S3_SECRET_KEY` 是否等于虚拟 bucket 凭据。
- `PASTEBOX_S3_USE_PATH_STYLE=true`。
- `s3o.example.com` 在 PasteBox 容器内是否解析到宿主机 gateway。
- Nginx 是否把 `Host` 原样传给 s3-orchestrator。

容器内可快速检查解析和访问：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-s3o.yaml \
  run --rm preflight sh -c 'getent hosts "$S3O_DOMAIN" && wget -qO- "https://$S3O_DOMAIN/health/ready"'
```

### S3 API 报 SignatureDoesNotMatch

常见原因：

- Nginx 没有传 `Host $host`。
- `PASTEBOX_S3_USE_PATH_STYLE` 没有设成 `true`。
- 访问路径被 Nginx 改写了。
- 客户端和服务器时间差太大。
- 用了 R2 后端凭据去访问 s3-orchestrator 虚拟 bucket。

### 上传失败或 413

分两段看：

- 用户到 PasteBox：受 Cloudflare 套餐上传上限、Nginx `client_max_body_size`、PasteBox 套餐限制影响。
- PasteBox 到 s3-orchestrator：受 `s3o.example.com` Nginx `client_max_body_size`、s3-orchestrator `max_object_size`、R2 限制影响。

如果用户上传大文件经过 Cloudflare Proxied 域名，Cloudflare 可能先拒绝，Nginx 和 PasteBox 都还没收到请求。

### s3-orchestrator 能启动但 R2 后端写入失败

检查：

- R2 endpoint 是否为 `https://<account_id>.r2.cloudflarestorage.com`。
- R2 region 是否为 `auto`。
- R2 token 是否有对应 bucket 的对象读写权限。
- `BACKEND*_BUCKET` 是否真实存在。
- `force_path_style: true` 是否保留。

### s3-orchestrator 元数据丢失

`deploy/s3-orchestrator/data/data.db` 是 s3-orchestrator 的对象索引。这个文件丢了，就算 R2 bucket 里还有对象，s3-orchestrator 也可能不知道对象在哪个后端。

必须把它纳入备份：

```text
/opt/pastebox/deploy/s3-orchestrator/data/data.db
/opt/pastebox/deploy/s3-orchestrator/config.yaml
/opt/pastebox/deploy/s3-orchestrator.env
```

## 扩容和维护

### 增加一个 R2 后端

1. 在 Cloudflare 创建新 R2 bucket。
2. 创建只访问该 bucket 的 R2 S3 API Token。
3. 在 `deploy/s3-orchestrator.env` 添加 `BACKEND4_*`。
4. 在 `deploy/s3-orchestrator/config.yaml` 添加新的 `backends` 项。
5. 重启 s3-orchestrator：

   ```sh
   docker compose --env-file deploy/production.env \
     -f compose.production.yaml \
     -f compose.nginx-s3o.yaml \
     up -d --build s3-orchestrator
   ```

6. 用 AWS CLI 上传一个测试对象并确认成功。

### 升级 s3-orchestrator

先备份：

```sh
cp -a deploy/s3-orchestrator/data deploy/s3-orchestrator/data.backup.$(date +%Y%m%d%H%M%S)
```

再更新源码 tag：

```sh
cd /opt/pastebox/vendor/s3-orchestrator
git fetch --tags
git checkout <new-version-tag>
cd /opt/pastebox
```

更新 `compose.nginx-s3o.yaml` 里的 `VERSION` 和 `image` tag，然后重建：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-s3o.yaml \
  up -d --build s3-orchestrator
```

升级后重新跑 S3 API smoke 和 PasteBox readiness。

## 上线前检查清单

- [ ] `pastebox.example.com` 在 Cloudflare 是 Proxied。
- [ ] `s3o.example.com` 是 DNS only，或明确知道 Cloudflare 代理对 S3 API 的影响。
- [ ] Cloudflare SSL/TLS 是 Full strict，不是 Flexible。
- [ ] Nginx `pastebox.example.com` 反代到 `127.0.0.1:18080`。
- [ ] Nginx `s3o.example.com` 反代到 `127.0.0.1:19000`。
- [ ] `deploy/s3-orchestrator.env` 权限是 `600`，没有提交到 Git。
- [ ] R2 后端凭据和 PasteBox 虚拟 bucket 凭据不是同一组。
- [ ] `PASTEBOX_S3_ENDPOINT=https://s3o.example.com`。
- [ ] `PASTEBOX_S3_BUCKET=pastebox-files`。
- [ ] `PASTEBOX_S3_USE_PATH_STYLE=true`。
- [ ] `preflight` 通过。
- [ ] `/readyz` 和 `/api/v1/ready` 通过。
- [ ] AWS CLI 对 s3-orchestrator 的 put/get/delete 通过。
- [ ] PasteBox UI 上传、下载、分享下载、删除通过。
- [ ] `deploy/s3-orchestrator/data/data.db` 已纳入备份。
