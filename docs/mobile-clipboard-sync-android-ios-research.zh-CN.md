# PasteBox Android / iOS 剪贴板同步调研

调研日期：2026-08-07

本文调查 Android 和 iOS 在系统权限、后台运行和应用商店政策允许范围内，怎样尽可能实现跨设备剪贴板同步。目标是为 PasteBox 后续移动客户端、同步协议和真机 PoC 提供可执行依据。

本文只讨论移动端能力边界和推荐方案，不修改网页版现有功能，也不要求把现有 Paste 数据模型直接改造成同步数据模型。

## 结论先说

移动端不能照搬 Windows/macOS 的常驻剪贴板监听方案：

- Android 普通应用只能在拥有输入焦点时读取剪贴板。前台服务只能提高进程存活率，不能获得剪贴板读取权限。
- Android 当前默认输入法不受输入焦点限制，可以作为接近全自动的增强模式，但用户接受度、开发成本和 Google Play 审核成本都较高。
- iOS 应用进入后台后通常会被挂起，不能持续监听剪贴板；直接读取其他应用写入的剪贴板还会触发系统确认。
- iOS 最可靠的上传路径是 Share Extension、快捷指令/App Intent 和 `UIPasteControl`；最可靠的接收路径是通知、主应用复制以及自定义键盘中的云剪贴板面板。
- 两个平台都应该把“服务器已经收到内容”和“设备系统剪贴板已经更新”作为两个不同状态。

推荐的产品定义是：

| 平台 | 本机内容上传 | 远端内容接收 | 对外承诺 |
| --- | --- | --- | --- |
| Android 普通模式 | 前台自动；后台通过分享、快捷磁贴等主动触发 | FCM 通知、通知复制按钮；可选尽力自动写入 | 系统允许范围内自动同步 |
| Android 高级模式 | 默认输入法配合常驻服务，接近自动监听 | 输入法面板直接插入或写入剪贴板 | 高级实验能力，不保证所有机型 |
| iOS | Share Extension、快捷指令、`UIPasteControl`、前台同步 | APNs 通知、打开应用复制、键盘面板插入 | 快速同步，不承诺后台自动监听 |

如果产品要求“四个平台在任何时候都无感、实时、完全自动”，那么 Android 普通模式和 iOS 无法满足。这是操作系统设计边界，不是增加开发时间就能解决的问题。

## 先区分两个方向

“剪贴板同步”包含两个能力，系统限制并不相同。

### 本机到云端

```text
其他应用产生剪贴板
  -> PasteBox 客户端读取
  -> 本地过滤和加密
  -> 上传同步事件
```

难点是客户端有没有资格读取其他应用写入的剪贴板，以及应用在后台有没有执行时间。

### 云端到本机

```text
PasteBox 收到其他设备事件
  -> Push 通知移动端
  -> 客户端拉取并解密
  -> 写入系统剪贴板或插入当前输入框
```

难点是 Push 是否及时到达、应用是否能被唤醒，以及自动覆盖用户当前剪贴板是否符合用户预期。

因此同步协议不能只记录一个 `synced=true`。至少需要区分：

- `received`：设备知道有新事件。
- `downloaded`：密文已下载到本地。
- `available`：内容已经出现在客户端历史中。
- `applied`：用户已复制、插入，或客户端已成功写入系统剪贴板。

## Android 系统能力边界

### 剪贴板读取限制

Android 提供 `ClipboardManager` 和 `OnPrimaryClipChangedListener`，但官方 API 明确说明：应用不是当前默认输入法，也没有输入焦点时，`getPrimaryClip()` 返回 `null`，`hasPrimaryClip()` 返回 `false`。

这意味着：

- Activity 位于前台并拥有输入焦点时，可以读取和监听。
- 普通后台 Service 不可以读取。
- Foreground Service 虽然显示常驻通知并提高存活率，但仍不等于拥有输入焦点。
- WorkManager、AlarmManager、FCM 唤醒也不会自动取得读取资格。
- 当前默认输入法属于官方明确列出的例外。

