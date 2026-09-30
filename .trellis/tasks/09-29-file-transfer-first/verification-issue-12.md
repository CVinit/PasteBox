# 工单 #12 验证记录：跨设备状态与文本保存同步

对应 Issue：https://github.com/CVinit/PasteBox/issues/12
分支：`main`（本地提交，未推送）
阻塞前置：#10（匿名领取名额与领取会话，已在本分支完成）
联调对象：#11（阅后即焚与可靠清理）——本票在浏览器验收里让 worker 进程在断线期间销毁传输，验证终态契约与跨进程传播。

## 已提交的自动化证据

```
go test ./cmd/... ./internal/... -count=1        # 通过
go test ./cmd/... ./internal/... -race           # 通过
make test-postgres                               # 通过（临时 PostgreSQL 17 容器）
sh scripts/check-postgres-integration.sh --coverage 的等价本地跑法
                                                 # 语句覆盖率 77.5%（门禁 75%）
npm --prefix web run typecheck                   # 通过
npm --prefix web run build                       # 通过
node scripts/check-web-launch-surfaces.mjs       # 通过
gofmt -l cmd internal                            # 无输出
go vet ./...                                     # 无输出
```

`make test-postgres` 未跳过：新增的 2 个账号快照用例与既有 transfer 用例都在真实 PostgreSQL 17 上执行并通过。
覆盖率用 `-coverpkg=./cmd/...,./internal/...` 单独测量，因为本机 `tmp/` 下有未入库的临时 Go 程序，
`./...` 会把它们算进去；CI 的干净检出与这个包集合一致。

## 通道选型（代码核验后的决定）

ADR-005 把协议留到代码核验后决定。核验结果与据此的选择：

- 仓库此前没有任何流式/推送机制（Go 与 TS 都没有 SSE、WebSocket 或长轮询）。
- `rateLimitRule` 只对 POST 生效，GET 不进入任何限速类别，所以状态读取必须是 GET。
- 接收端凭据在 Cookie（`pastebox_share_access` 只签发给 `…/attachments`，`pastebox_transfer_claim` 覆盖整个 share 子树），
  游客凭据在请求头 `X-PasteBox-Guest-Token`。EventSource 不能设置请求头，所以游客作用域不可能靠请求头订阅。
- 生产默认 `PASTEBOX_HTTP_WRITE_TIMEOUT_SECONDS=0`（无写超时），SSE 不会被服务端写超时掐断。

结论：**用 SSE（`text/event-stream`）做长连接**，每作用域一个 `GET …/events`，服务端按有界间隔重读数据库、
仅在快照变化时推送。用户已确认该选择。

## 新增接口与契约

| 接口 | 作用域与鉴权 | 载荷 |
| --- | --- | --- |
| `GET /api/v1/me/events` | 会话 Cookie（`requireUser`） | `AccountStatusView`：该账号的发送记录 + 内容记录变更标记 |
| `GET /api/v1/shares/{token}/events` | 页面授权 Cookie 或活动领取 Cookie | `TransferStatusView`：一次发送的领取与终态 |

两者都：首个快照即鉴权（未授权返回普通 JSON 错误而不是开流）、每 2 秒重读一次权威存储、
仅在序列化结果变化时推送、每 15 秒发一行 `: ping` 注释。服务端主动结束时发
`event: closed`，带 `{"reason":"unauthorized"|"stream_limit"}`：前者是凭据失效（页面显示「请重新打开页面」），
后者是连接数上限（页面显示「连接数已达上限」并按退避继续重试）。拒绝订阅也用同一种 `closed` 事件而不是
状态码，因为浏览器读不到失败事件流的状态码。

发送方成功页与接收方页面用**同一条**凭据通道：发布时服务端签发同一份页面授权
（`grantSenderShareAccess`），所以游客发送端不需要第二种协议、凭据也不进 URL。

## 新增/修改的测试

