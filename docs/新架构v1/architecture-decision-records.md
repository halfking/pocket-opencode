# 架构决策记录（ADR）v3

> 记录 v3 整合架构的关键决策、原因、替代方案。
> **核心变化（v1 → v2 → v3）**：
> - v1 计划自建所有组件；
> - v2 决定复用 acc-go / Memora / llm-gateway-go；
> - **v3 进一步考虑 RedClaw（PC 桌面企业平台 + OpenClaw + 本地 IDE 控制）后，新增独立的 ZAgentGateway 作为 RedClaw 与 OpenPocket / acc-go 的中介**。

---

## ADR-001 (v3)：新增独立服务 ZAgentGateway —— 重大架构变更

**状态**：🟡 目标架构，未实现；需通过审计门禁

**背景**：
- v2 假设 PC 端算力舱 = "acc-go Worker"（自建 Harness）；
- 实际 monorepo 里 RedClaw 已经是 **PC 桌面 OpenClaw 的企业级包装**，自带 platform-go 后端、agentcontainer、connectors、control signals (Ed25519 双签)；
- 用户的 PC 上还有 IDE（ZCode / VS Code / Cursor / OpenCode），这些需要被监测和控制；
- OpenPocket 的应用后端已经在用 RedClaw 的能力（虽然目前通过 echo stub）；
- 因此：**算力舱不是 acc-go Worker，而是 ZAgentGateway → RedClaw platform-go → OpenClaw CLI / 本地 IDE 插件**。

**v1 决策**：自建 Chief + Executor Bridge + Harness。

**v2 决策反转**：算力舱 = acc-go Worker。

**v3 决策再反转**：
1. **新增独立服务 `ZAgentGateway`**（端口 `:9100`）；
2. ZAG 注册为 acc-go 的 worker；
3. ZAG 接收 acc-go 派发的任务，**转发到 RedClaw platform-go**；
4. ZAG 暴露 REST + MCP + WebSocket API 给 OpenPocket / 第三方 MCP 客户端；
5. ZAG 聚合 RedClaw device / agent / session / IDE 状态。

**理由**：

| 反对意见 | 回复 |
|---|---|
| "为什么不让 OpenPocket 直接连 RedClaw？" | RedClaw 是企业平台（含 Plan E / Ed25519 / SSO），不能直接暴露给 mobile |
| "为什么不让 acc-go 直接调 RedClaw？" | acc-go 应该只看到"worker 在跑任务"，不该知道 RedClaw 存在 |
| "为什么不让 pocketd 自己做？" | pocketd 是 mobile BFF，职责过多会变臃肿 |
| "为什么不在 RedClaw 内加 mobile API？" | RedClaw 是企业平台，加 mobile 接口会污染 |
| "为什么不直接复用 acc-go device？" | acc-go device 是通用 worker；ZAG 是专用 worker，需要额外能力（事件转发 / MCP / IDE 聚合 / 鉴权边界）|

**净增量**：~8k 行 Go (ZAG) + ~500 行 Go (pocketd zag client) + ~500 行 Go (acc-go 集成) + ~3k 行 Vue (OpenPocket Mobile 新增)。

**影响**：

- 仓库结构新增：`services/zagent-gateway/`（或独立仓库）；
- pocketd `internal/zag/` 子包；
- acc-go 增量：worker 注册逻辑；
- OpenPocket Mobile `/fleet/*` 路由 + Live View + IDE 控制面板。

---

## ADR-002 (v3)：ZAG 作为 acc-go 的 worker 注册

**状态**：🟡 目标设计，需 ACC 合同验证

**背景**：ZAG 目标上作为 acc-go 与 RedClaw 的中介；当前尚无 ZAG 实现或 worker 注册证据。

**决策**：ZAG 启动时调用 `POST acc-go /api/v2/devices` 注册为 worker：

```json
{
  "deviceId": "zag_worker_001",
  "name": "ZAgentGateway",
  "kind": "zagent-gateway",
  "endpoint": "http://zag:9100/api/v1/tasks",
  "capabilities": ["openclaw", "zcode", "vscode", "cursor", "opencode"]
}
```

