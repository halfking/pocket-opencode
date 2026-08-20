# OpenCode 任务管理系统架构设计

## 1. 系统架构概览

```
┌─────────────────────────────────────────────────────────────┐
│                       前端 (Vue 3)                           │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │ OpenCode Hub │  │  Task Board  │  │Session Detail│      │
│  │ 实例管理中心  │  │  任务看板     │  │  会话详情    │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
│                                                               │
│  ┌────────────────────────────────────────────────────┐     │
│  │          OpenCode Store (Pinia)                     │     │
│  │  - instances: 实例列表                               │     │
│  │  - sessions: 会话列表 (按实例)                       │     │
│  │  - sessionHistory: 会话历史和状态                    │     │
│  │  - realTimeStatus: 实时执行状态                      │     │
│  └────────────────────────────────────────────────────┘     │
│                                                               │
└─────────────────────────────────────────────────────────────┘
                           │ HTTP/WebSocket
                           ▼
┌─────────────────────────────────────────────────────────────┐
│                    后端 API (Go)                             │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌──────────────────────────────────────────────────┐       │
│  │          OpenCode Manager Module                  │       │
│  │  - Instance Registry (实例注册与发现)             │       │
│  │  - Session Tracker (会话跟踪)                     │       │
│  │  - History Aggregator (历史聚合)                  │       │
│  │  - Status Monitor (状态监控)                      │       │
│  └──────────────────────────────────────────────────┘       │
│                                                               │
│  ┌──────────────────────────────────────────────────┐       │
│  │          Data Persistence                         │       │
│  │  - PostgreSQL (任务、会话链接、历史)              │       │
│  │  - Redis (实时状态、缓存)                         │       │
│  └──────────────────────────────────────────────────┘       │
│                                                               │
└─────────────────────────────────────────────────────────────┘
                           │ HTTP
                           ▼
┌─────────────────────────────────────────────────────────────┐
│              OpenCode 实例 (多个)                            │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │ OpenCode #1  │  │ OpenCode #2  │  │ OpenCode #N  │      │
│  │ kaixuan-71   │  │ kaixuan-252  │  │ dev-machine  │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
│                                                               │
│  提供 HTTP API:                                              │
│  - GET  /session           # 会话列表                       │
│  - GET  /session/status    # 实时状态                       │
│  - GET  /session/{id}/summarize  # 会话摘要                │
│  - GET  /session/{id}/history    # 会话历史                │
│                                                               │
└─────────────────────────────────────────────────────────────┘
```

## 2. 核心模块设计

### 2.1 OpenCode Hub (前端实例管理中心)

**功能:**
- 显示所有已注册的 OpenCode 实例
- 实例状态监控 (在线/离线/忙碌)
- 实例能力展示
- 快速切换和选择实例

**UI 设计:**
```
┌────────────────────────────────────────┐
│  OpenCode 实例中心            🔄 刷新  │
├────────────────────────────────────────┤
│                                         │
│  🟢 kaixuan-71 (主力开发机)            │
│     在线 • 3 个活跃会话 • 8 个历史     │
│     [查看任务] [连接终端] [查看日志]   │
│                                         │
│  🟢 kaixuan-252 (测试机)               │
│     在线 • 1 个活跃会话 • 15 个历史    │
│     [查看任务] [连接终端]              │
│                                         │
│  🔴 dev-laptop (离线)                  │
│     离线 • 上次活跃: 2小时前           │
│                                         │
└────────────────────────────────────────┘
```

### 2.2 Task Board (任务看板)

**功能:**
- 显示选定实例的所有开发会话(任务)
- 按状态分组: 进行中/空闲/已完成
- 显示任务执行进度和状态
- 支持任务筛选和搜索

**UI 设计:**
```
┌────────────────────────────────────────┐
│  任务看板 - kaixuan-71        + 新任务 │
├────────────────────────────────────────┤
│                                         │
│  🔴 进行中 (2)                          │
│  ┌──────────────────────────────────┐  │
│  │ 💬 修复用户登录 bug               │  │
│  │ ID: sess_abc123                   │  │
│  │ 状态: busy • 已运行 15 分钟       │  │
│  │ 📝 12 条消息 • 3 个文件已修改     │  │
│  │ [查看详情] [查看历史]             │  │
│  └──────────────────────────────────┘  │
│                                         │
│  ⚪ 空闲 (5)                            │
│  ┌──────────────────────────────────┐  │
│  │ 💬 添加邮件通知功能               │  │
│  │ ID: sess_def456                   │  │
│  │ 状态: idle • 上次活跃 30 分钟前   │  │
│  │ 📝 8 条消息 • 2 个文件已修改      │  │
│  │ [继续] [查看历史]                 │  │
│  └──────────────────────────────────┘  │
│                                         │
└────────────────────────────────────────┘
```

