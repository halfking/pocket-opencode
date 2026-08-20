# OpenCode 实例发现和任务感知 API 文档

## 概述

新增的 API 使你能够：
1. **自动发现** OpenCode 实例
2. **感知任务** 获取实例上的所有任务（会话）
3. **查看详情** 获取任务的完整信息和历史

---

## 🔍 API 端点

### 1. 实例发现 API

#### 发现所有可用实例
```http
GET /api/opencode/discover
POST /api/opencode/discover
```

**响应示例：**
```json
{
  "instances": [
    {
      "id": "opencode-71",
      "displayName": "开发机 71",
      "environment": "production",
      "health": "healthy",
      "capabilities": ["session", "summary", "pty"],
      "lastHeartbeat": "2026-07-03T10:30:00Z",
      "taskCount": 15,
      "activeTaskCount": 3
    },
    {
      "id": "opencode-252",
      "displayName": "测试机 252",
      "environment": "production",
      "health": "healthy",
      "capabilities": ["session", "summary"],
      "lastHeartbeat": "2026-07-03T10:30:00Z",
      "taskCount": 8,
      "activeTaskCount": 1
    }
  ],
  "total": 2,
  "timestamp": 1719999000
}
```

**说明：**
- 返回所有已发现的 OpenCode 实例
- 包含健康状态和任务统计
- 只有 `healthy` 状态的实例才会返回任务统计

---

### 2. 任务感知 API

#### 2.1 获取指定实例的任务列表
```http
GET /api/opencode/instances/{instance_id}/tasks?status=busy&limit=20
```

**查询参数：**
- `status` (可选) - 任务状态过滤：`busy` | `idle` | `retry` | `all`
- `limit` (可选) - 返回数量限制，默认 100

**响应示例：**
```json
{
  "instanceId": "opencode-71",
  "tasks": [
    {
      "id": "sess_abc123",
      "title": "修复用户登录bug",
      "status": "busy",
      "createdAt": "2026-07-03T09:00:00Z",
      "updatedAt": "2026-07-03T10:30:00Z",
      "messageCount": 15,
      "fileChanges": {
        "additions": 125,
        "deletions": 43,
        "files": 3
      },
      "duration": 5400,
      "metadata": {
        "agent": "claude-opus-4"
      }
    },
    {
      "id": "sess_def456",
      "title": "添加邮件通知功能",
      "status": "idle",
      "createdAt": "2026-07-03T08:00:00Z",
      "updatedAt": "2026-07-03T09:30:00Z",
      "messageCount": 8,
      "fileChanges": {
        "additions": 67,
        "deletions": 12,
        "files": 2
      },
      "duration": 2700
    }
  ],
  "total": 15,
  "filtered": 2,
  "timestamp": 1719999000
}
```

#### 2.2 获取所有实例的任务（聚合视图）
```http
GET /api/opencode/tasks?status=busy&limit=50
```

**查询参数：**
- `status` (可选) - 任务状态过滤
- `limit` (可选) - 返回数量限制

**响应示例：**
```json
{
  "tasks": [
    {
      "id": "sess_abc123",
      "instanceId": "opencode-71",
      "title": "修复用户登录bug",
      "status": "busy",
      "createdAt": "2026-07-03T09:00:00Z",
      "updatedAt": "2026-07-03T10:30:00Z",
      "messageCount": 15,
      "fileChanges": {
        "additions": 125,
        "deletions": 43,
        "files": 3
      },
      "duration": 5400
    }
  ],
  "total": 23,
  "filtered": 3,
  "instanceStats": {
    "opencode-71": 15,
    "opencode-252": 8
  },
  "statusStats": {
    "busy": 3,
    "idle": 18,
    "retry": 2
  },
  "timestamp": 1719999000
}
```

---

### 3. 任务详情 API

#### 获取任务的完整信息
```http
GET /api/opencode/tasks/{task_id}?instance_id={instance_id}
```

**查询参数：**
- `instance_id` (必需) - 实例 ID

**响应示例：**
```json
{
  "id": "sess_abc123",
  "instanceId": "opencode-71",
  "title": "修复用户登录bug",
  "status": "busy",
  "createdAt": "2026-07-03T09:00:00Z",
  "updatedAt": "2026-07-03T10:30:00Z",
  "messageCount": 15,
  "fileChanges": {
    "additions": 125,
    "deletions": 43,
    "files": 3
  },
  "duration": 5400,
  "summary": "修复了用户登录时的边界条件检查bug，添加了单元测试并验证通过...",
  "history": [
    {
      "timestamp": "2026-07-03T09:00:00Z",
      "type": "message",
      "actor": "user",
      "content": "修复登录时的边界条件检查"
    },
    {
      "timestamp": "2026-07-03T09:01:00Z",
      "type": "edit",
      "actor": "ai",
      "content": "编辑 src/auth/login.ts",
      "metadata": {
        "file": "src/auth/login.ts",
        "additions": 10,
        "deletions": 5
      }
    },
    {
      "timestamp": "2026-07-03T09:05:00Z",
      "type": "test",
      "actor": "system",
      "content": "单元测试通过"
    }
  ],
  "metadata": {
    "agent": "claude-opus-4",
    "repository": "project-alpha"
  }
}
```

---

## 🚀 使用场景

### 场景 1: 监控仪表板
```javascript
// 1. 发现所有实例
const response = await fetch('/api/opencode/discover');
const { instances } = await response.json();

// 2. 显示每个实例的任务统计
instances.forEach(instance => {
  console.log(`${instance.displayName}: ${instance.activeTaskCount} 个活跃任务`);
});
```

