package opencode

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

// Manager OpenCode 管理器，负责实例管理、会话跟踪、历史记录
type Manager struct {
	registry      *registry.Registry
	adapter       adapter.OpenCodeAdapter
	sessionCache  *SessionCache
	historyStore  HistoryStore
	statusMonitor *StatusMonitor
}

// SessionCache 会话缓存
type SessionCache struct {
	mu          sync.RWMutex
	sessions    map[string]*CachedSession // key: sessionID
	byInstance  map[string][]string       // key: instanceID, value: sessionIDs
	cachedAt    map[string]time.Time      // key: instanceID, value: 缓存时间（用于 TTL 校验）
	lastSeenEvt map[string]time.Time      // key: sessionID, value: 最近一次事件时间（用于 active/idle 推断）
	// Phase 1.4: 精确状态跟踪
	activeSteps map[string]int // key: sessionID, value: 进行中的步骤数
}

// CachedSession 缓存的会话信息
type CachedSession struct {
	ID           string                 `json:"id"`
	InstanceID   string                 `json:"instanceId"`
	Title        string                 `json:"title"`
	Status       string                 `json:"status"`
	CreatedAt    time.Time              `json:"createdAt"`
	UpdatedAt    time.Time              `json:"updatedAt"`
	MessageCount int                    `json:"messageCount"`
	FileChanges  *FileChangeStats       `json:"fileChanges,omitempty"`
	Duration     int64                  `json:"duration"` // 秒
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// FileChangeStats 文件变更统计
type FileChangeStats struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
	Files     int `json:"files"`
}

// HistoryEvent 历史事件
type HistoryEvent struct {
	Timestamp time.Time              `json:"timestamp"`
	Type      string                 `json:"type"` // message, edit, test, error
	Actor     string                 `json:"actor"` // user, ai, system
	Content   string                 `json:"content"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// HistoryStore 历史存储接口
type HistoryStore interface {
	SaveEvent(ctx context.Context, sessionID string, event *HistoryEvent) error
	GetHistory(ctx context.Context, sessionID string, limit int) ([]*HistoryEvent, error)
}

// StatusMonitor 状态监控器
type StatusMonitor struct {
	mu            sync.RWMutex
	statusMap     map[string]string // key: sessionID, value: status
	updateChannel chan StatusUpdate
	subscribers   []chan StatusUpdate
}

// StatusUpdate 状态更新
type StatusUpdate struct {
	Type      string                 `json:"type"` // session_started, session_updated, session_completed
	SessionID string                 `json:"sessionId"`
	Status    string                 `json:"status"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// NewManager 创建 OpenCode 管理器
func NewManager(reg *registry.Registry, adapter adapter.OpenCodeAdapter, historyStore HistoryStore) *Manager {
	return &Manager{
		registry:      reg,
		adapter:       adapter,
		sessionCache:  newSessionCache(),
		historyStore:  historyStore,
		statusMonitor: newStatusMonitor(),
	}
}

func newSessionCache() *SessionCache {
	return &SessionCache{
		sessions:    make(map[string]*CachedSession),
		byInstance:  make(map[string][]string),
		cachedAt:    make(map[string]time.Time),
		lastSeenEvt: make(map[string]time.Time),
		activeSteps: make(map[string]int), // Phase 1.4
	}
}

func newStatusMonitor() *StatusMonitor {
	return &StatusMonitor{
		statusMap:     make(map[string]string),
		updateChannel: make(chan StatusUpdate, 100),
		subscribers:   make([]chan StatusUpdate, 0),
	}
}

// GetSessions 获取指定实例的会话列表（带缓存，5分钟 TTL）
func (m *Manager) GetSessions(ctx context.Context, instanceID string) ([]*CachedSession, error) {
	// 先从缓存获取
	m.sessionCache.mu.RLock()
	sessionIDs, exists := m.sessionCache.byInstance[instanceID]
	cachedTime, hasCacheTime := m.sessionCache.cachedAt[instanceID]
	if exists && hasCacheTime {
		// TTL 校验：5 分钟内有效
		if time.Since(cachedTime) < 5*time.Minute {
			sessions := make([]*CachedSession, 0, len(sessionIDs))
			for _, sid := range sessionIDs {
				if session, ok := m.sessionCache.sessions[sid]; ok {
					// 深拷贝避免调用方修改共享指针
					copied := *session
					sessions = append(sessions, &copied)
				}
			}
			m.sessionCache.mu.RUnlock()
			if len(sessions) > 0 {
				return sessions, nil
			}
		}
	}
	m.sessionCache.mu.RUnlock()

	// 缓存未命中，从 OpenCode 实例获取
	apiBaseURL, err := m.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		return nil, fmt.Errorf("get instance API base failed: %w", err)
	}

	rawSessions, err := m.adapter.ListSessions(ctx, apiBaseURL)
	if err != nil {
		return nil, fmt.Errorf("list sessions failed: %w", err)
	}

	// 转换并缓存
	sessions := make([]*CachedSession, 0, len(rawSessions))
	sessionIDs = make([]string, 0, len(rawSessions))
	
	m.sessionCache.mu.Lock()
	defer m.sessionCache.mu.Unlock()
	
	for _, raw := range rawSessions {
		cached := &CachedSession{
			ID:         raw.ID,
			InstanceID: instanceID,
			Title:      raw.Title,
			Status:     raw.Status,
			CreatedAt:  time.Now(), // OpenCode API 返回的时间需要解析
			UpdatedAt:  time.Now(),
		}
		
	m.sessionCache.sessions[raw.ID] = cached
	sessions = append(sessions, cached)
	sessionIDs = append(sessionIDs, raw.ID)
}

m.sessionCache.byInstance[instanceID] = sessionIDs
m.sessionCache.cachedAt[instanceID] = time.Now() // 记录缓存时间用于 TTL 校验

return sessions, nil
}