acc-go 把 ZAG 当成一种"特殊 worker"，任务派发给 ZAG；ZAG 收到后转发到 RedClaw。

**理由**：

- 复用 acc-go 现有 orchestration v3 任务调度；
- ZAG 不需要自己实现任务队列 / 状态机 / lease；
- 多个 ZAG 实例可注册为多个 worker（acc-go 自动负载均衡）。

**净增量**：~300 行 Go（acc-go device registry 适配 + ZAG 启动逻辑）。

---

## ADR-003 (v3)：ZAG ↔ RedClaw 使用 mTLS + delegated token；高危控制使用独立审批签名

**状态**：🟡 安全设计待冻结；禁止 fail-open 降级

**背景**：ZAG 与 RedClaw 之间是"控制平面"通信；安全要求高。

**决策**：

| 层 | 机制 | 用途 |
|---|---|---|
| **mTLS** | 双向 TLS | 防中间人 + 防 Pod 冒充 |
| **delegated token** | 每次服务间调用 | 受众、租户、主体和 scope 绑定的短期身份 |
| **mTLS** | ZAG ↔ RedClaw / ACC | 服务身份与传输保护；失败必须 fail-closed |
| **独立审批签名** | Rollback / Upgrade / Terminate 控制信号 | 第二签名不得由 ZAG 持有或代签 |

**理由与约束**：

- RedClaw 有控制信号和签名实现，但 ZAG 不能把通用共享 HMAC 当作用户身份；
- 第二个签名必须来自独立主体、独立设备或独立审批服务，不能由 ZAG 持有 admin 私钥代签；
- 签名覆盖 canonical command、tenant、resource、policy version、digest、nonce 和有效期；
- mTLS enrollment、rotation、revoke 和 CA rollover 必须在 M0 通过测试；
- mTLS 失败、审计不可写或审批状态不确定时必须拒绝高危操作。

---

## ADR-004 (v3)：IDE 适配器必须独立实现并通过 RedClaw Connector 合同

**状态**：🟡 目标设计；当前 RedClaw generic connectors 不是已实现 IDE connector

**背景**：用户 PC 上有多个 IDE（ZCode / VS Code / Cursor / OpenCode），需要统一控制。

**决策**：每个 IDE 注册为 RedClaw Connector（已有 `internal/connectors/`）：

```go
{
  "connectorId": "ide_zcode",
  "authMode": "mTLS",
  "endpoints": ["unix:///Users/me/.zcode/socket"],
  "sideEffectLevel": "medium",
  "cursorType": "webhook",
}
```

ZAG 通过 Connector Execute 调用 IDE 命令。

**理由**：

- RedClaw Connectors 已经把外部集成的标准接口定义得很完整（AuthMode / Cursor / SideEffect / Idempotency）；
- 复用现成的 idempotency + permit + policy snapshot；
- 不需要为 IDE 单独造一套调度协议。

---

## ADR-005 (v3)：Charter / Skill / Memory / Cost 落 Memora（不变）

**状态**：✅ 已采纳（沿用 v2）

**决策**：ZAG 把以下数据落 Memora：

- `pocketfleet/build/{id}/events` — 所有任务 / agent / permission 事件；
- `pocketfleet/agent/{id}/memory/` — 跨任务 Agent 记忆；
- `pocketfleet/cost/{date}/{fleet_id}` — 成本聚合；
- `pocketfleet/charter/{fleet_id}` — Charter；
- `pocketfleet/skill/{skill_id}` — Skill。

**理由**：Memora 已经具备完整的 L1–L6 + CodeGraph + Dream Engine；ZAG 直接复用 v2 命名空间约定。

---

## ADR-006 (v3)：LLM 路由使用已验证 gateway，legacy RedClaw Gateway 不作为默认主路径

**状态**：🟡 目标设计，当前 legacy endpoint/mock 未验证

**决策**：

- 默认使用经过合同验证的 llm-gateway-go 或 RedClaw OpenAI-compatible gateway；
- legacy Pocket endpoint/echo response 不能作为生产成功证据；
- Provider fallback 必须遵守租户数据驻留、模型 allowlist 和审计策略；
- 不因 LLM gateway 故障绕过控制面执行高危 runtime 操作。

