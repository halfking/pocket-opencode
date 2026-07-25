# OpenCode 移动端管理系统 - 需求分析与设计方案

## 项目背景

基于前期的源码分析和数据验证，现在需要实现一个**完整的 OpenCode 移动端管理系统**，类似 Cursor 的移动端产品，能够对 OpenCode 进行全面的配置和任务管理。

## 需求清单

### 1. 会话管理
- ✅ 查看会话列表
- 🆕 **创建新会话**
- 🆕 **删除会话**
- 🆕 **归档会话**
- ✅ 查看会话详情
- 🆕 **切换会话**

### 2. 会话操作
- 🆕 **发送消息/Prompt**
- 🆕 **中止当前执行**
- 🆕 **继续执行**
- 🆕 **等待执行完成**
- 🆕 **压缩会话（Compact）**

### 3. 审批与交互
- 🆕 **查看待审批的权限请求**
- 🆕 **批准/拒绝权限请求**
- 🆕 **回答 AI 提出的问题**
- 🆕 **查看实时执行状态**

### 4. 配置管理
- 🆕 **切换模型（Model）**
- 🆕 **切换 Agent**
- 🆕 **配置 Provider**
- 🆕 **管理凭证（Credentials）**

### 5. 会话分析
- 🆕 **动态生成会话摘要**
- 🆕 **轮次分析与总结**
- 🆕 **代码变更统计**
- 🆕 **Token 使用分析**
- 🆕 **成本分析**

### 6. 消息管理
- 🆕 **查看会话消息历史**
- 🆕 **搜索消息内容**
- 🆕 **导出会话数据**
- 🆕 **查看上下文（Context）**

### 7. 实时监控
- 🆕 **WebSocket 实时更新**
- 🆕 **执行进度推送**
- 🆕 **错误通知**
- 🆕 **完成提醒**

## OpenCode API 能力映射

基于前期源码分析，OpenCode 已提供的 HTTP API：

### ✅ 已有 API（可直接使用）

#### Session 管理
```
GET  /api/session                     # 列出会话
POST /api/session                     # 创建会话
GET  /api/session/:sessionID          # 获取会话详情
POST /api/session/:sessionID/prompt   # 发送 Prompt ⭐
POST /api/session/:sessionID/compact  # 压缩会话
POST /api/session/:sessionID/wait     # 等待执行完成
```

#### 消息管理
```
GET /api/session/:sessionID/message   # 获取消息列表
GET /api/session/:sessionID/context   # 获取上下文
```

#### 权限管理
```
GET  /api/permission/request          # 获取权限请求列表 ⭐
GET  /api/session/:sessionID/permission # 获取会话权限请求
POST /api/session/:sessionID/permission/:requestID/reply # 回复权限请求 ⭐
```

#### 模型与 Provider
```
GET /api/model                        # 列出可用模型
GET /api/provider                     # 列出 Providers
GET /api/provider/:providerID         # Provider 详情
```

#### Agent 管理
```
GET /api/agent                        # 列出 Agents
```

#### 事件流
```
GET /api/event                        # 订阅事件流（WebSocket/SSE）⭐
```

#### 健康检查
```
GET /api/health                       # 健康检查
```

### 🆕 需要实现的高级功能

#### 1. 会话控制
```
POST /api/session/:sessionID/interrupt      # 中止执行 ⭐
POST /api/session/:sessionID/resume         # 继续执行
POST /api/session/:sessionID/switch-model   # 切换模型
POST /api/session/:sessionID/switch-agent   # 切换 Agent
DELETE /api/session/:sessionID              # 删除会话
```

#### 2. 会话分析
```
POST /api/session/:sessionID/analyze        # 分析会话
GET  /api/session/:sessionID/summary        # 生成摘要
GET  /api/session/:sessionID/turns          # 轮次分析
GET  /api/session/:sessionID/stats          # 统计信息
```

#### 3. 问答交互
```
GET  /api/question/pending                  # 待回答问题列表 ⭐
POST /api/question/:questionID/reply        # 回答问题 ⭐
```

## 架构设计

### 整体架构