// GetSessionHistory 获取会话的详细历史
func (m *Manager) GetSessionHistory(ctx context.Context, sessionID string, limit int) ([]*HistoryEvent, error) {
	if m.historyStore == nil {
		return nil, fmt.Errorf("history store not configured")
	}

	return m.historyStore.GetHistory(ctx, sessionID, limit)
}

// GetSessionSummary 获取会话摘要
func (m *Manager) GetSessionSummary(ctx context.Context, instanceID, sessionID string) (string, error) {
	apiBaseURL, err := m.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		return "", fmt.Errorf("get instance API base failed: %w", err)
	}

	summary, err := m.adapter.GetSessionSummary(ctx, apiBaseURL, sessionID)
	if err != nil {
		return "", fmt.Errorf("get session summary failed: %w", err)
	}

	return summary, nil
}

// UpdateSessionStatus 更新会话状态
func (m *Manager) UpdateSessionStatus(sessionID, status string) {
	m.statusMonitor.mu.Lock()
	m.statusMonitor.statusMap[sessionID] = status
	m.statusMonitor.mu.Unlock()

	// 广播状态更新
	update := StatusUpdate{
		Type:      "session_updated",
		SessionID: sessionID,
		Status:    status,
		Timestamp: time.Now(),
	}
	
	select {
	case m.statusMonitor.updateChannel <- update:
	default:
		log.Printf("warning: status update channel full, dropping update for session %s", sessionID)
	}
}

// SubscribeStatusUpdates 订阅状态更新
func (m *Manager) SubscribeStatusUpdates() <-chan StatusUpdate {
	ch := make(chan StatusUpdate, 10)
	m.statusMonitor.mu.Lock()
	m.statusMonitor.subscribers = append(m.statusMonitor.subscribers, ch)
	m.statusMonitor.mu.Unlock()
	return ch
}