剪贴板变化监听器还可能因为 `ClipDescription` 文本分类状态变化，对同一份内容回调多次。实现时必须通过内容哈希、MIME 类型和最近事件记录去重，不能把每次回调都上传成一条新内容。

### 不同 Android 版本的隐私行为

- Android 10（API 29）开始，后台应用不能读取前台应用的剪贴板。
- Android 12（API 31）开始，应用读取剪贴板内容时，系统通常会显示访问提示。
- Android 13（API 33）开始，系统会显示复制内容预览，并自动清理一段时间未使用的剪贴板内容。
- 来源应用可以用 `ClipDescription.EXTRA_IS_SENSITIVE` 标记密码、银行卡等敏感内容，系统会隐藏预览。

PasteBox 应在读取到 `EXTRA_IS_SENSITIVE=true` 时默认拒绝上传。这个标记依赖来源应用主动设置，不能覆盖所有敏感内容，因此还需要本地敏感内容规则。

### 文本、图片和文件不是同一种难度

纯文本可以直接存入 `ClipData`。图片和文件通常以 `content://` URI 形式存在，权限和生命周期由来源应用的 `ContentProvider` 控制：

- URI 可能只能在当前交互过程中读取。
- 应用稍后再读取时，临时授权可能已经失效。
- 远端图片写入剪贴板时，需要由 PasteBox 的 `FileProvider` 暴露本地缓存文件。
- MIME 类型、URI 权限和临时文件清理都需要单独处理。

所以移动端第一版应只同步纯文本和 URL。图片、文件优先通过系统分享入口上传，不依赖后台读取剪贴板 URI。

## Android 推荐方案

### 普通模式：推荐作为第一版

普通模式不要求默认输入法，也不使用无障碍权限。

#### 前台自动上传

应用进入前台后：

1. 注册 `OnPrimaryClipChangedListener`。
2. 比较剪贴板元数据和上次处理的内容哈希。
3. 读取内容并检查 MIME 类型、大小和敏感标记。
4. 执行本地敏感内容过滤。
5. 加密并写入本地上传队列。
6. 通过 API 上传，失败时交给 WorkManager 重试。

应用离开前台时应注销监听器。即使保留监听器，后台回调也不能绕过读取限制。

#### 系统分享入口

在清单中声明接收 `ACTION_SEND` 和 `ACTION_SEND_MULTIPLE`：

- 文本和 URL：接收 `text/plain`。
- 图片：接收 `image/*`。
- 文件：根据 MVP 支持范围声明具体 MIME 类型，避免使用过宽的 `*/*`。

用户操作路径是：

```text
选中内容 -> 分享 -> PasteBox -> 选择设备或同步到全部设备
```

这是 Android 上最稳定、权限最清楚的主动上传方式，也是图片和文件的首选路径。

#### 快捷磁贴、桌面快捷方式和小组件

可以提供“同步当前剪贴板”快捷入口：

- 快捷设置磁贴点击后启动一个极简 Activity。
- Activity 获得输入焦点后读取、上传并自动关闭。
- 桌面快捷方式和小组件触发同一个流程。

不能让后台 TileService 直接假定自己拥有读取资格。最稳妥的实现仍是通过 `PendingIntent` 打开可见 Activity，再读取剪贴板。

#### 接收远端内容

建议使用下面的流程：

```text
服务端产生同步事件
  -> FCM 发送 event_id 和游标
  -> 客户端拉取密文
  -> 本地解密并写入 Room 历史
  -> 显示“来自某设备的新剪贴板”通知
  -> 用户点击“复制”
  -> ClipboardManager.setPrimaryClip()
```

Push 负载不要包含明文剪贴板。短文本即使能放进 FCM 负载，也会扩大日志、第三方基础设施和调试工具泄露内容的范围。

高优先级 FCM 只适合时间敏感、用户可见的内容。Google 明确说明，如果高优先级消息长期不产生用户可见通知，后续消息可能被降级。因此：

- 新剪贴板通知应当可见，但默认隐藏具体内容。
- 多次快速复制应合并通知，避免通知轰炸。
- `onMessageReceived()` 只做短操作；需要拉取内容时使用 expedited WorkManager。
- 普通优先级消息在 Doze 状态可能延迟，不能承诺秒级到达。

