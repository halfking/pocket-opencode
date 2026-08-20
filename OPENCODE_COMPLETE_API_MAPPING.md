# OpenCode 完整 API 能力映射

## 基于源码分析的完整 API 清单

根据对 `~/workspace/ai/opencode/packages/server/src/groups/*.ts` 的分析，以下是 OpenCode 实际暴露的所有 HTTP API 端点。

---

## 1. Session 管理 API

### 1.1 会话列表
```http
GET /api/session
Query: workspace, directory, project, subpath, limit, order, search, cursor
Response: { "data": [SessionInfo], "cursor": { "previous", "next" } }
```

### 1.2 创建会话
```http
POST /api/session
Body: { "id"?, "agent"?, "model"?, "location"? }
Response: { "data": SessionInfo }
```

### 1.3 获取会话详情
```http
GET /api/session/:sessionID
Response: { "data": SessionInfo }
```

### 1.4 发送 Prompt ⭐⭐⭐
```http
POST /api/session/:sessionID/prompt
Body: {
  "id"?: string,           // Message ID
  "prompt": Prompt,        // 用户输入
  "delivery"?: string,     // "steer" | "immediate" | "background"
  "resume"?: boolean       // 是否自动执行
}
Response: { "data": SessionInputAdmitted }
```

### 1.5 压缩会话
```http
POST /api/session/:sessionID/compact
Response: 204 No Content
```

### 1.6 等待执行完成
```http
POST /api/session/:sessionID/wait
Response: 204 No Content
```

### 1.7 获取会话上下文
```http
GET /api/session/:sessionID/context
Response: { "data": [Message] }
```

### 1.8 删除会话 ⭐
```http
DELETE /api/session/:sessionID
(通过 CLI 实现: opencode session delete <sessionID>)
```

---

## 2. 消息管理 API

### 2.1 获取消息列表
```http
GET /api/session/:sessionID/message
Query: limit, order, cursor
Response: {
  "data": [Message],
  "cursor": { "previous", "next" }
}
```

---

## 3. 权限管理 API ⭐⭐⭐

### 3.1 列出待审批的权限请求（全局）
```http
GET /api/permission/request
Query: directory?, workspaceID?
Response: { "data": [PermissionRequest], "location": LocationInfo }
```

### 3.2 列出会话的权限请求
```http
GET /api/session/:sessionID/permission
Response: { "data": [PermissionRequest] }
```

### 3.3 回复权限请求 ⭐⭐⭐
```http
POST /api/session/:sessionID/permission/:requestID/reply
Body: {
  "reply": PermissionReply,  // "allow" | "deny" | "allow-once" | "allow-session" | ...
  "message"?: string
}
Response: 204 No Content
```

### 3.4 列出已保存的权限
```http
GET /api/permission/saved
Query: projectID?
Response: { "data": [PermissionSavedInfo] }
```

### 3.5 删除已保存的权限
```http
DELETE /api/permission/saved/:id
Response: 204 No Content
```

---

## 4. 问答交互 API ⭐⭐⭐

### 4.1 列出待回答的问题（全局）
```http
GET /api/question/request
Query: directory?, workspaceID?
Response: { "data": [QuestionRequest], "location": LocationInfo }
```

### 4.2 列出会话的问题
```http
GET /api/session/:sessionID/question
Response: { "data": [QuestionRequest] }
```

### 4.3 回答问题 ⭐⭐⭐
```http
POST /api/session/:sessionID/question/:requestID/reply
Body: QuestionReply  // 具体格式取决于问题类型
Response: 204 No Content
```

### 4.4 拒绝回答问题
```http
POST /api/session/:sessionID/question/:requestID/reject
Response: 204 No Content
```

---

## 5. 事件流 API ⭐⭐⭐

### 5.1 订阅事件流（Server-Sent Events）
```http
GET /api/event
Query: directory?, workspaceID?
Response: text/event-stream
```

**事件类型**：
- `session.created`
- `session.updated`
- `message.added`
- `permission.requested`
- `question.asked`
- `execution.started`
- `execution.completed`
- `execution.error`
- 等等...

---

## 6. 模型管理 API

### 6.1 列出可用模型
```http
GET /api/model
Response: { "data": [ModelInfo] }
```

### 6.2 切换会话模型 ⭐
```http
POST /api/session/:sessionID/switch-model
(需要查看是否有专门的端点，或通过 prompt 实现)
```

---

## 7. Provider 管理 API

### 7.1 列出 Providers
```http
GET /api/provider
Response: { "data": [ProviderInfo] }
```

