# 模块：Compute Pod = ZAgentGateway（v3 视角）

> **目标架构（v3）**：将算力舱设计为 **ZAgentGateway → RedClaw platform-go → OpenClaw/OpenCode runtime + 本地 IDE adapter**。当前 ZAG、IDE adapter、ACC worker 注册和 RedClaw connector 合同尚未实现/验证；默认保留 OpenCode coding runtime，不把 OpenClaw 当作 OpenCode drop-in replacement。

---

## 1. 算力舱的 v3 定义

### 1.1 三层定义

```
┌──────────────────────────────────────────────────────────────┐
│  Compute Pod（算力舱）= 用户 PC + 以下三层                       │
│                                                              │
│  Layer 1: ZAgentGateway (NEW, :9100)                          │
│    - 接收 acc-go 任务                                         │
│    - 转发到 RedClaw                                            │
│    - 暴露 REST + MCP + WS API                                 │
│    - 聚合 RedClaw 状态                                         │
│                                                              │
│  Layer 2: RedClaw platform-go (用户 PC 上, :8080-8093)         │
│    - orchestrator :8090 (任务队列 + 控制信号)                  │
│    - agentcontainer :8091 (OpenClaw runtime + skills)        │
│    - gateway (Bedrock / LLM 代理)                              │
│    - connectors (IDE / Browser / etc.)                        │
│    - authagent :8092 (SSO + 审批)                            │
│    - admin :8093 (控制台后端)                                  │
│                                                              │
│  Layer 3: OpenClaw CLI (用户机器上) + 本地 IDE 插件            │
│    - OpenClaw CLI (npm 包，PC 桌面 AI 助手)                   │
│    - ZCode (项目自有 IDE)                                       │
│    - VS Code + code-server                                    │
│    - Cursor (本地 binary)                                     │
│    - OpenCode serve (`:4096`)                                 │
└──────────────────────────────────────────────────────────────┘
```

### 1.2 与 v2 的根本差异

| 维度 | v2 | v3 |
|---|---|---|
| 算力舱入口 | acc-go Worker (自建 Harness) | **ZAgentGateway** |
| 执行器 | Claude / OpenCode CLI dialect (acc-go agent/harness) | **RedClaw OpenClaw CLI** |
| IDE 控制 | ❌ | **通过 RedClaw Connectors 控制 4 种 IDE** |
| 鉴权 | acc-go L1 tenant invariant | **mTLS + delegated token + 独立审批签名（目标）** |
| 控制信号 | acc-go orchestration v3 | **RedClaw Plan A/E + Ed25519 双签** |
| PC 设备管理 | acc-go devices | **ZAG pod registry（从 RedClaw 拉取）** |

---

## 2. 任务流转的完整路径

### 2.1 入口：acc-go 派任务给 ZAG

```
[acc-go orchestration v3]
  │ 1. HeuristicTriage → 任务类型
  │ 2. Agent router → 选 worker = ZAG
  │ 3. POST ZAG /api/v1/tasks
  │    body: { taskId, fleetId, sessionId, agentId, goal, model, ... }
  ▼
[ZAG]
  │ 1. 鉴权 + 注入 tenant context
  │ 2. 落本地 DB + Memora
  │ 3. POST RedClaw platform-go /api/v1/tasks
  ▼
[RedClaw platform-go :8090 orchestrator]
  │ 1. 任务入队（带 ZAG 的 fleet_id / tenant_id）
  │ 2. 派给 agentcontainer :8091
  ▼
[RedClaw agentcontainer :8091]
  │ 1. 装配 workspace (3-tier SOUL merge)
  │ 2. 加载 skill registry
  │ 3. 注入 Plan A permission
  │ 4. safety validation
  │ 5. 启动 OpenClaw CLI 子进程
  ▼
[OpenClaw CLI on user's Mac]
  │ 1. 接收 prompt
  │ 2. LLM call → 已验证的 gateway → Provider（fallback 受租户驻留策略约束）
  │ 3. tool_call (file_read / shell_run / git_push / web_fetch)
  │ 4. tool_call 通过已验证的 IDE adapter/connector 调本地 IDE 插件（planned）
  │    - 调 zcode apply_diff
  │    - 调 cursor apply_diff
  │    - 调 vscode apply_edit
  │    - 调 opencode session.send
  │ 5. permission_request (shell_run / git_push) → taskgate
  ▼
[RedClaw SSE → ZAG → acc-go SSE → pocketd WS Hub → OpenPocket Mobile]
```

### 2.2 事件回流链

```
[OpenClaw CLI]
  │ stdout / stderr
  ▼
[RedClaw agentcontainer]
  │ publish_event("agent.message")
  ▼
[RedClaw orchestrator]
  │ push SSE → task subscribers
  ▼
[ZAG]
  │ 订阅 SSE → 翻译为 zag event
  │ 落 Memora (audit)
  │ 推 acc-go SSE (NDJSON)
  │ 推 OpenPocket WS Hub
  ▼
[OpenPocket Mobile Live View]
```

---

## 3. ZAG 作为 acc-go Worker 的注册

### 3.1 启动时注册

