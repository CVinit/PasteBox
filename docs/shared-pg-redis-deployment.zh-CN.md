# PasteBox 共享 PostgreSQL/Redis 独立部署教程（宿主机 Nginx + s3-orchestrator）

本教程面向一台全新服务器，手把手部署以下架构：

- **PostgreSQL 和 Redis 各自独立部署**为单独的 Docker Compose project，供本机
  其他项目复用，生命周期与 PasteBox 完全解耦。
- **PasteBox 分别接入两个共享容器网络**实现数据库和缓存的内部互通；ClamAV 等
  不属于公共业务的容器仍随 PasteBox 项目一起启动。
- 边缘层是**宿主机 Nginx 反向代理**，证书用 certbot（Let's Encrypt）签发，
  不依赖 Cloudflare。
- 对象存储对接**已独立部署完成的 s3-orchestrator**（下文简称 s3o）；如果你
  还没部署 s3o，先看
  [s3-orchestrator 聚合 R2 对接 PasteBox 教程](s3-orchestrator-r2-pastebox-docker.zh-CN.md)。

如果你要的是单 project 的共享模式（PG + Redis 同一个 Compose project），或
PasteBox 自带数据库的一体化模式，见
[Docker + Nginx + Cloudflare 生产部署教程](production-docker-nginx-cloudflare.zh-CN.md)。

## 最终会部署出什么

同一台宿主机上运行四个彼此独立的 Docker Compose project：

```text
/opt/shared-postgres/     # 共享 PostgreSQL（shared-postgres project）
  ├── compose.yaml
  ├── .env
  └── pg_hba.conf

/opt/shared-redis/        # 共享 Redis（shared-redis project）
  ├── compose.yaml
  └── .env

/opt/pastebox/            # PasteBox 应用（pastebox project）
  ├── compose.production.yaml
  ├── compose.external-split-services.yaml
  ├── compose.nginx-host.yaml
  └── deploy/...

/opt/s3-orchestrator/     # 已部署完成，本教程不涉及
```

职责边界：

- `shared-postgres` 和 `shared-redis` 独立升级、重启、备份，互不影响；任何一个
  停止不会牵连另一个。
- PasteBox project 只管理 `api`、`worker`、`clamav` 等应用容器。
- 宿主机只对外开放 `80`、`443` 和 SSH 端口。

容器与网络布局：

| 服务 | 所属 project | 容器端口 | 宿主机监听 | 加入的网络 |
| --- | --- | ---: | --- | --- |
| PostgreSQL | shared-postgres | 5432 | `127.0.0.1:5432` | `shared-postgres-net` |
| Redis | shared-redis | 6379 | `127.0.0.1:6379` | `shared-redis-net` |
| PasteBox api/worker | pastebox | 8080 | `127.0.0.1:18080` | 项目默认网 + 上面两个 |
| ClamAV | pastebox | 3310 | 不发布 | 项目默认网 |
| s3-orchestrator | s3-orchestrator | 9000 | `127.0.0.1:19000` | s3o 自己的网络 |

PasteBox 容器在共享网络里通过服务别名访问数据库：

```text
PASTEBOX_DATABASE_URL = postgres://pastebox@shared-postgres:5432/pastebox?sslmode=disable
PASTEBOX_REDIS_ADDR   = shared-redis:6379
```

两个共享网络只存在于 Docker 内部，不发布到公网；宿主机程序如需直连，用
`127.0.0.1:5432` / `127.0.0.1:6379`。

### 请求链路

```text
浏览器
  -> https://pastebox.example.com
  -> 宿主机 Nginx (443)
  -> 127.0.0.1:18080 -> PasteBox api 容器
      |- shared-postgres-net -> PostgreSQL（读写业务数据）
      |- shared-redis-net    -> Redis（可用性检查）
      -> https://s3o.example.com
         -> 宿主机 Nginx -> s3-orchestrator 容器 -> R2 backend
```

