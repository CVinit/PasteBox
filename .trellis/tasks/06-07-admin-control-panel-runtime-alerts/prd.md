# 管理员后台控制、运行资源面板与 Telegram 告警

## 目标

为 PasteBox 增加一个更完整的管理员后台控制台，用于管理套餐、价格、用户限额、兑换码、外部 provider 配置状态、人工处理队列、运行资源指标，并在资源占用或关键队列异常时通过 Telegram Bot 通知管理员，降低生产运维对数据库直改和人工巡检的依赖。

## 我已经知道

- 用户希望后台覆盖：订阅套餐内容与价格、邮件发件配置、Google 登录配置、Turnstile 配置、套餐兑换码生成、未注册用户/免费用户/无套餐付费用户的空间与上传大小限制、需人工处理文件扫描、CPU/内存/硬盘/对象存储占用指标、Telegram Bot 高资源占用通知。
- 仓库已有 admin API 和前端 admin 入口，但目前偏“操作列表/队列/审计”，不是系统配置中心。
- 套餐与价格已有数据库表和 catalog store，适合作为可编辑配置的第一批对象。
- SMTP、Google OAuth、S3、支付、metrics 等配置当前由环境变量加载；直接改成后台热更新会影响 secret 管理、重启语义和生产安全边界。
- `/metrics` 和 Prometheus 规范已存在，当前缺少主机资源和对象存储占用指标。
- 附件扫描失败、邮件失败、reports、failed jobs 已有队列暴露，可扩展为“需人工处理”工作台。
- `smart-search` CLI 已可用并完成复核：`doctor` 通过 standard profile；Exa/Tavily/Context7 可用，Zhipu 返回 404 warning 但不影响本次官方文档检索。

## 研究参考

- [`research/repo-context.md`](research/repo-context.md) - 仓库现有后台、配置、套餐、指标和扫描能力。
- [`research/runtime-config-alerting.md`](research/runtime-config-alerting.md) - 运行时配置、敏感字段、Turnstile、Prometheus、Telegram Bot 告警惯例，以及 `smart-search` evidence 文件路径。

## 临时假设

- 后台只允许 admin 访问，所有修改动作必须写 audit log。
- 不在告警、日志、指标和前端响应中暴露密钥、邮箱、粘贴正文、对象 key、分享 token。
- 对象存储占用优先使用数据库元数据聚合，不在普通请求中遍历 bucket。
- 兑换码属于运营发放工具，支持已有套餐/固定时长和批次级规则，不做金额抵扣、百分比折扣、渠道追踪或邀请返利。

## 需求（演进中）

- 管理员可以查看并编辑套餐内容和价格，包括可见性、购买开关、周期、金额、币种、配额限制。
- 管理员可以配置或查看未注册/游客、免费、付费用户的空间、单文件、单次粘贴、每日上传和分享下载限额。
- 系统支持未注册游客创建短期文本和文件粘贴，但必须由后台开关控制是否开放；生产默认关闭，管理员显式开启后才对外可用。
- 管理员可以生成套餐兑换码批次，并查看使用状态；批次支持套餐、时长、数量、过期时间、每用户限制、总兑换次数、指定邮箱/域名、备注和禁用；用户可以兑换有效代码获得套餐权益。
- 管理员可以查看邮件、Google OAuth、Turnstile、Telegram Bot 配置状态，并执行安全的测试动作；这些 provider secret 在 MVP 中仍由环境变量/部署 secret 管理，后台不保存密钥。
- 管理员可以查看需人工处理的扫描失败、恶意/冻结附件、failed jobs、failed mails 和 open reports。
- 管理员可以查看运行环境 CPU、内存、磁盘、对象存储占用等指标。
- 系统通过应用内 worker 在资源占用或关键队列超过阈值时直接发送 Telegram Bot 通知，并支持冷却/去重、失败记录和后台测试发送。

## 待决问题

- 已解决。

## 扩展思考

### 未来演进

- 配置中心可能会演进为“版本化配置 + 回滚 + 变更审批 + 多管理员角色权限”。
- 兑换码可能会演进为营销活动、渠道追踪、邀请返利、批量导入导出。

### 相关场景

- Admin UI、typed API client、HTTP handler tests 和 service tests 必须同步更新，避免前后端契约漂移。
- 生产部署仍需要 `make test-web` 后同步 `web/dist` 到 `internal/httpserver/static`，再走 Go/Docker 验证。

### 失败与边界

- 配置错误不能让已有登录/session/支付路径直接崩溃；provider 测试失败应返回结构化错误。
- Telegram 告警失败不能阻塞主业务；需要冷却、失败记录和手动测试发送。
- Turnstile token 必须后端校验，不能只依赖前端 widget。
- 敏感配置必须脱敏展示，不能进入 audit metadata、metrics、日志或浏览器本地状态。