#### 自动写入系统剪贴板

`setPrimaryClip()` 没有与 `getPrimaryClip()` 相同的输入焦点限制。因此，在 FCM 成功唤醒进程并完成解密后，技术上可以尝试直接写入系统剪贴板。

但该能力应满足以下要求：

- 默认关闭，由用户主动开启。
- 同时显示通知，说明剪贴板已经被哪台设备更新。
- 只处理短文本，图片和文件仍需用户确认。
- 如果用户在远端事件产生之后又复制了本地内容，不应覆盖本地最新内容。
- OEM 省电策略可能导致 Push 或后台处理延迟，因此只能定义为“尽力而为”。
- 必须在 Pixel、Samsung、小米/Redmi、OPPO/一加等真机上验证。

### 高级模式：默认输入法

Android 官方允许当前默认输入法在没有普通 Activity 输入焦点时读取剪贴板。可以开发一个 PasteBox 输入法，通过下面的组合接近全自动：

```text
PasteBox 是当前默认输入法
  +
可见 Foreground Service 保持进程和监听器
  +
OnPrimaryClipChangedListener 发现变化
  +
读取、过滤、加密和上传
```

输入法还可以提供“云剪贴板”面板，用户点击历史内容后通过 `InputConnection.commitText()` 或等价接口直接插入当前输入框，不需要先覆盖系统剪贴板。

该方案的限制很明显：

- 同一时刻只有一个当前输入法，用户可能需要放弃 Gboard、搜狗等常用输入法。
- 如果只做简陋的剪贴板键盘，用户不会愿意长期设为默认输入法。
- 真正可替代日常键盘的输入法开发量远大于剪贴板客户端本身。
- 密码输入框必须隐藏内容且不能保存密码；部分应用会禁用个性化学习。
- 进程仍可能被 OEM 杀死，需要真机验证 InputMethodService 和前台服务组合。
- Android 14+ 前台服务必须声明类型。若使用 `specialUse`，用途说明会在 Google Play 提交时被审核。

推荐把输入法模式放在第二阶段，标记为“高级实验功能”，不要作为第一版核心承诺。也可以考虑拆成独立的 PasteBox Keyboard 配套应用，避免主应用因为高敏感权限降低信任度。

### 不建议采用的 Android 路径

#### 只运行 Foreground Service

只能保持进程，不能获得输入焦点，不能解决后台读取问题。

#### 无障碍服务

无障碍服务的主要用途是帮助残障用户操作设备。它不在 `ClipboardManager` 的后台读取豁免条件中，单独启用也不能可靠读取剪贴板。

利用无障碍监听复制按钮、读取界面内容或自动操作其他应用会带来高隐私风险。Google Play 要求权限声明、显著披露和用户同意，并禁止不符合核心无障碍目的的自主操作。PasteBox 不应把它作为正式版同步方案。

#### 透明 Activity 或悬浮窗抢焦点

这类方式会打断用户当前应用、产生闪屏或依赖系统漏洞，属于绕过平台隐私边界，不应实现。

#### Root、ADB 或系统签名权限

可以用于企业受控设备或内部研究，但不适用于 Google Play 面向普通用户发布的产品。

## iOS 系统能力边界

### Pasteboard 隐私确认

iOS 16 起，应用直接通过 `UIPasteboard` 读取其他应用写入的内容时，系统会确认用户意图。Apple 提供三条不弹出该确认的标准路径：

- 系统编辑菜单中的“粘贴”。
- 外接键盘的粘贴快捷键。
- UIKit 的 `UIPasteControl`。

普通自定义按钮点击后直接读取 `UIPasteboard.general`，不等同于 `UIPasteControl`，不能假定系统会把它视为标准粘贴动作。

`UIPasteboard.detectedPatterns(for:)` 只返回内容模式，不返回实际内容，因此不会触发读取内容通知。它可以用于判断剪贴板中是否可能有 URL、文本或数字，然后提示用户点击标准粘贴控件，但不能替代真正读取。

`changeCount` 可以用来判断剪贴板是否发生变化。它同样只能作为变化信号，不能绕过内容访问确认。