```go
// zagent-gateway/cmd/api/main.go
func main() {
    cfg := loadConfig()
    zag := buildZAgent(cfg)
    
    // 注册为 acc-go worker
    if cfg.Acc.URL != "" {
        if err := zag.RegisterAsACCWorker(context.Background(), acc.RegisterWorkerRequest{
            WorkerID:     cfg.ZAG.WorkerID,  // "zag_worker_001"
            Name:         "ZAgentGateway",
            Kind:         "zagent-gateway",
            Endpoint:     cfg.ZAG.PublicURL + "/api/v1/tasks",
            Capabilities: []string{"openclaw", "zcode", "vscode", "cursor", "opencode"},
            MaxConcurrent: 10,
        }); err != nil {
            log.Error("register as acc worker failed", "err", err)
        }
    }
    
    // 启动 HTTP API
    server.Start(":9100", zag.Handler())
}
```

### 3.2 接收任务

```go
// zagent-gateway/internal/api/handlers.go
func (h *Handler) SubmitTask(c *gin.Context) {
    var req SubmitTaskRequest
    if err := c.ShouldBindJSON(&req); err != nil { ... }
    
    // 1. 鉴权
    claims := auth.FromContext(c)
    
    // 2. 落本地 DB
    task := h.taskService.Create(c, claims, req)
    
    // 3. 转发到 RedClaw platform-go
    rcTask, err := h.redclaw.SubmitTask(c, redclaw.SubmitRequest{
        TenantID:  claims.WorkspaceID,
        UserID:    claims.UserID,
        AgentID:   req.AgentID,
        SessionID: req.SessionID,
        Goal:      req.Goal,
    })
    if err != nil {
        return jsonError(c, err)
    }
    
    // 4. 启动事件桥接
    go h.bridgeEvents(c, rcTask.TaskID, task.ID)
    
    // 5. 推事件给 acc-go
    h.acc.PublishTaskEvent(c, task.ACCTaskID, acc.TaskEvent{
        Type:    "task.update",
        Status:  "queued",
    })
    
    c.JSON(201, task)
}
```

### 3.3 心跳 + 能力更新

```go
// ZAG 每 30s 上报一次
func (z *ZAgent) heartbeat(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Second)
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C:
            z.acc.Heartbeat(ctx, acc.HeartbeatRequest{
                WorkerID: z.cfg.ZAG.WorkerID,
                InFlight: z.taskService.InFlightCount(),
                Capacity: z.cfg.ZAG.MaxConcurrent,
                Agents:   z.agentRepo.ListActive(),
                IDEs:     z.ideService.ListConnected(),
            })
        }
    }
}
```

---

## 4. ZAG 内部：Pod / Agent / IDE 注册中心

### 4.1 Pod 注册

```go
// ZAG 启动时聚合 RedClaw 设备
func (z *ZAgent) SyncPods(ctx context.Context) error {
    // 1. 拉 RedClaw devices
    rcDevices, _ := z.redclaw.ListDevices(ctx, redclaw.ListDevicesRequest{TenantID: z.fleetID})
    
    // 2. 拉每个 device 的 capabilities（CPU / 内存 / GPU / 工具 / IDE）
    var pods []Pod
    for _, dev := range rcDevices {
        pod := Pod{
            ID:      dev.DeviceID,
            FleetID: dev.TenantID,
            Name:    dev.Name,
            Status:  dev.Status,
            CPUs:    dev.CPUs,
            MemoryGB: dev.MemoryGB,
            GPU:     dev.GPU,
        }
        
        // 探测 IDE
        for _, ide := range []string{"zcode", "vscode", "cursor", "opencode"} {
            status, err := z.ideService.GetStatus(ctx, ide)
            if err == nil && status.Running {
                pod.IDEs = append(pod.IDEs, ide)
            }
        }
        
        pods = append(pods, pod)
    }
    
    // 3. 落本地 DB
    return z.podRepo.UpsertBatch(ctx, pods)
}
```

### 4.2 Agent 注册

```go
// ZAG 拉每个 device 上的 Agent（OpenClaw / zcode / vscode / cursor / opencode）
func (z *ZAgent) SyncAgents(ctx context.Context) error {
    pods, _ := z.podRepo.ListAll(ctx)
    
    var agents []Agent
    for _, pod := range pods {
        // OpenClaw
        if openclaw, err := z.redclaw.AgentContainer.GetAgent(ctx, pod.ID, "openclaw"); err == nil {
            agents = append(agents, z.translateAgent(openclaw, pod))
        }
        // zcode / vscode / cursor / opencode 作为 IDE plugin agents
        for _, ide := range pod.IDEs {
            if a, err := z.redclaw.AgentContainer.GetAgent(ctx, pod.ID, ide); err == nil {
                agents = append(agents, z.translateAgent(a, pod))
            }
        }
    }
    
    return z.agentRepo.UpsertBatch(ctx, agents)
}
```

---

## 5. Pod / Agent 控制信号（双签）

### 5.1 控制信号类型

