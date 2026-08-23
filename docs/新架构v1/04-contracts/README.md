# 04 — 协议与安全合约

> **目的**：把所有"接口契约 / 安全合同 / 状态枚举"的活页集中到一处，让任何模块的"它现在到底能做到什么"可以在 5 分钟内回答完。
>
> **维护原则**：本目录只放**合同草案**与**合同快照**。实现代码、运行配置、CI 脚本分别在 `backend/`、`deploy/`、`scripts/` 下；运行验证由 `05-security/` 下的测试矩阵驱动。

---

## 1. 目录与文件索引

| 文件 | 角色 | 当前状态 | 主要读者 |
|---|---|---|---|
| `_status.md` | 合同状态矩阵的总入口（`draft / reserved / implemented / contract-tested / mock-only / blocked`） | `draft` | 所有模块评审者 |
| `pocket-adapter-matrix.md` | Pocket 出边界的所有 adapter 的协议 / 鉴权 / 隔离 / 幂等 / 超时 / 重试 / outbox 七维矩阵 | `draft` | 子任务 D 评审、pocketd 维护者 |
| `pocket-zag-incremental.md` | `/api/zag/v1/*` 增量接口与授权路径的冻结草案（不破坏 `/api/agent/*`、`/api/opencode/*`） | `draft` | ZAG 实现者、pocketd 维护者 |
| `README.md`（本文件） | 本目录的导航 + 阅读顺序 + 状态枚举 + 合约与安全小节 | `draft` | 新加入者、跨模块评审 |

> 每一份文档头部都必须带"状态值 + 最近一次变更日期"。新增/修改任何合同条目必须同时更新 `_status.md`。

---

## 2. 状态枚举（status vocabulary）

从 `_status.md` 沿用：

| 状态 | 含义 | 谁负责升级到下一档 |
|---|---|---|
| `draft` | 合同草案，未进入代码层。 | 草案作者 |
| `reserved` | 代码层接口或路由已留位（接口/常量/路由占位），运行时未启用。 | 实现者，启用前需补 contract test。 |
| `mock-only` | 仅 mock / fake 实现存在，未接入运行时。 | 测试与实现协同。 |
| `contract-tested` | 行为由自动化测试覆盖（不要求在真实环境下跑通）。 | 测试维护者。 |
| `implemented` | 运行时已接入，被调用方依赖，行为固定。禁止静默修改；改动必须同步测试与文档。 | 任何修改者必须同时更新本状态。 |
| `blocked` | 因前置依赖显式禁止启用（如上游合同未冻结、安全门禁未通过）。 | 显式解锁条件由 `_status.md` 的"阻塞项"列追踪。 |

**禁止**：

- 把 `implemented` 标成"已通过集成测试"但没有真实环境证据——这是 `contract-tested`。
- 把 `mock-only` 标成 `contract-tested` 但没有真实环境测试运行记录。
- 用"完成" / "已完成"等无证据等级的中文词代替状态枚举。

---

## 3. 合约与安全（Contracts & Security）小节

> 本节是本目录与 `05-security/` 的交叉指针。完整的安全 ADR 与测试矩阵在 `05-security/`；本节只列**安全合同入口**。

### 3.1 安全 ADR（位于 `docs/security/`）

