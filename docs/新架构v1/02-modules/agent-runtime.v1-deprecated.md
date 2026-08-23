# 模块设计：Agent Runtime 与 Pi Agent 集成

---

## 1. 目标

在 PocketFleet Executor（算力舱）上**标准化**地运行多种 Agent Runtime，第一个完整支持 **Pi Coding Agent**（MIT 开源），并预留接入 Claude Code / Codex / Aider 的能力。

```
                         ┌──────────────────────┐
                         │ pocketd-executor      │
                         │ (Go, Pod 端)          │
                         │                      │
                         │  ┌────────────────┐  │
                         │  │ AgentSupervisor │◄┐
                         │  └───────┬────────┘  │
                         │          │ spawn     │
                         │          ▼           │
                         │  ┌────────────────┐  │
                         │  │  Pi Agent      │  │  ←── 默认
                         │  │  (RPC mode)    │  │
                         │  └────────────────┘  │
                         │  ┌────────────────┐  │
                         │  │  Claude Code   │  │  ←── 可选
                         │  │  (headless)    │  │
                         │  └────────────────┘  │
                         │  ┌────────────────┐  │
                         │  │  Codex CLI     │  │  ←── 可选
                         │  └────────────────┘  │
                         └──────────┬───────────┘
                                    │ JSON-RPC over stdio / WS
                                    ▼
                         ┌──────────────────────┐
                         │ Pocket Backend        │
                         │ ExecutorBridge        │
                         └───────────────────────┘
```

## 2. Pi Agent 集成详细

### 2.1 Pi Agent 的 RPC 模式

```bash
# 启动 Pi Agent，监听 stdin/stdout 的 JSON-RPC
pi --mode rpc --provider deepseek --model deepseek-v4-flash
```

请求：

```jsonc
{ "jsonrpc": "2.0", "id": 1, "method": "session.create",
  "params": { "system": "AGENTS.md content", "model": "deepseek-v4-flash" } }
```

响应：

```jsonc
{ "jsonrpc": "2.0", "id": 1, "result": { "sessionId": "pi_abc" } }
```

事件推送（Pi → Executor）：

```jsonc
{ "jsonrpc": "2.0", "method": "event",
  "params": { "sessionId": "pi_abc",
              "kind": "message" | "tool_call" | "tool_result" | "permission",
              "payload": { ... } } }
```

### 2.2 我们的 Adapter（Go）

```go
// backend/internal/agent/adapter_pi.go
package agent

import (
    "bufio"
    "context"
    "encoding/json"
    "os/exec"
    "sync"
)

type PiAgentAdapter struct {
    binPath string  // "pi"
    mu      sync.Mutex
    procs   map[string]*piProc  // sessionId → process
}

func (a *PiAgentAdapter) AdapterType() string { return "pi-agent" }

func (a *PiAgentAdapter) Capabilities(ctx context.Context, ref AgentRef) (*AgentCapabilities, error) {
    return &AgentCapabilities{
        Session:    true,
        Streaming:  true,
        Tools:      []string{"file_read", "file_write", "shell_run", "git_*"},
        Permission: true,
    }, nil
}

// CreateSession 通过 RPC 创建一个 Pi session。
func (a *PiAgentAdapter) CreateSession(ctx context.Context, ref AgentRef, req *CreateSessionRequest) (*AgentSession, error) {
    cmd := exec.CommandContext(ctx, a.binPath, "--mode", "rpc",
        "--provider", req.Model.Provider,
        "--model",    req.Model.Name,
        "--workspace", req.WorkspacePath)
    cmd.Env = append(os.Environ(),
        "DEEPSEEK_API_KEY="+req.ProviderKey,  // 来自 Backend secret store
    )
    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()

    if err := cmd.Start(); err != nil { return nil, err }

    proc := &piProc{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}
    a.mu.Lock()
    // ... 省略 session id 协商与 procs 注册
    a.mu.Unlock()
    return &AgentSession{ID: proc.sessionID}, nil
}

// SendPrompt 发送 prompt 给 Pi session，并把 Pi 的事件流转换成 AgentEvent。
func (a *PiAgentAdapter) SendPrompt(ctx context.Context, ref AgentRef, sessionID string, req *SendPromptRequest) (*SendPromptResult, error) {
    // ... 省略 JSON-RPC 请求与异步事件分发
}

// SubscribeEvents 返回 AgentEvent channel。
func (a *PiAgentAdapter) SubscribeEvents(ctx context.Context, ref AgentRef) (<-chan AgentEvent, func(), error) {
    // ... 省略
}

// 其他方法（ListSessions / GetMessages / InterruptSession / SetSessionMode ...）
//   按相同模式实现。
```

