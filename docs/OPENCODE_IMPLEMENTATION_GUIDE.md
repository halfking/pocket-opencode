# OpenCode 任务管理系统 - 实施指南

## 📋 已完成的工作

我已经为你创建了一个完整的 OpenCode 任务管理系统，包括：

### 1. 架构设计文档
- 📄 `docs/opencode-task-management-architecture.md` - 完整的系统架构设计

### 2. 后端模块

#### 核心管理模块
- 📄 `backend/internal/opencode/manager.go` - OpenCode 管理器
  - 实例管理
  - 会话跟踪
  - 状态监控
  - 缓存管理

#### 数据持久化
- 📄 `backend/internal/opencode/store.go` - PostgreSQL 历史存储
  - 会话记录存储
  - 历史事件记录
  - 数据库表初始化

#### API 路由处理
- 📄 `backend/internal/server/server_opencode.go` - OpenCode API 处理器
  - GET /api/opencode/sessions - 获取会话列表
  - GET /api/opencode/sessions/{id}/history - 获取会话历史
  - GET /api/opencode/sessions/{id}/summary - 获取会话摘要
  - GET /api/opencode/instances/{id}/stats - 获取实例统计
  - POST /api/opencode/cache/refresh - 刷新缓存

### 3. 前端模块

#### 状态管理
- 📄 `frontend/src/stores/opencode.ts` - OpenCode Store (Pinia)
  - 实例管理
  - 会话列表
  - 实时状态更新
  - WebSocket 集成

#### 用户界面
- 📄 `frontend/src/features/opencode/OpenCodeHub.vue` - 实例管理中心
  - 显示所有 OpenCode 实例
  - 在线/离线状态
  - 实例统计信息

- 📄 `frontend/src/features/opencode/SessionListView.vue` - 会话列表视图
  - 按状态分组显示会话
  - 实时状态更新
  - 会话详情导航

## 🔧 集成步骤

### 步骤 1: 初始化数据库表

在你的数据库初始化代码中添加：

```go
// backend/cmd/pocketd/main.go 或你的初始化文件

import (
    "github.com/halfking/pocket-opencode/backend/internal/opencode"
)

func initDatabase(db *sql.DB) error {
    // 初始化 OpenCode 相关表
    if err := opencode.InitSchema(db); err != nil {
        return fmt.Errorf("init opencode schema failed: %w", err)
    }
    
    log.Println("✅ OpenCode 数据库表初始化成功")
    return nil
}
```

这将创建以下表：
- `opencode_sessions` - 会话记录
- `opencode_session_history` - 会话历史事件

### 步骤 2: 在后端 main.go 中集成 OpenCode Manager

```go
// backend/cmd/pocketd/main.go

import (
    "github.com/halfking/pocket-opencode/backend/internal/opencode"
)

func main() {
    // ... 现有的初始化代码 ...
    
    // 创建 OpenCode 历史存储
    historyStore := opencode.NewPostgresHistoryStore(db)
    
    // 创建 OpenCode 管理器
    opencodeManager := opencode.NewManager(
        registry,       // 你现有的 registry
        opencodeAdapter, // 你现有的 opencode adapter
        historyStore,
    )
    
    // 启动状态监控（每 30 秒轮询一次）
    ctx := context.Background()
    go opencodeManager.StartStatusMonitoring(ctx, 30*time.Second)
    
    // 创建 Server 时传入 opencodeManager
    server := server.New(
        cfg,
        npsAdapter,
        opencodeAdapter,
        taskStore,
        registry,
        configAdapter,
        notesStore,
        emailStore,
        vaultStore,
        transcriber,
        mcpClient,
        embedder,
        llm,
        kxmemory,
        opencodeManager, // 新增参数
    )
    
    log.Println("✅ OpenCode 管理器已启动")
    
    // ... 其他启动代码 ...
}
```

### 步骤 3: 配置前端路由

在你的前端路由配置中添加：

```typescript
// frontend/src/app/router-mobile.ts 或 router.ts

import OpenCodeHub from '@/features/opencode/OpenCodeHub.vue'
import SessionListView from '@/features/opencode/SessionListView.vue'

const routes = [
  // ... 现有路由 ...
  
  {
    path: '/opencode/hub',
    name: 'OpenCodeHub',
    component: OpenCodeHub,
    meta: { requiresAuth: true }
  },
  {
    path: '/opencode/sessions',
    name: 'OpenCodeSessions',
    component: SessionListView,
    meta: { requiresAuth: true }
  },
  // 未来可以添加会话详情页
  // {
  //   path: '/opencode/sessions/:id',
  //   name: 'SessionDetail',
  //   component: SessionDetailView,
  //   meta: { requiresAuth: true }
  // }
]
```

### 步骤 4: 在主界面添加入口

在你的实例列表或主导航中添加 OpenCode Hub 入口：

```vue
<!-- frontend/src/features/instances/InstanceListView.vue -->
<template>
  <div class="actions">
    <button @click="$router.push('/opencode/hub')">
      🎯 OpenCode 管理中心
    </button>
  </div>
</template>
```

### 步骤 5: 编译并测试

```bash
# 后端
cd backend
go mod tidy
go build -o pocketd cmd/pocketd/main.go

# 前端
cd frontend
npm install
npm run build

# 启动服务
./backend/pocketd
```

## 📊 使用流程

### 用户体验流程

