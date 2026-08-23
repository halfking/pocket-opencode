# 模块设计：IDE 控制（目标设计，审计修正版）

> 当前 RedClaw generic connectors 不是已实现的 IDE connector。ZCode/VS Code/Cursor/OpenCode adapter 均为 planned；OpenCode 仍使用其真实 `/session`、`/event`、permission/question、PTY/ACP 合同。

> **目标**: 通过 RedClaw Connectors 控制用户 PC 上的 IDE；
> **核心**: ZAgentGateway 暴露统一的 IDE 控制 API，所有 IDE 都走同一套接口。

---

## 1. 为什么需要 IDE 控制？

PocketFleet 的"用户故事"中，很多场景需要：

- 让 OpenClaw 知道当前 IDE 在做什么；
- 在 IDE 中打开特定文件 / 应用 diff；
- 让多个 IDE 协同（比如 OpenClaw 在 Cursor 修改代码，VS Code 调试）；
- 通过 OpenPocket 远程触发 IDE 操作。

**这些都通过 RedClaw Connectors 实现** —— IDE 都注册为 Connector。

---

## 2. 各 IDE 的接入方式

| IDE | 接入方式 | 协议 | 备注 |
|---|---|---|---|
| **ZCode** | 本地 RPC | Unix socket / TCP | ZCode 是项目自有 IDE（`/Users/xutaohuang/workspace/official-deploy`）|
| **VS Code** | VS Code Extension API + LSP | TCP / WebSocket | 通过 Microsoft VS Code Server (code-server) |
| **Cursor** | Cursor API | HTTPS | cursor.com 后端 API + 本地 Cursor binary |
| **OpenCode** | OpenCode HTTP Server | HTTP REST | `opencode serve` 默认 `:4096` |

### 2.1 ZCode 接入

ZCode 是项目自有的 IDE（agent 工具）。它暴露：

- 本地 RPC over Unix socket (`~/.zcode/socket`)；
- 支持 commands: `open_file`, `apply_diff`, `run_command`, `get_workspace_state`。

### 2.2 VS Code 接入

