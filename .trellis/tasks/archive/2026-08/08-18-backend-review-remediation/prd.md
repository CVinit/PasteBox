# 修复后端审查发现的安全与一致性问题

## Goal

修复 2026-08-18 全项目 Backend Code Review 中确认的 6 个必须修复问题，重点保证资金状态一致、后台密钥不因目标地址变化而泄漏、异步任务不会被并发重复领取、上传在写入磁盘前完成权限与额度校验、生产反向代理下能识别真实客户端 IP，以及后台配置保存、审计与热更新不会出现分裂状态。

## What I already know

* 用户已在审查报告后回复“要”，确认按报告中的建议修复全部 6 个问题。
* 当前生产入口是 Caddy `reverse_proxy api:8080`，API 目前只使用 `RemoteAddr`。
* 任务和邮件当前通过“查询可运行记录 → 执行副作用 → 更新状态”处理，没有原子领取。
* 附件当前先写入最多 5 GiB 的临时文件，再校验 Paste、游客令牌和套餐额度。
* 支付、兑换码、审计、Webhook 与邮件落库分散在多个独立语句中。
* 管理配置允许地址变化时保留旧密钥，且配置保存、审计、热更新不是同一成功边界。

## Requirements

1. **资金状态事务化**
   * 兑换码占用、批次计数、兑换记录、用户套餐更新必须在一个 PostgreSQL 事务中完成。
   * 同一兑换码必须使用数据库条件更新或行锁保证只能成功一次，支持多 API 实例并发。
   * 支付成功时，用户套餐、订单、审计、Webhook 幂等事件和付款通知入队必须形成一致的事务边界。
   * 事务失败时不得提前修改长期存活的内存缓存。

2. **敏感密钥与目标地址绑定**
   * Turnstile 与 Telegram 在生产环境使用固定官方目标，不接受管理员任意重定向。
   * S3、SMTP 等必须支持自定义目标的服务，在目标主机、端口或协议发生变化时，必须同时重新提交对应密钥。
   * URL 校验必须拒绝凭据型 URL、危险重定向和不符合生产要求的协议。
   * HTTP 客户端不得通过环境代理发送敏感请求，并限制重定向行为。

3. **异步队列原子领取**
   * Job 和 Mail 使用数据库原子领取，多个 Worker 不得同时取得同一记录。
   * 领取记录包含 Worker 标识和租约截止时间；租约过期后允许恢复。
   * 完成、重试和失败更新必须校验领取者，避免旧 Worker 覆盖新领取结果。

4. **上传前置校验与限流**
   * 读取文件内容前校验用户/游客身份、Paste 所有权、功能开关和可用套餐额度。
   * 文件读取上限使用当前请求的实际可用额度，而不是固定 5 GiB。
   * 游客上传令牌和 Turnstile 令牌必须能在读取文件前取得；保留明确的兼容错误提示。
   * 最终落库前继续执行二次校验，防止并发额度变化。

5. **可信代理客户端 IP**
   * 增加长期部署级可信代理 CIDR 配置，并在生产示例中配置 Caddy/本机反代网段。
   * 仅当直连地址属于可信代理时解析转发头，按代理链取得真实客户端 IP。
   * 限流、Turnstile 和安全审计统一使用同一解析结果。
   * 不可信直连请求伪造转发头时仍使用 `RemoteAddr`。

6. **管理配置原子保存与热更新**
   * 管理配置和对应审计记录必须原子持久化。
   * 保存接口不得出现“返回失败但配置已经落库”的结果。
   * 运行时应用失败必须可见，不能只写日志后返回成功；旧客户端或明确的不可用状态必须与持久化状态一致。
   * 增加审计失败、运行时应用失败和刷新恢复测试。

## Acceptance Criteria

