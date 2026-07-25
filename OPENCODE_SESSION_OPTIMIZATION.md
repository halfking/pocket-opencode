# OpenCode 感知及会话管理 - 需求归纳与设计优化

## 文档版本
- 版本: v1.0
- 日期: 2026-07-06
- 作者: ZCode AI Assistant

---

## 一、当前实现现状分析

### 1.1 已实现功能

#### 后端 (Go)
| 模块 | 功能 | 状态 | 备注 |
|------|------|------|------|
| **Manager** | 会话缓存 (5分钟 TTL) | ✅ 已实现 | SessionCache + byInstance 索引 |
| **Manager** | 状态监控 (active/idle 推断) | ✅ 已实现 | 基于 lastSeenEvt 时间窗口 |
| **Manager** | 周期性状态刷新 | ✅ 已实现 | 30s 间隔, 5min 无事件=idle |
| **EventStreamManager** | SSE 事件订阅 | ✅ 已实现 | 自动重连 + 指数退避 |
| **EventStreamManager** | 事件扇出 | ✅ 已实现 | 多订阅者 + 缓冲 channel |
| **EventStreamManager** | 指标统计 | ✅ 已实现 | events/reconnects/errors |
| **PermissionManager** | 权限请求轮询 | ✅ 已实现 | 3s 间隔轮询 |
| **PermissionManager** | 权限审批/拒绝 | ✅ 已实现 | 事件发布 |
| **QuestionManager** | 问答请求轮询 | ✅ 已实现 | 3s 间隔轮询 |
| **QuestionManager** | 问题回答/拒绝 | ✅ 已实现 | 事件发布 |
| **Mobile Handler** | SSE 代理转发 | ✅ 已实现 | session 过滤 + 心跳 |
| **Mobile Handler** | 发送 Prompt | ✅ 已实现 | 异步返回 messageID |
| **Mobile Handler** | 中断执行 | ✅ 已实现 | POST interrupt |
| **Mobile Handler** | 历史消息 | ✅ 已实现 | 带 limit/limit/offset |

#### 前端 (Vue 3)
| 模块 | 功能 | 状态 | 备注 |
|------|------|------|------|
| **opencode Store** | 实例管理 | ✅ 已实现 | 加载/选择/刷新 |
| **opencode Store** | 会话列表 | ✅ 已实现 | active/idle 分组 |
| **opencode Store** | WebSocket 实时更新 | ✅ 已实现 | 自动重连 |
| **session Store** | 会话打开/关闭 | ✅ 已实现 | 历史加载 + SSE 订阅 |
| **session Store** | 消息流式更新 | ✅ 已实现 | text/reasoning/tool 事件 |
| **session Store** | 发送 Prompt | ✅ 已实现 | 用户消息即时显示 |
| **session Store** | 中断执行 | ✅ 已实现 | finalizeCurrentAssistant |
| **SessionSSEClient** | SSE 客户端 | ✅ 已实现 | EventSource + 重连 |
| **SessionListView** | 会话列表 UI | ✅ 已实现 | 卡片式布局 |
| **SessionDetailView** | 会话详情 UI | ✅ 已实现 | 信息卡片 + 时间线 |

### 1.2 已识别的问题和瓶颈

#### 架构层面问题

1. **SSE 连接冗余**
   - 问题: 每个移动端 SSE 客户端都会建立独立的上游 SSE 连接
   - 影响: N 个客户端 = N 个上游连接，资源浪费严重
   - 位置: `mobile_session_handler.go:162`

2. **会话状态推断不精确**
   - 问题: 基于 5 分钟无事件推断 idle，可能误判
   - 影响: 长时间思考的 AI 任务被标记为 idle
   - 位置: `manager.go:267-289`

3. **权限/问题轮询效率低**
   - 问题: 3 秒间隔轮询所有实例，延迟高且浪费资源
   - 影响: 用户响应延迟 0-3 秒
   - 位置: `permission_manager.go:129`, `question_manager.go:115`

4. **缓存一致性风险**
   - 问题: 会话缓存 5 分钟 TTL，可能读到过期数据
   - 影响: 用户看到过时的会话列表
   - 位置: `manager.go:119-135`

5. **事件类型硬编码**
   - 问题: 前端 SSE 客户端硬编码了所有事件类型
   - 影响: OpenCode 升级新增事件类型需要同步修改前端
   - 位置: `sse.ts:97-125`

#### 功能层面问题

1. **缺少会话删除/归档功能**
   - 状态: 文档规划但未实现
   - 影响: 用户无法清理不需要的会话

2. **缺少会话搜索功能**
   - 状态: 文档规划但未实现
   - 影响: 会话多时难以查找

3. **缺少 Token/成本统计**
   - 状态: 文档规划但未实现
   - 影响: 用户无法了解资源消耗

