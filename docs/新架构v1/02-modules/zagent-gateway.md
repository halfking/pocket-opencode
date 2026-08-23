# 模块设计：ZAgentGateway（核心新增，审计修正版）

> **代号**: ZAgentGateway (ZAG)
> **状态**: `planned`；当前仓库不存在该实现。
> **定位**: RedClaw（PC 桌面企业控制面）与 OpenPocket（移动）+ acc-go（任务编排）三者之间的**智能体监测与控制适配器**。
> **关键边界**: ZAG 不把 RedClaw task/session API 冒充为 OpenCode runtime API。默认保留 OpenCode coding runtime；OpenCode-compatible facade 是独立的后续项目。
> **证据等级**: 本文接口和代码均为目标设计；RedClaw generic connectors、真实 event projection、IDE adapter 和 ACC worker 注册尚未在当前仓库验证。

---

## 1. 为什么必须独立建一个 ZAgentGateway？

### 1.1 现有服务的边界

| 服务 | 它擅长 | 它不擅长 |
|---|---|---|
| **acc-go** | 任务编排 / Chief / MCP / A2A | 不应该知道 RedClaw 存在；不直接控制 PC Agent |
| **RedClaw platform-go** | 已确认：企业 task/session/control、agentcontainer、OpenClaw subprocess、策略/鉴权基础 | 当前未验证 OpenCode-compatible API；generic connectors 不是已实现的 IDE/ACP connector；不适合直接被移动端调用 |
| **pocketd (OpenPocket)** | 移动 UI；鉴权；WebSocket Hub；已有功能 | 不应该承担"PC Agent 控制中心"角色 |
| **llm-gateway-go** | LLM Provider 路由 | 与 PC Agent 控制无关 |
| **Memora** | 记忆 / Charter / Skill | 与 PC Agent 控制无关 |

**ZAgentGateway 的目标**：在不扩大 RedClaw 公网暴露面的前提下，把已验证的 RedClaw 控制面能力和未来的 OpenCode/IDE 适配能力规范化后对外暴露为 REST + MCP + WebSocket。只有通过真实合同测试的能力才能进入默认 tool allowlist。

### 1.2 不直接打通的根本原因

| 反模式 | 为什么不行 |
|---|---|
| OpenPocket 直接调 RedClaw | RedClaw 含 Plan E 审批 / Ed25519 双签 / SSO；直接暴露给移动端不安全 |
| OpenPocket 直接调 RedClaw Gateway (`:8092`) | 当前是 echo stub；且 Gateway 是 LLM 代理，不是 Agent 控制 |
| acc-go 直接调 RedClaw | 违反 acc-go 抽象；acc-go 应该只看到"worker 在跑任务" |
| pocketd 自己做这件事 | pocketd 是 mobile BFF；职责过多会导致耦合 |
| 在 RedClaw platform-go 内加 mobile API | RedClaw 是 PC 平台，加 mobile 接口会污染企业平台 |

**ZAgentGateway 是干净的边界**：边界内的复杂度归 ZAgentGateway；边界外的服务只看到简洁的 API。

---

## 2. ZAgentGateway 的六大职责

```
┌─────────────────────────────────────────────────────────────┐
│                      ZAgentGateway                           │
│                                                              │
│  1. PC Agent 注册与心跳聚合                                  │
│     - 从 RedClaw platform-go 拉 device / agent / session     │
│     - 规范化为统一格式（fleet_id / pod_id / agent_id）        │
│                                                              │
│  2. PC Agent 监测                                            │
│     - health / status / capabilities / in-flight             │
│     - SSE / WebSocket 实时推送                                │
│                                                              │
│  3. PC Agent 控制                                            │
│     - pause / resume / restart / upgrade / rollback          │
│     - 透传 RedClaw Ed25519 双签控制信号                       │
│                                                              │
│  4. 本地 runtime / IDE 适配（planned）                         │
│     - 单独实现并验证 OpenCode/ZCode/VS Code/Cursor adapter    │
│     - RedClaw connector 仅提供通用连接合同，不代表 IDE 已接通   │
│     - M0/M1 只读；写命令通过安全门禁后逐步开放                 │
│                                                              │
│  5. MCP server (Streamable HTTP)                             │
│     - 把 PC Agent 能力暴露为 zag_* MCP tools                  │
│     - 让 Cursor / Claude Code / acc-go 等 MCP 客户端调用     │
│                                                              │
│  6. acc-go worker 适配                                       │
│     - 注册为 acc-go 的 device / worker                        │
│     - 承接 acc-go 派发的任务；调用 RedClaw 执行              │
└─────────────────────────────────────────────────────────────┘
```

