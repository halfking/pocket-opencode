> **STATUS: superseded** (2026-08-23)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/新架构v1/04-contracts/pocket-adapter-matrix.md`](../docs/新架构v1/04-contracts/pocket-adapter-matrix.md)
> Do NOT use this doc for current implementation decisions.
>
> This doc claimed "适配器修正总结" at 2026-07-02 with no pinned OpenCode commit. At supersede time, no replacement evidence was captured in `docs/governance/EVIDENCE-LEDGER.md`. See `docs/governance/REVIEW-QUEUE.md` Q-004.

# OpenCode 适配器修正总结

## 修正日期
2026-07-02

## 问题分析

通过分析 `~/workspace/ai/opencode` 的实际源码，发现我们之前的适配器实现基于假设，与真实 API 存在多处不匹配。

## 关键差异

### 1. API 路径前缀
- ❌ **假设**: `/session`, `/session/{id}/summarize`
- ✅ **实际**: `/api/session`, `/api/session/:sessionID`

### 2. 响应格式
- ❌ **假设**: 直接返回数组 `[SessionInfo]`
- ✅ **实际**: 包装格式 `{ "data": [SessionInfo], "cursor": {...} }`

### 3. Session 数据结构
- ❌ **假设**: 包含 `status`, `additions`, `deletions`, `files` 字段
- ✅ **实际**: 
  - 没有直接的 `status` 字段（需通过 `time.updated` 推断）
  - 没有代码变更统计字段（需通过分析消息计算）
  - 完整字段见 `SessionSchema.Info`

### 4. 健康检查端点
- ❌ **假设**: `/healthz` 或 `/session`
- ✅ **实际**: `/api/health` 返回 `{ "healthy": true }`

### 5. 摘要端点
- ❌ **假设**: `/session/{id}/summarize` 返回 `{ "summary": "..." }`
- ✅ **实际**: 不存在此端点，使用 `GET /api/session/:sessionID` 的 `title` 字段

## 已修正的文件

### 1. `backend/internal/adapter/opencode_http.go`

#### 修正内容：
- ✅ 更新 `ListSessions()`: `/session` → `/api/session`
- ✅ 更新 `GetSessionSummary()`: 改用 `/api/session/:sessionID` 获取 title
- ✅ 更新 `ListRemoteTasks()`: 
  - 使用正确的 API 路径
  - 解析 `{ "data": [...] }` 格式
  - 根据 `time.updated` 判断状态
- ✅ 更新 `opencodeSessionInfo` 结构：完整映射真实字段
- ✅ 更新 `parseSessionList()`: 解析新的响应格式
- ✅ 新增 `GetSessionDetail()`: 获取会话详细信息
- ✅ 新增 `GetSessionMessages()`: 获取会话消息列表
- ✅ 新增 `HealthCheck()`: 使用正确的健康检查端点

### 2. `backend/internal/registry/registry.go`

#### 修正内容：
- ✅ 更新 `checkInstanceHealth()`:
  - 使用 `/api/health` 端点
  - 验证响应格式 `{ "healthy": true }`
  - 移除旧的 fallback 端点

## 新增的数据结构

### opencodeSessionInfo (完整版)
```go
type opencodeSessionInfo struct {
    ID        string
    ParentID  *string
    ProjectID string
    Agent     *string
    Model     *struct {
        ID         string
        ProviderID string
        Variant    *string
    }
    Cost   float64
    Tokens struct {
        Input     float64
        Output    float64
        Reasoning float64
        Cache     struct {
            Read  float64
            Write float64
        }
    }
    Time struct {
        Created  int64  // Unix milliseconds
        Updated  int64
        Archived *int64
    }
    Title    string
    Location struct {
        Directory   string
        WorkspaceID *string
    }
    Subpath *string
}
```

### opencodeMessage
```go
type opencodeMessage struct {
    ID   string
    Type string
    Data map[string]interface{}
}
```

## 实际 API 端点清单

| 端点 | 方法 | 说明 | 响应格式 |
|------|------|------|---------|
| `/api/health` | GET | 健康检查 | `{ "healthy": true }` |
| `/api/session` | GET | 列出会话 | `{ "data": [SessionInfo], "cursor": {...} }` |
| `/api/session` | POST | 创建会话 | `{ "data": SessionInfo }` |
| `/api/session/:sessionID` | GET | 获取会话 | `{ "data": SessionInfo }` |
| `/api/session/:sessionID/message` | GET | 获取消息 | `{ "data": [Message], "cursor": {...} }` |
| `/api/session/:sessionID/prompt` | POST | 发送提示 | `{ "data": SessionInputAdmitted }` |
| `/api/session/:sessionID/context` | GET | 获取上下文 | `{ "data": [Message] }` |
| `/api/session/:sessionID/compact` | POST | 压缩会话 | 204 No Content |
| `/api/session/:sessionID/wait` | POST | 等待空闲 | 204 No Content |

## 状态判断逻辑

由于 OpenCode 的 SessionInfo 没有直接的 `status` 字段，我们通过以下逻辑判断：

```go
status := "idle"
if time.Since(time.UnixMilli(s.Time.Updated)) < 5*time.Minute {
    status = "active"
}
```

**逻辑说明**：
- 如果最后更新时间在 5 分钟内 → `active`
- 否则 → `idle`

## 代码变更统计

SessionInfo 中没有代码变更统计字段。要获取这些数据，需要：

1. 调用 `/api/session/:sessionID/message` 获取消息列表
2. 分析消息类型和内容
3. 统计文件操作（编辑、创建、删除）
4. 计算 additions/deletions

**实现位置**：可在 `backend/internal/opencode/stats.go` 中实现。

## 测试工具

### 1. 快速测试脚本
```bash
./quick-test-opencode.sh
```

验证 OpenCode API 是否正常工作。

### 2. 完整集成测试
```bash
./test-opencode-integration.sh
```

测试完整的集成流程（OpenCode + 后端适配器）。

### 3. 手动验证
```bash
# 健康检查
curl http://localhost:4096/api/health

