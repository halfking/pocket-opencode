# 模块：Memory = Memora (kxmemory-go)

> **核心结论**：PocketFleet 的所有"长期记忆"需求 —— Charter、Skill、Agent Memory、Build Cost、Schedule —— 都落到 Memora (kxmemory-go) 的现有 v2 memories API。不新建任何记忆 schema。

---

## 1. 为什么是 Memora？

`services/kxmemory-go/` 是一个**生产就绪**的六层记忆系统（Go 1.22, 端口 `:8080`）。它的能力精确对应 PocketFleet 需要的全部记忆需求：

| PocketFleet 概念 | Memora 能力 |
|---|---|
| Charter（工作区级长期指令） | memories namespace `pocketfleet/charter/{fleet_id}` |
| Skill（共享 / 钉到 Agent） | memories namespace `pocketfleet/skill/{skill_id}` |
| Agent Memory（跨会话持久化） | memories namespace `pocketfleet/agent/{agent_id}` |
| Build Event Log | memories namespace `pocketfleet/build/{build_id}`, type=event |
| Cost Aggregate | memories namespace `pocketfleet/cost/{date}` |
| Schedule | memories namespace `pocketfleet/schedule/{id}` |
| Cross-Build Context | memories search + hybrid RAG |
| Project Context（注入 LLM） | `acc_knowledge_load` MCP tool |
| Skill 检索（语义搜索） | `acc_memora_search` MCP tool |

Memora 的能力远超 PocketFleet 现阶段的需求：

- **L1 Session**：会话级即时记忆
- **L2 Facts**：事实库
- **L3 Knowledge**：知识库
- **L4 Episode**：情节级
- **L5 Entity**：实体级
- **L6 Fact**：事实级（neo4j 图）
- **CodeGraph**：基于 tree-sitter + PageRank 的代码图谱
- **Hybrid RAG**：BM25 + dense vector + RRF
- **Dream Engine**：离线记忆整合 / 强化 / 压缩
- **Knowledge versioning / lineage / annotation**

---

## 2. 怎么用 Memora？

### 2.1 Namespace 规范

PocketFleet 在 Memora 里用统一的 namespace 前缀：

```
pocketfleet/
├── charter/{fleet_id}                          # 1 个 fleet 1 段 charter
├── skill/{skill_id}                            # 1 个 skill 1 个 memory
├── agent/{agent_id}/memory/{memory_id}         # 1 个 Agent 多条 memory
├── build/{build_id}/events                     # build 期间的 event log
├── cost/{YYYY-MM-DD}/{fleet_id}                # 日级 cost 聚合
├── schedule/{schedule_id}                      # 周期任务配置
├── project/{fleet_id}/context                  # 项目级上下文（注入 LLM）
└── audit/{YYYY-MM-DD}/{fleet_id}               # 审计 trail
```

### 2.2 Memory Type 与 Metadata

每个 Memory 用 `type` + `metadata` 字段描述：

```json
{
  "namespace": "pocketfleet/charter/ws_001",
  "type": "charter",
  "content": "# My Charter\n- Test first\n- ...",
  "metadata": {
    "fleetId": "ws_001",
    "updatedBy": "u_001",
    "updatedAt": "2026-08-23T10:00:00Z",
    "version": 3
  }
}
```

### 2.3 pocketd fleetbridge 直接调 Memora

```go
// backend/internal/fleetbridge/charter.go (伪代码)
func (h *CharterHandler) Get(ctx context.Context, fleetID string) (*Charter, error) {
    list, err := h.clients.Memora.ListMemories(ctx, memora.ListRequest{
        Namespace: fmt.Sprintf("pocketfleet/charter/%s", fleetID),
        Type:      "charter",
        Limit:     1,
    })
    if err != nil { return nil, err }
    if len(list.Items) == 0 { return &Charter{}, nil }
    return &Charter{Content: list.Items[0].Content, Version: list.Items[0].Metadata["version"]}, nil
}

func (h *CharterHandler) Put(ctx context.Context, fleetID, content string) error {
    // 获取旧版本 → diff → 写新版本
    // 触发 acc-go 重新载入 Charter（通过 acc-go 监听 Memora namespace）
    return h.clients.Memora.UpsertMemory(ctx, memora.Memory{
        Namespace: fmt.Sprintf("pocketfleet/charter/%s", fleetID),
        Type:      "charter",
        Content:   content,
        Metadata:  map[string]any{"version": time.Now().Unix(), "fleetId": fleetID},
    })
}
```

### 2.4 也可以通过 acc-go 调 Memora

acc-go 已经有 Memora client（`internal/kxmem/`），暴露了 4 个 MCP tool：