```
┌─────────────────────────────────────────────────────────────┐
│                    移动端（Vue 3 + Vant）                     │
│  ┌──────────┬──────────┬──────────┬──────────┬──────────┐   │
│  │ 会话管理 │ 实时监控 │ 审批中心 │ 配置管理 │ 数据分析 │   │
│  └──────────┴──────────┴──────────┴──────────┴──────────┘   │
└─────────────────────────┬───────────────────────────────────┘
                          │ REST API + WebSocket
┌─────────────────────────┴───────────────────────────────────┐
│              后端服务（Go + Gin + WebSocket）                 │
│  ┌──────────────────────────────────────────────────────┐   │
│  │          OpenCode 管理层（新增）                      │   │
│  │  - 会话控制器   - 审批管理器   - 实时推送             │   │
│  │  - 问答处理器   - 分析引擎     - 配置管理             │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │          OpenCode 适配器层（已有 + 扩展）             │   │
│  │  - HTTP 适配器  - 数据库适配器  - WebSocket 适配器   │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────┬───────────────────────────────────┘
                          │
┌─────────────────────────┴───────────────────────────────────┐
│              OpenCode 实例（HTTP API + SQLite）               │
│  - Session API      - Permission API    - Event Stream      │
│  - Model API        - Agent API         - Message API       │
└─────────────────────────────────────────────────────────────┘
```

### 关键模块设计

#### 1. 会话控制器（Session Controller）
```go
type SessionController struct {
    adapter     *OpenCodeHTTPAdapter
    eventStream *EventStreamManager
    permMgr     *PermissionManager
}

// 创建会话
func (c *SessionController) CreateSession(ctx context.Context, req CreateSessionRequest) (*Session, error)

// 发送 Prompt
func (c *SessionController) SendPrompt(ctx context.Context, sessionID, prompt string) (*PromptResponse, error)

// 中止执行
func (c *SessionController) Interrupt(ctx context.Context, sessionID string) error

// 继续执行
func (c *SessionController) Resume(ctx context.Context, sessionID string) error

// 切换模型
func (c *SessionController) SwitchModel(ctx context.Context, sessionID string, model ModelRef) error

// 等待完成
func (c *SessionController) Wait(ctx context.Context, sessionID string) error
```

#### 2. 审批管理器（Permission Manager）
```go
type PermissionManager struct {
    adapter *OpenCodeHTTPAdapter
    notifier *NotificationService
}

// 获取待审批列表
func (m *PermissionManager) GetPendingRequests(ctx context.Context, sessionID string) ([]*PermissionRequest, error)

// 批准请求
func (m *PermissionManager) Approve(ctx context.Context, requestID string, options ApprovalOptions) error

// 拒绝请求
func (m *PermissionManager) Reject(ctx context.Context, requestID string, reason string) error

// 订阅审批通知
func (m *PermissionManager) Subscribe(sessionID string, callback func(*PermissionRequest))
```

#### 3. 问答处理器（Question Handler）
```go
type QuestionHandler struct {
    adapter *OpenCodeHTTPAdapter
}

// 获取待回答问题
func (h *QuestionHandler) GetPendingQuestions(ctx context.Context, sessionID string) ([]*Question, error)

// 回答问题
func (h *QuestionHandler) Answer(ctx context.Context, questionID string, answer string) error
```

#### 4. 实时推送管理器（Event Stream Manager）
```go
type EventStreamManager struct {
    wsConn    *websocket.Conn
    listeners map[string][]EventListener
}

// 订阅会话事件
func (m *EventStreamManager) Subscribe(sessionID string, eventTypes []string, callback EventListener) error

// 推送到移动端
func (m *EventStreamManager) PushToMobile(event *Event) error

// 事件类型
// - session.created
// - session.updated
// - message.added
// - permission.requested
// - question.asked
// - execution.started
// - execution.completed
// - execution.error
```

#### 5. 会话分析引擎（Analysis Engine）
```go
type AnalysisEngine struct {
    dbAdapter *OpenCodeDBAdapter
    llmClient LLMClient
}

// 生成会话摘要
func (e *AnalysisEngine) GenerateSummary(ctx context.Context, sessionID string) (*Summary, error)

// 轮次分析
func (e *AnalysisEngine) AnalyzeTurns(ctx context.Context, sessionID string) ([]*TurnAnalysis, error)

// 代码变更分析
func (e *AnalysisEngine) AnalyzeCodeChanges(ctx context.Context, sessionID string) (*CodeChangeAnalysis, error)

// 成本分析
func (e *AnalysisEngine) AnalyzeCost(ctx context.Context, sessionID string) (*CostAnalysis, error)
```