```
1. 用户进入应用
   ↓
2. 点击"OpenCode 管理中心"
   ↓
3. 看到所有 OpenCode 实例列表
   - 🟢 在线实例（显示活跃会话数）
   - 🔴 离线实例
   ↓
4. 选择一个实例
   ↓
5. 进入该实例的会话列表
   - 🔴 进行中的会话（实时更新）
   - ⚪ 空闲的会话
   ↓
6. 点击会话查看详情（未来功能）
   - 会话历史时间线
   - 代码变更统计
   - 导出摘要
```

## 🎨 关键特性

### 1. 实时状态更新
- WebSocket 自动连接和重连
- 会话状态实时推送
- 自动更新 UI

### 2. 智能缓存
- 5分钟会话列表缓存
- 按实例分组缓存
- 支持手动刷新

### 3. 离线降级
- OpenCode 实例离线时显示历史数据
- 优雅的错误处理
- 自动重试机制

### 4. 多实例支持
- 聚合多个 OpenCode 实例
- 并发获取实例数据
- 统一的状态管理

## 🔍 API 端点说明

### 获取实例的会话列表
```bash
GET /api/opencode/sessions?instance_id=xxx&status=busy&limit=20

Response:
{
  "sessions": [
    {
      "id": "sess_abc123",
      "instanceId": "kaixuan-71",
      "title": "修复登录bug",
      "status": "busy",
      "messageCount": 12,
      "fileChanges": {
        "additions": 125,
        "deletions": 43,
        "files": 3
      },
      "duration": 900,
      "createdAt": "2026-07-02T14:30:00Z",
      "updatedAt": "2026-07-02T14:45:00Z"
    }
  ],
  "total": 1
}
```

### 获取会话历史
```bash
GET /api/opencode/sessions/sess_abc123/history?limit=100

Response:
{
  "sessionId": "sess_abc123",
  "timeline": [
    {
      "timestamp": "2026-07-02T14:30:00Z",
      "type": "message",
      "actor": "user",
      "content": "修复登录bug"
    },
    {
      "timestamp": "2026-07-02T14:31:00Z",
      "type": "edit",
      "actor": "ai",
      "content": "编辑 src/auth/login.ts",
      "metadata": {
        "file": "src/auth/login.ts",
        "additions": 10,
        "deletions": 5
      }
    }
  ],
  "total": 15
}
```

### 获取会话摘要
```bash
GET /api/opencode/sessions/sess_abc123/summary?instance_id=kaixuan-71

Response:
{
  "sessionId": "sess_abc123",
  "summary": "修复了用户登录时的边界条件检查bug，添加了单元测试..."
}
```

## 🚨 注意事项

### 1. OpenCode API 兼容性
当前实现基于 OpenCode 提供以下 API：
- GET /session - 会话列表
- GET /session/status - 实时状态
- GET /session/{id}/summarize - 会话摘要

**如果你的 OpenCode 版本不支持这些 API，需要调整 adapter 实现。**

### 2. 数据库权限
确保数据库用户有创建表的权限：
```sql
GRANT CREATE ON DATABASE pocket_db TO pocket_user;
```

### 3. WebSocket 配置
如果使用 Nginx 反向代理，需要配置 WebSocket 支持：
```nginx
location /ws {
    proxy_pass http://backend:9010;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
}
```

### 4. 性能优化
- 会话列表有缓存，默认 5 分钟失效
- 状态监控间隔建议 30-60 秒
- 大量实例时考虑分页

## 📝 下一步开发

### 高优先级
1. **Session Detail 详情页**
   - 完整的历史时间线
   - 代码变更可视化
   - 导出和分享功能

2. **搜索和筛选**
   - 按关键词搜索会话
   - 按时间范围筛选
   - 按状态过滤

### 中优先级
3. **实例注册界面**
   - 可视化注册新实例
   - 实例健康检查
   - 实例配置管理

4. **通知和提醒**
   - 会话完成通知
   - 错误提醒
   - 定时摘要推送

### 低优先级
5. **数据分析**
   - 使用统计
   - 效率分析
   - 趋势图表

6. **会话重放**
   - 回放历史会话
   - 时光机功能

## 🐛 故障排查

### 问题：前端看不到实例
**检查：**
1. 后端 registry 是否有注册实例？
2. 实例的 health 状态是否 healthy？
3. 浏览器控制台是否有错误？

### 问题：会话列表为空
**检查：**
1. OpenCode 实例是否正在运行？
2. OpenCode /session API 是否返回数据？
3. 后端日志是否有错误？

### 问题：实时更新不工作
**检查：**
1. WebSocket 是否成功连接？
2. Nginx 是否配置了 WebSocket 支持？
3. 防火墙是否阻止了 WebSocket？

## 📞 技术支持

如果遇到问题：
1. 查看后端日志：`tail -f /data/services/opencode-pocket/logs/pocket.log`
2. 查看浏览器控制台
3. 检查数据库连接和表结构
4. 确认 OpenCode 实例的 API 可访问性

---

## ✅ 集成清单

- [ ] 数据库表已初始化
- [ ] 后端 main.go 已集成 OpenCode Manager
- [ ] 前端路由已配置
- [ ] 主界面已添加入口
- [ ] 编译成功无错误
- [ ] 可以看到实例列表
- [ ] 可以看到会话列表
- [ ] WebSocket 实时更新工作正常

完成上述步骤后，你的 OpenCode 任务管理系统就可以投入使用了！🎉
