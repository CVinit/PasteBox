# 工单 #8 验证记录：内联时效与隐私设置

对应 Issue：https://github.com/CVinit/PasteBox/issues/8
分支：`main`（本地提交，未推送）
阻塞前置：#5、#7（均已在本分支完成）

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

## 新增 HTTP 权限矩阵（`internal/httpserver/transfers_access_test.go`）

同一份分享必须让链接与取件码走完全相同的鉴权，不能因入口不同绕过密码或登录要求。

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestTransferEntryPointsEnforceTheSameShareAuth` | 22 条矩阵：4 种分享设置（无保护／密码／需登录／密码+需登录）× 2 种入口（链接、6 位码）× 游客/登录查看者 × 无密码/错密码/正确密码。成功项断言两个入口打开的是同一 `pasteId` 与同一 share token |
| `TestPickupResolutionIsNotAuthorization` | 解析取件码只回传 `token`/`url` 两个字段（不含内容或分享标识）；解析成功不等于授权：游客拿到正确密码仍因需登录被拒，错密码同样被拒，登录后才打开 |
| `TestGuestTransferEntryPointsEnforceTheSameShareAuth` | 游客发送带密码时，链接与取件码同样要求密码；游客传 `loginRequired` 得到 400 `guest_share_login_required`，不再被静默丢弃 |
| `TestTransferShareExpiryNeverOutlivesItsContent` | 请求 10 倍保留期被收敛到方案保留期；share 到期时间与内容（paste）到期时间完全一致；过期后链接 410 `share_expired`、取件码 404 `pickup_not_found`；游客发送收敛到游客保留期（可控时钟，不 sleep） |
| `TestTransferExpiryFallsBackToThePlanPolicy` | `expiresInSeconds` 缺省（0）时使用方案保留期，不会产生永不过期的分享 |
| `TestTransferExpiryClampsRequestedValues` | 超过保留期的多种请求都收敛到策略上限，share 不会比内容活得更久 |

方案保留期由 `/api/v1/plans` 读取（`freePlanRetentionSeconds`），测试不复制后端数字。

## 浏览器验证（本轮执行，脚本未入库）

脚本：`tmp/verify/browser-issue08.cjs`（需本机 Playwright；`tmp/` 不纳入版本控制）
运行环境：本地 PostgreSQL/MinIO 容器 + 内嵌静态资源的 `pastebox api` + `pastebox worker --poll-interval 2s`，地址 `http://127.0.0.1:18080`。

结果：43/43 项通过。

首屏（zh-CN 与 en）：

- 参数恰好 4 条，且全部来自 `/api/v1/plans` 实际配置：单次容量 15 MB、保留期 6h、单文件上限 10 MB、注册要求「无需注册」；配置未公开的限速不展示。目录未返回时首屏不显示任何参数（不回落到本地数字）。
- 使用边界提示只有一处，文案为「禁止上传违法、恶意或侵权内容。」并链接 `/legal/abuse`（滥用/DMCA）与 `/legal/privacy`（隐私），与页脚用词一致。
- 品牌副标题「文件中转 · 跨设备传文件」、首屏 eyebrow「跨设备 · 文件中转」、站点描述（`meta description`、manifest）均以文件中转为主叙事，且不含调研流量结论（「数量级」等）。
- 首屏与发送区都没有阅后即焚开关。

发送与接收：

- 游客：发送设置区可选有效期仅 1 小时/6 小时（都不超过游客保留期），可设密码；设置分组在界面上有可见名称（Send settings），不会与其它动作混淆。成功页显示链接、取件码与「有效期至」，页面显示的到期时间与所选 1 小时一致。链接入口无密码/错密码都被拒绝，正确密码打开内容；取件码入口同样要求密码，正确密码打开同一份内容。
- 游客文本模式：刚输入的密码同样作用于生成的分享，不再出现「填了密码但分享没密码」的静默丢失。
- 登录用户（free 方案）：有效期下拉只有 1 小时/6 小时/24 小时，没有 7/30/180 天等方案不支持的选项；「需要登录」为内联开关，设置分组同样有可见名称。选择 1 小时 + 密码 + 需要登录后发送成功，成功页到期时间与 1 小时一致。匿名接收者无论走链接还是取件码都被拒绝（`login required for this share`）；已登录接收者仍需正确密码才能打开。
- 移动端 375px：落地页无横向滚动；发送设置行不撑宽所在面板，控件右边界都在视口内；游客与登录用户都能在该宽度下带设置完成发送。