---

## 3. 内部包结构

```
zagent-gateway/
├── cmd/
│   ├── api/main.go                # HTTP API + MCP + WebSocket
│   └── worker/main.go             # 后台同步 worker（RedClaw 心跳聚合等）
├── internal/
│   ├── platform/                  # config / logging / errors / db / health / middleware
│   │   ├── config/                # YAML + env 配置
│   │   ├── logging/               # zerolog
│   │   ├── errors/                # stable error envelope
│   │   ├── identity/              # tenant.From(ctx) 强制 L1
│   │   ├── db/                    # pgxpool + RLS
│   │   ├── middleware/            # JWT / tracing / metrics / rate-limit
│   │   └── server/                # gin server + graceful shutdown
│   │
│   ├── redclaw/                   # RedClaw platform-go 客户端
│   │   ├── client.go              # HTTP client（mTLS + HMAC + Ed25519）
│   │   ├── orchestrator.go        # 任务提交 + 状态查询
│   │   ├── agentcontainer.go      # invoke / skills / permissions
│   │   ├── connectors.go          # 外部集成（IDE / Browser / etc）
│   │   ├── control.go             # 控制信号（pause/resume/upgrade 等）
│   │   └── types.go
│   │
│   ├── acc/                       # acc-go 客户端
│   │   ├── client.go              # 任务提交 + SSE 订阅 + worker 注册
│   │   └── types.go
│   │
│   ├── llm/                       # llm-gateway-go 客户端（兜底）
│   │   ├── client.go              # OpenAI-compatible
│   │   └── types.go
│   │
│   ├── memora/                    # Memora 客户端
│   │   ├── client.go              # /api/v2/memories CRUD + search
│   │   └── types.go
│   │
│   ├── agent/                     # Agent 注册 / 心跳 / 能力
│   │   ├── service.go
│   │   ├── registry.go
│   │   └── types.go
│   │
│   ├── pod/                       # PC device / Pod 管理
│   │   ├── service.go
│   │   └── types.go
│   │
│   ├── ide/                       # IDE 控制
│   │   ├── service.go
│   │   ├── zcode.go
│   │   ├── vscode.go
│   │   ├── cursor.go
│   │   └── opencode.go
│   │
│   ├── session/                   # OpenClaw session 包装
│   │   ├── service.go
│   │   └── types.go
│   │
│   ├── task/                      # 任务提交 / 状态机
│   │   ├── service.go
│   │   ├── state_machine.go
│   │   └── types.go
│   │
│   ├── permission/                # 权限请求 / 审批
│   │   ├── service.go
│   │   └── types.go
│   │
│   ├── mcp/                       # Streamable HTTP MCP server
│   │   ├── server.go
│   │   ├── tools.go               # zag_* tools
│   │   └── handlers.go
│   │
│   ├── ws/                        # WebSocket Hub
│   │   ├── hub.go
│   │   ├── events.go
│   │   └── handlers.go
│   │
│   └── api/                       # REST handlers
│       ├── handlers.go
│       └── routes.go
├── migrations/                    # PostgreSQL DDL
├── deploy/                        # K8s / docker-compose
└── README.md
```

---

## 4. 数据模型

### 4.1 Pod（PC 设备）

```go
type Pod struct {
    ID           string    `json:"id"`
    FleetID      string    `json:"fleetId"`
    Name         string    `json:"name"`
    Hostname     string    `json:"hostname"`
    OS           string    `json:"os"`         // "darwin" / "linux" / "windows"
    Arch         string    `json:"arch"`       // "arm64" / "amd64"
    Status       PodStatus `json:"status"`     // online / busy / offline / asleep
    CPUs         int       `json:"cpus"`
    MemoryGB     int       `json:"memoryGB"`
    GPU          string    `json:"gpu,omitempty"`
    Agents       []string  `json:"agents"`     // agent IDs
    IDEs         []string  `json:"ides"`       // ["zcode", "vscode", "cursor"]
    Region       string    `json:"region"`     // "private" / "platform-cn-hangzhou"
    LastSeen     time.Time `json:"lastSeen"`
    Metadata     map[string]any `json:"metadata,omitempty"`
}
```

### 4.2 Agent

