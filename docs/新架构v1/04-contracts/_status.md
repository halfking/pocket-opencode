# 合同状态矩阵（Status Matrix）

> 本文件追踪新架构 v1 中所有"合同草案 → 实现"的状态。
> 每条目包含：合同位置、当前状态（`draft` / `reserved` / `implemented` / `contract-tested` / `mock-only` / `blocked`）、责任人、阻塞项、最近一次变更。
>
> 本节是子任务 D（OpenPocket Adapter 边界矩阵）的状态快照。

---

## Pocket Adapter（子任务 D）

- 关联合同：`docs/新架构v1/04-contracts/pocket-adapter-matrix.md`、`pocket-zag-incremental.md`
- 关联代码：`backend/internal/zagclient/{client.go,noop.go,client_test.go}`
- 关联现状：legacy redclaw（`implemented`）、facade client（`contract-tested`）、mcp client（`implemented`）、opencode_http adapter（`implemented`）、目标 ZAG client（`reserved`）

| 适配器 | 接口契约 | 鉴权 | tenant 隔离 | 幂等 | 超时 | 重试 | outbox | 当前状态 | 阻塞项 |
|---|---|---|---|---|---|---|---|---|---|
| legacy redclaw client | `/api/v1/pocket/*` | 静态 `Bearer + X-Tenant-ID` | 客户端覆盖 | 无 | 30s | 无 | 无 | `implemented`（仅 legacy bridge） | 安全模型 v3 §3.1 不允许作为新授权路径 |
| facade client | `/api/v2/{tasks,approvals,notifications,memory}` | 服务 JWT | JWT claim | 自动 Idempotency-Key | 30s / SSE 独立 | 由调用方 reconcile | 无 | `contract-tested` | — |
| mcp client | JSON-RPC 2.0 + MCP `2024-11-05` | HS256 内部 JWT | JWT claim + ACC RLS | JSON-RPC `id` | 30s / init TTL 5min | `sync.Once` 缓存失败 | 无 | `implemented` | — |
| opencode_http adapter | OpenCode runtime HTTP | 无（受信网络） | 本地目录边界 | runtime 端保证 | 实例化时配置 | 无 | 无 | `implemented` | 不感知 tenant，需在更高层兜底 |
| 目标 ZAG client | `Client` 接口（`backend/internal/zagclient/client.go`） | mTLS **或** short-lived delegated JWT | JWT claim + mTLS SAN 双向校验 | 写路径强制 `Idempotency-Key` | 30s / SSE 独立 | 调用方 reconcile + `indeterminate` | 先 outbox 再执行；高危 fail-closed | `reserved`（仅接口 + Noop 实现 + 单测；运行时未注册路由） | 1) ZAG 协议合同冻结；2) mTLS / delegated JWT 签发；3) outbox + reconciliation worker；4) M0/M1 门禁通过 |

### 状态值定义

- `draft`：合同草案，未进入代码层。
- `reserved`：代码层接口或路由已留位（接口/常量/路由占位），运行时未启用。
- `implemented`：运行时已接入，被调用方依赖。
- `contract-tested`：行为由自动化测试覆盖。
- `mock-only`：仅 mock / fake 实现存在。
- `blocked`：因前置依赖显式禁止启用。

### 子任务 D 交付清单

| 类别 | 文件 | 状态 | 行数 |
|---|---|---|---|
| 边界矩阵 | `docs/新架构v1/04-contracts/pocket-adapter-matrix.md` | `draft` | 见 wc -l |
| 增量接口设计 | `docs/新架构v1/04-contracts/pocket-zag-incremental.md` | `draft` | 见 wc -l |
| 状态矩阵（本文件） | `docs/新架构v1/04-contracts/_status.md` | `draft` | 见 wc -l |
| ZAG client 接口契约 | `backend/internal/zagclient/client.go` | `reserved` | 见 wc -l |
| ZAG noop 实现 | `backend/internal/zagclient/noop.go` | `reserved` | 见 wc -l |
| ZAG client 单测 | `backend/internal/zagclient/client_test.go` | `contract-tested` | 见 wc -l |

### 最近一次变更

- 2026-08-23：子任务 D 首版（接口预留 + 文档冻结）。