### 后台运行限制

iOS 应用进入后台后通常会被挂起，不再持续获得 CPU 时间。以下机制都不能实现常驻剪贴板监听：

- `BGAppRefreshTask`：系统决定何时执行，适合偶尔刷新。
- `BGProcessingTask`：适合可延迟的大任务，不是实时监听器。
- `BGContinuedProcessingTask`：用于完成用户明确发起的工作，不是无限后台服务。
- 静默 Push：低优先级、不保证送达，还可能被限流。
- 普通计时器：应用挂起后不会继续可靠运行。

Apple 的后台 Push 文档明确建议不要尝试每小时发送超过两三条后台通知。剪贴板可能一分钟变化多次，所以静默 Push 不能承担逐条实时同步。

用户从多任务界面强制关闭应用后，系统会把它理解为不希望应用继续后台运行，静默通知和后台刷新都不能作为恢复手段。只有用户重新打开应用，相关后台能力才可能恢复。

### Universal Clipboard 不能被第三方复用

Apple 自带的 Universal Clipboard 能在 Apple 设备之间实现接近无感的复制粘贴，但这是系统 Continuity 能力。公开的 UIKit 接口仍然受 Pasteboard 隐私和后台执行限制，第三方应用不能把 Universal Clipboard 当作可调用的通用同步 API。

## iOS 推荐方案

### Share Extension：推荐作为主要上传入口

Share Extension 适合将用户当前选中的文本、URL、图片或文件提交到 PasteBox：

```text
选中内容 -> 分享 -> PasteBox -> 同步
```

实现要点：

- 使用 `NSExtensionContext` 和 `NSItemProvider` 读取用户明确分享的内容。
- 只声明实际支持的 UTType，避免扩展出现在无法处理的内容中。
- 文本可以快速加密后直接上传。
- 大文件先写入 App Group 队列，再使用后台 `URLSession` 上传。
- 扩展调用完成方法后要准备被系统终止，不能依赖扩展进程长期存活。
- 登录令牌和设备密钥通过 Keychain Access Group 与主应用共享；队列和密文通过 App Group 共享。

这是 iOS 上处理图片和文件最可靠的路径。

### 快捷指令和 App Intent：推荐作为一键入口

主应用可以提供至少两个 App Intent：

- “同步剪贴板”：接受文本、URL 或文件输入，加密后上传。
- “复制最新内容”：拉取或读取本地最新历史，写入系统剪贴板。

用户可以在 Shortcuts 中组合：

```text
获取剪贴板
  -> PasteBox：同步剪贴板
```

以及：

```text
PasteBox：获取最新内容
  -> 拷贝至剪贴板
```

这类快捷指令可以从桌面、Siri、Spotlight、Action Button、轻点背面等用户主动入口触发。PasteBox App Intent 接收的是 Shortcuts 显式传入的数据，不需要自己在后台轮询 `UIPasteboard`。

不能承诺用户安装应用后这些系统入口会自动配置。客户端需要提供引导，但最终启用和触发都由用户完成。

### 主应用前台同步

主应用进入前台后可以：

1. 拉取服务器增量事件并写入本地历史。
2. 比较 `changeCount` 或使用模式检测判断剪贴板是否变化。
3. 显示 `UIPasteControl`，提示用户“同步此剪贴板”。
4. 用户点击后读取、过滤、加密并上传。
5. 根据用户设置，将最新远端文本写入 `UIPasteboard.general`。

如果系统设置中已经允许该应用从其他应用粘贴，前台体验可能更顺畅，但产品不能依赖某个系统版本的具体设置项，也不能假定该授权永久有效。

### APNs 通知：负责提醒，不负责保证实时执行

推荐使用普通可见通知：

```text
来自 MacBook 的新剪贴板
[打开] [复制]
```

通知负载只携带事件 ID、来源设备显示名和内容类型，不携带明文内容。客户端打开后拉取密文、解密并复制。

“复制”通知动作可以作为 PoC 项目：系统可能把动作回调交给后台应用处理，此时尝试从本地缓存解密并写入 `UIPasteboard.general`。但 Apple 官方没有把“后台通知动作修改全局 Pasteboard”描述为稳定、持久的后台能力，因此：

