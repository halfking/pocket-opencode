# 会话 B 提示词：后端服务与 OpenCode 探测

> 复制以下内容到新会话

---

## 身份与背景

你是 OpenCode Pocket 应用的后端工程师。项目位于 `/Users/xutaohuang/workspace/official-deploy/services/opencode-pocket`，后端是 Go 语言编写的服务 (`pocketd`)，负责管理多个 OpenCode AI 编程代理实例。

当前状态文档：`SESSION_HANDOFF.md`

## 你的任务范围

**只处理后端 Go 代码，不修改前端 Vue 组件。**

## 架构概览

```
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│  Mobile App  │────▶│   pocketd    │────▶│  OpenCode 实例   │
│  (Vue 3)     │◀────│  (Go HTTP)   │◀────│  (HTTP API)      │
└─────────────┘     └──────────────┘     └─────────────────┘
                           │
                     ┌─────┴─────┐
                     │ PostgreSQL │
                     └───────────┘
```

**关键文件:**
- `backend/cmd/pocketd/main.go` — 入口，组装所有依赖
- `backend/internal/server/server.go` — HTTP 路由注册
- `backend/internal/adapter/opencode_http.go` — OpenCode HTTP 适配器
- `backend/internal/opencode/manager.go` — 会话管理器（缓存 + 状态推断）
- `backend/internal/opencode/event_stream.go` — SSE 事件流管理
- `backend/internal/registry/registry.go` — 实例注册与发现

## 具体任务清单

### 1. 实现 STT 端点（高优先级）
当前状态：`POST /api/stt/transcribe` 返回 501。

需要实现：
1. 接收音频文件（multipart/form-data 或文件路径）
2. 优先使用 Groq Whisper Large v3 Turbo（已有 `POCKET_GROQ_API_KEY` 配置）
3. 备选：调用本地 sherpa-onnx 服务
4. 返回 `{ text: string, confidence: number }`

参考实现：
```go
// backend/internal/server/server_assistant.go 约 600 行
func (s *Server) handleSTTTranscribe(w http.ResponseWriter, r *http.Request) {
    // TODO: Phase 3: STT audio file handling from multipart
}
```

Groq API 端点：`POST https://api.groq.com/openai/v1/audio/transcriptions`
模型：`whisper-large-v3-turbo`

### 2. 实现 OpenCode 实例自动发现（高优先级）
当前状态：实例从 `POCKET_OPENCODE_INSTANCES` JSON 静态配置加载。

需要实现：
1. **网络扫描** — 扫描本地网络的常见端口 (14096, 14097, ...)
2. **健康检查** — `GET http://{ip}:{port}/api/health` 验证是否为 OpenCode 实例
3. **mDNS 发现**（可选）— 使用 `mdns` 库发现 `_opencode._tcp` 服务
4. **结果缓存** — 发现的实例缓存到内存，定期刷新

参考文件：
```go
// backend/internal/registry/registry.go
func (r *Registry) EnableAutoDiscovery(ctx context.Context) {
    // 已有框架，需要填充实现
}
```

API 端点已存在：`GET /api/opencode/discover`

### 3. 实现任务状态更新和删除 API（高优先级）
当前状态：前端只能创建和读取任务，不能更新状态或删除。

需要实现：
```go
// backend/internal/server/server_assistant.go 或新建 task_handler.go

// PATCH /api/tasks/{id}
func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
    // Body: { "status": "active|blocked|completed", "priority": "high|medium|low", "title": "..." }
    // 更新 PostgreSQL 中的任务记录
}

// DELETE /api/tasks/{id}
func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
    // 删除任务及其会话关联
}
```

路由注册在 `server.go` 的 `Handler()` 方法中。

### 4. LLM 网关配置持久化（中优先级）
当前状态：`currentLLMGateway` 存在内存变量中，重启丢失。

需要：
1. 在 PostgreSQL 创建 `llm_gateway_configs` 表
2. 实现 `SaveConfig()` 和 `LoadConfig()` 方法
3. 在 `handleSaveGatewayConfig` 中调用持久化
4. 启动时从数据库加载最新配置

```sql
CREATE TABLE IF NOT EXISTS llm_gateway_configs (
    id SERIAL PRIMARY KEY,
    base_url TEXT NOT NULL,
    api_key_encrypted TEXT NOT NULL,
    models JSONB,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
```

### 5. 统一 API 框架（中优先级）
当前状态：`mobile_api.go` 使用 Echo 框架，主服务器使用 `net/http`。

方案选择：
- **方案 A**: 将 `mobile_api.go` 的路由迁移到 `net/http`（推荐，减少依赖）
- **方案 B**: 将整个服务器迁移到 Echo

建议选方案 A，因为主服务器的 `net/http` 路由已经很成熟。

### 6. 实现游标分页（中优先级）
当前状态：使用 offset 分页，对大数据集不高效。

需要：
1. 定义游标格式：`base64({id, created_at})`
2. 修改 `ListSessions` 和 `ListMessages` 支持 `cursor` + `limit` 参数
3. 返回 `{ data: [], next_cursor: string, has_more: boolean }`

### 7. 补全 OpenCode 端点（低优先级）
前端 `stores/opencode.ts` 调用了不存在的端点：

```go
// 需要实现：
GET /api/opencode/sessions/{id}/history   // 会话历史
GET /api/opencode/sessions/{id}/summary   // 会话摘要
```

这些可以代理到 OpenCode 实例的对应 API。

### 8. WebSocket Origin 检查（低优先级）
当前状态：`server_plugin_ws.go` 允许所有 origin。

需要：
1. 从配置加载允许的 origin 列表
2. 在 WebSocket upgrade 时检查 `Origin` header
3. 开发模式允许 `localhost:*`

## 技术约束

- **不要修改前端代码** — API 接口向后兼容
- **使用 pgxpool** — 不要引入新的数据库驱动
- **保持向后兼容** — 新端点不破坏现有 API
- **错误处理** — 所有 API 返回标准错误格式 `{ "error": "message" }`
- **日志** — 使用 `log.Printf` 记录关键操作

## 验证方法

每次改动后：
1. `cd backend && go build ./cmd/pocketd` — 确保编译成功
2. `go test ./...` — 运行所有测试
3. 启动服务并测试 API：
   ```bash
   POCKET_DEV_AUTH=true ./pocketd
   curl -s http://localhost:8088/healthz
   curl -s http://localhost:8088/api/sessions
   ```

## 数据库迁移

如果需要新建表，创建迁移文件：
```
backend/migrations/00X_description.sql
```

并在 `main.go` 中调用迁移。

## 提交规范

```
feat(api): [简要描述]
fix(api): [简要描述]
refactor(api): [简要描述]
```

每次提交前运行 `go vet ./...` 确认代码质量。