PasteBox 容器通过 `extra_hosts` 把 `s3o.example.com` 解析到 Docker host
gateway，回环经过宿主机 Nginx 访问 s3o，证书校验和 SNI 都能正常工作。

## 前置条件

- 一台 Ubuntu 22.04/24.04 或 Debian 12 VPS，建议 2 核 CPU、4GB 内存、50GB 系统盘
  （ClamAV 初始化占内存较多）。
- 一个域名，两条 DNS 记录直接解析到服务器公网 IP（不走 Cloudflare 代理）：
  - `pastebox.example.com  -> A 记录 -> 服务器 IP`
  - `s3o.example.com       -> A 记录 -> 服务器 IP`（s3o 已部署时应已存在）
- 已部署完成的 s3-orchestrator，并且已经创建好给 PasteBox 用的虚拟 bucket
  （endpoint、bucket、access key、secret key 备用）。
- sudo 权限和备用 SSH 登录方式。

下面用 `pastebox.example.com` 和 `s3o.example.com` 作示例，部署时替换为真实域名。

## 第 1 步：服务器准备

安装基础软件：

```sh
sudo apt update
sudo apt install -y ca-certificates curl git nginx certbot
```

安装 Docker Engine 和 Compose plugin（官方源）：

```sh
curl -fsSL https://get.docker.com | sudo sh
sudo systemctl enable --now docker
docker version
docker compose version
```

预期两个命令都输出版本号。

开放防火墙（保留 SSH）：

```sh
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
sudo ufw status
```

检查端口没有被占用（5432、6379、18080 都应为空）：

```sh
sudo ss -ltnp | grep -E ':5432|:6379|:18080' || echo "ports free"
```

创建目录：

```sh
sudo mkdir -p /opt/shared-postgres /opt/shared-redis /opt/pastebox
sudo chown -R "$USER" /opt/shared-postgres /opt/shared-redis /opt/pastebox
```

## 第 2 步：取得仓库模板文件

PasteBox 不在服务器上构建镜像，但需要仓库里的 Compose 文件和部署脚本。在本地
机器或服务器上克隆仓库，把模板复制过去：

```sh
git clone https://github.com/CVinit/PasteBox.git /tmp/pastebox-repo
```

PasteBox 侧文件复制到 `/opt/pastebox`（注意保留 `deploy/` 子目录结构，备份脚本
和 pg_hba.conf 都在里面）：

```sh
cd /tmp/pastebox-repo
cp compose.production.yaml compose.external-split-services.yaml /opt/pastebox/
cp -r deploy /opt/pastebox/
```

共享 PostgreSQL 模板复制到 `/opt/shared-postgres`：

```sh
cp compose.shared-postgres.yaml /opt/shared-postgres/compose.yaml
cp deploy/shared-postgres.env.example /opt/shared-postgres/.env
cp deploy/postgres/pg_hba.conf /opt/shared-postgres/pg_hba.conf
```

共享 Redis 模板复制到 `/opt/shared-redis`：

```sh
cp compose.shared-redis.yaml /opt/shared-redis/compose.yaml
cp deploy/shared-redis.env.example /opt/shared-redis/.env
```

最终三个目录的文件：

```text
/opt/shared-postgres: compose.yaml  .env  pg_hba.conf
/opt/shared-redis:    compose.yaml  .env
/opt/pastebox:        compose.production.yaml
                      compose.external-split-services.yaml
                      deploy/（含 pastebox-deploy.sh、备份脚本等）
```

## 第 3 步：部署共享 PostgreSQL

编辑 `/opt/shared-postgres/.env`：

```sh
cd /opt/shared-postgres
chmod 600 .env
```

生成超级管理员密码并替换：

```sh
openssl rand -base64 24
```

需要修改的项：

```sh
SHARED_POSTGRES_PASSWORD=<上面生成的密码>
```

