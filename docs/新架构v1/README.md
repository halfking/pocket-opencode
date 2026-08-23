# 新架构 v3 — ZAgentGateway + 双端协同（OpenPocket + RedClaw）

> **代号**: PocketFleet v3
> **版本**: v3.0
> **日期**: 2026-08-23
> **状态**: 审计后设计阶段（未实现）
> **重要边界**: 本目录是目标架构，不代表当前运行能力。当前仓库没有 ZAgentGateway、`/api/fleet/*`、IDE 控制面板或 RedClaw/OpenCode 端到端实现；所有相关功能必须以 `implemented / contract-tested / source-inspected / mock-only / planned` 标注证据等级。
> **核心改动**: 引入 **ZAgentGateway** —— 一个全新的独立 Go 服务，作为 RedClaw（PC 桌面）与 OpenPocket（移动端）+ acc-go（任务编排）三者之间的智能体监测与控制中介。
> **审计结论**: RedClaw 当前不能直接作为 OpenCode 的 drop-in backend。推荐保留 OpenCode coding runtime，将 RedClaw 用作企业控制面；如需无感兼容 OpenCode，必须另行实现完整 OpenCode-compatible facade。

---

## 一、TL;DR（v1 → v2 → v3 的演进）

| 版本 | 思路 | 关键问题 |
|---|---|---|
| **v1** | 在 pocketd 内自建 Chief / Executor Bridge / Harness 协议 | 重复造轮子；与 monorepo 已有能力冲突 |
| **v2** | 复用 acc-go / Memora / llm-gateway-go 作为 Chief / Memory / LLM；算力舱 = acc-go Worker | **忽略了 RedClaw 是 PC 桌面端，已经有 OpenClaw Runtime + 本地 IDE 控制**；OpenPocket 的应用后端已经在用 RedClaw 的能力 |
| **v3** | **新增 `ZAgentGateway`**：独立服务，把 RedClaw 的 PC Agent Runtime + 本地 IDE 控制能力，统一暴露为 REST/MCP API；同时接入 acc-go 作为任务编排；供 OpenPocket 移动端调用 | 完整的"PC 端执行 + 移动端监控 + 任务编排"三角闭环 |

---

## 二、v3 的三大设计原则

1. **不动 RedClaw 的现有架构** —— RedClaw 仍然是 PC 桌面 OpenClaw 的企业包装；OpenClaw Runtime + 本地 IDE 控制由 RedClaw 自己管理。
2. **新建独立的 `ZAgentGateway` 服务** —— 单一职责：**对外**暴露 PC Agent 的监测与控制 API（REST + MCP + WebSocket），**对内**对接 RedClaw platform-go + acc-go + llm-gateway-go + Memora。
3. **OpenPocket 不直接连 RedClaw 控制面** —— 正常路径上 OpenPocket 只连 **ZAgentGateway**。RedClaw 是 PC 端企业控制/执行层，ZAgentGateway 是受控适配层，OpenPocket 是移动控制台。任何降级路径也不得绕过租户校验、授权、审批和审计。

---

## 三、整体架构图（v3）

