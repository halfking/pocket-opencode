# Pi 智能体内嵌架构（In-App Pi Agent Architecture）

> 角色：资深系统架构师
> 范围：将 `~/workspace/ai/pi` 中的 Pi 智能体（`@earendil-works/pi-coding-agent`）置入自有 App，并强化"本地智能体执行"能力
> 文档状态：方案稿 v1（基于代码评审 + 既有约束）

---

## 0. 摘要（TL;DR）

Pi 在仓库中已经是一个高度模块化的智能体运行时（已分层为 `pi-ai`、`pi-agent-core`、`pi-coding-agent`、`pi-server`、`pi-client`、`chord`、`telemetry`、`session-backends/sqlite-node`），且 **2 种本地嵌入路径已现成可用**：

| 路径 | 形态 | 适合场景 | 风险 |
|------|------|----------|------|
| **A. RPC 模式** | App 作为父进程拉起 `pi --mode rpc`，stdin/stdout JSONL | 单进程集成，最低改动；功能覆盖 TUI/Interactive 全集 | 需要父进程生命周期管理；与 App 进程崩溃耦合 |
| **B. Pi Server（实验性）** | App 作为 Unix Socket 客户端连 `@earendil-works/pi-server`；server 内部再 spawn 出 `session-worker` 子进程 | 多端点、长会话、需要 sandbox 隔离；可水平扩 worker | API 标注 "experimental"，稳定性风险；Unix-only |

**推荐主线：路径 A（MVP） → 路径 B（M2+）**。理由：先把"功能齐全 + 可观测 + 可降级"的单进程方案跑通；本地执行强化（容器化/VM）单独成阶段。

---

## 1. 假设与约束（用户 / 流量 / 限制）

> 用户未显式给出具体数字，下列为分析时采用的合理假设与可调区间。

### 1.1 用户画像
- **主用户**：开发者（与 Pi 的原生受众一致）。App 形态定位 = **桌面 IDE 插件 / 本地助理类应用**（Mac/Win/Linux 三端）。
- **使用模式**：单用户单进程；偶有"远程接入"（手机端查看会话）需求。
- **诉求**：在 App 内直接执行 Bash、读写本地工程文件、跑构建、调用 LLM；会话可恢复。

### 1.2 流量与并发
- **每端点同时活跃会话**：1–3（M1），目标 ≤10（M2）。
- **每会话 QPS**：取决于工具调用；典型 0.2–2 req/s（流式）。
- **本地进程模型**：单进程同源，便于 1:N（用户 ↔ 会话 ↔ LLM 流）。

### 1.3 限制与红线
| 维度 | 限制 | 应对 |
|------|------|------|
| 文件系统访问 | Pi 默认**无内置权限模型**（见 README §Permissions & Containerization） | 必须用 sandbox（Gondolin/Docker/OpenShell/自研 micro-VM） |
| 凭据 | Pi 凭据默认落 `~/.pi/agent`；端上不能用明文 | 系统 Keychain（macOS）、DPAPI（Windows）、Secret Service（Linux） |
| 进程隔离 | `bash`/`edit`/`write` 工具对宿主有完全权限 | 走 sandbox 路由；UI 显示安全指示 |
| 模型流量 | LLM 调用走 OpenAI/Anthropic 等云 API | 模型 API key 不进 sandbox；走本机代理（参见 v5-integration.md） |
| 资源 | 长会话累积 token → 上下文爆炸 | 用 Pi 的 `harness/compaction`（`compact` / `findCutPoint` / `generateSummary`） |

---

## 2. 系统组件与职责

