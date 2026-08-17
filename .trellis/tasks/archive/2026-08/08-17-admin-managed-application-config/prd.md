# 将应用配置迁移到管理员后台

## Goal

大幅精简生产环境文件。应用启动后才能决定的站点、业务和第三方服务配置统一由管理员后台维护；只有数据库、配置解密主密钥和 Compose 自身启动所需的基础设施参数留在部署层。

## Confirmed Decisions

- 使用“启动根配置 + 后台加密配置”的边界。
- S3、SMTP、OAuth、Turnstile、Telegram、支付等敏感配置允许在后台修改。
- 敏感值加密存入 PostgreSQL，后台只返回是否已配置，不返回明文。
- API 和 Worker 使用数据库中的有效配置，修改后无需整体重启。
- `pastebox admin create` 直接连接数据库创建管理员，不再输出引导环境变量。
- 旧环境变量仅在数据库尚无应用配置时做一次性兼容导入，避免已有部署升级后丢配置。
- Compose、数据库、配置解密主密钥、监控、备份等启动前配置继续留在部署层。

## Requirements

### 启动根配置

- 应用运行环境、数据库连接、Redis 地址和新增加的配置加密主密钥由环境变量提供。
- CSRF 签名密钥从配置加密主密钥稳定派生，移除独立的生产 CSRF 环境变量。
- 指标令牌继续由部署层提供，因为 Prometheus 在应用启动前需要同一令牌。
- HTTP 监听地址和超时保留代码默认值，可选覆盖，但不再作为生产模板必填项。
- Compose 镜像、域名、证书邮箱、数据库密码、监控和备份凭据属于部署参数，不进入管理员后台。

### 后台配置

- 站点：名称、公开 URL、支持邮箱、滥用邮箱、CORS 来源。
- 安全与业务：日志级别、注册策略、访客上传、限流、Worker 心跳阈值。
- 存储与扫描：S3 全部连接信息、路径风格、扫描提供方、ClamAV 地址与超时。
- 邮件：提供方、SMTP 主机、端口、用户名、密码、发件信息和 TLS 模式。
- 身份验证：Google、GitHub OAuth；Cloudflare Turnstile。
- 通知：Telegram Bot 和 Chat ID。
- 支付：Stripe、Epusdt 的开关、密钥和业务参数。
- 每个第三方配置区提供测试能力；失败时保留旧的有效配置。

### 密钥安全

- 新环境变量 `PASTEBOX_CONFIG_ENCRYPTION_KEY` 必须是 Base64 编码的 32 字节随机值。
- 使用 AES-256-GCM；每个密钥值使用独立随机 nonce，并以配置键名作为 AAD。
- 数据库存储密文、nonce 和版本，不存明文。
- 管理 API 的密钥字段采用三态语义：省略表示保留，空字符串表示清除，非空表示替换。
- 响应、日志、审计记录和错误信息不得包含密钥明文。

### 运行时一致性

- 单进程内后台保存后立即生效。
- API 和 Worker 定时检查数据库配置版本，以支持多进程同步。
- 动态客户端更新必须并发安全；正在执行的请求可完成，后续请求使用新配置。
- S3、SMTP、扫描、OAuth、Turnstile、Telegram 和支付逻辑读取有效运行时配置。
- 数据库或解密失败时继续使用最后一次有效配置并记录脱敏错误，不用空配置覆盖。

### 升级和部署

- 当数据库没有应用配置时，从旧环境变量构建一次初始配置并持久化；后续以数据库为准。
- 精简 `.env.example` 和 `deploy/production.env.example`，清楚区分应用根配置与 Compose 参数。
- 更新生产预检：先验证启动根配置，再连接数据库验证后台配置完整性。
- 更新部署文档，说明首次管理员创建、配置加密密钥生成、后台配置顺序及回滚方法。

## Acceptance Criteria

- [x] 缺少数据库连接或无效加密主密钥时，生产启动和预检给出明确错误。
- [x] 数据库中看不到第三方密钥明文，管理 API 也不返回明文。
- [x] 管理员可保存、保留、替换、清除并测试第三方配置。
- [x] 修改站点、限流、OAuth、Turnstile、邮件、S3、扫描、通知或支付配置后，新请求使用新值。
- [x] API 和 Worker 能读取同一份配置并在限定时间内同步。
- [x] 全新部署只需填写精简后的部署模板即可启动，然后从后台补齐应用配置。
- [x] 旧环境变量部署首次升级后自动导入，删除旧变量并重启仍保持配置。
- [x] `pastebox admin create` 将管理员写入 PostgreSQL。
- [x] Go 测试、vet、前端检查和生产 Compose/预检检查通过。

## Out of Scope

- 在后台修改数据库地址、Redis 地址、容器镜像、域名证书、数据库密码、监控或备份容器凭据。
- 在后台直接重启或重建 Docker Compose 服务。
- 提供已保存密钥的明文查看或导出功能。

## Technical Notes

- 当前非敏感运行配置保存在 `system_configs`，入口位于 `internal/app/runtime_control.go` 与 `internal/postgres/runtime_controls.go`。
- 当前第三方配置由 `internal/config/config.go` 从环境变量加载，并在 API/Worker 启动时创建客户端。
- 当前管理员页已具备运行配置和 provider 状态，可在现有页面增设配置分组。
- 详细现状见 `research/current-config-architecture.md`。

## Definition of Done

- 相关单元/集成测试更新并通过。
- Go 格式化、测试、vet、前端 `npm run check` 通过。
- 生产 Compose 模板可渲染，预检覆盖新的配置边界。
- 部署文档和回滚说明更新。