```go
type Agent struct {
    ID            string    `json:"id"`
    FleetID       string    `json:"fleetId"`
    PodID         string    `json:"podId"`
    Name          string    `json:"name"`
    Kind          string    `json:"kind"`       // "openclaw" / "zcode" / "vscode" / "cursor" / "opencode"
    Runtime       string    `json:"runtime"`    // "openclaw@1.2.3" / "vscode@1.85"
    Status        AgentStatus `json:"status"`   // online / busy / offline / draining / quarantined
    Capabilities  []string  `json:"capabilities"` // ["file_read", "shell_run", "git_push"]
    Harness       string    `json:"harness"`    // "openclaw" / "claude-code" / "opencode-http" / "pi"
    Model         string    `json:"model"`      // "deepseek-v4-flash"
    Version       string    `json:"version"`
    LastSeen      time.Time `json:"lastSeen"`
    InFlight      int       `json:"inFlight"`
    MaxConcurrent int       `json:"maxConcurrent"`
    Metadata      map[string]any `json:"metadata,omitempty"`
}
```

### 4.3 Session

```go
type Session struct {
    ID          string    `json:"id"`
    FleetID     string    `json:"fleetId"`
    PodID       string    `json:"podId"`
    AgentID     string    `json:"agentId"`
    UserID      string    `json:"userId"`
    Title       string    `json:"title"`
    Workspace   string    `json:"workspace"`
    Status      SessionStatus `json:"status"` // active / idle / archived
    CreatedAt   time.Time `json:"createdAt"`
    UpdatedAt   time.Time `json:"updatedAt"`
    MessageCount int      `json:"messageCount"`
}
```

### 4.4 Task

```go
type Task struct {
    ID          string    `json:"id"`
    FleetID     string    `json:"fleetId"`
    SessionID   string    `json:"sessionId"`
    PodID       string    `json:"podId"`
    AgentID     string    `json:"agentId"`
    Goal        string    `json:"goal"`
    Status      TaskStatus `json:"status"` // queued / running / awaiting_permission / done / failed / cancelled
    Priority    Priority  `json:"priority"`
    Progress    *Progress `json:"progress,omitempty"`
    Cost        *CostBreakdown `json:"cost,omitempty"`
    StartedAt   *time.Time `json:"startedAt,omitempty"`
    FinishedAt  *time.Time `json:"finishedAt,omitempty"`
    Artifacts   []Artifact `json:"artifacts,omitempty"` // PR URL, commits, files
}
```

### 4.5 IDE Status

```go
type IDEStatus struct {
    Name         string    `json:"name"`       // "zcode" / "vscode" / "cursor" / "opencode"
    Version      string    `json:"version"`
    Running      bool      `json:"running"`
    Workspace    string    `json:"workspace,omitempty"`
    Extensions   []string  `json:"extensions,omitempty"`
    LastCommand  string    `json:"lastCommand,omitempty"`
    LastActivity time.Time `json:"lastActivity"`
}
```

### 4.6 Permission Request

```go
type PermissionRequest struct {
    ID          string    `json:"id"`
    TaskID      string    `json:"taskId"`
    Tool        string    `json:"tool"`       // "shell_run" / "git_push" / "file_write"
    Args        any       `json:"args"`
    RiskLevel   string    `json:"riskLevel"`  // "low" / "medium" / "high"
    Required    bool      `json:"required"`
    Reason      string    `json:"reason,omitempty"`
    ExpiresAt   time.Time `json:"expiresAt"`
}
```

---

## 5. REST API 详细

### 5.1 Agents

```http
GET /api/v1/agents
GET /api/v1/agents?fleetId=&status=&podId=&kind=
GET /api/v1/agents/:id
POST /api/v1/agents/:id/invoke
{
  "sessionId": "...",
  "message": "请修复 OAuth 登录 bug",
  "model": "deepseek-v4-flash"
}
GET /api/v1/agents/:id/events    # SSE 流
POST /api/v1/agents/:id/restart
```

### 5.2 Pods

```http
GET /api/v1/pods
GET /api/v1/pods/:id
POST /api/v1/pods/:id/control
{
  "kind": "pause" | "resume" | "restart" | "upgrade" | "rollback" | "terminate",
  "reason": "..."
}
```

### 5.3 Sessions

```http
GET /api/v1/sessions?fleetId=&status=&agentId=
POST /api/v1/sessions
{
  "agentId": "agent_001",
  "userId": "u_001",
  "title": "OAuth 修复",
  "workspace": "/Users/me/myapp"
}
GET /api/v1/sessions/:id
POST /api/v1/sessions/:id/messages
{
  "role": "user",
  "content": "..."
}
POST /api/v1/sessions/:id/cancel
POST /api/v1/sessions/:id/archive
```