```
                       ┌──────────────────────────────┐
                       │  移动端 OpenPocket            │
                       │  (Vue 3 + Capacitor + PWA)   │
                       │  - Live Activity             │
                       │  - Permission Modal          │
                       │  - Voice / Camera            │
                       │  - Schedule / Cost Dashboard │
                       └──────────────┬───────────────┘
                                      │ HTTPS / WSS
                                      │ JWT (POCKET_JWT_SECRET)
                                      ▼
              ┌─────────────────────────────────────────────────┐
              │      OpenPocket Backend (pocketd, :8088)         │
              │      ┌──────────────────────────────────────┐   │
              │      │  internal/fleetbridge/               │   │
              │      │  - intent.go (POST /api/fleet/intent)│   │
              │      │  - build.go / task.go / permission.go│   │
              │      │  - ws_bridge.go (SSE → WS Hub)       │   │
              │      │  - charter.go / skill.go (Memora)    │   │
              │      │  - cost.go (llm-gateway-go usage)   │   │
              │      │  - pod.go / agent.go (透传 ZAG)     │   │
              │      └──────────────────────────────────────┘   │
              └──┬───────────┬─────────────────┬─────────────────┘
                 │           │                 │
                 ▼           ▼                 ▼
        ┌─────────────────────────┐  ┌─────────────────────────┐
        │  Memora                 │  │  llm-gateway-go         │
        │  kxmemory-go (:8080)    │  │  (:8781)                │
        │  - Charter / Skill      │  │  - LLM Provider Router  │
        │  - Agent Memory         │  │  - URSM / P2C / Bandit │
        │  - Build Event Log      │  │  - DeepSeek 一等公民    │
        │  - Cost Aggregate       │  │                         │
        └─────────────────────────┘  └─────────────────────────┘
                 ▲
                 │  X-Tenant-ID + JWT
                 │
   ┌─────────────┴──────────────────────────────────────┐
   │      ZAgentGateway (NEW, :9100)                    │
   │      ┌────────────────────────────────────────┐   │
   │      │  智能体监测与控制 API                    │   │
   │      │  - /api/v1/agents    (CRUD / heartbeat) │   │
   │      │  - /api/v1/pods      (PC device 列表)   │   │
   │      │  - /api/v1/sessions  (OpenClaw session) │   │
   │      │  - /api/v1/tasks     (任务分配 / 状态)   │   │
   │      │  - /api/v1/permissions                   │   │
   │      │  - /api/v1/events   (SSE)               │   │
   │      │  - /mcp             (Streamable HTTP)    │   │
   │      │  - /ws              (WebSocket)          │   │
   │      └────────────────────────────────────────┘   │
   │      ┌────────────────────────────────────────┐   │
   │      │  内部分层                                │   │
   │      │  - redclaw-client   (调 RedClaw gateway)│   │
   │      │  - acc-client       (调 acc-go)         │   │
   │      │  - llm-client       (调 llm-gateway-go) │   │
   │      │  - memora-client    (调 Memora)         │   │
   │      └────────────────────────────────────────┘   │
   └─────────────┬──────────────────────────┬──────────┘
                 │                          │
                 ▼                          ▼
   ┌──────────────────────────┐  ┌─────────────────────────┐
   │  acc-go (:4101)          │  │  RedClaw platform-go    │
   │  - taskdecompose         │  │  (PC 桌面企业包装)        │
   │  - orchestrator          │  │  ┌────────────────────┐ │
   │  - orchestration v3      │  │  │ orchestrator       │ │
   │  - MCP /api/v2/mcp       │  │  │ :8090 (任务队列)   │ │
   │  - A2A /api/v2/a2a       │  │  └────────────────────┘ │
   └──────────────────────────┘  │  ┌────────────────────┐ │
                                │  │ agentcontainer     │ │
                                │  │ :8091 (OpenClaw    │ │
                                │  │   运行时 + Skills)  │ │
                                │  └────────────────────┘ │
                                │  ┌────────────────────┐ │
                                │  │ gateway            │ │
                                │  │ :8091 (Bedrock代理) │ │
                                │  └────────────────────┘ │
                                │  ┌────────────────────┐ │
                                │  │ connectors         │ │
                                │  │ (zcode/ide 插件    │ │
                                │  │  + OpenClaw 控制)   │ │
                                │  └────────────────────┘ │
                                │  ┌────────────────────┐ │
                                │  │ authagent          │ │
                                │  │ :8092 (SSO + 审批) │ │
                                │  └────────────────────┘ │
                                │  ┌────────────────────┐ │
                                │  │ admin              │ │
                                │  │ :8093 (控制台后端) │ │
                                │  └────────────────────┘ │
                                └─────────────┬───────────┘
                                              │
                                              ▼
                                ┌─────────────────────────┐
                                │  PC Desktop (用户的 Mac) │
                                │  ┌─────────────────────┐│
                                │  │ OpenClaw CLI        ││
                                │  │ (PC 桌面 AI 助手)    ││
                                │  └─────────────────────┘│
                                │  ┌─────────────────────┐│
                                │  │ 本地 IDE 控制       ││
                                │  │  - ZCode             ││
                                │  │  - VS Code           ││
                                │  │  - Cursor            ││
                                │  │  - OpenCode          ││
                                │  │  - 其他 IDE 插件     ││
                                │  └─────────────────────┘│
                                │  ┌─────────────────────┐│
                                │  │ Browser / Shell /   ││
                                │  │ File System / Git   ││
                                │  └─────────────────────┘│
                                └─────────────────────────┘
```

---

## 四、角色分工（v3）

