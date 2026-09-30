# 工单 #11 验证记录：阅后即焚与可靠清理

对应 Issue：https://github.com/CVinit/PasteBox/issues/11
分支：`main`（本地提交，未推送）
阻塞前置：#10（匿名领取名额与领取会话，已在本分支完成）

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

`make test-postgres` 未跳过：新增的 3 个持久化用例与既有 6 个 transfer store 用例都在真实 PostgreSQL 17 上执行并通过。

## 新增/修改的测试

### HTTP 集成（`internal/httpserver/transfers_burn_test.go`）

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestBurnAfterReadingIsOptInOverHTTP` | 开关默认关闭；只有请求里带 `burnAfterReading` 的传输才开启；视图回显开关状态 |
| `TestBurnTransferEndsWithItsLastSessionOverHTTP` | 2 名额：第一位结束会话后传输与内容保持，第二位仍能下载整批；最后一位结束后传输 `destroyed`（`destroyReason=claims_ended`、`cleanupStatus=pending_delete`、share 已撤销）；页面/下载/领取/新访客全部 410 `transfer_destroyed`；取件码按既有设计保持不可解析 |
| `TestBurnTransferExpiryIsReportedAsItsTerminalState` | 未领满 + 有效期结束：下一个访客触发并看到 `transfer_destroyed`，发送者视图 `destroyReason=expired`，已开启的会话下载被拒 |
| `TestBurnRevocationOnlyInvalidatesTheLinkOverHTTP` | 撤销只失效链接/取件码/已开会话，不销毁内容（已批准的销毁条件只有「名额领完且会话结束」与「有效期结束」） |
| `TestOrdinaryTransferSurvivesItsLastClaimOverHTTP` | 回归：未开启开关的传输在最后一个会话结束后仍为 `published`、内容 `active`、share 未撤销 |

### 服务层（`internal/app/burn_test.go`）

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestBurnAfterReadingDefaultsOffAndNeverDestroysAnOrdinarySend` | 默认关闭；打开页面/结束会话/后台 sweep 都不销毁；不产生清理任务 |
| `TestBurnTransferIsDestroyedOnlyAfterEveryClaimEnds` | 打开页面不销毁也不扣名额；第一位结束会话不删除剩余名额需要的内容；最后一位结束后销毁 + 撤销 share + 内容 `pending_delete` + 排队 1 个 cleanup 任务；四条入口（页面/下载/领取）都返回 `transfer_destroyed` |
| `TestBurnTextTransferEndsOnlyAfterEverySlotIsSpent` | 文本在还有名额时（含前一位窗口已过）保持可读；最后一个名额的窗口结束、sweep 才销毁正文 |
| `TestBurnSweepDestroysExpiredSends` | 未领满 + 到期由 sweep 以 `expired` 销毁；撤销不销毁，仍等到期 |
| `TestBurnStopsAnOpenDownloadWhenItsSessionEnds` | 已打开的流在会话结束（`claim_ended`）或链接被撤销后于下一个检查点中断；已传出的字节不收回 |
| `TestBurnCleanupFailureKeepsTheSendRefusedAndRetryable` | 物理清理失败时终态不变、访问不恢复，清理任务保持 pending 等待 worker 重试 |
| `TestBurnCleanupReleasesOnlyItsOwnContentAndProtectsSharedObjects` | 共享对象（引用未归零）不被物理删除，另一条记录仍可下载；仅本传输独享的对象在清理后删除；无关内容不受影响 |

