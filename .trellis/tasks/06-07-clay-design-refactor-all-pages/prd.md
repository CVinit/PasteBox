# brainstorm: clay design refactor all pages

## Goal

严格按照 `docs/DESIGN-clay.md` 的 Clay 风格系统，重构 PasteBox 前端所有页面，让公开页、登录注册、分享访问、工作台和管理页使用统一的奶油色画布、近黑主按钮、饱和色功能卡片、圆润排版和 Clay 风格视觉资产。

## What I already know

* 用户要求：`docs/DESIGN-clay.md 严格按照这个文档重构本项目的所有页面`。
* 设计源是本仓库未提交文件 `docs/DESIGN-clay.md`。
* 前端是 React + Vite + TypeScript，入口集中在 `web/src/App.tsx` 和 `web/src/styles.css`。
* 现有页面/视图主要包括：
  * 首页营销页：`LandingPage`
  * 登录/注册/魔法链接/重置/邮箱验证：`AuthScreen`
  * 公开分享页：`PublicShareScreen`
  * 法务/状态/支持等公开文档页：`PublicPageScreen`
  * 登录后工作台：`inbox`、`shared`、`billing`、`settings`、`admin`
* 当前样式仍大量使用冷灰、紫蓝渐变、玻璃拟态、重阴影和装饰网格，和 `DESIGN-clay.md` 的奶油画布 + 饱和单色卡片方向不一致。
* 仓库当前已有多个未提交改动和 Trellis 活跃任务，尤其涉及 admin UI 和 blank page 调试，后续实现必须只纳入本任务实际改动。

## Design Requirements From `docs/DESIGN-clay.md`

* 全站默认画布必须是奶油色 `#fffaf0`，避免冷灰画布。
* 主 CTA 使用近黑 `#0a0a0a`，按钮高度至少 44px，圆角 12px。
* 使用 6 色功能卡片节奏：粉、深青、薰衣草、桃、赭、奶油卡片，避免连续重复同色。
* 页眉、正文、卡片、表单、价格卡、页脚都要走同一套 token，而不是页面各自写散乱颜色。
* 大标题使用 Inter 500 模拟 Plain Black，允许负字距；正文和 UI 使用 Inter。
* 卡片圆角、间距、分栏、移动端折叠要符合文档的 12/16/24px 圆角和 96px section rhythm。
* 页脚保持浅奶油色，不能换成深色 footer。
* 需要产品 UI fragment 和 Clay 风格 3D/黏土视觉资产作为主要视觉信号，不能用纯抽象渐变替代。

## Assumptions

* 本次只重构 Web 前端，不改后端 API、业务状态机、数据库和接口契约。
* 不新增复杂 UI 框架；优先在现有 React/CSS 结构上做必要补丁，减少重写风险。
* “所有页面”包含公开页面和登录后所有 tab，但不包含 `web/dist` 这类构建产物。
* 现有中英文文案和功能流程优先保留，只调整信息层级、布局、视觉组件和必要的展示结构。

## Open Questions

* None. 用户已确认采用 A：生成/加入少量黏土风位图资产。

## Requirements

* 建立 `DESIGN-clay.md` 对应的 CSS design tokens，替换当前冷灰/紫蓝/玻璃拟态视觉基础。
* 重构首页为 Clay 式营销页：奶油画布、64px 顶部导航、7/5 hero、主产品 UI fragment、饱和功能卡片、浅色 footer。
* 重构登录注册页：保留认证功能，视觉上和首页同源，表单控件符合 44px 高度和 12px 圆角。
* 重构公开分享页：分享输入、解锁后内容、附件下载都使用同一套 card/input/button token。
* 重构法务/状态/支持页面：奶油画布、浅色文档卡/侧边导航、无深色 footer。
* 重构登录后工作台：侧边栏、统计、列表、编辑器、计费、设置、管理页全部使用 Clay token；管理页仍保持数据密度和可扫描性。
* 移动端不得出现横向滚动，导航和卡片网格按文档断点折叠。
* 新增少量项目内黏土风位图资产，至少覆盖首页 hero/CTA/footer 和重点功能卡片；资产必须保存在前端静态资源目录，不能只留在生成工具默认目录。

## Acceptance Criteria

* [ ] `web/src/App.tsx` 中所有页面/视图都完成视觉重构，功能事件和 API 调用保持原样。
* [ ] `web/src/styles.css` 中颜色、字体、圆角、间距主要来自 Clay token。
* [ ] 首页、登录页、公开分享页、公开文档页、工作台各 tab、管理页在桌面和移动端都能正常显示。
* [ ] 项目内存在被页面实际引用的 Clay 风格位图资产。
* [ ] 构建通过：`npm run build`（在 `web/` 下）。
* [ ] 可用浏览器或截图检查确认无空白页、无明显重叠、无横向滚动。

## Definition of Done

* TypeScript/Vite 构建通过。
* 关键页面手动或自动浏览器检查通过。
* 不提交/修改 `web/dist` 构建产物，除非用户单独要求。
* 只把本任务实际改动纳入最终提交计划，不混入已有无关 WIP。

## Out of Scope

* 后端接口、鉴权逻辑、支付逻辑、存储逻辑重构。
* 引入新的路由框架或组件库。
* 改写所有业务组件结构到多文件架构，除非实现过程中证明是必要的。

## Technical Notes

* `web/src/App.tsx` 定义主要路由和页面组件，`View = "inbox" | "shared" | "billing" | "settings" | "admin"`。
* `publicPageForPath()` 覆盖 `/legal/*`、`/status`、`/support` 等公开文档页。
* `authModeForPath()` 覆盖 `/register`、`/login`、`/magic`、`/password-reset`、`/email-verification`。
* `/s/:token` 走公开分享页面。
* `web/package.json` 已有 `npm run build` 和 `npm run typecheck`。
* `docs/DESIGN-clay.md` 明确禁止冷灰画布、深色 footer、平面矢量替代 3D 黏土视觉、过重 hover 和 7 色外卡片扩展。