| 角色 | 服务 | 端口 | 职责 |
|---|---|---|---|
| **移动控制台** | OpenPocket (pocketd) | 8088 | 移动 UI + 鉴权 + WebSocket Hub + 已有功能（email/notes/vault/meeting 等）|
| **独立中介（NEW）** | **ZAgentGateway** | **9100** | **统一 PC Agent 监测与控制 API；MCP server；对接 RedClaw + acc-go + llm-gateway-go + Memora** |
| **任务编排** | acc-go | 4101 | Chief 拆分 + orchestrator + MCP 41 tools + A2A |
| **PC 桌面控制/执行（目标集成）** | RedClaw platform-go | 由 RedClaw 部署配置决定；不要假设 8090-8093 可公网暴露 | 已确认：task/session/control/agentcontainer/OpenClaw 基础；OpenCode-compatible API、IDE connector 和真实 event projection 尚未验证 |
| **PC 桌面助手（被包装的）** | OpenClaw CLI | - | 用户 Mac 上的桌面 AI 助手 |
| **本地 IDE 控制** | RedClaw connectors + IDE 插件 | - | 控制 ZCode / VS Code / Cursor / OpenCode 等 |
| **LLM 网关** | llm-gateway-go | 8781 | 多 Provider 路由 + URSM + DeepSeek 等 |
| **记忆 / Charter / Skill** | Memora (kxmemory-go) | 8080 | L1–L6 + CodeGraph + Dream Engine |
| **RedClaw Edge Gateway** | RedClaw Gateway | 8092 | 移动端专用 edge（与 ZAgentGateway 并存） |

---

## 五、ZAgentGateway 的核心定位

> **ZAgentGateway = RedClaw 与 OpenPocket / acc-go 之间的"智能体监测与控制中介"**

它存在的根本原因是：

1. **RedClaw 不适合被移动端直接调用** —— RedClaw platform-go 是企业平台（含 SSO、Plan E 审批、Ed25519 双签名等），直接暴露给移动端不安全、且 API 不友好。
2. **acc-go 不应该直接连 RedClaw** —— acc-go 是任务编排器，不应该知道 RedClaw 的存在；它只该知道"我的 worker 在跑任务"。
3. **OpenPocket 已经有后端 API 用 RedClaw 的能力** —— 但目前是 pocketd 直接调 RedClaw Gateway（`:8092`，echo stub），需要把这条路径规范化。
4. **未来要支持"PC Agent 控制 + 多 IDE 插件"** —— ZCode / VS Code / Cursor / OpenCode 等需要被统一监测和控制。

### 5.1 ZAgentGateway 的六大能力

1. **PC Agent 注册与心跳聚合** —— RedClaw 的 device / agent / session 信息被 ZAgentGateway 拉取并规范化。
2. **PC Agent 监测** —— health / status / capabilities / in-flight / resource usage；SSE/WebSocket 推送。
3. **PC Agent 控制** —— pause / resume / restart / upgrade / rollback（对接 RedClaw control signals with Ed25519 双签）。
4. **本地 IDE 控制** —— 通过 RedClaw connectors 控制 ZCode / VS Code / Cursor / OpenCode 等。
5. **MCP server** —— 把 PC Agent 能力暴露为 MCP tools，让 acc-go / Cursor / Claude Code 等 MCP 客户端调用。
6. **acc-go 集成** —— ZAgentGateway 注册为 acc-go 的 device / worker，承接 acc-go 派发的任务，调用 RedClaw 执行。

### 5.2 ZAgentGateway 的关键 API 表面

```
REST (v1, /api/v1/*):
  GET    /agents                 # PC Agent 列表
  POST   /agents/:id/invoke      # 触发一个 Agent 调用
  GET    /agents/:id/events      # SSE 流

  GET    /pods                   # RedClaw device 列表
  GET    /pods/:id               # device 详情 + capabilities
  POST   /pods/:id/control       # pause / resume / restart (双签)

  GET    /sessions               # OpenClaw session 列表
  POST   /sessions               # 创建 session
  GET    /sessions/:id           # session 详情
  POST   /sessions/:id/messages  # 发送消息
  POST   /sessions/:id/cancel

  GET    /tasks                  # 任务列表
  POST   /tasks                  # 提交任务
  GET    /tasks/:id              # 任务详情
  POST   /tasks/:id/cancel

  GET    /permissions            # 待审批权限
  POST   /permissions/:id/reply

  GET    /ide/:name/status       # IDE 状态（zcode / vscode / cursor / opencode）
  POST   /ide/:name/command      # IDE 命令

MCP:
  Streamable HTTP at /mcp
  Tools: zag_* (agent 列表 / invoke / monitor / control)

WebSocket:
  /ws (JWT)
  Events: agent.status / session.message / task.update / permission.request / cost.tick
```