```
┌─────────────────────────────────────────────────────────────┐
│                    App Shell（前端 / UI 层）                  │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────────────┐  │
│  │  Chat Panel  │  │  Files Tree  │  │  Tool Result View  │  │
│  │  (流式展示)   │  │  (Diff View) │  │  (Shell/Edit/Render)│  │
│  └──────────────┘  └──────────────┘  └────────────────────┘  │
│             ▲                  ▲                  ▲          │
│             └────── Event Bus / JSON-RPC WS ─────┘          │
└───────────────────────┬─────────────────────────────────────┘
                        │  stdio JSONL (M1)
                        │  或 Unix Socket + CBOR (M2+)
┌───────────────────────▼─────────────────────────────────────┐
│              In-App Agent Bridge（嵌入式桥）                │
│  ┌──────────────────────────┐  ┌────────────────────────┐    │
│  │  Pi Process Manager       │  │  Sandbox Router         │   │
│  │  - spawn pi --mode rpc    │  │  - 工具执行路由         │   │
│  │  - 健康探针 / 重启        │  │  - Gondolin/Docker hook │   │
│  │  - 流式 JSONL 解析        │  │  - 权限策略            │   │
│  └──────────────────────────┘  └────────────────────────┘    │
│  ┌──────────────────────────┐  ┌────────────────────────┐    │
│  │  Session Store (SQLite)   │  │  Credential Vault       │   │
│  │  - 会话元数据 + 消息      │  │  - 接入系统 Keychain    │   │
│  │  - 断线恢复              │  │  - token 注入 pi 子进程 │   │
│  └──────────────────────────┘  └────────────────────────┘    │
│  ┌──────────────────────────┐  ┌────────────────────────┐    │
│  │  Cache Layer              │  │  Telemetry/Logs         │   │
│  │  - LLM 响应缓存           │  │  - pi-telemetry schemas │   │
│  │  - 模型目录缓存           │  │  - 本地 opentelemetry   │   │
│  │  - 工具结果缓存           │  │                        │   │
│  └──────────────────────────┘  └────────────────────────┘    │
└───────────────────────┬─────────────────────────────────────┘
                        │  stdin / control line
┌───────────────────────▼─────────────────────────────────────┐
│         Pi 子进程：@earendil-works/pi-coding-agent           │
│  ┌────────────────┐  ┌────────────────┐  ┌───────────────┐  │
│  │  pi-ai         │  │  pi-agent-core │  │  pi-server    │  │
│  │  多 Provider   │  │  Agent/Harness │  │  (M2+,可选)   │  │
│  └────────────────┘  └────────────────┘  └───────────────┘  │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  Tools: bash / read / edit / write / grep / find / ls│   │
│  │  （可经 BashOperations 钩到 sandbox）                  │   │
│  └──────────────────────────────────────────────────────┘   │
└───────────────────────┬─────────────────────────────────────┘
                        │  经 sandbox 路由
┌───────────────────────▼─────────────────────────────────────┐
│           Sandbox Backends（可插拔）                         │
│  • Gondolin（micro-VM，默认推荐，Pi 已自带扩展）             │
│  • Docker（最简单，但需要把凭据传进容器）                     │
│  • OpenShell（策略化，适合企业部署）                         │
│  • Local（无隔离，信任模式，开发态）                         │
└─────────────────────────────────────────────────────────────┘
```

### 2.1 关键组件职责矩阵

| 组件 | 职责 | 不该做的事 |
|------|------|-----------|
| **App Shell** | UI、路由、事件展示、用户输入 | 不直连 LLM；不持久化会话 |
| **Agent Bridge** | 进程管理、协议编解码、Sandbox 路由、Cache、凭据 | 不解析模型输出；不做 UI |
| **Pi 子进程** | LLM 调用、Agent 推理循环、工具调用执行 | 不持有长期凭据（按需注入） |
| **Session Store** | 会话/消息持久化、断线恢复 | 不参与运行时状态 |
| **Sandbox Router** | 把 `bash`/`edit`/`write` 等危险工具"代理"到隔离边界 | 不解析语义 |
| **Cache** | 减少 token 消耗 & 加速 | 不存敏感凭据 |
| **Credential Vault** | OS 级安全存储，进程注入时注入 | 不打日志 |

---

## 3. 数据流

### 3.1 主链路：用户发问到工具执行

```
[User]  ──text──▶  App Shell
                       │
                       │ (JSON-RPC over WS / stdio)
                       ▼
              Agent Bridge
        ┌──────────┼────────────────┐
        │          │                │
        ▼          ▼                ▼
   Session Store  Cache         Pi 子进程 (stdin JSONL)
   (SQLite)       Lookup       {"type":"prompt","text":...}
                                   │
                                   │ (内部 Agent 循环)
                                   ▼
                              LLM Provider API
                                   │
                                   │ (流式 events)
                                   ▼
                              Pi 子进程 (stdout JSONL)
                                   │ 解析 + 关联 sessionId
                                   ▼
                          Agent Bridge
                                   │
                                   ▼
                          App Shell (流式 UI)
                                   │
                       ┌───────────┼────────────┐
                       ▼           ▼            ▼
                message_update  tool_call   tool_result
                (text_delta)    (bash)      (output)
```