其余保持默认即可，含义如下（遇到冲突再改）：

- `SHARED_POSTGRES_HOST_PORT=127.0.0.1:5432`：宿主机监听地址，只绑回环。
- `SHARED_POSTGRES_NETWORK=shared-postgres-net`：Docker 网络名，其他项目接入时
  用这个名字。
- `SHARED_POSTGRES_VOLUME=shared-postgres-data`：数据卷名，固定不动。
- `SHARED_BACKUP_VOLUME=shared-postgres-backups`：备份卷名，WAL 归档和 base
  backup 都写这里。

创建备份卷并启动：

```sh
docker volume create shared-postgres-backups
docker compose up -d
docker compose ps
```

预期 `postgres` 状态为 `Up (healthy)`：

```text
NAME                IMAGE               STATUS            PORTS
shared-postgres-1   postgres:17-alpine  Up 30 seconds     127.0.0.1:5432->5432/tcp
```

验证网络和数据库：

```sh
docker network ls | grep shared-postgres-net
docker compose exec postgres pg_isready -U postgres -d postgres
```

预期输出 `accepting connections`。

模板默认开启了 WAL 归档（`wal_level=replica`、`archive_mode=on`），归档写入
`shared-postgres-backups` 卷，为后面的 PITR 备份做准备。

## 第 4 步：部署共享 Redis

编辑 `/opt/shared-redis/.env` 并收紧权限：

```sh
cd /opt/shared-redis
chmod 600 .env
```

默认值通常不用改：

- `SHARED_REDIS_HOST_PORT=127.0.0.1:6379`：宿主机监听，只绑回环。
- `SHARED_REDIS_NETWORK=shared-redis-net`：Docker 网络名。
- `SHARED_REDIS_VOLUME=shared-redis-data`：数据卷名。

启动并验证：

```sh
docker compose up -d
docker compose ps
docker network ls | grep shared-redis-net
docker compose exec redis redis-cli ping
```

预期 `redis` 为 `Up (healthy)`，`redis-cli ping` 返回 `PONG`。

Redis 持久化策略为 AOF + 每分钟 RDB（`--appendonly yes --save 60 1`），当前
PasteBox 只把它用于可用性检查，不承载核心业务数据。

## 第 5 步：配置 PasteBox 环境

进入 PasteBox 目录，创建生产环境文件：

```sh
cd /opt/pastebox
cp deploy/production.split.env.example deploy/production.env
chmod 600 deploy/production.env
```

生成两把密钥：

```sh
openssl rand -base64 32   # PASTEBOX_CONFIG_ENCRYPTION_KEY
openssl rand -hex 32      # PASTEBOX_METRICS_TOKEN
```

编辑 `deploy/production.env`，至少替换这些项：

```sh
PASTEBOX_IMAGE=ghcr.io/cvinit/pastebox:sha-<commit>
PASTEBOX_DOMAIN=pastebox.example.com
PASTEBOX_ADMIN_EMAIL=admin@example.com

PASTEBOX_CONFIG_ENCRYPTION_KEY=<base64-32-byte-key>
PASTEBOX_METRICS_TOKEN=<long-random-token>

PASTEBOX_POSTGRES_PASSWORD=<为 pastebox 账号新生成的长随机密码>
PASTEBOX_DATABASE_URL=postgres://pastebox@shared-postgres:5432/pastebox?sslmode=disable
PASTEBOX_REDIS_ADDR=shared-redis:6379

PASTEBOX_RESTIC_REPOSITORY=s3:https://<backup-storage-endpoint>/pastebox-backups
PASTEBOX_RESTIC_PASSWORD=<long-random-restic-password>
PASTEBOX_BACKUP_S3_ACCESS_KEY=<backup-access-key>
PASTEBOX_BACKUP_S3_SECRET_KEY=<backup-secret-key>
```

要点：