### 5.3 ZAgentGateway 与各服务的关系

| 上游 / 下游 | 关系 | 协议 |
|---|---|---|
| **OpenPocket (pocketd)** → ZAgentGateway | pocketd 调 ZAgentGateway 拿 PC Agent 信息、提交任务、订阅事件 | HTTPS REST + WSS |
| **ZAgentGateway → acc-go** | ZAgentGateway 注册为 acc-go 的 device / worker，承接 acc-go 派发的任务 | REST + SSE |
| **ZAgentGateway → RedClaw platform-go** | ZAgentGateway 调 RedClaw 的 orchestrator / agentcontainer / connectors；用 Ed25519 双签控制信号 | REST + Ed25519 |
| **ZAgentGateway → RedClaw Gateway (`:8092`)** | ZAgentGateway 调 RedClaw Gateway 走 LLM / knowledge 检索；逐步接管其 echo stub | REST |
| **ZAgentGateway → llm-gateway-go** | ZAgentGateway 直接调 llm-gateway-go 调 LLM（兜底；主路径走 RedClaw gateway） | OpenAI-compatible REST |
| **ZAgentGateway → Memora** | ZAgentGateway 写 Agent 事件日志、Charter、Skill、Memory | REST /api/v2/memories |
| **MCP 客户端（Cursor / Claude Code）→ ZAgentGateway** | 通过 MCP 协议调用 ZAgentGateway 的 zag_* 工具 | Streamable HTTP MCP |

---

## 六、数据流（v3）

### 6.1 用户故事：手机发起"修复 typo"

```
1. 用户在 OpenPocket mobile 输入 "修复 README typo"
2. pocketd POST /api/fleet/intent
3. pocketd fleetbridge.intent.go 转发到 acc-go:
   POST /api/v2/canonical/tasks
4. acc-go taskdecompose (规则化，单任务)
5. acc-go orchestration v3 → 选可用 worker
   → 选中 ZAgentGateway (worker_001)
6. acc-go 通过 SSE 推 mission 事件:
   POST ZAgentGateway /api/v1/tasks
7. ZAgentGateway 收到任务 → 转发到 RedClaw platform-go:
   POST /api/v1/tasks
8. RedClaw orchestrator 入队
9. RedClaw agentcontainer 派给 OpenClaw CLI:
   - 创建 workspace, 拉代码
   - 调用 OpenClaw invoke (调 llm-gateway-go)
   - OpenClaw 通过 ZCode / VS Code 插件修改文件
10. RedClaw SSE 流回到 ZAgentGateway
11. ZAgentGateway SSE 流回到 acc-go
12. acc-go SSE 流被 pocketd ws_bridge.go 订阅 → 转 WS Hub
13. OpenPocket mobile Live UI 显示实时进度
14. 完成 → 触发 PR → 用户在手机上看到链接
```

### 6.2 用户故事：手机查看 PC Agent 状态

```
1. 用户在 OpenPocket mobile 进入 "Pods" 页面
2. pocketd GET /api/fleet/pods
3. pocketd fleetbridge.pod.go 转发到 ZAgentGateway:
   GET /api/v1/pods
4. ZAgentGateway 内部:
   - 调 RedClaw platform-go GET /api/v1/devices
   - 调 acc-go GET /api/v2/devices (合并)
   - 加 capabilities (本地 IDE / OpenClaw version / etc.)
5. ZAgentGateway 返回统一格式的 Pod 列表
6. pocketd 转给 mobile
7. mobile 显示 "mbp16-居家 / 3 个 IDE 已连接 / 5 个 Agent 在线"
```

### 6.3 用户故事：手机审批 PC Agent 的 git push

```
1. ZAgentGateway 收到 RedClaw 推送的 permission request
   (OpenClaw 想 git push)
2. ZAgentGateway 转发:
   - 落 Memora (审计)
   - 推 SSE 给 acc-go
   - 推 WebSocket 给 OpenPocket (通过 pocketd fleetbridge.ws_bridge.go)
3. OpenPocket mobile 显示 Modal
4. 用户点击 "Allow"
5. POST pocketd /api/fleet/builds/:id/permissions/:pid/reply
6. pocketd fleetbridge.permission.go 转发到 ZAgentGateway:
   POST /api/v1/permissions/:id/reply
7. ZAgentGateway 转发到 RedClaw platform-go (HMAC token 验证 + Ed25519 双签):
   POST /api/v1/control/:command_id/signature
   POST /api/v1/control/:command_id/execute
8. RedClaw 执行 git push
9. 事件回流到 OpenPocket
```