- `acc_memora_search` —— 语义搜索
- `acc_memora_store` —— 存储
- `acc_knowledge_load` —— bulk load 注入 LLM
- `acc_get_project_context` —— 项目级上下文

PocketFleet 也可以通过 acc-go MCP 调这些工具，路径：

```
pocketd ──HTTP REST──► acc-go /api/v2/mcp (tools/call) ──► Memora
```

这种方式的好处：

- 复用 acc-go 的租户隔离 + auth；
- 复用 acc-go 的 audit；
- 一次集成，到处可用（acc-go 已经把 Memora 封装好了）。

**建议**：默认走 acc-go，复杂场景（如直接 dump 全量 memories）才走 pocketd → Memora 直连。

---

## 3. Charter：详解

### 3.1 数据结构

Charter 是一个简单的 Markdown 文档 + 元数据：

```go
type Charter struct {
    FleetID    string            `json:"fleetId"`
    Content    string            `json:"content"`    // Markdown
    Version    int               `json:"version"`
    UpdatedBy  string            `json:"updatedBy"`
    UpdatedAt  time.Time         `json:"updatedAt"`
    History    []CharterSnapshot `json:"history,omitempty"`
}
```

### 3.2 Charter 注入到 LLM 的流程

```
[Mobile]
   │ 用户编辑 Charter → PUT /api/fleet/charter
   ▼
[pocketd charter.go]
   │ 1. POST Memora memories (write new version)
   │ 2. 通知 acc-go："Charter updated for ws_001"
   ▼
[acc-go]
   │ 3. 收到通知 → 加载 Charter 到 session context
   │ 4. 下次 Chief plan 自动读 Charter
   ▼
[LLM (via llm-gateway-go)]
   │ 5. system prompt 包含 Charter
```

### 3.3 Charter 注入的具体位置

通过 acc-go 的 `acc_knowledge_load` MCP tool（按需注入），或者通过 llm-gateway-go 的 session cache 预热。

---

## 4. Skill：详解

### 4.1 Skill 数据结构

Skill 是一个有 SKILL.md + 元数据的 memory：

```go
type Skill struct {
    ID          string    `json:"id"`           // "release-checklist"
    Name        string    `json:"name"`
    Description string    `json:"description"`
    Content     string    `json:"content"`      // SKILL.md content
    Source      string    `json:"source"`       // "builtin" / "github:url" / "user"
    Tags        []string  `json:"tags"`
    PinnedTo    []string  `json:"pinnedTo"`     // agent IDs
    CreatedAt   time.Time `json:"createdAt"`
    UpdatedAt   time.Time `json:"updatedAt"`
}
```

### 4.2 Skill 注入到 Agent 的流程

```
[User 在 Mobile 上] "把这个 skill 钉给 Backend Engineer Agent"
   │ POST /api/fleet/skills/{id}/pin { agentId }
   ▼
[pocketd skill.go]
   │ 1. Memora memories.update (pinnedTo += agentId)
   ▼
[acc-go 下次调度]
   │ 2. 选 Agent 时读 Memora namespace `pocketfleet/skill/?pinnedTo=agent_X`
   │ 3. 把匹配的 Skill 内容作为 system prompt 的一部分
   ▼
[LLM]
   │ 4. 知道有 release-checklist skill
```

### 4.3 内置 Skill 列表（v1 内置）

```yaml
- release-checklist    # PR 前的检查清单
- design-system        # 设计系统规范
- typescript-strict    # TypeScript strict mode 规范
- go-style             # Go 风格
- api-design           # REST API 设计原则
- security-baseline    # 安全基线
- testing-pattern      # 测试模式
- documentation        # 文档规范
```

---

## 5. Agent Memory：详解

### 5.1 概念

每个 Agent 都有跨会话的长期记忆。Agent 完成一个任务后，把"经验"自动写入 Memora；下次类似任务时，检索这些经验注入 prompt。

### 5.2 数据结构

```go
type AgentMemory struct {
    ID        string    `json:"id"`
    AgentID   string    `json:"agentId"`
    FleetID   string    `json:"fleetId"`
    Summary   string    `json:"summary"`     // 一句话摘要
    Content   string    `json:"content"`     // 详细
    Tags      []string  `json:"tags"`        // ["typescript", "oauth", "fix"]
    Source    string    `json:"source"`      // "auto:build:1234" 或 "user:explicit"
    CreatedAt time.Time `json:"createdAt"`
}
```

### 5.3 自动写入流程