### 5.4 Tasks

```http
GET /api/v1/tasks?fleetId=&status=&podId=&limit=&cursor=
POST /api/v1/tasks
{
  "sessionId": "...",
  "goal": "为 OAuth 添加 Google 登录",
  "pinnedPodId": "p_abc",
  "preferences": { "model": "deepseek-v4-flash", "maxDuration": 600 }
}
GET /api/v1/tasks/:id
GET /api/v1/tasks/:id/events    # SSE
POST /api/v1/tasks/:id/cancel
POST /api/v1/tasks/:id/follow-up
{
  "message": "补一个 integration test"
}
```

### 5.5 Permissions

```http
GET /api/v1/permissions?taskId=&status=pending
POST /api/v1/permissions/:id/reply
{
  "decision": "allow_once" | "allow_always" | "deny",
  "note": "PR looks good"
}
```

### 5.6 IDEs

```http
GET /api/v1/ide
GET /api/v1/ide/:name/status     # name: zcode / vscode / cursor / opencode
POST /api/v1/ide/:name/command
{
  "command": "open_file",
  "args": { "path": "/Users/me/myapp/auth.py" }
}
```

### 5.7 WebSocket

```text
GET /api/v1/ws
Authorization: Bearer <short-lived delegated token>
# 或先换取一次性 WS ticket；禁止长期 JWT query token

# Events:
- agent.status
- agent.message
- agent.tool_call
- agent.tool_result
- session.created
- session.message
- session.completed
- task.update
- task.completed
- task.failed
- permission.request
- permission.resolved
- pod.status
- ide.status
- cost.tick
```

---

## 6. MCP Server

```
Streamable HTTP at /mcp

Tools:
- zag_list_agents          List all PC agents
- zag_get_agent            Get one agent details
- zag_invoke_agent         Invoke an agent with a message
- zag_restart_agent        Restart an agent
- zag_list_pods            List all PC devices (pods)
- zag_get_pod              Get one pod details
- zag_control_pod          pause/resume/restart/upgrade/rollback/terminate
- zag_list_sessions        List OpenClaw sessions
- zag_create_session       Create a new session
- zag_send_message         Send a message to a session
- zag_cancel_session       Cancel a session
- zag_list_tasks           List tasks
- zag_submit_task          Submit a new task
- zag_get_task_status      Get task status
- zag_cancel_task          Cancel a task
- zag_reply_permission     Approve/deny a permission request
- zag_get_ide_status       Get IDE status (zcode/vscode/cursor/opencode)
- zag_control_ide          Send command to an IDE
- zag_search_skills        Search skills in Memora
- zag_store_skill          Store a skill
```

### 6.1 MCP 调用示例

```json
POST /mcp
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "zag_invoke_agent",
    "arguments": {
      "agentId": "agent_001",
      "sessionId": "s_001",
      "message": "请帮我修复 README 拼写错误",
      "model": "deepseek-v4-flash"
    }
  }
}

→ Response
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [{"type": "text", "text": "{\"taskId\":\"t_abc\",\"status\":\"queued\"}"}]
  }
}
```

---

## 7. 关键工作流

### 7.1 acc-go 把 ZAgentGateway 注册为 worker

```go
// ZAgentGateway 启动时
func (z *ZAgent) RegisterAsACCWorker(ctx context.Context) error {
    return z.acc.RegisterWorker(ctx, acc.RegisterWorkerRequest{
        WorkerID:    "zag_worker_001",
        Name:        "ZAgentGateway",
        Kind:        "zagent-gateway",
        Endpoint:    "http://zag:9100/api/v1/tasks",
        Capabilities: []string{"openclaw", "zcode", "vscode", "cursor", "opencode"},
    })
}

// acc-go 派任务给 ZAG
acc → POST /api/v1/tasks (ZAG)
```

### 7.2 ZAG 收到 acc-go 任务后转发到 RedClaw

