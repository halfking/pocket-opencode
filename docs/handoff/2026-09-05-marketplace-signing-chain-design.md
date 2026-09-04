# ADR: Marketplace 签名链路设计（ed25519 + 内容寻址 blob + 精确版本依赖）

- 日期：2026-09-05
- 状态：Accepted（本 sprint 实施）
- 前置：`/tmp/handoff-20260905-pg-it-closeout-signchain-plan.md` §5

## 1. 背景与目标

marketplace 目前只到"install 记录"：包内容（blob）没有存储与下载，签名只存不验，
依赖字段只是元数据。本 sprint 补齐三条链路，目标是**公共安装（跨租户装公开包）
在启用签名校验后有信任锚点，未启用时行为与现状完全一致（不阻断既有流程）**：

1. 版本签名：提交/发布时校验 ed25519 签名，签名覆盖规范化 manifest + digest；
2. 包内容：内容寻址（sha256 hex）的 blob 存取 + release 下载端点；
3. 依赖解析：精确 pinned 版本 + 环检测 + 深度上限。

## 2. 决策（Decisions）

| # | 决策 | 选择 | 理由 / 被否方案 |
|---|------|------|-----------------|
| D1 | 签名算法 | `crypto/ed25519`（Go 标准库） | 零新依赖、签名小验证快；否决 RSA（大、慢）与 ECDSA（确定性差） |
| D2 | root 公钥分发 | 环境变量 `POCKET_MARKETPLACE_ROOT_PUBKEY` | 验收标准要求"未配置 root 公钥时保持仅记录语义"，env 的有无天然表达 enabled/disabled；否决 DB 表（root 信任锚不能存在被签对象同库）、否决配置文件（部署面已有 env 通道） |
| D3 | publisher 公钥 | DB 表 `publisher_signing_keys`，`(publisher_id, key_id)` 唯一 | per-publisher 轮换与吊销需要持久状态 |
| D4 | 签名载荷 | `canonicalJSON(Manifest) ‖ 0x00 ‖ digest(ASCII)` | 规范化 JSON 消除 key 序/空白歧义；0x00 分隔防拼接歧义；digest 单独入签防止 manifest.digest 与行 digest 不一致被偷换 |
| D5 | 签名编码 | base64 std encoding 存 `marketplace_versions.signature`（列已存在）；新列 `signing_key_id` 标识签名密钥 | handoff 既定 |
| D6 | canonical JSON | 递归排序 key 的紧凑 JSON（无空格）；Manifest 内字段均为 string/[]string/map[string]string，无数字格式化问题 | 避免 JSONB 存储格式影响验签 |
| D7 | blob 后端 | PG bytea 表 `marketplace_blobs`，digest 主键内容寻址去重 | 起步够用；`BlobStore` 思路预留 S3/MinIO，本 sprint 不引入接口抽象（避免过早抽象，YAGNI） |
| D8 | 依赖解析 | 精确 pinned 版本（`manifest.dependencies` 全部 exact），环检测，深度上限 32，缺失依赖聚合报错 | handoff 既定；semver 区间留待后续 |
| D9 | 启用语义 | root 公钥**未配置** → `SIGNING: disabled (public key not configured)`，Submit/Publish 不校验签名（现状语义）；**已配置** → fail-closed：Submit 缺签名/验签失败、Publish 时存储态验签失败、依赖不可解析，一律拒绝 | 明确的两态语义，杜绝"半启用"歧义 |

## 3. 数据模型（schema 增量，幂等）

追加进 `marketplace.Store` 现有 `schema` 常量（`CREATE TABLE IF NOT EXISTS` 语义，
随 `Init()` 执行，与既有表同库同 schema）：

```sql
CREATE TABLE IF NOT EXISTS publisher_signing_keys (
    publisher_id TEXT NOT NULL,
    key_id       TEXT NOT NULL,
    public_key   BYTEA NOT NULL,          -- ed25519 公钥原始 32 字节
    alg          TEXT NOT NULL DEFAULT 'ed25519',
    status       TEXT NOT NULL DEFAULT 'active',  -- active | revoked
    created_at   BIGINT NOT NULL,
    revoked_at   BIGINT,
    PRIMARY KEY (publisher_id, key_id)
);

CREATE TABLE IF NOT EXISTS marketplace_blobs (
    digest       TEXT PRIMARY KEY,        -- sha256 hex（64 字符小写）
    content      BYTEA NOT NULL,
    size         BIGINT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    created_at   BIGINT NOT NULL
);

ALTER TABLE marketplace_versions ADD COLUMN IF NOT EXISTS signing_key_id TEXT;
```

