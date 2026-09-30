# 工单 #10 验证记录：匿名领取名额与领取会话

对应 Issue：https://github.com/CVinit/PasteBox/issues/10
分支：`main`（本地提交，未推送）
阻塞前置：#6、#8、#9（均已在本分支完成）

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

本轮 PostgreSQL 用例**未跳过**：`make test-postgres` 与定向 `-run 'TestTransferStoreClaim' -v` 均在真实 PostgreSQL 17 容器上执行并通过（见下）。

## 新增 HTTP 测试（`internal/httpserver/transfers_claim_test.go`）

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestTransferClaimQuotaAndSessionCoverTheBatch` | 预览与状态查询不占名额；未领取时下载被拒（403 `claim_required`）；领取扣 1 个名额；同一会话内逐个下载与重试不重复扣；同 operationId 重试返回同一 claim；第二个接收者用掉最后一个名额；第三个领取 410 `claim_quota_exhausted` 但两个已授权会话仍可下载；发送者视图读到 `claimedCount=2`；主动完成只结束该会话（410 `claim_ended`）且不退款 |
| `TestTransferClaimSessionExpiresAndNeverOutlivesTheShare` | 可控时钟：会话 = min(领取时刻+30 分钟, 分享有效期)；29 分钟后仍可下载，31 分钟后 410 `claim_ended` 且同 operationId 无法复活；有效期短于 30 分钟的分享把会话收敛到分享到期时刻 |
| `TestTransferTextClaimIsBoundedAndReplayable` | 文本领取返回正文；同 operationId 在 2 分钟窗口内重放同一正文、不重复扣名额；窗口过后重放 410，名额不退还，页面正文不可读 |
| `TestTransferClaimEnforcesShareCredentialsAndIsolatesSessions` | 密码错误/缺失不能领取且不占名额；缺 operationId 400 `invalid_claim_operation`；需登录分享拒绝匿名领取、接受已登录领取；A 分享的 claim 不能下载 B 分享（403 `claim_required`） |
| `TestTransferClaimQuotaDefaultsAndBounds` | 未设置时为 1 个名额；`-1` 与 `>100` 返回 400 `invalid_claim_quota` |
| `TestGuestTransferClaimUsesTheSameQuotaContract` | 游客传输沿用同一名额契约（默认与上限、领完 410） |
| `TestTransferClaimQuotaIsAtomicUnderConcurrentClaims` | 12 个并发领取对 3 个名额：恰好 3 个 200，其余 410，不多发 |
| `TestTransferMixedSendWithholdsItsTextUntilClaimed` | 文本+文件混合传输在未领取时正文为空；领取后正文可读（回归：混合传输不能因 `kind=file` 零成本泄露正文） |
| `TestPlanCatalogPublishesTheClaimBound` | 公开目录发布 `transfers.maxClaimQuota`，前端据此收敛输入而不是复制数字 |

既有工单测试同步更新：`transfers_test.go`、`transfers_multifile_test.go`、`transfers_text_test.go` 中的接收流程改为「先领取再下载」，文本接收改为「打开页面不返回正文、领取后返回正文」。

## 新增 PostgreSQL 集成测试（`internal/postgres/transfer_claims_test.go`）

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestTransferStoreClaimQuotaIsAtomicAcrossConnections` | 两个连接池共 12 个并发领取对 3 个名额：恰好 3 个创建成功，其余 `ErrTransferClaimQuotaExhausted`；`transfers.claimed_count=3`、`transfer_claims` 恰好 3 行；`TransferByShareID` 可从另一连接命中 |
| `TestTransferStoreClaimRetryIsIdempotentAcrossConnections` | 同一 operationId 在两个连接并发领取：只创建 1 条 claim、只扣 1 个名额，两个调用都返回同一条 claim |

## 浏览器验证（本轮执行，脚本未入库）

脚本：`tmp/verify/browser-issue10.cjs`（需本机 Playwright；`tmp/` 不纳入版本控制）
运行环境：本地 PostgreSQL 17 与 MinIO 容器（随机端口）+ 内嵌静态资源的 `pastebox api` + `pastebox worker --poll-interval 2s`，地址 `http://127.0.0.1:18080`。

结果：21/21 项通过。用三个互相独立的浏览器上下文模拟三个接收者，验证的是真实 cookie 隔离而不是同一页面内的状态。

发送方与设置：

- 发送设置里新增「可领取次数」，默认 1；填写 2 后成功页显示「可领取 2 次」。
- 设置分组下的说明是「次数是匿名名额，不等于不同人数。」（本票要求的匿名口径），没有接收人名单输入。
- 移动端 375px：发送设置不撑宽页面（`scrollWidth - innerWidth = 0`），可领取次数字段在视口宽度内。

接收方（接收者 1）：

- 打开链接只显示清单与「剩余可领取 2 次」，没有下载链接，也没有正文。
- 点击「领取文件」后出现「领取会话有效期至 …」与下载链接；剩余变为 1。
- 同一下载在同一会话内连续两次都返回 200/10 字节，剩余仍是 1（不重复扣名额）。
- 刷新页面后仍显示已领取与同一到期时刻（服务端 cookie 决定，不依赖页面内存）。