### 持久化（`internal/postgres/transfer_burn_test.go`）

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestTransferStoreDestroyCommitsTheWholeTerminalState` | 一次事务内完成：终态 + 撤销 share + 正文/附件 `pending_delete` + 1 个 cleanup 任务；重复调用不再排队、终态不覆盖；草稿不可销毁 |
| `TestTransferStoreBurnSweepQueryTracksClaimsAndExpiry` | sweep 候选集只含「领满且无存活会话」或「已过期」的开启传输；存活/已完成/已过期会话的计数正确；非开启传输与草稿不受影响；另一连接可见终态 |
| `TestTransferStoreDestroyRacesClaimsSafely` | 8 个并发领取与 1 个销毁：行锁串行化，成功领取数 == `claimed_count` ≤ 配额，销毁后领取被拒，只排队 1 个清理任务 |

### worker（`internal/worker/worker_test.go`）

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestRunnerSweepsBurnTransfersOnEveryRun` | 每次轮询都执行销毁 sweep，销毁数进入 `Summary.Destroyed` |
| `TestRunnerKeepsWorkingWhenTheBurnSweepFails` | sweep 失败只记日志，不终止 worker，同批次的清理任务照常执行 |

既有 `TestRunnerRetriesFailedJobWithBackoff` 覆盖「清理任务失败 → 退避重试」，与上面的「失败不恢复访问」用例互补。

## 浏览器验证（本轮执行，脚本未入库）

脚本：`tmp/verify/browser-issue11.cjs`（需本机 Playwright；`tmp/` 不纳入版本控制）
运行环境：本地 PostgreSQL 17 与 MinIO 容器 + 内嵌静态资源的 `pastebox api` + `pastebox worker --poll-interval 2s`，地址 `http://127.0.0.1:18080`。

结果：**19/19 项通过**（对最终提交的代码重跑）。用互相独立的浏览器上下文模拟不同接收者，验证的是真实 cookie 隔离。

- 发送方：开关默认未勾选；开启前的说明文字同时包含触发条件与「已保存副本无法收回」；未开启时成功页不出现销毁承诺，开启后出现「阅后即焚已开启…」与「可领取 N 次」。
- 接收者 1（2 名额）：打开页面即列出清单；领取后下载 200/10 字节；点「完成领取」后页面显示「剩余可领取 1 次」，传输仍存活。
- 接收者 2：仍能领取并下载整批；完成领取后页面显示「这份传输已销毁：正文与附件已失效，附件正在后台清理。接收方已保存到本地的副本不受影响。」，页面不再有下载链接，直接请求原下载地址返回 410。
- 新访客打开同一链接：同样看到已销毁文案，而不是坏页面或 404。
- 回归：未开启开关的传输在最后一个会话结束后显示「名额已领完」，清单仍在。
- 文本：领取后读到正文；会话结束后页面不再提供正文。
- 移动端 375px：开关与说明不撑宽页面（`scrollWidth - innerWidth = 0`）。

### worker 后台清理（无任何流量的端到端验证）

用 API 创建一个 `expiresInSeconds=4`、3 名额、开启阅后即焚、无人领取的传输并发布，等待 10 秒后：

- `POST /shares/{token}/access` → 410（访客触发的是终态，不是通用过期）。
- worker 日志出现 `burn-after-reading transfers destroyed count=1`。
- 数据库：`transfers.status=destroyed`、`destroy_reason=expired`、`pastes.status=deleted`，cleanup 任务 `completed`。

另有一次文本传输（1 名额，领取后不完成）：在 2 分钟文本窗口结束后由 sweep 以 `claims_ended` 销毁，`pastes.status=deleted`；同一时刻未开启开关的传输仍是 `published` + `active`。

## 关键实现决定