约束语义：
- `marketplace_blobs.digest` 主键 → 天然去重，`PutBlob` 对同 digest 重复上传
  返回首次结果（幂等），不比较内容（digest 即内容标识）；
- 吊销密钥：`status='revoked'` 行保留作审计；验签查询只取 `active` 行，
  吊销后验签即失败（fail-closed）。

## 4. 签名协议

### 4.1 密钥层级

- **root key**（平台级）：env `POCKET_MARKETPLACE_ROOT_PUBKEY`，base64（std/raw）
  或 hex 的 ed25519 公钥（32 字节）。key_id 固定为 `"root"`。配置了但解析失败 →
  pocketd 启动 fatal（fail-fast，拒绝带错误信任锚启动）。
- **publisher key**：`publisher_signing_keys` 内 active 行，`alg` 仅支持
  `ed25519`（其他值验签直接拒绝）。

### 4.2 载荷与签名

```
payload  = canonicalJSON(manifest) || 0x00 || ascii(version_row.digest)
sig      = ed25519.Sign(priv, payload)        // 64 字节
signature 存储格式: base64.StdEncoding.EncodeToString(sig)
```

`canonicalJSON(manifest)`：对 `Manifest` struct 做 `encoding/json` 序列化后
重新走 map 规范化（递归排序 key、紧凑分隔符）。**签名方与验签方都必须用本包
`CanonicalizeManifest` / `SigningPayload` 生成字节**，不得自行 `json.Marshal`。

### 4.3 验签路径（`Store.VerifyVersion(ctx, versionID)`）

1. 读版本行（manifest JSONB、digest、signature、signing_key_id、package → publisher）；
2. `signature` 或 `signing_key_id` 为空 → `ErrSignatureMissing`；
3. `signing_key_id == "root"` → 用已配置 root 公钥；未配置 root →
   `ErrSigningUnavailable`（无法验证平台签名，审计语义）；
4. 否则查 `publisher_signing_keys WHERE publisher_id=? AND key_id=? AND status='active'`，
   查不到（含 revoked）→ `ErrSigningKeyNotFound`；
5. `canonicalJSON(stored_manifest) || 0x00 || stored_digest` 上
   `ed25519.Verify`，失败 → `ErrSignatureInvalid`。

注意：验签基于**行内存储的 manifest 重新规范化**，与提交方字节无关——提交方
必须对同样规范化的字节签名（4.2），两端协议一致即可。

### 4.4 强制点（root 公钥已配置时，fail-closed）

| 时机 | 校验 | 失败行为 |
|------|------|----------|
| `Store.Submit` | `signature`/`signing_key_id` 必填且验签通过（key 为 root 或 publisher key） | 422 语义错误返回，事务回滚 |
| `Store.Publish` | 行锁内重新 `VerifyVersion`（防提交后换 key/吊销窗口） | `ErrMarketplaceConflict` 包装 |
| `Store.Publish` | 该版本 manifest 的全部依赖可解析（`ResolveDependencies`，resolver 查 PG store） | `ErrMarketplaceConflict` 包装 |

root 公钥未配置时以上三点全部跳过（仅日志记录，保持既有行为）。
memstore（本地 dev/test）不强制——无密钥设施，文档注明不对称性。

## 5. Blob 协议

- `Store.PutBlob(ctx, digest, content []byte, contentType string) (BlobMeta, error)`：
  sha256(content) 必须等于 digest（hex 大小写不敏感入参、存储小写），不符 →
  `ErrBlobDigestMismatch`；同 digest 幂等返回既有 meta；
  单 blob 上限 `MaxBlobSize = 64 MiB`（防御性，超限 `ErrBlobTooLarge`）。
- `Store.GetBlob(ctx, digest) (BlobMeta, []byte, error)`：404 →
  `ErrMarketplaceNotFound`。
- 版本 digest 约定：`manifest.digest` = `marketplace_versions.digest` = blob 的
  `sha256:<hex>` 形式前缀可存在；blob 表只存裸 hex（64 字符）。