- `PASTEBOX_DATABASE_URL` 的主机名必须是 `shared-postgres`（共享网络里的服务
  别名），不是 `127.0.0.1`。URL 里不写密码，部署脚本通过
  `PASTEBOX_POSTGRES_PASSWORD` 注入。
- `PASTEBOX_CONFIG_ENCRYPTION_KEY` 必须单独备份：后台保存的第三方密钥用它做
  AES-256-GCM 加密，数据库恢复时必须使用同一把密钥。
- 备份用 S3 凭据（`PASTEBOX_BACKUP_S3_*`）必须与 s3o 虚拟 bucket 凭据分开，
  不要复用。

部署脚本默认在仓库根目录找共享 compose 文件。本教程把共享服务独立放到
`/opt/shared-postgres` 和 `/opt/shared-redis`，必须把路径覆盖写进
`/opt/pastebox/.env.shared-split`：

```sh
cat > /opt/pastebox/.env.shared-split <<'EOF'
export PASTEBOX_DEPLOY_MODE=shared-split
export PASTEBOX_SHARED_POSTGRES_COMPOSE_FILE=/opt/shared-postgres/compose.yaml
export PASTEBOX_SHARED_POSTGRES_ENV_FILE=/opt/shared-postgres/.env
export PASTEBOX_SHARED_REDIS_COMPOSE_FILE=/opt/shared-redis/compose.yaml
export PASTEBOX_SHARED_REDIS_ENV_FILE=/opt/shared-redis/.env
EOF
chmod 600 /opt/pastebox/.env.shared-split
```

之后每次操作 PasteBox 前先加载：

```sh
. /opt/pastebox/.env.shared-split
```

也可以写进 `~/.profile`。cron 任务同样需要 `source` 这个文件。

再创建 Nginx 覆盖文件 `/opt/pastebox/compose.nginx-host.yaml`（把
`s3o.example.com` 换成真实对象存储域名）：

```yaml
services:
  api:
    ports:
      - "127.0.0.1:18080:8080"
    extra_hosts:
      - "s3o.example.com:host-gateway"

  worker:
    extra_hosts:
      - "s3o.example.com:host-gateway"

  preflight:
    extra_hosts:
      - "s3o.example.com:host-gateway"
```

## 第 6 步：初始化共享数据库并启动 PasteBox

部署脚本检测到共享 PostgreSQL 后，会用超级管理员创建仅供 PasteBox 使用的
`pastebox` role 和 `pastebox` 数据库。加载独立目录覆盖后执行：

```sh
cd /opt/pastebox
. /opt/pastebox/.env.shared-split
./deploy/pastebox-deploy.sh init
```

预期输出：

```text
共享 PostgreSQL、Redis 和 PasteBox 独立数据库已就绪。
```

运行根配置预检：

```sh
./deploy/pastebox-deploy.sh preflight-root
```

预期输出 `production preflight passed (startup roots only)`。

一条命令完成拉取镜像、数据库迁移并启动 ClamAV、API 和 worker：

```sh
./deploy/pastebox-deploy.sh up
./deploy/pastebox-deploy.sh status
```

预期运行的 PasteBox 服务包含 `api`、`worker`、`clamav`。ClamAV 首次下载病毒库
可能需要几分钟：

```sh
./deploy/pastebox-deploy.sh logs clamav
```

后续命令都假设已经执行过 `. /opt/pastebox/.env.shared-split`。新开一个
shell 时再加载一次即可。

## 第 7 步：宿主机 Nginx + certbot 证书

### 申请证书

安装 certbot（第 1 步已装）并创建 webroot：

```sh
sudo mkdir -p /var/www/letsencrypt/.well-known/acme-challenge
```

先创建临时 bootstrap 站点 `/etc/nginx/sites-available/pastebox-bootstrap.conf`：

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name pastebox.example.com;

    location /.well-known/acme-challenge/ {
        root /var/www/letsencrypt;
    }

    location / {
        return 404;
    }
}
```

启用并签发：

```sh
sudo ln -sf /etc/nginx/sites-available/pastebox-bootstrap.conf /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx

sudo certbot certonly \
  --webroot \
  --webroot-path /var/www/letsencrypt \
  -d pastebox.example.com
```

预期证书生成在：

```text
/etc/letsencrypt/live/pastebox.example.com/fullchain.pem
/etc/letsencrypt/live/pastebox.example.com/privkey.pem
```

测试续期：

```sh
sudo certbot renew --dry-run
```

### 完整 Nginx 站点

创建 `/etc/nginx/sites-available/pastebox.conf`（证书路径、域名换成真实值）：

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

    location /.well-known/acme-challenge/ {
        root /var/www/letsencrypt;
    }

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name pastebox.example.com;

    ssl_certificate /etc/letsencrypt/live/pastebox.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/pastebox.example.com/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;

    client_max_body_size 100m;

    proxy_connect_timeout 30s;
    proxy_send_timeout 300s;
    proxy_read_timeout 300s;

    add_header X-Content-Type-Options nosniff always;
    add_header X-Frame-Options DENY always;
    add_header Referrer-Policy strict-origin-when-cross-origin always;
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;

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

切换到完整站点（停用 bootstrap，避免重复监听）：

```sh
sudo rm /etc/nginx/sites-enabled/pastebox-bootstrap.conf
sudo ln -sf /etc/nginx/sites-available/pastebox.conf /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
```

`X-Forwarded-Proto https` 必须传递：PasteBox 靠它判断原始协议，决定 session
cookie 是否带 `Secure` 标记。

s3o 的 Nginx 站点沿用你已部署的配置即可（`127.0.0.1:19000` 上游），本教程不
重复。

## 第 8 步：创建管理员并保存后台配置

创建管理员：

```sh
cd /opt/pastebox
. /opt/pastebox/.env.shared-split
./deploy/pastebox-deploy.sh admin \
  admin@pastebox.example.com \
  '<long-random-admin-password>'
```

浏览器打开 `https://pastebox.example.com`，用该账号登录，进入
**管理后台 > 应用配置**，按实际情况填写并保存：

- 站点名称、公网 URL（`https://pastebox.example.com`）、CORS origin。
- 对象存储：endpoint `https://s3o.example.com`、bucket 为 s3o 虚拟 bucket 名、
  access/secret key 为虚拟 bucket 凭据、开启 path style。
- SMTP、OAuth、Turnstile、Telegram、ClamAV 地址（默认 `clamav:3310`）、
  Worker 心跳、支付等。

保存后等约 10 秒让 API 和 worker 刷新运行时配置，再运行完整预检：

```sh
. /opt/pastebox/.env.shared-split
./deploy/pastebox-deploy.sh preflight
./deploy/pastebox-deploy.sh status
```

健康检查：

```sh
curl -fsS https://pastebox.example.com/healthz
curl -fsS https://pastebox.example.com/readyz
```

`/healthz` 返回 `{"status":"ok"}`；`/readyz` 各组件（database、redis、
object_storage、scanner、mail 等）全部 `ok` 才算部署完成。上传一个附件并在
s3o 日志里看到 `PutObject`，可进一步确认对象存储链路。

## 第 9 步：备份

PasteBox 的备份体系基于 maintenance profile 容器，全部通过部署脚本调用
（split 模式下它们自动连接 `shared-postgres-net` 里的 `shared-postgres`，读写
`shared-postgres-backups` 卷）。以下命令都在 `/opt/pastebox` 下执行，并已
`. /opt/pastebox/.env.shared-split`。

逻辑备份（pg_dump 自定义格式，保留 `PASTEBOX_BACKUP_RETENTION_DAYS` 天）：

```sh
./deploy/pastebox-deploy.sh compose --profile maintenance run --rm postgres-backup
```

PITR base backup（物理全量，WAL 归档的基础）：