### 服务层（`internal/app/status_test.go`）

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestAccountStatusReportsSendsAndRecordMarkers` | 快照按账号隔离；每条发送带配额、已领取、剩余、文件数、标题、链接与取件码；内容记录只给变更标记不给正文 |
| `TestAccountStatusFollowsClaimsRevocationAndDestruction` | 领取推进计数 → `exhausted`；撤销只失效链接不销毁；阅后即焚结束后 `destroyed` + `claims_ended` + `pending_delete`；到期为 `expired` |
| `TestShareStatusReadNeverSpendsAClaim` | 连续读取与重复读取都不动 `claimed_count`、不动 `visit_count`；已领取会话被如实报告 |
| `TestShareStatusRequiresAPageGrantOrALiveClaim` | 无凭据、他人凭据、已结束的领取都被拒；同一领取只对自己的分享有效；老分享（非传输）明确返回无可报告状态 |
| `TestShareStatusReportsDestroyedInsteadOfRefusingIt` | 终态以状态上报而不是报错，且发送端与接收端共享同一 `state` 与 `destroyReason` |
| `TestShareStatusExpiryIsReportedBeforeAnythingIsRefused` | 到期在拒绝之前就被报成 `expired`，且不带销毁终态 |

### HTTP 集成（`internal/httpserver/status_test.go`）

真实 HTTP 服务 + 真实 SSE 读取，不是把 handler 直接当函数调用。

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestAccountStatusStreamUpdatesASecondDevice` | 同账号第二设备在推送里看到领取数从 0/1 变 1/1，页面无需刷新；订阅本身没有多花名额 |
| `TestAccountStatusStreamRejectsAnUnauthenticatedCaller` | 账号通道在会话之后，匿名订阅 401 |
| `TestShareStatusStreamServesRecipientAndSenderWithoutSpendingAClaim` | 发布者凭页面授权订阅成功；从未打开分享的浏览器 401；接收方领取后自己那条连接只报告计数变化（连接早于领取），重连后才报告 `claimed`；重连不花名额 |
| `TestShareStatusStreamReportsDestructionAcrossDevices` | 最后一位领取结束后，发送端成功页收到 `destroyed` + 原因 + `pending_delete` |
| `TestShareStatusStreamRefusesAForeignGrant` | 真实 Cookie jar 验证：发布签发的授权路径是 `/api/v1/shares/{token}`，对另一条分享不会被发送，取另一条分享状态返回 401 |
| `TestStatusStreamReconnectReadsTheAuthoritativeSnapshot` | 断线期间发生的领取，在重连的首个快照里读到（不是重放事件） |
| `TestStatusStreamEndsWhenItsCredentialStopsAuthorizing` | 账号被冻结后服务端主动发 `closed` 并收流，而不是让页面一直重试 403 |
| `TestStatusStreamEndsWhenTheServerShutsDown` | 进程关闭时订阅立即结束，不把关闭拖到超时 |
| `TestStatusStreamBoundsOpenSubscriptions` | 单凭据 4 条上限触发 `status_stream_limit`，另一账号不受影响 |
| `TestStatusStreamReportsUnchangedStateOnlyOnce` | 无变化的时间片不推送，订阅不是「换皮的轮询」 |

### 持久化（`internal/postgres/account_status_test.go`）

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestAccountStatusStoreReportsCommittedSendState` | 两条查询取回已提交的发送、分享、清理边界与文件数，不含附件行与正文；第二条连接得到相同结果；无关账号为空 |
| `TestAccountStatusIsConsistentAcrossInstances` | 两个服务实例（各自的缓存与对象存储、同一个数据库）读到同一状态；领取与销毁跨实例可见；`transfers.status/destroy_reason` 与 `pastes.status` 的库内值被直接断言 |

## 浏览器验证（本轮执行，脚本未入库）

脚本：`tmp/verify/browser-issue12.cjs`（需本机 Playwright；`tmp/` 不纳入版本控制）
运行环境：本地 PostgreSQL 17 与 MinIO 容器 + 内嵌静态资源的 `pastebox api` + `pastebox worker --poll-interval 2s`，地址 `http://127.0.0.1:18080`。
用互相独立的浏览器上下文模拟不同设备与不同凭据，每个上下文有自己的 Cookie jar。

结果：**36/36 项通过**。

