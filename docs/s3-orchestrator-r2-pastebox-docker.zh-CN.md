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

本文不是配置片段集合，而是从零开始的操作手册。建议第一次部署时按章节顺序执行，不要先启动 PasteBox 再回头补对象存储。

## 最终会部署出什么

同一台宿主机上运行两个彼此独立的 Docker Compose project：

```text
/opt/s3-orchestrator
  ├── compose.yaml
  ├── .env
  ├── config.yaml
  ├── data/
  └── source/                 # 固定到 v0.62.28 的上游源码

/opt/pastebox
  ├── compose.production.yaml
  ├── compose.nginx-host.yaml
  ├── deploy/production.env
  └── deploy/...              # 数据库、备份和监控辅助文件
```

两个 Compose project 不合并，原因是：

- s3-orchestrator 可以独立升级、重启、备份和回滚。
- PasteBox 使用 GitHub Actions 已构建的 GHCR 镜像，不需要在服务器构建应用。
- 两边的环境变量和密钥不会混在同一个文件里。
- PasteBox 的 `compose.production.yaml` 不需要为了第三方服务而改动。

宿主机只对外开放 `80`、`443` 和受限的 SSH 端口。容器端口只绑定回环地址：

| 服务 | 容器端口 | 宿主机监听 | 公网入口 |
| --- | ---: | --- | --- |
| PasteBox API | `8080` | `127.0.0.1:18080` | `https://pastebox.example.com` |
| s3-orchestrator | `9000` | `127.0.0.1:19000` | `https://s3o.example.com` |
| PostgreSQL、Redis、ClamAV | 各自默认端口 | 不发布到宿主机 | 无 |

### 请求链路

附件上传时，数据按下面的顺序流动：

```text
浏览器
  -> https://pastebox.example.com
  -> Cloudflare
  -> 宿主机 Nginx
  -> PasteBox api 容器
  -> https://s3o.example.com
  -> 宿主机 Nginx
  -> s3-orchestrator 容器
  -> 某个 Cloudflare R2 backend bucket
```

PasteBox 容器通过 `extra_hosts` 把 `s3o.example.com` 解析到 Docker host gateway。请求仍然使用真实域名和 HTTPS，所以证书校验、SNI 和 PasteBox 生产 preflight 都能正常工作。

### 三组名称和凭据不要混用

这套部署最容易出错的是把 R2 凭据和虚拟 bucket 凭据混在一起：

| 用途 | 名称示例 | 谁使用 | 凭据从哪里来 |
| --- | --- | --- | --- |
| R2 backend 1 | `pastebox-r2-a` | s3-orchestrator | Cloudflare R2 API Token |
| R2 backend 2 | `pastebox-r2-b` | s3-orchestrator | Cloudflare R2 API Token |
| R2 backend 3 | `pastebox-r2-c` | s3-orchestrator | Cloudflare R2 API Token |
| s3-orchestrator 虚拟 bucket | `pastebox-files` | PasteBox | 你自己生成并写入 s3-orchestrator 配置 |

PasteBox 永远不直接拿 R2 backend 的密钥。PasteBox 只知道：

```text
Endpoint:   https://s3o.example.com
Bucket:     pastebox-files
Access Key: 你生成的虚拟 bucket access key
Secret Key: 你生成的虚拟 bucket secret key
```

## 教程执行顺序

1. 准备服务器、Docker、Nginx、域名和端口。
2. 在 Cloudflare 创建 R2 bucket 和最小权限凭据。
3. 用 AWS CLI 直连测试每个 R2 bucket。
4. 独立部署并验证 s3-orchestrator。
5. 配置 DNS、证书和宿主机 Nginx。
6. 从 GHCR 拉取固定版本的 PasteBox 镜像。
7. 配置并启动 PasteBox 依赖、迁移、API 和 worker。
8. 依次完成 S3 API、readiness 和浏览器业务验收。

## 域名规划

下面用示例域名说明，部署时换成你的真实域名：

```text
pastebox.example.com  -> Cloudflare Proxied -> Nginx -> 127.0.0.1:18080 -> PasteBox api
s3o.example.com       -> DNS only           -> Nginx -> 127.0.0.1:19000 -> s3-orchestrator
```

`pastebox.example.com` 可以走 Cloudflare CDN，因为它面对浏览器用户。

`s3o.example.com` 建议 DNS only，因为它是 S3 API endpoint。Cloudflare CDN、WAF、缓存和上传大小限制容易干扰 S3 签名、上传和长连接。PasteBox 会在服务端访问 `https://s3o.example.com`，不需要用户浏览器直接访问它。

## 前置条件

- 一台 Ubuntu 22.04/24.04 或 Debian 12 VPS。
- 建议至少 2 核 CPU、4GB 内存和 50GB 系统盘；ClamAV 初始化会占用较多内存。
- 已有 sudo 权限，并且 SSH 有备用登录方式。
- 已安装 Docker Engine、Docker Compose plugin、Nginx、Git、curl、OpenSSL。
- Cloudflare 托管你的域名。
- 已在 Cloudflare 账户中开通 R2，并能添加付款方式。
- 已准备创建一个或多个 Cloudflare R2 bucket。
- PasteBox 镜像使用固定 tag 或 digest，例如 `ghcr.io/cvinit/pastebox:sha-<commit>`，不要用 `latest`。

### 安装基础软件

以下命令在宿主机执行：

```sh
sudo apt update
sudo apt install -y ca-certificates curl gnupg git nginx nano openssl ufw unzip
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker "$USER"
```

重新登录 SSH，让 Docker 用户组生效，然后检查：

```sh
docker version
docker compose version
nginx -v
git --version
curl --version
```

预期结果：

- `docker version` 同时显示 Client 和 Server。
- `docker compose version` 显示 Compose v2，不是旧的 `docker-compose`。
- Nginx 处于 running 状态：

  ```sh
  sudo systemctl enable --now nginx
  sudo systemctl status nginx --no-pager
  ```

如果 `docker version` 报 permission denied，先退出 SSH 再重新登录，不要长期用 `sudo docker` 掩盖用户组问题。

### 检查端口占用

```sh
sudo ss -lntp
```

确认：

- `80` 和 `443` 可以由 Nginx 使用。
- `18080` 和 `19000` 没有被其他程序占用。
- PostgreSQL、Redis、ClamAV 不需要暴露公网。

### 创建两个部署目录

```sh
sudo mkdir -p /opt/s3-orchestrator /opt/pastebox
sudo chown -R "$USER:$USER" /opt/s3-orchestrator /opt/pastebox
```

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

保存后从宿主机检查解析：

```sh
dig +short pastebox.example.com A
dig +short s3o.example.com A
```

没有 `dig` 时安装 `dnsutils`：

```sh
sudo apt install -y dnsutils
```

`s3o.example.com` 应直接返回宿主机公网 IP。`pastebox.example.com` 开启代理后通常返回 Cloudflare 边缘 IP，这是正常现象。

### PasteBox 域名

Cloudflare SSL/TLS 推荐：

```text
SSL/TLS encryption mode: Full (strict)
Always Use HTTPS: On
Automatic HTTPS Rewrites: On
Minimum TLS Version: TLS 1.2 或更高
```

不要用 `Flexible`。Flexible 会让 Cloudflare 到源站走 HTTP，容易导致 cookie、重定向、OAuth 回调和安全判断出问题。

PasteBox 源站证书可以用 Cloudflare Origin Certificate：

1. Cloudflare 进入 `SSL/TLS -> Origin Server`。
2. 点击 `Create Certificate`。
3. Hostnames 只填 `pastebox.example.com`。
4. 有效期按你的证书轮换制度选择。
5. 分别保存 Origin Certificate 和 Private Key。

这个证书只需要覆盖：

```text
pastebox.example.com
```

把证书保存到宿主机：

```sh
sudo install -d -m 700 /etc/nginx/ssl/pastebox
sudo nano /etc/nginx/ssl/pastebox/origin.pem
sudo nano /etc/nginx/ssl/pastebox/origin.key
sudo chmod 644 /etc/nginx/ssl/pastebox/origin.pem
sudo chmod 600 /etc/nginx/ssl/pastebox/origin.key
```