**理由**：

- RedClaw Gateway 是 RedClaw 生态的 LLM 出口；优先使用它以保持一致性；
- 当前 RedClaw Gateway 是 echo stub（在 FreshLab/RedClaw2 外部仓库），需要等待改造；
- llm-gateway-go 已有复杂的路由、凭据池和审计代码；其生产资格、Provider 合同和租户数据驻留仍必须通过当前部署的真实合同测试确认。

---

## ADR-007 (v3)：MCP Server = ZAG 暴露 `zag_*` tools

**状态**：✅ 已采纳

**决策**：ZAG 启动 Streamable HTTP MCP server，路径 `/mcp`，暴露 `zag_*` 工具：

```
zag_list_agents
zag_invoke_agent
zag_list_pods
zag_control_pod
zag_list_sessions
zag_create_session
zag_send_message
zag_list_tasks
zag_submit_task
zag_get_task_status
zag_reply_permission
zag_get_ide_status
zag_control_ide
...
```

**理由**：

- 让 Cursor / Claude Code / acc-go 等 MCP 客户端能直接调用 ZAG；
- 让 acc-go 通过 MCP 调用 ZAG 的 zag_*（不用 HTTP）；
- 与 acc-go 的 `/api/v2/mcp`（41 个 acc_* tools）并存。

---

## ADR-008 (v3)：ZAG 技术栈与既有服务对齐

**状态**：✅ 已采纳

**决策**：

| 选择 | 理由 |
|---|---|
| **Go 1.25** | 与 acc-go / RedClaw platform-go / Memora / llm-gateway-go 对齐 |
| **gin** | 与 RedClaw platform-go 一致；REST API 风格一致 |
| **gorilla/websocket** | 与 pocketd / RedClaw 一致 |
| **`modelcontextprotocol/go-sdk`** | 与 acc-go 一致 |
| **pgx/v5** | 与 acc-go / RedClaw 一致 |
| **`tenant.From(ctx)` L1 invariant** | 与 acc-go 一致 |
| **Zerolog** | 与 RedClaw 一致 |
| **PostgreSQL** | 共享 cluster |

---

## ADR-009 (v3)：包名 / 路径规范

**状态**：✅ 已采纳

**决策**：

- ZAG 仓库：`services/zagent-gateway/`（monorepo 内）或独立仓库；
- Go module：`github.com/halfking/zagent-gateway`；
- pocketd 客户端：`opencode-pocket/backend/internal/fleetbridge/zag/`；
- pocketd 仍以 `internal/fleetbridge/` 作为 v3 主入口；
- 前端 feature：`frontend/src/features/fleet/`（不变）；
- REST 前缀：`/api/fleet/`（pocketd 对外）+ `/api/v1/`（ZAG 对外）；
- 文档：`docs/新架构v1/`（本目录）。

---

## ADR-010 (v3)：不引入微服务（与 v1/v2 一致）

**状态**：✅ 已采纳

**决策**：单 Go 单体 (pocketd) + 单 Go 单体 (ZAG) + 跨服务调 acc-go / Memora / llm-gateway-go / RedClaw platform-go。

**理由**：

- ZAG 是一个独立服务（不是微服务化 monolith）；
- 它有自己的 DB / 缓存 / 日志；
- 但与 monorepo 内其他服务一样，单 binary 部署。

---

## ADR-011 (v3)：测试策略

**状态**：✅ 已采纳

**决策**：

- **ZAG 单元测试**：mock RedClaw / acc-go / Memora / llm-gateway-go 客户端；
- **ZAG 集成测试**：起 mock RedClaw platform-go + mock acc-go；
- **pocketd 单元测试**：mock ZAG client；
- **E2E**：起完整 docker-compose（含 RedClaw platform-go mock）；
- **真机 E2E**：真实 RedClaw platform-go + 真实 OpenClaw CLI + 真实 OpenPocket mobile；
- **故障注入**：RedClaw / acc-go / Memora / llm-gateway-go / ZAG 任一挂；
- **注入测试**：恶意 intent / 越权 / Ed25519 错误签名。