## 前端页面结构

### 主要页面

#### 1. 会话列表页（Session List）
```vue
<template>
  <van-pull-refresh v-model="refreshing" @refresh="onRefresh">
    <van-list>
      <van-swipe-cell v-for="session in sessions" :key="session.id">
        <session-card 
          :session="session"
          @click="openSession"
          @interrupt="interruptSession"
          @delete="deleteSession"
        />
      </van-swipe-cell>
    </van-list>
    
    <!-- 浮动操作按钮 -->
    <van-floating-button @click="createNewSession">
      <van-icon name="plus" />
    </van-floating-button>
  </van-pull-refresh>
</template>
```

#### 2. 会话详情页（Session Detail）
```vue
<template>
  <div class="session-detail">
    <!-- 顶部状态栏 -->
    <session-status-bar 
      :status="session.status"
      :model="session.model"
      @switch-model="showModelPicker"
    />
    
    <!-- 消息列表 -->
    <message-list 
      :messages="messages"
      :loading="loading"
      @load-more="loadMoreMessages"
    />
    
    <!-- 底部输入框 -->
    <message-input
      v-model="inputText"
      :disabled="session.status !== 'idle'"
      @send="sendPrompt"
      @voice="startVoiceInput"
    />
    
    <!-- 审批通知 -->
    <permission-notification
      v-if="pendingPermissions.length > 0"
      :count="pendingPermissions.length"
      @click="openApprovalCenter"
    />
  </div>
</template>
```

#### 3. 审批中心页（Approval Center）
```vue
<template>
  <div class="approval-center">
    <van-tabs v-model="activeTab">
      <!-- 权限审批 -->
      <van-tab title="权限请求" :badge="permissionCount">
        <permission-request-list
          :requests="permissionRequests"
          @approve="approvePermission"
          @reject="rejectPermission"
        />
      </van-tab>
      
      <!-- 问答交互 -->
      <van-tab title="问答" :badge="questionCount">
        <question-list
          :questions="questions"
          @answer="answerQuestion"
        />
      </van-tab>
    </van-tabs>
  </div>
</template>
```

#### 4. 实时监控页（Real-time Monitor）
```vue
<template>
  <div class="monitor">
    <!-- 执行状态 -->
    <execution-status 
      :session="currentSession"
      :progress="executionProgress"
    />
    
    <!-- 实时日志 -->
    <log-stream 
      :events="realtimeEvents"
      :auto-scroll="true"
    />
    
    <!-- 控制按钮 -->
    <control-panel
      :can-interrupt="canInterrupt"
      :can-resume="canResume"
      @interrupt="interrupt"
      @resume="resume"
      @wait="wait"
    />
  </div>
</template>
```

#### 5. 数据分析页（Analytics）
```vue
<template>
  <div class="analytics">
    <!-- 概览卡片 -->
    <summary-cards :stats="sessionStats" />
    
    <!-- 图表区域 -->
    <van-tabs>
      <van-tab title="代码变更">
        <code-change-chart :data="codeChanges" />
      </van-tab>
      
      <van-tab title="Token 使用">
        <token-usage-chart :data="tokenUsage" />
      </van-tab>
      
      <van-tab title="成本分析">
        <cost-analysis-chart :data="costData" />
      </van-tab>
      
      <van-tab title="轮次分析">
        <turn-analysis-timeline :turns="turns" />
      </van-tab>
    </van-tabs>
  </div>
</template>
```

#### 6. 配置管理页（Settings）
```vue
<template>
  <div class="settings">
    <van-cell-group title="模型配置">
      <van-cell 
        title="默认模型" 
        :value="defaultModel"
        @click="selectDefaultModel"
      />
      <van-cell 
        title="Provider 管理"
        @click="navigateToProviders"
      />
    </van-cell-group>
    
    <van-cell-group title="Agent 配置">
      <van-cell 
        title="默认 Agent"
        :value="defaultAgent"
        @click="selectDefaultAgent"
      />
    </van-cell-group>
    
    <van-cell-group title="凭证管理">
      <credential-list
        :credentials="credentials"
        @add="addCredential"
        @delete="deleteCredential"
      />
    </van-cell-group>
  </div>
</template>
```

## 核心功能实现