### 7.2 获取 Provider 详情
```http
GET /api/provider/:providerID
Response: { "data": ProviderInfo }
```

---

## 8. Agent 管理 API

### 8.1 列出 Agents
```http
GET /api/agent
Response: { "data": [AgentInfo] }
```

---

## 9. PTY（伪终端）管理 API

### 9.1 列出 PTY 会话
```http
GET /api/pty
Query: directory?, workspaceID?
Response: { "data": [PtyInfo] }
```

### 9.2 创建 PTY 会话
```http
POST /api/pty
Body: PtyCreateInput
Response: { "data": PtyInfo }
```

### 9.3 获取 PTY 详情
```http
GET /api/pty/:ptyID
Response: { "data": PtyInfo }
```

### 9.4 更新 PTY
```http
PUT /api/pty/:ptyID
Body: PtyUpdateInput
Response: { "data": PtyInfo }
```

### 9.5 连接到 PTY（WebSocket）
```http
GET /api/pty/:ptyID/connect?ticket=<ticket>
Upgrade: websocket
```

---

## 10. 文件系统 API

### 10.1 读取文件
```http
GET /api/fs/read/*
Query: directory?, workspaceID?
Response: 文件内容
```

### 10.2 列出目录
```http
GET /api/fs/list
Query: directory?, workspaceID?, path
Response: { "data": [FileInfo] }
```

### 10.3 查找文件
```http
GET /api/fs/find
Query: directory?, workspaceID?, pattern
Response: { "data": [FilePath] }
```

---

## 11. 凭证管理 API

### 11.1 删除凭证
```http
DELETE /api/credential/:credentialID
Response: 204 No Content
```

---

## 12. 集成管理 API

### 12.1 列出集成
```http
GET /api/integration
Response: { "data": [IntegrationInfo] }
```

### 12.2 获取集成详情
```http
GET /api/integration/:integrationID
Response: { "data": IntegrationInfo }
```

### 12.3 连接集成（Key）
```http
POST /api/integration/:integrationID/connect/key
Body: { "key": string }
Response: { "data": ConnectionInfo }
```

### 12.4 连接集成（OAuth）
```http
POST /api/integration/:integrationID/connect/oauth
Body: OAuth 参数
Response: { "data": ConnectionInfo }
```

---

## 13. 健康检查 API

### 13.1 健康检查
```http
GET /api/health
Response: { "healthy": true }
```

---

## 核心 Session Interface (内部实现)

基于 `packages/core/src/session.ts` 的分析，Session Service 提供以下方法：

```typescript
interface SessionInterface {
  // 已有 HTTP 端点
  list(input?: ListInput): Effect<SessionInfo[]>
  create(input: CreateInput): Effect<SessionInfo>
  get(sessionID: SessionID): Effect<SessionInfo, NotFoundError>
  prompt(input: PromptInput): Effect<SessionInputAdmitted, NotFoundError | PromptConflictError>
  messages(input: MessagesInput): Effect<Message[], NotFoundError | MessageDecodeError>
  message(input: MessageInput): Effect<Message | undefined>
  context(sessionID: SessionID): Effect<Message[], NotFoundError | MessageDecodeError>
  events(input: EventsInput): Stream<Event, NotFoundError>
  
  // 控制方法
  interrupt(sessionID: SessionID): Effect<void>  ⭐⭐⭐
  resume(sessionID: SessionID): Effect<void, NotFoundError | RunError>  ⭐⭐⭐
  wait(id: SessionID): Effect<void, NotFoundError | OperationUnavailableError>
  
  // 配置方法
  switchModel(input: SwitchModelInput): Effect<void, NotFoundError | ...>  ⭐⭐⭐
  switchAgent(input: SwitchAgentInput): Effect<void, OperationUnavailableError>  ⭐
  
  // 其他方法
  compact(input: CompactInput): Effect<void, NotFoundError | OperationUnavailableError>
  shell(input: ShellInput): Effect<void, OperationUnavailableError>
  skill(input: SkillInput): Effect<void, OperationUnavailableError>
}
```

**注意**：某些方法可能没有直接的 HTTP 端点，但可以通过内部服务调用。

---

## 移动端管理系统 - API 使用映射

### 功能需求 → API 映射

