# ACP（Agent Client Protocol）集成指南

> 状态：✅ Phase 1 框架交付（接口 + 3 transport + OpenCode adapter + mock）。  
> 真实 ACP agent（Codex / Claude Code / Gemini CLI）接入留待后续 PR。

## 1. 概述

pocketd 通过 `internal/agent` 包实现 ACP 通用化。任何实现 ACP 的编程 agent（不只是 OpenCode）都能接入：

```
+-------------------+      +-----------------+      +-----------------+
|   Handler 层      | ---> |  agent.Registry | ---> |  AgentAdapter   |
| (s.agents.Get())  |      |  按 AgentRef    |      |  (opencode/mock)|
+-------------------+      |  分发           |      +-----------------+
                          +-----------------+              |
                                                       +-- Stdio (ACP)
                                                       +-- HTTP   (ACP)
                                                       +-- WS     (ACP)
                                                       +-- OpenCode (HTTP)
```

## 2. 核心抽象

### 2.1 AgentAdapter 接口

```go
type AgentAdapter interface {
    AdapterType() string
    Capabilities(ctx, ref AgentRef) (*AgentCapabilities, error)
    HealthCheck(ctx, ref AgentRef) error
    ListSessions(ctx, ref AgentRef, opts ListOptions) ([]AgentSession, error)
    CreateSession(ctx, ref AgentRef, req *CreateSessionRequest) (*AgentSession, error)
    LoadSession(ctx, ref, sessionID string) (*AgentSession, error)
    DeleteSession(ctx, ref, sessionID string) error
    GetMessages(ctx, ref, sessionID string, opts ListOptions) ([]AgentMessage, error)
    SendPrompt(ctx, ref, sessionID string, req *SendPromptRequest) (*SendPromptResult, error)
    InterruptSession(ctx, ref, sessionID string) error
    SetSessionMode(ctx, ref, sessionID, modeID string) error
    SubscribeEvents(ctx, ref) (<-chan AgentEvent, func(), error)
}

// Optional capabilities
type PermissionCapable interface { ... }
type QuestionCapable interface { ... }
```

### 2.2 AgentRef

标识一个具体 agent 实例：

```go
type AgentRef struct {
    Type   string  // "opencode" | "acp-stdio" | "acp-http" | "acp-ws" | "mock"
    Target string  // URL / file path / instance id
}
```

例：
- `{Type: "opencode", Target: "http://localhost:4096"}`
- `{Type: "acp-stdio", Target: "/usr/local/bin/codex"}`
- `{Type: "acp-http", Target: "https://api.example.com"}`
- `{Type: "acp-ws", Target: "ws://localhost:8080/acp"}`

### 2.3 Transport 抽象

JSON-RPC 2.0 over 不同底层协议：

```go
type Transport interface {
    Start(ctx) error
    Close() error
    Call(ctx, method string, params any, out any) error
    Notify(ctx, method string, params any) error
    Recv(ctx) ([]byte, error)
}
```

实现：
- `StdioTransport` — 子进程 stdin/stdout JSON-RPC 帧
- `HTTPTransport` — Streamable HTTP（POST /acp + SSE）
- `WSTransport` — WebSocket /acp

## 3. 当前阶段交付

| Phase | 状态 | 内容 |
|-------|------|------|
| W1 接口/类型/错误 | ✅ | types.go, interface.go, errors.go, jsonrpc.go, codec.go |
| W2 stdio transport | ✅ | transport_stdio.go + cmd/agent_echo 测试 fake |
| W3 HTTP/WS transport | ✅ | transport_http.go, transport_ws.go |
| W4 OpenCode adapter | ✅ | adapter_opencode.go + adapter_mock.go + registry.go |
| W5 Server 增量 | ✅ | 新增 `s.agents` 字段 + `/api/diagnostics/agents` 端点 |
| W6 文档/验证 | ✅ | 本文档 |

**当前 handler 仍走 `s.opencode` 老路径**。新代码（diagnostics、健康检查）走 `s.agents`。完整 handler 迁移（18+ 处调用点）留待后续 PR，避免一次引入过多破坏性变更。

## 4. 测试

### 4.1 单元测试

`internal/agent/` 35+ 测试：

| 包 | 测试数 | 覆盖 |
|----|--------|------|
| `jsonrpc_test.go` | 13 | ID allocator, marshal/parse, PendingCalls |
| `errors_test.go` | 4 | ClassifyNetworkError, WithStatus, Unwrap |
| `transport_stdio_test.go` | 8 | StartClose, CallEcho, Notify, Timeout, SpawnProcess |
| `adapter_mock_test.go` | 12 | CreateSession, SendPrompt, Delete, ForceError, SubscribeEvents |
| `registry_test.go` | 5 | Register, GetByInstanceID, Unregister, HealthCheckAll |

### 4.2 测试 fake agent

`cmd/agent_echo/main.go` — Go 写的 mock agent，支持：
- `-echo-only` — 启动不响应（用于 StartClose 测试）
- `-echo` — 回显 params 当 result（用于 Call 测试）
- `-hang` — 永不响应（用于 Timeout 测试）

构建：`go build -o ./agent_echo ./cmd/agent_echo/`

### 4.3 端到端（E2E）

```bash
# 1. 启动 fake agent
./agent_echo -echo &
# 2. 启动 pocketd，配置 acp-stdio agent
POCKET_ACP_STDIO_AGENTS='[{"path":"./agent_echo","args":["-echo"],"name":"echo-agent"}]' \
  ./pocketd
# 3. 通过 /api/diagnostics/agents 查看
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8088/api/diagnostics/agents
```

## 5. API 端点

