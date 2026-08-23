# 模块：Chief = acc-go（目标集成，审计修正版）

> **状态**：`source-inspected` / `planned`，不是当前 pocketd 已装配的生产链路。
> 
> acc-go 的 `taskdecompose`、`orchestrator`、`orchestration` 和 MCP 能力来自相邻仓库源码；本仓库当前只有部分 MCP client 装配，尚未证明 canonical task REST、Mission SSE、动态 tenant 映射和 ZAgentGateway worker 闭环可用。
> 
> **默认执行边界**：acc-go 负责任务编排；ZAG/RedClaw/OpenCode 负责后续控制面和 runtime 适配。不要把 acc-go task 直接当成 OpenCode session。

---

## 1. 为什么是 acc-go？

`services/agent-control-center/acc-go/` 是相邻仓库中的任务编排实现（Go 1.25，端口配置需以实际部署为准）。源码检查显示它包含 taskdecompose/orchestrator/orchestration/MCP 等模块；这只能证明能力设计和代码存在，不能替代本仓库的真实集成、租户、合同和运行时验证。

| todos.dev Chief 概念 | acc-go 对应能力 |
|---|---|
| Chief（规划 Agent） | `internal/orchestrator/` `HeuristicTriage` + session state machine |
| Charter（长期指令） | Memora namespace `pocketfleet/charter/{fleet_id}` |
| 任务拆分 | `internal/taskdecompose/` `decompose/ai-decompose/validate` |
| 子任务分配 | `internal/taskdecompose/` `assign-subtasks` (round-robin) |
| 并行执行 | `internal/orchestration/` v3 durable state machine |
| Plan / Build 两层 | `internal/taskdecompose/templates` + Mission/Subtask |
| Reviewer（Architect/QA） | `internal/agent/specialists/` fusion/judge/pipeline |
| Schedules | `internal/autoscheduler/` |
| 多 Agent 团队 | `internal/crews/` |
| 持久化记忆 | `internal/kxmem/`（Memora client）+ `internal/memory-bank/` |
| MCP 协议（让外部 Agent 接入） | `internal/mcp/` Streamable HTTP @ `/api/v2/mcp` |
| Agent-to-Agent 协议 | `internal/a2a/` |

acc-go 的 41 个 MCP 工具已经把"规划 / 拆分 / 分配 / 执行 / 审批 / 完成"全流程覆盖。

---

## 2. pocketd 怎么"用" acc-go 作为 Chief？

### 2.1 总体思路

pocketd 不实现任何规划逻辑；pocketd 把用户输入"翻译"成 acc-go 的任务，并订阅 acc-go 的事件流。

```
用户输入 ──► pocketd fleetbridge.intent.go ──► acc-go task
                                              │
                                              ▼
                                          acc-go 自己规划 / 拆分 / 派发 / 监控
                                              │
                                              ▼
                                          SSE 流 ──► pocketd WS Hub ──► 手机
```

### 2.2 关键映射

| PocketFleet 概念 | acc-go 概念 | 说明 |
|---|---|---|
| 用户的一句话目标 | `Task.intent` | 落到 `canonical_tasks` 表 |
| PocketTask (根任务) | `canonical_tasks` row | `parent_task_id = null` 的任务 |
| Subtask | `canonical_tasks` row | `parent_task_id = <root>` 的任务 |
| Decomposition 边 | `task_decompositions` row | parent_task_id → sub_task_id 的有向边 |
| Build | `Mission` | 一个 Mission 调度一组 subtasks |
| Plan / Build 拆分 | `DecompositionTemplate` + `decomposition_type` | 模板驱动 |
| Charter | Memora `pocketfleet/charter/{fleet_id}` memory | 直接走 Memora |
| Skill | Memora `pocketfleet/skill/{skill_id}` memory | 直接走 Memora |
| Permission Request | `TaskGate` (acc_task_request_human_approval) | acc-go 原生 |
| Cost | llm-gateway-go usage webhook + Memora 聚合 | 跨服务 |
| Agent (Backend Eng) | acc-go Employee / Agent Profile | `employees` 表 / `agent_profiles` |
| Pod (算力舱) | acc-go Device / Worker Cell | `devices` 表 |