### 3.2 工具执行（本地强化）

```
Pi Agent ── toolCall(bash) ──▶ Pi 内部 Bash Tool
                                    │
                                    │ BashOperations.execute() 钩子
                                    ▼
                            Sandbox Router
                          ┌─────┴───────┐
                          ▼             ▼             ▼
                   Gondolin VM     Docker        Local
                   (默认推荐)      (fallback)    (dev only)
                          │
                          ▼
                   真实 bash / read / write
                          │
                          ▼
                   截断 / 流式 / 渲染 (OutputAccumulator)
                          │
                          ▼
                   toolResult 回 Pi Agent
```

### 3.3 跨进程生命周期

```
App 启动
   │
   ▼
Bridge: spawn pi --mode rpc
   │
   ├─ 健康探针（每 5s 探 stdin 心跳 / stdout ping）
   │
   ├─ 异常退出 → 自动重启（指数退避，最大 3 次）
   │
   ▼
App 退出 → graceful shutdown：
   1. 发送 {"type":"exit"} → pi
   2. 等 ≤2s
   3. SIGTERM
   4. 5s 后 SIGKILL
```

---

## 4. API 设计

### 4.1 App ↔ Bridge（前端 ↔ 桥接层）

> 选用 **JSON-RPC 2.0 over WebSocket**（便于后续跨设备）；也支持 IPC fallback。

| 方法 | 方向 | 用途 |
|------|------|------|
| `session.create` | C→S | 新建会话（含 cwd、模型、扩展配置） |
| `session.list` | C→S | 列出本地所有会话（分页） |
| `session.open` | C→S | 恢复会话；订阅流 |
| `session.close` | C→S | 关闭并 flush |
| `session.delete` | C→S | 物理删除（含审计日志保留） |
| `prompt.send` | C→S | 推一条 user message |
| `prompt.abort` | C→S | 中断当前 LLM 流 |
| `tool.confirm` | C→S | 响应工具执行确认弹窗 |
| `agent.event` | S→C | 流式事件（message_update、tool_call、tool_result） |
| `agent.status` | S→C | idle / thinking / tool_running / error |
| `model.list` | C→S | 当前可用模型 |
| `model.set` | C→S | 切换会话模型 |

事件载荷（**关键**：尽量复用 Pi 现有事件形状以减少映射）：

```ts
type AgentEvent =
  | { type: "message_update"; sessionId: string; delta: TextDelta | ToolCallDelta }
  | { type: "message_end";   sessionId: string; message: AgentMessage }
  | { type: "tool_call";     sessionId: string; id: string; name: string; args: unknown }
  | { type: "tool_result";   sessionId: string; id: string; output: ToolOutput; status: "ok" | "error" }
  | { type: "turn_start" | "turn_end"; sessionId: string; usage?: Usage }
  | { type: "agent_error";   sessionId: string; error: { code: string; message: string } }
  | { type: "agent_status";  sessionId: string; status: AgentStatus };
```

### 4.2 Bridge ↔ Pi 子进程（JSONL on stdio）

> 直接消费 `packages/coding-agent/src/modes/rpc/rpc-types.ts` 已有 schema；**不重新发明协议**。

```
← stdout（Pi → Bridge）
{ "type":"response", "command":"prompt", "success":true, "data": {...} }
{ "type":"event", "event": { "type":"message_update", ... } }
{ "type":"extension_ui_request", ... }

→ stdin（Bridge → Pi）
{ "type":"prompt", "id":"req-1", "text":"..." }
{ "type":"set_model", "provider":"anthropic", "model":"claude-sonnet-4-6" }
{ "type":"abort" }
{ "type":"exit" }
```

**权衡**：直接复用 Pi 现有 schema 而不是自定义抽象层 → 零协议维护成本，但需在 Bridge 中**严格做 schema 校验**（推荐 ajv + zod 二选一），避免 Pi 升级带来 breaking change。

