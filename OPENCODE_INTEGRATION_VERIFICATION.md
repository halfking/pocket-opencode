> **STATUS: superseded** (2026-08-23)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/governance/STATUS-MATRIX.md`](../docs/governance/STATUS-MATRIX.md)
> Do NOT use this doc for current implementation decisions.
>
> This doc claimed integration verification at its write time. At supersede time, no integration-test log was captured in `docs/governance/EVIDENCE-LEDGER.md`. See `docs/governance/REVIEW-QUEUE.md`.

# OpenCode 集成验证指南

## 概述

本文档说明如何验证 Pocket OpenCode 后端适配器与真实 OpenCode API 的集成。

## 前置条件

1. **OpenCode 已安装**：
   ```bash
   cd ~/workspace/ai/opencode
   ```

2. **依赖已安装**：
   ```bash
   bun install
   ```

3. **后端服务器已编译**：
   ```bash
   cd ~/workspace/official-deploy/services/opencode-pocket/backend
   go build -o pocket-server ./cmd/server
   ```

## 步骤 1：启动 OpenCode 实例

### 1.1 启动 OpenCode 开发服务器

```bash
cd ~/workspace/ai/opencode
bun run dev
```

**预期输出**：
```
OpenCode listening on http://0.0.0.0:4096
```

### 1.2 验证 OpenCode API

在新终端中测试：

```bash
# 健康检查
curl http://localhost:4096/api/health

# 预期响应：
# {"healthy":true}

# 列出 sessions
curl http://localhost:4096/api/session | jq .

# 预期响应格式：
# {
#   "data": [
#     {
#       "id": "ses_xxxxx",
#       "projectID": "proj_xxxxx",
#       "title": "...",
#       "time": {
#         "created": 1234567890000,
#         "updated": 1234567890000
#       },
#       "tokens": { ... },
#       "location": { ... }
#     }
#   ],
#   "cursor": { ... }
# }
```

## 步骤 2：配置后端实例

### 2.1 创建实例配置文件

创建 `backend/opencode-instances.json`：

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

### 2.2 配置环境变量

创建 `backend/.env`：

```bash
# OpenCode 实例发现配置
OPENCODE_DISCOVERY_MODE=file
OPENCODE_CONFIG_FILE=./opencode-instances.json
OPENCODE_HEALTH_CHECK_INTERVAL=30s

# 数据库配置
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=your_password
DB_NAME=pocket_opencode

# 服务器配置
SERVER_PORT=8080
```

## 步骤 3：启动后端服务器

```bash
cd backend
./pocket-server
```

**预期日志输出**：
```
✅ 启动 OpenCode 实例自动发现（间隔: 30s）
✅ 加载配置实例: 本地开发实例 (local-dev)
✅ 实例健康检查: local-dev -> healthy
🚀 服务器启动在 :8080
```

## 步骤 4：运行集成测试

### 4.1 自动化测试脚本

```bash
cd ~/workspace/official-deploy/services/opencode-pocket
./test-opencode-integration.sh
```

### 4.2 手动测试

#### 测试 1：健康检查
```bash
# 测试 OpenCode 健康状态
curl http://localhost:4096/api/health

# 测试后端健康状态
curl http://localhost:8080/api/health
```

#### 测试 2：发现实例
```bash
curl http://localhost:8080/api/opencode/instances | jq .
```

**预期响应**：
```json
{
  "instances": [
    {
      "id": "local-dev",
      "displayName": "本地开发实例",
      "health": "healthy",
      "capabilities": ["session", "summary", "pty"],
      "lastHeartbeatAt": "2026-07-02T10:30:00Z",
      "environment": "development"
    }
  ]
}
```

#### 测试 3：获取实例任务
```bash
curl "http://localhost:8080/api/opencode/instances/local-dev/tasks?limit=10" | jq .
```

**预期响应**：
```json
{
  "instanceId": "local-dev",
  "tasks": [
    {
      "id": "ses_xxxxx",
      "title": "Session title",
      "status": "active",
      "owner": "default-agent"
    }
  ]
}
```

#### 测试 4：获取任务详情
```bash
SESSION_ID="ses_xxxxx"  # 替换为实际的 session ID
curl "http://localhost:8080/api/opencode/tasks/$SESSION_ID" | jq .
```

**预期响应**：
```json
{
  "taskId": "ses_xxxxx",
  "instanceId": "local-dev",
  "title": "Session title",
  "status": "active",
  "createdAt": "2026-07-02T10:00:00Z",
  "updatedAt": "2026-07-02T10:30:00Z",
  "messageCount": 15,
  "tokens": {
    "input": 1000,
    "output": 2000,
    "reasoning": 500
  },
  "cost": 0.05
}
```

## 步骤 5：验证数据格式

### 5.1 验证 Session 数据结构

创建验证脚本 `verify-session-structure.sh`：

```bash
#!/bin/bash