### 2.3 一次"用户输入"的完整旅程

```
[用户] "给 X 仓库加 OAuth 登录"
   │
   ▼
[Mobile] PocketFleet UI 输入框
   │
   │ POST /api/fleet/intent
   ▼
[pocketd fleetbridge.intent.go]
   │
   │ 1. claimsFromContext → workspace_id, user_id
   │ 2. POST acc-go /api/v2/canonical/tasks
   │    body: { title, intent, source: "pocket-fleet", metadata: {pocketWorkspaceId, pocketUserId} }
   │
   ▼
[acc-go canonical task handler]
   │
   │ 3. CREATE canonical_tasks row (intent=<user text>, status='planning')
   │ 4. orchestrator.HeuristicTriage → 判定为 complex
   │ 5. 启动 taskdecompose.ai-decompose worker
   │    (异步调用 llm-gateway-go 调 LLM 拆分)
   │
   ▼
[acc-go taskdecompose worker]
   │
   │ 6. POST llm-gateway-go /v1/chat/completions (model=deepseek-v4-pro)
   │ 7. 解析 LLM 输出 → subtasks
   │ 8. INSERT INTO canonical_tasks (subtask1, subtask2, subtask3)
   │ 9. INSERT INTO task_decompositions (parent → subtask)
   │
   ▼
[acc-go orchestrator.assign]
   │
   │ 10. assign-subtasks → round-robin 选 Agent
   │ 11. INSERT INTO task_decompositions (assignee)
   │ 12. 创建 Discussion (forum_posts row)
   │ 13. 写 Memora: 任务开始事件
   │
   ▼
[acc-go orchestration v3]
   │
   │ 14. 为每个 subtask 创建 Mission
   │ 15. agentspawner → 调度到 Pod（acc-go Worker Cell）
   │ 16. lease + outbox + SSE projector
   │
   ▼
[acc-go SSE: /api/v2/missions/{missionId}/events]
   │
   │ - mission.start
   │ - subtask.spawn (× N)
   │ - agent.message (streaming)
   │ - tool.call
   │ - tool.result
   │ - gate.requested → permission.request
   │ - gate.approved (after mobile reply)
   │ - subtask.complete
   │ - mission.complete
   │
   ▼
[pocketd fleetbridge.ws_bridge.go]
   │
   │ 17. 订阅 SSE → 转换为 pocketd WS Hub 事件
   │ 18. 推送给 mobile
   │
   ▼
[Mobile Live UI]
```

---

## 3. 主要 REST 调用模板（pocketd → acc-go）

### 3.1 提交意图

```http
POST /api/v2/canonical/tasks
Authorization: Bearer <ACC_INTERNAL_SECRET>
Content-Type: application/json
X-Tenant-ID: ws_001
X-User-ID: u_001

{
  "title": "<user text>",
  "intent": "<user text>",
  "source": "pocket-fleet",
  "metadata": {
    "pocketWorkspaceId": "ws_001",
    "pocketUserId": "u_001",
    "pinnedPodId": null,
    "preferences": {
      "model": "deepseek-v4-flash",
      "maxBuilds": 3
    }
  }
}
```

### 3.2 触发 AI 拆分

```http
POST /api/v2/tasks/{taskId}/ai-decompose
Authorization: Bearer <ACC_INTERNAL_SECRET>
X-Tenant-ID: ws_001

{
  "model": "deepseek-v4-pro",
  "maxSubtasks": 5
}
```

### 3.3 自动分配子任务

```http
POST /api/v2/tasks/{taskId}/assign-subtasks
Authorization: Bearer <ACC_INTERNAL_SECRET>
X-Tenant-ID: ws_001

{
  "strategy": "round_robin" | "least_loaded" | "specialty_match",
  "createDiscussion": true
}
```

