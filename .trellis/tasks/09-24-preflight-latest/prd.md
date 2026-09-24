# PRD: preflight 支持显式放开 latest 镜像

## 背景

用户按 `docs/postgresql-redis-deployment.zh-CN.md` 部署时，在 `PASTEBOX_IMAGE`
填 `ghcr.io/cvinit/pastebox:latest`，执行 `./deploy/pastebox-deploy.sh preflight-root`
报错：

```
production preflight failed: PASTEBOX_IMAGE must be a sha-* tag or digest, got "ghcr.io/cvinit/pastebox:latest"
```

来源：`cmd/pastebox/main.go:559` 调用 `isPinnedImage`（`cmd/pastebox/main.go:717-732`），
该函数明确拒绝 `:latest` 和非 `sha-*` 的 tag，只接受 `sha-*` tag 或 `@sha256:` digest。

这是项目的刻意生产策略，多处文档已声明（`docs/production-secrets.md:49`、
`docs/production-launch-evidence-checklist.md:75`、`docs/deployment.zh-CN.md:40`、
`docs/s3-orchestrator-r2-pastebox-docker.zh-CN.md:220`），并有 3 个测试锁定行为
（`cmd/pastebox/main_test.go:355-407`）。

用户希望使用 `latest`，并选择了**可选开关**方案：默认策略不变，只有显式设置环境
变量时才放行 `latest`。

## 目标

新增显式逃生开关 `PASTEBOX_ALLOW_LATEST_IMAGE=true`，让用户在明确知情的前提下
绕过固定镜像校验。默认（不设置该变量）行为完全不变。

## 需求

### R1 新增开关

- 环境变量名：`PASTEBOX_ALLOW_LATEST_IMAGE`
- 生效值：大小写不敏感的 `true`（去首尾空格后比较）
- 其他任何值（含空、`1`、`yes`、`false`）均视为未开启，保持现有拒绝行为

### R2 校验逻辑

- `runProductionPreflight` 中的镜像校验改为：`!isPinnedImage(image) && !allowLatestImage()`
  时才报错
- 报错信息保持可诊断：当因 `latest` 被拒绝时，提示中应告知可用
  `PASTEBOX_ALLOW_LATEST_IMAGE=true` 显式放开

### R3 作用范围

- 仅影响 `preflight production`（即 `preflight-root` / `preflight` 走的路径）
- 不改动 `isPinnedImage` 本身对 digest / `sha-*` 的判定
- 开关打开时**跳过整个固定镜像校验**（用户选定实现）：
  `!isPinnedImage(image) && !allowLatestImage()` 才报错。
  因此开关打开时 `:latest` 和 `v1.2.3` 等非固定 tag 都会通过。
  命名沿用用户选定值 `PASTEBOX_ALLOW_LATEST_IMAGE`，但语义是"跳过固定镜像校验"，
  这一点需在文档中如实说明。

### R4 文档

- 在部署相关文档中说明该开关存在及其风险（移动标签不可复现、回滚依赖记录 digest）
- 保留现有"生产推荐用 `sha-*` / digest"的表述，把开关描述为显式例外

### R5 测试

- 新增：设置 `PASTEBOX_ALLOW_LATEST_IMAGE=true` + `:latest` → preflight 通过
- 新增：设置非 `true` 值（如 `1`/`false`）+ `:latest` → 仍失败
- 保留现有 3 个测试（`RejectsLatestImage`、`RejectsNonShaImageTag`、`AllowsDigestImage`），
  确认不设置开关时行为不变

## 非目标

- 不删除 `isPinnedImage` 校验
- 不默认允许 `latest`
- 不修改 Compose 文件里的必填校验（`compose.production.yaml` 等）
- 不涉及镜像发布流程

## 验收标准

1. `go test ./cmd/pastebox/...` 全部通过（含新增测试）
2. 不设置 `PASTEBOX_ALLOW_LATEST_IMAGE` 时，`:latest` 仍被拒绝
3. 设置 `PASTEBOX_ALLOW_LATEST_IMAGE=true` 时，`:latest` 通过 preflight
4. 文档更新到位，如实说明开关会跳过整个固定镜像校验