Origin Certificate 只受 Cloudflare 信任，因此 `pastebox.example.com` 必须保持 Proxied。浏览器绕过 Cloudflare 直连源站时不会信任这个证书。

### s3-orchestrator 域名

`s3o.example.com` 是 DNS only，不能使用只受 Cloudflare 信任的 Origin Certificate，必须使用 Let's Encrypt 或其他公开可信证书。

安装 Certbot：

```sh
sudo apt update
sudo apt install -y certbot
sudo mkdir -p /var/www/letsencrypt/.well-known/acme-challenge
```

在完整 Nginx 配置启用前，创建临时文件 `/etc/nginx/sites-available/s3o-bootstrap.conf`：

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name s3o.example.com;

    location /.well-known/acme-challenge/ {
        root /var/www/letsencrypt;
    }

    location / {
        return 404;
    }
}
```

启用临时站点并申请证书：

```sh
sudo ln -sf \
  /etc/nginx/sites-available/s3o-bootstrap.conf \
  /etc/nginx/sites-enabled/s3o-bootstrap.conf
sudo nginx -t
sudo systemctl reload nginx

sudo certbot certonly \
  --webroot \
  --webroot-path /var/www/letsencrypt \
  -d s3o.example.com
```

申请成功后应存在：

```text
/etc/letsencrypt/live/s3o.example.com/fullchain.pem
/etc/letsencrypt/live/s3o.example.com/privkey.pem
```

测试自动续期：

```sh
sudo certbot renew --dry-run
```

后面启用完整 `pastebox.conf` 前要停用 `s3o-bootstrap.conf`，避免两个 server block 重复监听同一域名。

如果不想让 `s3o.example.com` 长期对公网开放，可以使用 Cloudflare DNS challenge 申请证书，然后在 Nginx 里按 Docker 网段和管理员 IP 做访问限制。注意：因为 `s3o.example.com` 是 DNS only，宿主机 `443` 不能全局只允许 Cloudflare IP，否则 PasteBox 容器和外部 S3 客户端可能无法直连它。

## Cloudflare R2：从开通到取得 S3 凭据

下面的菜单名称按 2026-08-05 的 Cloudflare 控制台和官方文档编写。Cloudflare 后续可能调整菜单位置；若名称略有变化，优先查阅文末官方链接。

### 1. 开通 R2

1. 登录 [Cloudflare Dashboard](https://dash.cloudflare.com/)。
2. 切换到准备使用的 Cloudflare account。
3. 左侧进入 `Storage & databases -> R2 object storage`。
4. 如果页面要求开通或购买 R2，按提示绑定付款方式并确认。
5. 回到 R2 Overview，确认页面可以显示 Buckets 和 Account Details。

Cloudflare 当前要求先开通 R2，之后才能创建 R2 专用 API token。这里创建的是 R2 S3 API 凭据，不是 Cloudflare 全局 API Key，也不是普通 Zone API Token。

### 2. 规划 bucket

本文用三个 backend 演示：

```text
pastebox-r2-a
pastebox-r2-b
pastebox-r2-c
```

可以只部署一个 bucket。只使用一个 bucket 时，删除配置中的 `BACKEND2_*`、`BACKEND3_*` 和对应的两个 `backends` 项即可。

重要计费提醒：

- 多建 bucket 可以做权限隔离、故障隔离和路由分组。
- 同一个 Cloudflare account 下多建 bucket，不代表每个 bucket 都自动获得一份新的 10GB 免费用量。
- Cloudflare 定价页按每月总存储和请求量计算 included usage。不要把 “3 个 bucket” 直接理解成 “30GB 永久免费”。
- 本文的 `quota_bytes` 是 s3-orchestrator 的保护阈值，不是 Cloudflare 账单上限。

如果三个 bucket 都属于同一个 Cloudflare account，它们通常共用同一个 Account ID 和 S3 endpoint，但仍建议每个 bucket 使用独立 token。

### 3. 创建第一个 bucket

1. 在 `R2 object storage -> Overview` 点击 `Create bucket`。
2. Bucket name 填 `pastebox-r2-a`。
3. Location 或 Location hint：
   - 不确定时选 `Automatic`。
   - 尽量选择靠近宿主机或主要用户的区域。
   - Location hint 不是 AWS region，R2 S3 客户端的 region 仍填 `auto`。
4. Jurisdiction：
   - 没有数据驻留要求时使用默认 jurisdiction。
   - 只有明确要求数据必须留在欧盟时才选 EU。
5. Default storage class 选择 `Standard`。
6. 创建 bucket。

PasteBox 临时附件会频繁写入、读取和删除，`Standard` 没有 Infrequent Access 的 30 天最短保存期和读取处理费，更适合这个场景。

创建完成后检查 bucket 设置：

- Public access 保持关闭。
- 不需要启用 `r2.dev` public URL。
- 不需要给 backend bucket 绑定自定义域名。
- 不需要配置浏览器 CORS，因为只有服务器上的 s3-orchestrator 访问 R2。

重复相同步骤创建 `pastebox-r2-b` 和 `pastebox-r2-c`。

### 4. 找到 Account ID 和 endpoint

在 `R2 object storage -> Overview` 的 `Account Details` 区域找到 Account ID。也可以从 Cloudflare account 首页右侧复制。

默认 jurisdiction 的 endpoint：

```text
https://<ACCOUNT_ID>.r2.cloudflarestorage.com
```

EU jurisdiction 的 endpoint：

```text
https://<ACCOUNT_ID>.eu.r2.cloudflarestorage.com
```

不要把下面这些地址当作 S3 endpoint：

- Cloudflare Dashboard 浏览器地址。
- bucket 的 `r2.dev` 地址。
- 绑定给公开 bucket 的 custom domain。
- `s3o.example.com`。这个是后面由 s3-orchestrator 提供的虚拟 S3 endpoint。

R2 backend 的固定参数是：

```text
region: auto
force_path_style: true
```

### 5. 为第一个 bucket 创建最小权限凭据

1. 进入 `R2 object storage -> Overview`。
2. 在 `Account Details` 找到 `API Tokens`。
3. 点击旁边的 `Manage`。
4. 选择 `Create Account API token`。
5. Token name 填 `pastebox-s3o-r2-a`。
6. Permissions 选择 `Object Read & Write`。
7. Bucket scope 选择只允许 `pastebox-r2-a`。
8. 如果页面提供有效期和 IP 限制：
   - 有成熟轮换流程时可设置有效期。
   - 只有宿主机公网出口 IP 固定时才设置 IP 限制。
9. 点击创建。

不要选 `Admin Read & Write`。s3-orchestrator 只需要在现有 bucket 中读、写、列出和删除对象，不需要创建或删除 bucket。

Cloudflare 当前只有 Super Administrator 可以查看或创建 Account API token。如果你没有这个角色，可以让账户管理员创建；也可以选择 `Create User API token`，但它会继承个人账号生命周期，个人被移出 account 后 token 会失效。生产服务器优先使用有明确所有者和轮换制度的 Account API token。

创建成功后页面会显示：

```text
Access Key ID
Secret Access Key
S3 endpoint
```

立即把它们保存到密码管理器。Secret Access Key 离开页面后不能再次查看；丢失时只能创建新 token 并撤销旧 token。

重复创建：

| Token name | Permission | Bucket scope |
| --- | --- | --- |
| `pastebox-s3o-r2-a` | Object Read & Write | `pastebox-r2-a` |
| `pastebox-s3o-r2-b` | Object Read & Write | `pastebox-r2-b` |
| `pastebox-s3o-r2-c` | Object Read & Write | `pastebox-r2-c` |

### 6. 建立部署记录

先在密码管理器中建立这张表，不要把真实值写进本文或提交到 Git：

| S3Orchestrator 变量 | R2 a | R2 b | R2 c |
| --- | --- | --- | --- |
| `BACKEND*_NAME` | `r2-a` | `r2-b` | `r2-c` |
| `BACKEND*_BUCKET` | `pastebox-r2-a` | `pastebox-r2-b` | `pastebox-r2-c` |
| `BACKEND*_ENDPOINT` | Account a endpoint | Account b endpoint | Account c endpoint |
| `BACKEND*_REGION` | `auto` | `auto` | `auto` |
| `BACKEND*_ACCESS_KEY` | Token a Access Key ID | Token b Access Key ID | Token c Access Key ID |
| `BACKEND*_SECRET_KEY` | Token a Secret Access Key | Token b Secret Access Key | Token c Secret Access Key |

同一 account 下三个 endpoint 可以相同；bucket 名和 token 必须对应。

### 7. 安装并配置 AWS CLI

AWS CLI 只用于部署前后验收，不是 S3Orchestrator 的运行依赖。

Debian/Ubuntu 仓库能提供 AWS CLI 时可以安装：

```sh
sudo apt update
sudo apt install -y awscli
aws --version
```

如果系统仓库没有 `awscli`，按 [AWS CLI 官方安装教程](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) 安装 v2。

为第一个 bucket 创建本地 profile：

```sh
aws configure --profile pastebox-r2-a
```

依次输入：

```text
AWS Access Key ID: <R2 a Access Key ID>
AWS Secret Access Key: <R2 a Secret Access Key>
Default region name: auto
Default output format: json
```

`aws configure` 会把凭据写入当前 Linux 用户的 `~/.aws/credentials`。确认权限：

```sh
chmod 700 ~/.aws
chmod 600 ~/.aws/credentials ~/.aws/config
```

### 8. 直连验证 R2 backend

先检查 bucket 权限：

```sh
aws --profile pastebox-r2-a \
  --endpoint-url https://<ACCOUNT_ID>.r2.cloudflarestorage.com \
  --region auto \
  s3api head-bucket \
  --bucket pastebox-r2-a