### 3.4 订阅 SSE

```http
GET /api/v2/missions/{missionId}/events
Accept: text/event-stream
Authorization: Bearer <ACC_INTERNAL_SECRET>
X-Tenant-ID: ws_001

→ SSE stream:
event: mission.update
data: {"missionId":"m_001","status":"running","ts":...}

event: agent.message
data: {"buildId":"b_001","role":"assistant","delta":"..."}

event: gate.requested
data: {"gateId":"g_001","buildId":"b_001","tool":"shell_run","args":{"cmd":"git push ..."}}
```

### 3.5 审批 Gate

```http
POST /api/v2/tasks/{taskId}/gates/{gateId}/approve
Authorization: Bearer <ACC_INTERNAL_SECRET>
X-Tenant-ID: ws_001

{
  "decision": "allow_once",
  "note": "PR looks good"
}
```

### 3.6 查 Mission

```http
GET /api/v2/missions/{missionId}
GET /api/v2/missions/{missionId}/artifacts
GET /api/v2/missions?status=running&limit=20&cursor=...
```

---

## 4. MCP 工具的复用

acc-go 暴露的 41 个 MCP 工具可以被任何 MCP 客户端调用，包括：

1. **未来 PocketFleet Mobile 自身的 AI Agent** —— 如果手机端要装一个小 LLM Agent，它可以通过 MCP 调 acc-go 的所有能力。
2. **第三方 Agent** —— 比如 Cursor / Claude Code 通过 MCP 调 acc-go 的任务能力。
3. **acc-go 内部不同模块之间的协作** —— 已经实现。

### 4.1 通过 MCP 调用的示例

```json
// POST /api/v2/mcp (Streamable HTTP)
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "acc_create_task",
    "arguments": {
      "title": "Fix typo in README",
      "intent": "...",
      "metadata": {}
    }
  }
}

// 响应
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [{"type": "text", "text": "{\"taskId\":\"t_001\",\"status\":\"planning\"}"}]
  }
}
```

### 4.2 PocketFleet 是否走 MCP？

可选。

- **简单路径**：pocketd 直接 HTTP REST 调 acc-go（如上）；
- **MCP 路径**：pocketd 内嵌一个 MCP 客户端（如 `modelcontextprotocol/go-sdk`），通过 `/api/v2/mcp` 调。

建议**简单路径**：HTTP REST 更直接、可控、易调试。MCP 在跨服务、跨 SDK 场景更友好。

---

## 5. 现有 acc-go 能力的限制 / PocketFleet 增强

> 如果 acc-go 现有能力不够用，需要**小范围增强** acc-go。下面列出可能需要的增强项。

### 5.1 需要的增强（按优先级）

| 增强项 | 原因 | 工作量 |
|---|---|---|
| **Mission event 流加 cost 字段** | mobile UI 要实时 cost | 0.5 天 |
| **Permission gate 加 riskLevel 字段** | mobile UI 按 risk 显示 | 0.5 天 |
| **Device 加 capabilities 字段** | mobile UI 显示 Pod 能力 | 1 天 |
| **Task 加 "Charter hash" 字段** | 让 Chief 决策可追溯 | 0.5 天 |
| **Discussion 加 "agent vote"** | 多人 Agent 决策 | 2 天 |
| **Memory namespace 隔离** | PocketFleet 跟 acc-go 自己的 memory 分开 | 0.5 天 |

总工作量约 **5 天 / 1 个 acc-go 工程师**。

### 5.2 不需要重新做的

- ❌ Chief Planner —— acc-go 已有
- ❌ 任务持久化 —— acc-go orchestration v3 已有
- ❌ Agent Harness —— acc-go agent/harness 已有
- ❌ MCP server —— acc-go 已有
- ❌ A2A —— acc-go 已有

---

## 6. 故障降级（如果 acc-go 不可用）

