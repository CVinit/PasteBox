# 工单 #9 验证记录：文本／图片模式与原有内容管理兼容

对应 Issue：https://github.com/CVinit/PasteBox/issues/9
分支：`main`（本地提交，未推送）
阻塞前置：#5（已在本分支完成）

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

## 新增 HTTP 测试（`internal/httpserver/transfers_text_test.go`）

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestTransferTextSendPublishesLinkAndPickupCode` | 文本单独构成一份传输：`items` 为空、正文即内容；无上传即可发布；创建重试复用同一传输；链接与 6 位码读回同一份正文与同一个 `pasteId`；记录保留标题与正文 |
| `TestTransferSendRefusesEmptyAndOverLimitContent` | 既无文本也无文件 → 400 `transfer_content_required`；纯空白文本同样在创建时被拒（不会留下永远无法发布的草稿）；超方案文本上限 → 413 `text_too_large`；标签仍受方案限制 → 403 `tag_limit` |
| `TestTransferTextSendKeepsShareProtections` | 文本发送不是更软的通道：无密码/错密码的链接与取件码都被拒（401），正确密码才读到正文 |
| `TestTransferImageSendKeepsImageContentType` | 图片走既有附件流程：上传后 `image/png` 保留到发布与下载，接收方看到的附件类型可判断为图片，下载字节与上传一致 |
| `TestTransferSendLeavesExistingRecordsUntouched` | 新发送建立自己的记录与凭据：既有笔记的标题、正文、附件不变，`/shares` 只多出这次发送自己的分享 |
| `TestGuestTransferTextSendPublishesPickupCode` | 游客文本发送与登录用户同一套契约，并沿用游客文本上限（超限 413 `text_too_large`） |

## 新增 PostgreSQL 集成测试（`internal/postgres/transfers_test.go`）

| 测试 | 覆盖的验收条件 |
| --- | --- |
| `TestTransferStorePublishesTextOnlyTransfer` | 无声明文件的传输在真实数据库上按调用方判定发布；调用方报告“没有内容”时拒绝发布。内存实现与 PostgreSQL 实现共用同一条内容规则，规则只存在于服务层 |

该测试的由来：第一轮实现把“有没有内容”的规则同时写在 `internal/app/transfers.go`（`strings.TrimSpace`）和 `internal/postgres/transfers.go`（`btrim(text_body, E' \t\n\r\f')`）里，两者语义不同（NBSP 等 Unicode 空白只在 Go 侧被当作空白），内存存储与 PostgreSQL 对同一份数据可能给出不同结论，且只跑内存测试发现不了。现在 `ensureTransferCompleteLocked` 返回 `allowNoItems`，`AtomicTransferStore.PublishTransfer` 接收该判定，store 只保留它独有的“所有声明文件都已上传且附件仍属于该记录”检查。

## 浏览器验证（本轮执行，脚本未入库）

脚本：`tmp/verify/browser-issue09.cjs`（需本机 Playwright；`tmp/` 不纳入版本控制）
运行环境：本地 PostgreSQL/MinIO 容器 + 内嵌静态资源的 `pastebox api` + `pastebox worker --poll-interval 2s`，地址 `http://127.0.0.1:18080`。

结果：28/28 项通过。

模式与草稿（游客工作台，zh-CN）：

- 默认文件模式：激活页签为「文件」，面板是文件投放区，不是文本或图片。
- 切换到文本输入草稿，再切图片、切回文本：草稿仍在（模式切换不静默丢草稿）。
- 文件模式暂存 1 个文件，切图片再切回文件：暂存清单仍在（1 → 1）。
- 图片模式选择非图片：提示「请选择图片文件。」且不进入清单。

图片发送与接收：

- 图片模式选图后清单显示 `photo.png`；发送成功页给出链接、6 位取件码与有效期。
- 接收方用取件码打开同一份分享；图片附件出现「预览图片」按钮，点击后 `<img>` 真实加载（`naturalWidth > 0`，下载响应 200）。
- 上传失败（脚本拦截 `**/items/**` 制造失败）后报告失败；此时重新选图会替换失败草稿并回到可发送状态，不再提示「本次发送已开始」。

文本发送与接收：

- 游客文本模式发送后给出 6 位取件码；接收方用码打开并读到原文。
- 登录工作区文本模式同时提供「仅创建记录（不分享）」与「发送文本」：前者只创建记录（页面无分享面板），后者发布链接与取件码。

原有内容管理：

- 编辑选中记录的正文并保存，刷新后仍是新正文（编辑、标签输入与分享框未回退）。
- 选中该记录后发送文本：新发送被选中，卡片是这次发送的标题与正文；原记录卡片仍显示自己的正文，说明新发送没有写进历史笔记。

移动端 375px：