---

## ADR-012 (v3)：版本兼容

**状态**：🟡 目标设计，需合同测试

**决策**：

- ZAG ↔ RedClaw platform-go：version header，semver；
- ZAG ↔ acc-go：version header，semver；
- ZAG ↔ Memora：沿用 v1/v2 兼容；
- ZAG ↔ llm-gateway-go：OpenAI 兼容协议，无版本问题；
- Pod 上的 OpenClaw CLI：pin `openclaw@1.2.x`，不自动升。

---

## ADR-013 (v3)：可观测性

**状态**：🟡 目标设计，未实现

**决策**：

- **Metrics**：目标使用 ZAG Prometheus + acc-go Prometheus + RedClaw Prometheus + llm-gateway-go Prometheus；
- **Logs**：ZAG zerolog + 各服务；
- **Traces**：OpenTelemetry 跨服务 trace（已规划）；
- **Audit**：所有 ZAG 操作落 Memora `audit/{date}/{fleet_id}` + RedClaw audit。

---

## ADR-014 (v3)：迁移路径（既有 opencode-pocket 用户）

**状态**：🟡 目标迁移策略，未实现

**决策**：

- 现有 opencode-pocket 用户**应保持兼容**；
- 现有 session / instance / email / notes / vault / task / meeting 不动；
- 新增 `/fleet` 入口，作为新功能；
- 现有 `internal/agent/`（ACP Adapter）继续工作，作为 Pod 上的 Harness dialect 之一。

**影响**：

- 数据迁移脚本：无；
- UI 上加 Fleet 入口；
- 文档"升级指南"：v3 vs v2 对照表。

---

## ADR-015 (v3)：降级策略

**状态**：✅ 已采纳

**决策**：

| 故障 | 行为 |
|---|---|
| ZAG 不可用 | 只读/入队降级；不得绕过授权直接调 RedClaw/CLI |
| acc-go 不可用 | 只读/入队降级；不得跳过授权和审计执行高危任务 |
| RedClaw platform-go 不可用 | ZAG 任务暂时不可用；OpenPocket 显示降级提示 |
| RedClaw Gateway (LLM) 不可达 | ZAG 自动 fallback 到 llm-gateway-go |
| Memora 不可用 | 有界缓存；durable audit outbox 仍必须可写；高危操作等待 |
| llm-gateway-go 不可用 | ZAG 返回错误给用户 |
| DeepSeek 限流 | llm-gateway-go 自动 fallback 到 OpenAI / Claude |
| OpenClaw CLI crash | RedClaw agentcontainer 自动重启；事件回流 |
| IDE 插件不可用 | RedClaw Connector 自动 reconnect；不影响其他 IDE |

---

## ADR-016 (v3)：开源策略

**状态**：🟡 待定

**决策**：

- ZAG：核心闭源；可发布"社区版"；
- ZAG 集成 OpenClaw / IDE 的 adapter：可能开源（鼓励第三方接入）；
- 其他：与 v2 相同（acc-go / Memora / llm-gateway-go 已开源；RedClaw 已有 OpenClaw 开源）。

---

## ADR-017 (v3)：v1 → v2 → v3 关键差异（一览）