```sh
./deploy/pastebox-deploy.sh compose --profile maintenance run --rm postgres-basebackup
```

WAL 新鲜度检查（确认归档没有落后）：

```sh
./deploy/pastebox-deploy.sh compose --profile maintenance run --rm postgres-wal-check
```

逻辑恢复演练（把最近一次逻辑备份恢复到隔离数据库验证可读）：

```sh
./deploy/pastebox-deploy.sh compose --profile maintenance run --rm postgres-restore-drill
```

PITR 恢复演练（用 base backup + WAL 回放到指定时间点）：

```sh
./deploy/pastebox-deploy.sh compose --profile maintenance run --rm postgres-pitr-drill
```

推送 off-host restic 备份（把备份卷加密推到 `PASTEBOX_RESTIC_REPOSITORY`）：

```sh
./deploy/pastebox-deploy.sh compose --profile maintenance run --rm backup-push
```

上线前不要只证明"备份命令能跑"，还要证明"能恢复"：至少完成一次
`postgres-restore-drill` 和一次 `postgres-pitr-drill`，并确认 restic 仓库里
`snapshots` 列表出现了新条目：

```sh
docker run --rm \
  -e RESTIC_REPOSITORY="$PASTEBOX_RESTIC_REPOSITORY" \
  -e RESTIC_PASSWORD="$PASTEBOX_RESTIC_PASSWORD" \
  -e AWS_ACCESS_KEY_ID="$PASTEBOX_BACKUP_S3_ACCESS_KEY" \
  -e AWS_SECRET_ACCESS_KEY="$PASTEBOX_BACKUP_S3_SECRET_KEY" \
  restic/restic:0.18.1 snapshots
```

（命令中的变量从 `deploy/production.env` 读取；也可用 `set -a; . deploy/production.env; set +a`
导出后执行。）

备份排期建议（cron 示例，`crontab -e`）：

```cron
0 3 * * * . /opt/pastebox/.env.shared-split && cd /opt/pastebox && ./deploy/pastebox-deploy.sh compose --profile maintenance run --rm postgres-backup
30 3 * * 0 . /opt/pastebox/.env.shared-split && cd /opt/pastebox && ./deploy/pastebox-deploy.sh compose --profile maintenance run --rm postgres-basebackup
0 4 * * * . /opt/pastebox/.env.shared-split && cd /opt/pastebox && ./deploy/pastebox-deploy.sh compose --profile maintenance run --rm backup-push
```

注意：共享 PostgreSQL 停止时备份容器无法运行；先 `infra-status` 确认数据库
健康再排期。异地备份仓库与附件对象存储必须使用不同凭据。

## 第 10 步：日常运维

所有命令在 `/opt/pastebox` 下执行（已 `. /opt/pastebox/.env.shared-split`）：

```sh
./deploy/pastebox-deploy.sh status         # PasteBox 容器状态
./deploy/pastebox-deploy.sh logs           # 跟随 api/worker 日志
./deploy/pastebox-deploy.sh logs clamav    # 指定服务日志
./deploy/pastebox-deploy.sh down           # 停止 PasteBox（共享 PG/Redis 不受影响）
./deploy/pastebox-deploy.sh infra-status   # 查看两个共享 project 状态
./deploy/pastebox-deploy.sh infra-down     # 停止共享 PG 和 Redis（影响所有接入项目）
./deploy/pastebox-deploy.sh upgrade        # 拉新镜像、迁移并滚动更新
```

单独管理共享服务（独立升级 PostgreSQL 或 Redis 时）：

```sh
cd /opt/shared-postgres
docker compose pull && docker compose up -d   # 升级 PostgreSQL

cd /opt/shared-redis
docker compose pull && docker compose up -d   # 升级 Redis
```

升级共享服务前先完成一次备份；PostgreSQL 大版本升级（17 -> 18）不能只换镜像
tag，需要 `pg_upgrade` 或逻辑导出导入，另行规划。