- 游客模式页签与文本面板都在视口内，无横向滚动。
- 登录工作区模式页签与面板、面板内每个输入/按钮都在视口内，无横向滚动。

运行期核对：发送设置（有效期、分享密码）在三个模式下共用；成功页显示的到期时间与设置一致；取件码入口与链接入口读回同一份内容。

## 关键实现决定

- **文本发送就是一次传输**：`TransferInput` 增加 `Title/Text/Tags`，写入该次传输自己的记录；`items` 为空且正文非空白即可发布，因此文本发送天然获得链接、6 位码、有效期与密码，而不再走旧的「先建 paste 再建 share」路径。空内容（含纯空白）在创建时即被拒，不会留下永远无法发布的草稿。
- **“有没有内容”只由服务层决定**：`ensureTransferCompleteLocked` 返回 `allowNoItems` 并传给原子 store；store 不再自己解释正文，避免内存实现与 PostgreSQL 对同一份数据判定不一致（见上）。
- **文本计入每日上传配额**：文本发送复用 `ensureCanCreatePasteLocked` 的文本/标签/配额检查，并按 `CreatePasteWithContext` 的既有做法记录当日上传字节，文本不能因为换入口而绕过日流量限制。
- **标题与标签随发送走**：登录工作区三个模式共用标题与标签输入，二者写入本次发送创建的记录；文件模式仍以文件名为缺省标题。
- **三个模式共用一个页签组件**：`SendModeTabs` 同时用于游客工作台与登录工作区，顺序固定为文件 / 图片 / 文本，键盘行为一致；文件与图片各用一个独立的 `useTransferQueue` 实例（同一个适配器），所以切换模式既保留草稿，也不会把暂存的文件和暂存的图片混进同一次发送。
- **队列阶段也放进 ref**：`useTransferQueue` 原来只在 state 里保存阶段，导致「先 reset 再暂存」这种同一事件内完成的操作用到过期阶段。现在阶段与清单都以 ref 为准，state 只用于渲染。
- **图片模式只送一张图**：选择、粘贴、拖入都进同一附件流程；`stageOneImage` 由两个发送区共用，决定“替换未开始的草稿 / 重新开始已完成的发送 / 失败草稿先取消再替换 / 上传中必须先取消”。
- **登录工作区保留两个文本动作**（用户选择 B）：`仅创建记录（不分享）` 仍是原来的 `createPaste` 行为，`发送文本` 走传输并给出链接与取件码；按钮文案改成能区分两者的说法。
- **新发送不写历史笔记**：传输路径总是新建自己的记录；「附加到当前记录」仍是唯一显式写进选中记录的动作，且只在文件模式出现。`createPaste` 与传输使用同一份草稿（标题/正文/标签），但两者各自创建新记录。
- **文本发送的幂等**：`useTextSend` 按草稿签名保存幂等键与传输 id，失败重试复用同一次传输（不因重试多发一份）；草稿被编辑后视为新的发送。
- **接收页按类型展示**：正文仍以文本呈现；图片附件多一个「预览图片」按钮，点击才通过同一鉴权下载端点取图，页面渲染本身不发起下载请求。

## 已知边界

- 图片预览是显式点击加载，不是自动缩略图：自动加载会在限下载次数的分享上消耗额度，而 #10 的领取会话还会给下载端点加上授权前置，届时预览与普通下载走同一套判定。
- 游客工作台不再调用 `/guest/pastes`、`/guest/pastes/{id}/shares`；这些端点与 `web/src/api.ts` 里对应的客户端方法保留，历史链接与旧客户端继续可用。
- 模式草稿存在组件 state 里，刷新页面即丢失；本轮不引入本地持久化。
- 游客文案表只保留统一发送区仍会读取的字符串；旧的 `modeText/modeImage/...`、`imageOnly` 等已删除，避免出现第二套模式文案。
- 文件与图片模式各一个队列，因此“文件模式暂存后切到图片模式”看到的是图片队列（空），这是刻意行为；切回文件模式草稿仍在。
- 图片模式仍是单张图片，与 #6 的既有约定一致；多图请用文件模式，本轮不做文件夹上传、打包下载或断点续传。
- 浏览器验证为人工执行脚本，未纳入 CI；仓库当前没有浏览器测试运行器，与既有做法一致（HTTP 集成测试为主要入口），脚本与运行日志留在未入库的 `tmp/verify/`。
- 首轮浏览器验证中出现过一次共享下载 410 `attachment_unavailable`（对象存储读取瞬时失败），随后同一流程与后续多次下载均为 200；该路径本票未改动，未再复现。
- `internal/httpserver/static/assets/` 中仍留有更早工单的旧构建产物；本票只同步本次构建引用的两个文件，未清理历史遗留。