```
[Agent 完成一个 build]
   │
   ▼
[acc-go agent loop]
   │ 1. 任务完成后，acc-go 自动调用 LLM (cheap model) 生成 summary
   │ 2. POST Memora memories: namespace=`pocketfleet/agent/{id}/memory/`
   ▼
[Memora]
   │ 3. 自动 embedding → 存入 Qdrant
   │ 4. 触发 Dream Engine 周期整合
```

### 5.4 检索 / 注入流程

```
[Agent 开始一个新 build]
   │
   ▼
[acc-go agent loop]
   │ 1. POST Memora search: namespace=`pocketfleet/agent/{id}/memory`, query=当前任务描述
   │ 2. 拿到 top-5 相关的历史 memory
   │ 3. 注入到 system prompt 的 "Agent Memory" 段
   ▼
[LLM]
   │ 4. 知道类似任务的历史经验
```

---

## 6. Build Event Log

### 6.1 概念

每个 Build 期间产生的大量事件（agent.message、tool.call、tool.result、gate.requested...）需要持久化，方便后续查询、回放、审计。

### 6.2 实现

直接利用 acc-go 现有的 SSE event 流 + 落 Memora（可选）。或者：

- **轻量方案**：只把关键事件（tool_call, permission, completion）落 Memora；流式事件只走 SSE，不落库。
- **完整方案**：所有事件落 Memora + 索引；提供回放 API。

### 6.3 pocketd 侧的简化

```go
// 在 ws_bridge.go 里，收到关键事件时同步落 Memora
func (b *WSBridge) onPermissionRequest(ev Event) {
    b.hub.BroadcastToFleet(ev.FleetID, "permission.request", ev)
    if b.memoraClient != nil {
        _ = b.memoraClient.Store(ctx, memora.Memory{
            Namespace: fmt.Sprintf("pocketfleet/build/%s/events", ev.BuildID),
            Type:      "permission_request",
            Content:   jsonStringify(ev),
        })
    }
}
```

---

## 7. Cost 聚合

### 7.1 数据源

LLM cost 的真实数据来自：

- **llm-gateway-go** `/api/admin/usage` —— 每个 LLM 请求的 token + cents

### 7.2 落 Memora

```http
POST /api/v2/memories
{
  "namespace": "pocketfleet/cost/2026-08-23/ws_001",
  "type": "cost_aggregate",
  "content": "{...}",
  "metadata": {
    "date": "2026-08-23",
    "fleetId": "ws_001",
    "totalCents": 12345,
    "byModel": {...},
    "byAgent": {...}
  }
}
```

### 7.3 读取

```http
GET /api/v2/memories?namespace=pocketfleet/cost/2026-08-23/ws_001
```

---

## 8. Schedule（周期任务）

### 8.1 数据结构

```go
type Schedule struct {
    ID         string    `json:"id"`
    FleetID    string    `json:"fleetId"`
    Cron       string    `json:"cron"`        // natural language: "every morning at eight"
    Intent     string    `json:"intent"`
    Enabled    bool      `json:"enabled"`
    LastRunAt  *time.Time `json:"lastRunAt,omitempty"`
    NextRunAt  *time.Time `json:"nextRunAt,omitempty"`
}
```

### 8.2 实现

pocketd 内置一个 cron worker（可以复用 `internal/quota/` 或 `internal/notification/` 现有的 scheduler）：

```go
// 每分钟 tick
func (s *Scheduler) tick(ctx context.Context) {
    for _, sched := range s.enabledSchedules() {
        if sched.NextRunAt.Before(time.Now()) {
            // 触发 intent
            s.intentHandler.Handle(ctx, IntentRequest{
                Text: sched.Intent,
                FleetID: sched.FleetID,
            })
            // 更新 NextRunAt
            sched.LastRunAt = ptr(time.Now())
            sched.NextRunAt = s.computeNextRun(sched.Cron)
            s.memora.UpdateMemory(sched.ID, sched)
        }
    }
}
```

---

## 9. 总结：PocketFleet 在 Memora 里的"印记"

```
pocketfleet/
├── charter/{fleet_id}                          # Charter 文档
├── skill/{skill_id}                            # 共享 / 钉到 Agent
├── agent/{agent_id}/memory/{memory_id}         # 跨会话经验
├── build/{build_id}/events                     # 关键事件持久化
├── cost/{YYYY-MM-DD}/{fleet_id}                # 成本聚合
├── schedule/{schedule_id}                      # 周期任务
├── project/{fleet_id}/context                  # 项目上下文（注入 LLM）
└── audit/{YYYY-MM-DD}/{fleet_id}               # 审计
```

每个 namespace 都能用现有 Memora v2 API 直接操作；不需要任何 schema 改动。
