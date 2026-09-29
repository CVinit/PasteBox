# 工单 #6 验证记录：多文件队列与失败重试

对应 Issue：https://github.com/CVinit/PasteBox/issues/6
分支：`main`（本地提交，未推送）

## 已提交的自动化证据

```
go test ./cmd/... ./internal/... -count=1        # 通过
go test ./cmd/... ./internal/... -race           # 通过
make test-postgres                               # 通过（临时 PostgreSQL 17 容器）
npm --prefix web run typecheck                   # 通过
npm --prefix web run build                       # 通过
gofmt -l cmd internal                            # 无输出
go vet ./...                                     # 无输出
```

新增 HTTP 集成测试（`internal/httpserver/transfers_multifile_test.go`）：

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestGuestMultiFileTransferPartialFailureRetryAndPublish` | 部分失败后只重试失败项、已成功项不重传、清单未完成不发布、恢复后逐个下载 |
| `TestTransferUploadNamesTheLimitThatApplied` | 超配额错误命名实际生效的限制（`paste_too_large` 413、`daily_upload_limit` 403、`storage_limit` 403） |
| `TestTransferIncompleteManifestStaysPrivate` | 未完成清单不泄露凭据、他人不可见、发布后清单冻结 |
| `TestTransferConcurrentItemUploadKeepsOneAttachment` | 并发上传同一项只绑定一个附件，接收页只有一份文件 |
| `TestTransferPublishRaceReturnsOneShare` | 并发发布返回同一份分享凭据 |
| `TestAuthedMultiFileTransferGroupsRecordsBySend` | 一次发送 = 一条记录（含重名文件），传输记录按批次归组 |
| `TestTransferItemCountLimitRejectsOversizedSends` | 超出套餐文件数上限在创建阶段拒绝 |

发布竞争的数据库层证据沿用 `internal/postgres/transfers_test.go`（`FOR UPDATE` + 双连接并发发布只产生一条 share）。

## 浏览器验证（本轮执行，脚本未入库）

脚本：`tmp/verify/browser.cjs`（需本机 Playwright；`tmp/` 不纳入版本控制）。
运行环境：本地 PostgreSQL/Redis/MinIO 容器 + `pastebox api` + `pastebox worker` + 内嵌静态资源，地址 `http://127.0.0.1:18080`。

结果：33/33 项通过，覆盖：

- 游客选择 3 个文件 → 逐文件真实进度 → 单一分享链接；接收页列出 3 个文件并逐个下载到各自字节。
- 中途注入一次上传失败（`route.abort`）→ 只有失败项重试，已成功项仅上传一次，恢复后生成链接。
- 注入一次发布失败 → 两个文件保持已上传，出现「重试这次发送」，重试不重传任何文件即生成链接。
- 登录工作区发送 3 个文件（含两个同名 `report.txt`）→ 一条记录、文件清单可区分重名、传输记录一条。
- 375px 视口下队列与发送按钮可用。
- 拖拽 2 个文件进入发送区 → 进入队列并生成同一链接。
- 发送中/已完成后 drop zone 标记为不可用，取消或「再发一次」后恢复可用。

截图：`tmp/verify/queue-staged.png`、`queue-sent.png`、`queue-mobile.png`。

## 已知边界

- 浏览器验证为人工执行的脚本，未纳入 CI；仓库当前没有浏览器测试运行器，与本项目既有做法一致（HTTP 集成测试为主要入口）。
- 游客「图片 / 文本」模式仍是单文件单条记录，多文件队列只覆盖文件模式；图片/文本模式的兼容属 Issue #9。
- 同一项上传的并发去重依赖进程内对象锁；单实例部署（`compose.production.yaml` 只有单个 `api` 服务）下成立。多实例同时上传同一项时，发布只会绑定一个附件，落败请求写入的附件行需由后续对象引用清理处理——属已知限制，未在本轮改动。
- 「发布」阶段的失败重试已通过 `retrySend` 入口补齐；`create` 阶段失败同样走该入口。