### 5.1 `GET /api/diagnostics/agents`

列出所有注册的 agent + 健康状态。

**响应示例**：
```json
{
  "registered": [
    {"ref": {"type":"opencode","target":"http://localhost:4096"}, "type": "opencode"}
  ],
  "health": {
    "opencode:http://localhost:4096": {"up": true}
  }
}
```

当 `s.agents == nil`（未注入）时返回空 + note 字段。

## 6. 接入真实 ACP agent（后续 PR）

### 6.1 Codex / Claude Code（acp-stdio）

```go
import "github.com/halfking/pocket-opencode/backend/internal/agent"

// 1. 创建 stdio transport
tr := agent.NewStdioTransport(agent.TransportConfig{
    AgentPath: "/usr/local/bin/codex",
    AgentArgs: []string{"--acp"},
})

// 2. 创建 adapter（用通用 JSON-RPC 客户端模式）
adapter := agent.NewACPAdapter(tr, "codex")  // 待实现

// 3. 注册到 registry
reg.Register(agent.AgentRef{Type: "acp-stdio", Target: "/usr/local/bin/codex"}, adapter)
```

### 6.2 远程 SaaS agent（acp-http）

```go
tr := agent.NewHTTPTransport(agent.TransportConfig{
    BaseURL:   "https://api.example.com",
    AuthToken: "Bearer xxx",
})
```

### 6.3 反向连入（acp-ws + PluginHub 集成）

让 agent 通过 `/plugin/ws` 反向注册，pocketd 接受 ws 后用 WSTransport 接入：
- 复用现有 `internal/websocket/plugin_hub.go` 的 manager connection 类型
- 给 ws connection 加 JSON-RPC 帧封装

### 6.4 ACP 协议层完整实现（待补）

当前 `interface.go` 已声明完整 ACP 方法集，但 OpenCode adapter 包装层只实现了 OpenCode HTTP 实际支持的部分：

| ACP 方法 | OpenCode adapter | 真实 ACP adapter（待做） |
|----------|------------------|--------------------------|
| `initialize` | implicit | ✓ 必须 |
| `session/new` | ✓ via CreateSession | ✓ |
| `session/prompt` | ✓ via SendPrompt | ✓ |
| `session/cancel` | ✓ via InterruptSession | ✓ |
| `session/list` | ✓ via ListSessions | ✓ |
| `session/delete` | ✓ via DeleteSession | ✓ |
| `session/load` | ❌ NewCapabilityError | ✓ |
| `session/set_mode` | ❌ NewCapabilityError | ✓ |
| `session/request_permission` | ✓ via PermissionCapable | ✓ |
| `session/update` (notification) | ✓ via SubscribeEvents | ✓ |
| `authenticate` | ❌ | ✓ |
| `fs/read_text_file` | ❌ | optional |
| `terminal/*` | ❌ | optional |

## 7. 错误码规范

`internal/agent.Error` 字段：

| Code | 触发 | retryable | HTTP 状态 |
|------|------|-----------|-----------|
| `AGENT_UNREACHABLE` | dial / DNS / WS 断开 | true | 503 |
| `AGENT_TIMEOUT` | ctx deadline / IO 超时 | true | 503 |
| `AGENT_UPSTREAM` | agent 返回 5xx | true | 502 |
| `AGENT_BAD_REQUEST` | agent 返回 4xx | false | 502 |
| `AGENT_PROTOCOL` | JSON-RPC 帧错 / schema 违反 | false | 502 |
| `AGENT_CAPABILITY` | agent 不支持该能力 | false | 501 |
| `AGENT_CANCELLED` | 用户主动取消 | false | 499 |

前端可基于 `code` + `retryable` 做差异化处理。

## 8. 配置（env vars）

| 变量 | 默认 | 说明 |
|------|------|------|
| `POCKET_ACP_STDIO_AGENTS` | `""` | JSON 数组，定义 acp-stdio agent 列表 |
| `POCKET_ACP_HTTP_AGENTS` | `""` | JSON 数组，定义 acp-http agent 列表 |
| `POCKET_ACP_WS_AGENTS` | `""` | JSON 数组，定义 acp-ws agent 列表 |

格式（待 Phase 2 实装）：
```json
[
  {"name": "codex", "path": "/usr/local/bin/codex", "args": ["--acp"]},
  {"name": "claude-code", "path": "/usr/local/bin/claude", "args": ["--acp"]}
]
```

## 9. 兼容性

- **老 OpenCode handler 完全不变**（仍走 `s.opencode` 字段）
- 老 query 参数 `instance_id` 通过 `s.agents.GetByInstanceID()` 解析
- 新代码用 `s.agents.Get(AgentRef{...})` 显式选择 agent
- 不破坏任何前端 API

## 10. 后续 PR

| PR | 范围 | 工期 |
|----|------|------|
| #1（已完成）| 框架 + transport + OpenCode adapter + diagnostics | 6 周 |
| #2 | 完整 handler 迁移（18+ 调用点）+ 真实 Codex adapter + 测试 fake | 2 周 |
| #3 | acp-http transport 真接入（找一个实现该 draft 的 agent 测试）| 1 周 |
| #4 | acp-ws + PluginHub 集成（agent 反向连入）| 1 周 |
| #5 | 性能基准 + 大规模并发测试 | 1 周 |

## 11. 参考

- ACP 协议：https://agentclientprotocol.com
- JSON-RPC 2.0：https://www.jsonrpc.org/specification
- MCP（参考实现）：https://modelcontextprotocol.io
- Zed（参考实现方）：https://zed.dev
- 现有 OpenCode HTTP API：参考 `internal/adapter/opencode_http.go`