* [ ] 两个并发兑换请求只有一个成功，数据库和内存中的兑换码、批次和用户套餐一致。
* [ ] 资金事务任一步注入失败后，订单、套餐、事件、通知和审计均不存在部分提交。
* [ ] 修改敏感服务目标但不重新提交密钥时返回明确错误，旧密钥不会发往新目标。
* [ ] 两个 Worker 并发领取时 Job/Mail 集合不重叠，租约过期任务可恢复。
* [ ] 无效游客令牌和超套餐文件在大文件写入临时目录前被拒绝。
* [ ] Caddy/可信代理场景下不同客户端使用不同 IP 桶，伪造转发头不能绕过。
* [ ] 管理配置保存和审计同成同败，热更新错误会返回给管理员并有可测试状态。
* [ ] `gofmt -l cmd internal` 无输出。
* [ ] `go test ./...`、`go vet ./...`、`go test -race ./...` 全部通过。
* [ ] `make test-postgres` 通过，并覆盖新增事务、领取和并发场景。
* [ ] `git diff --check -- cmd internal go.mod go.sum deploy` 通过。

## Definition of Done

* 相关单元测试和 PostgreSQL 集成测试已新增或更新。
* 后端格式、单测、vet、race 和 PostgreSQL 集成检查通过。
* 部署示例和必要配置说明同步更新。
* 不提交或覆盖任务开始前已有的无关脏文件。
* 回滚时可通过回退新增迁移和应用版本恢复旧行为。

## Technical Approach

* 在 `app` 层定义面向业务结果的事务接口，由 `postgres` 层通过同一 `pgx.Tx` 实现；Service 只在事务成功后刷新缓存。
* Job/Mail 通过 `FOR UPDATE SKIP LOCKED` 选取并在同一 SQL/事务中标记领取，更新时携带 `claimed_by`。
* 上传拆为“权限和上限预检 → 限长流式暂存 → 最终二次校验”。游客凭据通过请求头或 Cookie 在文件前传递。
* 客户端 IP 解析使用显式可信 CIDR，不直接信任 `X-Forwarded-For`。
* 管理配置保存使用配置存储与审计的组合事务；敏感目标变化执行密钥重新绑定检查。

## Decision (ADR-lite)

**Context**: 这些问题横跨 HTTP、Service、PostgreSQL、Worker 和部署层，仅靠进程内互斥锁或调用顺序不能保证多实例和失败场景正确。

**Decision**: 将并发和一致性保证下沉到 PostgreSQL；网络信任边界由部署级静态配置控制；Service 在持久化成功后才发布内存状态。

**Consequences**: 需要新增数据库迁移和少量接口调整，但能得到可验证的原子性、租约恢复和多实例行为。部署者需要配置可信代理 CIDR。

## Implementation Plan

1. PR1：可信代理 IP、敏感目标绑定及上传前置校验。
2. PR2：Job/Mail 原子领取、租约和并发集成测试。
3. PR3：兑换码与支付事务化、管理配置原子保存和失败注入测试。
4. PR4：完整质量检查、部署说明和回归修正。

## Out of Scope

* 本轮不拆分 `internal/app/app.go` 或 `internal/httpserver/server.go` 大文件。
* 本轮不重做全量缓存和全局锁架构。
* 本轮不实现 Redis 分布式限流，只修复真实客户端 IP 信任边界。
* SMTP 在“发送成功但进程在落状态前崩溃”的严格 exactly-once 语义不在本轮保证范围；本轮保证不会被多个 Worker 并发领取。

## Technical Notes

* 重点文件：`internal/app/app.go`、`internal/app/redemptions.go`、`internal/app/runtime_control.go`、`internal/httpserver/server.go`、`internal/postgres/operational_state.go`、`internal/postgres/runtime_controls.go`、`internal/worker/worker.go`、`cmd/pastebox/main.go`。
* 生产入口：`deploy/caddy/Caddyfile`、`compose.production.yaml`。
* 现有质量门：`make test-api`、`make test-postgres`、`go vet ./...`、`go test -race ./...`。
* 任务开始前已有脏文件必须保持不变并排除在提交之外。