```go
const (
    KindPause     Kind = "pause"        // 单签
    KindResume    Kind = "resume"       // 单签
    KindRestart   Kind = "restart"      // 单签
    KindRedirect  Kind = "redirect"     // 单签
    KindRetry     Kind = "retry"        // 单签
    KindInject    Kind = "inject"       // 单签
    KindRollback  Kind = "rollback"     // 双签
    KindUpgrade   Kind = "upgrade"      // 双签
    KindTerminate Kind = "terminate"    // 双签
)
```

### 5.2 ZAG 发起控制信号

```go
func (z *ZAgent) ControlPod(ctx context.Context, podID string, req ControlRequest) error {
    // 1. 创建 control command
    cmd := redclaw.NewCommand(req.TaskID, req.SessionID, z.zagID, redclaw.Kind(req.Kind), req.Args)
    
    // 2. ZAG Ed25519 私钥签第一个
    if err := cmd.Sign(z.redclaw.Dispatcher, z.zagID, "zagent-gateway", z.ed25519Priv); err != nil {
        return err
    }
    
    // 3. 双签需求
    if redclaw.RequiresDoubleSignature(cmd.Kind) {
        // 等 ZAG admin 审批
        if err := z.waitForAdminApproval(ctx, cmd, 5*time.Minute); err != nil {
            return err
        }
    }
    
    // 4. 执行
    return z.redclaw.Orchestrator.ExecuteControl(ctx, cmd)
}

func (z *ZAgent) waitForAdminApproval(ctx context.Context, cmd *redclaw.Command, timeout time.Duration) error {
    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    
    z.pendingCmdsMu.Lock()
    z.pendingCmds[cmd.CommandID] = cmd
    z.pendingCmdsMu.Unlock()
    
    select {
    case <-cmd.Signed():
        return nil
    case <-ctx.Done():
        return ErrAdminApprovalTimeout
    }
}
```

---

## 6. Permission 流程

### 6.1 OpenClaw 想 git push → 用户在 OpenPocket 审批

```
[OpenClaw CLI]
  │ tool_call(git_push, ...)
  ▼
[RedClaw agentcontainer]
  │ taskgate.request(git_push, ...)
  ▼
[RedClaw orchestrator]
  │ SSE: gate.requested
  ▼
[ZAG]
  │ 1. 翻译事件 → permission.request
  │ 2. 落 Memora (audit)
  │ 3. 推 SSE 给 acc-go (NDJSON)
  │ 4. 推 WS 给 OpenPocket
  ▼
[OpenPocket Modal: "Allow git push?"]
  │ 用户点击 Allow
  ▼
[POST pocketd /api/fleet/builds/:id/permissions/:pid/reply]
  ▼
[pocketd fleetbridge.permission.go]
  │ POST ZAG /api/v1/permissions/:id/reply
  ▼
[ZAG]
  │ 1. 落 Memora (audit)
  │ 2. POST RedClaw platform-go /api/v1/control/:command_id/execute
  ▼
[RedClaw platform-go]
  │ 验证 + 执行 git push
  ▼
[OpenClaw CLI]  ← git push 完成
  ▼
[事件回流 OpenPocket → 显示 "PR #145 opened"]
```

---

## 7. 与 v2 算力舱方案的对比

| 维度 | v2 算力舱（acc-go Worker） | v3 算力舱（ZAG → RedClaw）|
|---|---|---|
| **入口** | acc-go 任务直接派给 worker | acc-go 派给 ZAG，ZAG 转发 RedClaw |
| **执行器** | acc-go agent/harness (claude/opencode) | RedClaw OpenClaw CLI + 本地 IDE 插件 |
| **协议** | acc-go A2A | mTLS + delegated token + 独立审批签名（目标） |
| **IDE 控制** | ❌ | planned：需实际 adapter 和 connector contract |
| **OpenClaw 能力** | ❌ | source-inspected/contract-tested，非当前 ZAG 已接通 |
| **PC 设备状态聚合** | ❌ | planned：需真实 RedClaw/ACC 合同 |
| **会话管理** | acc-go session | RedClaw session + OpenClaw session |
| **权限审批** | acc-go taskgate | RedClaw Plan A/E + Ed25519 双签 |
| **LLM 调用** | acc-go → llm-gateway-go | RedClaw Gateway (主) + llm-gateway-go (兜底) |
| **PC 桌面 IM** | ❌ | planned：需事件合同 |
| **企业 SSO** | ❌ | planned：需受众和委托 token 合同 |

---

## 8. 实施步骤

### 8.1 M0（最小化）

- ZAG 接收任务 → RedClaw mock 转发 → 事件回流 → OpenPocket Live View

### 8.2 M1

- ZAG Pod / Agent registry 完整实现
- ZAG ↔ RedClaw control signals 单签
- IDE 列表（zcode 适配）

### 8.3 M3

- IDE 完整适配（VS Code / Cursor / OpenCode）
- Ed25519 双签
- Agent Memory 注入

---

## 9. 一句话总结

**v3 的目标算力舱 = ZAG → RedClaw 控制面 → 已验证的 OpenCode/OpenClaw runtime 与 IDE adapter。实现前必须完成 OpenCode contract、身份链、命令沙箱、事件补偿和审计门禁。**