- HTTP：`GET /api/marketplace/releases/{release_id}/blob`
  - release_id 可含 `/`（形如 `ws/name@1.0.0-stable-…`），路由在
    `handleMarketplaceRouter` 内按"前缀 releases/ + 后缀 /blob"截取；
  - 可见性与 `Install` 完全一致（同 workspace 或 package `visibility='public'`），
    不可见 → 404（不泄露存在性）；
  - 响应 `Content-Type` 取 blob 的 content_type，`Content-Length` 设置，
    `X-Digest` 头回显 digest；blob 缺失 → 404。
- 本 sprint 不含 HTTP 上传端点（`PutBlob` 为 store 级 API，测试直接调用）；
  上传 API + 配额列入下 sprint。

## 6. 依赖解析协议

```go
type DependencyResolver interface {
    GetVersion(ctx, packageID, version string) (*Manifest, error) // ErrMarketplaceNotFound=缺失
}
func ResolveDependencies(ctx, resolver DependencyResolver, root Manifest, maxDepth int) ([]Dependency, error)
```

- 返回拓扑序（被依赖者在前）的完整传递闭包（含 root 自身依赖，不含 root）；
- 环（含自依赖）→ `ErrDependencyCycle`（带环路径描述）；深度 > maxDepth →
  `ErrDependencyTooDeep`；缺失/多版本冲突 → 聚合错误 `ErrDependenciesUnresolved`
  （Error() 列出全部缺失项，不逐个失败即停）；
- 同一 manifest 内同 package 重复列出且版本不一致 → 解析错误；
- `maxDepth <= 0` 视为非法（调用方传 32；常量 `DefaultMaxDependencyDepth = 32`）。

## 7. 文件归属（并行实现不重叠）

| 文件 | 责任 |
|------|------|
| `marketplace.go`（预置，主会话完成） | schema 增量、`PackageVersion/SubmitRequest.SigningKeyID` 字段贯穿、Submit/ListVersions 读写新列 |
| `signing.go` / `signing_test.go`（Agent A） | canonical JSON、payload、`VerifyVersion`、`RegisterPublisherKey/RevokePublisherKey/ActivePublisherKey`、`SetRootPublicKey`、`SignVersion` 助手；纯内存单测 |
| `blob.go` / `blob_test.go`（Agent B） | `PutBlob/GetBlob` + PG 集成测试（复用 store_test.go 的 harness）；`server_marketplace.go` 增 blob 路由 + handler 测试 |
| `deps.go` / `deps_test.go`（Agent C） | `ResolveDependencies` + `ResolveDependenciesWithStore`（PG resolver）；纯内存单测 |
| `marketplace.go` Submit/Publish 强制点、`config.go`、`cmd/pocketd/main.go`（Wave2，主会话完成） | fail-closed 装配 + `SIGNING:` banner |

## 8. 运维语义（banner）

```
SIGNING: enabled (root key configured)                    // env 已配置且解析成功
SIGNING: disabled (public key not configured)             // env 未设置（含无 PG 模式）
FATAL: marketplace root public key: ...                   // env 已配置但解析失败 → 拒绝启动
```

config 新增字段 `MarketplaceRootPubKey`（`POCKET_MARKETPLACE_ROOT_PUBKEY`，默认空）；
生产校验不强制要求配置（可选特性）。

## 9. 测试与验收

- 纯内存：signing（正/反/吊销/root）、deps（链/环/深/缺失聚合）；
- PG（`POCKET_TEST_POSTGRES_DSN`，随机 schema 隔离）：blob put/get/去重/404/
  digest 不符；签名强制开/关两态的 Submit/Publish 行为；
- 既有全部测试回归：`make -C backend test-pg`（race, count=1）全绿；
- `go build ./... && go vet ./... && gofmt -l`（改动文件）全绿；
- 未配置 root 公钥时：既有 submit→review→publish→install 流程行为不变。

## 10. 未来工作（本 sprint 不做）

- blob HTTP 上传端点 + 配额/计费；
- semver 区间依赖、override/lockfile；
- root 公钥轮换（双 key 并存窗口）；
- publisher key 注册/吊销 HTTP API（当前 store 级）；
- S3/MinIO blob 后端。
