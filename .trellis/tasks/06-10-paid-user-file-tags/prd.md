# 付费用户文件 Tag 功能

## Goal

给 PasteBox 的付费用户增加更清晰的 Tag 能力，让用户能给文本、文件、图片内容打标签，并用标签更快找回内容。不同套餐可使用的 Tag 数量不同，免费用户默认不开放或只保留极低额度，避免把付费权益做虚。

## What I already know

* 用户希望给付费用户增加 Tag 功能，方便搜索文件或图片。
* 不同付费等级可添加的 Tag 数量不同。
* 用户已确认 Tag 限额按“每条内容最多几个 Tag”计算，而不是按账号级 Tag 库计算。
* 用户已确认默认套餐上限：`free` 每条内容 0 个 Tag，`plus` 每条内容 5 个 Tag，`pro` 每条内容 20 个 Tag。
* 用户已确认前端 MVP 入口：内容卡片显示已有 Tag chip，点击某个 Tag 后按该 Tag 筛选内容。
* 用户已确认免费用户的 Tag 输入入口显示但禁用，并提示升级。
* 用户已确认套餐过期或降级后，已有 Tag 保留并可搜索，但不能新增或修改 Tag；续费/升级后恢复编辑。
* 用户已确认范围选择 B：本次不做完整标签管理页或批量接口，但字段、接口、校验函数命名要给后续批量整理/标签管理留余地。
* 当前默认套餐有 `free`、`plus`、`pro` 三档。
* 当前后端已经有基础 `tags` 字段：`Paste` / `PasteView` / `PasteInput` / `PastePatch` 都带 `Tags`。
* PostgreSQL `pastes.tags` 当前是 `jsonb`，并已有 `pastes_tags_gin_idx`。
* 当前创建、编辑 paste 都支持提交 tags，后端会用 `normalizeTags` 做小写、trim、逗号拆分、去重、排序。
* 当前搜索会匹配标题、正文、tags 和附件文件名，也支持 `ListOptions.Tag` / `GET /api/v1/pastes?tag=`。
* 当前前端搜索参数只传 `query` 和 `filter`，还没有把 `tag` 参数接入 UI。
* 当前 `Plan` 只有容量、数量、保留时间、上传/下载额度等字段，没有 Tag 数量字段。
* 管理后台已有套餐编辑界面，保存套餐会写入 PostgreSQL `plans` 表。

## Assumptions (temporary)

* Tag 数量限制应该是套餐能力，而不是写死在业务代码里。
* Tag 能力应覆盖已登录用户的文本 paste、文件 paste、图片 paste。
* 游客临时上传不作为本次重点，避免和付费权益混在一起。

## Open Questions

* 无，等待最终确认。

## Requirements (evolving)

* 套餐模型需要有 Tag 数量限制字段。
* 默认套餐上限为 `free=0`、`plus=5`、`pro=20`，均按每条内容计算。
* Tag 限额按每条 paste/content 计算；同一条内容的 Tag 数超过当前套餐上限时拒绝保存。
* 后端创建和更新 paste 时必须按用户当前套餐校验 Tag 数量。
* 超过套餐 Tag 限制时返回稳定错误码，前端能展示失败信息。
* 前端需要保留创建/编辑 tags 能力，并提供内容卡片上的 Tag chip 点击筛选入口。
* 当前套餐 Tag 上限为 0 的用户仍能看到 Tag 输入入口，但输入框禁用并提示升级。
* 套餐过期或降级后，已有 Tag 保留展示并仍可搜索；用户不能新增、修改 Tag，直到重新升级到有 Tag 上限的套餐。
* 付费套餐的 Tag 限制要能通过现有套餐接口暴露给前端。
* 实现时给后续批量/管理能力留余地：Plan JSON 字段使用清晰的 `tagsPerPasteLimit` 语义；后端集中做 Tag 归一化和套餐校验；列表接口继续保留 `tag` 查询参数。
* 管理后台套餐编辑需要能查看和修改每条内容 Tag 上限。

## Acceptance Criteria (evolving)