```

命令退出码为 0 且没有错误输出，就表示 endpoint、bucket、Access Key、Secret Key 和权限能配合工作。

创建测试文件并完成上传、下载、对比和删除：

```sh
printf 'pastebox r2 backend smoke\n' > /tmp/pastebox-r2-a.txt

aws --profile pastebox-r2-a \
  --endpoint-url https://<ACCOUNT_ID>.r2.cloudflarestorage.com \
  --region auto \
  s3api put-object \
  --bucket pastebox-r2-a \
  --key smoke/backend-a.txt \
  --body /tmp/pastebox-r2-a.txt \
  --content-type text/plain

aws --profile pastebox-r2-a \
  --endpoint-url https://<ACCOUNT_ID>.r2.cloudflarestorage.com \
  --region auto \
  s3api get-object \
  --bucket pastebox-r2-a \
  --key smoke/backend-a.txt \
  /tmp/pastebox-r2-a.out

diff -u /tmp/pastebox-r2-a.txt /tmp/pastebox-r2-a.out

aws --profile pastebox-r2-a \
  --endpoint-url https://<ACCOUNT_ID>.r2.cloudflarestorage.com \
  --region auto \
  s3api delete-object \
  --bucket pastebox-r2-a \
  --key smoke/backend-a.txt
```

预期结果：

- `put-object` 返回 ETag。
- `get-object` 显示下载字节数。
- `diff` 没有输出并返回 0。
- `delete-object` 返回成功。

为 `pastebox-r2-b`、`pastebox-r2-c` 分别建立 profile 并重复测试。任意 backend 失败时，先修好 R2 凭据，不要继续部署 s3-orchestrator。

常见错误：

| 错误 | 优先检查 |
| --- | --- |
| `InvalidAccessKeyId` | Access Key 是否来自 R2 专用 token |
| `SignatureDoesNotMatch` | Secret Key、endpoint、系统时间是否正确 |
| `AccessDenied` | Token 是否是 Object Read & Write，是否限制到了错误 bucket |
| `NoSuchBucket` | Bucket 名拼写和 Cloudflare account 是否正确 |
| 连接超时 | 宿主机 DNS、出站 443、防火墙 |

## 部署 S3Orchestrator

S3Orchestrator 单独放在 `/opt/s3-orchestrator`，不放进 PasteBox 仓库，也不加入 PasteBox 的 Compose 文件。

### 1. 准备目录

以下命令在宿主机执行：

```sh
cd /opt/s3-orchestrator
mkdir -p data
```

最终目录应当是：

```text
/opt/s3-orchestrator/
  compose.yaml
  config.yaml
  .env
  data/
  source/
```

### 2. 拉取固定版本源码

本文核对的上游正式版本是 `v0.62.28`。固定 tag 拉取：

```sh
cd /opt/s3-orchestrator
git clone \
  --branch v0.62.28 \
  --depth 1 \
  https://github.com/afreidah/s3-orchestrator.git \
  source
```

检查实际版本：

```sh
git -C /opt/s3-orchestrator/source describe --tags --exact-match
git -C /opt/s3-orchestrator/source rev-parse HEAD
```

预期分别得到：

```text
v0.62.28
cd9a7eacc143ed0f9cd03bc38f5d65c28c8cdbaa
```

如果 tag 对应的 commit 不一致，先检查仓库 URL 和供应链状态，不要继续构建。

### 3. 准备 SQLite 数据目录

上游 Dockerfile 使用 UID `10001` 的非 root 用户运行。给它数据目录写权限：

```sh
sudo chown -R 10001:10001 /opt/s3-orchestrator/data
sudo chmod 750 /opt/s3-orchestrator/data
```

不要把数据库放在容器可写层。容器重建后可写层会丢失，`data/` 必须持久化到宿主机。

## 配置 S3Orchestrator

创建 `/opt/s3-orchestrator/config.yaml`：

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
    unsigned_payload: true
    quota_bytes: 9663676416

  - name: ${BACKEND2_NAME}
    endpoint: ${BACKEND2_ENDPOINT}
    region: ${BACKEND2_REGION}
    bucket: ${BACKEND2_BUCKET}
    access_key_id: ${BACKEND2_ACCESS_KEY}
    secret_access_key: ${BACKEND2_SECRET_KEY}
    force_path_style: true
    unsigned_payload: true
    quota_bytes: 9663676416

  - name: ${BACKEND3_NAME}
    endpoint: ${BACKEND3_ENDPOINT}
    region: ${BACKEND3_REGION}
    bucket: ${BACKEND3_BUCKET}
    access_key_id: ${BACKEND3_ACCESS_KEY}
    secret_access_key: ${BACKEND3_SECRET_KEY}
    force_path_style: true
    unsigned_payload: true
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
    enabled: false
    path: /metrics
```

关键配置解释：

- `server.listen_addr=:9000`：监听容器内部端口，宿主机稍后映射到 `127.0.0.1:19000`。
- `server.max_object_size=5368709120`：允许最大 5GiB 对象。PasteBox 自身、Nginx 和 Cloudflare 可能有更小的上限。
- `database.path`：必须落到挂载的 `/var/lib/s3-orchestrator` 目录。
- `buckets[].name=pastebox-files`：这是给 PasteBox 使用的虚拟 bucket，不是 R2 bucket。
- `force_path_style=true`：R2 和 PasteBox 对接 S3Orchestrator 时都采用 path-style。
- `unsigned_payload=true`：通过 HTTPS 向 R2 流式发送对象，避免为了 SigV4 哈希把整个对象先缓存在内存。
- `routing_strategy=spread`：优先选择当前使用率较低的 backend，使相同配额的 bucket 大致均匀使用。
- `replication.factor=1`：每个对象只保存到一个 backend，适合聚合容量。
- `telemetry.metrics.enabled=false`：本文不把指标端点暴露在公网 S3 域名上。后续接入内部 Prometheus 时再单独配置监听地址和访问控制。

如果你更希望先填满第一个 bucket，再使用第二个，可以把 `routing_strategy` 改成 `pack`。

`replication.factor=2` 会在两个 backend 保存副本，可以提升 backend 故障容忍度，但可用容量约减半，R2 写请求也会增加。改副本数前先核算成本并做恢复演练。

