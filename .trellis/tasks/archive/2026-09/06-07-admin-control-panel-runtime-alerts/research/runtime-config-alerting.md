# 运行时配置与告警调研

## 调研结论

- `smart-search doctor --format json` 已通过 standard profile：main search、docs search、web fetch 可用。Zhipu provider 返回 HTTP 404 warning，但 Exa/Tavily/Context7 可用，足以支撑本次官方文档复核。
- 当前项目已经遵循环境变量配置模型，`internal/config` 从 `PASTEBOX_` 环境变量加载 SMTP、Google OAuth、S3、支付、指标令牌等配置。对于 SMTP 密码、Google OAuth secret、Turnstile secret、Telegram bot token 这类敏感配置，继续保留 env/secret-store 作为默认来源最符合现有架构。
- 如果要让后台直接保存敏感密钥并热生效，需要新增持久化配置表、字段级加密、密钥轮换策略、审计日志、配置版本和回滚语义。否则后台只能安全地显示“已配置/未配置”和提供测试按钮。
- 非敏感业务配置适合放到数据库并通过后台热更新，例如套餐内容、价格可见性、购买开关、游客/免费/付费用户的限额、兑换码批次规则和人工处理队列策略。
- 告警应尽量少而可操作。仓库已有 `/metrics`、Prometheus 配置和告警规范，适合继续把资源占用指标暴露为聚合指标，再由一个内部告警 worker 发送 Telegram 通知，避免在请求路径中发送通知。
- Telegram Bot 官方 API 是 HTTP-based interface，要求通过 HTTPS 请求 Bot API；`sendMessage` 支持向目标 chat 发送文本消息。因此告警最小实现可以只使用 HTTPS POST 调用 Bot API，配置项为 bot token、chat ID、启用开关、静默开关和冷却时间。消息内容应避免用户邮箱、粘贴正文、对象 key、下载链接和密钥。
- Turnstile 不应只做前端 widget。后端必须调用 Cloudflare Siteverify 校验 token；token 有时效和单次使用语义，因此后台配置要明确“哪些动作启用 Turnstile”和“验证失败如何处理”。
- Cloudflare Turnstile 官方文档明确要求后端调用 Siteverify；token 有 300 秒有效期、单次使用、最大长度 2048 字符，Siteverify 支持 `application/x-www-form-urlencoded` 和 `application/json` 请求并返回 JSON。
- Prometheus 官方告警实践强调少量、可操作、面向症状的告警；告警规则支持 `for` 和 `keep_firing_for` 来降低瞬时抖动和恢复抖动。Prometheus 自身不是完整通知方案，Alertmanager 承担汇总、限速、静默和通知分发。
- 12-factor 官方配置章节把外部服务凭据归入部署间变化的 config，并建议存储在环境变量中。这强化了“敏感密钥默认由 env/secret-store 管理，后台只管理非敏感业务配置”的推荐方向。

## 可行方案

### 方案 A：混合配置中心（推荐）

- 非敏感业务配置入库并热生效：套餐、价格、配额、兑换码、资源阈值、告警开关、人工处理队列策略。
- 敏感 provider secret 保持环境变量/部署 secret 为权威来源；后台只显示配置状态、非敏感字段和连接测试结果。
- 优点：符合现有 `internal/config` 和 12-factor 配置模型，风险低，MVP 可控。
- 缺点：不能完全在后台改 SMTP 密码、Google secret、Turnstile secret、Telegram token。

### 方案 B：全量 DB 配置中心

- 所有配置都可在后台保存并热生效，敏感字段用环境变量中的 master key 加密。
- 优点：后台控制最完整。
- 缺点：需要加密、轮换、回滚、审计、脱敏、备份恢复策略，实施和验证成本明显更高。

### 方案 C：只读配置状态页

- 后台只展示当前配置状态和资源指标，不允许修改配置。
- 优点：最快、风险最低。
- 缺点：不能满足“控制后台”核心诉求，只能作为临时运维面板。

## 参考来源

- Telegram Bot API: `https://core.telegram.org/bots/API`
- Cloudflare Turnstile server-side validation: `https://developers.cloudflare.com/turnstile/get-started/server-side-validation/`
- Prometheus alerting practices: `https://prometheus.io/docs/practices/alerting/`
- Prometheus alerting rules: `https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/`
- Twelve-Factor config: `https://www.12factor.net/config`

## Smart Search Evidence

- `smart-search doctor --format json` - 通过；main search、docs search、web fetch 可用；Zhipu 为 warning，不作为本次依据。
- `smart-search exa-search "Telegram Bot API sendMessage official documentation" --num-results 5 --include-highlights --format json --output research/evidence/telegram-sendmessage-search.json`
- `smart-search fetch "https://core.telegram.org/bots/api#sendmessage" --format json --output research/evidence/telegram-sendmessage-fetch.json` - Tavily 未能抓取正文，保留失败证据。
- `smart-search exa-search "sendMessage chat_id text parse_mode disable_notification Telegram Bot API" --include-domains core.telegram.org --num-results 5 --include-text --include-highlights --format json --output research/evidence/telegram-sendmessage-exa-text.json` - 用作 Telegram 官方正文证据。
- `smart-search exa-search "Cloudflare Turnstile server side validation official documentation" --num-results 5 --include-highlights --format json --output research/evidence/turnstile-siteverify-search.json`
- `smart-search fetch "https://developers.cloudflare.com/turnstile/get-started/server-side-validation/" --format json --output research/evidence/turnstile-siteverify-fetch.json`
- `smart-search exa-search "Prometheus alerting rules best practices official documentation" --num-results 5 --include-highlights --format json --output research/evidence/prometheus-alerting-search.json`
- `smart-search fetch "https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/" --format json --output research/evidence/prometheus-alerting-rules-fetch.json`
- `smart-search fetch "https://prometheus.io/docs/practices/alerting/" --format json --output research/evidence/prometheus-alerting-practices-fetch.json`
- `smart-search exa-search "The Twelve-Factor App config official documentation environment variables" --num-results 5 --include-highlights --format json --output research/evidence/twelve-factor-config-search.json`
- `smart-search fetch "https://12factor.net/config" --format json --output research/evidence/twelve-factor-config-fetch.json`