4. **缺少会话摘要生成**
   - 状态: 后端接口存在但功能不完整
   - 影响: 无法快速了解会话内容

5. **缺少配置管理功能**
   - 状态: 文档规划但未实现
   - 影响: 用户无法切换模型/Agent

---

## 二、核心需求归纳

### 2.1 功能需求矩阵

| 需求类别 | 需求项 | 优先级 | 当前状态 | 目标状态 |
|----------|--------|--------|----------|----------|
| **会话感知** | 实例状态感知 | P0 | ✅ 已实现 | 优化精度 |
| **会话感知** | 会话状态感知 | P0 | ✅ 已实现 | 优化精度 |
| **会话感知** | 执行进度感知 | P1 | 🔄 部分实现 | 完整实现 |
| **会话感知** | 错误状态感知 | P1 | 🔄 部分实现 | 完整实现 |
| **会话管理** | 创建会话 | P0 | ✅ 已实现 | 保持 |
| **会话管理** | 查看会话列表 | P0 | ✅ 已实现 | 保持 |
| **会话管理** | 查看会话详情 | P0 | ✅ 已实现 | 保持 |
| **会话管理** | 删除会话 | P1 | ❌ 未实现 | 新增 |
| **会话管理** | 归档会话 | P2 | ❌ 未实现 | 新增 |
| **会话管理** | 搜索会话 | P1 | ❌ 未实现 | 新增 |
| **会话交互** | 发送 Prompt | P0 | ✅ 已实现 | 保持 |
| **会话交互** | 中断执行 | P0 | ✅ 已实现 | 保持 |
| **会话交互** | 继续执行 | P1 | ❌ 未实现 | 新增 |
| **会话交互** | 等待完成 | P1 | ❌ 未实现 | 新增 |
| **会话交互** | 压缩会话 | P2 | ❌ 未实现 | 新增 |
| **审批交互** | 权限请求感知 | P0 | ✅ 已实现 | 优化延迟 |
| **审批交互** | 权限审批/拒绝 | P0 | ✅ 已实现 | 保持 |
| **审批交互** | 问题回答/拒绝 | P0 | ✅ 已实现 | 保持 |
| **配置管理** | 切换模型 | P2 | ❌ 未实现 | 新增 |
| **配置管理** | 切换 Agent | P2 | ❌ 未实现 | 新增 |
| **数据分析** | Token 统计 | P2 | ❌ 未实现 | 新增 |
| **数据分析** | 成本分析 | P2 | ❌ 未实现 | 新增 |
| **数据分析** | 代码变更统计 | P1 | 🔄 部分实现 | 完整实现 |

### 2.2 非功能需求

| 需求类别 | 需求项 | 指标 | 当前状态 | 目标状态 |
|----------|--------|------|----------|----------|
| **性能** | SSE 连接数 | 单实例单连接 | N 个客户端 N 个连接 | 共享连接 |
| **性能** | 状态更新延迟 | < 1 秒 | 0-3 秒 (轮询) | 实时 (事件驱动) |
| **性能** | 会话列表加载 | < 500ms | 依赖网络 | 缓存优化 |
| **可靠性** | SSE 断线重连 | 自动重连 | ✅ 已实现 | 保持 |
| **可靠性** | 状态一致性 | 最终一致 | 🔄 部分实现 | 事件驱动 |
| **可扩展性** | 实例数量 | 支持 10+ | ✅ 已实现 | 保持 |
| **可扩展性** | 并发客户端 | 支持 100+ | 🔄 部分实现 | 连接池优化 |
| **可观测性** | 连接状态 | 可查询 | 🔄 部分实现 | 完整指标 |
| **可观测性** | 错误追踪 | 可追溯 | 🔄 部分实现 | 完整日志 |

---

## 三、设计优化方案

### 3.1 架构优化: SSE 连接共享

#### 当前问题
```
移动端客户端 A ──SSE──> 后端 ──SSE──> OpenCode 实例
移动端客户端 B ──SSE──> 后端 ──SSE──> OpenCode 实例  (重复连接!)
移动端客户端 C ──SSE──> 后端 ──SSE──> OpenCode 实例  (重复连接!)
```

#### 优化方案
```
移动端客户端 A ──SSE──> 后端 EventStreamManager ──SSE──> OpenCode 实例
移动端客户端 B ──SSE──>      (共享连接)            ──┐
移动端客户端 C ──SSE──>      (共享连接)            ──┘
```

#### 实现细节