* [ ] `free`、`plus`、`pro` 能配置不同 tag 上限。
* [ ] 默认 catalog 中 `free` 的每条内容 tag 上限为 0，`plus` 为 5，`pro` 为 20。
* [ ] Plan API/前端类型/管理后台都包含每条内容 Tag 上限字段。
* [ ] 创建 paste 时超过当前套餐 tag 上限会被拒绝。
* [ ] 编辑 paste 时超过当前套餐 tag 上限会被拒绝。
* [ ] 同一账号可以在多条内容中复用相同 tag，不受账号级唯一 Tag 数限制。
* [ ] 列表搜索可按 tag 精确筛选。
* [ ] 内容卡片显示已有 Tag chip，点击 chip 后列表按该 Tag 筛选。
* [ ] 免费用户看到禁用的 Tag 输入入口和升级提示，不能提交新 Tag。
* [ ] 降级/过期用户的已有 Tag 仍展示和可搜索，但保存 Tag 修改会被禁止。
* [ ] 后端单元测试覆盖套餐 tag 上限。
* [ ] 如涉及 PostgreSQL schema，迁移和 catalog 读写测试更新。

## Technical Approach

* 在套餐模型上新增 `tagsPerPasteLimit`，默认值为 `free=0`、`plus=5`、`pro=20`。
* PostgreSQL `plans` 表增加对应列；默认 catalog、catalog 读取、catalog 保存、管理后台编辑同步更新。
* 后端在创建和更新 paste 时复用集中校验：先 `normalizeTags`，再按当前用户套餐检查数量；错误码建议为 `tag_limit`。
* 降级/过期用户已有 Tag 不做清理；搜索和展示继续读已有 `pastes.tags`。只有当请求尝试保存 Tag 变化或新增超限 Tag 时才拒绝。
* 前端按当前套餐上限控制 Tag 输入：上限为 0 时禁用输入并显示升级提示；上限大于 0 时显示当前数量/上限。
* 内容卡片渲染 Tag chip；点击 chip 设置 `tag` 筛选参数并刷新列表，保留现有 query/filter 搜索能力。
* 为未来标签管理预留：保持 API 的 `tags: string[]` 形态和 `tag` 查询参数稳定，不在本次引入账号级标签表、批量重命名接口或独立标签管理 UI。

## Decision (ADR-lite)

**Context**: 现有代码已经有 paste 级 `tags` 字段、PostgreSQL jsonb 存储和基础搜索能力，但套餐没有 Tag 上限，前端也没有 Tag 筛选入口。

**Decision**: 本次把 Tag 做成付费套餐能力，按每条内容限制数量。实现核心校验、套餐配置、Tag chip 点击筛选，并为后续标签管理保留稳定字段和接口形态。

**Consequences**: MVP 可以较快上线，不破坏现有 paste 数据；未来若要做账号级标签库或批量管理，可能需要新增表和接口，但当前 `tags` 数组和 `tag` 查询参数可以继续兼容。

## Definition of Done

* Tests added/updated for backend and frontend behavior where appropriate.
* Lint / typecheck / project checks pass.
* Public API shape and plan catalog shape保持前后端一致。
* 如修改数据库 schema，迁移可重复执行，旧数据有合理默认值。

## Out of Scope (explicit)

* 暂不做复杂 Tag 管理页、批量重命名、Tag 合并。
* 暂不做标签批量管理 API；本次只做命名和接口形态预留。
* 暂不做账号级 Tag 库总量限制。
* 暂不做跨用户公开标签、推荐标签或 AI 自动打标。
* 暂不做全文搜索引擎或独立索引服务。
* 暂不把游客临时 paste 的 Tag 能力作为付费权益重点。

## Technical Notes

* `internal/plans/plans.go`：当前 `Plan` 需要新增 tag limit 字段。
* `internal/app/app.go`：`CreatePaste`、`UpdatePaste`、`normalizeTags`、`matchesPaste` 是核心路径。
* `internal/httpserver/server.go`：`listPastes` 已读取 `tag` 查询参数。
* `internal/postgres/migrations/000001_initial_schema.sql`：`plans` 表和 `pastes.tags` 已存在，需要新增可迁移的 plan 字段。
* `internal/postgres/catalog.go`：catalog 读写 SQL 需要同步 plan 字段。
* `web/src/api.ts`：`Plan` 类型需要同步字段。
* `web/src/App.tsx`：创建/编辑 tags 已有输入框；`searchParams` 暂未传 `tag`。

## Implementation Plan

* PR1: 套餐模型和持久化。新增 `tagsPerPasteLimit` 字段，更新默认 catalog、PostgreSQL schema/catalog 读写、管理后台套餐编辑。
* PR2: 后端业务规则。集中 Tag 限额校验，覆盖创建、编辑、降级/过期只读语义和错误码测试。
* PR3: 前端体验。Tag 输入禁用/升级提示、数量上限展示、内容卡片 Tag chip、点击 Tag 筛选。
* PR4: 验证收尾。跑 Go 测试、前端 typecheck/build；如有必要更新规格或说明。