必须明确：`replication.factor=1` 只有容量聚合，没有跨 backend 副本。某个 backend 永久丢失时，只存在于该 backend 的对象也会永久丢失；多个 bucket 位于同一个 Cloudflare account 时，也不能当作跨供应商灾备。需要容忍 backend 丢失时，应至少使用 `factor=2`，并把 backend 分散到满足你故障隔离要求的账户或供应商。

`quota_bytes=9663676416` 约等于 9GiB，只是示例保护阈值：

- 三个 bucket 在同一 Cloudflare account 时，不能把它理解成三份免费额度。
- 如果你愿意按量付费，可以按预算设置更高配额。
- 如果不使用配额，所有 backend 都应把 `quota_bytes` 设为 `0` 或全部省略；不要混用有限和无限 backend。

### 创建 S3Orchestrator 环境变量

创建 `/opt/s3-orchestrator/.env`：

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

S3O_HOST_HTTP_PORT=19000
```

设置权限：

```sh
chmod 600 /opt/s3-orchestrator/.env
```

不要把 `/opt/s3-orchestrator/.env` 提交到 Git、发到聊天窗口或放进普通压缩包。Docker Compose 会同时用它做 Compose 变量替换，并通过 `env_file` 把变量传给 S3Orchestrator。

生成虚拟 bucket 凭据可以用：

```sh
openssl rand -hex 20
openssl rand -hex 32
```

第一条输出填 `BUCKET_ACCESS_KEY`，第二条输出填 `BUCKET_SECRET_KEY`。它们不是 Cloudflare 凭据，不需要在 Cloudflare 控制台创建。

填完后检查是否还有占位符：

```sh
cd /opt/s3-orchestrator
grep -n '<' .env
```

正常情况不应有输出。不要运行 `docker compose config` 后把完整输出发给别人，因为渲染后的配置可能包含密钥。

### 创建独立 Compose 文件

创建 `/opt/s3-orchestrator/compose.yaml`：

```yaml
name: s3-orchestrator

services:
  s3-orchestrator:
    build:
      context: ./source
      args:
        VERSION: v0.62.28
    image: local/s3-orchestrator:v0.62.28
    restart: unless-stopped
    init: true
    command: ["-config", "/etc/s3-orchestrator/config.yaml"]
    env_file:
      - ./.env
    ports:
      - "127.0.0.1:${S3O_HOST_HTTP_PORT:-19000}:9000"
    volumes:
      - ./config.yaml:/etc/s3-orchestrator/config.yaml:ro
      - ./data:/var/lib/s3-orchestrator
    security_opt:
      - no-new-privileges:true
    stop_grace_period: 30s
    logging:
      driver: json-file
      options:
        max-size: "20m"
        max-file: "5"
```

上游镜像自身已经包含 `/health/ready` healthcheck，并以 UID `10001` 运行。这里没有把 `9000` 映射到 `0.0.0.0`，所以公网不能绕过 Nginx 直接访问容器。

### 渲染并校验配置

执行目录：`/opt/s3-orchestrator`。

先确认 Compose 能解析：

```sh
cd /opt/s3-orchestrator
docker compose config --quiet
```

预期没有输出且退出码为 0。

构建固定版本镜像：

```sh
docker compose build --pull s3-orchestrator
```

检查镜像：

```sh
docker image inspect local/s3-orchestrator:v0.62.28 \
  --format '{{json .Config.Labels}}'
```

输出中应包含版本 `v0.62.28` 和上游源码地址。

使用容器内置命令校验 `config.yaml`：

```sh
docker compose run --rm \
  s3-orchestrator \
  validate -config /etc/s3-orchestrator/config.yaml
```

预期配置验证成功。如果失败：

- 报环境变量为空：检查 `.env` 中对应的 `BACKEND*` 或 `BUCKET_*`。
- 报 backend 重名：每个 `BACKEND*_NAME` 必须唯一。
- 报 bucket 重名：检查 `config.yaml` 中虚拟 bucket。
- 报 quota 混用：全部 backend 统一使用有限配额，或全部使用无限配额。

### 首次启动 S3Orchestrator

```sh
cd /opt/s3-orchestrator
docker compose up -d s3-orchestrator
docker compose ps
```

第一次启动后等 health 状态变为 `healthy`。查看日志：

```sh
docker compose logs --tail=200 s3-orchestrator
```

宿主机直连健康检查：

```sh
curl -fsS http://127.0.0.1:19000/health/ready
```

这一步只验证容器、配置、SQLite 和 backend 初始化。`https://s3o.example.com` 要等 DNS、证书和 Nginx 配好后再验证。

确认端口只监听回环地址：

```sh
sudo ss -lntp | grep ':19000'
```

应看到 `127.0.0.1:19000`，不应看到 `0.0.0.0:19000` 或 `[::]:19000`。

## 部署 PasteBox：使用 GitHub 自动构建镜像

PasteBox 的 Docker 镜像由仓库的 `.github/workflows/docker-image.yml` 自动构建并发布到：

```text
ghcr.io/cvinit/pastebox
```

当前工作流在 main、版本 tag 或手动触发后发布多架构镜像，包含 `linux/amd64` 和 `linux/arm64`，并生成：

- `latest`：main 当前最新构建，会移动。
- `vX.Y.Z`：Git tag 构建。
- `sha-<commit>`：固定 Git commit 的构建。

生产部署使用 GitHub Packages 页面实际显示的 `sha-*` tag，或者进一步固定到 digest。不要直接使用 `latest`。

### 1. 取得生产 Compose 文件

即使不在服务器构建 PasteBox，仍需从仓库取得 `compose.production.yaml`、环境变量模板、数据库配置和备份脚本。

如果 `/opt/pastebox` 还是空目录：

```sh
git clone https://github.com/CVinit/PasteBox.git /opt/pastebox
cd /opt/pastebox
```

生产上应切换到准备发布的固定 commit，而不是长期停留在不断变化的 main：

```sh
git fetch --all --tags
git checkout <release-commit>
git rev-parse HEAD
```

这里的 `<release-commit>` 应与 GHCR 的 `sha-*` 镜像来自同一次提交。仓库文件和镜像版本不一致时，Compose、环境变量或迁移命令可能不兼容。

### 2. 确认 GitHub Actions 已成功构建

在浏览器打开 PasteBox GitHub 仓库：

1. 进入 `Actions`。
2. 选择 `Docker image` workflow。
3. 打开目标 commit 对应的运行记录。
4. 确认 `Build and publish image` 成功。
5. 回到仓库首页，进入 `Packages` 中的 PasteBox container package。
6. 记录页面显示的 `sha-*` tag 或 digest。

如果 workflow 失败，不要假设镜像已经存在。先修复构建或选择上一个已成功发布的固定版本。

### 3. 登录 GHCR

如果 package 是 public，可以直接测试：

```sh
docker pull ghcr.io/cvinit/pastebox:sha-<commit>
```

如果返回 `denied`、`unauthorized` 或 package 是 private，需要 GitHub personal access token classic：

1. GitHub 进入 `Settings -> Developer settings -> Personal access tokens -> Tokens (classic)`。
2. 创建只用于服务器拉取镜像的 token。
3. 最小权限选择 `read:packages`。
4. 如果组织要求 SSO，为 token 授权该组织。

避免把 token 直接写在命令行参数中：

```sh
read -rsp 'GitHub package token: ' CR_PAT
printf '%s' "$CR_PAT" | docker login ghcr.io -u '<github-username>' --password-stdin
unset CR_PAT
```

预期看到 `Login Succeeded`。

### 4. 拉取并固定镜像

先用 package 页面显示的 tag 拉取：

```sh
docker pull ghcr.io/cvinit/pastebox:sha-<commit>
```

记录镜像 digest：

```sh
docker image inspect \
  ghcr.io/cvinit/pastebox:sha-<commit> \
  --format '{{index .RepoDigests 0}}'
```

输出类似：

```text
ghcr.io/cvinit/pastebox@sha256:<64-hex-digest>
```

两种固定方法：

```sh
# 可读性更好，固定到 commit 构建
PASTEBOX_IMAGE=ghcr.io/cvinit/pastebox:sha-<commit>

# 最严格，固定到完全不可变的镜像内容
PASTEBOX_IMAGE=ghcr.io/cvinit/pastebox@sha256:<digest>
```

