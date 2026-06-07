# 仓库上下文记录

## 现有能力

- 后端已有 `/api/v1/admin` 路由，覆盖 dashboard、users、pastes、attachments、shares、orders、webhook-events、audit-logs、queues、reports、billing reconcile、cleanup、manual mark-paid。
- Admin 权限在 service 层通过 `requireAdminLocked` 控制，HTTP 层通过登录 session 取用户后调用 service 方法，非 admin 应返回 `admin_required`。
- 现有前端 `web/src/App.tsx` 只有一个聚合式 admin 视图，入口由 `user.role === "admin"` 控制。
- 套餐和价格已有 `plans`、`prices` 表，并由 `postgres.CatalogStore` 加载到 `app.Service.catalog`。
- 默认套餐仍在 `internal/plans/plans.go`，测试和本地内存服务会使用默认 catalog。
- 配置当前来自 `internal/config/config.go` 的环境变量，包括 SMTP、Google OAuth、S3、Stripe、Epusdt、metrics token、scanner、bootstrap admin。
- 资源/运维指标已有 `/metrics` 和 `app.Service.OperationalMetrics()`，目前覆盖业务聚合指标、队列深度、邮件失败等，不覆盖 CPU/内存/磁盘/对象存储桶占用。
- 对象存储接口当前只有 Put/Get/Delete/Health；对象存储空间占用可优先从 `object_refs` 或 attachments 元数据聚合，不建议在请求路径实时遍历 bucket。
- 邮件队列、报告、扫描失败、失败任务已在 admin queues 中暴露，适合扩展成“需人工处理”工作台。

## 主要缺口

- 缺少可编辑的系统配置 store/API/UI。
- 缺少兑换码模型、生成批次、兑换接口、兑换审计和防重复使用逻辑。
- 缺少未注册/游客用户模型及其空间/上传限制的明确产品定义。
- 缺少运行环境 CPU、内存、磁盘指标采集和对象存储占用面板。
- 缺少 Telegram Bot 通知 sender、告警规则、冷却/去重状态和测试发送接口。
- 缺少 Turnstile 配置、前端 widget、后端 Siteverify 校验和动作级开关。

## 相关文件

- `internal/config/config.go`
- `internal/plans/plans.go`
- `internal/postgres/catalog.go`
- `internal/postgres/migrations/000001_initial_schema.sql`
- `internal/app/app.go`
- `internal/app/operational_stores.go`
- `internal/app/content_stores.go`
- `internal/objectstore/s3.go`
- `internal/httpserver/server.go`
- `internal/httpserver/server_test.go`
- `web/src/api.ts`
- `web/src/App.tsx`
- `.trellis/spec/backend/quality-guidelines.md`