运行期核对：失败信息与 HTTP 层一致（`share password is invalid`、`login required for this share`），说明前端没有替后端放宽任何鉴权。

## 关键实现决定

- **发送设置就地成组**：新增 `SendSettingsFields`（有效期、分享密码、需要登录）放在主输入区旁，分组在界面上有可见名称，避免把「分享密码」误读成同一面板里「创建记录」按钮的设置。登录工作区与游客工作台复用同一组件，避免两处行为漂移。游客不渲染「需要登录」——服务端本就不允许游客要求登录（`guest_share_login_required`），因此控件只在能兑现的入口出现。
- **有效期只给策略允许的档位**：`expiryOptionsFor(maxSeconds, currentSeconds, t)` 按当前方案（或游客保留期）过滤档位，不在梯级上的上限会被补进来；目录未加载（上限未知）时只展示当前值而不是编造限制。方案降级时用 effect 把草稿收敛到新上限。
- **默认值来自现有策略**：密码默认空、登录要求默认关闭、有效期默认沿用既有 24 小时草稿值并按方案收敛；服务端在缺省（`expiresInSeconds <= 0`）时仍按方案保留期兜底。
- **游客 `loginRequired` 由静默忽略改为显式拒绝**：与既有游客分享规则（`guest_share_login_required`）统一，避免发送者以为开启了登录保护而实际没有。这是本票唯一的对外行为变化。
- **游客文本/图片分享接入同一组设置**：`createGuestPaste` 与 `createGuestShare` 使用所选有效期与密码，保证「设置了密码」不会被模式切换悄悄丢掉；模式结构与内容管理仍归 Issue #9，本票不改。
- **首屏参数来自配置**：`landingFacts()` 最多返回 4 条，且只在目录已返回时渲染——没有目录就没有可承诺的数字。参数优先描述游客当下可用的单次容量/保留期/单文件上限与注册要求；游客上传关闭时退化为免费方案的同名参数 + 「需注册」。公开目录没有限速字段，因此不展示限速，避免承诺配置未支持的能力。
- **内容规范/举报/隐私统一口径**：新增 `UsageBoundaryNotice`，在工作区页脚与公共页脚渲染同一段文案并复用页脚既有的「滥用/DMCA」「隐私」用词；站点描述（`web/public/manifest.webmanifest`）从「私有云剪贴板」改为文件中转叙事。
- **给门禁加断言**：`scripts/check-web-launch-surfaces.mjs` 现在检查首屏参数上限（`return facts.slice(0, 4);`）、统一提示文案在源码与产物中都存在、manifest 不再使用剪贴板叙事，并禁止产物出现「数量级 / order of magnitude」等调研流量措辞。

## 已知边界

- 公开目录（`/api/v1/plans`）不包含限速配置，首屏因此只展示容量、保留期、单文件上限与注册要求；若后续要展示限速，需要先由后端公开该字段。
- 有效期档位是前端梯级（1 小时/6 小时/24 小时/7 天/30 天/180 天），上限由后端目录决定；梯级本身不是后端契约，改档位只影响可选项，服务端仍按方案保留期收敛。
- 游客 `loginRequired` 的 400 是本票有意引入的契约收紧：此前该字段被 JSON 解码丢弃并照常 201，等于对发送者撒谎；UI 从不发送该字段。
- 本票不新增阅后即焚开关（Issue #11），也不改动游客/登录用户的容量、保留期与权限边界。
- 登录工作区的有效期控件同时决定新建记录的保留期与本次文件发送的有效期，沿用原有共享草稿值（`draft.expiresInSeconds`）的既有行为，不新增第二个有效期控件。
- 取件码与链接的失败响应仍不同（码为 404 防枚举，链接为 410），这是 #7 的既定设计；两者对密码、登录与过期的判定完全一致。
- 浏览器验证为人工执行脚本，未纳入 CI；仓库当前没有浏览器测试运行器，与既有做法一致（HTTP 集成测试为主要入口），脚本与运行日志留在未入库的 `tmp/verify/`。
- `internal/httpserver/static/assets/` 中仍留有更早工单的旧构建产物；本票只同步本次构建引用的两个文件，未清理历史遗留。