名额耗尽与权限隔离：

- 接收者 2 领取最后一个名额后剩余为 0；接收者 3 打开同一链接看到「名额已领完，无法再领取。」且没有领取按钮与下载链接。
- 接收者 1 点击「完成领取」后其下载返回 410，接收者 2 的下载仍为 200（一个会话结束不影响另一个）。
- 文本传输：未领取时页面无正文，点击「查看内容」后读到正文；下一位接收者看到「名额已领完」。

## 关键实现决定

- **名额与领取会话是新持久化，不套用旧计数**：`transfers` 增加 `claim_quota`/`claimed_count`，新增 `transfer_claims` 表；旧分享的 `max_visits`/`max_downloads` 语义不变。`claim_quota` 默认 1，与发送默认一致。
- **原子分配在 store 里，判定在服务层**：`AllocateTransferClaim` 在事务里 `SELECT … FOR UPDATE` 锁传输行，再查 operationId 幂等、检查剩余名额、插入 claim、`claimed_count + 1`，一次提交。跨连接与跨实例都走同一把行锁，因此不会超领；服务层只负责窗口长度与错误映射。
- **领取凭据是独立于 15 分钟访问 Cookie 的 claim cookie**：`pastebox_transfer_claim`，路径限定 `/api/v1/shares/{token}`，因此一个浏览器的多个领取互不覆盖，也不能拿 A 分享的 claim 下载 B 分享。老链接仍用 `pastebox_share_access`；对新传输，该 15 分钟 Cookie 不再能授权下载（`OpenSharedAttachmentWithClaimOrAccessGrantContext` 按分享是否属于传输分流）。
- **打开页面不消费名额，且不泄露内容**：`sharedAccessViewLocked` 在调用方没有有效 claim 时清空 `text`/`textPreview`，无论传输是文本还是「文本+文件」。正文只在 claim 响应或持有 claim 的页面读取中出现。
- **文本领取是「一次性 + 有界重放」**：claim 窗口 2 分钟，同 operationId 在窗口内重放同一 claim 与正文（不重复扣名额），窗口过后 410；没有长期可读端点。
- **文件会话最长 30 分钟且不超过分享有效期**：`min(领取时刻+30min, share.expiresAt)`；主动完成或到期即结束；重试下载与逐个下载都不再扣名额，但仍走既有流量与扫描规则（`ConsumeShareDownload` 与 clean 扫描检查保持不变）。
- **claim 凭据随领取一起持久化**：`token` 明文 + `token_hash` 唯一索引，与既有 `shares.token_ciphertext`（实际存的就是可回显的 token）保持一致；这样同 operationId 重放能返回同一凭据，网络失败不会白白浪费一个名额。
- **上限由后端发布**：公开目录新增 `transfers.maxClaimQuota`，发送表单据此收敛输入；目录未加载时不编造上限，交由服务端拒绝。
- **接口按接收者可见的凭据落位**：领取是接收者动作，路由为 `POST /api/v1/shares/{token}/claims` 与 `POST /api/v1/shares/{token}/claims/{claimID}/complete`，而不是设计稿里的 `/transfers/{id}/claims`——接收者只持有分享凭据，不应知道传输 ID。下载仍复用既有 `/shares/{token}/attachments/{id}/download`，只是对传输分享改为 claim 鉴权。

## 已知边界

- **存量传输会被纳入领取门禁**：迁移给既有 `transfers` 行 `claim_quota=1`，因此本分支此前创建的传输分享在第一个领取后不再接受新领取。功能尚未上线、没有生产数据；老分享（从未走传输的分享）语义完全不变，仍只用访问 Cookie 下载。若上线前已有真实数据，需要先决定存量传输是补名额还是保持旧语义。
- **`kind` 仍是字符串**：`file`/`text` 沿用仓库既有的 `Transfer.Status`/`Share` 状态字符串风格，没有引入新类型；kind 只影响按钮文案与会话窗口，鉴权判定不依赖它。
- **claim 凭据明文落库**：与 `shares` 的表现一致（列名叫 `token_ciphertext`，实际存的是可回显的 token）。若后续要求静态加密，`shares` 与 `transfer_claims` 应一起改。
- **不做阅后即焚**：本票只在名额耗尽时拒绝新领取，不销毁内容、不缩短分享有效期；销毁与后台清理属于 #11。会话结束后正在传输的字节不会被收回（与 #11 的进行中流处理一并考虑）。
- **状态同步属于 #12**：本票只让 `access` 返回名额与领取状态（不占名额），没有新增推送通道；成功页与接收页的自动刷新留给 #12。
- 浏览器验证为人工执行脚本，未纳入 CI；仓库当前没有浏览器测试运行器，与既有做法一致（HTTP 集成测试为主要入口），脚本与运行日志留在未入库的 `tmp/verify/`。
- `internal/httpserver/static/assets/` 中仍留有更早工单的旧构建产物；本票只同步本次构建引用的文件，未清理历史遗留。
