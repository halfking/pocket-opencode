# 模块设计：算力舱（Compute Pod）与 Executor Bridge

---

## 1. 概念

**Compute Pod = 一台能跑 Agent 的机器**，无论是：

- 用户家里的 Mac Studio；
- 公司的 Linux 工作站；
- 公有云的 GPU 实例（RunPod / 阿里云 PAI）；
- 边缘算力盒子（Jetson / RK3588）。

PocketFleet **不拥有 Pod**，只通过 **`pocketd-executor`** 二进制把 Pod 接入 Fleet。

```
┌─────────────── 用户的 Pod ───────────────┐
│                                          │
│   pocketd-executor  ◀── Go 二进制        │
│      │                                  │
│      ├─ mTLS WSS ──► Pocket Backend     │
│      │                                  │
│      └─ spawns:                         │
│           • pi-agent                    │
│           • claude-code (optional)      │
│           • codex (optional)            │
│           • aider (optional)            │
│                                          │
│   工作区:                                │
│      ~/pocket/workspaces/<ws_id>/       │
│           ├── .git/  (worktree)         │
│           └── work/                     │
│                                          │
│   Sandbox:                               │
│      • docker / firecracker / cgroup    │
│      • 隔离网络 (egress 过滤)            │
│      • per-build user                   │
│                                          │
└──────────────────────────────────────────┘
```

## 2. pocketd-executor CLI

### 2.1 安装

```bash
# macOS / Linux
curl -fsSL https://pocket.example.com/install.sh | sh
# 或
brew install pocketd-executor
# 或
nix-env -iA nixpkgs.pocketd-executor
```

### 2.2 命令

```bash
# 1. 注册（一次性，需要交互或 API key）
pocketd-executor enroll \
  --server https://pocket.example.com \
  --api-key pk_live_xxx  # 或 --interactive 打开浏览器
# 成功后写入 ~/.pocket/executor.json (mode 600)

# 2. 启动后台守护进程
pocketd-executor start
# 可选 flags：
#   --foreground, -f          # 前台运行（便于 systemd）
#   --name "我的 MBP 16"      # 自定义显示名
#   --workspaces-dir ~/pocket # 工作区根目录
#   --auto-update             # 启用自动升级

# 3. 其他
pocketd-executor status
pocketd-executor logs --follow
pocketd-executor stop
pocketd-executor restart
pocketd-executor update
pocketd-executor logout
```

### 2.3 配置文件

`~/.pocket/executor.json`：

```jsonc
{
  "server": "https://pocket.example.com",
  "podId": "p_abc123",
  "teamId": "f_001",
  "apiKeyRef": "vault://pocket/executor-api-key",  // macOS Keychain / Linux Secret Service
  "name": "mbp16-居家",
  "workspacesDir": "/Users/me/pocket/workspaces",
  "capabilities": {
    "auto": true    // 自动检测：CPU/内存/GPU/工具
  },
  "sandbox": {
    "mode": "user" | "docker" | "firecracker",
    "networkEgress": "default" | "allowlist"
  },
  "logLevel": "info"
}
```

### 2.4 自动能力探测

启动时上报：

```jsonc
{
  "cpu": { "model": "Apple M3 Max", "cores": 16, "threads": 16 },
  "memoryGB": 64,
  "gpu": [{ "model": "Apple M3 Max GPU", "vramGB": 0, "cuda": false }],
  "tools": {
    "git": "2.43.0",
    "node": "20.10.0",
    "python": "3.12.1",
    "docker": "24.0.7",
    "go": "1.22.3"
  },
  "agentRuntimes": {
    "pi-agent": "/usr/local/bin/pi",
    "claude-code": null,   // 未装
    "codex": null
  },
  "concurrentBuilds": 3   // 默认按内存计算
}
```

## 3. Executor Bridge（Backend ↔ Pod）

### 3.1 职责

在 Backend 一侧（`backend/internal/fleet/executor_bridge.go`），负责：

- 维护 **Pod 心跳表**（map[PodID]LastSeen）。
- 接收来自 Fleet service 的 RPC 调用，转成 WSS 消息发给目标 Pod。
- 接收来自 Pod 的推送事件，转发到 WebSocket Hub + kxmemory audit。
- 处理 Pod 断连 / 重连 / 重放。

### 3.2 接口