---

## 七、为什么必须新增 ZAgentGateway 而不是直接打通？

| 反对意见 | 回复 |
|---|---|
| "为什么不让 OpenPocket 直接连 RedClaw？" | RedClaw 是企业平台，包含 Plan E 审批、Ed25519 双签、SSO 等；直接暴露给移动端不安全。ZAgentGateway 做边界。 |
| "为什么不让 acc-go 直接调 RedClaw？" | acc-go 应该只关心"worker 在跑任务"，不应该知道 RedClaw 的存在；让 ZAgentGateway 做 acc-go 的 worker 适配器。 |
| "为什么不让 pocketd 直接做这件事？" | pocketd 是移动端后端；它的职责是 mobile 适配、鉴权、WebSocket Hub；不应该承担"PC Agent 控制中心"的角色。把这个角色独立出来更清晰。 |
| "为什么不直接复用 acc-go 的 device？" | acc-go 的 device 是通用 worker；ZAgentGateway 是专用 worker，额外能力：① 转发 RedClaw 事件；② 暴露 MCP；③ IDE 状态聚合；④ 鉴权边界 |
| "为什么不复用 RedClaw 的 gateway？" | RedClaw gateway 是 LLM 代理；ZAgentGateway 是 Agent 控制中心；职责不同 |

---

## 八、ZAgentGateway 的技术栈

- **语言**: Go 1.25（与 RedClaw platform-go / acc-go 一致）
- **HTTP**: gin 或 echo
- **MCP**: `modelcontextprotocol/go-sdk`（acc-go 已经在用）
- **WebSocket**: gorilla/websocket
- **存储**: PostgreSQL（共享 cluster）；Qdrant / Neo4j 通过 Memora
- **LLM**: llm-gateway-go 客户端
- **租户**: tenant.From(ctx) 强制 L1 invariant（沿用 acc-go 风格）
- **部署**: Docker / K8s；多副本 + 健康检查 + graceful shutdown

### 8.1 包结构

```
zagent-gateway/
├── cmd/
│   ├── api/main.go          # HTTP API + MCP + WebSocket
│   └── worker/main.go       # 后台同步 worker
├── internal/
│   ├── platform/            # 配置 / 日志 / 错误 / DB / 健康 / 监控
│   ├── redclaw/             # RedClaw client + control signals
│   ├── acc/                 # acc-go client
│   ├── llm/                 # llm-gateway-go client
│   ├── memora/              # Memora client
│   ├── agent/               # Agent 注册 / 心跳 / 能力
│   ├── pod/                 # PC device 列表 / 能力
│   ├── ide/                 # IDE 控制（zcode/vscode/cursor/opencode）
│   ├── session/             # OpenClaw session 包装
│   ├── task/                # 任务提交 / 状态机
│   ├── permission/          # 权限请求 / 审批
│   ├── mcp/                 # MCP server
│   ├── ws/                  # WebSocket hub
│   └── api/                 # REST handlers
├── migrations/              # PostgreSQL DDL
├── deploy/                  # K8s / docker-compose
└── README.md
```

### 8.2 数据模型

```go
type Agent struct {
    ID          string
    FleetID     string
    Name        string
    Kind        string         // "openclaw" / "zcode" / "vscode" / "cursor" / "opencode"
    Status      string         // online / busy / offline
    Version     string
    Capabilities []string      // ["file_read", "shell_run", "git_push"]
    PodID       string
    LastSeen    time.Time
    Metadata    map[string]any
}

type Pod struct {
    ID          string
    Name        string
    Hostname    string
    OS          string
    Status      string         // online / offline / asleep
    CPUs        int
    MemoryGB    int
    GPU         string
    Agents      []string       // agent IDs
    IDEs        []string       // ["zcode", "vscode"]
    Region      string
    LastSeen    time.Time
}

type IDEStatus struct {
    Name        string         // "zcode" / "vscode" / "cursor" / "opencode"
    Version     string
    Running     bool
    Workspace   string
    Extensions  []string
}
```

---