### 4.3 Bridge ↔ Sandbox

内部接口，不走网络。设计为 **策略表**：

```ts
interface SandboxPolicy {
  mode: "local" | "docker" | "gondolin" | "openshell";
  allowedPaths: string[];     // 工具可触达路径白名单
  deniedPaths: string[];      // 黑名单（如 ~/.ssh）
  networkPolicy: "none" | "egress-allowlist" | "full";
  timeoutMs: number;
  envInjects: Record<string, string>;
}
```

---

## 5. 数据存储方案

### 5.1 存储分层

| 数据 | 存储 | 加密 | 保留策略 |
|------|------|------|---------|
| 会话元数据 + 消息流 | **SQLite**（`pi-session-backend-sqlite-node` 已就绪；自托管用 better-sqlite3 / node:sqlite） | 应用级加密（SQLCipher 可选） | 用户删除即删；30 天后归档提示 |
| 工具输出大文件（截断部分） | 文件系统 `${DATA_DIR}/blobs/<sha256>.bin` | AES-GCM 静态加密 | 默认 90 天 |
| LLM 凭据 | **OS Keychain**（macOS）/ DPAPI（Win）/ Secret Service（Linux） | OS 管理 | 用户显式撤销 |
| 模型目录 | 内存 + 启动时从 `pi-ai` `createModels()` hydrate，落盘 `models.json` | 否 | 7 天 |
| 缓存（响应/工具） | `${CACHE_DIR}/llm/` + `${CACHE_DIR}/tools/` | 否（仅缓存去标识化结果） | 14 天 LRU |
| 审计日志 | SQLite 独立表 `audit_log` | 否（脱敏） | 180 天 |
| 遥测 | 本地 OTel exporter → 文件 + 可选 OTLP | 否 | 30 天 |

### 5.2 Schema（关键表）

```sql
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,            -- uuidv7
  title TEXT,
  cwd TEXT NOT NULL,
  model_provider TEXT,
  model_id TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  parent_session TEXT,
  status TEXT CHECK(status IN ('active','archived','deleted')) DEFAULT 'active',
  metadata_json TEXT
);

CREATE TABLE messages (
  id TEXT PRIMARY KEY,            -- uuidv7
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  role TEXT CHECK(role IN ('user','assistant','toolResult','system')),
  ordinal INTEGER NOT NULL,       -- 会话内顺序
  content_json TEXT NOT NULL,     -- AgentMessage JSON
  usage_json TEXT,
  created_at INTEGER NOT NULL,
  UNIQUE(session_id, ordinal)
);
CREATE INDEX idx_messages_session ON messages(session_id, ordinal);

CREATE TABLE tool_invocations (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  message_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  args_hash TEXT,                 -- sha256(args) 用于缓存去重
  args_json TEXT,
  output_ref TEXT,                -- blob path
  status TEXT,
  started_at INTEGER,
  finished_at INTEGER,
  exit_code INTEGER
);

CREATE TABLE audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  actor TEXT,                     -- 'user' / 'agent' / 'system'
  action TEXT NOT NULL,           -- 'tool.exec' / 'session.create' / 'llm.call'
  session_id TEXT,
  detail_json TEXT,               -- 脱敏
  sandbox_mode TEXT
);

CREATE TABLE llm_call_cache (
  cache_key TEXT PRIMARY KEY,     -- sha256(model + system + msgs-tail + tools-fingerprint)
  response_json BLOB NOT NULL,
  usage_json TEXT,
  created_at INTEGER NOT NULL,
  ttl INTEGER NOT NULL
);
CREATE INDEX idx_llm_cache_ttl ON llm_call_cache(created_at, ttl);
```

### 5.3 选型权衡

| 选择 | 替代 | 取舍 |
|------|------|------|
| **SQLite 单库** | Postgres / LevelDB | SQLite 零运维；端上单进程；并发读足够；M2+ 如需多端点共用，可上 SQLite WAL + 远程挂载或换 Postgres |
| **应用级加密** | 仅 OS 全盘加密 | 防止设备被拔走后访问；性能损耗 ~10-15%，可接受 |
| **凭据走 OS Keychain** | 自建 vault | 复用 OS 安全模型；跨平台需适配层 |

