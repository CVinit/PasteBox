# 移动端剪贴板同步调研文档

## Goal

将已完成的 Android 与 iOS 剪贴板同步调研整理为一份可长期维护的中文文档，为后续移动客户端产品边界、技术架构和 PoC 验证提供依据。

## Requirements

- 新增 `docs/mobile-clipboard-sync-android-ios-research.zh-CN.md`。
- 分别说明 Android 和 iOS 的系统限制、可行方案、替代路径与应用商店审核风险。
- 明确区分本机剪贴板上传与远端内容写入本机剪贴板。
- 给出推荐的移动端交互、客户端架构、服务端对接需求和安全要求。
- 包含分阶段实施建议、两周 PoC 验证清单和官方资料链接。
- 保持网页版现有功能和现有部署文档不变。

## Acceptance Criteria

- [x] 文档为中文，结构完整，可独立阅读。
- [x] Android 与 iOS 的后台能力边界表述准确，不承诺系统无法保证的行为。
- [x] 推荐方案区分低风险主路径和高风险增强路径。
- [x] 所有关键平台结论都附有对应官方资料链接。
- [x] Markdown 格式检查通过，Git 差异只包含本任务所需文件。

## Definition of Done

- 文档内容经过人工结构检查和链接提取检查。
- 未修改业务代码、现有网页功能或部署配置。
- 明确记录尚需真机 PoC 验证的推断性能力。

## Technical Approach

以官方 Android Developers、Google Play、Firebase、Apple Developer 和 Apple Support 文档为主要证据，按“系统边界、可行路径、推荐产品方案、技术架构、安全与审核、PoC”组织内容。对官方未明确保证的行为标记为需真机验证，不作为核心能力承诺。

## Decision (ADR-lite)

**Context**：四端剪贴板同步中，移动系统对后台读取和运行有严格限制，不能直接照搬桌面端方案。

**Decision**：Android 采用普通模式为主、默认输入法增强模式为辅；iOS 采用 Share Extension、快捷指令、通知和键盘面板组合，不承诺持续后台自动监听。

**Consequences**：产品文案必须使用“系统允许范围内自动同步”；移动端需要保留待复制历史，不能把服务端已收到等同于设备系统剪贴板已更新。

## Out of Scope

- 不编写 Android、iOS 或服务端代码。
- 不选择最终移动端 UI 框架。
- 不修改网页版现有行为。
- 不在本任务中完成真机 PoC。

## Technical Notes

- 调研结论基于 2026-08-07 获取的官方资料。
- 目标文档：`docs/mobile-clipboard-sync-android-ios-research.zh-CN.md`。
- Android 关键依据：`ClipboardManager`、安全剪贴板、FCM 优先级、前台服务类型、输入法和无障碍政策。
- iOS 关键依据：Pasteboard 隐私、Share Extension、App Intents、后台推送、后台任务、自定义键盘和 App Store 审核指南。