```go
package fleet

type ExecutorBridge interface {
    // 连接管理
    Connect(ctx context.Context, pod Pod) error
    Disconnect(ctx context.Context, podID string, reason string) error
    Heartbeat(ctx context.Context, podID string) error

    // Workspace
    CreateWorkspace(ctx context.Context, podID string, req WorkspaceRequest) (*Workspace, error)
    PullWorkspace(ctx context.Context, podID, wsID string) error
    CleanupWorkspace(ctx context.Context, podID, wsID string) error

    // Agent
    SpawnAgent(ctx context.Context, podID, wsID string, spec AgentSpec) (AgentHandle, error)
    KillAgent(ctx context.Context, podID string, h AgentHandle) error

    // File
    ReadFile(ctx context.Context, podID, wsID, path string) ([]byte, error)
    WriteFile(ctx context.Context, podID, wsID, path string, data []byte) error
    Diff(ctx context.Context, podID, wsID, base, head string) (*Diff, error)

    // Command / Shell
    RunCommand(ctx context.Context, podID, wsID string, cmd CommandSpec, callback OutputCallback) error
    OpenShell(ctx context.Context, podID, wsID string, cols, rows int) (ShellID, error)
    ShellInput(ctx context.Context, podID string, sid ShellID, data []byte) error
    ShellSignal(ctx context.Context, podID string, sid ShellID, sig string) error

    // Health / Diag
    HealthCheck(ctx context.Context, podID string) error
    Diagnostics(ctx context.Context, podID string) (*PodDiagnostics, error)
}
```

### 3.3 实现骨架

```go
type executorBridge struct {
    mu        sync.RWMutex
    conns     map[string]*podConn     // podID → 长连
    hub       *websocket.Hub
    permMgr   *permission.Manager
    audit     kxmemory.AuditClient
    logger    *zap.Logger
}

type podConn struct {
    podID  string
    ws     *websocket.Conn
    in     chan<- []byte   // send queue
    out    <-chan []byte   // recv queue
    lastSeen time.Time
}

func (b *executorBridge) Connect(ctx context.Context, pod Pod) error {
    dialer := websocket.Dialer{
        TLSClientConfig: &tls.Config{
            Certificates: []tls.Certificate{pod.ClientCert},
            RootCAs:      pocketFleetCAPool,
        },
    }
    ws, _, err := dialer.DialContext(ctx, pod.WSSEndpoint(), nil)
    if err != nil { return err }

    pc := &podConn{podID: pod.ID, ws: ws, lastSeen: time.Now()}
    b.mu.Lock(); b.conns[pod.ID] = pc; b.mu.Unlock()

    // 发 hello
    if err := ws.WriteJSON(map[string]any{
        "jsonrpc": "2.0", "method": "hello",
        "params": pod.Hello(),
    }); err != nil { return err }

    // 后台读 loop
    go b.readLoop(pc)
    return nil
}

func (b *executorBridge) readLoop(pc *podConn) {
    for {
        _, data, err := pc.ws.ReadMessage()
        if err != nil {
            b.handleDisconnect(pc, err)
            return
        }
        pc.lastSeen = time.Now()
        b.dispatch(pc.podID, data)
    }
}

func (b *executorBridge) dispatch(podID string, data []byte) {
    var msg struct {
        Method string          `json:"method"`
        Params json.RawMessage `json:"params"`
    }
    if err := json.Unmarshal(data, &msg); err != nil { return }

    switch msg.Method {
    case "agent.event":
        var ev AgentEvent
        json.Unmarshal(msg.Params, &ev)
        b.hub.BroadcastToFleet(ev.FleetID, "build.update", ev)
        b.audit.Write("fleet.event", ev)
    case "shell.output":
        b.hub.BroadcastToFleet(podID, "shell.output", msg.Params)
    case "executor.ping":
        // 心跳，更新 lastSeen
    }
}
```

## 4. Pod 调度（Assigner）

```go
package fleet

type Assigner interface {
    Assign(ctx context.Context, build Build) (PodID, error)
    Release(ctx context.Context, podID string) error
}

// 默认：最闲 + 不被 pin 阻挡 + capabilities 满足
type leastLoadedAssigner struct {
    pods  PodRegistry
    usage map[string]int  // podID → in-flight count
}

func (a *leastLoadedAssigner) Assign(ctx context.Context, build Build) (PodID, error) {
    // 1. 如果 build.PinnedPod 强制指定，直接返回（验证 online + capabilities）
    if build.PinnedPod != "" {
        pod, ok := a.pods.Get(build.PinnedPod)
        if !ok { return "", ErrPodNotFound }
        if !pod.Online() { return "", ErrPodOffline }
        if !pod.Supports(build.RequiredCapabilities) {
            return "", ErrPodCapMismatch
        }
        return pod.ID, nil
    }

    // 2. 否则选 in-flight 最少的 online pod
    candidates := a.pods.Online()
    var best *Pod
    for i := range candidates {
        p := &candidates[i]
        if !p.Supports(build.RequiredCapabilities) { continue }
        if best == nil || a.usage[p.ID] < a.usage[best.ID] {
            best = p
        }
    }
    if best == nil { return "", ErrNoPodAvailable }
    return best.ID, nil
}
```

调度策略可替换：round-robin / region-affinity / cost-aware / model-aware。

## 5. 工作流：完整一次 Build