```go
func (z *ZAgent) HandleACCTask(ctx context.Context, task Task) error {
    // 1. 调 RedClaw platform-go 提交任务
    rcTask, err := z.redclaw.SubmitTask(ctx, redclaw.SubmitRequest{
        TenantID:  task.FleetID,
        UserID:    task.UserID,
        AgentID:   task.AgentID,
        Goal:      task.Goal,
        Priority:  mapPriority(task.Priority),
    })
    if err != nil {
        return err
    }
    
    // 2. 启动事件桥接：RedClaw SSE → acc-go SSE
    go z.bridgeEvents(ctx, rcTask.TaskID, task.ID)
    
    return nil
}

func (z *ZAgent) bridgeEvents(ctx context.Context, rcTaskID, accTaskID string) {
    sseURL := z.redclaw.OrchestratorURL + "/api/v1/tasks/" + rcTaskID + "/events"
    sse, _ := z.redclaw.SubscribeSSE(ctx, sseURL)
    
    for event := range sse {
        // 转换 RedClaw 事件 → ZAG 事件 → acc-go 事件
        accEvent := z.translateEvent(event, accTaskID)
        z.acc.PublishTaskEvent(ctx, accTaskID, accEvent)
        
        // 也推 WebSocket 给 OpenPocket
        z.wsHub.BroadcastToFleet(fleetID, accEvent.Type, accEvent.Payload)
    }
}
```

### 7.3 OpenPocket 通过 ZAG 查询 PC Agent 状态

```go
// pocketd fleetbridge.pod.go
func (h *PodHandler) List(c echo.Context) error {
    pods, err := h.zag.ListPods(c.Request().Context(), zag.ListPodsRequest{
        FleetID: claims.WorkspaceID,
    })
    if err != nil { return jsonError(c, err) }
    return c.JSON(200, pods)
}

// ZAgentGateway redclaw.pods.go
func (z *ZAgent) ListPods(ctx context.Context, req ListPodsRequest) ([]Pod, error) {
    // 1. 从 RedClaw platform-go 拉 device 列表
    rcDevices, _ := z.redclaw.ListDevices(ctx, redclaw.ListDevicesRequest{
        TenantID: req.FleetID,
    })
    
    // 2. 从 acc-go 拉 acc-go device 列表（合并）
    accDevices, _ := z.acc.ListDevices(ctx, acc.ListDevicesRequest{
        FleetID: req.FleetID,
    })
    
    // 3. 合并 + 加 IDE / OpenClaw capability
    pods := z.mergeAndEnrich(rcDevices, accDevices)
    
    // 4. 落本地 DB（PostgreSQL）
    z.podRepo.Upsert(ctx, pods)
    
    return pods, nil
}
```

### 7.4 OpenClaw 想 git push → 手机审批

```go
// RedClaw platform-go 推 permission request 给 ZAG（sse）
// ZAG 收到后:
// 1. 落 Memora (audit)
// 2. 推 WebSocket 给 OpenPocket
// 3. 推 SSE 给 acc-go（如果有 task 上下文）

func (z *ZAgent) onPermissionRequest(ev PermissionEvent) {
    // 落 Memora
    z.memora.Upsert(ctx, memora.Memory{
        Namespace: fmt.Sprintf("pocketfleet/build/%s/events", ev.TaskID),
        Type:      "permission_request",
        Content:   jsonStringify(ev),
    })
    
    // 推 WS 给 OpenPocket
    z.wsHub.BroadcastToFleet(ev.FleetID, "permission.request", ev)
    
    // 推 SSE 给 acc-go
    z.acc.PublishTaskEvent(ctx, ev.TaskID, acc.TaskEvent{
        Type: "permission.request",
        Payload: ev,
    })
}

// OpenPocket 用户点击 Allow
// POST pocketd /api/fleet/builds/:id/permissions/:pid/reply
//   ↓
// ZAG POST /api/v1/permissions/:id/reply
//   ↓
// ZAG 透传到 RedClaw platform-go: 
//   POST /api/v1/control/:command_id/signature  (ZAG 用 Ed25519 私钥签)
//   POST /api/v1/control/:command_id/execute    (执行)
//   ↓
// RedClaw 执行 git push
//   ↓
// 事件回流到 OpenPocket
```

---

## 8. 与 RedClaw 集成的关键细节

### 8.1 鉴权（mTLS + delegated token + 独立审批签名）

ZAG 与 RedClaw platform-go 之间：

- **mTLS**：双向 TLS，证书由受管 CA 签发并绑定 ZAG instance、tenant 和环境；支持短期 leaf、轮换和撤销。禁止自动信任未知自签 CA，也禁止 mTLS 失败后降级。
- **delegated token**：每个 REST 请求带受众限定、短期 token，包含 `iss/aud/sub/tenant_id/actor/scope/jti/exp`；裸 `X-Tenant-ID` 不构成授权。
- **独立审批签名**：Rollback / Upgrade / Terminate 需要独立审批主体签名；ZAG 不得持有第二个 admin 私钥。
- **防重放/幂等**：所有写请求绑定 method/path/body hash/nonce/expiry/key_id，并要求 `Idempotency-Key`。