### 2.3 Session Detail (会话详情与历史)

**功能:**
- 显示会话的完整历史记录
- 实时显示执行状态和输出
- 代码变更统计
- 时间线视图
- 支持导出会话摘要

**UI 设计:**
```
┌────────────────────────────────────────┐
│  ← 返回   修复用户登录 bug             │
├────────────────────────────────────────┤
│                                         │
│  📊 会话信息                            │
│  ID: sess_abc123                        │
│  状态: 🔴 进行中                        │
│  创建时间: 2026-07-02 14:30            │
│  持续时间: 15 分钟                      │
│                                         │
│  📈 代码变更统计                        │
│  +125 行 / -43 行 / 3 个文件           │
│                                         │
├────────────────────────────────────────┤
│  📜 执行历史                            │
├────────────────────────────────────────┤
│                                         │
│  14:30  💬 用户: 修复登录bug           │
│  14:31  🤖 AI: 我来帮你分析...         │
│  14:32  📝 编辑: src/auth/login.ts     │
│  14:33  ✅ 测试通过                     │
│  14:35  💬 用户: 再检查一下边界情况    │
│  14:36  🤖 AI: 正在检查...             │
│  14:45  📝 编辑: src/auth/login.test.ts│
│  ...                                    │
│                                         │
│  [导出摘要] [下载日志] [分享链接]      │
│                                         │
└────────────────────────────────────────┘
```

## 3. 后端 API 设计

### 3.1 实例管理 API

```go
// GET /api/opencode/instances
// 获取所有 OpenCode 实例
type InstancesResponse struct {
    Instances []OpenCodeInstance `json:"instances"`
}

type OpenCodeInstance struct {
    ID              string    `json:"id"`
    DisplayName     string    `json:"displayName"`
    BaseURL         string    `json:"baseUrl"`
    Status          string    `json:"status"` // online, offline, busy
    ActiveSessions  int       `json:"activeSessions"`
    TotalSessions   int       `json:"totalSessions"`
    LastHeartbeat   time.Time `json:"lastHeartbeat"`
    Capabilities    []string  `json:"capabilities"`
}

// POST /api/opencode/instances/register
// 注册新的 OpenCode 实例
type RegisterInstanceRequest struct {
    ID          string   `json:"id"`
    DisplayName string   `json:"displayName"`
    BaseURL     string   `json:"baseUrl"`
    ApiKey      string   `json:"apiKey,omitempty"`
}
```

### 3.2 会话管理 API

```go
// GET /api/opencode/sessions?instance_id=xxx&status=busy|idle|all
// 获取指定实例的会话列表
type SessionsResponse struct {
    Sessions []OpenCodeSession `json:"sessions"`
    Total    int              `json:"total"`
}

type OpenCodeSession struct {
    ID              string            `json:"id"`
    InstanceID      string            `json:"instanceId"`
    Title           string            `json:"title"`
    Status          string            `json:"status"` // busy, idle, retry
    CreatedAt       time.Time         `json:"createdAt"`
    UpdatedAt       time.Time         `json:"updatedAt"`
    MessageCount    int               `json:"messageCount"`
    FileChanges     FileChangeStats   `json:"fileChanges"`
    Duration        int64             `json:"duration"` // 秒
}

type FileChangeStats struct {
    Additions int `json:"additions"`
    Deletions int `json:"deletions"`
    Files     int `json:"files"`
}

// GET /api/opencode/sessions/{session_id}/history
// 获取会话的详细历史
type SessionHistoryResponse struct {
    SessionID string          `json:"sessionId"`
    Timeline  []HistoryEvent  `json:"timeline"`
}

type HistoryEvent struct {
    Timestamp   time.Time              `json:"timestamp"`
    Type        string                 `json:"type"` // message, edit, test, error
    Actor       string                 `json:"actor"` // user, ai, system
    Content     string                 `json:"content"`
    Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// GET /api/opencode/sessions/{session_id}/summary
// 获取会话摘要
type SessionSummaryResponse struct {
    SessionID    string    `json:"sessionId"`
    Summary      string    `json:"summary"`
    KeyActions   []string  `json:"keyActions"`
    FilesChanged []string  `json:"filesChanged"`
    GeneratedAt  time.Time `json:"generatedAt"`
}
```