### 2.3 在 Pocket 后端注册

```go
// backend/internal/server/server.go (扩展示例)
import "github.com/halfking/pocket-opencode/backend/internal/agent"

func New(...) *Server {
    // ...
    piAdapter := agent.NewPiAgentAdapter("/usr/local/bin/pi")

    registry := agent.NewRegistry()
    // OpenCode HTTP 适配器（既有）
    ocAdapter := opencode.NewOpenCodeAdapter(...)
    registry.Register(agent.AgentRef{Type: "opencode", Target: "http://localhost:4096"}, ocAdapter, "inst-default")

    // Pi Agent 适配器（新增）
    registry.Register(agent.AgentRef{Type: "pi-agent", Target: "pod://p_001"}, piAdapter)

    // 注入到 fleet.ExecutorBridge
    fleetSvc := fleet.NewService(..., fleet.WithRegistry(registry))

    return ...
}
```

## 3. 工具集统一（Tool Schema）

为了让 Chief / 用户在 UI 上看到统一的"工具描述"，我们在 Backend 维护一份**统一 Schema**：

```go
type ToolSpec struct {
    Name        string
    Description string
    ArgsSchema  json.RawMessage
    Category    string  // "file" | "shell" | "git" | "web" | "agent"
    RequiresPermission bool
    RiskLevel   string  // "low" | "medium" | "high"
}

var CommonTools = []ToolSpec{
    { Name: "file_read", Category: "file", RequiresPermission: false, RiskLevel: "low", ... },
    { Name: "file_edit", Category: "file", RequiresPermission: false, RiskLevel: "low", ... },
    { Name: "shell_run", Category: "shell", RequiresPermission: true, RiskLevel: "medium", ... },
    { Name: "git_push", Category: "git", RequiresPermission: true, RiskLevel: "high", ... },
    { Name: "web_fetch", Category: "web", RequiresPermission: false, RiskLevel: "low", ... },
    { Name: "delegate_to_subagent", Category: "agent", RequiresPermission: true, RiskLevel: "medium", ... },
}
```

执行时：

1. Backend 把 **CommonTools ∩ AgentSpec.Permissions** 转成 LLM `tools` 参数。
2. LLM 输出 tool_call → Backend 在权限闸门校验 → 通过后转给对应 Runtime。
3. 不同 Runtime 的 tool call 格式略有差异 → 由 Adapter 适配（如 Pi Agent 用 `tool` 字段，OpenCode 用 `tool_name`）。

## 4. 消息协议

LLM 调用参数（Pi Agent / DeepSeek 通用）：

```jsonc
{
  "model": "deepseek-v4-flash",
  "stream": true,
  "messages": [
    { "role": "system", "content": "<AGENTS.md content>" },
    { "role": "user", "content": "请为 backend/auth/oauth.py 增加 Google OAuth" }
  ],
  "tools": [...],
  "tool_choice": "auto",
  "reasoning_effort": "medium",
  "metadata": {
    "build_id": "b_123",
    "agent_id": "agent_002",
    "fleet_id": "f_001"
  }
}
```

返回（SSE 流）：

