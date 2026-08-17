# Refactor admin dashboard UI

## Goal

重构 PasteBox 后台页面，让它更像可长期使用的运维控制台：信息层级清楚、运行状态更容易扫读、表单和按钮更稳定，同时不改变现有后台接口和业务流程。

## What I already know

* 用户已确认走“稳妥版”后台 UI 重构方案。
* 后台主要渲染代码在 `web/src/App.tsx`，样式集中在 `web/src/styles.css`。
* 后台数据来自现有 admin API，例如 dashboard、runtime config、runtime panel、manual work items、redemption batches、alerts、queues、audit logs。
* 项目是 Go + React + TypeScript + Vite，生产静态资源会同步到 `internal/httpserver/static/`。
* 当前工作区已有其他未提交变更，本任务只改必要文件，不混入无关改动。

## Assumptions

* 这次只做后台页面的视觉、布局和交互体验重构。
* 不新增后台接口，不改数据库，不改权限模型。
* 不引入新 UI 框架；沿用现有 React/CSS 结构，必要时使用已安装的图标库。

## Requirements

* 后台首页改成清晰的运维控制台布局，核心状态、资源、配置、告警、队列、最近对象分区明确。
* 后台页面保留现有操作能力：保存运行配置、测试 provider、发送测试告警、账单对账、重试扫描、冻结附件、撤销分享、标记订单、重放 webhook、处理举报等。
* 表单、按钮、状态标签、列表卡片要有统一的密度和视觉层级。
* 交互控件满足可点击区域不小于 44px，保留清楚的 focus/hover/disabled 状态。
* 移动端 375px 不出现横向滚动，复杂列表允许内容换行或横向安全滚动。
* 文案继续使用现有 i18n，不把功能说明硬编码进页面。

## Acceptance Criteria

* [ ] 管理员登录后后台页面能正常渲染，不出现空白页或控制台运行时错误。
* [ ] 运行配置、资源状态、告警、队列、用户/附件/分享/订单等后台区块层级清楚。
* [ ] 375px、桌面宽度下页面布局可读、控件不重叠。
* [ ] `make test` 能通过；如果完整测试受环境限制，至少跑前端 typecheck/build 并说明原因。
* [ ] 如前端产物变化，同步 `web/dist` 到 `internal/httpserver/static/`。

## Definition of Done

* 只改必要文件，避免大范围重写。
* 跑相关测试/构建。
* 尽量用浏览器检查后台页面。
* 说明验证结果、风险点和下一步。

## Out of Scope

* 不新增后台业务功能。
* 不改 API contract。
* 不接入新的组件库或复杂图表库。
* 不做生产部署、推送或发布。

## Technical Notes

* `ui-ux-pro-max` 建议后台按 SaaS/operations dashboard 做高对比、低装饰、信息密集但有清楚分区的设计。
* 本任务要避免营销式 hero、装饰性渐变/orb、过大的卡片堆叠。
* 后台样式应该优先使用现有 CSS 变量和局部 class，减少对公共页面的副作用。
