# S3 orchestrator integration and streaming file transfer

## Goal

把 PasteBox 的对象存储接入路径验证到 s3-orchestrator，并把文件上传、下载从“服务端整文件读入内存”改成“服务端流式转发”。目标是保持现有前端和 HTTP API 基本不变，同时降低大文件上传下载时的内存占用。

## What I already know

* 本项目生产模式已经通过 AWS SDK v2 访问 S3 兼容对象存储。
* 对象存储配置已经支持 endpoint、bucket、region、access key、secret key、path-style。
* s3-orchestrator 文档和源码确认支持 AWS SDK v2、path-style、SigV4、PutObject、GetObject、DeleteObject、HeadBucket。
* 当前上传链路会 `io.ReadAll(io.LimitReader(file, 5<<30))`，把 multipart 文件一次性读进内存。
* 当前下载链路会把 S3 object body 一次性读成 `[]byte`，再写 HTTP response。
* 游客上传限制和保留时间属于应用层 runtime config，不依赖对象存储网关。
* 当前默认游客配置是单文件 10MB、保留 6 小时；如果要 5MB 和 15 分钟，需要调整 runtime config 或默认值。
* 生产 preflight 要求 `PASTEBOX_S3_ENDPOINT` 是 HTTPS 且是真实域名；本地 `http://localhost:9000` 只适合开发或 PoC。

## Assumptions

* 本次不改前端上传/下载 API，不做浏览器直连 S3。
* 本次不引入预签名 URL；仍由 PasteBox 服务端做代理，但代理过程要流式。
* 本次 s3-orchestrator PoC 以本地或测试 endpoint 验证 S3 兼容性为主，不强依赖真实云厂商免费额度账号。
* 当前对象 key、附件引用计数、扫描、配额、分享下载计数逻辑继续由 PasteBox 应用层维护。

## Open Questions

* 已确认：本次只做服务端代理流式上传/下载，不做前端直传和预签名 URL。

## Requirements

* 对象存储适配保持 S3 兼容配置方式，可以指向 s3-orchestrator endpoint。
* 上传附件时不能把完整文件内容一次性读入内存后再上传 S3。
* 下载附件时不能把完整对象内容一次性读入内存后再写给客户端。
* 现有用户上传、游客上传、分享下载、附件删除、对象引用计数、扫描队列、下载计数、配额校验行为保持一致。
* 错误码和用户可见行为尽量保持当前兼容。
* 保留现有内存对象存储测试路径，方便单元测试继续使用。

## Acceptance Criteria

* [ ] PasteBox 可以通过 S3 env 指向 s3-orchestrator，并通过 readiness 的 HeadBucket 检查。
* [ ] 用户附件上传成功后可以下载，下载内容和上传内容一致。
* [ ] 游客附件上传成功后可以通过分享下载，下载内容和上传内容一致。
* [ ] 上传链路不再使用整文件 `io.ReadAll` 作为主路径。
* [ ] S3 下载链路不再使用整对象 `io.ReadAll` 作为主路径。
* [ ] 相关 Go 测试通过，至少覆盖对象存储和 HTTP 上传下载关键路径。

## Definition of Done

* 相关后端代码完成最小必要改动。
* 相关测试已新增或更新。
* 能跑的 Go 测试已跑；如有测试无法跑，说明原因。
* s3-orchestrator PoC 或等价 S3 兼容性验证有记录。
* 风险点和回滚方式清楚。

## Out of Scope

* 本次不实现浏览器直传 S3。
* 本次不实现预签名上传/下载 URL。
* 本次不修改套餐策略。
* 本次不把默认游客限制强行改为 5MB/15 分钟，除非后续确认这是本任务范围。
* 本次不接入真实免费对象存储账号池。

## Technical Notes

* `internal/objectstore/s3.go`：当前 S3Store 使用 AWS SDK v2，主路径是 `PutObject([]byte)`、`GetObject() []byte`、`DeleteObject`、`HeadBucket`。
* `internal/app/content_stores.go`：当前 `ObjectStore` interface 只接受/返回 `[]byte`，流式改造需要调整接口。
* `internal/httpserver/server.go`：用户和游客上传 handler 当前都整文件读内存；下载 helper 当前按 `[]byte` 设置 Content-Length 并写出。
* `internal/app/app.go` 与 `internal/app/runtime_control.go`：附件创建、下载、游客上传、分享下载依赖当前 `[]byte` 内容计算 hash、content-type、图片尺寸、配额和扫描队列。
* 流式上传要继续计算 SHA-256、size、content type、图片尺寸和配额；需要避免为了这些元数据重新整文件常驻内存。
* 可行方向：上传阶段使用临时文件/spool，同时计算 hash/size/content-type/图片尺寸，再把临时文件 seek 回开头流式传给对象存储。
* 可行方向：下载阶段让对象存储返回 `io.ReadCloser` + size/content type，HTTP 层用 `io.Copy` 写出。

## Research References

* `research/s3-orchestrator-compatibility.md`：确认 s3-orchestrator 支持 PasteBox 需要的 path-style S3 SDK 调用、虚拟 bucket 认证和 HeadBucket readiness。