`infra-reset --confirm-delete-all-data` 会删除共享数据库和 Redis 数据卷，仅供
开发环境重来，生产环境不要执行。

### 其他项目复用共享服务

其他 Docker 项目接入时：

1. PostgreSQL：为其创建独立的数据库和账号（参考第 6 步脚本创建 `pastebox`
   的方式），把项目容器加入外部网络 `shared-postgres-net`，主机写
   `shared-postgres:5432`。
2. Redis：加入外部网络 `shared-redis-net`，主机写 `shared-redis:6379`；与
   PasteBox 共用实例时建议使用不同 DB 编号或 key 前缀。
3. Compose 写法示例：

```yaml
networks:
  shared-postgres:
    external: true
    name: shared-postgres-net
  shared-redis:
    external: true
    name: shared-redis-net
```

不要让其他项目共用 `pastebox` 数据库或账号。

## 常见问题

### `init` 报错 `共享 PostgreSQL 在 120 秒内未就绪`

```sh
cd /opt/shared-postgres && docker compose ps && docker compose logs postgres
```

常见原因：`.env` 里 `SHARED_POSTGRES_PASSWORD` 为空、数据卷残留旧集群初始化
数据、内存不足。

### `up` 或 `migrate` 连不上数据库

确认三个条件：共享 PG 已 `Up (healthy)`；`deploy/production.env` 的
`PASTEBOX_DATABASE_URL` 主机是 `shared-postgres`（不是 `postgres` 或
`127.0.0.1`）；当前 shell 已加载 `. /opt/pastebox/.env.shared-split`（否则
不会加载 `compose.external-split-services.yaml`，容器不会加入共享网络）。

验证容器网络连通性（busybox `nc` 探测 TCP 端口，容器名以 `docker ps` 实际
输出为准）：

```sh
docker exec "$(docker ps -qf name=api)" \
  nc -zv shared-postgres 5432
docker exec "$(docker ps -qf name=api)" \
  nc -zv shared-redis 6379
```

### 登录后马上掉线

Nginx 必须传递 `X-Forwarded-Proto https`；用 HTTPS 访问但没传该头时，后端会
把 cookie 标记为非 `Secure` 导致浏览器丢弃。

### 上传失败或大文件 413

检查 Nginx `client_max_body_size`（示例为 100m），以及后台套餐的单文件大小
限制。直传 s3o 的附件另受 s3o 侧配置约束。

### `readyz` 里 object_storage 不通过

确认后台对象存储 endpoint/bucket/凭据与 s3o 虚拟 bucket 一致、开了 path
style；在宿主机用 AWS CLI 对 `https://s3o.example.com` 做 head-bucket 验证；
查看 PasteBox 容器能否解析 `s3o.example.com`（`extra_hosts` 里的 host-gateway
是否写的是真实域名）。

### `readyz` 里 database/redis 不通过

`infra-status` 确认两个共享 project 健康；分别用第 3、4 步的
`pg_isready`/`redis-cli ping` 验证。Redis 只用于可用性检查，database 不通才是
业务故障。

### 备份容器启动即退出

备份类容器依赖共享 PG 健康，且读取 `shared-postgres-backups` 外部卷；先跑
`infra-status`，再确认卷存在：`docker volume ls | grep shared-postgres-backups`。

## 与本架构相关的其他文档

- [s3-orchestrator 聚合 R2 对接 PasteBox 教程](s3-orchestrator-r2-pastebox-docker.zh-CN.md)
  — s3o 部署、R2 凭据、虚拟 bucket 管理。
- [Docker + Nginx + Cloudflare 生产部署教程](production-docker-nginx-cloudflare.zh-CN.md)
  — 单 project 共享模式和一体化模式。
- [PasteBox 中文部署文档](deployment.zh-CN.md) — 演示栈说明与镜像发布流程。