# 列出会话
curl http://localhost:4096/api/session?limit=5 | jq .

# 获取详情
curl http://localhost:4096/api/session/ses_xxxxx | jq .
```

## 配置示例

### OpenCode 实例配置 (`backend/opencode-instances.json`)
```json
[
  {
    "id": "local-dev",
    "displayName": "本地开发实例",
    "apiBaseURL": "http://localhost:4096",
    "environment": "development",
    "npsClientId": 0
  }
]
```

### 环境变量 (`backend/.env`)
```bash
OPENCODE_DISCOVERY_MODE=file
OPENCODE_CONFIG_FILE=./opencode-instances.json
OPENCODE_HEALTH_CHECK_INTERVAL=30s
```

## 验证清单

- [x] 分析 OpenCode 实际源码
- [x] 识别 API 路径和响应格式差异
- [x] 修正适配器代码
- [x] 更新健康检查逻辑
- [x] 添加新的 API 方法
- [x] 创建测试脚本
- [x] 编写验证文档
- [ ] 启动 OpenCode 实例测试
- [ ] 验证数据格式匹配
- [ ] 测试实时更新
- [ ] 集成到前端 UI

## 下一步行动

1. **本地测试**：
   ```bash
   # 启动 OpenCode
   cd ~/workspace/ai/opencode
   bun run dev
   
   # 运行快速测试
   cd ~/workspace/official-deploy/services/opencode-pocket
   ./quick-test-opencode.sh
   ```

2. **启动后端**：
   ```bash
   cd backend
   go build -o pocket-server ./cmd/server
   ./pocket-server
   ```

3. **完整集成测试**：
   ```bash
   ./test-opencode-integration.sh
   ```

4. **前端集成**：
   - 更新前端 store 以使用新的数据格式
   - 测试 UI 显示
   - 验证实时更新

## 参考文档

- [API 分析报告](./OPENCODE_API_ANALYSIS.md)
- [集成验证指南](./OPENCODE_INTEGRATION_VERIFICATION.md)
- [源码位置](~/workspace/ai/opencode)
  - `packages/core/src/session/schema.ts` - Session 数据模型
  - `packages/server/src/groups/session.ts` - HTTP 端点定义
  - `packages/opencode/src/server/server.ts` - 服务器实现

## 总结

通过分析真实源码，我们发现并修正了适配器的以下问题：

1. ✅ API 路径错误 → 已修正为 `/api/*`
2. ✅ 响应格式假设错误 → 已适配 `{ "data": ... }` 格式
3. ✅ 数据结构不完整 → 已映射完整的 SessionInfo
4. ✅ 健康检查端点错误 → 已修正为 `/api/health`
5. ✅ 缺少必要的 API 方法 → 已添加 GetSessionDetail、GetSessionMessages、HealthCheck

现在适配器代码已与真实 OpenCode API 保持一致，可以进行本地测试验证。