## 可行方案

### 方案 A：混合配置中心（推荐）

非敏感业务配置入库并热生效，敏感 provider secret 仍由 env/secret-store 管理；后台显示配置状态、非敏感字段和测试结果。适合作为 MVP，符合现有架构，安全风险最低。

### 方案 B：全量 DB 配置中心

所有配置都由后台保存并热生效，敏感字段使用 master key 加密。控制能力最强，但需要实现字段级加密、密钥轮换、配置版本、回滚和更严格审计，MVP 成本高。

### 方案 C：只读运维面板

只展示配置状态、资源指标、人工处理队列和告警测试，不做配置写入。最快但不能满足“后台控制”的核心目标。

## 决策记录（ADR-lite）

### 敏感配置策略

**Context**: 邮件 SMTP 密码、Google client secret、Turnstile secret、Telegram bot token 等敏感配置若由后台保存并热生效，需要额外实现字段级加密、master key、密钥轮换、回滚、审计脱敏和备份恢复策略。当前项目已经以 `internal/config` 加载 `PASTEBOX_` 环境变量为配置入口，官方调研也支持将外部服务凭据作为部署配置处理。

**Decision**: MVP 采用混合配置中心。非敏感业务配置入库并热生效；敏感 provider secret 保持环境变量/部署 secret 为权威来源。后台可以显示配置状态、非敏感字段、测试连接结果和缺失提示，但不保存 SMTP 密码、Google client secret、Turnstile secret、Telegram bot token。

**Consequences**: MVP 风险和实施范围可控，避免在本任务中引入加密配置系统。代价是管理员不能完全通过后台替换 provider 密钥；这部分需要通过部署配置变更完成，后续可单独演进为加密 DB 配置中心。

### 游客上传能力

**Context**: 未注册游客上传能降低试用门槛，但会引入匿名身份、滥用治理、Turnstile、防刷限额、扫描、清理和投诉处理压力。当前产品核心路径以登录用户为主，直接默认开放游客文件上传风险较高。

**Decision**: MVP 支持游客短期文本和文件上传，但必须有后台开关控制是否开放。生产默认关闭；管理员开启后，游客走独立限额、强制短保留期、Turnstile 校验、IP/设备级限流、扫描队列和自动清理。

**Consequences**: 保留完整的增长入口，同时让运营可以在风险可控时开启。实现范围增加：需要匿名访问标识、游客配额模型、游客内容归属、清理策略、下载/分享限制，以及后台可见的游客内容和人工处理入口。

### 兑换码范围

**Context**: 套餐兑换码需要覆盖实际运营发放，但完整营销系统会显著扩大范围。当前后台目标是让管理员能批量创建、暂停和审计兑换码，同时让用户自助兑换套餐权益。

**Decision**: MVP 采用“固定套餐/固定时长 + 批次规则”。每个批次指定套餐、权益时长、生成数量、批次过期时间、总兑换次数、每用户兑换限制、可选指定邮箱/域名、备注和启用/禁用状态。每个兑换码默认一次性使用；兑换成功写用户计划变更和 audit。

**Consequences**: 覆盖常见运营赠送、内测、客服补偿和指定用户/域名投放场景。暂不做金额抵扣、百分比折扣、渠道追踪、邀请返利、复杂叠加规则或批量导入导出。

### Telegram 告警链路

**Context**: 仓库已有 `/metrics` 和 Prometheus 规范，但本任务明确要求接入 Telegram Bot 并在资源占用高时发送通知。Prometheus + Alertmanager 是更标准的生产告警链路，但会引入额外部署和配置复杂度。

**Decision**: MVP 使用应用内 worker 直接发送 Telegram 告警。应用采集运行资源和关键队列指标，读取后台配置的阈值、开关和冷却时间，超过阈值时调用 Telegram Bot API。后台提供测试发送、最近发送结果、失败原因和冷却状态。Prometheus 指标继续保留为外部监控扩展点。

**Consequences**: 部署简单，能满足单机/轻量生产的直接通知需求。代价是告警聚合、静默、排班和复杂路由能力不如 Alertmanager；这些作为后续演进，不进入 MVP。

## 技术方案