- 不能作为唯一接收路径。
- 内容未预取时应打开主应用完成。
- 必须在不同 iOS 版本、锁屏状态、低电量模式和强制关闭状态下真机验证。
- 如果验证结果不稳定，按钮应改为打开极简复制页面。

静默 Push 只用于偶尔预取或刷新游标。不能为每次复制发送静默 Push，也不能用它承诺秒级同步。

### 自定义键盘：适合远端内容插入

PasteBox Keyboard 可以显示最近的云剪贴板历史：

```text
用户进入任意普通文本框
  -> 切换到 PasteBox Keyboard
  -> 点击历史内容
  -> textDocumentProxy.insertText()
```

优点是内容可以直接插入当前输入框，不需要先修改全局剪贴板。

限制包括：

- 默认键盘扩展没有网络权限，也不能与主应用共享容器。
- 开启 `RequestsOpenAccess` 后才能访问网络和共享容器，用户会看到 Full Access 的高敏感授权提示。
- 安全输入框会强制切回系统键盘。
- 电话号码键盘等特殊输入区域不会使用第三方键盘。
- 银行、医疗等应用可以完全拒绝第三方键盘。
- 键盘不能读取任意选中文本，也不能把光标附近内容当成完整剪贴板。
- 不应收集或上传用户键入的按键内容。

因此键盘适合作为“远端历史快速插入”功能，不适合作为 iOS 后台剪贴板上传绕过方案。

### 不建议采用的 iOS 路径

- 后台计时器持续轮询 `UIPasteboard`：应用会被挂起。
- 为每条剪贴板发送静默 Push：不保证送达并会被限流。
- 伪装成音频、定位、VoIP 等后台用途保持运行：违反 App Store 后台服务用途要求。
- 利用通知扩展偷偷读取或改写其他应用数据：权限边界不成立，审核风险高。
- 把 Full Access 键盘当作后台键盘记录器：严重违反用户信任和隐私要求。

## 两个平台的推荐交互

### Android 普通模式

```text
上传：
  App 前台复制 -> 自动同步
  其他 App -> 分享到 PasteBox
  其他 App -> 点击快捷磁贴同步当前剪贴板

接收：
  FCM -> 本地历史 -> 通知
  用户点“复制” -> 写入系统剪贴板
```

设置项建议：

- 前台自动同步：默认开。
- 收到内容时自动写入系统剪贴板：默认关。
- 显示通知内容预览：默认关。
- 同步敏感内容：默认关。
- 仅 Wi-Fi 上传图片/文件：默认开。

### Android 高级模式

```text
默认输入法监听 -> 自动上传
云剪贴板键盘面板 -> 点击插入
```

设置页必须清楚说明：为什么需要默认输入法、会读取哪些内容、哪些内容永远不会同步、怎样暂停和撤销权限。

### iOS

```text
上传：
  分享到 PasteBox
  快捷指令“同步剪贴板”
  主 App 的 UIPasteControl

接收：
  APNs -> 打开并复制
  快捷指令“复制最新内容”
  PasteBox Keyboard -> 点击插入
```

iOS 页面不应显示“后台自动监听已开启”之类无法兑现的状态。可以显示：

- 上次从服务器刷新时间。
- 待复制内容数量。
- 快捷指令是否已安装的操作引导。
- Share Extension 和 Keyboard 的启用状态。

## 推荐同步架构

### 客户端组件

移动客户端至少需要：

- `CaptureAdapter`：封装前台、分享、快捷指令、输入法等来源。
- `SensitiveContentFilter`：本地敏感内容检查。
- `CryptoEngine`：客户端加解密和设备密钥管理。
- `SyncQueue`：离线队列、重试和幂等。
- `EventStore`：本地剪贴板历史和同步游标。
- `PushCoordinator`：FCM/APNs 注册、续期和事件唤醒。
- `ClipboardApplier`：写系统剪贴板或插入当前输入框。

即使使用 Flutter，共享层也只能覆盖网络、加密、数据库和主要界面。以下能力仍需要原生 Kotlin/Swift：