### 1. 发送 Prompt（重要）

```go
// backend/internal/opencode/session_controller.go
func (c *SessionController) SendPrompt(ctx context.Context, req SendPromptRequest) (*PromptResponse, error) {
    // 调用 OpenCode API
    url := fmt.Sprintf("%s/api/session/%s/prompt", c.instanceURL, req.SessionID)
    
    payload := map[string]interface{}{
        "prompt": req.Prompt,
        "delivery": req.Delivery, // "steer" | "immediate" | "background"
        "resume": true,
    }
    
    resp, err := c.httpClient.Post(url, payload)
    if err != nil {
        return nil, err
    }
    
    // 订阅事件流监听执行
    c.eventStream.Subscribe(req.SessionID, []string{
        "execution.started",
        "execution.completed",
        "execution.error",
    }, func(event *Event) {
        c.notifyMobile(req.SessionID, event)
    })
    
    return resp, nil
}
```

```typescript
// frontend/src/services/opencode/session.ts
export async function sendPrompt(sessionId: string, prompt: string) {
  const response = await api.post(`/api/opencode/sessions/${sessionId}/prompt`, {
    prompt,
    delivery: 'steer',
    resume: true
  })
  
  // 订阅实时更新
  subscribeToSession(sessionId, (event) => {
    // 更新 UI
    updateSessionStatus(sessionId, event)
  })
  
  return response.data
}
```

### 2. 权限审批（重要）

```go
// backend/internal/opencode/permission_manager.go
func (m *PermissionManager) GetPendingRequests(ctx context.Context, sessionID string) ([]*PermissionRequest, error) {
    url := fmt.Sprintf("%s/api/session/%s/permission", m.instanceURL, sessionID)
    
    resp, err := m.httpClient.Get(url)
    if err != nil {
        return nil, err
    }
    
    var requests []*PermissionRequest
    json.Unmarshal(resp.Body, &requests)
    
    // 过滤待审批的
    pending := filterPending(requests)
    
    return pending, nil
}

func (m *PermissionManager) Approve(ctx context.Context, sessionID, requestID string, options ApprovalOptions) error {
    url := fmt.Sprintf("%s/api/session/%s/permission/%s/reply", 
        m.instanceURL, sessionID, requestID)
    
    payload := map[string]interface{}{
        "decision": "approve",
        "options": options,
    }
    
    _, err := m.httpClient.Post(url, payload)
    return err
}
```

### 3. 中止/继续执行（重要）

```go
// 基于源码分析，OpenCode 提供了 interrupt 方法
func (c *SessionController) Interrupt(ctx context.Context, sessionID string) error {
    // OpenCode 的 interrupt 是通过内部方法，没有直接的 HTTP 端点
    // 需要通过事件发布来实现
    
    url := fmt.Sprintf("%s/api/session/%s/interrupt", c.instanceURL, sessionID)
    _, err := c.httpClient.Post(url, nil)
    return err
}

// Resume 通过 prompt API 实现
func (c *SessionController) Resume(ctx context.Context, sessionID string) error {
    url := fmt.Sprintf("%s/api/session/%s/resume", c.instanceURL, sessionID)
    _, err := c.httpClient.Post(url, nil)
    return err
}
```

### 4. 实时事件流（重要）

```go
// backend/internal/opencode/event_stream.go
func (m *EventStreamManager) ConnectToOpenCode(instanceURL string) error {
    // 连接到 OpenCode 的事件流
    wsURL := strings.Replace(instanceURL, "http", "ws", 1) + "/api/event"
    
    conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
    if err != nil {
        return err
    }
    
    m.wsConn = conn
    
    // 开始接收事件
    go m.receiveEvents()
    
    return nil
}

func (m *EventStreamManager) receiveEvents() {
    for {
        var event Event
        err := m.wsConn.ReadJSON(&event)
        if err != nil {
            log.Printf("Error reading event: %v", err)
            continue
        }
        
        // 分发事件到订阅者
        m.dispatch(&event)
        
        // 推送到移动端
        m.pushToMobile(&event)
    }
}
```

### 5. 会话分析