| 功能 | OpenCode API | 状态 |
|------|-------------|------|
| **会话管理** | | |
| 查看会话列表 | `GET /api/session` | ✅ 已有 |
| 创建新会话 | `POST /api/session` | ✅ 已有 |
| 删除会话 | CLI: `opencode session delete` | ⚠️ 需适配 |
| 查看会话详情 | `GET /api/session/:sessionID` | ✅ 已有 |
| **会话操作** | | |
| 发送消息/Prompt | `POST /api/session/:sessionID/prompt` | ✅ 已有 ⭐ |
| 中止执行 | `interrupt(sessionID)` | ⚠️ 内部方法 ⭐ |
| 继续执行 | `resume(sessionID)` | ⚠️ 内部方法 ⭐ |
| 等待完成 | `POST /api/session/:sessionID/wait` | ✅ 已有 |
| 压缩会话 | `POST /api/session/:sessionID/compact` | ✅ 已有 |
| **审批与交互** | | |
| 查看权限请求 | `GET /api/session/:sessionID/permission` | ✅ 已有 ⭐ |
| 批准/拒绝权限 | `POST /api/session/:sessionID/permission/:requestID/reply` | ✅ 已有 ⭐ |
| 查看问题 | `GET /api/session/:sessionID/question` | ✅ 已有 ⭐ |
| 回答问题 | `POST /api/session/:sessionID/question/:requestID/reply` | ✅ 已有 ⭐ |
| **配置管理** | | |
| 切换模型 | `switchModel()` | ⚠️ 内部方法 ⭐ |
| 切换 Agent | `switchAgent()` | ⚠️ 内部方法 |
| 列出模型 | `GET /api/model` | ✅ 已有 |
| 列出 Agents | `GET /api/agent` | ✅ 已有 |
| **实时监控** | | |
| 订阅事件 | `GET /api/event` (SSE) | ✅ 已有 ⭐ |
| 实时状态 | 通过 event stream | ✅ 已有 |
| **消息管理** | | |
| 查看消息历史 | `GET /api/session/:sessionID/message` | ✅ 已有 |
| 查看上下文 | `GET /api/session/:sessionID/context` | ✅ 已有 |
| **数据分析** | | |
| 会话摘要 | 需要实现（基于消息） | 🆕 需实现 |
| 轮次分析 | 需要实现（基于消息） | 🆕 需实现 |
| 代码统计 | 数据库已有字段 | ✅ 可用 |
| Token 统计 | SessionInfo 中已有 | ✅ 可用 |

---

## 关键发现

### ✅ 已有完整支持的功能
1. **发送 Prompt** - `POST /api/session/:sessionID/prompt`
2. **权限审批** - `GET/POST /api/session/:sessionID/permission/*`
3. **问答交互** - `GET/POST /api/session/:sessionID/question/*`
4. **事件流** - `GET /api/event` (Server-Sent Events)
5. **消息管理** - `GET /api/session/:sessionID/message`

### ⚠️ 需要适配的功能
1. **中止执行** - `interrupt()` 是内部方法，可能需要：
   - 通过自定义端点包装
   - 或通过数据库直接操作
   
2. **继续执行** - `resume()` 是内部方法
   
3. **切换模型** - `switchModel()` 是内部方法
   
4. **删除会话** - CLI 命令，需要：
   - 通过数据库实现
   - 或调用 CLI

### 🆕 需要新实现的功能
1. **会话摘要生成** - 通过 LLM 分析消息
2. **轮次分析** - 分析消息的问答轮次
3. **导出功能** - 导出会话数据

---

## 实现策略

### 策略 A: 纯 HTTP API（推荐）
对于已有 HTTP 端点的功能，直接调用。

### 策略 B: 混合模式
- 优先使用 HTTP API
- 无 HTTP 端点的功能：
  - 方案 1：通过数据库直接操作（如 interrupt）
  - 方案 2：调用 CLI 命令
  - 方案 3：实现自定义后端 API 包装

### 策略 C: 事件驱动
利用 SSE (`/api/event`) 实现实时推送。

---

## 下一步计划

1. ✅ **验证关键 API** - 启动 OpenCode HTTP 服务器测试
2. ✅ **实现 HTTP 适配器扩展** - 添加所有新发现的 API
3. ✅ **实现事件流管理器** - SSE 订阅和推送
4. ✅ **实现审批管理器** - 权限和问答处理
5. ✅ **实现前端页面** - 基于 Vue 3 + Vant

---

## 参考文档

- OpenCode 源码: `~/workspace/ai/opencode/packages/server/src/groups/*.ts`
- Session Interface: `~/workspace/ai/opencode/packages/core/src/session.ts`
- Permission: `~/workspace/ai/opencode/packages/core/src/permission`
- Question: `~/workspace/ai/opencode/packages/core/src/question`
