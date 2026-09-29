# 工单 #7 验证记录：6 位取件码接收

对应 Issue：https://github.com/CVinit/PasteBox/issues/7
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

新增 HTTP 集成测试（`internal/httpserver/transfers_pickup_test.go`）：

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestTransferPickupCodeResolvesTheSameShareAsTheLink` | 6 位码字符集与长度、大小写与空格不敏感、链接与码指向同一分享、解析不消费访问次数、接收者用码打开同一份内容 |
| `TestPickupCodeAnswersUnknownAndRevokedCodesAlike` | 无效码、撤销码返回完全相同的 404 `pickup_not_found`；撤销后原链接同样失效 |
| `TestPickupCodeStopsResolvingAfterShareExpiry` | 分享过期后码不再解析（可控时钟，不依赖 sleep） |
| `TestPickupCodeAttemptsAreRateLimited` | 10 次猜错后第 11 次返回 429 `pickup_rate_limited` 且带 `Retry-After`；预算用尽时正确码同样被拒 |
| `TestSuccessfulPickupsDoNotSpendTheGuessBudget` | 连续 12 次成功解析全部 200，之后仍能记满 10 次失败并触发限流（成功取件不占用猜码预算） |
| `TestPickupBudgetIsSpentFromTheSharedStore` | 服务接线到共享计数存储时，失败计数来自该存储（进程内 map 为 0 次），证明多实例路径不是摆设 |
| `TestGuestTransferPickupCodeResolves` | 游客发送同样获得取件码，且码解析到同一份分享令牌 |
| `TestPickupCodeKeepsSharePasswordAndLoginRules` | 带密码的传输：码可解析但访问仍需正确密码（错误密码 401）；要求登录的传输：未登录 401、登录后 200 |
| `TestLegacySharesKeepWorkingWithoutPickupCodes` | 旧分享不补码、响应不含 `pickupCode`、老链接仍可打开、未签发的码无法命中 |
| `TestPickupCodeGrantsNoSenderControls` | 解析响应只含 `token`/`url`，不授予撤销分享或读取传输记录的管理权限 |
| `TestPickupCredentialsStayOutOfLogs` | debug 日志确实输出了发布与解析行，且其中不出现取件码与分享令牌 |

应用层测试（`internal/app/pickup_codes_test.go`）：字符集/长度/归一化、发布冲突重试（注入前两次冲突，断言三次尝试码各不相同并最终落库成功）。

数据库层测试（`internal/postgres/pickup_codes_test.go`，跨连接）：

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestSharePickupCodeIsUniqueAcrossConnections` | 双连接写入同一有效码，第二条返回 `ErrSharePickupCodeExists`；`ShareByPickupCode` 只命中属主 |
| `TestPickupAttemptBudgetIsSharedAcrossConnections` | 两个连接交替写入的失败计数彼此可见；窗口过期后读数归零并从 1 重新计 |
| `TestPublishTransferRejectsADuplicatePickupCode` | 发布路径遇到重复码返回冲突并整体回滚（传输保持 draft），换码后可重发 |

## 浏览器验证（本轮执行，脚本未入库）

脚本：`tmp/verify/browser-pickup.cjs`（需本机 Playwright；`tmp/` 不纳入版本控制）。
运行环境：本地 PostgreSQL/MinIO 容器 + `pastebox api` + `pastebox worker --poll-interval 2s` + 内嵌静态资源，地址 `http://127.0.0.1:18080`。

结果：22/22 项通过。

- 桌面：发送成功页显示 6 位码与链接；点击「复制取件码」后剪贴板内容与码一致；输入小写并带空格的码打开同一分享（URL 与链接令牌一致）并下载到相同字节；粘贴完整链接仍走原路径。
- 移动端（375px）：码输入框完整落在视口内，复制码、输入码、粘贴链接三条路径均可用并下载成功。
- 登录工作区：发送成功页同样显示并可复制取件码。
- 无效码：停留在入口页并显示统一文案「That link or pickup code is invalid or has expired.」。
- 限流：连续猜错 10 次后第 11 次请求返回 429，界面显示「Too many pickup code attempts. Try again shortly.」；此前 6 次成功解析未消耗预算。

运行期核对：

- `grep` 本轮使用过的 3 个取件码，API 与 worker 日志中均无出现。
- `pickup_code_attempts` 中 `pickup:127.0.0.1` 计数为 10（全部来自猜错），成功取件未计入。

## 关键实现决定

- 字符集 `23456789ABCDEFGHJKMNPQRSTUVWXYZ`（排除 0/O、1/I/L），长度 6，共 31^6 ≈ 8.87 亿种；`crypto/rand` 拒绝采样取字符，避免取模偏置。
- 归一化仅去除空白与 `-`、`_` 并转大写；格式非法、未命中、已过期、已撤销统一返回 404 `pickup_not_found`，不区分「存在但失效」，避免猜码获得信息。
- 唯一性：`shares.pickup_code` 上的部分唯一索引覆盖所有历史码，码不回收、不复用；发布时冲突重试最多 8 次，仍失败返回 `pickup_code_unavailable`，不产生半成品分享。
- 限流按「失败次数」而非「全部尝试」计数：一次成功解析不消耗预算，因此同一出口 IP 的正常取件不会被邻居的猜码拖累；猜错（含过期/撤销）才计数。预算为每客户端 10 次失败/窗口，窗口取运行时 `rateLimits.windowSeconds`（`PASTEBOX_RATE_LIMIT_WINDOW_SECONDS` 默认 60s）；计数落在 `pickup_code_attempts` 表，多实例共享，每次失败顺带清理最多 50 条过期计数行。存储只负责计数，阈值留在服务层。
- 取件码与链接指向同一分享行，因此密码、登录限制、有效期、撤销、访问/下载计数全部复用现有 `validShareAccessLocked` 路径；解析接口只回传 `token`/`url`，不返回分享或传输标识，也不授予发送者管理权限。
- 取件码随发布写入 `shares.pickup_code`，与既有 `token_ciphertext`（同权凭据）一样以明文保存：发送者需要回显，且码与链接的泄露面相同。若后续要求凭据静态加密，链接令牌与取件码必须一起处理，属独立议题。
- 旧分享 `pickup_code` 为空且不参与查询，保持纯链接可用。

## 已知边界

- 只有「传输」发布时生成取件码；文本/图片模式与工作区「为选中记录创建分享」仍走既有 `createShare` 路径，本票不为其补码，它们会在 Issue #9 统一到传输后获得取件码。ADR-003 只要求不强制给历史分享补码，本票按该边界执行。
- 取件码是承载凭据：拿到码的人与拿到链接的人权限完全相同，仍受密码/登录限制约束；本票不引入领取名额与会话（属 Issue #10）。
- 限流按 IP 计数失败次数，同一出口 IP 在窗口内累计 10 次失败后会整体拒绝（含正确码）；这是防枚举控制，不替代 Issue #8 的入口鉴权统一。
- 码空间有限但足够；按当前「永不复用」策略，长期看插入冲突率会随分享量缓慢上升，重试机制已覆盖。
- 浏览器验证为人工执行的脚本，未纳入 CI；仓库当前没有浏览器测试运行器，与本项目既有做法一致（HTTP 集成测试为主要入口）。