| 决策 | v1 | v2 | v3 |
|---|---|---|---|
| Chief | 自建 Go 包 | 复用 acc-go | **复用 acc-go** |
| 任务编排 | 自建 | 复用 acc-go | **复用 acc-go** |
| 算力舱 / 执行层 | 自建 pocketd-executor | acc-go Worker | **ZAgentGateway + RedClaw platform-go + OpenClaw CLI** |
| Executor Bridge | 自建 WSS | 复用 acc-go A2A | **ZAG 中介 + RedClaw 内部协议** |
| 本地 IDE 控制 | ❌ | ❌ | **ZAG + RedClaw Connectors** |
| PC Agent 监控 | ❌ | ❌ | **ZAG 拉 RedClaw device / agent** |
| PC Agent 控制 | ❌ | ❌ | **ZAG 转 RedClaw control signals + Ed25519** |
| 多 Agent Harness | 自建 | 复用 acc-go | **复用 RedClaw OpenClaw CLI** |
| LLM Provider 路由 | 自建 | 复用 llm-gateway-go | **复用 llm-gateway-go（兜底）+ RedClaw Gateway（主）** |
| Charter / Skill / Memory | 自建 | 复用 Memora | **复用 Memora** |
| Permission | 自建 | 复用 acc-go taskgate | **复用 RedClaw Plan A/E + acc-go taskgate** |
| 移动端 | pocketd | pocketd | **pocketd + fleetbridge + zag client** |
| Net new Go code | ~5k | ~2k | **~11k**（含 ZAG ~8k + pocketd zag ~500 + acc-go 集成 ~500 + 增强 ~2k） |
| Net new Vue code | ~3k | ~3k | **~3.5k**（加 IDE 控制面板） |

---

## ADR-019 (v3)：RedClaw 不直接替代 OpenCode runtime

**状态**：✅ 审计结论；默认路线

**决策**：

- RedClaw platform-go 作为企业控制面：tenant、task/run、policy、approval、audit、OpenClaw adapter；
- OpenCode 保留为 coding runtime：session、message parts、tool loop、PTY、ACP、`/event`、permission/question；
- ZAG 负责 task/run/session、事件、权限和身份映射；
- 只有在独立兼容项目完成固定版本真实合同测试后，才考虑 OpenCode-compatible facade；
- RedClaw `/api/v1/tasks`、`/api/v1/sessions`、façade `/api/v2/tasks` 不能直接冒充 OpenCode `/session`。

**理由**：当前源码显示两者 API、认证、状态模型、运行时和事件协议均不兼容。直接替换会破坏现有 OpenPocket Go adapter、OpenCode IDE extension、PTY、permission/question 和 session parts。

---

## ADR-020 (v3)：证据等级优先于“完成”标记

**状态**：✅ 已采纳

**决策**：所有架构能力必须标注：

```text
implemented       # 有源码装配和测试证据
contract-tested   # mock/合同测试通过
source-inspected  # 已读源码，未证明部署可用
mock-only         # 仅 mock/echo
planned           # 设计目标
blocked           # 依赖或合同未确认
```

没有证据的 roadmap 项不得使用 `✅`。ZAG、`/api/fleet/*`、IDE 控制和 RedClaw/OpenCode 端到端链路当前均为 `planned` 或 `blocked`。

---

## ADR-021 (v3)：安全基础设施提前到 M0/M1

**状态**：✅ 已采纳

以下内容不得延后到商业化阶段：

- delegated token、对象级 tenant binding、最小 RBAC/ABAC；
- mTLS enrollment/rotation/revoke，失败 fail-closed；
- 命令 schema、路径/workspace sandbox、connector allowlist；
- idempotency、nonce、防重放、unknown-result reconciliation；
- durable audit outbox、审批持久化、事件 cursor；
- WS/SSE ticket/header auth、subscribe ACL、背压和补偿。

M4 只扩展 Team、计费和合规运营，不首次建立基础安全。

---

## ADR-022 (v3)：不允许绕过控制面的安全降级

**状态**：✅ 已采纳

- ZAG/ACC/RedClaw 不可用时可以提供只读或入队降级；
- 不得绕过 ZAG/授权直接执行 RedClaw、OpenClaw CLI 或 IDE 写操作；
- mTLS 失败不得降级为仅 HMAC；
- 审计不可写不得执行高危操作；
- LLM Provider fallback 必须遵循租户数据驻留和模型 allowlist；
- 超时后必须 query/reconcile，禁止无条件重复执行。

---

## ADR-023 (v3)：OpenCode-compatible facade 是可选独立项目

**状态**：🟡 待评估

如果产品需要让现有 OpenCode client/IDE 无感切换到 RedClaw，必须另建兼容层并完成：

```text
/session
/session/:id/message（parts）
/event（EventV2/SSE）
/permission、/question
/pty（WebSocket）
Basic Auth / 受控 auth bridge
snapshot/diff/revert/compact/ACP
```