---

## 6. 缓存策略

### 6.1 三层缓存

```
┌────────────────────────────────────────────┐
│ L1：内存 LRU（命中即返回）                    │
│  - 当前会话最近 50 条消息哈希                  │
│  - 当前会话最近 100 个工具结果                 │
│  - 模型目录、Provider 配置                    │
├────────────────────────────────────────────┤
│ L2：磁盘 SQLite（跨重启、跨会话）             │
│  - llm_call_cache 表（精确请求去重）          │
│  - tool_invocations.output 复用（同 hash）    │
├────────────────────────────────────────────┤
│ L3：Provider-side prompt cache（外部）       │
│  - Anthropic cache_control / OpenAI prompt_cache_key │
│  - 桥接层透传，不自己做                       │
└────────────────────────────────────────────┘
```

### 6.2 缓存键设计

- **LLM 响应缓存键**：`sha256( model_id || system_prompt_hash || last-20-msgs-hash || tools-fingerprint )`
  - 只缓存 **temperature=0** 或显式 deterministic 请求
  - 不缓存含图片、文件引用等大体积输入
  - 默认 TTL 24h；用户级可配
- **工具结果缓存键**：`sha256( tool_name || args_hash || cwd-fingerprint || sandbox-policy-hash )`
  - **必须**包含 cwd 和策略哈希，否则跨项目污染
  - 默认 TTL 7 天

### 6.3 失效策略

- **会话删除** → 关联缓存条目 GC
- **工具版本变更**（如升级 Pi） → 整库失效 `tools` 前缀
- **模型目录刷新** → 失效 `models:*` 键
- **手工按钮**：UI 提供"清空缓存"操作（带确认 + 影响说明）

### 6.4 权衡

| 决策 | 替代 | 取舍 |
|------|------|------|
| **精确键缓存** | 语义缓存（embedding 相似度） | 简单、可解释；命中率低；对长上下文应用足够 |
| **Provider 缓存透传** | 自建缓存网关 | 避免重复造轮子；不同 Provider 行为需适配 |
| **磁盘 SQLite 缓存** | Redis | 端上零运维；性能足够 |

---

## 7. 故障处理与降级

### 7.1 故障分类与策略

| 故障 | 检测 | 应对 | 降级 |
|------|------|------|------|
| **Pi 子进程崩溃** | stdio EOF / 心跳超时 | 自动重启（指数退避），会话状态由 Bridge 维护 | 最多 3 次失败 → UI 显示"agent offline" + 离线操作（只读） |
| **LLM Provider 429/5xx** | HTTP 状态码 | 内置重试 3 次 + jitter；尊重 Retry-After | 切换备用 Provider（如 Anthropic 不可用 → OpenAI）；最终切到本地模型（Ollama 兼容） |
| **LLM Provider 鉴权失败** | 401/403 | 引导用户重新走 OAuth/API Key 流程 | 阻止后续 prompt，保留会话可读 |
| **Sandbox 不可用** | Gondolin 退出 / Docker daemon down | 切换到下一个 backend（依策略表优先级） | 全部失败 → 阻止危险工具、仅允许只读 `read`/`grep`/`find`/`ls` |
| **磁盘满** | ENOSPC | 阻断大 blob 写入；触发 LRU | 提示用户清理，禁用缓存写入 |
| **SQLite 锁** | SQLITE_BUSY | 增加 busy_timeout 到 5s；启用 WAL | 极端情况切回内存（仅当 session 关闭时） |
| **断网** | 网络探针 | 切到本地模型（若配置） | 提示用户并保存待发 |
| **上下文超限** | Pi `shouldCompact()` | 调 `pi-agent-core/harness/compaction.compact()` | 用户级阈值可配；失败时强制截断旧消息 |
| **内存泄漏** | heap 监控 | 重启子进程（保留会话） | 提示用户导出日志 |

### 7.2 Sandbox 策略的纵深防御

```
Layer 1: OS 用户权限（pi 子进程用低权账户运行）
Layer 2: Sandbox 隔离（Gondolin / Docker）
Layer 3: 工具白名单 + 路径白名单（Bridge 层）
Layer 4: 用户交互确认（危险操作弹窗）
Layer 5: 审计日志（事后追查）
```

