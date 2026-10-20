# 验收记录

日期：2026-10-20。

## 已通过的检查

- `make test`：Go 各包、前端 35 条测试、TypeScript 类型检查、Vite 构建通过。
- `make test-postgres`：临时 PostgreSQL 17 容器中的真实集成测试通过，执行后自动清理。
- 最后窄屏样式修正后再次运行 `make test-web` 和 `make build-api` 均通过；Go 可执行文件已构建，嵌入资源与 web/dist 逐文件内容一致。
- `git diff --check` 通过。

## 浏览器验收

使用本地真实 Go HTTP 服务和 26 位普通用户、26 个文件与分享的隔离内存数据；所有管理操作真实发出 HTTP 请求，无 API 拦截或伪造响应，不接项目真实数据库/Redis。

- 用户：第一页 20 条、第二页 7 条；搜索跨页定位；冻结/解冻；套餐改为 Plus、到期时间保存后刷新保留；当前登录账号冻结按钮禁用；调整自身套餐后侧栏用量套餐同步更新。
- 文件：第一页 20 条、第二页 6 条；长中文文件名搜索；冻结/解冻；重新扫描后显示等待扫描；下架内容后 API 返回 pasteStatus=taken_down，界面显示已下架且禁用文件操作。
- 分享：分页和真实撤销通过，撤销后状态中文显示、操作按钮禁用；过期/撤销的四语言状态另有 SSR 回归测试。
- 套餐开关：关闭后工作区会员入口立即隐藏，另开官网标签的套餐区隐藏；重新开启后两处恢复。重启持久化、失败不修改状态与旧 JSON 默认显示由 app 测试覆盖。
- 审计：中文操作名、时间列、可展开完整用户/对象 ID；审计表内移动端横向滚动，不撑开整页。分页和长 ID 的四语言 SSR 测试通过。
- 1440px、375px、320px：12 个后台导航项逐个检查，document/admin-panel/admin-grid 均无横向溢出；手机点击后菜单收起。补查 390px、1024px 文件页通过。
- en、zh-CN、zh-TW、es：桌面全部 12 个页面和手机审计页检查通过；未出现 activeStorageBytes、smtp.password、unitSeconds、stateRevoked 等未翻译键名。
- 最终加载的页面控制台 0 错误、0 警告。之前未登录 /me 探测的 401、预览进程重启导致的旧会话 401 单独排除，不作为已登录页面错误。

## 预览与截图

- 本地预览：http://127.0.0.1:18345/app （仅开发进程运行期间可用）。
- 管理员：preview-admin@example.com；测试密码：preview-admin-password。
- `.cache/admin-management-preview/admin-audit-desktop.png`
- `.cache/admin-management-preview/admin-audit-mobile.png`
- `.cache/admin-management-preview/admin-files-desktop.png`

## 边界

- 没有部署或推送，未触碰已有真实业务数据。浏览器使用真实服务加隔离内存存储；数据库路径由真实 PostgreSQL 集成测试覆盖。
- 审计仍沿用后端最近 100 条的窗口，在前端每页 20 条；未扩展历史审计查询接口。
- 套餐开关只控制前台展示，不改权益或购买校验；可见价格与允许购买仍分别控制。
- 自动验收不等同用户对视觉风格的最终确认。
- 起始已有 AGENTS.md、workspace 日志、其他任务、docs/agents、研究文档、截图及辅助目录改动，均排除在后台功能提交外。
