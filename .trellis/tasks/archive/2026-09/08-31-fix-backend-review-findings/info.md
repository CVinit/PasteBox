# 进度记录

## 2026-09-24 状态盘点（接手未提交改动）

工作区中 9 月 17–20 日的未提交改动（`internal/app`、`internal/postgres`、
`internal/httpserver`、`internal/scanner`、`internal/worker`，约 +4290/-473 行）
属于本任务，此前未在任务目录留下进度记录。本次盘点结果：

### 门禁现状

| 检查 | 结果 |
|------|------|
| gofmt / go build / go vet | 通过 |
| go test ./... | 通过 |
| go test -race ./internal/... | 通过 |
| PostgreSQL 集成测试（scripts/check-postgres-integration.sh） | 通过 |
| 合并语句覆盖率（无 DB） | 57.4%（app 70.5%，httpserver 71.8%，postgres 1.8%） |
| govulncheck | 未运行（安装在 ~/go/bin，不在 PATH） |
| production-readiness | 未运行 |

### 已完成（有代码 + 测试证据）

- P0：注销持久化失败返回错误；令牌 `ConsumeAuthToken` 原子消费；密码重置防枚举；
  分享密码 Argon2id + 旧哈希升级；分享访问/下载 `ConsumeShareVisit`/`ConsumeShareDownload`
  PostgreSQL 原子计数；对象引用 `WithObjectRefLock` + `Increment/DecrementObjectRef`。
- P1：`ListPastesByUserPage(WithOptions)` 用户范围分页；`ScanStream` 流式扫描；
  登录/OAuth/会话持久化不持 Service 锁；对象键级锁替代全局写锁；
  `loadBoundedContentCaches` 有界启动加载；部分方法已有 `*WithContext` 变体。
- P2：`decodeLimited` 严格 JSON；生产 Cookie 固定 Secure；`writeJSON` 编码失败不重复写；
  注册/OAuth/密码重置事务化（`BusinessTransactionStore`）。
- P3：上述各项均有单测；集成测试覆盖事务提交/回滚。

### 未完成

1. **P1 Context 传递**：HTTP 层仍有约 60 个 Service 方法调用不带 ctx
   （`AccessShare`、`CreatePaste`、`GetPaste`、`RunCleanup`、`Admin*` 等），
   `internal/app/app.go` 内仍有 112 处 `context.Background()`。
2. **P2 登录失败防护**：仍是按邮箱 5 次/15 分钟硬锁（`recordLoginFailureLocked`），
   未降低恶意锁号风险。
3. **P2 文件拆分**：未开始，`app.go` 从 5067 行增长到 6246 行。
4. **P3 覆盖率**：总覆盖率 57.4%，未达 75%。无 DB 时 postgres 包 1681 条语句几乎为 0，
   数学上无 DB 最高约 84%，需把 PostgreSQL 集成测试纳入测量口径。
5. **P3 最终门禁**：govulncheck、production-readiness 未跑。

### 与本任务无关的工作区改动（不清理）

- `internal/httpserver/static/index.html` 指向新构建产物 `assets/index-DYJsOGyx.js`（未跟踪）。
- 未跟踪：`.claude/`、`.spec-workflow/`、`docs/object-storage-research.zh-CN.md`、
  两张 png 截图、6 月的旧 assets。


## 2026-09-25 完成记录

- 已完成 P1/P2 收尾：HTTP/Worker 到持久化边界传递 context；慢数据库、邮件、
  对象存储和扫描操作释放 Service 锁；登录限制按邮箱与可信 IP 隔离并使用数据库
  原子计数；app.go 按职责拆分。
- 补齐启动/后台分页、数据库聚合统计、到期订单分批对账。导出和删号按用户读取
  全部持久化记录，1002 条内容回归测试证明不依赖 1000 条启动缓存。
- 清理与上传统一锁顺序，多 Worker 清理互斥；并发读取在所有查询完成后一起发布
  内容和附件缓存。旧记录回写不降低原子计数或恢复已撤销分享。
- 新账号和已有账号 OAuth 写入均使用事务；新增失败回滚、多连接上限、请求取消、
  SMTP/ClamAV 取消与 2 GiB 流式扫描测试。
- 新增 make test-coverage：临时 PostgreSQL，项目所有 Go 包跨包合并统计，门槛 75%。
  项目把 GOPATH 放在仓库 .cache 内，因此覆盖包限定 pastebox/...，避免把缓存中的
  第三方依赖误算成项目代码。最终门禁输出 75.1%。
- 自检：gofmt、git diff --check、go vet、全量测试、真实 PostgreSQL 集成与竞态、
  make test-coverage、前端类型/构建、完整 production-readiness 和 Docker 构建通过。
- govulncheck：可达漏洞 0；仍报告 4 个导入包及 4 个依赖模块漏洞，但调用链不触达。
  已升级 x/text 到 v0.39.0（同时 x/sync v0.21.0），修复可达无限循环漏洞。
- 无新增迁移；保留旧方法封装和旧分享密码兼容。回滚使用本任务前的提交，数据库
  schema 无需回滚。没有部署或推送，支付/真实外部服务验收仍按现有上线手册执行。
- 后续任务：按根目录 TODO 的 7 项处理前端设置、会话和交互问题。