- 新增 DB-backed 系统配置 store，覆盖非敏感业务配置：套餐/价格/限额、游客上传开关与游客限额、兑换码批次规则、资源告警阈值、Telegram 告警非敏感参数。
- 保持敏感 provider secret 由环境变量/部署 secret 管理：SMTP 密码、Google client secret、Turnstile secret、Telegram bot token 不入库，后台只显示配置状态和测试结果。
- 扩展套餐 catalog 写路径，使公开 plan/pricing、quota、上传限制和后台编辑使用同一份持久化配置。
- 新增游客内容模型和匿名访问标识；游客上传默认关闭，开启后必须按游客限额、Turnstile、IP/设备限流、扫描和短保留期执行。
- 新增兑换码批次、兑换码、兑换记录模型；兑换成功变更用户套餐并写 audit。
- 扩展人工处理工作台：聚合 scan failures、malicious/frozen attachments、failed jobs、failed mails、open reports 和游客内容风险项。
- 新增 runtime metrics collector：采集 CPU、内存、磁盘，结合 `object_refs` 或 attachment metadata 聚合对象存储占用；避免在请求路径实时遍历 bucket。
- 新增 alert worker：定期评估资源阈值和关键队列阈值，执行冷却/去重，发送 Telegram，并记录最近告警状态。
- 前端将现有单页 admin 聚合视图拆成更清晰的配置、兑换码、人工处理、运行资源、审计/队列区域，并保持 typed API client 与后端 JSON contract 同步。

## 验收标准（演进中）

- [ ] 非 admin 调用新增 admin API 返回 `403 admin_required`。
- [ ] Admin 修改业务配置会持久化、热生效并写 audit log。
- [ ] 套餐/价格/限额修改后，公开 pricing、quota 和创建/上传限制使用同一份配置。
- [ ] 后台可以开启/关闭游客上传；关闭时游客创建文本和文件都被拒绝，开启时按游客限额、Turnstile、扫描和保留期规则执行。
- [ ] 兑换码生成、兑换、重复兑换、过期、禁用批次、超出批次/用户次数限制、邮箱/域名不匹配、无效码都有 service 和 HTTP 测试。
- [ ] 人工处理队列能列出扫描失败/恶意/冻结附件，并支持重试或冻结/解除冻结。
- [ ] 资源面板展示 CPU、内存、磁盘、对象存储占用；对象存储占用不依赖请求路径 bucket 全量扫描。
- [ ] 应用内 alert worker 能按阈值发送 Telegram 告警；配置测试、发送失败、冷却/去重、最近状态都有 service 和 HTTP 测试。
- [ ] Turnstile 后端 Siteverify 校验有成功、失败、超时/重复 token 行为测试。
- [ ] 前端 typed client、admin UI 和后端 JSON contract 一致。
- [ ] `make test-web` 和 `make test` 通过；如改动前端构建产物，`web/dist` 已同步到 `internal/httpserver/static`。

## 实施计划（小 PR 切分）

- PR1: 配置中心基础设施。新增系统配置表/store/service、admin config API、audit、基础 UI，先覆盖套餐/价格/限额和 provider 状态展示。
- PR2: 游客上传。新增游客开关、匿名标识、游客限额、Turnstile 校验接入、游客文本/文件上传、短保留期和清理。
- PR3: 兑换码。新增兑换码批次/代码/兑换记录模型、生成接口、用户兑换接口、限制校验、后台列表和审计。
- PR4: 人工处理与资源面板。扩展 admin workbench，加入扫描/冻结/失败队列、CPU/内存/磁盘/对象存储占用。
- PR5: Telegram 告警。新增 Telegram sender、alert worker、阈值配置、冷却/去重、测试发送和最近告警状态。
- PR6: 前端整合与质量门。完善 admin UI 信息架构、响应式布局、typed client、测试、`make test-web`、`make test`、静态资源同步。

## Definition of Done

- Tests added/updated: Go service tests、HTTP handler tests、Postgres migration/store tests、frontend build/typecheck where appropriate.
- Lint/typecheck/CI green through repo-local verification commands.
- Docs/notes updated if runtime configuration, deployment env vars, or operational runbooks change.
- Rollout/rollback considered for migrations and provider config changes.

## 暂定不做范围

- 多管理员细粒度 RBAC 和审批流。
- 金额抵扣、百分比折扣、复杂营销系统、邀请返利、渠道归因、兑换码批量导入导出。
- 在 MVP 中实现完整 secret rotation、KMS 集成或后台保存 provider secret。
- Prometheus Alertmanager 告警路由、排班、静默和复杂通知聚合；本任务只接入应用内后台和 Telegram 直接通知。

## 技术笔记

- 新增持久化表优先用新 migration，不改历史 migration。
- 新配置 store 应和 `CatalogStore`、`OperationalStores` 的接口风格一致，内存实现用于 tests。
- Admin mutation 必须在 service 层做权限校验和 audit。
- 指标应使用聚合字段，不把用户 ID、邮箱、对象 key、分享 token 放入 Prometheus label。
- 对象存储用量可以先从 `object_refs.size_bytes` 聚合；如未来要对接 provider bucket quota，需要放入后台异步采样任务。