### 7.3 优雅降级矩阵

| 能力 | 完整 | 部分降级 | 最低 |
|------|------|---------|------|
| LLM 调用 | 云端主 Provider | 备用 Provider | 本地模型 |
| 文件写入 | Sandbox + 确认 | Sandbox 无确认 | 仅允许特定目录 |
| Bash | Sandbox | 受限命令白名单（如禁用 `rm -rf`） | 禁用 bash |
| 跨设备同步 | 全量 | 仅会话列表 | 无 |

### 7.4 观测性

- **Telemetry**：`pi-telemetry` 的 schema 直接落入本地 OTel exporter
- **关键 Span**：`bridge.session.create`、`bridge.pi.spawn`、`bridge.tool.exec`、`bridge.llm.call`
- **关键指标**：活跃会话数、P95 prompt-to-first-token、工具失败率、缓存命中率、Sandbox 启动 P95
- **用户可见状态**：App 内固定位置显示"agent 状态徽章"（idle/thinking/tool running/error）

---

## 8. 最小功能实现（M1）

> 目标：**2 周内**跑通"App 内问一句 → 智能体执行 bash → 看到结果"。

### 8.1 M1 范围

- [ ] Bridge 进程：拉起 `pi --mode rpc`，解析 JSONL
- [ ] SQLite 会话存储（messages + sessions 两表）
- [ ] UI：一个聊天面板 + 工具结果折叠面板 + 流式渲染
- [ ] 工具：全部内置工具（Gondolin 沙箱**未启用**，先跑通 Local 模式）
- [ ] Provider：单一云 Provider（MVP 选 Anthropic 或 OpenAI）
- [ ] 凭据：OS Keychain 接入 + 启动时注入
- [ ] 缓存：仅 L2 磁盘缓存（命中即跳过 LLM）
- [ ] 故障：子进程崩溃自动重启 + 限流重试
- [ ] 观测：最小 OTel span + console 日志

### 8.2 M2（强化本地执行）

- [ ] 接入 Gondolin sandbox（Pi 自带扩展，照搬 `examples/extensions/gondolin`）
- [ ] 工具路径白名单 + 网络策略
- [ ] 危险工具交互确认
- [ ] 工具结果缓存 + L1 内存缓存
- [ ] 备用 Provider 自动切换

### 8.3 M3（多端点 / 可扩展）

- [ ] 切换到 `@earendil-works/pi-server`（实验性，注意稳定性）
- [ ] session-worker 子进程模型（按会话隔离）
- [ ] 多端点：手机端只读订阅
- [ ] Docker Sandboxes 作为 Gondolin 替代（适合 Linux Server 部署）
- [ ] Plugin 系统暴露（chord facet 服务）

### 8.4 M1 工程里程碑

```
W1D1-2  Bridge 骨架 + stdio JSONL 解析（已可对照 packages/coding-agent/src/modes/rpc/）
W1D3-4  Pi 子进程管理 + 心跳 + 重启
W1D5    SQLite 接入 + 会话/消息持久化
W2D1-2  UI 聊天面板 + 流式
W2D3    工具结果展示（折叠 + 高亮）
W2D4    凭据 Keychain + 模型选择
W2D5    端到端冒烟 + 缓存 + 观测
```

---

## 9. 重要决策与权衡（汇总）

### 9.1 嵌入形态：RPC vs Pi-Server

| | RPC 模式（M1） | Pi-Server（M3） |
|---|---|---|
| **进程模型** | 单子进程 | server + 多 session-worker |
| **传输** | stdio JSONL | Unix Socket + CBOR |
| **稳定性** | 稳定（CLI 模式之一） | **实验性**（README 自标） |
| **扩展性** | 单用户单端点 | 多端点 + 会话隔离 |
| **复杂度** | 低 | 中-高 |
| **决策** | ✅ M1 起步 | ✅ M3 评估时再切 |

**为何不直接上 server**：实验性接口的协议稳定性未保证，强行进入会让我们背上"协议抽象层"的额外维护负担。RPC 模式已被 CLI 客户（VS Code 插件等）广泛使用，路径成熟。

### 9.2 Sandbox 选择：Gondolin > Docker > OpenShell