### 8.2 事件桥接（关键）

RedClaw platform-go 的事件流是 SSE，ZAG 需要把它转换为：

- ZAG 内部事件（落到 ws.Hub）；
- Memora 事件日志；
- acc-go 兼容事件（如果 acc-go 上有任务上下文）；
- OpenPocket WebSocket 事件。

事件映射表：

| RedClaw Event | ZAG Event | Memora | acc-go | OpenPocket WS |
|---|---|---|---|---|
| `task.submitted` | `task.update` (status=queued) | planned | planned | planned |
| `task.running` | `task.update` (status=running) | planned | planned | planned |
| `agent.message` | `agent.message` (delta) | planned (summary) | planned | planned |
| `agent.tool_call` | `agent.tool_call` | planned | planned | planned |
| `agent.tool_result` | `agent.tool_result` | planned | planned | planned |
| `permission.requested` | `permission.request` | planned | planned | planned |
| `permission.approved` | `permission.resolved` | planned | planned | planned |
| `task.completed` | `task.completed` | planned | planned | planned |
| `task.failed` | `task.failed` | planned | planned | planned |
| `device.status` | `pod.status` | no | planned | planned |
| `ide.status` | `ide.status` | no | no | blocked（无已验证 IDE connector） |
| `usage.tick` | `cost.tick` | planned | planned | planned |

### 8.3 RedClaw Gateway (`:8092`) 的接管

当前 Pocket legacy bridge 的目标端点与 RedClaw platform-go 主接口不匹配，历史 Gateway 还存在 echo/mock 证据。处理方式：

- v3 控制面不依赖该 legacy Gateway；
- LLM 调用走经过合同验证的 llm-gateway-go 或 RedClaw OpenAI-compatible gateway；
- Provider fallback 必须受 tenant 数据驻留和模型 allowlist 约束，不能因故障自动越区；
- echo/mock response 只能作为失败或 mock evidence，不能标记任务成功。

---

## 9. 与 acc-go 集成的关键细节

### 9.1 ZAG 注册为 acc-go worker

```http
POST /api/v2/devices  (acc-go)
Authorization: Bearer <acc-internal-secret>
Content-Type: application/json
X-Tenant-ID: ws_001

{
  "deviceId": "zag_worker_001",
  "name": "ZAgentGateway",
  "kind": "zagent-gateway",
  "endpoint": "http://zag:9100/api/v1/tasks",
  "capabilities": ["openclaw", "zcode", "vscode", "cursor", "opencode"],
  "maxConcurrent": 10
}
```

### 9.2 acc-go 派任务给 ZAG

```http
POST /api/v1/tasks  (ZAG, 由 acc-go 调)
Authorization: Bearer <hmac-token>
Content-Type: application/json
X-Tenant-ID: ws_001

{
  "taskId": "t_001",      // acc-go 的 task ID
  "fleetId": "ws_001",
  "sessionId": "s_001",
  "agentId": "agent_002",
  "goal": "OAuth 修复",
  "model": "deepseek-v4-flash",
  "pinnedPodId": null
}
```

### 9.3 ZAG 推事件给 acc-go

```http
POST /api/v2/missions/{taskId}/events  (acc-go)
Authorization: Bearer <acc-internal-secret>
Content-Type: application/x-ndjson
X-Tenant-ID: ws_001

event: task.update
data: {"status":"running","ts":...}

event: agent.message
data: {"role":"assistant","delta":"...","ts":...}
```

（这与 acc-go 的 SSE 事件格式一致；ZAG 直接复用。）

---

## 10. 与 Memora 集成的关键细节

### 10.1 Namespace 约定

```
pocketfleet/
├── charter/{fleet_id}
├── skill/{skill_id}
├── agent/{agent_id}/memory/{memory_id}
├── build/{build_id}/events
├── cost/{YYYY-MM-DD}/{fleet_id}
├── schedule/{schedule_id}
├── project/{fleet_id}/context
└── audit/{YYYY-MM-DD}/{fleet_id}
```

ZAG 主要写：

- `build/{id}/events`：所有 task / agent / permission 事件；
- `agent/{id}/memory/`：跨任务 Agent 记忆；
- `cost/{date}/{fleet_id}`：成本聚合；
- `charter/{fleet_id}`：Charter；
- `skill/{skill_id}`：Skill。

### 10.2 ZAG 写 Memora 的代码骨架

