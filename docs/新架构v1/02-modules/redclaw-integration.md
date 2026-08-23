# 模块设计：RedClaw 集成（ZAgentGateway ↔ RedClaw platform-go，审计修正版）

> **状态**：目标适配设计；当前 ZAG 尚未实现，RedClaw generic connectors 也不是已验证的 IDE/ACP connector。
> 
> **重要结论**：RedClaw platform-go 可以作为企业控制面和 OpenClaw 执行适配器，但不能直接作为 OpenCode drop-in backend。默认保留 OpenCode runtime；需要无感兼容时另行实现 OpenCode-compatible facade。

---

## 1. RedClaw 是什么

> 来自 RedClaw README：
> "RedClaw is the enterprise re-platforming of OpenClaw, an open-source AI assistant. It wraps OpenClaw into a multi-cloud, multi-tenant, multi-agent orchestration platform."

- **形态**: PC 桌面 AI 助手（OpenClaw）+ 企业级包装；
- **后端**: `services/platform-go/`（Go 1.25），多个微服务：
  - `api` (:8080) — 主 HTTP API；
  - `orchestrator` (:8090) — 任务队列 + 会话 + 控制信号 + WS 事件；
  - `agentcontainer` (:8091) — OpenClaw runtime + skills + permissions；
  - `gateway` (:8091 同进程 /api/gateway/*) — Bedrock / LLM 代理；
  - `authagent` (:8092) — SSO + 审批；
  - `admin` (:8093) — 控制台后端；
  - `dal` — 数据访问层。
- **OpenClaw CLI**: npm 包，装在用户 Mac 上的桌面 AI 助手运行时。

---

## 2. ZAG 与 RedClaw 的关系

```
┌─────────────────────────────────────────────────────────────┐
│                     ZAgentGateway                            │
│                                                              │
│   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐         │
│   │ redclaw/    │  │ redclaw/    │  │ redclaw/    │         │
│   │ orchestrator│  │agentcontainer│ │connectors  │         │
│   │  client     │  │   client    │  │  client    │         │
│   └──────┬──────┘  └──────┬──────┘  └──────┬──────┘         │
│          │                │                │                │
└──────────┼────────────────┼────────────────┼────────────────┘
           │                │                │
           ▼                ▼                ▼
   ┌──────────────────────────────────────────────────┐
   │       RedClaw platform-go :8080-8093              │
   │                                                  │
   │   orchestrator(:8090)  agentcontainer(:8091)     │
   │   connectors(/)        authagent(:8092)         │
   └──────────────────────────────────────────────────┘
                          │
                          ▼
   ┌──────────────────────────────────────────────────┐
   │       OpenClaw CLI (user's Mac)                  │
   │       + Local IDE plugins                        │
   └──────────────────────────────────────────────────┘
```

**ZAG 是 RedClaw 的"控制中心中介"** —— 它不直接连 OpenClaw CLI，而是通过 RedClaw platform-go 的 HTTP API。

---

## 3. 鉴权

### 3.1 三层身份与传输保护（目标契约）

| 层 | 机制 | 用途 |
|---|---|---|
| **mTLS** | 受管 CA、短期证书、绑定 ZAG instance/tenant | 服务身份和传输保护；失败必须 fail-closed |
| **delegated token** | `iss/aud/sub/tenant_id/actor/scope/jti/exp` | 用户/服务委托和对象授权 |
| **独立审批签名** | 独立主体/设备/审批服务 | 高危控制；ZAG 不持有第二个 admin 私钥 |

裸 `X-Tenant-ID`、`X-User-ID` 或 body 字段不能独立授权。所有写请求还要求 idempotency key、nonce 和签名覆盖。

```yaml
# zagent-gateway.yaml
redclaw:
  url: https://redclaw.internal:8080
  tls_cert: /certs/zag.crt
  tls_key: /certs/zag.key
  ca_cert: /certs/redclaw-ca.crt
  hmac_secret: ${REDCLAW_HMAC_SECRET}
  ed25519_private_key: /keys/zag_ed25519.key
  ed25519_public_key: /keys/zag_ed25519.pub
```

### 3.3 ZAG 启动时建立会话

```go
func (z *ZAgent) InitRedClawClient(ctx context.Context) error {
    cert, err := tls.LoadX509KeyPair(z.cfg.RedClaw.TLSCert, z.cfg.RedClaw.TLSKey)
    if err != nil { return err }
    
    caPool := x509.NewCertPool()
    caCert, _ := os.ReadFile(z.cfg.RedClaw.CACert)
    caPool.AppendCertsFromPEM(caCert)
    
    z.redclaw = redclaw.NewClient(redclaw.Config{
        BaseURL: z.cfg.RedClaw.URL,
        TLS:     &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: caPool},
        HMACSecret: z.cfg.RedClaw.HMACSecret,
        Ed25519Key: loadEd25519(z.cfg.RedClaw.Ed25519PrivateKey),
        FleetID:    z.fleetID,
    })
    
    // Health check
    return z.redclaw.HealthCheck(ctx)
}
```

---

## 4. RedClaw Orchestrator Client

### 4.1 任务提交

```go
func (z *ZAgent) SubmitTask(ctx context.Context, req SubmitRequest) (*Task, error) {
    rcReq := redclaw.SubmitTaskRequest{
        TenantID:  req.FleetID,
        UserID:    req.UserID,
        AgentID:   req.AgentID,
        SessionID: req.SessionID,
        Goal:      req.Goal,
        Priority:  z.mapPriority(req.Priority),
        TimeoutSec: req.TimeoutSec,
        Metadata:  req.Metadata,
    }
    return z.redclaw.Orchestrator.SubmitTask(ctx, rcReq)
}

// ZAG API 暴露给 OpenPocket
// POST /api/v1/tasks
{
  "sessionId": "s_001",
  "agentId": "agent_002",
  "goal": "为 OAuth 添加 Google 登录",
  "priority": "normal",
  "timeoutSec": 600
}
```

### 4.2 任务状态查询

```go
// GET /api/v1/tasks/:id
func (z *ZAgent) GetTask(ctx context.Context, taskID string) (*Task, error) {
    rcTask, err := z.redclaw.Orchestrator.GetTask(ctx, taskID)
    if err != nil { return nil, err }
    
    return z.translateTask(rcTask), nil
}

// Translation: RedClaw task → ZAG task
func (z *ZAgent) translateTask(rc *redclaw.Task) *Task {
    return &Task{
        ID:        rc.TaskID,
        FleetID:   rc.TenantID,
        PodID:     rc.Metadata["podId"],
        AgentID:   rc.AgentID,
        Goal:      rc.Goal,
        Status:    z.mapStatus(rc.Status),
        Priority:  z.mapPriority(rc.Priority),
        StartedAt: rc.StartedAt,
        FinishedAt: rc.FinishedAt,
    }
}
```

### 4.3 任务取消

```go
// POST /api/v1/tasks/:id/cancel
func (z *ZAgent) CancelTask(ctx context.Context, taskID string) error {
    return z.redclaw.Orchestrator.CancelTask(ctx, taskID)
}
```

### 4.4 任务事件订阅（SSE）

```go
// GET /api/v1/tasks/:id/events
func (z *ZAgent) SubscribeTaskEvents(ctx context.Context, taskID string) (<-chan Event, error) {
    sse, err := z.redclaw.Orchestrator.SubscribeTaskEvents(ctx, taskID)
    if err != nil { return nil, err }
    
    out := make(chan Event, 64)
    go func() {
        defer close(out)
        for ev := range sse {
            out <- z.translateEvent(ev, taskID)
        }
    }()
    return out, nil
}

func (z *ZAgent) translateEvent(rc redclaw.Event, taskID string) Event {
    return Event{
        Type:      mapEventType(rc.Type),
        TaskID:    taskID,
        Payload:   rc.Payload,
        Timestamp: rc.Timestamp,
    }
}

func mapEventType(rcType string) string {
    switch rcType {
    case "task.submitted":     return "task.update"
    case "task.running":       return "task.update"
    case "task.completed":     return "task.completed"
    case "task.failed":        return "task.failed"
    case "agent.message":      return "agent.message"
    case "agent.tool_call":    return "agent.tool_call"
    case "agent.tool_result":  return "agent.tool_result"
    case "permission.request": return "permission.request"
    case "permission.reply":   return "permission.resolved"
    case "device.status":      return "pod.status"
    case "ide.status":         return "ide.status"
    case "usage.tick":         return "cost.tick"
    default:                   return "agent.event"
    }
}
```

---

## 5. RedClaw AgentContainer Client

### 5.1 Agent 列表 / 详情

```go
// GET /api/v1/agents (ZAG) → 翻译自 RedClaw platform-go

func (z *ZAgent) ListAgents(ctx context.Context, req ListAgentsRequest) ([]Agent, error) {
    // 1. 从 RedClaw platform-go 拉 device 列表
    rcDevices, _ := z.redclaw.Orchestrator.ListDevices(ctx, redclaw.ListDevicesRequest{
        TenantID: req.FleetID,
    })
    
    // 2. 拉每个 device 上的 agent 详情
    var agents []Agent
    for _, dev := range rcDevices {
        rcAgents, _ := z.redclaw.AgentContainer.ListAgents(ctx, dev.ID)
        for _, rca := range rcAgents {
            agents = append(agents, z.translateAgent(rca, dev))
        }
    }
    
    // 3. 落本地 DB
    z.agentRepo.Upsert(ctx, agents)
    
    return agents, nil
}

func (z *ZAgent) translateAgent(rca redclaw.Agent, dev redclaw.Device) Agent {
    return Agent{
        ID:       rca.AgentID,
        FleetID:  dev.TenantID,
        PodID:    dev.DeviceID,
        Name:     rca.Name,
        Kind:     rca.Kind,
        Runtime:  rca.RuntimeVersion,
        Status:   z.mapAgentStatus(rca.Status),
        Capabilities: rca.Capabilities,
        Harness:  rca.Harness,
        Model:    rca.Model,
        LastSeen: rca.LastHeartbeatAt,
    }
}
```

### 5.2 Agent Invoke（直接调用）

```go
// POST /api/v1/agents/:id/invoke
func (z *ZAgent) InvokeAgent(ctx context.Context, agentID string, req InvokeRequest) (*InvocationResult, error) {
    // 1. 找 agent 对应的 device
    agent, err := z.agentRepo.Get(ctx, agentID)
    if err != nil { return nil, err }
    
    // 2. 调 RedClaw agentcontainer invoke
    result, err := z.redclaw.AgentContainer.Invoke(ctx, redclaw.InvokeRequest{
        DeviceID:  agent.PodID,
        AgentID:   agentID,
        SessionID: req.SessionID,
        UserID:    req.UserID,
        Message:   req.Message,
        Model:     req.Model,
        Timeout:   req.Timeout,
    })
    if err != nil { return nil, err }
    
    return &InvocationResult{
        InvocationID: result.InvocationID,
        Reply:        result.Reply,
        TokensIn:     result.TokensIn,
        TokensOut:    result.TokensOut,
        Duration:     result.Duration,
    }, nil
}
```

### 5.3 Skills 查询 / 调用

```go
// GET /api/v1/skills
func (z *ZAgent) ListSkills(ctx context.Context, req ListSkillsRequest) ([]Skill, error) {
    return z.redclaw.AgentContainer.ListSkills(ctx, redclaw.ListSkillsRequest{
        TenantID: req.FleetID,
        AgentID:  req.AgentID,
    })
}
```

### 5.4 Permissions Profiles

```go
// POST /api/v1/permissions/profiles
func (z *ZAgent) UpsertPermissionProfile(ctx context.Context, req UpsertProfileRequest) error {
    return z.redclaw.AgentContainer.UpsertProfile(ctx, redclaw.UpsertProfileRequest{
        TenantID:    req.FleetID,
        PositionID:  req.PositionID,
        Profile:     req.Profile,
    })
}
```

---

## 6. RedClaw Connectors Client（IDE 控制的关键）

### 6.1 为什么 Connectors 是 IDE 控制的天然接口

RedClaw Connectors 的设计（`internal/connectors/connectors.go`）已经把外部集成的标准接口定义得很清楚：

- **AuthMode**: "oauth2", "api_key", "basic", "mTLS"；
- **CursorType**: "offset", "timestamp", "webhook"；
- **SideEffectLevel**: 操作的危险等级；
- **Idempotency Key**: 幂等保证；
- **Policy Snapshot + Assurance Permit**: 权限校验。

**这正好是 IDE 控制需要的** —— ZCode / VS Code / Cursor / OpenCode 都通过 Connector 注册。

### 6.1 为什么 generic Connectors 还不能证明 IDE 已接通

RedClaw `internal/connectors/` 提供的是通用外部系统连接合同（auth mode、cursor、ingest、execute、idempotency、side-effect），但源码中的内存实现仍包含 stub credential/receipt，当前未发现 ZCode、VS Code、Cursor、OpenCode 的真实 adapter、endpoint、ACP 或 OpenCode event contract。

因此 ZAG 必须为每个 IDE 单独完成：

- 本地出站 agent/extension 或受控 socket；
- connector registration 与 endpoint allowlist；
- 命令 schema、workspace/path sandbox；
- status/command/stream contract；
- tenant/RBAC/approval/audit；
- 真实 IDE 集成测试。

在此之前，IDE 能力标记为 `blocked`/`planned`，不能把示例注册代码当成已实现。

### 6.2 IDE Connector 注册（目标合同）

```go
// ZAG 启动时为每个支持的 IDE 注册 Connector
func (z *ZAgent) RegisterIDEConnectors(ctx context.Context) error {
    ides := []struct{
        Kind    string
        AuthMode string
        Endpoints []string
    }{
        {"zcode",    "mTLS",  []string{"localhost:7777"}},
        {"vscode",   "oauth2", []string{"https://vscode.dev/api"}},
        {"cursor",   "oauth2", []string{"https://api.cursor.sh"}},
        {"opencode", "mTLS",  []string{"http://localhost:4096"}},
    }
    
    for _, ide := range ides {
        z.redclaw.Connectors.RegisterConnector(ctx, redclaw.ConnectorDefinition{
            ConnectorID:   "ide_" + ide.Kind,
            TenantID:      z.fleetID,
            Name:          ide.Kind,
            Version:       "1.0.0",
            AuthMode:      ide.AuthMode,
            Endpoints:     ide.Endpoints,
            DataClasses:   []string{"workspace", "files", "commands"},
            RateLimit:     60,
            CursorType:    "webhook",
            SideEffectLevel: "medium",
        })
    }
    
    return nil
}
```

### 6.3 IDE 状态查询

```go
// GET /api/v1/ide/:name/status
func (z *ZAgent) GetIDEStatus(ctx context.Context, name string) (*IDEStatus, error) {
    // 1. 通过 Connector Ingest 拉状态
    connection, err := z.redclaw.Connectors.GetConnection(ctx, redclaw.ConnectionQuery{
        TenantID:     z.fleetID,
        ConnectionID: "ide_" + name,
    })
    if err != nil { return nil, err }
    
    // 2. 通过 Connector Execute 拉 IDE 实际状态
    receipt, err := z.redclaw.Connectors.Execute(ctx, redclaw.ExecuteCommand{
        TenantID:       z.fleetID,
        ConnectionID:   connection.ConnectionID,
        Operation:      "status",
        IdempotencyKey: uuid.NewString(),
    })
    if err != nil { return nil, err }
    
    // 3. 解析 receipt.ResponseBody
    var status IDEStatus
    json.Unmarshal([]byte(receipt.ResponseBody), &status)
    return &status, nil
}
```

### 6.4 IDE 命令执行

```go
// POST /api/v1/ide/:name/command
func (z *ZAgent) ExecuteIDECommand(ctx context.Context, name string, req IDECommand) (*ExecutionReceipt, error) {
    return z.redclaw.Connectors.Execute(ctx, redclaw.ExecuteCommand{
        TenantID:       z.fleetID,
        ConnectionID:   "ide_" + name,
        Operation:      req.Command,
        Payload:        req.Args,
        IdempotencyKey: uuid.NewString(),
    })
}

// ZAG API
// POST /api/v1/ide/zcode/command
{
  "command": "open_file",
  "args": { "path": "/Users/me/myapp/auth.py" }
}

// POST /api/v1/ide/cursor/command
{
  "command": "apply_diff",
  "args": {
    "path": "/Users/me/myapp/auth.py",
    "diff": "..."
  }
}

// POST /api/v1/ide/opencode/command
{
  "command": "session.create",
  "args": { "title": "OAuth 修复" }
}
```

### 3.5 各 IDE 的具体适配

详见 [ide-control.md](ide-control.md)。

---

## 7. RedClaw Control Signals（控制 Agent）

### 7.1 控制信号类型

```go
const (
    KindPause     Kind = "pause"
    KindResume    Kind = "resume"
    KindRedirect  Kind = "redirect"
    KindRetry     Kind = "retry"
    KindRollback  Kind = "rollback"   // 需要 Ed25519 双签
    KindUpgrade   Kind = "upgrade"    // 需要 Ed25519 双签
    KindTerminate Kind = "terminate"  // 需要 Ed25519 双签
    KindInject    Kind = "inject"
)
```

### 7.2 ZAG 发出控制信号

```go
// POST /api/v1/pods/:id/control
func (z *ZAgent) ControlPod(ctx context.Context, podID string, req ControlRequest) error {
    // 1. 创建 control command
    cmd := redclaw.NewCommand(req.TaskID, req.SessionID, z.zagID, redclaw.Kind(req.Kind), req.Args)
    
    // 2. ZAG 用自己的 Ed25519 私钥签第一个签名
    if err := cmd.Sign(z.redclaw.Dispatcher, z.zagID, "zagent-gateway", z.ed25519Priv); err != nil {
        return err
    }
    
    // 3. 若是双签需求（Rollback/Upgrade/Terminate），需要第二个签名
    if redclaw.RequiresDoubleSignature(cmd.Kind) {
        // 这里有几种选择：
        // a) 同步等用户审批 → 通过 OpenPocket Mobile
        // b) 异步让 admin 审批 → 后台 cron 检查
        
        // 简化版：同步等 ZAG 管理员审批（通过 ZAG 自己的 admin endpoint）
        if err := z.waitForSecondSignature(ctx, cmd, 5*time.Minute); err != nil {
            return err
        }
    }
    
    // 4. 执行
    return z.redclaw.Orchestrator.ExecuteControl(ctx, cmd)
}

func (z *ZAgent) waitForSecondSignature(ctx context.Context, cmd *redclaw.Command, timeout time.Duration) error {
    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    
    // 注册命令到待签列表
    z.pendingCmds[cmd.CommandID] = cmd
    
    // 等审批（通过 admin API 或直接调 Ed25519 库）
    select {
    case <-cmd.Signed():
        return nil
    case <-ctx.Done():
        return ErrTimeout
    }
}
```

### 7.3 Rollback 双签流程（完整）

```
1. OpenPocket 用户在手机点击 "Rollback Pod"
2. POST pocketd /api/fleet/pods/:podId/control { kind: "rollback" }
3. pocketd fleetbridge → ZAG POST /api/v1/pods/:podId/control
4. ZAG:
   a. 创建 control command
   b. ZAG Ed25519 签第一个
   c. 需要第二个签名 → 推给 ZAG admin (或 ZAG 的 pending queue)
   d. 等第二个签名
5. 第二个 admin 在 ZAG admin UI 上签名（或者通过飞书 / 邮件一键审批）
6. ZAG 用 admin 私钥签第二个
7. ZAG 调 RedClaw platform-go: POST /api/v1/control/:command_id/execute
8. RedClaw platform-go 验证双签 → 执行 rollback
9. 事件回流到 OpenPocket
```

---

## 8. RedClaw AuthAgent（SSO + 审批）

### 8.1 SSO 登录（OpenPocket 用户用）

```go
// POST /api/v1/auth/login (RedClaw platform-go)
func (z *ZAgent) RedClawLogin(ctx context.Context, req LoginRequest) (*LoginResponse, error) {
    return z.redclaw.AuthAgent.Login(ctx, redclaw.LoginRequest{
        TenantID: req.FleetID,
        Username: req.Username,
        Password: req.Password,
        // 或 OAuth2 SSO
    })
}
```

### 8.2 Approval Flow（Plan A / Plan E）

```go
// POST /api/v1/approvals (RedClaw platform-go)
func (z *ZAgent) RequestApproval(ctx context.Context, req ApprovalRequest) (*ApprovalToken, error) {
    return z.redclaw.AuthAgent.RequestApproval(ctx, redclaw.ApprovalRequest{
        TenantID:      req.FleetID,
        UserID:        req.UserID,
        Operation:     req.Operation,
        Reason:        req.Reason,
        PlanLevel:     req.PlanLevel, // "A" 或 "E"
    })
}
```

---

## 9. RedClaw Gateway / LLM 路由（目标适配）

### 9.1 当前状态与使用规则

RedClaw Gateway/legacy Pocket endpoint 当前存在 echo/mock 或接口不一致证据，不能作为生产成功响应。ZAG 的默认 LLM 路径必须使用已完成合同验证的 llm-gateway-go/RedClaw OpenAI-compatible gateway；Provider fallback 受 tenant 数据驻留、模型 allowlist 和审计策略约束。

- echo/mock response 只允许在 mock-only 测试中出现；
- 生产任务必须有 provider/model/usage 证据；
- gateway 失败不应绕过 RedClaw/审批控制直接执行高危 runtime 操作；
- 对外服务端口以实际 RedClaw deployment 为准，不能将 `8092` 同时假设为 authagent 和 legacy Gateway 的同一监听器。

```go
// 目标 LLM client 伪代码；不代表当前已实现。
type LLMClient struct {
    verifiedGateway *llmgateway.Client
}

func (l *LLMClient) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
    // 只调用经过合同验证、符合 tenant/model residency policy 的 gateway。
    return l.verifiedGateway.Chat(ctx, req)
}
```

---

## 10. 数据流：目标路径（OpenPocket → RedClaw 控制面 → 已验证 runtime）

> 以下是目标设计，不是当前已跑通的端到端证据。默认 runtime 可以是 OpenCode；OpenClaw 仅在其独立合同和安全策略通过后启用。

```
[OpenPocket Mobile]
  │ POST /api/fleet/intent { text: "修复 OAuth bug" }
  ▼
[pocketd :8088]
  │ fleetbridge.intent.go → POST /api/v2/canonical/tasks (acc-go)
  ▼
[acc-go :4101]
  │ taskdecompose
  │ orchestration v3 → 选 worker = ZAG
  │ POST ZAG /api/v1/tasks
  ▼
[ZAgentGateway :9100]
  │ 1. 收到任务
  │ 2. POST RedClaw platform-go /api/v1/tasks
  ▼
[RedClaw platform-go :8090]
  │ orchestrator 入队
  │ 调 agentcontainer invoke
  ▼
[RedClaw agentcontainer :8091]
  │ 创建 workspace
  │ 启动 OpenClaw CLI 子进程
  ▼
[OpenClaw CLI on user's Mac]
  │ 调 llm-gateway-go 调 DeepSeek
  │ 通过 RedClaw connectors 调 zcode / vscode 插件修改文件
  │ git commit + (想 push 时) 触发 permission request
  ▼
[RedClaw platform-go 推 permission request]
  ▼
[ZAgentGateway]
  │ 翻译为 zag event
  │ 落 Memora
  │ 推 SSE 给 acc-go
  │ 推 WS 给 OpenPocket
  ▼
[OpenPocket Mobile Modal: "Allow git push?"]
  │ 用户点击 Allow
  ▼
[POST pocketd /api/fleet/builds/:id/permissions/:pid/reply]
  ▼
[ZAgentGateway POST /api/v1/permissions/:id/reply]
  ▼
[RedClaw platform-go 用 Ed25519 双签确认]
  ▼
[RedClaw 执行 git push]
  ▼
[事件回流 OpenPocket → "PR #145 opened"]
```

---

## 11. 错误处理与降级（安全门禁）

| 故障 | ZAG 行为 |
|---|---|
| RedClaw platform-go 不可达 | 任务进入 blocked/queued；不得直接调 OpenClaw CLI 或绕过审批 |
| RedClaw Gateway/LLM 不可达 | 只切到已批准且符合数据驻留策略的 gateway；否则失败 |
| Memora 不可达 | 有界缓存；durable audit outbox 仍必须可写；高危操作等待 |
| acc-go 不可达 | 只读/入队降级；不得跳过 Chief 后直接执行高危任务 |
| OpenClaw/OpenCode runtime crash | 进入 indeterminate/blocked，先 reconciliation；不得盲目切 runtime 双执行 |
| IDE adapter 不可用 | 只读状态标记 degraded；写命令停止 |

---

## 12. 测试

### 12.1 单元测试

- mock RedClaw client；
- 验证：事件转换 / Ed25519 签名 / 错误归一化。

### 12.2 集成测试

- 起完整 stack（含 RedClaw platform-go mock）；
- 跑通：mobile 发起任务 → ZAG → RedClaw mock → 事件回流。

### 12.3 E2E

- 真实 RedClaw platform-go + 真实 OpenClaw CLI（容器化）；
- 真实 OpenPocket mobile。

---

## 13. 一句话总结

**ZAG 的目标是通过受管 mTLS、delegated token、对象授权和独立审批签名，对接 RedClaw 控制面，并把已验证的 OpenCode/OpenClaw runtime 事件安全地投影给 OpenPocket；当前 RedClaw 不能直接替代 OpenCode，generic connectors 也不能视为已实现 IDE 控制。**