## 九、与其他模块的关系（v3 更新）

### 9.1 与 v2 的差异

| 维度 | v2 | v3 |
|---|---|---|
| Chief | 复用 acc-go | **复用 acc-go**（不变）|
| 任务编排 | 复用 acc-go | **复用 acc-go**（不变）|
| 多 Agent Harness | acc-go agent/harness | **acc-go + RedClaw OpenClaw**（扩展）|
| 算力舱 | acc-go Worker | **ZAgentGateway 作为 acc-go 的 worker；ZAgentGateway 内部转发到 RedClaw**（重要变化）|
| 本地 IDE 控制 | 无 | **ZAgentGateway + RedClaw connectors**（新增）|
| 移动端壳 | pocketd | **pocketd**（不变）|
| LLM 网关 | llm-gateway-go | **llm-gateway-go**（不变）|
| 记忆 | Memora | **Memora**（不变）|
| MCP | acc-go /api/v2/mcp | **acc-go MCP + ZAgentGateway MCP (zag_*)**（双 MCP）|

### 9.2 与 RedClaw 的关系

RedClaw **不变**。它继续作为：
- PC 桌面 OpenClaw 的企业包装
- 自带 platform-go 后端（api / orchestrator / agentcontainer / gateway / connectors / authagent / admin）
- 自带 CloudFormation 部署
- 自带 OpenClaw CLI runtime + Skill 系统

ZAgentGateway 是**新增的中间层**，把 RedClaw 的能力规范化后暴露给其他服务。

### 9.3 与 OpenPocket 的关系

OpenPocket 不变（除了新增 `internal/fleetbridge/` 模块）。pocketd fleetbridge 把 mobile intent 转换为对 ZAgentGateway + acc-go 的调用。

---

## 十、净增量代码清单（v3）

### 10.1 新建：ZAgentGateway（~8k 行 Go）

```
zagent-gateway/
├── cmd/api/main.go                  # ~300 行
├── cmd/worker/main.go               # ~200 行
├── internal/platform/                # ~1k 行
├── internal/redclaw/                 # ~1.5k 行（RedClaw client + Ed25519 双签）
├── internal/acc/                     # ~500 行（acc-go client）
├── internal/llm/                     # ~300 行（llm-gateway-go client）
├── internal/memora/                  # ~400 行（Memora client）
├── internal/agent/                   # ~600 行
├── internal/pod/                     # ~500 行
├── internal/ide/                     # ~800 行（ZCode/VSCode/Cursor/OpenCode 适配）
├── internal/session/                 # ~500 行
├── internal/task/                    # ~600 行
├── internal/permission/              # ~400 行
├── internal/mcp/                     # ~600 行（Streamable HTTP MCP）
├── internal/ws/                      # ~500 行
└── internal/api/                     # ~800 行
```

总计 ~8k 行 Go（含测试）。

### 10.2 pocketd 新增：`internal/fleetbridge/` (~2k 行)

不变（v2 设计）。

### 10.3 pocketd 新增：`internal/zag/` (~500 行)

pocketd 调 ZAgentGateway 的客户端封装。

### 10.4 acc-go 增量（~500 行）

- `internal/zag/` —— ZAgentGateway 作为 worker 的注册逻辑；
- 在 MCP tool 列表加 `acc_zag_*` —— 转发到 ZAgentGateway 的 MCP tool。

### 10.5 其他（**零改动**）

- llm-gateway-go：完全不动。
- Memora (kxmemory-go)：完全不动。
- RedClaw platform-go：完全不动。
- OpenClaw CLI：完全不动。

---

## 十一、立即可启动的 6 周 PoC（M0）

- **Week 1-2**：ZAgentGateway 骨架（HTTP + WebSocket + MCP）+ RedClaw platform-go client（基础 GET/POST）。
- **Week 3**：实现 `/api/v1/pods`、`/api/v1/agents` 列表（从 RedClaw platform-go 拉取 + 规范化）。
- **Week 4**：实现 `/api/v1/tasks` 提交 + 状态查询（调 RedClaw orchestrator + agentcontainer）；SSE 流转发。
- **Week 5**：实现 MCP server，暴露 `zag_*` tools；acc-go MCP client 调用。
- **Week 6**：pocketd `internal/fleetbridge/` + `internal/zag/` 联调；OpenPocket mobile Live View 跑通。

**M0 交付物**：

