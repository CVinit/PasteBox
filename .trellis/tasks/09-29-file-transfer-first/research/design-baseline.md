# 技术设计核验与基线

## 实际执行

- `go test ./internal/app -run '^TestShareVisitAndDownloadLimitsAreSeparate$' -count=1`：通过，退出码 0。
- `go test ./internal/worker -run '^TestRunner(CompletesCleanupJob|RetriesFailedJobWithBackoff)$' -count=1`：通过，退出码 0。
- `go test ./internal/httpserver -run '^Test(AuthPasteUploadShareAndQuotaHTTPContracts|GuestAttachmentUploadPreflightsBeforeFileContent)$' -count=1`：通过，退出码 0。
- design.md 中列出的代码路径存在，行尾空白检查通过。
- GitHub Issue #4 无新增评论；本地任务仍是 planning，未开始应用代码修改。

## 结果解释

以上只验证已有访问/下载限额分离、清理任务/失败重试、HTTP 上传分享配额与游客上传预检查，共 5 个定向测试。

本轮没有新增功能实现；没有跑全量测试、真实 PostgreSQL 并发测试、浏览器流程、前端构建或生产部署验证。

## 后续关键检查

- 新领取名额必须使用数据库原子计数，不能套用旧 MaxVisits/MaxDownloads。
- 新会话最长 30 分钟与旧分享 Cookie 的 15 分钟分别处理。
- 新传输内容必须与当前选中的历史笔记区分，销毁遵循对象引用计数。
- 状态通道、文本重试恢复及正在下载流的失效策略仍需实施前验证。