```go
// internal/memora/client.go
package memora

import (
    "context"
    "encoding/json"
    "net/http"
    "time"

    "github.com/halfking/pocket-opencode/backend/internal/zagent-gateway/internal/identity"
)

type Client struct {
    BaseURL string
    Secret  string
    HTTPDo  *http.Client
}

func New(baseURL, secret string) *Client {
    return &Client{
        BaseURL: baseURL,
        Secret:  secret,
        HTTPDo:  &http.Client{Timeout: 30 * time.Second},
    }
}

func (c *Client) Upsert(ctx context.Context, m Memory) (*Memory, error) {
    req, _ := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/v2/memories", jsonBody(m))
    req.Header.Set("Authorization", "Bearer "+c.Secret)
    req.Header.Set("Content-Type", "application/json")
    if claims, ok := identity.FromContext(ctx); ok {
        req.Header.Set("X-Tenant-ID", claims.WorkspaceID)
        req.Header.Set("X-User-ID", claims.UserID)
    }
    // ... send + decode
}

func (c *Client) Search(ctx context.Context, namespace, query string, topK int) ([]Memory, error) {
    // POST /api/v2/memories/search
}
```

---

## 11. 与 llm-gateway-go 集成的关键细节

### 11.1 兜底路径

主路径：ZAG → RedClaw Gateway (`:8092`) → llm-gateway-go → Provider。
兜底路径：ZAG → llm-gateway-go 直连 → Provider。

如果 RedClaw Gateway 不可达或返回 echo stub，ZAG 自动 fallback。

### 11.2 LLM Provider 注册

llm-gateway-go 已支持：

- OpenAI / Anthropic / Responses / Gemini 原生协议；
- DeepSeek 一等公民（OpenAI-compatible）；
- Qwen / GLM / Doubao（中国境内 LLM）。

ZAG 不修改 llm-gateway-go 配置，只调它的 API。

---

## 12. 与 pocketd 集成的关键细节

pocketd fleetbridge 新增 `internal/zag/` 子包，封装对 ZAG 的调用。

```go
// backend/internal/fleetbridge/zag/client.go
package zag

type Client struct {
    BaseURL string
    Secret  string
    HTTPDo  *http.Client
}

func New(baseURL, secret string) *Client {
    return &Client{BaseURL: baseURL, Secret: secret, HTTPDo: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) ListPods(ctx context.Context, req ListPodsRequest) ([]Pod, error) {
    var resp struct{ Items []Pod `json:"items"` }
    err := c.do(ctx, "GET", "/api/v1/pods?fleetId="+req.FleetID, nil, &resp)
    return resp.Items, err
}

// ... 其他方法
```

---

## 13. 安全模型（审计修正版）

> ZAgentGateway 当前不存在，以下是实现门禁，不是已具备能力。

- **身份**：使用短期 delegated token + mTLS；token 必须验证 `iss/aud/sub/tenant_id/actor/scope/jti/exp`。裸 `X-Tenant-ID`、`X-User-ID`、`fleetId` 不能独立授权。
- **mTLS**：受管 CA、一次性 enrollment、证书绑定、轮换和撤销；失败必须 fail-closed，禁止降级为 HMAC。
- **授权**：M0 实现 viewer/operator/approver/admin 的对象级 RBAC/ABAC；MCP 使用独立 client/scopes/tool allowlist。
- **命令**：仅允许注册的 command schema；使用 argv、workspace root/path canonicalization、symlink/TOCTOU 防护、环境变量和 connector endpoint allowlist。
- **审批**：`allow_always` 必须绑定资源、工具、参数、版本和有效期；高危控制的第二签名来自独立主体/设备/审批服务，ZAG 不持有第二个 admin 私钥。
- **请求安全**：写请求使用 `Idempotency-Key`、operation ID、body hash、nonce、expiry 和 key ID；超时先 query/reconcile，不得盲目重试。
- **事件**：事件包含 event ID、sequence、tenant、aggregate version、schema version；支持 Last-Event-ID 补偿、去重、背压和断线重认证。
- **审计**：持久 outbox + append-only/WORM 归档；审计不可写时高危操作停止。Memora 可用于检索和长期记忆，但不是唯一不可篡改审计存储。
- **状态**：approval、control command、operation mapping、event cursor、idempotency result 必须持久化，不能只存进程内 map。
- **数据**：代码、diff、prompt、命令输出、密钥、IDE/PC 元数据按数据分类和租户驻留策略处理；不默认写普通日志。

### 13.1 OpenCode 兼容边界

