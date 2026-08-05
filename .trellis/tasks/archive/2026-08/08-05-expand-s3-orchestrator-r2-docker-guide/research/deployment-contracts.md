# Deployment contracts research

核对日期：2026-08-05。

## Cloudflare R2

官方资料：

* https://developers.cloudflare.com/r2/buckets/create-buckets/
* https://developers.cloudflare.com/r2/api/tokens/
* https://developers.cloudflare.com/r2/api/s3/api/
* https://developers.cloudflare.com/r2/reference/data-location/
* https://developers.cloudflare.com/r2/pricing/

结论：

* R2 bucket 默认不是公开 bucket，本部署无需开启公开访问或 R2 自定义域名。
* Cloudflare 当前要求先开通/购买 R2，才能创建 R2 S3 API token。
* R2 控制台路径为 `R2 object storage -> Overview -> Account Details -> API Tokens -> Manage`。
* 长期运行的服务器推荐使用 Account API token；选择 `Object Read & Write` 并限制到指定 bucket，避免使用可管理 bucket 的 Admin 权限。
* Access Key ID 和 Secret Access Key 创建后需要立即保存，Secret Access Key 后续不能重新查看。
* 普通 bucket endpoint 为 `https://<ACCOUNT_ID>.r2.cloudflarestorage.com`，region 为 `auto`。
* EU jurisdiction bucket 使用 `https://<ACCOUNT_ID>.eu.r2.cloudflarestorage.com`，不能和默认 jurisdiction endpoint 混用。
* R2 实现 `HeadBucket`、`PutObject`、`GetObject` 和 `DeleteObject`，足够作为 S3Orchestrator backend。
* R2 的 included usage 按账户用量统计；同一账户增加 bucket 不代表每个 bucket 都获得一份新的免费额度。

## S3Orchestrator

官方资料：

* https://github.com/afreidah/s3-orchestrator
* https://raw.githubusercontent.com/afreidah/s3-orchestrator/main/README.md
* https://raw.githubusercontent.com/afreidah/s3-orchestrator/main/docs/user-guide.md
* `.trellis/tasks/06-09-s3-orchestrator-streaming-transfer/research/s3-orchestrator-compatibility.md`

结论：

* 2026-08-05 使用 `git ls-remote --tags --refs --sort=-v:refname` 核对，上游最新正式 tag 为 `v0.62.28`，commit 为 `cd9a7eacc143ed0f9cd03bc38f5d65c28c8cdbaa`。
* 上游仓库包含 Dockerfile，可以固定 tag 后本地构建，避免依赖来源和更新策略不明确的第三方镜像。
* `v0.62.28` Dockerfile 使用 UID `10001`，暴露 `9000`，内置 `/health/ready` healthcheck，并把 `VERSION` 写入 OCI label。
* 配置加载器会展开进程环境变量，因此挂载的 `config.yaml` 可以使用 `${VAR}`，由 Compose `env_file` 注入真实值。
* S3Orchestrator 的 bucket 由服务器配置，不由客户端动态创建。虚拟 bucket 凭据控制 PasteBox 能访问哪个虚拟 bucket。
* PasteBox 已通过本地 PoC 验证 `HeadBucket`、`PutObject`、`GetObject`、`DeleteObject` 和 path-style SigV4 调用。
* SQLite 数据库保存对象位置元数据。丢失数据库后，即使 R2 中仍有对象，S3Orchestrator 也可能无法定位它们，因此数据库、配置和密钥必须一起备份。
* `replication.factor=1` 适合聚合容量；提高副本数会降低可用容量并增加写请求。

## GitHub Container Registry

官方资料：

* https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry

仓库资料：

* `.github/workflows/docker-image.yml`
* `compose.production.yaml`
* `deploy/production.env.example`

结论：

* 当前工作流发布 `latest`、分支、语义化 tag 和 `sha-` 前缀镜像标签。
* `compose.production.yaml` 要求 `PASTEBOX_IMAGE` 指向固定的 `ghcr.io/cvinit/pastebox:sha-*` tag 或 digest。
* 公开 GHCR package 可以匿名拉取；私有 package 需要 personal access token classic，并至少有 `read:packages` 权限。
* GitHub 官方建议需要完全固定镜像时使用 digest。
* 服务器仍需取得仓库内 `compose.production.yaml`、环境变量模板和 deploy 辅助文件，但 PasteBox 应从 GHCR 拉取，不应在服务器本地构建。

## Same-host topology

* PasteBox 生产 preflight 拒绝本地 HTTP 对象存储 endpoint，因此不能把生产配置写成 `http://s3-orchestrator:9000`。
* 推荐两套独立 Compose project：S3Orchestrator 绑定 `127.0.0.1:19000`，PasteBox API 绑定 `127.0.0.1:18080`。
* 宿主机 Nginx 为 `pastebox.example.com` 和 `s3o.example.com` 终止 TLS。
* PasteBox 的 `api`、`worker`、`preflight` 添加 `s3o.example.com:host-gateway`，使容器使用真实域名、正确证书和 SNI 访问宿主机 Nginx，避免公网 DNS 回环问题。
* `pastebox.example.com` 面向浏览器，可使用 Cloudflare Proxied；`s3o.example.com` 是 S3 API endpoint，推荐 DNS only，减少代理缓存、WAF、请求体和 SigV4 兼容风险。

## Verification performed

* 用假凭据渲染独立 S3Orchestrator Compose，`docker compose config --quiet` 通过。
* 把文档中的 PasteBox override 与当前 `compose.production.yaml`、`deploy/production.env.example` 合并渲染，`docker compose config --quiet` 通过。
* 用上游 `v0.62.28` 实际二进制执行 `validate -config`，文档中的 S3Orchestrator YAML 通过验证。
* 文档 Markdown 代码围栏成对，`git diff --check` 通过，未发现旧部署目录和旧 Compose 文件名残留。