- Android ClipboardManager、TileService、InputMethodService、通知动作。
- iOS Share Extension、App Intent、Keyboard Extension、Keychain/App Group、通知扩展。

### 服务端接口

建议新增独立同步领域，不把每次复制直接创建成现有 Paste：

```text
POST   /api/v1/devices/register
GET    /api/v1/devices
DELETE /api/v1/devices/{device_id}

POST   /api/v1/sync/events
GET    /api/v1/sync/events?after={cursor}&limit={n}
POST   /api/v1/sync/events/{event_id}/ack
POST   /api/v1/sync/push-tokens
DELETE /api/v1/sync/push-tokens/{token_id}
```

建议事件字段：

```text
event_id
cursor
origin_device_id
content_type
ciphertext
encrypted_key_version
content_hash
size_bytes
created_at
expires_at
```

服务端不需要知道剪贴板明文。图片和文件的密文可以存入现有 S3/R2 基础设施，事件只保存对象键和加密元数据。

### 去重和循环防护

以下情况必须处理：

```text
Windows 上传 A
  -> Android 自动写入 A
  -> Android 监听到 A
  -> 再次上传 A
  -> Windows 再次写入 A
```

建议同时使用：

- `origin_device_id`：知道事件来自哪台设备。
- `event_id`：每次服务端事件的唯一标识。
- `content_hash`：客户端对规范化内容计算哈希。
- `last_applied_event_id`：记录本机最近应用的远端事件。
- 应用远端内容后的短时间抑制窗口。

纯文本规范化要保守。不能随意裁剪空格或换行，否则代码、密钥和格式化文本会被错误合并。

### 冲突规则

不建议简单采用“服务器时间最后写入者获胜”，因为移动设备时钟、网络延迟和离线队列会造成错序。

推荐：

- 服务端使用单调递增游标确定事件顺序。
- 客户端历史保留多个事件，不因新事件删除旧事件。
- 自动写入系统剪贴板前检查本机剪贴板是否在远端事件产生后被用户修改。
- 检测到本地更新时，将远端事件放入“待复制”，不强制覆盖。

## 安全和隐私要求

剪贴板常见内容包括密码、验证码、API Token、银行卡号、身份证信息、聊天内容和公司机密。安全要求应当从第一版协议开始，而不是上线后补救。

### 端到端加密

推荐：

- 每个账号拥有同步组密钥或等价的密钥层级。
- 新设备通过二维码或已有设备确认加入。
- 内容在客户端加密后上传。
- 服务端只保存密文和最少必要元数据。
- iOS 密钥存入 Keychain，Android 密钥由 Keystore 保护。
- 设备丢失后可以吊销设备令牌，并轮换后续事件使用的密钥。

密钥恢复方案需要单独设计。没有恢复码时，所有设备丢失意味着历史密文无法恢复；提供服务端托管恢复又会降低端到端加密强度。

### 本地敏感内容过滤

第一版建议默认跳过：

- Android 标记为 `EXTRA_IS_SENSITIVE` 的内容。
- 常见一次性验证码。
- JWT、私钥、云服务 Access Key、GitHub/OpenAI 等常见 Token 形式。
- 银行卡号和明显的密码字段内容。
- 超过大小上限或包含不支持 MIME 类型的内容。

规则只能降低风险，不能保证识别所有秘密。客户端必须提供：

- 一键暂停同步。
- 单次“不要同步”。
- 清空本地和云端历史。
- 按设备撤销访问。
- 历史保留期设置。

### 日志和通知

- 客户端、服务端和 Push 日志不得记录明文剪贴板。
- 崩溃报告不得包含剪贴板内容、密文或密钥。
- 锁屏通知默认只显示来源设备和内容类型。
- 调试构建中的网络抓包和日志开关不能进入正式版默认配置。
- 服务端管理后台不提供查看用户剪贴板明文的能力。

## 应用商店审核风险

### Google Play

- 默认输入法属于高信任组件，必须准确披露网络传输和数据处理用途。
- Android 14+ 的前台服务类型需要在清单和 Play Console 中声明。
- `specialUse` 前台服务需要说明为什么没有其他合适类型。
- 无障碍服务不应作为剪贴板同步捷径。
- Data safety 表单和隐私政策必须覆盖剪贴板内容、账号、设备标识和诊断数据。