SESSION_DATA=$(curl -s http://localhost:4096/api/session?limit=1 | jq '.data[0]')

echo "验证 Session 数据结构..."
echo "$SESSION_DATA" | jq .

# 检查必需字段
REQUIRED_FIELDS=("id" "projectID" "title" "time" "tokens" "location" "cost")

for field in "${REQUIRED_FIELDS[@]}"; do
    if echo "$SESSION_DATA" | jq -e ".$field" > /dev/null 2>&1; then
        echo "✓ $field 存在"
    else
        echo "✗ $field 缺失"
    fi
done
```

## 步骤 6：测试实时更新

### 6.1 连接 WebSocket

```javascript
// 在浏览器控制台或 Node.js 中
const ws = new WebSocket('ws://localhost:8080/api/opencode/realtime');

ws.onopen = () => {
    console.log('WebSocket 连接已建立');
};

ws.onmessage = (event) => {
    const update = JSON.parse(event.data);
    console.log('收到更新:', update);
};
```

### 6.2 创建新 Session 并观察更新

在 OpenCode 中创建新 session：

```bash
curl -X POST http://localhost:4096/api/session \
  -H "Content-Type: application/json" \
  -d '{
    "location": {
      "directory": "/tmp/test-project"
    }
  }'
```

**预期 WebSocket 消息**：
```json
{
  "type": "session_created",
  "sessionId": "ses_xxxxx",
  "status": "active",
  "timestamp": "2026-07-02T10:30:00Z"
}
```

## 常见问题排查

### 问题 1：OpenCode API 返回 404

**原因**：使用了错误的 API 路径。

**解决方案**：
- 确保使用 `/api/session` 而不是 `/session`
- 确保使用 `/api/health` 而不是 `/healthz`

### 问题 2：响应格式不匹配

**原因**：期望的响应格式与实际不同。

**解决方案**：
- OpenCode 的响应总是包装在 `{ "data": ... }` 中
- 检查 `OPENCODE_API_ANALYSIS.md` 中的实际响应格式

### 问题 3：健康检查失败

**原因**：OpenCode 服务未启动或监听不同端口。

**解决方案**：
```bash
# 检查 OpenCode 是否运行
curl http://localhost:4096/api/health

# 检查 OpenCode 配置
cat ~/workspace/ai/opencode/.opencode/config.json
```

### 问题 4：Session 列表为空

**原因**：OpenCode 实例没有任何 session。

**解决方案**：
```bash
# 创建一个测试 session
curl -X POST http://localhost:4096/api/session \
  -H "Content-Type: application/json" \
  -d '{
    "location": {
      "directory": "'$(pwd)'"
    }
  }'
```

## 性能测试

### 测试并发请求

```bash
# 使用 Apache Bench
ab -n 100 -c 10 http://localhost:8080/api/opencode/instances

# 使用 wrk
wrk -t4 -c100 -d30s http://localhost:8080/api/opencode/instances
```

## 日志分析

### 启用详细日志

修改 `backend/main.go`：

```go
log.SetFlags(log.LstdFlags | log.Lshortfile)
log.SetPrefix("[OpenCode] ")
```

### 查看关键日志

```bash
# 实例发现日志
grep "发现" backend/logs/app.log

# 健康检查日志
grep "健康" backend/logs/app.log

# API 请求日志
grep "GET\|POST" backend/logs/app.log
```

## 下一步

完成验证后：

1. ✅ 确认适配器正确调用 OpenCode API
2. ✅ 确认数据格式匹配
3. ✅ 确认健康检查工作正常
4. ⏭️ 集成到前端 UI
5. ⏭️ 添加错误处理和重试逻辑
6. ⏭️ 实现数据缓存优化
7. ⏭️ 部署到生产环境

## 参考文档

- [OpenCode API 分析](./OPENCODE_API_ANALYSIS.md)
- [架构设计文档](./docs/opencode-task-management-architecture.md)
- [实现指南](./docs/OPENCODE_IMPLEMENTATION_GUIDE.md)