- 账号通道：发送方成功页与「发送记录」列表都显示真实连接状态；第二设备（同账号登录）无需刷新就从「已领取 0/2 次」变成「已领取 1/2 次」。
- 接收页：领取后显示剩余次数与会话有效期；另一台设备领走最后一个名额后本页变成「剩余可领取 0 次」；新访客看到「名额已领完」且没有领取按钮；发送方撤销后本页变成「链接已撤销」。
- 文本同步：一台设备留有未保存草稿时，另一台设备保存不会覆盖它，页面给出「本地未保存的修改已保留」并让用户选择；「保留本地修改」与「载入远端版本」都按预期生效；草稿干净时编辑器自动跟随远端新版本。
- 凭据隔离：从未打开分享的浏览器订阅该分享 401；匿名订阅账号通道 401；只有分享凭据的浏览器订阅账号通道 401；一条分享的授权不会被送到另一条分享；页面授权 Cookie 不出现在 `document.cookie`。
- 阅后即焚联调：最后一位领取结束后，发送端成功页与接收端页面都变成「已销毁」，数据库为 `destroyed/claims_ended`。
- 断线恢复（真实进程中断）：停掉 API 进程 → 页面徽标「重连中」→ 12 秒后「连接中断」；**在断线期间**让 worker 进程按到期销毁该传输；再启动 API → 徽标自行回到「已连接」，接收页与成功页都读到断线期间错过的「已销毁」。
- 老分享链接：没有传输在背后的分享页照常显示正文，并且**不开**任何状态通道（页面上没有连接状态徽标）。
- 订阅与重连不消费名额：整轮结束后数据库 `claimed_count` 与实际领取次数一致（2）。

## 两轴代码评审后的修正

按 `/code-review` 的两轴评审结果改了以下各点：

- 记录里原本写错的成本说法改为「固定查询条数，返回行数仍随账号规模增长」，并补上阅后即焚满额时的那次存活会话计数查询。
- `shareAccessCookiePath` 的改动写进了 `.trellis/spec/backend/quality-guidelines.md` 的 HTTP 契约（该文档明确要求 cookie 契约变更同批更新），并补上传输与状态通道的路由与事件契约。
- 前端两条通道地址改为 `api.ts` 里的路径助手（仓库约定「API 响应类型与取数助手放在 api.ts」），不再在组件里写内联字面量。
- 发送状态词表收敛成一张表（词 + 是否终态），领取进度文案收敛成一个函数，供成功页与发送记录列表共用。
- 去掉 `ShareStatusWithContext` 里没有使用的 viewer 参数、`writeStatusEvent` 没有使用的返回值、以及 `s.content.AccountStatus` 上恒真的类型断言；`AccountStatusStore` 移到 `content_stores.go` 与其他 store 接口同处。
- `Server.Shutdown` 改名为 `ShutdownStatusStreams`，避免与 `http.Server.Shutdown` 混淆。
- 修掉评审发现的真 bug：老分享（没有传输）的接收页原本也会开状态通道，服务端 400 拒绝后页面会一直「重连中 → 连接中断」。现在前端只在 `access.transfer` 存在时订阅，服务端也把 400 视为终结性错误。
- 连接数上限从「假装的连接中断」改成单独的「连接数已达上限」状态，并继续退避重试。

## 关键实现决定