本文后续使用 `sha-*` 表示。要求严格可复现时改成 digest。

### 5. 创建生产环境文件

执行目录：`/opt/pastebox`。

```sh
cd /opt/pastebox
cp deploy/production.env.example deploy/production.env
chmod 600 deploy/production.env
```

先填写基础运行项：

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
```

生成应用密钥：

```sh
openssl rand -base64 48
openssl rand -hex 32
```

分别填入：

```sh
PASTEBOX_CSRF_SECRET=<long-random-secret>
PASTEBOX_METRICS_TOKEN=<long-random-token>
```

数据库、Redis 和病毒扫描：

```sh
PASTEBOX_POSTGRES_DB=pastebox
PASTEBOX_POSTGRES_USER=pastebox
PASTEBOX_POSTGRES_PASSWORD=<long-random-postgres-password>
PASTEBOX_DATABASE_URL=postgres://pastebox:<same-password>@postgres:5432/pastebox?sslmode=disable

PASTEBOX_REDIS_ADDR=redis:6379
PASTEBOX_WORKER_ID=pastebox-worker
PASTEBOX_WORKER_HEARTBEAT_MAX_AGE_SECONDS=120

PASTEBOX_SCANNER_PROVIDER=clamav
PASTEBOX_CLAMAV_ADDR=clamav:3310
PASTEBOX_CLAMAV_TIMEOUT_SECONDS=30
```

`PASTEBOX_POSTGRES_PASSWORD` 和 `PASTEBOX_DATABASE_URL` 中的密码必须完全一致。如果密码含有 `@`、`:`、`/`、`?` 等 URL 特殊字符，需要先做 URL 编码；为减少首次部署错误，可以使用足够长的十六进制随机值。

生产 preflight 还会检查 SMTP、OAuth、支付、管理员和备份变量。根据实际启用的供应商填写 `deploy/production.env`，不要删除模板中的其他变量，也不要用本地 Mailpit、MinIO 或占位地址绕过检查。

### 6. 填写其他生产集成

邮件必须使用真实 SMTP：

```sh
PASTEBOX_MAILER_PROVIDER=smtp
PASTEBOX_SMTP_HOST=smtp.example-provider.com
PASTEBOX_SMTP_PORT=587
PASTEBOX_SMTP_USERNAME=<smtp-user>
PASTEBOX_SMTP_PASSWORD=<smtp-password>
PASTEBOX_SMTP_FROM_EMAIL=no-reply@your-domain.com
PASTEBOX_SMTP_FROM_NAME=PasteBox
PASTEBOX_SMTP_TLS_MODE=starttls
```

如果供应商要求 465 端口，通常改为：

```sh
PASTEBOX_SMTP_PORT=465
PASTEBOX_SMTP_TLS_MODE=tls
```

Google OAuth：

```sh
PASTEBOX_GOOGLE_OAUTH_CLIENT_ID=<google-client-id>
PASTEBOX_GOOGLE_OAUTH_CLIENT_SECRET=<google-client-secret>
PASTEBOX_GOOGLE_OAUTH_REDIRECT_URL=https://pastebox.example.com/api/v1/auth/google/callback
```

Google Cloud Console 中登记的 redirect URI 必须和这里逐字一致，包括 `https`、域名和路径。

Stripe：

```sh
PASTEBOX_STRIPE_ENABLED=true
PASTEBOX_STRIPE_WEBHOOK_SECRET=whsec_<stripe-webhook-secret>
PASTEBOX_STRIPE_CHECKOUT_URL_TEMPLATE=https://<stripe-checkout-host>/checkout?order_id={order_id}&plan_id={plan_id}&period={period}&amount_cents={amount_cents}&currency={currency}&success_url={success_url}&cancel_url={cancel_url}
```

Stripe webhook 地址：

```text
https://pastebox.example.com/api/v1/billing/webhooks/stripe
```

Epusdt：

```sh
PASTEBOX_EPUSDT_ENABLED=true
PASTEBOX_EPUSDT_PID=<epusdt-pid>
PASTEBOX_EPUSDT_SECRET_KEY=<epusdt-secret>
PASTEBOX_EPUSDT_CHECKOUT_URL_TEMPLATE=https://<epusdt-host>/pay?order_id={order_id}&amount_cents={amount_cents}&currency={currency}
PASTEBOX_EPUSDT_ADDRESS=<usdt-receive-address>
PASTEBOX_EPUSDT_CHAIN=USDT-TRC20
```

Epusdt webhook 地址：

```text
https://pastebox.example.com/api/v1/billing/webhooks/epusdt
```

首次启动的管理员账号：

```sh
PASTEBOX_BOOTSTRAP_ADMIN_EMAIL=admin@your-domain.com
PASTEBOX_BOOTSTRAP_ADMIN_PASSWORD=<long-random-admin-password>
```

不要复用数据库、邮箱或 GitHub token 作为管理员密码。

PostgreSQL 异地备份使用独立的 S3 凭据，不复用附件存储或 S3Orchestrator 凭据：

```sh
PASTEBOX_RESTIC_REPOSITORY=s3:https://<backup-storage-endpoint>/pastebox-backups
PASTEBOX_RESTIC_PASSWORD=<long-random-restic-password>
PASTEBOX_BACKUP_S3_ACCESS_KEY=<backup-access-key>
PASTEBOX_BACKUP_S3_SECRET_KEY=<backup-secret-key>
PASTEBOX_BACKUP_S3_REGION=us-east-1
```

这部分仅给出环境变量关系。每个外部供应商都应先按照 `docs/production-provider-smoke-tests.md` 做真实 smoke，生产 preflight 不能证明外部账户、回调或邮件一定可用。

### 7. 配置 PasteBox 对接 S3Orchestrator

编辑 `/opt/pastebox/deploy/production.env`，对象存储相关项改成：

```sh
PASTEBOX_HOST_HTTP_PORT=18080

S3O_DOMAIN=s3o.example.com

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

对应关系再确认一次：

| PasteBox 变量 | 值来自哪里 |
| --- | --- |
| `PASTEBOX_S3_ENDPOINT` | 宿主机 Nginx 提供的 S3Orchestrator HTTPS 域名 |
| `PASTEBOX_S3_BUCKET` | `config.yaml` 的 `buckets[].name` |
| `PASTEBOX_S3_ACCESS_KEY` | `/opt/s3-orchestrator/.env` 的 `BUCKET_ACCESS_KEY` |
| `PASTEBOX_S3_SECRET_KEY` | `/opt/s3-orchestrator/.env` 的 `BUCKET_SECRET_KEY` |
| `PASTEBOX_S3_REGION` | 固定 `us-east-1` |
| `PASTEBOX_S3_USE_PATH_STYLE` | 固定 `true` |

R2 backend 的 region 是 `auto`，PasteBox 访问虚拟 S3 endpoint 的 region 是 `us-east-1`。这是两段不同的 S3 连接，不要强行改成相同值。

### 8. 创建 PasteBox 的 Nginx Compose override

创建 `/opt/pastebox/compose.nginx-host.yaml`：

```yaml
services:
  api:
    ports:
      - "127.0.0.1:${PASTEBOX_HOST_HTTP_PORT:-18080}:8080"
    extra_hosts:
      - "${S3O_DOMAIN:?set S3O_DOMAIN}:host-gateway"

  worker:
    extra_hosts:
      - "${S3O_DOMAIN:?set S3O_DOMAIN}:host-gateway"

  preflight:
    extra_hosts:
      - "${S3O_DOMAIN:?set S3O_DOMAIN}:host-gateway"
```

这个 override 做两件事：

1. 只把 PasteBox API 发布到宿主机 `127.0.0.1:18080`。
2. 让需要访问对象存储的容器把 `s3o.example.com` 解析到宿主机 Docker gateway。

两个 Compose project 彼此独立，因此这里没有跨 project 的 `depends_on`。启动顺序由后文命令保证。

