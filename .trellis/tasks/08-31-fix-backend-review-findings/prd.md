# 修复后端全面审查问题

## Goal

逐项修复 2026-08-31 后端全面审查发现的安全、数据一致性、并发、性能、可维护性和测试问题，在不破坏现有 HTTP API 与部署方式的前提下，使 API/Worker 多实例运行时具备可靠的一致性和可扩展性。

## What I already know

* 项目是 Go + PostgreSQL + S3/MinIO + ClamAV 架构，生产环境 API 与 Worker 是独立进程。
* 当前 `gofmt`、`go vet`、普通单测、竞态测试、PostgreSQL 集成测试、依赖校验和构建均通过。
* 默认后端语句覆盖率为 57.3%，`govulncheck` 当前未安装。
* 用户要求逐项修复全部审查问题，而不是只修前三项。
* 当前工作区已有与本任务无关的 Trellis、文档和静态资源改动，实施时不得覆盖或清理。

## Confirmed Decisions

* 保持现有 HTTP 路由、请求和响应字段向后兼容。
* 允许增加向后兼容的 PostgreSQL 迁移和内部 Store/Service 接口。
* 分享密码旧 SHA-256 哈希保留兼容读取，并在成功验证后升级到 Argon2id。
* 优先解决安全与数据一致性，再处理性能和文件拆分，避免边修业务边大规模搬代码。
* 所有数据库迁移都需要可回滚或具备明确回滚方案。

## Open Questions

* 无。用户已确认稳妥方案。

## Requirements (evolving)

### P0 安全与数据一致性

* 注销和全部注销必须可靠持久化；失败时不得返回虚假成功。
* 验证令牌必须通过单条条件更新原子消费；密码重置相关写入必须事务化。
* 密码重置接口不得泄露账户是否存在或是否已验证。
* 分享密码改用带随机盐的 Argon2id，并兼容旧哈希渐进升级。
* 分享访问、下载次数及每日下载流量必须由 PostgreSQL 原子校验和递增。
* 对象引用计数必须以 PostgreSQL 为事实源，上传和清理不得因跨进程竞争删除仍被引用的对象。

### P1 并发与性能

* 用户粘贴列表不得刷新全库缓存或重写对象引用，必须使用用户范围查询与分页。
* 病毒扫描必须流式读取对象，不得把最大 2 GiB 文件完整载入内存。
* Argon2、数据库、S3、邮件和扫描网络 I/O 不得持有全局 Service 锁。
* 上传不得使用覆盖所有对象的进程级全局写锁，改为数据库/对象键级协调。
* 启动与后台列表不得无边界加载全部业务数据。
* 请求上下文必须传递到数据库、对象存储和外部服务边界。

### P2 防护与可维护性

* JSON 解码必须严格限制请求体大小并拒绝尾随 JSON/垃圾数据。
* 登录失败防护不得只依赖邮箱硬锁，降低恶意锁号风险。
* 生产会话 Cookie 必须固定启用 Secure；转发头只能来自可信代理或不参与该决策。
* JSON 响应编码失败不得在状态码写出后追加 `http.Error`。
* 注册、OAuth、订单等多实体写入使用事务或事务 Outbox，避免半完成状态。
* 将超大核心文件按 auth/content/share/billing/admin/runtime 等职责渐进拆分，不整片重写。

### P3 测试与验证

* 为令牌并发消费、注销持久化失败、分享计数、对象引用清理、并发上传和大文件流式扫描增加测试。
* 默认测试覆盖率提升到至少 75%，关键安全与事务模块优先达到更高覆盖率。
* 修复完成后通过格式、静态检查、普通测试、竞态测试、PostgreSQL 集成测试、构建及生产就绪检查。

## Implementation Phases

1. **认证与输入安全**：注销错误、令牌原子消费、密码重置防枚举、分享密码 Argon2id、严格 JSON/Cookie。
2. **数据库一致性**：分享原子计数、对象引用事务、注册/OAuth/订单事务与 Outbox。
3. **并发与流式 I/O**：扫描流式化、上传锁细化、移除读路径全库刷新、传递 Context。
4. **分页与架构整理**：后台分页、启动缓存收敛、按领域拆分大文件。
5. **测试补齐与最终门禁**：并发/故障注入测试、覆盖率、race、PostgreSQL、构建与生产检查。

每个阶段完成后单独运行对应测试并复查行为，再进入下一阶段；不把全部重构堆到最后一次验证。

## Acceptance Criteria (evolving)

* [ ] 注销存储失败时接口返回错误，旧会话不会被当作已撤销。
* [ ] 同一验证令牌并发消费时只有一个请求成功。
* [ ] 密码重置对存在和不存在账户返回相同的公开响应。
* [ ] 新分享密码使用 Argon2id，旧 SHA-256 分享仍可验证并升级。
* [ ] 多连接并发访问不会突破分享访问/下载/流量限制。
* [ ] API 上传与 Worker 清理并发时，不会删除仍有引用的对象。
* [ ] 2 GiB 上限文件的扫描路径保持流式，内存占用不随文件大小线性增长。
* [ ] 用户列表和后台列表有数据库分页，不再触发全库缓存刷新。
* [ ] 慢登录、慢 S3 上传不会串行阻塞无关请求。
* [ ] 请求取消能够终止相关数据库或对象存储操作。
* [ ] JSON 超限及尾随内容被正确拒绝。
* [ ] 生产环境会话 Cookie 始终带 `Secure`。
* [ ] 核心文件按职责缩小，外部 HTTP API 保持兼容。
* [ ] 默认后端语句覆盖率达到至少 75%。
* [ ] 所有项目质量门禁通过。

## Definition of Done (team quality bar)

* Tests added/updated (unit/integration where appropriate)
* Lint / typecheck / CI green
* Docs/notes updated if behavior changes
* Rollout/rollback considered if risky
* 数据迁移、旧分享密码兼容和多实例部署均有验证证据

## Out of Scope (explicit)

* 不改前端页面设计和交互结构。
* 不改变现有套餐额度、支付价格和业务规则。
* 不清理当前工作区中与本任务无关的文件。
* 不在本任务中更换 PostgreSQL、S3、ClamAV 或 HTTP 框架。

## Technical Notes

* 重点文件：`internal/app/app.go`、`internal/app/attachment_streams.go`、`internal/httpserver/server.go`、`internal/postgres/*`、`internal/scanner/*`、`internal/worker/*`。
* 现有 `ListPastesByUser`、`StreamingObjectStore.OpenObject`、业务事务与 advisory lock 模式应优先复用。
* 数据库规范要求参数化 SQL、跨实体业务事务、额度校验失败关闭。
* 日志应继续使用结构化 `slog`，不得记录邮箱、令牌、密码或密钥。
