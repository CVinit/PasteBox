# 工单 #5 验证记录：单文件发送与链接接收闭环

对应 Issue：https://github.com/CVinit/PasteBox/issues/5
分支：`main`
阻塞前置：无（该票最先开始）

## 这份记录是回溯补写的

#5 当时没有留下验证记录（任务目录里只有 `verification-issue-06..12.md`），本文件补齐这一空缺。
原始运行没有留下可引用的日志，所以下面每一项都是**现在对当前代码重跑**的结果，
不是对当时运行的回忆；`tmp/verify/browser.cjs` 这个文件名当时属于本票，后来被 #6 的脚本覆盖，
因此浏览器部分也用新写的 `browser-issue05.cjs` 重新执行了一遍。

## 已提交的自动化证据

```
go test ./cmd/... ./internal/... -count=1        # 通过
go test ./cmd/... ./internal/... -race           # 通过
make test-postgres                               # 通过（临时 PostgreSQL 17 容器）
npm --prefix web run typecheck                   # 通过
npm --prefix web run build                       # 通过
node scripts/check-web-launch-surfaces.mjs       # 通过
gofmt -l cmd internal                            # 无输出
go vet ./...                                     # 无输出
```

本票相关的用例重跑结果：

```
go test ./internal/app/ -run 'TestCreateTransfer|TestTransferItemUpload|TestPublishTransfer|TestCancelTransfer' -v
  TestCreateTransferIsIdempotentByKey                 PASS
  TestCreateTransferValidatesDeclaredItems            PASS
  TestTransferItemUploadRetryReusesAttachment         PASS
  TestPublishTransferRequiresEveryItemAndCleanScan    PASS
  TestPublishTransferRetriesPickupCodeCollision       PASS
  TestCancelTransferReleasesQuotaAndRejectsPublished  PASS

go test ./internal/httpserver/ -run 'TestTransfer|TestGuestTransfer|TestLegacyPasteShare' -v
  TestTransferSendPublishAndDownloadHTTPContract      PASS
  TestTransferSendLeavesExistingRecordsUntouched      PASS
  TestTransferRejectsForeignOwnersAndIncompleteItems  PASS
  TestTransferCancelReleasesQuotaAndRejectsPublished  PASS
  TestGuestTransferPublishAndDownloadHTTPContract     PASS
  TestGuestTransferCancelReleasesQuota                PASS
  TestLegacyPasteShareDownloadStillWorksAlongsideTransfers PASS
```

## 测试与本票验收条件的对应

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestTransferSendPublishAndDownloadHTTPContract` | 登录用户创建 → 逐项上传 → 发布 → 分享链接下载；成功页所需的链接与有效期来自同一份传输视图 |
| `TestGuestTransferPublishAndDownloadHTTPContract` | 游客走同一形状的入口，配额、扫描与访问控制一致 |
| `TestTransferSendLeavesExistingRecordsUntouched` | 上传不会误附到当前选中的历史记录 |
| `TestLegacyPasteShareDownloadStillWorksAlongsideTransfers` | 老分享链接继续可用，不被传输改造破坏 |
| `TestCreateTransferIsIdempotentByKey` | 创建重试幂等：同一 idempotency key 不产生第二条传输或第二条记录 |
| `TestCreateTransferValidatesDeclaredItems` | 清单校验（条数上限、重复 item、计划附件上限） |
| `TestTransferItemUploadRetryReusesAttachment` | 上传重试复用已存附件，不重复占用对象与配额 |
| `TestPublishTransferRequiresEveryItemAndCleanScan` | 服务端在发布时核对归属、完整性与扫描状态，未完成不对外开放 |
| `TestPublishTransferRetriesPickupCodeCollision` | 发布重试不重复生成分享凭据 |
| `TestTransferRejectsForeignOwnersAndIncompleteItems` | 他人传输与不完整清单被拒 |
| `TestCancelTransferReleasesQuotaAndRejectsPublished` / `TestGuestTransferCancelReleasesQuota` | 放弃上传有清理与配额恢复路径；已发布不能被取消 |
| `TestTransferStorePersistsAndPublishesOnce` / `TestTransferStorePublishesTextOnlyTransfer`（`internal/postgres`，真实 PostgreSQL） | 持久化跨重启一致；发布只发生一次 |

## 浏览器验证（本次重跑，脚本未入库）

脚本：`tmp/verify/browser-issue05.cjs`（需本机 Playwright；`tmp/` 不纳入版本控制）
运行环境：本地 PostgreSQL 17 与 MinIO 容器 + 内嵌静态资源的 `pastebox api` + `pastebox worker --poll-interval 2s`，地址 `http://127.0.0.1:18080`。

结果：**9/9 项通过**。

- 首屏：游客工作台默认「文件」模式，发送表单与取件入口都在首屏，没有被注册墙挡在后面。
- 成功页：给出可复制的分享链接、取件码与有效期。
- 接收：打开链接先列出文件清单（此时不扣名额），领取后下载得到 200 与正确的 10 字节。
- 独立归属：登录用户先选中一条历史记录再发送文件，文件进入新的记录（卡片显示「beta.txt / 1 个附件」），被选中的那条记录既没有文件名也没有附件。
- 登录用户能在记录列表里找到该次发送。
- 移动端 375px：发送表单不横向溢出（`scrollWidth - innerWidth = 0`），发布后同样能领取并下载（200，9 字节）。

## 关键实现决定

（摘自当时提交 `97546c3` 的说明，代码未在本次补写中改动。）

- 新增 `transfers` / `transfer_items` 持久化（迁移 000010）与内存等价实现；发布在数据库事务内锁定传输行，重试不会重复生成分享凭据。
- 新增 `/api/v1/transfers` 与 `/api/v1/guest/transfers`：创建、逐项上传、发布、取消；上传复用现有配额、扫描与对象引用计数。
- 首页默认文件发送，主入口为发文件 / 取文件；成功页展示可复制链接与有效期。
- 发送文件不再附加到当前选中的历史记录，另设显式「附加到当前记录」入口。
- 接收页在扫描未完成时重读分享状态（仅不限访问次数的分享，避免消耗次数）。

## 已知边界

- **本记录是回溯补写的**：原始运行没有留下日志或脚本，#5 的浏览器闭环是重新执行的，不能当作当时运行的证据。
- **`tmp/verify/browser.cjs` 被 #6 覆盖**：同一目录里的脚本按票命名，早期脚本没有保留，这也是 #6 之后每票改用 `browser-issueNN.cjs` 命名的原因。
- **浏览器验收为人工执行脚本，未纳入 CI**：仓库当前没有浏览器测试运行器，与后续各票一致（HTTP 集成测试为主要入口）。
- **`internal/httpserver/static/` 只同步构建引用的文件**：与 #10、#11、#12 的处理一致。