后续所有 PasteBox 生产命令都使用：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  <command>
```

不要运行不带 service 名称的 `docker compose up -d`，因为 `compose.production.yaml` 默认还包含 `caddy`。本文由宿主机 Nginx 占用 `80` 和 `443`，只显式启动 `postgres`、`redis`、`clamav`、`api` 和 `worker`。

### 9. 检查 PasteBox 配置

检查是否还有模板占位符：

```sh
cd /opt/pastebox
grep -nE 'CHANGE_ME|example\.com|<[^>]+>' deploy/production.env
```

正常情况不应有输出。本文中的 `example.com` 必须全部替换成真实域名。

渲染 Compose：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  config --quiet
```

如果提示缺少 `PASTEBOX_IMAGE`、数据库密码或备份变量，回到 `deploy/production.env` 补齐，不要把必填校验从 Compose 中删除。

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
    add_header Strict-Transport-Security "max-age=31536000" always;

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

这里先只对 `pastebox.example.com` 启用 HSTS。只有确认主域名下所有子域名都长期支持 HTTPS，并理解浏览器 preload 清单难以撤销后，才考虑增加 `includeSubDomains` 和 `preload`。

启用配置：

```sh
sudo test ! -L /etc/nginx/sites-enabled/s3o-bootstrap.conf || \
  sudo unlink /etc/nginx/sites-enabled/s3o-bootstrap.conf
sudo test ! -L /etc/nginx/sites-enabled/default || \
  sudo unlink /etc/nginx/sites-enabled/default
sudo ln -sf /etc/nginx/sites-available/pastebox.conf /etc/nginx/sites-enabled/pastebox.conf
sudo nginx -t
sudo systemctl reload nginx
```

如果 `nginx -t` 失败，不要 reload。常见原因：

- Cloudflare Origin Certificate 或 private key 路径错误。
- Let's Encrypt 证书还没有成功签发。
- 临时 `s3o-bootstrap.conf` 没有停用，导致重复 server block。
- `80` 或 `443` 已被 Caddy、Apache 或其他容器占用。

此时先验证已经启动的 S3Orchestrator：

```sh
curl -fsS http://127.0.0.1:19000/health/ready
curl -fsS https://s3o.example.com/health/ready
```

PasteBox 还没有启动，所以 `127.0.0.1:18080` 暂时不通是正常的。启动 PasteBox 后再验证 `https://pastebox.example.com/healthz`。

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

PasteBox 域名走 Cloudflare Proxied 时，上传大小受 Cloudflare account 套餐限制。Cloudflare 当前公开的最大请求体是：

| Cloudflare 套餐 | 最大请求体 |
| --- | ---: |
| Free | 100MB |
| Pro | 100MB |
| Business | 200MB |
| Enterprise | 默认 500MB，可联系 Cloudflare 调整 |

Nginx 的 `client_max_body_size` 设得更大也绕不过 Cloudflare 边缘限制。本文的 PasteBox Nginx 示例使用 `100m`，与 Free/Pro 上限匹配。

`s3o.example.com` 是 DNS only，所以 PasteBox 到 S3Orchestrator 的请求不经过 Cloudflare 代理；它仍受 Nginx `5g`、S3Orchestrator `max_object_size` 和 R2 限制。

如果要支持更大的文件上传，有三个方向：

- 升级 Cloudflare 套餐。
- 上传域名改 DNS only。
- 后续改造为浏览器直传对象存储。

当前本文按服务端代理上传下载处理。

## 防火墙

只开放 SSH、HTTP 和 HTTPS：

```sh
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
sudo ufw status verbose
```

执行 `ufw enable` 前确认当前 SSH 连接有备用入口，避免把自己锁在服务器外。

Docker 发布端口可能绕过部分 UFW 转发规则，因此这里不能只依赖 UFW。关键保护是 Compose 明确绑定：

```text
127.0.0.1:18080:8080
127.0.0.1:19000:9000
```

再次检查：

```sh
sudo ss -lntp | grep -E ':(80|443|18080|19000)\b'
```

`18080`、`19000` 必须只出现在 `127.0.0.1`。不要把 PostgreSQL `5432`、Redis `6379` 或 ClamAV `3310` 发布到公网。

## 启动顺序

下面的顺序用于第一次上线，也适合故障恢复时逐层定位。不要一次性把所有容器拉起来。

### 1. 确认 S3Orchestrator 已就绪

执行目录：`/opt/s3-orchestrator`。

```sh
cd /opt/s3-orchestrator
docker compose up -d s3-orchestrator
docker compose ps
curl -fsS http://127.0.0.1:19000/health/ready
```

`docker compose ps` 应显示 `healthy`，curl 应返回成功。失败时先看：

```sh
docker compose logs --tail=200 s3-orchestrator
```

### 2. 确认 Nginx 和公网 S3 域名

```sh
sudo nginx -t
sudo systemctl reload nginx
curl -fsS https://s3o.example.com/health/ready
```

如果宿主机 HTTP 健康检查成功、HTTPS 域名失败，问题在 DNS、证书、Nginx 或防火墙，不在 R2。

### 3. 拉取 PasteBox 镜像

执行目录：`/opt/pastebox`。

```sh
cd /opt/pastebox
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance \
  pull api worker preflight migrate
```

检查实际镜像：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  images
```

### 4. 启动 PasteBox 基础依赖

先启动 PostgreSQL、Redis 和 ClamAV：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  up -d postgres redis clamav
```

查看状态：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  ps
```

等待 PostgreSQL 和 Redis 变为 `healthy`。ClamAV 首次下载病毒库可能需要几分钟，可以查看：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  logs --tail=200 clamav
```

### 5. 运行生产 preflight

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm preflight
```

preflight 必须通过。它会检查生产域名、HTTPS 对象存储、密钥、数据库、SMTP、OAuth、支付和其他生产约束。不要通过改成 development 或删除变量来绕过。

如果 preflight 的对象存储检查失败，先运行后文“从 PasteBox 容器检查 S3 域名”的命令。

### 6. 执行数据库迁移

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm migrate
```

迁移命令必须以 0 退出。失败时不要启动 API，先备份数据库并处理迁移错误。

### 7. 启动 PasteBox API 和 worker

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  up -d api worker
```

等待 API healthcheck：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  ps
```

运行中的 service 应包含 `api`、`worker`、`postgres`、`redis`、`clamav`，不应包含 `caddy`。

如果看到 `caddy`，停止它：

```sh
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  stop caddy
```

## 验证 S3Orchestrator S3 API

安装 AWS CLI 后，用虚拟 bucket 凭据测试。这里测试的是 S3Orchestrator 暴露给 PasteBox 的虚拟 bucket，不是 R2 backend bucket。

创建单独的 AWS CLI profile：

```sh
aws configure --profile pastebox-s3o
```

输入：

```text
AWS Access Key ID: <BUCKET_ACCESS_KEY>
AWS Secret Access Key: <BUCKET_SECRET_KEY>
Default region name: us-east-1
Default output format: json
```

然后检查虚拟 bucket：

```sh
aws --profile pastebox-s3o \
  --endpoint-url https://s3o.example.com \
  --region us-east-1 \
  s3api head-bucket \
  --bucket pastebox-files
```

上传、下载、删除一个测试对象：

```sh
printf 'pastebox s3 orchestrator smoke\n' > /tmp/pastebox-s3o-smoke.txt

aws --profile pastebox-s3o \
  --endpoint-url https://s3o.example.com \
  --region us-east-1 \
  s3api put-object \
  --bucket pastebox-files \
  --key smoke/pastebox-s3o-smoke.txt \
  --body /tmp/pastebox-s3o-smoke.txt \
  --content-type text/plain

aws --profile pastebox-s3o \
  --endpoint-url https://s3o.example.com \
  --region us-east-1 \
  s3api get-object \
  --bucket pastebox-files \
  --key smoke/pastebox-s3o-smoke.txt \
  /tmp/pastebox-s3o-smoke.out

diff -u /tmp/pastebox-s3o-smoke.txt /tmp/pastebox-s3o-smoke.out

aws --profile pastebox-s3o \
  --endpoint-url https://s3o.example.com \
  --region us-east-1 \
  s3api delete-object \
  --bucket pastebox-files \
  --key smoke/pastebox-s3o-smoke.txt
```