### 3.3 实时状态 API (WebSocket)

```go
// WebSocket: /ws/opencode/status?instance_id=xxx
// 实时推送会话状态变化

type StatusUpdate struct {
    Type      string                 `json:"type"` // session_started, session_updated, session_completed
    SessionID string                 `json:"sessionId"`
    Status    string                 `json:"status"`
    Data      map[string]interface{} `json:"data"`
    Timestamp time.Time              `json:"timestamp"`
}
```

## 4. 数据库设计

### 4.1 opencode_sessions 表 (会话记录)

```sql
CREATE TABLE opencode_sessions (
    id              VARCHAR(64) PRIMARY KEY,
    instance_id     VARCHAR(64) NOT NULL,
    title           TEXT NOT NULL,
    status          VARCHAR(20) NOT NULL, -- busy, idle, retry, completed
    created_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMP,
    message_count   INTEGER DEFAULT 0,
    additions       INTEGER DEFAULT 0,
    deletions       INTEGER DEFAULT 0,
    files_changed   INTEGER DEFAULT 0,
    duration_secs   INTEGER DEFAULT 0,
    summary         TEXT,
    metadata        JSONB,
    
    INDEX idx_instance_id (instance_id),
    INDEX idx_status (status),
    INDEX idx_created_at (created_at DESC)
);
```

### 4.2 opencode_session_history 表 (会话历史事件)

```sql
CREATE TABLE opencode_session_history (
    id              SERIAL PRIMARY KEY,
    session_id      VARCHAR(64) NOT NULL,
    timestamp       TIMESTAMP NOT NULL DEFAULT NOW(),
    event_type      VARCHAR(32) NOT NULL, -- message, edit, test, error
    actor           VARCHAR(32) NOT NULL, -- user, ai, system
    content         TEXT,
    metadata        JSONB,
    
    FOREIGN KEY (session_id) REFERENCES opencode_sessions(id) ON DELETE CASCADE,
    INDEX idx_session_id (session_id),
    INDEX idx_timestamp (timestamp)
);
```

### 4.3 opencode_instances 表 (实例注册)

```sql
CREATE TABLE opencode_instances (
    id              VARCHAR(64) PRIMARY KEY,
    display_name    VARCHAR(255) NOT NULL,
    base_url        VARCHAR(512) NOT NULL,
    api_key         VARCHAR(255),
    status          VARCHAR(20) NOT NULL DEFAULT 'offline',
    last_heartbeat  TIMESTAMP,
    capabilities    TEXT[], -- {session, summary, pty, ...}
    metadata        JSONB,
    created_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    
    INDEX idx_status (status),
    INDEX idx_last_heartbeat (last_heartbeat DESC)
);
```

## 5. 前端 Store 设计

### 5.1 OpenCode Store (Pinia)

```typescript
// frontend/src/stores/opencode.ts
import { defineStore } from 'pinia'
import { api } from '@/api/client'

export const useOpenCodeStore = defineStore('opencode', {
  state: () => ({
    instances: [] as OpenCodeInstance[],
    selectedInstance: null as OpenCodeInstance | null,
    sessions: [] as OpenCodeSession[],
    sessionHistory: {} as Record<string, HistoryEvent[]>,
    realTimeStatus: {} as Record<string, string>,
    loading: false,
    error: null as string | null
  }),

  getters: {
    activeSessions: (state) => 
      state.sessions.filter(s => s.status === 'busy'),
    
    idleSessions: (state) => 
      state.sessions.filter(s => s.status === 'idle'),
    
    completedSessions: (state) => 
      state.sessions.filter(s => s.status === 'completed'),
    
    onlineInstances: (state) => 
      state.instances.filter(i => i.status === 'online')
  },

  actions: {
    async loadInstances() {
      this.loading = true
      try {
        const response = await api.get('/api/opencode/instances')
        this.instances = response.data.instances
      } catch (error) {
        this.error = error.message
      } finally {
        this.loading = false
      }
    },

    async selectInstance(instanceId: string) {
      const instance = this.instances.find(i => i.id === instanceId)
      if (instance) {
        this.selectedInstance = instance
        await this.loadSessions(instanceId)
      }
    },

    async loadSessions(instanceId: string) {
      this.loading = true
      try {
        const response = await api.get('/api/opencode/sessions', {
          params: { instance_id: instanceId }
        })
        this.sessions = response.data.sessions
      } catch (error) {
        this.error = error.message
      } finally {
        this.loading = false
      }
    },

    async loadSessionHistory(sessionId: string) {
      try {
        const response = await api.get(`/api/opencode/sessions/${sessionId}/history`)
        this.sessionHistory[sessionId] = response.data.timeline
      } catch (error) {
        this.error = error.message
      }
    },

    updateRealTimeStatus(sessionId: string, status: string) {
      this.realTimeStatus[sessionId] = status
      const session = this.sessions.find(s => s.id === sessionId)
      if (session) {
        session.status = status
      }
    }
  }
})
```