| 场景 | PocketFleet 行为 |
|---|---|
| acc-go 短暂不可达（< 30s） | pocketd 重试 + 队列缓冲 |
| acc-go 长时间不可用 | pocketd 回退到 v1 简化模式：直接调 llm-gateway-go 跑单 Agent 任务 |
| acc-go 部分能力故障（如 mission SSE） | 降级为轮询 GET /api/v2/missions/:id |
| acc-go 数据库故障 | 紧急通知 + 停服 |

### 6.1 降级规则（审计修正版）

ACC 不可用时可以创建待处理记录或提供只读状态，但不得跳过授权直接执行 RedClaw/OpenCode/IDE 高危操作。任何直达 ZAG 的路径必须生成独立 operation/idempotency/reconciliation 记录，并继续执行相同的 tenant、RBAC、审批和审计门禁。

```go
// backend/internal/fleetbridge/intent.go (伪代码)
func (h *IntentHandler) Handle(ctx context.Context, req IntentRequest) (*IntentResponse, error) {
    // 尝试 acc-go
    resp, err := h.clients.ACC.CreateCanonicalTask(ctx, toACCTask(req))
    if err == nil {
        return fromACCTask(resp), nil
    }

    // acc-go 不可用 → 回退到直接 LLM
    log.Warn("acc-go unavailable, fallback to direct LLM", "err", err)
    fallbackResp, err := h.fallbackSimpleIntent(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("both acc-go and fallback failed: %w", err)
    }
    return fallbackResp, nil
}

func (h *IntentHandler) fallbackSimpleIntent(ctx context.Context, req IntentRequest) (*IntentResponse, error) {
    // 直接调 llm-gateway-go；不拆分；单 Agent 任务
    chatResp, err := h.clients.LLMGw.Chat(ctx, llm.ChatRequest{
        Model: "deepseek-v4-flash",
        Messages: []llm.Message{
            {Role: "system", Content: "You are a helpful coding assistant."},
            {Role: "user", Content: req.Text},
        },
    })
    if err != nil { return nil, err }

    // 把响应作为 PocketTask（没有 subtask）
    return &IntentResponse{
        MissionID: "fb_" + uuid.New().String(),
        TaskID:    "fb_t_" + uuid.New().String(),
        Status:    "fallback",
        Reply:     chatResp.Message.Content,
    }, nil
}
```

---

## 7. 测试策略

### 7.1 单元测试

- fleetbridge 各 handler 用 mock ACCClient；
- 验证：正确转换 JWT claims / 正确传 tenant / 正确错误归一化。

### 7.2 集成测试

- 起完整 docker-compose：pocketd + acc-go + Memora + llm-gateway-go；
- 跑"修复 typo" 任务；
- 验证：mobile 端能看到 Live Activity + PR 完成。

### 7.3 注入测试

- 恶意 Charter / 越权 intent → 验证 acc-go taskdecompose 不被劫持；
- 跨 tenant 访问 → 验证 403。

### 7.4 故障注入测试

- 杀掉 acc-go 容器 → 验证 pocketd fallback 正常；
- 杀掉 llm-gateway-go 容器 → 验证 error 正确返回。

---

## 8. 相关文件位置

- acc-go 入口：`services/agent-control-center/acc-go/cmd/acc/main.go`
- 任务存储：`services/agent-control-center/acc-go/internal/task/`
- 任务拆分：`services/agent-control-center/acc-go/internal/taskdecompose/`
- 编排：`services/agent-control-center/acc-go/internal/orchestrator/`, `internal/orchestration/`
- MCP：`services/agent-control-center/acc-go/internal/mcp/server.go`
- LLM 客户端：`services/agent-control-center/acc-go/internal/llm/`
- Memora 客户端：`services/agent-control-center/acc-go/internal/kxmem/`
- pocketd fleetbridge 计划：`backend/internal/fleetbridge/`（v2 新增）

---

## 9. 一句话总结

**Chief = acc-go。pocketd 不实现任何规划 / 拆分 / 派发 / 监控逻辑；只做 acc-go 的 REST/RPC 客户端，把 acc-go 的能力转译为 PocketFleet Mobile 的体验。**