预期：

- `head-bucket` 退出码为 0。
- `put-object` 返回 ETag。
- `get-object` 成功写入输出文件。
- `diff` 没有输出。
- `delete-object` 成功。
- S3Orchestrator 日志出现 `HeadBucket`、`PutObject`、`GetObject` 和 `DeleteObject`。

如果这里失败，先不要启动 PasteBox 业务流量。优先修 S3Orchestrator、R2 凭据、Nginx 和域名。

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
7. 观察 worker 日志，确认对应 cleanup job 已处理。
8. 确认旧下载链接不可用。
9. 到 Cloudflare R2 dashboard 里确认对象落到了某个后端 bucket，并在删除后确认对象按清理流程消失。

## 日志排查

查看 S3Orchestrator 日志，执行目录为 `/opt/s3-orchestrator`：

```sh
cd /opt/s3-orchestrator
docker compose logs -f --tail=200 s3-orchestrator
```

查看 PasteBox API 和 worker 日志，执行目录为 `/opt/pastebox`：

```sh
cd /opt/pastebox
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  logs -f --tail=200 api worker
```

查看 Nginx 日志：

```sh
sudo tail -f /var/log/nginx/access.log /var/log/nginx/error.log
```

正常情况下，S3Orchestrator 日志里应该能看到 `HeadBucket`、`PutObject`、`GetObject`、`DeleteObject` 这类请求。

按现象找日志：

| 现象 | 先看 |
| --- | --- |
| 浏览器打不开 PasteBox | Nginx -> PasteBox API |
| PasteBox `readyz` 的 object storage 失败 | PasteBox API -> Nginx s3o -> S3Orchestrator |
| S3Orchestrator 返回 5xx | S3Orchestrator -> R2 |
| 上传被 413 拒绝 | Cloudflare 限制 -> Nginx `client_max_body_size` -> PasteBox 套餐限制 -> S3Orchestrator `max_object_size` |
| 文件扫描卡住 | worker -> ClamAV |

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
  -f compose.nginx-host.yaml \
  run --rm \
  --entrypoint /bin/sh \
  preflight \
  -c 'grep "$S3O_DOMAIN" /etc/hosts && wget -S -O- "https://$S3O_DOMAIN/health/ready"'
```

`/etc/hosts` 中应出现 `s3o.example.com` 到 host gateway 的映射，`wget` 应完成 TLS 校验并返回 ready 响应。

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

### S3Orchestrator 元数据丢失

`/opt/s3-orchestrator/data/data.db` 是 S3Orchestrator 的对象位置索引。这个文件丢了，即使 R2 bucket 中仍有对象，S3Orchestrator 也可能不知道每个对象位于哪个 backend。

必须一起备份：

```text
/opt/s3-orchestrator/data/
/opt/s3-orchestrator/config.yaml
/opt/s3-orchestrator/.env
/opt/s3-orchestrator/compose.yaml
```

R2 bucket 不是 SQLite 元数据的替代备份，SQLite 元数据也不是 R2 对象内容的替代备份，两边缺一不可。

## 备份与恢复

### 一致性备份 S3Orchestrator

最容易理解的方式是短暂停止 S3Orchestrator，完整备份数据目录。停机期间附件上传、下载和 PasteBox object storage readiness 会失败，应在维护窗口执行。

```sh
sudo install -d -m 700 /var/backups/s3-orchestrator

cd /opt/s3-orchestrator
docker compose stop s3-orchestrator

sudo tar -C /opt/s3-orchestrator \
  -czf "/var/backups/s3-orchestrator/s3o-$(date +%Y%m%d-%H%M%S).tar.gz" \
  compose.yaml config.yaml .env data

docker compose start s3-orchestrator
curl -fsS http://127.0.0.1:19000/health/ready
```

然后：

```sh
sudo chmod 600 /var/backups/s3-orchestrator/s3o-*.tar.gz
sudo ls -lh /var/backups/s3-orchestrator/
```

归档包含 R2 和虚拟 bucket 密钥，必须加密后复制到异地存储，不能只留在同一块宿主机磁盘上。

### 检查备份能否读取

```sh
sudo tar -tzf /var/backups/s3-orchestrator/s3o-<timestamp>.tar.gz
```

列表至少应包含：

```text
compose.yaml
config.yaml
.env
data/data.db
```

安装 `sqlite3` 后可以在隔离目录做完整性检查：

```sh
sudo apt install -y sqlite3
sudo mkdir -p /var/backups/s3-orchestrator/restore-check
sudo tar -xzf \
  /var/backups/s3-orchestrator/s3o-<timestamp>.tar.gz \
  -C /var/backups/s3-orchestrator/restore-check
sudo sqlite3 \
  /var/backups/s3-orchestrator/restore-check/data/data.db \
  'PRAGMA integrity_check;'
```

预期输出 `ok`。这只证明数据库文件结构完整，不等于已经完成业务恢复演练。至少定期在隔离环境启动一次备份副本，并完成 S3 smoke。

### PasteBox 数据库和异地备份

PasteBox 的 PostgreSQL、WAL、Redis 持久化和 off-host restic 备份由 `compose.production.yaml` 中的 maintenance services 管理。S3Orchestrator 的 SQLite 备份不会包含 PasteBox 用户、paste、订单和附件元数据。

至少执行并监控仓库已有的：

```sh
cd /opt/pastebox

docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm postgres-backup

docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm postgres-basebackup

docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm postgres-wal-check

docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm backup-push
```

备份成功不等于可以恢复。至少再完成两种隔离演练：

```sh
# 把最新逻辑备份恢复到临时数据库，校验 schema_migrations 后自动删除
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm postgres-restore-drill

# 用最新 base backup 和 WAL 在容器内临时目录完成 PITR 演练
docker compose --env-file deploy/production.env \
  -f compose.production.yaml \
  -f compose.nginx-host.yaml \
  --profile maintenance run --rm postgres-pitr-drill
```

两条命令都必须以 0 退出。逻辑恢复演练不会覆盖正式数据库；PITR 演练默认使用容器内临时目录，也不会改动正式 PostgreSQL 数据卷。失败时先查看对应命令的完整输出，不要把未验证的备份当成可恢复备份。

附件对象、S3Orchestrator SQLite、PasteBox PostgreSQL 必须使用同一恢复时间点进行演练，否则可能出现数据库引用存在但对象索引或对象内容缺失。

## 扩容和凭据轮换

### 增加一个 R2 backend

1. 在 Cloudflare 创建新 R2 bucket。
2. 创建只访问该 bucket 的 `Object Read & Write` token。
3. 用 AWS CLI 直连完成 head、put、get、delete 测试。
4. 在 `/opt/s3-orchestrator/.env` 添加 `BACKEND4_*`。
5. 在 `/opt/s3-orchestrator/config.yaml` 添加新的 `backends` 项。
6. 保持所有 backend 的 `quota_bytes` 策略一致。
7. 校验并重建容器：

   ```sh
   cd /opt/s3-orchestrator
   docker compose run --rm \
     s3-orchestrator \
     validate -config /etc/s3-orchestrator/config.yaml
   docker compose up -d --force-recreate s3-orchestrator
   docker compose ps
   ```

8. 重新执行 S3Orchestrator 虚拟 bucket smoke 和 PasteBox readiness。

不要为了验证新 backend 而删除旧 backend。删除一个仍持有对象的 backend 前，必须先按照 S3Orchestrator 上游运维文档完成迁移或 rebalance。

### 轮换 R2 backend 凭据

1. 在 Cloudflare 为同一个 bucket 创建新 token。
2. 用 AWS CLI 单独验证新 token。
3. 备份 `/opt/s3-orchestrator/.env`。
4. 把对应 `BACKEND*_ACCESS_KEY` 和 `BACKEND*_SECRET_KEY` 替换为新值。
5. 重建容器并跑 smoke：

   ```sh
   cd /opt/s3-orchestrator
   docker compose up -d --force-recreate s3-orchestrator
   curl -fsS http://127.0.0.1:19000/health/ready
   ```

6. 确认 PasteBox 上传、下载、删除正常。
7. 最后回到 Cloudflare 撤销旧 token。

不要先撤销旧 token 再修改服务器，这会直接造成 backend 不可用。

### 轮换 S3Orchestrator 虚拟 bucket 凭据

S3Orchestrator 的一个虚拟 bucket 可以临时配置多组凭据。低停机轮换流程：

1. 用 OpenSSL 生成新的 Access Key 和 Secret Key。
2. 在 `config.yaml` 的 `pastebox-files.credentials` 中保留旧凭据并新增一组新凭据。
3. 在 `.env` 增加 `BUCKET_ACCESS_KEY_NEW` 和 `BUCKET_SECRET_KEY_NEW`。
4. 校验配置并重建 S3Orchestrator。
5. 把 PasteBox `PASTEBOX_S3_ACCESS_KEY`、`PASTEBOX_S3_SECRET_KEY` 改为新值。
6. 重建 API 和 worker：

   ```sh
   cd /opt/pastebox
   docker compose --env-file deploy/production.env \
     -f compose.production.yaml \
     -f compose.nginx-host.yaml \
     up -d --force-recreate api worker
   ```

7. 确认 readiness 和附件业务正常。
8. 从 S3Orchestrator 配置中删除旧凭据，再次校验并重建。

轮换期间的配置形态：

```yaml
buckets:
  - name: pastebox-files
    credentials:
      - access_key_id: ${BUCKET_ACCESS_KEY}
        secret_access_key: ${BUCKET_SECRET_KEY}
      - access_key_id: ${BUCKET_ACCESS_KEY_NEW}
        secret_access_key: ${BUCKET_SECRET_KEY_NEW}