VS Code 通过 `code-server` (https://github.com/coder/code-server) 暴露：

- HTTP API at port `8080` (default)；
- 或 VS Code Extension 通过 LSP 通信；
- commands: `vscode.openFile`, `vscode.applyEdit`, `vscode.debug.start`。

### 2.3 Cursor 接入

Cursor 桌面端：

- 本地 binary `cursor` 通过 stdin/stdout RPC；
- 或 Cursor Server (`cursor-server`) at port `8080`；
- commands: `cursor.open`, `cursor.apply_diff`, `cursor.chat`。

### 2.4 OpenCode 接入（目标 adapter，不是 RedClaw 已有能力）

OpenCode 当前真实协议包括：

- `POST /session/:sessionID/message`，消息使用顶层 `parts`；
- `GET /event`，SSE EventV2；
- `/permission`、`/question`；
- `/pty` 和 WebSocket connect；
- Basic Auth 或 `auth_token`。

ZAG/OpenCode adapter 必须固定 OpenCode 版本并完成真实 server contract tests。不能使用 RedClaw `/api/v1/tasks`、`/api/v1/sessions` 或 generic connector receipt 伪装兼容。

---

### 3. RedClaw Connector 注册（目标合同，当前未实现）

RedClaw `internal/connectors` 当前是通用外部系统连接模型，不是已实现 IDE/ACP connector。以下注册仅作为目标 schema：

```go
// ZAG 启动时注册
func (z *ZAgent) RegisterIDEConnectors(ctx context.Context) error {
    connectors := []redclaw.ConnectorDefinition{
        {
            ConnectorID:    "ide_zcode",
            TenantID:       z.fleetID,
            Name:           "ZCode",
            Version:        "1.0.0",
            AuthMode:       "mTLS",
            Endpoints:      []string{"unix:///Users/me/.zcode/socket"},
            DataClasses:    []string{"workspace", "files", "commands"},
            RateLimit:      60,
            CursorType:     "webhook",
            SideEffectLevel: "medium",
        },
        {
            ConnectorID:    "ide_vscode",
            TenantID:       z.fleetID,
            Name:           "VS Code",
            Version:        "1.0.0",
            AuthMode:       "oauth2",
            Endpoints:      []string{"https://localhost:8080"},
            DataClasses:    []string{"workspace", "files", "commands", "debug"},
            RateLimit:      60,
            CursorType:     "webhook",
            SideEffectLevel: "medium",
        },
        {
            ConnectorID:    "ide_cursor",
            TenantID:       z.fleetID,
            Name:           "Cursor",
            Version:        "1.0.0",
            AuthMode:       "oauth2",
            Endpoints:      []string{"https://api.cursor.sh", "localhost:8080"},
            DataClasses:    []string{"workspace", "files", "commands"},
            RateLimit:      60,
            CursorType:     "webhook",
            SideEffectLevel: "high",  // Cursor 可以远程修改代码，side-effect 高
        },
        {
            ConnectorID:    "ide_opencode",
            TenantID:       z.fleetID,
            Name:           "OpenCode",
            Version:        "1.0.0",
            AuthMode:       "mTLS",
            Endpoints:      []string{"http://localhost:4096"},
            DataClasses:    []string{"workspace", "files", "commands", "sessions"},
            RateLimit:      60,
            CursorType:     "webhook",
            SideEffectLevel: "medium",
        },
    }
    
    for _, c := range connectors {
        if err := z.redclaw.Connectors.RegisterConnector(ctx, c); err != nil {
            return fmt.Errorf("register %s: %w", c.ConnectorID, err)
        }
    }
    return nil
}
```

---

## 4. ZAG IDE Control API

### 4.1 IDE 状态

```http
GET /api/v1/ide
GET /api/v1/ide/:name/status
```

`name` 取值：`zcode` | `vscode` | `cursor` | `opencode`

```json
// GET /api/v1/ide/zcode/status
{
  "name": "zcode",
  "version": "1.2.3",
  "running": true,
  "workspace": "/Users/me/myapp",
  "extensions": ["opencode@0.5.0", "pylsp@1.10"],
  "lastCommand": "open_file",
  "lastActivity": "2026-08-23T10:23:45Z"
}
```

### 4.2 IDE 命令

```http
POST /api/v1/ide/:name/command
{
  "command": "open_file" | "apply_diff" | "run_command" | "session.create" | ...,
  "args": { ... }
}
```

返回：

```json
{
  "receiptId": "r_001",
  "success": true,
  "statusCode": 200,
  "responseBody": "{ ... }",
  "executedAt": "..."
}
```

### 4.3 常用 IDE 命令

#### ZCode

| 命令 | 参数 | 说明 |
|---|---|---|
| `open_file` | `{ path }` | 在 ZCode 中打开文件 |
| `apply_diff` | `{ path, diff }` | 应用 diff 到文件 |
| `get_workspace_state` | `{}` | 拿当前工作区状态 |
| `run_command` | `{ cmd }` | 在 ZCode terminal 跑命令 |

#### VS Code

| 命令 | 参数 | 说明 |
|---|---|---|
| `vscode.openFile` | `{ path }` | 打开文件 |
| `vscode.applyEdit` | `{ path, edits[] }` | 应用编辑 |
| `vscode.debug.start` | `{ config }` | 启动调试 |
| `vscode.terminal.run` | `{ cmd }` | 跑命令 |

#### Cursor

| 命令 | 参数 | 说明 |
|---|---|---|
| `cursor.open` | `{ path }` | 打开文件 |
| `cursor.applyDiff` | `{ path, diff }` | 应用 diff |
| `cursor.chat` | `{ message }` | 发送聊天 |
| `cursor.cmdk` | `{ prompt }` | Cmd+K 命令 |

#### OpenCode

| 命令 | 参数 | 说明 |
|---|---|---|
| `session.create` | `{ title }` | 创建 session |
| `session.send` | `{ sessionId, message }` | 发送消息 |
| `session.list` | `{}` | 列出 session |
| `session.delete` | `{ sessionId }` | 删除 session |
| `file.read` | `{ path }` | 读文件 |
| `file.write` | `{ path, content }` | 写文件 |

---

## 5. ZAG 内部实现

### 5.1 IDE Service

```go
// internal/ide/service.go
package ide

type Service struct {
    redclaw *redclaw.Client
    repo    *Repository
}

func (s *Service) GetStatus(ctx context.Context, name string) (*IDEStatus, error) {
    // 1. 通过 Connector Execute 拉状态
    receipt, err := s.redclaw.Connectors.Execute(ctx, redclaw.ExecuteCommand{
        TenantID:       s.fleetID,
        ConnectionID:   "ide_" + name,
        Operation:      "status",
        IdempotencyKey: uuid.NewString(),
    })
    if err != nil { return nil, err }
    
    // 2. 解析
    var status IDEStatus
    json.Unmarshal([]byte(receipt.ResponseBody), &status)
    
    // 3. 落本地 DB
    s.repo.UpsertStatus(ctx, name, status)
    
    return &status, nil
}

func (s *Service) ExecuteCommand(ctx context.Context, name string, req IDECommand) (*ExecutionReceipt, error) {
    return s.redclaw.Connectors.Execute(ctx, redclaw.ExecuteCommand{
        TenantID:       s.fleetID,
        ConnectionID:   "ide_" + name,
        Operation:      req.Command,
        Payload:        req.Args,
        IdempotencyKey: uuid.NewString(),
    })
}

func (s *Service) ListAll(ctx context.Context) ([]IDEStatus, error) {
    var all []IDEStatus
    for _, name := range []string{"zcode", "vscode", "cursor", "opencode"} {
        status, err := s.GetStatus(ctx, name)
        if err != nil {
            // 单个 IDE 不可用不阻塞整体
            continue
        }
        all = append(all, *status)
    }
    return all, nil
}
```

### 5.2 各 IDE 适配（细节）

详见后续 IDE 适配文件。M3 阶段实现。

---

## 6. 与 PocketFleet 的集成

### 6.1 Pod 页面显示 IDE

OpenPocket 的 Pod 页面增加一栏 "IDE 状态"：

```
┌──────────────────────────────────────────┐
│  🟢 mbp16-居家                            │
│  Apple M3 Max · 64 GB · macOS 15          │
│  3 个 Agent 在线 · 5 个任务                │
│                                          │
│  IDE:                                    │
│    🟢 ZCode v1.2.3  工作区: /Users/me/myapp│
│    🟢 VS Code 1.85  无工作区              │
│    🟡 Cursor 0.40    未运行               │
│    ⚪ OpenCode      未安装                │
│                                          │
│  OpenClaw: v1.2.3 (running)              │
│                                          │
│  [ Send Command ]  [ View Workspace ]    │
└──────────────────────────────────────────┘
```

### 6.2 OpenPocket 调用 IDE API

```go
// pocketd fleetbridge.ide.go
func (h *IDEHandler) Status(c echo.Context) error {
    name := c.Param("name")
    status, err := h.zag.GetIDEStatus(c.Request().Context(), name)
    if err != nil { return jsonError(c, err) }
    return c.JSON(200, status)
}
```

### 6.3 MCP 暴露

```json
// MCP tool
{
  "name": "zag_get_ide_status",
  "description": "Get IDE status (zcode/vscode/cursor/opencode)",
  "inputSchema": {
    "type": "object",
    "properties": {
      "name": { "type": "string", "enum": ["zcode", "vscode", "cursor", "opencode"] }
    },
    "required": ["name"]
  }
}
```

---

## 7. 权限与安全

### 7.1 IDE 命令的 SideEffect Level

每个 IDE Connector 注册时指定 `SideEffectLevel`：

| IDE | SideEffect | 含义 |
|---|---|---|
| ZCode | medium | 可读 + 写文件 + 跑命令 |
| VS Code | medium | 可读 + 写文件 + 调试 |
| Cursor | high | 可远程修改代码 + AI 协助 |
| OpenCode | medium | planned；必须兼容固定版本的 `/session`、`parts`、`/event`、PTY/ACP |

### 7.2 Plan A / Plan E 审批

ZAG 转发 IDE 命令到 RedClaw 时，按 Plan 决定是否需要审批：

- **Plan A**：低风险命令（read-only）直接执行；
- **Plan E**：高风险命令（git push、删除文件）需要用户审批。

### 7.3 与 OpenClaw Permission 协同

OpenClaw 想调 IDE 时，permission request 走 OpenClaw → RedClaw → ZAG → OpenPocket。

用户在 OpenPocket Modal 中看到 "OpenClaw 想用 Cursor apply_diff，是否允许？"。

---

## 8. 实施计划

### 8.1 M0（最小化）

- ZAG 启动时注册 4 个 IDE Connector（如果 PC 上有）；
- ZAG 暴露 `/api/v1/ide` list + status；
- OpenPocket Pod 页面显示 IDE 列表；
- 不实现 IDE 命令执行（M1+）。

### 8.2 M1（基本功能）

- ZAG 暴露 `/api/v1/ide/:name/command`（转发到 RedClaw Connector Execute）；
- OpenPocket IDE 控制面板（按钮 + 命令）；
- ZCode 适配完整（项目自有，优先）；
- OpenCode 适配（目标；必须固定版本并验证 `/session`、parts、`/event`、permission/question、PTY/ACP）。

### 8.3 M2（完整功能）

- VS Code 适配；
- Cursor 适配；
- 与 OpenClaw 协同：OpenClaw 跑任务时通过 IDE 显示进度。

---

## 9. 一句话总结

**ZAG 的目标是统一暴露 IDE adapter；当前不能宣称四个 IDE 已被 RedClaw Connectors 接通。默认先做只读状态和固定 OpenCode contract，写操作必须经过命令 schema、workspace sandbox 和审批门禁。**