- **一条通道两种作用域，鉴权方式各自复用既有凭据**：账号作用域用会话，凭据作用域用页面授权 Cookie 或活动领取 Cookie。状态读取既不消费领取名额、也不消费访问次数，且从不返回正文、文件名或附件标识。
- **发布即签发页面授权**（`grantSenderShareAccess`）：发送方成功页因此能复用接收方同一条通道，不需要为游客凭据发明第二种传输方式。授权有效期取 `min(分享到期 + 15 分钟, 签发后 1 小时)`——留出的宽限期让短时效发送在结束时仍可被观察，而该授权只能读状态。
- **`shareAccessCookiePath` 从 `/api/v1/shares/{token}/attachments` 放宽到 `/api/v1/shares/{token}`**：页面授权覆盖分享自身子树，状态通道才不必再引入一个凭据。授权本身不因路径放宽而多授予任何东西：传输型分享的下载仍然只认领取，`/access` 与 `/claims` 也仍然只认密码。
- **订阅即快照，存储是唯一事实来源**：每次连接与每个时间片都重新读库（账号快照固定两条查询：transfers join shares/pastes 一次、pastes 一次；凭据快照为分享、传输、内容记录各一次，阅后即焚且名额领满时再加一次存活会话计数），比较序列化结果决定是否推送，不做单实例内存广播。因此重连读到的就是当前状态，跨实例一致。查询条数有界，返回行数仍随账号规模增长。
- **服务端只在变化时推送**：一个时间片没变化只花查询、不花带宽；`TestStatusStreamReportsUnchangedStateOnlyOnce` 固定了这一点。
- **连接数量有界**：单凭据 4 条、单进程 256 条，超限 503 `status_stream_limit`；连接上限在鉴权之前检查，未授权调用不会为了被拒而先做一次存储读取（代价是已饱和作用域上的探测返回 503 而不是 401，已在「已知边界」记录）。
- **真实连接状态由传输决定**：前端只在服务端往返之后才改状态，`navigator.onLine` 只用来提前重试，从不作为状态本身。掉线先「重连中」，同一次中断持续超过 12 秒才「连接中断」（倒计时按「一次中断」计，不按重试次数重置）。
- **领取凭据在连接建立时固定**：SSE 请求头不会变，所以一条已开的连接看不到之后才拿到的领取 Cookie。页面在领取/结束领取后主动重连（`runClaimAction` 调用 `reconnect`），这与页面本来就重新读取访问状态一致。
- **服务端自己收流**：`*Server` 现在自己实现 `ServeHTTP` 并暴露 `Shutdown()`，`main.go` 在 `http.Server.Shutdown` 之前调用它。否则每条打开的订阅都会把关闭拖满 10 秒超时（本机日志里出现过 `api server shutdown failed: context deadline exceeded`），修复后日志只有 `api server stopped`。
- **文本同步只做「保存后同步」**：账号快照只带记录的 `status|updatedAt` 变更标记，客户端比对本地列表与上一次快照：远端变了且本地列表还是旧版才处理。选中记录且草稿有未保存修改时只提示、不覆盖；草稿干净时跟随远端版本。没有逐字协同，也不读取系统剪贴板。
- **发送记录列表复用账号快照**：新「发送记录」视图直接渲染通道给的最后一份快照，不再单独请求 `GET /api/v1/transfers`，因此不会出现「列表与通道说法不一致」。
- **终态契约与 #11 共用**：`state`（`draft/canceled/claimable/claimed/exhausted/expired/revoked/destroyed`）与 `destroyReason`/`cleanupStatus` 是发送端与接收端共用的同一套词；已到期但 worker 还没 sweep 的阅后即焚传输按 `transferBurnDueLocked` 上报为 `destroyed`，与下一个请求会得到的答案一致。

## 已知边界

- **连接上限先于鉴权**：某个作用域已有 4 条订阅时，未授权探测会收到 `closed{reason:"stream_limit"}` 而不是 401。这暴露「该作用域已饱和」，但不暴露内容；换顺序则会让未授权请求先做一次存储读取。
- **连接上限是「容量答案」而不是「连接中断」**：页面为它单独显示「连接数已达上限」并继续按退避重试，不假装是网络掉线。
- **接收端页面授权 15 分钟**：未领取的接收方页面在页面授权过期后由服务端发 `closed` 收流，页面显示「请重新打开页面」（重新打开分享即可恢复）。已领取的会话另有最长 30 分钟的领取 Cookie 同样能授权状态读取，所以领取后的窗口跟随领取会话而不是 15 分钟。这是「接收页自动显示状态」在超长会话上的部分满足，没有延长既有的页面授权有效期。
- **游客作用域用发布时签发的页面授权**：因此游客发送端不引入游客 Cookie，也不需要把游客凭据放进 URL；代价是游客成功页的通道最长 1 小时（或分享到期 + 15 分钟）。
- **一个时间片 2 秒**：状态最多滞后 2 秒；这是「有界查询成本」的实现，不是即时推送承诺。
- **`statusStreamInterval` 是服务端字段**：测试在服务启动前把它改短，生产固定 2 秒。
- **`-coverpkg` 需要显式包集合**：本机 `tmp/` 下有未入库的临时 Go 程序，`./...` 会把它们算进覆盖率；CI 干净检出不受影响。
- **`internal/httpserver/static/` 只同步了本次构建引用的文件**（新增 `index-CcQYMSCa.js`、更新 `index.html`；CSS 复用既有哈希 `index-Vr5DerIQ.css`），历史遗留产物未清理，与 #10、#11 的处理一致。
- **未纳入本票**：逐字协同编辑、自动读取系统剪贴板、匿名共享房间、SSE 之外的第二套推送机制。
