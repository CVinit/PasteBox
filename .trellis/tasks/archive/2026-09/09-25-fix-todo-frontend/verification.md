# 验证记录

日期：2026-09-25。目标为根目录 TODO 的 7 项，使用独立本地 PostgreSQL 数据库和开发账号验证。

## 自动检查

- `make production-readiness` 通过：项目测试、前端类型检查/构建、依赖审计、部署配置检查、PostgreSQL 集成测试、覆盖率门槛及 Docker 构建。
- 全项目覆盖率 **75.3%**，高于 75% 门槛。
- `go vet ./cmd/... ./internal/...` 通过。
- `go test -race ./internal/app ./internal/httpserver ./internal/postgres` 通过，包含真实 PostgreSQL 测试。
- 移动端按钮布局补丁后再次执行 `make test-web build`，重新生成并嵌入静态资源；`check-web-launch-surfaces.mjs`、Prettier 与 `git diff --check` 通过。
- 新回归测试覆盖部分保存、其他数据保留、非法输入、权限、严格 JSON 解码和审计失败时的事务回滚。

## 浏览器检查

使用 Tabbit，桌面 1440×1000、手机 375×900。

| TODO | 实际验证结果 |
| --- | --- |
| 1 | 安全配置页面输入站点密钥/服务端密钥，启用 Turnstile 后保存成功；再次留空保存不清除密钥。公开配置返回新站点密钥与启用状态。 |
| 2 | 匿名首页显示登录/注册；登录后回首页显示管理员名称及工作区入口。 |
| 3 | 有效会话访问 `/register`、`/login` 都进入 `/app`；暂停 `/api/v1/me` 响应时只显示会话加载提示，没有密码输入框。 |
| 4 | 最近内容的五个按钮显示文字和图标；点击置顶、收藏后文字和 `aria-pressed` 同步更新。截图发现旧 grid 导致拥挤，补丁后重新截图确认。 |
| 5 | 空套餐名称触发真实后端校验错误，错误在保存按钮附近且草稿保留；成功保存显示局部反馈，取消重复顶部消息。 |
| 6 | 套餐、价格、兑换码分组；单次目录请求只有一个套餐或价格，不包含衍生支付字段。编辑其他条目后切回保存，草稿仍在、后端其他条目未变。应用配置只提交当前分组，并保留其他分组草稿。 |
| 7 | 页脚链接计算样式有下划线，键盘 Tab 后有实线聚焦轮廓；桌面截图确认链接与分组文字有区分。 |

en、zh-CN、zh-TW、es 均检查了新增分组/价格文案和六个应用配置分组；375px 下未发现横向溢出，浏览器 `pageerror` 为空。

关键浏览器请求记录：`login-and-routes`、`security-saved-state`、`security-empty-and-plans`、`catalog-draft-isolation`、`price-save-and-config-groups`、`localized-groups-mobile`、`delayed-session-probe`、`final-mobile-actions`、`footer-and-final-drafts`、`desktop-save-visual`。

## 范围和边界

- Turnstile 使用本地测试密钥验证配置链路，未验证 Cloudflare 的真实挑战；生产使用时仍需站点自己的有效密钥。
- 安全配置先保存 Turnstile 密钥，再保存注册/限流开关；第二步失败时显示错误并保留草稿，第一步已保存的密钥不会回滚。
- 本轮仅本地提交，没有推送或部署。初始无关文档、截图、Trellis 模板哈希及旧未跟踪资源保留。