1. 手机 OpenPocket 发起"修复 typo"指令 → acc-go 派给 ZAgentGateway → ZAgentGateway 转发给 RedClaw platform-go → OpenClaw CLI 跑任务 → 事件流回到手机 Live View。
2. 手机 OpenPocket "Pods" 页面显示 RedClaw device 列表。
3. 手机 OpenPocket 审批 OpenClaw 的 git push 权限。

---

## 十二、文档目录（v3）

| 文档 | 用途 | 状态 |
|---|---|---|
| [README.md](README.md) | 本文档 | **重写**（v3） |
| [00-research/竞品分析.md](00-research/竞品分析.md) | todos.dev / Cursor / Factory / Devin / OpenHands | 保留 |
| [00-research/技术栈调研.md](00-research/技术栈调研.md) | DeepSeek / Pi Agent / 算力舱 | 保留 |
| [00-research/现有服务能力盘点.md](00-research/现有服务能力盘点.md) | acc-go / Memora / llm-gateway-go / RedClaw platform-go | **更新**（加 RedClaw） |
| [00-research/RedClaw作为OpenCode后端审计.md](00-research/RedClaw作为OpenCode后端审计.md) | **NEW** RedClaw 作为 OpenCode 后端的可行性与安全审计 |
| [01-architecture/系统总览.md](01-architecture/系统总览.md) | 整体架构图 | **重写**（v3） |
| [01-architecture/数据流与协议.md](01-architecture/数据流与协议.md) | 协议栈 | **重写**（v3） |
| [01-architecture/安全模型.md](01-architecture/安全模型.md) | 安全模型 | **更新** |
| [02-modules/zagent-gateway.md](02-modules/zagent-gateway.md) | **NEW** ZAgentGateway 详细设计 |
| [02-modules/redclaw-integration.md](02-modules/redclaw-integration.md) | **NEW** RedClaw 集成细节 |
| [02-modules/ide-control.md](02-modules/ide-control.md) | **NEW** IDE 控制（zcode/vscode/cursor/opencode） |
| [02-modules/chief-as-acc.md](02-modules/chief-as-acc.md) | Chief = acc-go | 保留 |
| [02-modules/memory-as-memora.md](02-modules/memory-as-memora.md) | Memory = Memora | 保留 |
| [02-modules/llm-gateway-integration.md](02-modules/llm-gateway-integration.md) | LLM 路由集成 | 保留 |
| [02-modules/pocketd-fleet-bridge.md](02-modules/pocketd-fleet-bridge.md) | pocketd fleetbridge | **更新**（加 ZAgentGateway 客户端） |
| [02-modules/compute-pod-as-zag.md](02-modules/compute-pod-as-zag.md) | **NEW** 算力舱 = ZAgentGateway (而非 acc-go worker) |
| [02-modules/mobile-shell.md](02-modules/mobile-shell.md) | Mobile shell | 保留 |
| [03-roadmap/里程碑.md](03-roadmap/里程碑.md) | M0–M5 | **重写**（v3） |
| [03-roadmap/接口规范.md](03-roadmap/接口规范.md) | REST + WS + MCP 接口 | **重写**（v3） |
| [04-appendix/对比表.md](04-appendix/对比表.md) | 与竞品对比 | 保留 |
| [04-appendix/风险与缓解.md](04-appendix/风险与缓解.md) | 风险与缓解 | **更新** |
| [architecture-decision-records.md](architecture-decision-records.md) | ADR | **重写**（v3） |

---

## 十三、与 v2 的核心差异（一眼对照）

```
v2:
   手机 → pocketd → acc-go → llm-gateway-go → DeepSeek
                       ↓
                       Pod (acc-go worker on user's machine)
                       ↓
                       Claude / OpenCode CLI

v3:
   手机 → pocketd → acc-go → ZAgentGateway → RedClaw platform-go → OpenClaw CLI
                       ↓                    ↓                          ↓
                    (任务编排)        (控制平面 / Ed25519 双签)        (实际执行)
                                          ↓
                                       zcode / vscode / cursor / opencode 插件
```

**v3 把 v2 中的 "Pod = acc-go Worker" 替换为 "Pod = ZAgentGateway + RedClaw platform-go + OpenClaw CLI + 本地 IDE 控制"**。

这一改动的本质：把 OpenClaw（PC 桌面 AI 助手）的能力纳入 PocketFleet 的执行层，让用户能在手机上控制自己 PC 上的 OpenClaw + IDE 完成编程任务。