```go
// 优化后的 EventStreamManager 已具备此能力
// 关键: 通过 Subscribe() 方法实现连接共享

// 后端为每个实例维护单一 SSE 连接
// 移动端通过 Subscribe() 获取事件 channel
type EventStreamManager struct {
    streams map[string]*instanceStream // 每个实例一个连接
    // ...
}

// Subscribe 返回 per-subscriber 的 channel
func (m *EventStreamManager) Subscribe(ctx context.Context, opts SubscribeOptions) (<-chan DomainEvent, func(), error) {
    stream, err := m.getOrCreateStream(ctx, opts) // 复用或创建连接
    sub := stream.addSubscriber(opts.BufferSize)
    return sub.ch, cleanup, nil
}
```

#### 前端适配
```typescript
// 移动端 SSE 客户端改为通过后端代理
// 后端 /api/mobile/sessions/{id}/event 内部使用 EventStreamManager.Subscribe()
// 而不是直接建立上游连接
```

### 3.2 架构优化: 事件驱动的状态管理

#### 当前问题
- 权限/问题轮询延迟 0-3 秒
- 会话状态基于时间窗口推断，不精确

#### 优化方案

**1. 权限/问题事件驱动**
```go
// 利用已有的 EventStreamManager 事件流
// 监听 permission.requested / question.asked 事件
// 立即触发 PermissionManager/QuestionManager 更新

type EventDrivenPermissionManager struct {
    eventStream *EventStreamManager
    // ...
}

func (m *EventDrivenPermissionManager) Start(ctx context.Context) {
    events, cleanup, _ := m.eventStream.Subscribe(ctx, SubscribeOptions{...})
    defer cleanup()
    
    for evt := range events {
        switch evt.Type {
        case "permission.requested":
            m.handleNewPermission(evt)
        case "permission.resolved":
            m.handleResolvedPermission(evt)
        }
    }
}
```

**2. 精确的会话状态**
```go
// 基于 OpenCode 事件精确判断状态
// 而不是时间窗口推断

func (m *Manager) OnSessionEvent(sessionID, eventType string) {
    switch eventType {
    case "session.next.prompted",
         "session.next.step.started",
         "session.next.shell.started",
         "session.next.text.delta",
         "session.next.tool.called":
        m.UpdateSessionStatus(sessionID, "active")
        
    case "session.next.step.ended",
         "session.next.shell.ended":
        // 检查是否还有进行中的步骤
        if !m.hasActiveSteps(sessionID) {
            m.UpdateSessionStatus(sessionID, "idle")
        }
    }
}
```

### 3.3 功能优化: 会话管理增强

#### 3.3.1 会话删除

**后端 API**
```go
// DELETE /api/mobile/sessions/{id}?instance_id=xxx
func (s *Server) handleMobileSessionDelete(w http.ResponseWriter, r *http.Request, sessionID string) {
    instanceID := r.URL.Query().Get("instance_id")
    apiBaseURL, _ := s.registry.GetInstanceAPIBase(instanceID)
    
    // 调用 OpenCode API 删除会话
    err := s.opencode.DeleteSession(r.Context(), apiBaseURL, sessionID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadGateway)
        return
    }
    
    // 清理本地缓存
    s.ocMgr.InvalidateCache(instanceID)
    
    w.WriteHeader(http.StatusNoContent)
}
```

**前端实现**
```typescript
async function deleteSession(sessionId: string, instanceId: string) {
  await http(`/api/mobile/sessions/${sessionId}?instance_id=${instanceId}`, {
    method: 'DELETE'
  })
  // 从列表移除
  sessions.value = sessions.value.filter(s => s.id !== sessionId)
}
```

#### 3.3.2 会话搜索

**后端 API**
```go
// GET /api/mobile/sessions/search?q=keyword&instance_id=xxx
func (s *Server) handleMobileSessionSearch(w http.ResponseWriter, r *http.Request) {
    query := r.URL.Query().Get("q")
    instanceID := r.URL.Query().Get("instance_id")
    
    // 从缓存中搜索
    sessions, _ := s.ocMgr.GetAllSessions(r.Context())
    
    results := filterSessions(sessions, query)
    writeJSON(w, http.StatusOK, results)
}
```

#### 3.3.3 会话摘要生成

**后端 API**
```go
// GET /api/mobile/sessions/{id}/summary?instance_id=xxx
func (s *Server) handleMobileSessionSummary(w http.ResponseWriter, r *http.Request, sessionID string) {
    instanceID := r.URL.Query().Get("instance_id")
    apiBaseURL, _ := s.registry.GetInstanceAPIBase(instanceID)
    
    // 获取消息历史
    messages, _ := s.opencode.GetMessages(r.Context(), apiBaseURL, sessionID, 50, "desc")
    
    // 使用 LLM 生成摘要
    summary, _ := s.llmGateway.Summarize(messages)
    
    writeJSON(w, http.StatusOK, map[string]string{
        "summary": summary,
    })
}
```