```go
// backend/internal/opencode/analysis_engine.go
func (e *AnalysisEngine) GenerateSummary(ctx context.Context, sessionID string) (*Summary, error) {
    // 获取会话详情
    session, err := e.dbAdapter.GetSessionDetail(ctx, sessionID)
    if err != nil {
        return nil, err
    }
    
    // 获取消息列表
    messages, err := e.dbAdapter.GetSessionMessages(ctx, sessionID, 100)
    if err != nil {
        return nil, err
    }
    
    // 使用 LLM 生成摘要
    summary, err := e.llmClient.Summarize(messages)
    if err != nil {
        return nil, err
    }
    
    return &Summary{
        SessionID: sessionID,
        Title: session.Title,
        Summary: summary,
        CodeChanges: session.CodeChanges,
        Tokens: session.Tokens,
        Duration: session.TimeUpdated.Sub(session.TimeCreated),
    }, nil
}
```

## 数据模型

### 权限请求
```go
type PermissionRequest struct {
    ID          string    `json:"id"`
    SessionID   string    `json:"sessionId"`
    Type        string    `json:"type"` // bash, read, write, delete, etc.
    Description string    `json:"description"`
    Command     string    `json:"command,omitempty"`
    FilePath    string    `json:"filePath,omitempty"`
    Status      string    `json:"status"` // pending, approved, rejected
    CreatedAt   time.Time `json:"createdAt"`
}
```

### 问答记录
```go
type Question struct {
    ID        string    `json:"id"`
    SessionID string    `json:"sessionId"`
    Question  string    `json:"question"`
    Options   []string  `json:"options,omitempty"`
    Answer    string    `json:"answer,omitempty"`
    Status    string    `json:"status"` // pending, answered
    AskedAt   time.Time `json:"askedAt"`
}
```

### 执行状态
```go
type ExecutionStatus struct {
    SessionID   string    `json:"sessionId"`
    Status      string    `json:"status"` // idle, executing, waiting
    CurrentTask string    `json:"currentTask"`
    Progress    float64   `json:"progress"`
    StartedAt   time.Time `json:"startedAt"`
    UpdatedAt   time.Time `json:"updatedAt"`
}
```

## 技术栈

### 后端
- **语言**: Go 1.21+
- **框架**: Gin
- **WebSocket**: gorilla/websocket
- **数据库**: SQLite（OpenCode）+ PostgreSQL（自己的数据）
- **HTTP 客户端**: 标准库 net/http

### 前端
- **框架**: Vue 3 + TypeScript
- **UI 组件**: Vant 4（移动端）
- **状态管理**: Pinia
- **路由**: Vue Router 4
- **WebSocket**: 原生 WebSocket API
- **图表**: ECharts（数据分析）
- **请求库**: Axios

## 下一步实现计划

### Phase 1: 核心功能（Week 1-2）
1. ✅ HTTP 适配器扩展（新增控制 API）
2. ✅ 会话控制器实现
3. ✅ 权限管理器实现
4. ✅ WebSocket 事件流
5. ✅ 前端基础页面

### Phase 2: 审批与交互（Week 3）
1. ✅ 权限审批 UI
2. ✅ 问答交互 UI
3. ✅ 实时通知
4. ✅ 移动端推送

### Phase 3: 数据分析（Week 4）
1. ✅ 会话摘要生成
2. ✅ 轮次分析
3. ✅ 图表展示
4. ✅ 导出功能

### Phase 4: 配置管理（Week 5）
1. ✅ 模型切换
2. ✅ Agent 管理
3. ✅ Provider 配置
4. ✅ 凭证管理

### Phase 5: 优化与测试（Week 6）
1. ✅ 性能优化
2. ✅ 错误处理
3. ✅ 单元测试
4. ✅ 集成测试

## 参考资料

- **Cursor 移动端**: 研究其交互模式和功能设计
- **OpenCode 源码**: `~/workspace/ai/opencode`
- **已有的适配器**: 本项目已实现的 HTTP 和数据库适配器

## 总结

这是一个完整的 OpenCode 移动端管理系统设计，包含：

1. **完整的会话管理** - 创建、查看、控制、删除
2. **实时交互** - 发送 Prompt、审批权限、回答问题
3. **执行控制** - 中止、继续、等待
4. **配置管理** - 模型、Agent、Provider
5. **数据分析** - 摘要、轮次、成本、代码变更
6. **实时推送** - WebSocket 事件流

所有设计都基于之前的源码分析和真实数据验证，确保与 OpenCode 完全兼容。