// OnSessionEvent 由 EventStreamManager 在收到事件时调用。
// Phase 1.4: 优化状态推断算法 - 基于步骤计数而非时间窗口。
func (m *Manager) OnSessionEvent(sessionID, eventType string) {
	if sessionID == "" {
		return
	}
	now := time.Now()

	m.sessionCache.mu.Lock()
	m.sessionCache.lastSeenEvt[sessionID] = now
	m.sessionCache.mu.Unlock()

	// 活跃事件类型：prompted/step-start/shell-start/text-delta/reasoning-delta
	switch eventType {
	case "session.next.prompted",
		"session.next.prompt.admitted",
		"session.next.context.updated":
		// 用户输入或上下文更新 -> 活跃
		m.UpdateSessionStatus(sessionID, "active")

	case "session.next.step.started":
		// 步骤开始 -> 增加活跃步骤计数
		m.sessionCache.mu.Lock()
		m.sessionCache.activeSteps[sessionID]++
		m.sessionCache.mu.Unlock()
		m.UpdateSessionStatus(sessionID, "active")

	case "session.next.shell.started",
		"session.next.tool.called":
		// 工具/Shell 开始 -> 活跃
		m.UpdateSessionStatus(sessionID, "active")

	case "session.next.text.delta",
		"session.next.reasoning.delta",
		"session.next.tool.input.delta":
		// 流式输出中 -> 活跃
		m.UpdateSessionStatus(sessionID, "active")

	case "session.next.step.ended":
		// 步骤结束 -> 减少活跃步骤计数
		m.sessionCache.mu.Lock()
		m.sessionCache.activeSteps[sessionID]--
		if m.sessionCache.activeSteps[sessionID] <= 0 {
			delete(m.sessionCache.activeSteps, sessionID)
			// 所有步骤完成，检查是否还有进行中的工具
			m.sessionCache.mu.Unlock()
			// 短延迟后检查是否真的空闲（可能有后续步骤）
			go func() {
				time.Sleep(2 * time.Second)
				m.sessionCache.mu.RLock()
				steps := m.sessionCache.activeSteps[sessionID]
				m.sessionCache.mu.RUnlock()
				if steps <= 0 {
					m.UpdateSessionStatus(sessionID, "idle")
				}
			}()
		} else {
			m.sessionCache.mu.Unlock()
		}

	case "session.next.shell.ended",
		"session.next.tool.success",
		"session.next.tool.failed":
		// 工具/Shell 结束 -> 检查是否所有活动都完成
		go func() {
			time.Sleep(1 * time.Second)
			m.sessionCache.mu.RLock()
			steps := m.sessionCache.activeSteps[sessionID]
			lastSeen := m.sessionCache.lastSeenEvt[sessionID]
			m.sessionCache.mu.RUnlock()
			// 如果没有进行中的步骤，且最近事件超过2秒，标记为空闲
			if steps <= 0 && time.Since(lastSeen) > 2*time.Second {
				m.UpdateSessionStatus(sessionID, "idle")
			}
		}()

	case "session.next.text.ended",
		"session.next.reasoning.ended",
		"session.next.compaction.ended":
		// 文本/推理结束 -> 不立即标记空闲，等待步骤结束

	case "session.completed":
		// 会话完成 -> 空闲
		m.UpdateSessionStatus(sessionID, "idle")
		m.sessionCache.mu.Lock()
		delete(m.sessionCache.activeSteps, sessionID)
		m.sessionCache.mu.Unlock()
	}
}

// RefreshStatuses 周期性根据 lastSeenEvt 和 activeSteps 推断 idle/active。
// Phase 1.4: 结合步骤计数和时间窗口，更精确判断状态。
func (m *Manager) RefreshStatuses(idleAfter time.Duration) {
	now := time.Now()
	m.sessionCache.mu.RLock()
	snapshot := make(map[string]time.Time, len(m.sessionCache.lastSeenEvt))
	stepsSnapshot := make(map[string]int, len(m.sessionCache.activeSteps))
	for k, v := range m.sessionCache.lastSeenEvt {
		snapshot[k] = v
	}
	for k, v := range m.sessionCache.activeSteps {
		stepsSnapshot[k] = v
	}
	m.sessionCache.mu.RUnlock()

	for sid, ts := range snapshot {
		// Phase 1.4: 优先检查步骤计数
		if steps, ok := stepsSnapshot[sid]; ok && steps > 0 {
			// 有进行中的步骤 -> 活跃
			m.statusMonitor.mu.RLock()
			cur := m.statusMonitor.statusMap[sid]
			m.statusMonitor.mu.RUnlock()
			if cur != "active" {
				m.UpdateSessionStatus(sid, "active")
			}
			continue
		}

		// 没有进行中的步骤，检查时间窗口
		status := "idle"
		if now.Sub(ts) < idleAfter {
			status = "active"
		}
		// 只在状态变化时写，避免噪声广播
		m.statusMonitor.mu.RLock()
		cur := m.statusMonitor.statusMap[sid]
		m.statusMonitor.mu.RUnlock()
		if cur != status {
			m.UpdateSessionStatus(sid, status)
		}
	}
}

// InvalidateCache 失效缓存
func (m *Manager) InvalidateCache(instanceID string) {
	m.sessionCache.mu.Lock()
	defer m.sessionCache.mu.Unlock()

	// 删除该实例的所有会话缓存
	if sessionIDs, exists := m.sessionCache.byInstance[instanceID]; exists {
		for _, sid := range sessionIDs {
			delete(m.sessionCache.sessions, sid)
		}
		delete(m.sessionCache.byInstance, instanceID)
	}
}

// GetAllSessions 获取所有实例的会话（聚合）
func (m *Manager) GetAllSessions(ctx context.Context) ([]*CachedSession, error) {
	instances := m.registry.ListInstances()
	
	allSessions := make([]*CachedSession, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, inst := range instances {
		wg.Add(1)
		go func(instanceID string) {
			defer wg.Done()
			
			sessions, err := m.GetSessions(ctx, instanceID)
			if err != nil {
				log.Printf("Failed to get sessions for instance %s: %v", instanceID, err)
				return
			}

			mu.Lock()
			allSessions = append(allSessions, sessions...)
			mu.Unlock()
		}(inst.ID)
	}

	wg.Wait()
	return allSessions, nil
}