### 3.4 性能优化: 连接池和缓存

#### 3.4.1 HTTP 连接池
```go
// 优化 HTTP 客户端配置
func NewOpenCodeHTTPAdapter(timeoutMS int) *OpenCodeHTTPAdapter {
    transport := &http.Transport{
        MaxIdleConns:        100,
        MaxIdleConnsPerHost: 10,
        IdleConnTimeout:     90 * time.Second,
    }
    
    return &OpenCodeHTTPAdapter{
        client:  &http.Client{Transport: transport},
        timeout: time.Duration(timeoutMS) * time.Millisecond,
    }
}
```

#### 3.4.2 会话缓存优化
```go
// 增强缓存策略
type SessionCache struct {
    mu          sync.RWMutex
    sessions    map[string]*CachedSession
    byInstance  map[string][]string
    cachedAt    map[string]time.Time
    lastSeenEvt map[string]time.Time
    
    // 新增: 缓存版本号，用于增量更新
    version     map[string]uint64
}

// 增量更新而不是全量刷新
func (m *Manager) UpdateSessionCache(instanceID string, updates []*CachedSession) {
    m.sessionCache.mu.Lock()
    defer m.sessionCache.mu.Unlock()
    
    for _, update := range updates {
        existing, ok := m.sessionCache.sessions[update.ID]
        if !ok || existing.Version < update.Version {
            m.sessionCache.sessions[update.ID] = update
        }
    }
}
```

### 3.5 可观测性优化

#### 3.5.1 指标收集
```go
// 扩展指标收集
type Metrics struct {
    // SSE 连接指标
    ActiveSSEConnections   prometheus.Gauge
    SSEEventsTotal         prometheus.Counter
    SSEReconnectsTotal     prometheus.Counter
    
    // 会话指标
    ActiveSessions         prometheus.Gauge
    SessionOperationsTotal *prometheus.CounterVec
    
    // 权限/问题指标
    PendingPermissions     prometheus.Gauge
    PendingQuestions       prometheus.Gauge
    PermissionResponseTime prometheus.Histogram
    
    // 缓存指标
    CacheHitRate           prometheus.Gauge
    CacheSize              prometheus.Gauge
}
```

#### 3.5.2 健康检查增强
```go
// 增强健康检查
func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
    health := map[string]interface{}{
        "status": "ok",
        "components": map[string]interface{}{
            "opencode_adapter": s.checkOpencodeAdapter(),
            "event_stream":     s.checkEventStream(),
            "permission_mgr":   s.checkPermissionManager(),
            "question_mgr":     s.checkQuestionManager(),
            "session_cache":    s.checkSessionCache(),
        },
        "metrics": s.collectMetrics(),
    }
    
    writeJSON(w, http.StatusOK, health)
}
```

---

## 四、实施路线图

### Phase 1: 架构优化 (Week 1-2)
- [ ] 优化 SSE 连接共享机制
- [ ] 实现事件驱动的权限/问题管理
- [ ] 优化会话状态推断算法
- [ ] 增强缓存一致性

### Phase 2: 核心功能补全 (Week 3-4)
- [ ] 实现会话删除功能
- [ ] 实现会话搜索功能
- [ ] 实现会话摘要生成
- [ ] 实现继续执行/等待完成

### Phase 3: 高级功能 (Week 5-6)
- [ ] 实现会话归档功能
- [ ] 实现 Token/成本统计
- [ ] 实现配置管理 (模型/Agent 切换)
- [ ] 实现会话导出功能

### Phase 4: 优化和测试 (Week 7-8)
- [ ] 性能优化和压测
- [ ] 可观测性增强
- [ ] 单元测试和集成测试
- [ ] 文档更新

---

## 五、风险和缓解措施

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|----------|
| OpenCode API 变更 | 功能失效 | 中 | 适配器层隔离，版本兼容 |
| SSE 连接不稳定 | 实时性下降 | 高 | 自动重连 + 指数退避 |
| 缓存不一致 | 数据错误 | 中 | 版本号 + 增量更新 |
| 高并发压力 | 性能下降 | 低 | 连接池 + 限流 |
| 内存泄漏 | 服务崩溃 | 低 | 定期清理 + 监控告警 |

---

## 六、总结

本需求归纳和设计优化方案基于对现有代码的深入分析，识别了架构和功能层面的问题，并提出了针对性的优化方案。主要改进点包括:

1. **架构优化**: SSE 连接共享、事件驱动状态管理
2. **功能补全**: 会话删除/搜索/摘要等核心功能
3. **性能优化**: 连接池、缓存策略、增量更新
4. **可观测性**: 指标收集、健康检查、错误追踪

通过分阶段实施，可以在保证系统稳定性的前提下，逐步提升 OpenCode 感知和会话管理的能力。