| ADR | 主题 | 状态 | 主要约束 |
|---|---|---|---|
| `zag-adr-0001-token-format.md` | ZAG delegated token 格式与失败模式 | `Proposed (M0)` | 短期受众限定 token；`aud=zagent-gateway`；禁止裸 header 鉴权；签发 key 不可用 → fail-closed。 |
| `zag-adr-0002-mtls.md` | mTLS transport（与 ADR-0001 配对） | `Proposed` | 受管 CA；leaf cert + SAN/CN 双向校验；mTLS 失败不得降级 HMAC。 |
| `zag-adr-0003-authz-model.md` | RBAC + ABAC 对象级授权 | `Proposed` | 默认 deny；role × resource × action 矩阵；所有写路径走 outbox。 |
| `zag-adr-0004-request-safety.md` | `Idempotency-Key` + `operation_id` + `X-Request-Id` | `Proposed` | 写路径必须携带 idempotency key；未知超时先 query/reconcile。 |
| `zag-adr-0005-state-replication.md` | 多副本状态、撤销列表、outbox 持久化 | `Proposed` | RPO ≤ 5s、RTO ≤ 30s；状态分层 hot/warm/cold。 |
| `zag-adr-0006-event-safety.md` | SSE / WebSocket 事件安全 | `Proposed` | `event_id` + `sequence` + `tenant_id` + `aggregate_version`；慢消费者背压。 |
| `zag-adr-0007-audit.md` | 高危审计 fail-closed | `Proposed` | append-only + 哈希链；audit backend 不可用 → 高危操作停止。 |

### 3.2 安全测试矩阵（位于 `05-security/`）

完整的威胁模型 + 测试矩阵 + 发布门禁清单在 `docs/新架构v1/05-security/`。本目录（`04-contracts/`）的合同条目都会在 `05-security/` 中至少有一条对应的"违反该合同 = 哪条测试会失败"的反向引用。

### 3.3 与 `01-architecture/安全模型.md` v3 的对齐

| 安全模型 v3 条款 | 对应合约 | 对应 ADR |
|---|---|---|
| §3 身份与委托链（仅 claims 鉴权） | `pocket-zag-incremental.md` §3 | ADR-0001 |
| §5 mTLS / 密钥 / 签名 | `pocket-zag-incremental.md` §3.2 路径 A；`pocket-adapter-matrix.md` §2 auth 列 | ADR-0002 |
| §6 WebSocket / SSE 安全（无 query JWT） | `pocket-zag-incremental.md` §1 + §4 | ADR-0006 |
| §7 SSOT / 幂等 / 故障恢复 | `pocket-adapter-matrix.md` §2 idempotency / 重试 / outbox 列；`pocket-zag-incremental.md` §2 错误信封 | ADR-0004、ADR-0005、ADR-0007 |
| §10 M0/M1 门禁 | `_status.md` 的"阻塞项"列 | 全部 |

---

## 4. 阅读顺序（推荐）

1. `_status.md` — 先看现状总览，确认你要找的东西在 `draft / reserved / blocked` 中。
2. `pocket-adapter-matrix.md` — 任何对"pocketd 出边界"的改动必须先读这张表。
3. `pocket-zag-incremental.md` — 任何对 `/api/zag/v1/*` 的改动必须先读这份冻结草案。
4. `docs/security/zag-adr-000N-*.md` — 任何涉及鉴权 / 授权 / 审计的改动必须先读对应 ADR。
5. `docs/新架构v1/05-security/` — 任何要写到生产路径的改动必须先读威胁模型与测试矩阵。

---

## 5. 变更规则（governance）

任何对 `_status.md`、`pocket-adapter-matrix.md`、`pocket-zag-incremental.md` 或 `docs/security/zag-adr-000N-*.md` 的修改：

1. 必须同时更新本 README 的"目录与文件索引"和"合约与安全"小节（如果是新增文件）。
2. 必须把变更记录到 `_status.md` 的"最近一次变更"行。
3. 升级 `draft → reserved / mock-only / contract-tested / implemented` 时，必须给出升级证据路径（测试报告、commit SHA、运行截图）。
4. **禁止**在不升级 ADR 状态的情况下修改 ADR 的 "Context" 或 "Decision" 段落。如必须修改，等同于新 ADR：旧 ADR 标 `Superseded by ADR-XXXX`。

---

## 6. 最近一次变更

- 2026-08-23：04-contracts 目录骨架建立；`_status.md`、`pocket-adapter-matrix.md`、`pocket-zag-incremental.md` 首版落地；本 README 起草。