### Apple App Store

- 后台模式只能用于声明用途，不能借音频、定位等类型维持剪贴板监听。
- Full Access 键盘必须有清楚、直接且与核心功能相关的用途。
- Share/Keyboard/Notification Extension 必须服务于主应用功能。
- 隐私标签和隐私政策需要覆盖用户内容、账号、设备标识和诊断数据。
- 测试版应通过 TestFlight 分发，不把未完成的后台行为作为正式功能宣传。

## 两周真机 PoC 计划

PoC 的目标不是做完整 App，而是尽早验证系统边界和最危险的假设。

### 第 1 周：Android

#### 验证设备

- Google Pixel 或接近原生 Android 的设备。
- Samsung 设备。
- 小米/Redmi 或 OPPO/一加设备。
- 覆盖项目计划支持的最低 Android 版本和最新稳定版本。

#### 验证场景

1. 前台 Activity 注册监听器后，复制文本能否稳定读取。
2. 同一文本是否因分类状态产生重复回调。
3. 普通 Foreground Service 在后台读取是否按预期返回空。
4. 当前默认输入法配合前台服务时，键盘隐藏后能否持续收到并读取变化。
5. 进程被系统或 OEM 杀死后，输入法/前台服务能否恢复。
6. FCM 高优先级消息在亮屏、锁屏、Doze 和省电模式下的延迟。
7. 通知“复制”动作是否能稳定写入文本剪贴板。
8. 后台自动写入是否触发系统预览，以及是否影响用户当前复制内容。
9. `EXTRA_IS_SENSITIVE` 标记是否能够被识别并跳过。
10. 分享入口对文本、URL、图片和文件 URI 的权限生命周期。

#### Android 通过标准

- 普通模式不依赖无障碍、悬浮窗或默认输入法。
- 通知复制动作在目标机型上可稳定完成。
- 输入法增强模式如果无法在主流机型稳定恢复，直接降级为键盘历史面板，不承诺自动上传。

### 第 2 周：iOS

#### 验证设备

- 至少一台当前主流 iPhone。
- 覆盖计划支持的最低 iOS 版本和最新稳定版本。
- 额外覆盖锁屏、低电量模式、后台刷新关闭和应用被强制关闭状态。

#### 验证场景

1. 直接读取 `UIPasteboard` 的系统确认行为。
2. `UIPasteControl` 是否能在不出现额外确认的情况下取得文本。
3. `detectedPatterns` 和 `changeCount` 能否只作为变化提示使用。
4. Share Extension 上传文本、URL、图片和文件。
5. Share Extension 启动后台 `URLSession` 后被终止，上传能否继续。
6. “获取剪贴板 -> PasteBox App Intent”快捷指令能否完成加密上传。
7. APNs 普通通知在前台、后台和锁屏状态的行为。
8. 通知“复制”动作在内容已缓存和未缓存时的行为。
9. 静默 Push 在低电量、长时间未启动和强制关闭后的实际表现。
10. Full Access 键盘读取本地历史、联网刷新和插入文本。
11. 安全输入框、电话键盘和禁止第三方键盘应用的降级行为。

#### iOS 通过标准

- Share Extension 和快捷指令可以稳定上传文本。
- 主应用可以稳定拉取、解密和复制远端内容。
- 键盘可以在普通文本框插入本地缓存历史。
- 通知动作和静默 Push 无论测试结果多好，都只作为优化，不作为唯一核心路径。

## 建议实施顺序

### 阶段 0：协议和安全设计

- 确定设备配对、设备令牌和撤销流程。
- 确定端到端加密和密钥恢复方案。
- 定义事件、游标、去重、过期和冲突规则。

### 阶段 1：移动 PoC

- 按本文两周清单完成真机验证。
- 把实验结果记录为“保证能力”和“尽力能力”。

### 阶段 2：文本同步 MVP

- Android 普通模式。
- iOS Share Extension、快捷指令和主应用。
- FCM/APNs、文本历史、手动复制。
- 只支持纯文本和 URL。