## 6. 实现优先级

### Phase 1: 基础架构 (1-2 天)
- ✅ 后端 OpenCode Manager 模块
- ✅ 数据库表设计和迁移
- ✅ 基础 REST API 实现
- ✅ 前端 OpenCode Store

### Phase 2: 实例与会话管理 (2-3 天)
- ✅ OpenCode Hub 界面
- ✅ 实例注册和发现
- ✅ 会话列表展示
- ✅ 基础状态同步

### Phase 3: 历史与详情 (2-3 天)
- ✅ 会话历史记录
- ✅ Session Detail 界面
- ✅ 时间线视图
- ✅ 摘要生成

### Phase 4: 实时监控 (1-2 天)
- ✅ WebSocket 实时状态推送
- ✅ 状态变化动画
- ✅ 心跳检测

### Phase 5: 高级功能 (可选)
- 🔲 会话搜索和筛选
- 🔲 导出和分享
- 🔲 性能监控和告警
- 🔲 会话重放

## 7. 技术栈

### 后端
- **语言**: Go 1.21+
- **框架**: 标准库 net/http + gorilla/websocket
- **数据库**: PostgreSQL 14+ (已有)
- **缓存**: Redis (可选，用于实时状态)

### 前端
- **框架**: Vue 3 + Composition API
- **状态管理**: Pinia
- **路由**: Vue Router
- **HTTP**: Axios
- **WebSocket**: 原生 WebSocket API
- **UI**: 自定义组件 + Ionic (移动端)

## 8. 部署架构

```
┌─────────────────────────────────────────────────────────┐
│  Nginx (56/252)                                          │
│  - 反向代理                                              │
│  - SSL 终止                                              │
│  - 静态资源                                              │
└─────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────┐
│  Pocket Backend (184:9010)                               │
│  - OpenCode Manager                                      │
│  - REST API                                              │
│  - WebSocket Server                                      │
└─────────────────────────────────────────────────────────┘
                          │
        ┌─────────────────┼─────────────────┐
        ▼                 ▼                 ▼
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│ OpenCode #1 │  │ OpenCode #2 │  │ OpenCode #N │
│  (71:3000)  │  │ (252:3000)  │  │   (...)     │
└─────────────┘  └─────────────┘  └─────────────┘
```

## 9. 关键设计决策

### 9.1 为什么不直接让前端连接 OpenCode?
- **统一管理**: 后端作为中间层，统一管理多个 OpenCode 实例
- **安全性**: 避免前端直接暴露各个 OpenCode 的访问地址和凭证
- **数据持久化**: 后端可以记录历史，OpenCode 本身可能不持久化
- **聚合能力**: 可以跨实例聚合数据和统计

### 9.2 为什么需要 WebSocket?
- **实时性**: OpenCode 会话状态变化需要实时推送到前端
- **长轮询替代**: WebSocket 比轮询更高效，减少服务器压力
- **双向通信**: 未来可以支持前端主动控制 OpenCode 会话

### 9.3 如何处理 OpenCode 实例宕机?
- **心跳检测**: 后端定期检查实例健康状态
- **降级处理**: 实例离线时，前端显示离线状态但保留历史数据
- **自动重连**: 实例恢复后自动重新建立连接

## 10. 下一步行动

1. **确认架构**: 和团队确认这个架构是否符合需求
2. **创建数据库迁移**: 创建上述三张表
3. **实现后端模块**: 先实现基础的实例和会话管理 API
4. **创建前端 Store**: 实现 OpenCode Store
5. **开发 UI 组件**: 按照设计稿实现 OpenCode Hub
6. **测试集成**: 在一个真实的 OpenCode 实例上测试

---

**需要我开始实现吗？我可以先从哪个模块开始？**