### 场景 2: 任务列表
```javascript
// 获取指定实例的所有进行中的任务
const response = await fetch('/api/opencode/instances/opencode-71/tasks?status=busy');
const { tasks } = await response.json();

// 显示任务列表
tasks.forEach(task => {
  console.log(`${task.title} - ${task.status}`);
});
```

### 场景 3: 任务详情
```javascript
// 获取任务的完整信息
const response = await fetch('/api/opencode/tasks/sess_abc123?instance_id=opencode-71');
const taskDetail = await response.json();

// 显示详细信息
console.log('标题:', taskDetail.title);
console.log('摘要:', taskDetail.summary);
console.log('历史记录:', taskDetail.history.length, '条');
```

### 场景 4: 全局搜索
```javascript
// 搜索所有实例中的活跃任务
const response = await fetch('/api/opencode/tasks?status=busy&limit=100');
const { tasks, instanceStats, statusStats } = await response.json();

console.log('总计:', tasks.length, '个活跃任务');
console.log('实例分布:', instanceStats);
console.log('状态分布:', statusStats);
```

---

## 🔧 后端集成示例

### 启用自动发现

```go
// backend/cmd/pocketd/main.go

import (
    "github.com/halfking/pocket-opencode/backend/internal/registry"
)

func main() {
    // ... 其他初始化代码 ...

    // 创建 Registry
    reg := registry.NewRegistry()

    // 方案 1: 基于环境变量的发现
    // 设置环境变量: OPENCODE_INSTANCES='[{"id":"opencode-71",...}]'
    discoveryFunc := registry.EnvironmentVariableDiscovery()

    // 方案 2: 基于配置文件的发现
    // discoveryFunc := registry.StaticFileDiscovery("config/instances.json")

    // 方案 3: 基于 NPS API 的发现（自动发现）
    // discoveryFunc := registry.NPSBasedDiscovery(
    //     "http://14.103.169.56:8080",
    //     "your-nps-token"
    // )

    // 方案 4: 多源组合发现
    // discoveryFunc := registry.MultiSourceDiscovery(
    //     registry.EnvironmentVariableDiscovery(),
    //     registry.StaticFileDiscovery("config/instances.json"),
    //     registry.NPSBasedDiscovery("http://nps-server", "token")
    // )

    // 启用自动发现（每 30 秒）
    reg.EnableAutoDiscovery(discoveryFunc, 30*time.Second)

    // 启动自动发现和健康检查
    ctx := context.Background()
    go reg.StartAutoDiscovery(ctx)

    log.Println("✅ OpenCode 实例自动发现已启动")

    // ... 其他代码 ...
}
```

### 配置环境变量

```bash
# .env 或 环境变量
export OPENCODE_INSTANCES='[
  {
    "id": "opencode-71",
    "displayName": "开发机 71",
    "npsClientId": 71,
    "apiBaseURL": "http://14.103.169.71:3000",
    "environment": "production"
  },
  {
    "id": "opencode-252",
    "displayName": "测试机 252",
    "npsClientId": 252,
    "apiBaseURL": "http://14.103.169.252:3000",
    "environment": "production"
  }
]'
```

### 配置文件示例

```json
// config/instances.json
[
  {
    "id": "opencode-71",
    "displayName": "开发机 71",
    "npsClientId": 71,
    "apiBaseURL": "http://14.103.169.71:3000",
    "environment": "production"
  },
  {
    "id": "opencode-252",
    "displayName": "测试机 252",
    "npsClientId": 252,
    "apiBaseURL": "http://14.103.169.252:3000",
    "environment": "production"
  }
]
```

---

## 🎯 工作流程

```
1. 启动应用
   ↓
2. Registry 自动发现实例（首次立即执行）
   ↓
3. 对每个实例执行健康检查
   ↓
4. 标记实例状态（healthy/unhealthy/offline）
   ↓
5. 定时循环（30秒）
   - 重新发现实例
   - 执行健康检查
   - 更新状态
   ↓
6. API 可随时查询：
   - GET /api/opencode/discover - 查看所有实例
   - GET /api/opencode/instances/{id}/tasks - 查看实例任务
   - GET /api/opencode/tasks/{id}?instance_id=xxx - 查看任务详情
```

---

## 📊 状态说明

### 实例健康状态
- `healthy` - 实例在线且响应正常
- `unhealthy` - 实例在线但响应异常
- `offline` - 实例离线或无法连接
- `unknown` - 状态未知（刚注册）

### 任务状态
- `busy` - 任务进行中（AI 正在工作）
- `idle` - 任务空闲（等待用户输入）
- `retry` - 任务重试中（遇到错误正在重试）
- `completed` - 任务已完成

---

## 🔒 安全建议

1. **API 认证** - 建议添加 API Key 或 JWT 认证
2. **速率限制** - 防止 API 滥用
3. **访问控制** - 限制敏感信息的访问
4. **HTTPS** - 生产环境使用 HTTPS

---

## 🐛 故障排查

### 问题 1: 发现不到实例
**检查：**
- 环境变量或配置文件是否正确
- 实例的 API 地址是否可访问
- 防火墙规则

### 问题 2: 健康检查失败
**检查：**
- OpenCode 实例是否运行
- `/healthz` 或 `/session` 端点是否可访问
- 网络连接

### 问题 3: 任务列表为空
**检查：**
- 实例健康状态是否为 `healthy`
- OpenCode 上是否有活跃会话
- 缓存是否过期（尝试刷新）

---

## 📈 性能优化

1. **缓存机制** - 会话列表缓存 5 分钟
2. **并发请求** - 多实例并发查询
3. **健康检查** - 3 秒超时
4. **批量查询** - 使用聚合 API 减少请求数

---

## 🎉 完整示例

查看 `docs/INTEGRATION_EXAMPLE.go` 获取完整的集成示例代码。

---

**更新时间**: 2026-07-03  
**API 版本**: v1.1
