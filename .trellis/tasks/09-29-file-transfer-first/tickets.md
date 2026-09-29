# 已发布纵向工单

父 Issue #4 未修改；各工单均为 OPEN，带 ready-for-agent 标签。已回读核验正文及原生阻塞边。

| 工单 | 阻塞于 |
| --- | --- |
| [#5 [文件中转] 单文件发送与链接接收闭环](https://github.com/CVinit/PasteBox/issues/5) | 无 |
| [#6 [文件中转] 多文件队列与失败重试](https://github.com/CVinit/PasteBox/issues/6) | #5 |
| [#7 [文件中转] 6 位取件码接收](https://github.com/CVinit/PasteBox/issues/7) | #5 |
| [#8 [文件中转] 内联时效与隐私设置](https://github.com/CVinit/PasteBox/issues/8) | #5, #7 |
| [#9 [文件中转] 文本／图片模式与原有内容管理兼容](https://github.com/CVinit/PasteBox/issues/9) | #5 |
| [#10 [文件中转] 匿名领取名额与领取会话](https://github.com/CVinit/PasteBox/issues/10) | #6, #8, #9 |
| [#11 [文件中转] 阅后即焚与可靠清理](https://github.com/CVinit/PasteBox/issues/11) | #10 |
| [#12 [文件中转] 跨设备状态与文本保存同步](https://github.com/CVinit/PasteBox/issues/12) | #10 |

当前可开始：#5。#11 与 #12 可在 #10 完成后独立开始，后完成者负责销毁状态同步联调。

用户本轮已确认工单粒度、依赖与销毁条件：全部名额已领取且所有会话结束，或分享到期后销毁。父规格保留发布时原文，本确认记录于 #11 与本地 ADR。