### 阶段 3：增强体验

- Android 快捷磁贴和可选自动写入。
- iOS Keyboard Extension。
- Android 默认输入法实验模式。
- 图片和文件分享同步。

### 阶段 4：生产强化

- 密钥恢复、设备丢失和密钥轮换。
- Push 可用性监控和降级策略。
- 商店审核材料、隐私政策和数据安全表单。
- 多机型稳定性、耗电和网络质量测试。

## 最终推荐决策

1. 不宣传 Android/iOS 都能完全后台自动监听。
2. Android 第一版不申请无障碍权限，不要求默认输入法。
3. Android 默认输入法只作为后续高级模式，必须先通过真机和商店审核验证。
4. iOS 以 Share Extension 和快捷指令解决上传，以通知、主应用和键盘解决接收。
5. 第一版只支持文本和 URL，图片、文件通过分享入口在第二阶段加入。
6. 从第一版协议开始使用客户端加密、事件游标和循环防护。
7. 先做真机 PoC，再决定 Flutter 共享层与原生扩展的最终边界。

## 官方资料

### Android / Google

- [ClipboardManager API](https://developer.android.com/reference/android/content/ClipboardManager)
- [OnPrimaryClipChangedListener API](https://developer.android.com/reference/android/content/ClipboardManager.OnPrimaryClipChangedListener)
- [复制和粘贴开发指南](https://developer.android.com/develop/ui/views/touch-and-input/copy-paste)
- [安全剪贴板处理](https://developer.android.com/privacy-and-security/risks/secure-clipboard-handling)
- [接收其他应用分享的数据](https://developer.android.com/training/sharing/receive)
- [创建输入法](https://developer.android.com/develop/ui/views/touch-and-input/creating-input-method)
- [前台服务类型](https://developer.android.com/develop/background-work/services/fgs/service-types)
- [FCM Android 消息优先级](https://firebase.google.com/docs/cloud-messaging/android/message-priority)
- [Google Play 无障碍服务政策](https://support.google.com/googleplay/android-developer/answer/10964491?hl=en)

### iOS / Apple

- [WWDC22：What's new in privacy](https://developer.apple.com/videos/play/wwdc2022/10096/)
- [UIPasteboard detectedPatterns](https://developer.apple.com/documentation/uikit/uipasteboard/detectedpatterns(for:))
- [Share Extension 指南](https://developer.apple.com/library/archive/documentation/General/Conceptual/ExtensibilityPG/Share.html)
- [Custom Keyboard 指南](https://developer.apple.com/library/archive/documentation/General/Conceptual/ExtensibilityPG/CustomKeyboard.html)
- [App Intents 示例](https://developer.apple.com/documentation/appintents/acceleratingappinteractionswithappintents)
- [Shortcuts 剪贴板动作](https://support.apple.com/guide/shortcuts/about-actions-in-complicated-shortcuts-apd081d9d61f/ios)
- [后台 Push 更新](https://developer.apple.com/documentation/usernotifications/pushing-background-updates-to-your-app)
- [后台任务](https://developer.apple.com/documentation/backgroundtasks/refreshing-and-maintaining-your-app-using-background-tasks)
- [WWDC25：Finish tasks in the background](https://developer.apple.com/videos/play/wwdc2025/227/)
- [App Store Review Guidelines](https://developer.apple.com/app-store/review/guidelines/)

## 调研复核命令

关键页面通过以下命令抓取并核对正文：

```bash
smart-search fetch "https://developer.android.com/reference/android/content/ClipboardManager" --format json
smart-search fetch "https://developer.android.com/privacy-and-security/risks/secure-clipboard-handling" --format json
smart-search fetch "https://firebase.google.com/docs/cloud-messaging/android/message-priority" --format json
smart-search fetch "https://developer.apple.com/videos/play/wwdc2022/10096/" --format json
smart-search fetch "https://developer.apple.com/documentation/usernotifications/pushing-background-updates-to-your-app" --format json
smart-search fetch "https://developer.apple.com/library/archive/documentation/General/Conceptual/ExtensibilityPG/CustomKeyboard.html" --format json
```
