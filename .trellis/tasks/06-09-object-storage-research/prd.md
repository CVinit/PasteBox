# 对象存储免费与付费方案调研

## Goal

保存 PasteBox 对象存储免费额度调研结果，并补充付费对象存储方案调研，为游客临时上传、免费用户、付费用户的存储策略提供依据。

## What I Already Know

- 用户要求全程中文，并把调研结果实际保存到文档中。
- 上一轮已调研免费对象存储，重点结论是游客 5MB、15 分钟删除的临时文件可主要依赖 Cloudflare R2 免费额度。
- 付费调研需要重点比较存储单价、出站流量、请求费、最低消费、S3 兼容性和适合 PasteBox 的程度。

## Requirements

- 新增中文 Markdown 文档保存调研结果。
- 覆盖免费对象存储和付费对象存储。
- 付费对象存储至少比较 Cloudflare R2、Backblaze B2、Tigris、IDrive e2、Wasabi、Hetzner、Scaleway、OVH、AWS S3、Google Cloud Storage、Azure Blob、Oracle、IBM。
- 给出 PasteBox 推荐策略，区分游客临时文件、免费用户、付费用户。

## Acceptance Criteria

- [x] `docs/object-storage-research.zh-CN.md` 存在且为中文。
- [x] 文档包含免费对象存储结论、容量估算、付费对象存储对比和推荐方案。
- [x] 关键价格/额度附来源链接。
- [x] 做基础自检，确认 Markdown 文件可读且没有明显占位内容。

## Out of Scope

- 本轮不改代码。
- 本轮不实现对象存储网关。
- 本轮不申请或实测各云厂商账号。

## Technical Notes

- 使用 `smart-search` 获取当前公开价格页和官方文档。
- 结果保存为正式 docs 文档，同时在任务内保留调研笔记。