```
data: {"choices":[{"delta":{"role":"assistant","content":"正在读取"}}]}
data: {"choices":[{"delta":{"content":" OAuth 配置..."}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"file_read","arguments":"{\"path\":\"backend/auth/oauth.py\"}"}}]}}]}
data: [DONE]
```

我们包装为统一 `AgentEvent`：

```go
type AgentEvent struct {
    BuildID   string
    AgentID   string
    Kind      string  // "message" | "tool_call" | "tool_result" | "permission" | "done" | "error"
    Delta     string  // 流式内容片段
    ToolName  string
    ToolArgs  json.RawMessage
    Result    json.RawMessage
    Error     string
    Timestamp time.Time
}
```

## 5. 权限闸门（关键安全点）

`shell_run` / `git_push` / `delegate_to_subagent` 三个高危工具必经闸门：

```go
func (e *ExecutorBridge) handlePermission(ctx context.Context, ev AgentEvent) error {
    // 1. 判断是否需要用户审批
    if !requiresApproval(ev.ToolName, ev.ToolArgs) {
        return e.forwardToPod(ctx, ev)
    }

    // 2. 发到 Backend PermissionManager
    permReq := permission.NewRequest(ev.BuildID, ev.ToolName, ev.ToolArgs)
    if err := e.permMgr.Submit(ctx, permReq); err != nil { return err }

    // 3. 等用户决策（WebSocket 推送 + Push notification）
    decision, err := e.permMgr.WaitDecision(ctx, permReq.ID, 5*time.Minute)
    if err != nil { return err }   // timeout → cancel

    // 4. 决定后再 forward
    return e.forwardToPod(ctx, ev.WithDecision(decision))
}
```

Mobile UI 收到 `permission.request` 事件后展示卡片：

```
┌──────────────────────────────────────────────┐
│ 🛡️  Permission Request                        │
│ Agent: backend-engineer                      │
│ Build: b_123 / add-oauth                     │
│                                              │
│ Tool: shell_run                              │
│ Command: git push origin feature/oauth       │
│                                              │
│        [ Deny ]  [ Allow Once ]  [ Always ]  │
└──────────────────────────────────────────────┘
```

## 6. 上下文压缩（Context Compaction）

当 LLM context 超阈值（默认 80% of max）：

1. 让 Pi Agent 走 `session.compact`（Pi 自带）。
2. 后端落"压缩前快照"到 kxmemory。
3. 继续后续 turn。

用户可在 Mobile UI 看到：

```
Context: 458K / 1M (45.8%) · 3 compactions so far
```

## 7. 错误恢复

| 故障 | 行为 |
|---|---|
| LLM 5xx | 重试 3 次 → 切备选模型 |
| LLM 4xx（tool schema 错） | 修正 schema 重试 |
| Pi Agent 子进程挂掉 | ExecutorBridge 重启进程，从最近一个 event id 重放 |
| Pod 断网 | 任务标记 `pod_offline`；重连后自动续跑 |
| 用户撤掉权限 | 当前 tool 失败 → Agent 收到 `permission_denied` → 自己决定下一步 |

## 8. 测试

- 单测：mock Pi Agent 进程；验证 SendPrompt / SubscribeEvents 正确性。
- 集成：启动真实 Pi + DeepSeek，跑通一个 "修复 typo" 任务。
- 端到端：手机发起 → Chief plan → Pi Agent 真跑 → 完成 → 通知。

---

> **⚠️ DEPRECATED (2026-08-23)**：本文档是 v1 方案（自建 Pi Agent Adapter），已被 v2 方案取代。
>
> **v2 方案**：Pi Agent 作为可选 Harness dialect 加在 acc-go 的 `internal/agent/harness/pi.go`（约 200 行 Go），复用 acc-go 现有的 Agent Loop 框架。详见：
> - [compute-pod-as-acc-worker.md §4](compute-pod-as-acc-worker.md)
> - [architecture-decision-records.md §ADR-004](../architecture-decision-records.md)
>
> 本文件保留作为"v1 设计思路"的参考，但不构成当前方案。
