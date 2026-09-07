> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/新架构v1/03-roadmap/接口规范.md`](docs/新架构v1/03-roadmap/接口规范.md)
> Do NOT use this doc for current implementation decisions.
> （补横幅：SUPERSEDED.md Group 1 已登记）

# OpenCode 实际 API 分析报告

## 分析时间
2026-07-02

## 源码位置
`~/workspace/ai/opencode`

## 关键发现

### 1. HTTP 服务器架构

OpenCode 使用 **Effect HTTP** 框架构建 HTTP API 服务器：

- **主服务器文件**: `packages/opencode/src/server/server.ts`
- **路由定义**: `packages/server/src/routes.ts`
- **API 组定义**: `packages/server/src/groups/*.ts`
- **框架**: Effect HTTP (effect/unstable/http)

### 2. 实际暴露的 Session API 端点

根据 `packages/server/src/groups/session.ts` 的分析，OpenCode 实际暴露以下 Session 相关端点：

#### 2.1 列出 Sessions
```
GET /api/session
Query Parameters:
  - workspace: WorkspaceID (optional)
  - directory: AbsolutePath (optional)  
  - project: ProjectID (optional)
  - subpath: RelativePath (optional)
  - limit: PositiveInt (optional, max 50 by default)
  - order: "asc" | "desc" (optional)
  - search: string (optional)
  - cursor: SessionsCursor (optional, for pagination)

Response:
{
  "data": [SessionInfo],
  "cursor": {
    "previous": string (optional),
    "next": string (optional)
  }
}
```

#### 2.2 创建 Session
```
POST /api/session
Payload:
{
  "id": SessionID (optional),
  "agent": AgentID (optional),
  "model": ModelRef (optional),
  "location": LocationRef (optional)
}

Response:
{
  "data": SessionInfo
}
```

#### 2.3 获取单个 Session
```
GET /api/session/:sessionID

Response:
{
  "data": SessionInfo
}
```

#### 2.4 发送 Prompt
```
POST /api/session/:sessionID/prompt
Payload:
{
  "id": MessageID (optional),
  "prompt": Prompt,
  "delivery": "steer" | "immediate" | "background" (optional),
  "resume": boolean (optional)
}

Response:
{
  "data": SessionInputAdmitted
}
```

#### 2.5 获取 Session 上下文
```
GET /api/session/:sessionID/context

Response:
{
  "data": [Message]
}
```

#### 2.6 获取 Session 消息
```
GET /api/session/:sessionID/message
Query Parameters:
  - limit: number (optional, 1-200)
  - order: "asc" | "desc" (optional)
  - cursor: string (optional)

Response:
{
  "data": [Message],
  "cursor": {
    "previous": string (optional),
    "next": string (optional)
  }
}
```

#### 2.7 其他操作
```
POST /api/session/:sessionID/compact
POST /api/session/:sessionID/wait
```

### 3. Session 数据模型

根据 `packages/core/src/session/schema.ts`：

```typescript
class SessionInfo {
  id: SessionID                    // "ses_xxxxx"
  parentID?: SessionID             // 父 session
  projectID: ProjectID             // 项目 ID
  agent?: AgentID                  // Agent ID
  model?: ModelRef                 // 模型引用
  cost: number                     // 成本
  tokens: {
    input: number
    output: number
    reasoning: number
    cache: {
      read: number
      write: number
    }
  }
  time: {
    created: DateTime              // 创建时间
    updated: DateTime              // 更新时间
    archived?: DateTime            // 归档时间
  }
  title: string                    // 标题
  location: LocationRef            // 位置引用
  subpath?: RelativePath           // 子路径
}
```

### 4. Health Check 端点

```
GET /api/health

Response:
{
  "healthy": true
}
```

### 5. 其他可用端点

根据 `packages/server/src/groups/` 分析：

- **Agent 管理**: `GET /api/agent`
- **Model 管理**: `GET /api/model`
- **Provider 管理**: `GET /api/provider`, `GET /api/provider/:providerID`
- **文件系统**: `GET /api/fs/read/*`, `GET /api/fs/list`, `GET /api/fs/find`
- **事件订阅**: `GET /api/event` (WebSocket/SSE)
- **权限管理**: `/api/permission/*`
- **PTY 管理**: `GET /api/pty`, `POST /api/pty`
- **集成管理**: `/api/integration/*`

## 关键差异分析

### 我们之前的假设 vs 实际情况

| 假设的端点 | 实际端点 | 状态 |
|----------|---------|------|
| `GET /session` | `GET /api/session` | ✅ 存在，但路径有 `/api` 前缀 |
| `GET /session/{id}` | `GET /api/session/:sessionID` | ✅ 存在 |
| `GET /session/{id}/summarize` | ❌ 不存在 | ⚠️ 需要使用 `/api/session/:sessionID/context` |
| `GET /session/{id}/history` | `GET /api/session/:sessionID/message` | ✅ 类似功能 |
| `POST /session/{id}/interrupt` | ❌ 未在 HTTP API 中暴露 | ⚠️ 可能通过其他方式 |

### 关键问题

1. **没有直接的"摘要"端点**: 
   - 原假设有 `/summarize` 端点
   - 实际需要通过 `/context` 或 `/message` 获取数据后自己处理

2. **响应格式统一**:
   - 所有成功响应都包装在 `{ "data": ... }` 中
   - 分页响应还包含 `cursor` 对象

3. **没有"状态"字段**:
   - SessionInfo 中没有直接的 `status: "active" | "idle"` 字段
   - 需要通过其他方式判断 session 是否活跃

4. **没有代码变更统计**:
   - SessionInfo 中没有 `additions`、`deletions`、`files_changed` 字段
   - 这些需要通过分析消息历史来计算

## 发现服务器的方式

OpenCode 支持通过 **mDNS** (Multicast DNS) 进行服务发现：

```typescript
// packages/core/src/v1/config/server.ts
{
  port?: number,
  hostname?: string,
  mdns?: boolean,              // 启用 mDNS 服务发现
  mdnsDomain?: string,         // 自定义域名（默认: opencode.local）
  cors?: string[]              // CORS 允许的域名
}
```

相关实现在 `packages/opencode/src/server/mdns.ts`。

## 适配器需要修改的地方

### 1. 基础 URL 路径
所有端点需要加 `/api` 前缀。

### 2. 响应解析
需要从 `{ "data": ... }` 中提取实际数据。

### 3. Session 状态判断
由于没有直接的状态字段，需要：
- 通过 `time.updated` 判断活跃度
- 或者通过查询当前是否有正在执行的任务

### 4. 代码变更统计
需要：
- 获取 session 的消息历史
- 分析消息中的文件操作
- 计算代码变更统计

### 5. Health Check
使用 `GET /api/health` 端点。

### 6. 实例发现
可以利用 mDNS 进行自动发现，或者通过配置文件指定实例地址。

## 下一步行动

1. ✅ 完成 API 分析
2. ⏳ 修改 `backend/internal/opencode/client.go` 适配真实 API
3. ⏳ 修改 `backend/internal/opencode/manager.go` 处理真实数据格式
4. ⏳ 实现 mDNS 服务发现（可选）
5. ⏳ 本地启动 OpenCode 实例进行测试
6. ⏳ 验证完整的集成流程

## 参考文件

- `~/workspace/ai/opencode/packages/core/src/session/schema.ts` - Session 数据模型
- `~/workspace/ai/opencode/packages/core/src/public/session.ts` - Public Session API
- `~/workspace/ai/opencode/packages/server/src/groups/session.ts` - HTTP Session 端点
- `~/workspace/ai/opencode/packages/server/src/groups/message.ts` - HTTP Message 端点
- `~/workspace/ai/opencode/packages/server/src/groups/health.ts` - Health Check
- `~/workspace/ai/opencode/packages/opencode/src/server/server.ts` - 服务器主文件