- **Gondolin**：本地 micro-VM，**Pi 已自带扩展**（`packages/coding-agent/examples/extensions/gondolin`），最快落地。需要 QEMU + Node ≥23.6。
- **Docker**：最简单但**凭据需进容器**，违背"凭据不出端"原则。
- **OpenShell**：策略化强但要外部网关，部署重。
- **本地模式（M1 临时）**：完全无隔离，便于开发；UI 必须显著标识。

### 9.3 协议层：复用 vs 抽象

- **不**自建抽象层（避免协议漂移）
- **直接消费** Pi 的 JSONL 事件 → 在 Bridge 做严格校验（zod）
- 升级 Pi 时只动 Bridge 一处 schema 适配

### 9.4 数据存储：SQLite 起步

- 单进程 + 单用户场景下 SQLite 是 sweet spot
- 应用级加密（SQLCipher）补足设备被盗风险
- 未来如要多端共享会话 → 再迁 Postgres

### 9.5 缓存：精确键而非语义

- 实现成本低、可观测、可调试
- 命中率虽不如 embedding 相似度，但对长上下文 + 重复工具调用足够

### 9.6 安全：纵深防御而非单点

- OS 用户权限 + Sandbox + 白名单 + 确认弹窗 + 审计
- 任一层失守不立即酿成事故

### 9.7 进程隔离 vs 线程

- 选用 **OS 进程隔离**（Pi 默认）：崩溃不影响 App；沙箱友好
- 不用 Worker Threads：共享堆削弱了隔离性

### 9.8 不做什么（Anti-Goals）

- ❌ 不做"语义缓存"（成本/复杂度 vs 收益不划算）
- ❌ 不做"智能路由"（多 Provider 同时问再选最优）—— 留给 M4+
- ❌ 不做"跨设备会话同步"（M1 不需要；M3 再议）
- ❌ 不自研协议层（违背"复用 Pi"原则）

---

## 10. 验证 / 验收清单

M1 验收必须覆盖：

1. ✅ App 内输入 → 3 秒内首个 token 出现
2. ✅ 工具调用（bash `ls`）可在 UI 看到结果
3. ✅ 重启 Pi 子进程，会话可恢复（断电模拟）
4. ✅ LLM 故障 → 自动重试 → 备用 Provider 切换
5. ✅ Sandbox 关闭 → 工具被阻止 + UI 显著提示
6. ✅ 缓存命中：相同请求二次发起零 token 消耗
7. ✅ 凭据不在日志/磁盘明文
8. ✅ 100 次会话压测无 SQLite 锁

---

## 11. 风险登记

| 风险 | 等级 | 触发条件 | 缓解 |
|------|------|---------|------|
| Pi 接口实验性变更 | 中 | 升级到下一个 minor | 固定 Pi 版本；Bridge 处做 schema 校验与版本协商 |
| Gondolin 跨平台 | 中 | Windows 端用户体验差 | Linux/macOS 主用 Gondolin；Windows 退化 Docker Sandboxes |
| 上下文爆涨 | 中 | 超长会话 | 启用 `pi-agent-core` 自带 compaction；强制阈值 |
| LLM 成本失控 | 中 | 用户高频调用 | token 用量实时显示 + 限速开关 |
| 凭据泄露 | 高 | 任何代码路径明文存 | 自动化扫描 + review 流程 |

---

## 12. 参考

- Pi 仓库：`~/workspace/ai/pi`
- 关键源码：
  - `packages/coding-agent/src/modes/rpc/` — RPC 协议定义
  - `packages/coding-agent/src/core/tools/` — 内置工具实现
  - `packages/coding-agent/docs/containerization.md` — 沙箱方案
  - `packages/server/` — 长期形态（M3+）
  - `packages/agent/src/harness/compaction/` — 上下文压缩
  - `packages/ai/` — 多 Provider LLM 层
- 本仓既有文档：
  - `docs/v5-integration.md`
  - `docs/2026-08-27-ai-chat-and-gateway-management.md`
  - `docs/2026-08-28-multi-agent-workbench-design.md`

---

**作者**：ZCode 系统架构师角色
**变更日志**：
- v1（2026-09-08）首版。基于代码评审与既有约束的方案稿。