```
[ Mobile ]
  │  1. 用户在 Live UI 点击 "Approve" on permission request
  │
  ▼
[ Backend ]
  │  2. POST /api/fleet/builds/b_123/permissions/pr_456/reply { decision: "allow" }
  │  3. permission.Manager 标记 decision
  │  4. ExecutorBridge.forwardToPod(b_123, { tool_call: shell_run(...), decision: allow })
  │
  ▼
[ Pod - pocketd-executor ]
  │  5. 收到 JSON-RPC forward
  │  6. 在 sandbox（docker / user）中执行 git push
  │  7. 通过 stdout / stderr 流式回推
  │
  ▼
[ Backend ]
  │  8. 收到 "command.output" 事件 → broadcast 到 mobile
  │  9. 收到 "agent.event" (tool_result) → broadcast
  │  10. 收到 "agent.event" (message delta) → broadcast
  │  11. 收到 "build.completed" → 更新 PocketTask 状态
  │
  ▼
[ Mobile ]
  │  12. Live UI 收到 push: "Build b_123 完成, PR #145 opened"
  │  13. 用户点击 → 跳转 PR Diff viewer
```

## 6. 算力舱生命周期

```
                ┌──────────────┐
                │  not_enrolled│
                └──────┬───────┘
                       │ pocketd-executor enroll
                       ▼
                ┌──────────────┐
                │   enrolled   │
                └──────┬───────┘
                       │ pocketd-executor start
                       ▼
   ┌──────────┐   ┌──────────┐
   │  offline │◄──┤  online  ├──► asleep (Platform Pod 模式)
   └──────────┘   └────┬─────┘
          ▲            │ 主动 stop / 崩溃
          │            ▼
          │     ┌──────────────┐
          └─────┤ disconnecting│
                └──────┬───────┘
                       │ 重连成功
                       ▼
                ┌──────────────┐
                │   online     │
                └──────────────┘
```

心跳机制：

- 每 30s 上报一次，含 CPU / 内存 / 磁盘 / in-flight 数。
- Backend 90s 未收心跳 → 标 offline；in-flight build 进入"等待 Pod 回来"状态。
- 重连成功后，Backend 重放离线期间的事件（按 `Last-Event-ID`）。

## 7. 平台托管 Pod（Platform Machine）

参考 todos.dev 的 "Platform Machine"：

- 后端在用户没自有 Pod 时，按需启动云端 GPU Pod（RunPod / Modal / 阿里云 PAI）。
- 走相同 ExecutorBridge 接口，但 Pod 类型标 `platform`。
- "asleep while idle, awake as soon as there is work"。
- 计费：按 GPU 小时计；纳入 `internal/quota`。

**第一阶段**只支持自有 Pod；Platform Pod 作为 v1.1。

## 8. 安全加固

- Pod 上的 `~/.pocket/executor.json` 模式 600；
- API key 走 OS keystore（macOS Keychain / Linux Secret Service / Windows DPAPI）；
- pocketd-executor **不直接**读 LLM provider key；key 走 Backend 注入；
- 自升级：每次 start 时检查 server version → 自动下载新版并 reload；
- 远程命令白名单：仅 PocketFleet 后端能 spawn 进程，不能由 Pod 主动开新会话。

## 9. 与现有 OpenCode Adapter 的关系

既有 `internal/adapter/opencode_http.go` 是 **OpenCode Server HTTP 客户端** —— 假设 OpenCode 实例在同一台机器或同网络。

PocketFleet 的 Executor Bridge 是 **更通用的远程协议** —— 不限定 agent runtime。

**第一阶段共存**：

- 既有 OpenCode HTTP adapter 继续工作；
- Pi Agent Adapter 新增；
- 用户可以在 UI 上选"用哪个"。

**第二阶段收敛**：

- 把 OpenCode HTTP adapter 包成 Pod 上的 Runtime 之一；
- 所有 Agent 统一走 Executor Bridge。

## 10. 测试

- 单测：mock WSS 连接；测试 readLoop / dispatch。
- 集成：起 2 个 pocketd-executor 实例（docker compose），跑通：
  - 同时派 2 个 build；
  - 杀掉其中一个 executor，验证另一台接管或 task 进入 wait。
- 端到端：手机 → Chief → Pod → Pi Agent → PR → 完成。

---

> **⚠️ DEPRECATED (2026-08-23)**：本文档是 v1 方案（自建 ComputePod + Executor Bridge），已被 v2 方案取代。
>
> **v2 方案**：算力舱 = 一台跑 acc-go worker 模式的机器，复用 acc-go 现有的 agentspawner + harness + A2A + orchestration v3 + taskgate。详见：
> - [compute-pod-as-acc-worker.md](compute-pod-as-acc-worker.md)
> - [architecture-decision-records.md §ADR-004](../architecture-decision-records.md)
>
> 本文件保留作为"v1 设计思路"的参考，但不构成当前方案。