```

确认 PasteBox 已改用新凭据后，才能删除第一组旧凭据。

## 升级与回滚

### 升级 S3Orchestrator

1. 阅读上游 changelog 和版本迁移说明。
2. 做一致性备份并确认 SQLite 完整性。
3. 拉取目标 tag：

   ```sh
   cd /opt/s3-orchestrator
   git -C source fetch --depth 1 origin tag <new-version-tag>
   git -C source checkout <new-version-tag>
   git -C source rev-parse HEAD
   ```

4. 修改 `compose.yaml` 中的 `VERSION` 和 `image` tag。
5. 先构建新镜像并验证配置：

   ```sh
   docker compose build --pull s3-orchestrator
   docker compose run --rm \
     s3-orchestrator \
     validate -config /etc/s3-orchestrator/config.yaml
   ```

6. 重建并检查：

   ```sh
   docker compose up -d s3-orchestrator
   docker compose ps
   curl -fsS https://s3o.example.com/health/ready
   ```

7. 重新跑虚拟 bucket S3 smoke 和 PasteBox readiness。

回滚时把 `source`、`VERSION` 和 `image` 恢复到旧 tag，再从升级前备份恢复兼容的 SQLite 数据。上游如果做了不可逆数据库迁移，仅回退镜像可能不安全，必须以对应版本迁移说明为准。

### 升级 PasteBox GHCR 镜像

1. 确认目标 commit 的 GitHub Actions `Docker image` workflow 成功。
2. 记录当前 `PASTEBOX_IMAGE`，并完成 PostgreSQL、WAL 和异地备份。
3. 让 `/opt/pastebox` 切换到与新镜像匹配的 release commit。
4. 把 `deploy/production.env` 的 `PASTEBOX_IMAGE` 改为新 `sha-*` tag 或 digest。
5. 拉取、preflight、迁移、重建：

   ```sh
   cd /opt/pastebox

   docker compose --env-file deploy/production.env \
     -f compose.production.yaml \
     -f compose.nginx-host.yaml \
     --profile maintenance \
     pull api worker preflight migrate

   docker compose --env-file deploy/production.env \
     -f compose.production.yaml \
     -f compose.nginx-host.yaml \
     --profile maintenance run --rm preflight

   docker compose --env-file deploy/production.env \
     -f compose.production.yaml \
     -f compose.nginx-host.yaml \
     --profile maintenance run --rm migrate

   docker compose --env-file deploy/production.env \
     -f compose.production.yaml \
     -f compose.nginx-host.yaml \
     up -d --force-recreate api worker
   ```

6. 验证 health、ready、登录、上传、下载、分享和删除。

应用回滚时恢复旧 `PASTEBOX_IMAGE` 和匹配的仓库 commit，然后重建 API/worker。数据库迁移是否可回滚必须查看目标版本说明；不要在不确认数据库兼容性的情况下只换旧镜像。

## 上线前检查清单

- [ ] `pastebox.example.com` 在 Cloudflare 是 Proxied。
- [ ] `s3o.example.com` 是 DNS only。
- [ ] Cloudflare SSL/TLS 是 Full strict，不是 Flexible。
- [ ] PasteBox Origin Certificate 和 S3Orchestrator Let's Encrypt 证书路径正确。
- [ ] `certbot renew --dry-run` 通过。
- [ ] Nginx `pastebox.example.com` 反代到 `127.0.0.1:18080`。
- [ ] Nginx `s3o.example.com` 反代到 `127.0.0.1:19000`。
- [ ] 宿主机没有公开 `18080`、`19000`、PostgreSQL、Redis、ClamAV 端口。
- [ ] `/opt/s3-orchestrator/.env` 权限是 `600`，没有提交到 Git。
- [ ] 每个 R2 token 只有对应 bucket 的 Object Read & Write 权限。
- [ ] 所有 R2 backend 都通过 AWS CLI 直连测试。
- [ ] R2 backend 凭据和 PasteBox 虚拟 bucket 凭据不是同一组。
- [ ] S3Orchestrator 使用固定 `v0.62.28` 镜像，不使用 `latest`。
- [ ] PasteBox 使用固定 `sha-*` tag 或 digest，不使用 `latest`。
- [ ] `PASTEBOX_S3_ENDPOINT=https://s3o.example.com`。
- [ ] `PASTEBOX_S3_BUCKET=pastebox-files`。
- [ ] `PASTEBOX_S3_REGION=us-east-1`。
- [ ] `PASTEBOX_S3_USE_PATH_STYLE=true`。
- [ ] PasteBox 容器内 `s3o.example.com` 映射到 host gateway。
- [ ] `preflight` 和 `migrate` 通过。
- [ ] `/readyz` 和 `/api/v1/ready` 通过。
- [ ] AWS CLI 对 S3Orchestrator 的 head/put/get/delete 通过。
- [ ] PasteBox UI 上传、下载、分享下载、删除通过。
- [ ] `caddy` 没有运行。
- [ ] `/opt/s3-orchestrator/data/data.db` 已做一致性异地备份。
- [ ] PasteBox PostgreSQL、WAL 和 restic 异地备份已验证。
- [ ] 已记录 S3Orchestrator 和 PasteBox 的回滚版本。

## 官方资料

本文在 2026-08-05 核对过以下资料。正式部署和升级前建议重新检查：

- [Cloudflare R2 创建 bucket](https://developers.cloudflare.com/r2/buckets/create-buckets/)
- [Cloudflare R2 创建 S3 API 凭据](https://developers.cloudflare.com/r2/api/tokens/)
- [Cloudflare R2 S3 API 兼容性和 region](https://developers.cloudflare.com/r2/api/s3/api/)
- [Cloudflare R2 数据位置和 jurisdiction](https://developers.cloudflare.com/r2/reference/data-location/)
- [Cloudflare R2 定价](https://developers.cloudflare.com/r2/pricing/)
- [Cloudflare 请求体大小限制](https://developers.cloudflare.com/workers/platform/limits/#request-limits)
- [GitHub Container Registry 使用说明](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [S3Orchestrator 上游仓库](https://github.com/afreidah/s3-orchestrator)
- [S3Orchestrator 部署说明](https://github.com/afreidah/s3-orchestrator/blob/main/docs/deployment.md)
- [S3Orchestrator 配置参考](https://github.com/afreidah/s3-orchestrator/blob/main/docs/configuration.md)
- [S3Orchestrator 灾难恢复](https://github.com/afreidah/s3-orchestrator/blob/main/docs/disaster-recovery.md)