在固定 OpenCode 版本的真实 server、SDK、IDE 和 PTY 合同测试全部通过前，不能把该项目标为兼容或替代 runtime。

---

## ADR-024 (v3)：文档与实现状态治理

**状态**：✅ 已采纳

- 当前 `docs/新架构v1/` 是目标设计和审计后的 RFC，不是交付报告；
- 旧的“最终交付/完全兼容”文档必须标记 superseded，并指向当前 OpenCode contract；
- 外部 RedClaw/ACC 能力必须记录外部仓库 commit、endpoint contract 和真实测试日志；
- ZAG 实现前不创建“已完成”证据；
- 该目录必须纳入 Git review，避免作为未跟踪文件漂移。

---

## 附录：审计引用

- `00-research/RedClaw作为OpenCode后端审计.md`
- OpenCode session/event/permission/question/pty 源码：`/Users/xutaohuang/workspace/ai/opencode/packages/opencode/src/server/routes/instance/httpapi/groups/`
- RedClaw façade：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/facade/`
- RedClaw agentcontainer：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/agentcontainer/`
- Pocket OpenCode adapter：`backend/internal/adapter/opencode_http.go`
- Pocket当前事实审计：`docs/优化v4/01-现状审计与差距.md`

**状态**：✅ 已采纳

**决策**：

- **保留**：`00-research/竞品分析.md`、`00-research/技术栈调研.md`、`02-modules/mobile-shell.md`、`04-appendix/对比表.md`、`02-modules/chief-as-acc.md`、`02-modules/memory-as-memora.md`、`02-modules/llm-gateway-integration.md`（v2 文档）；
- **重写**：`README.md`、`00-research/现有服务能力盘点.md`、`01-architecture/系统总览.md`、`01-architecture/数据流与协议.md`、`01-architecture/安全模型.md`、`02-modules/pocketd-fleet-bridge.md`（加 ZAG client）、`03-roadmap/里程碑.md`、`03-roadmap/接口规范.md`、`04-appendix/风险与缓解.md`、`architecture-decision-records.md`；
- **新增**：`00-research/zagent-gateway-design.md`（设计背景）、`02-modules/zagent-gateway.md`、`02-modules/redclaw-integration.md`、`02-modules/ide-control.md`、`02-modules/compute-pod-as-zag.md`；
- **重命名为 .v2-deprecated.md**：`02-modules/compute-pod-as-acc-worker.md`（v2 算力舱 = acc-go Worker 方案）；
- **删除**（保留 .v1-deprecated.md）：`02-modules/chief-planner.v1-deprecated.md`、`02-modules/compute-pod.v1-deprecated.md`、`02-modules/agent-runtime.v1-deprecated.md`（v1 自建方案）。

---

## 附录：ADR 索引

| ADR | 标题 | 状态 | 版本 |
|---|---|---|---|
| 001 | 新增 ZAgentGateway | ✅ | v3 |
| 002 | ZAG 注册为 acc-go worker | ✅ | v3 |
| 003 | mTLS + delegated token + 独立审批签名 | 🟡 | v3 |
| 004 | IDE = 独立 adapter + Connector 合同 | 🟡 | v3 |
| 005 | Memora 落库（不变） | ✅ | v3 |
| 006 | LLM 路由（已验证 gateway；legacy endpoint 不默认） | 🟡 | v3 |
| 007 | MCP Server = zag_* tools | ✅ | v3 |
| 008 | 技术栈对齐 | ✅ | v3 |
| 009 | 包名 / 路径规范 | ✅ | v3 |
| 010 | 不引入微服务 | ✅ | v3 |
| 011 | 测试策略 | ✅ | v3 |
| 012 | 版本兼容 | ✅ | v3 |
| 013 | 可观测性 | ✅ | v3 |
| 014 | 迁移路径 | ✅ | v3 |
| 015 | 降级策略 | ✅ | v3 |
| 016 | 开源策略 | 🟡 待定 | v3 |
| 017 | v1 → v2 → v3 关键差异 | ✅ | v3 |
| 018 | 文档保留与重写 | ✅ | v3 |
