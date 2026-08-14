# 16 PR2 — `/api/v4` 控制面 ADR

**版本**：优化 v4
**日期**：2026-08-14
**PR 主题**：`docs(adr): defer /api/v4 control-plane facade to P1`
**范围**：仅文档（ADR + 触发条件 + 兼容性影响 + 旧端点矩阵）；不实现 facade
**前置依赖**：PR1（[15-PR1-契约冻结与发布前置.md](./15-PR1-契约冻结与发布前置.md) §6 已锁定 P0 不实现 `/api/v4`）

---

## 1. 状态

Accepted（继承自 PR1 §6 决议，并在本文档中固化触发条件与重新打开条件）。

## 2. 背景

`/api/v4` 是 docs/优化v4/04-目标架构与领域拆分.md §3 设定的目标 control-plane 合同：把 Action / Run / Approval / Question / Event 五类对象统一为前后端可消费的资源。理论上它能：

- 给前端一个稳定的 WS envelope + REST 资源入口；
- 让 Action/Approval/Event/Audit 共享同一套 correlation id；
- 让客户端不必再为每个领域写专属的 `useXxx` store。

但当前 `/api/v4` 仅是设计文档：

1. **现状事实**：仓库代码无 `/api/v4` 路由；`docs/优化v4/` 也明确指出该合同是“目标设计”，不是“已实现”。
2. **既有替代**：现有端点（Notes / Email / Vault / Sessions / Mobile Session）已在 PR6、PR9 中加入作用域、错误码和审计字段。直接把这些端点对齐 envelope 与 capability，比引入第二个 URL 空间对前端更轻。
3. **P0 容量**：首批 P0 PR 的范围已经在 14 §2 列出 13 个主题；`/api/v4` 落地会引入新 DTO、新事件、新测试、新文档、新错误码，对 4 周内完成的 P0 目标风险过高。

## 3. 决定

`/api/v4` control-plane facade **不进入 P0a / P0b 首批交付**：

- 既有端点继续演进，按 PR6 + PR9 加入 scope / confirmation / 结构化错误码 / 审计字段；
- 事件层使用 PR5 的 envelope 形式，但不强制要求所有领域在 P0 内升级；
- `PR2`（本文档）固化延后决议、触发条件和重新打开条件。

## 4. 触发重新打开 `/api/v4` 的条件

**任一** 条件成立时，必须由 MA + SEC + PO + REL 联合签字才能重新打开 ADR 并启动实现：

1. ≥ 3 个领域模块同时升级到 envelope v1+（数据来源：`frontend/src/services/idempotentWsBus.ts` 的订阅计数 + 后端 envelope 适配率）。
2. ≥ 50% 客户端切换到 envelope 消费者（数据来源：`frontend/src/stores/*.ts` 的 import graph）。
3. P0 验收门（PR12 + PR11）发现某领域因缺少统一合同导致 ≥ 2 个 P0/P1 缺陷（来源：`test-evidence/PR11/` + SEC 风险台账）。
4. OpenCode / ACP 上游发布新协议并需要服务端做兼容桥接（来源：OpenCode / ACP release notes）。

任何一项满足之前，`/api/v4` 路由不得新增；`useXxx` store 必须继续基于既有端点工作。

## 5. 不进入 P0 的具体影响（兼容性矩阵）

| 客户端模块 | 当前契约 | v4 引入后影响（仅说明，不实现） |
|---|---|---|
| `frontend/src/api/notes.ts` | 既有 `/api/mobile/notes` | 仍按既有路径；如未来升级，前端 store 通过 PR3 的 AsyncState 适配 |
| `frontend/src/api/email.ts` | 既有 `/api/mobile/email` | 同上 |
| `frontend/src/api/vault.ts` | 既有 `/api/mobile/vault` | 同上 |
| `frontend/src/services/idempotentWsBus.ts` (PR5) | envelope v1 形式 | 已经满足 v4 事件层要求；不需要新代码 |
| `frontend/src/utils/asyncState.ts` (PR3) | 通用异步状态 | 已经满足 v4 资源层要求 |
| 后端 `backend/internal/server/mobile_session_handler.go` | 既有 `/api/mobile/sessions` | 同 Notes |
| 后端 `backend/internal/server/server_assistant.go` | 既有 `/api/mobile/notes` `/api/mobile/email` | 同 Notes |
| 后端 `backend/internal/server/server_agentbridge.go` | 既有 `/api/mobile/agents` | 同 Notes |

## 6. 兼容性窗口（若未来重新打开）

为避免把既有客户端逼到升级悬崖，重新打开 `/api/v4` 时必须：

1. **首期双协议并行**：服务端同时保留 v3（既有）和 v4 路由；前端通过 feature flag（PR1 §7）切换。
2. **逐领域升级**：先单领域迁移（例如先 Notes）→ 全量灰度 → 旧端点下线；不得一次性切换多个领域。
3. **兼容期限**：旧端点至少保留 1 个版本（≈ 1 个 minor）；下线前必须有 ≥ 30 天的 analytics 表明旧端点流量 < 5%。
4. **回滚路径**：保留 `feature flag` 关闭开关（`approval.server_confirm_required` 或新增 `controlplane.api_v4_enabled`）；回滚只允许保留旧端点，禁止直接删除 v4 路由。
5. **数据迁移**：若 v4 引入新的存储形态，必须提供不可逆迁移标记（PR1 §8）+ forward-only 读路径。

## 7. ADR 与文档索引

- 状态：`docs/优化v4/12-ADR与风险台账.md` ADR-011（已在 PR1 中升级为 Accepted）。
- 设计背景：`docs/优化v4/04-目标架构与领域拆分.md` §3。
- P0 范围：`docs/优化v4/14-首批PR与执行顺序.md` §2（PR1 行决议）。
- 错误码：`docs/优化v4/15-PR1-契约冻结与发布前置.md` §10。
- 触发重新打开：本文件 §4。

## 8. 验收

- [x] 决议已写入 12-ADR ADR-011。
- [x] 本文档提供触发重新打开的具体条件（§4）。
- [x] 兼容性矩阵（§5）明确既有模块不受影响。
- [x] 未来重新打开时遵循 §6 的兼容窗口与回滚路径。

## 9. 范围、非目标与回滚

### 9.1 范围

- 新增本文件 `docs/优化v4/16-PR2-ADR-api-v4-延后.md`。
- 不写任何后端 facade、不引入 v4 路由、不修改前端 store。

### 9.2 非目标

- 不在 P0a / P0b 中实现 `/api/v4`。
- 不为 `/api/v4` 创建新 DTO、新事件、新错误码。
- 不修改现有任何路由或 store。

### 9.3 回滚

- 直接 `git revert` 本提交；本 PR 仅新增文件，无副作用。