RedClaw task/session API 与 OpenCode runtime API 不同。ZAG 的 `/api/v1/sessions` 是控制面包装，不是 OpenCode drop-in API。默认保留 OpenCode runtime；如需无感兼容，必须另行实现并验证：

```text
/session
/session/:id/message（parts）
/event（EventV2/SSE）
/permission、/question
/pty（WebSocket）
```

在固定 OpenCode 版本真实联调通过前，OpenCode 只作为受控 runtime，不由 RedClaw 直接替换。

---

## 14. 测试策略（发布门禁）

### 14.1 单元测试

- mock RedClaw / ACC / Memora / LLM client；
- 验证：事件转换、身份注入、对象授权、错误归一化、幂等和 nonce。

### 14.2 合同测试

- 固定 OpenCode commit，覆盖 `/session`、`message(parts)`、`/event`、permission/question、PTY；
- RedClaw facade/task/run 与 ZAG mapping 的 mock/真实 endpoint 差异；
- generic Connector 不得直接通过 IDE contract 测试，除非有实际 adapter。

### 14.3 集成测试

- 逐步启动 Pocket + ACC + ZAG + RedClaw mock + 固定 OpenCode + Memora + LLM gateway；
- M0 只读；M1 低风险；M2 才开放受控写；
- 跑通 operation mapping、事件回流、移动审批和 reconciliation。

### 14.4 故障注入

- RedClaw/ACC/ZAG/Memora/LLM 任一不可用；
- mTLS 失败、token 过期/撤销、nonce 重放、签名篡改；
- SSE/WS 断线、慢消费者、重复事件、未知执行结果；
- OpenCode runtime crash 和 IDE connector 断连。

### 14.5 安全测试

- 跨 tenant、越权 task/pod/session/IDE；
- 路径逃逸、shell 注入、SSRF、环境变量泄露、TOCTOU；
- 双签 signer independence、canonical payload、一次性消费；
- 审计 outbox 恢复和高危 fail-closed。

---

## 15. 部署（目标配置；未实现）

```yaml
services:
  zagent-gateway:
    image: zagent-gateway:latest
    ports: ["9100:9100"]
    environment:
      - ZAG_REDCLAW_URL=https://redclaw.internal:8080
      - ZAG_REDCLAW_TLS_CERT=/certs/zag.crt
      - ZAG_REDCLAW_TLS_KEY=/certs/zag.key
      - ZAG_REDCLAW_TOKEN_ISSUER_KEY_REF=${REDCLAW_TOKEN_ISSUER_KEY_REF}
      - ZAG_REDCLAW_MTLS_CA=/certs/redclaw-ca.crt
      - ZAG_ACC_URL=http://acc-go:4101
      - ZAG_ACC_DELEGATED_AUDIENCE=acc-go
      - ZAG_MEMORA_URL=http://memora:8080
      - ZAG_MEMORA_TOKEN_ISSUER_KEY_REF=${MEMORA_TOKEN_ISSUER_KEY_REF}
      - ZAG_LLM_GATEWAY_URL=http://llm-gateway:8781
      - ZAG_LLM_GATEWAY_KEY_REF=${LLM_GATEWAY_KEY_REF}
      - ZAG_DATABASE_URL=${DATABASE_URL}
      - ZAG_SIGNING_KEY_REF=${ZAG_SIGNING_KEY_REF}
      - ZAG_AUDIT_OUTBOX_REQUIRED=true
```

### 15.2 K8s

- Deployment: 2-3 副本；
- Service: ClusterIP；
- Ingress: 9100 (HTTPS) + 9101 (MCP, HTTPS);
- ConfigMap: 配置；
- Secret: 密钥；
- PVC: 1GB（PostgreSQL schema metadata）；
- HPA: CPU > 70% → scale up。

---

## 16. 相关文件位置（v3 新增）

- 计划新建：`zagent-gateway/`（仓库根或 monorepo `services/zagent-gateway/`）
- pocketd 客户端：`opencode-pocket/backend/internal/fleetbridge/zag/`
- acc-go 集成：`services/agent-control-center/acc-go/cmd/acc/main.go`（mount device register 逻辑）
- RedClaw 客户端：`zagent-gateway/internal/redclaw/`

---

## 17. 一句话总结

**ZAgentGateway = PC 桌面（RedClaw / OpenClaw / 本地 IDE）能力的统一 API 出口；OpenPocket / acc-go / 第三方 MCP 客户端都通过它访问 PC Agent；既不污染 RedClaw 企业平台，也不让 pocketd 变臃肿。**