- **开关只在发送时决定，默认关闭**：`transfers.burn_after_reading` 默认 false，只有 `burnAfterReading=true` 的请求才写入；迁移不给历史行开启，访问页面也不会开启或触发销毁。
- **销毁条件只有两个**（用户已批准）：全部名额领完且所有领取会话结束（`claims_ended`），或分享有效期结束（`expired`）。`transferBurnDueLocked` 是唯一判定点；「存活会话」按 `status=active AND expires_at > now` 计算，因此结束一个会话不会带走其他名额需要的内容，文本在最后一个窗口结束后才失效。
- **撤销不等于销毁**：`RevokeShareWithContext` 保持原语义（失效链接、取件码与已开会话），不销毁内容；已撤销的开启传输仍等到期由 sweep 销毁。这是刻意收窄范围：销毁不可逆，而批准的条件清单里没有撤销。
- **先禁止访问、再后台清理**：`AtomicTransferDestroyStore.DestroyTransfer` 在一个事务里提交终态 + 撤销 share + 标记正文/附件 `pending_delete` + 插入 cleanup 任务，所以不会出现「访问已禁止但没人负责释放」或反过来的半成品；物理删除仍由既有 worker 清理路径（引用计数、对象引用归零才删）执行。
- **清理失败不恢复访问**：拒绝访问依据的是已提交的终态，清理任务失败只影响字节释放，worker 按既有退避重试。
- **终态可查询**：`GET /api/v1/transfers/{id}` 返回 `burnAfterReading`、`destroyedAt`、`destroyReason`、`cleanupStatus`（`active`/`pending_delete`/`deleted`），接收页与发送成功页的文案据此展示。
- **进行中的流**：transfer-backed 下载的响应体包了一层复查（`TransferStreamRecheckInterval = 5s`），复查复用 `validTransferClaimLocked`，因此会话完成/到期、分享过期或撤销、以及其它实例完成的销毁都会在下一个检查点中断流；已写出的字节不收回。
- **worker 每轮轮询都跑一次 sweep**：这是「没有流量也能可靠销毁」的实现方式；sweep 失败只记日志（下一轮重试），不影响同一批次的作业。
- **接口落位沿用 #10**：领取是接收者动作，路由仍是 `POST /api/v1/shares/{token}/claims...`；销毁不新增公开路由。

## 已知边界

- **发送端「已销毁」尚无实时通道**：本票让页面显示已失效/已销毁（接收页文案 + 四条入口的 `transfer_destroyed`），发送端可查询终态字段，但没有推送通道把销毁事件推到发送成功页/记录列表——按验收条件，这属于状态同步票 #12 的联调范围，本票保留明确可查询终态。
- **取件码在销毁后按既有设计不可解析**：`resolvePickupCode` 对已撤销/已过期/内容不可见的分享统一返回「取件码无效」，不透露该码曾经存在（#7 的防猜码口径）。持链接者能看到「已销毁」，持码者只会看到无效提示。
- **不做撤销即销毁**：见上，刻意收窄。
- **5 秒检查点**：短于 5 秒的下载不会被复查；这是「不能只在下载开始时检查一次」的有界实现，不是即时中断承诺。跨实例传播依赖每次复查读库，不做广播。
- **`internal/app/burn.go` 承担三件事**：销毁判定、sweep、下载流复查。三者共享同一套判定与鉴权函数，因此暂不拆分；若后续再增长应拆成 `burn.go` 与 `transfer_stream.go`。
- **`TransferView.CleanupStatus` 直接复用 `pastes.status` 字符串**：`active`/`pending_delete`/`deleted`，没有引入独立枚举，与仓库既有状态字符串风格一致。
- **`viewTransferLocked` 现在多读一次 paste 行**：为了报告清理边界；`ListTransfersWithContext` 因此是每次传输一次额外读取。当前没有发送端记录列表的批量接口，规模可控。
- **`seedBurnTransfer`（postgres 测试）与 `seedPublishedTransfer` 形状相近**：前者额外需要开启开关、附件与自定义到期时间，而后者被 #10 的用例共用；为避免扰动既有夹具，保留为独立的测试局部助手。
- **浏览器验证为人工执行脚本，未纳入 CI**：仓库当前没有浏览器测试运行器，与既有做法一致（HTTP 集成测试为主要入口），脚本与运行日志留在未入库的 `tmp/verify/`。
- **`internal/httpserver/static/` 只同步了本次构建引用的文件**（新增 `index-DShde2SZ.js`、更新 `index.html`）；CSS 与历史遗留产物未清理，与 #10 的处理一致。
