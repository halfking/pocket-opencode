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

// QuestionManager orchestrates the question-request lifecycle for the mobile
// admin UI:
//
//  1. Subscribes to EventStreamManager for real-time question events
//     (question.asked, question.answered) - Phase 1.3 优化
//  2. Falls back to periodic polling for instances without event stream
//  3. Caches pending requests in memory
//  4. Emits QuestionEvent values to subscribers
//  5. Forwards user answers (or rejections) to OpenCode
//     (POST /api/session/:sessionID/question/:requestID/{reply,reject})
type QuestionManager struct {
	registry    *registry.Registry
	adapter     adapter.OpenCodeAdapter
	eventStream *EventStreamManager // Phase 1.3: 事件驱动

	mu      sync.RWMutex
	pending map[string]*pendingQuestion

	subsMu sync.RWMutex
	subs   map[uint64]chan QuestionEvent
	nextID uint64

	closed  bool
	closeCh chan struct{}

	pollInterval time.Duration
}

type pendingQuestion struct {
	InstanceID  string
	SessionID   string
	Request     adapter.QuestionRequest
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// QuestionEvent is emitted to subscribers on state changes.
type QuestionEvent struct {
	Type       string                    `json:"type"` // "new" | "resolved" | "rejected" | "expired"
	InstanceID string                    `json:"instanceId"`
	SessionID  string                    `json:"sessionId"`
	RequestID  string                    `json:"requestId,omitempty"`
	Request    *adapter.QuestionRequest  `json:"request,omitempty"`
	Answers    []adapter.QuestionAnswer  `json:"answers,omitempty"`
	Timestamp  time.Time                 `json:"timestamp"`
}

// QuestionManagerOptions configures the manager.
type QuestionManagerOptions struct {
	PollInterval time.Duration // default: 3s
}

// NewQuestionManager creates a new question manager.
// Phase 1.3: eventStream 参数支持事件驱动模式（可选，为 nil 时降级为轮询）
func NewQuestionManager(reg *registry.Registry, ad adapter.OpenCodeAdapter, opts QuestionManagerOptions, eventStream *EventStreamManager) *QuestionManager {
	interval := opts.PollInterval
	if interval <= 0 {
		interval = 3 * time.Second
	}
	return &QuestionManager{
		registry:     reg,
		adapter:      ad,
		eventStream:  eventStream,
		pending:      make(map[string]*pendingQuestion),
		subs:         make(map[uint64]chan QuestionEvent),
		closeCh:      make(chan struct{}),
		pollInterval: interval,
	}
}

// Subscribe returns a channel of question events and a cleanup function.
func (m *QuestionManager) Subscribe(bufferSize int) (<-chan QuestionEvent, func()) {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	m.nextID++
	id := m.nextID
	ch := make(chan QuestionEvent, bufferSize)
	m.subs[id] = ch
	return ch, func() {
		m.subsMu.Lock()
		defer m.subsMu.Unlock()
		if existing, ok := m.subs[id]; ok {
			close(existing)
			delete(m.subs, id)
		}
	}
}

// Start begins processing question requests.
// Phase 1.3: 优先使用事件驱动模式，降级为轮询模式。
func (m *QuestionManager) Start(ctx context.Context) {
	// Phase 1.3: 如果有 EventStreamManager，使用事件驱动模式
	if m.eventStream != nil {
		m.startEventDriven(ctx)
		return
	}
	// 降级：轮询模式
	m.startPolling(ctx)
}

// startEventDriven 事件驱动模式 - 实时响应问题事件
func (m *QuestionManager) startEventDriven(ctx context.Context) {
	log.Println("[question-mgr] starting in event-driven mode")

	// 订阅所有实例的事件
	instances := m.registry.ListInstances()
	for _, inst := range instances {
		if inst.Health != "healthy" {
			continue
		}
		go m.subscribeInstanceEvents(ctx, inst.ID)
	}

	// 保留低频轮询作为兜底（30秒间隔）
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.closeCh:
			return
		case <-ticker.C:
			// 兜底轮询：检查是否有遗漏的问题请求
			m.pollAllInstances(ctx)
		}
	}
}

// subscribeInstanceEvents 订阅单个实例的问题事件
func (m *QuestionManager) subscribeInstanceEvents(ctx context.Context, instanceID string) {
	events, cleanup, err := m.eventStream.Subscribe(ctx, SubscribeOptions{
		InstanceID: instanceID,
		BufferSize: 64,
	})
	if err != nil {
		log.Printf("[question-mgr] subscribe instance=%s failed: %v, falling back to polling", instanceID, err)
		return
	}
	defer cleanup()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.closeCh:
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			m.handleQuestionEvent(evt)
		}
	}
}

// handleQuestionEvent 处理问题相关事件
func (m *QuestionManager) handleQuestionEvent(evt DomainEvent) {
	sessionID := evt.SessionID

	switch evt.Type {
	case "question.asked":
		// 新问题请求
		if data, ok := evt.Raw.Data.(map[string]any); ok {
			m.handleNewQuestionFromEvent(evt.InstanceID, sessionID, data)
		}

	case "question.answered", "question.rejected":
		// 问题已回答/拒绝
		if data, ok := evt.Raw.Data.(map[string]any); ok {
			m.handleResolvedQuestionFromEvent(evt.InstanceID, sessionID, data)
		}
	}
}

// handleNewQuestionFromEvent 从事件中处理新问题请求
func (m *QuestionManager) handleNewQuestionFromEvent(instanceID, sessionID string, data map[string]any) {
	requestID, _ := data["id"].(string)
	if requestID == "" {
		requestID, _ = data["requestID"].(string)
	}
	if requestID == "" {
		return
	}

	key := permissionKey(instanceID, sessionID, requestID)
	now := time.Now()

	m.mu.Lock()
	existing, ok := m.pending[key]
	if ok {
		existing.LastSeenAt = now
		m.mu.Unlock()
		return
	}

	// 构建 QuestionRequest
	req := adapter.QuestionRequest{
		ID:        requestID,
		SessionID: sessionID,
	}
	// 从事件数据构建 Questions 数组
	if questions, ok := data["questions"].([]interface{}); ok {
		for _, q := range questions {
			if qMap, ok := q.(map[string]interface{}); ok {
				info := adapter.QuestionInfo{}
				if question, ok := qMap["question"].(string); ok {
					info.Question = question
				}
				if header, ok := qMap["header"].(string); ok {
					info.Header = header
				}
				if options, ok := qMap["options"].([]interface{}); ok {
					for _, opt := range options {
						if optMap, ok := opt.(map[string]interface{}); ok {
							optInfo := adapter.QuestionOption{}
							if label, ok := optMap["label"].(string); ok {
								optInfo.Label = label
							}
							if desc, ok := optMap["description"].(string); ok {
								optInfo.Description = desc
							}
							info.Options = append(info.Options, optInfo)
						}
					}
				}
				req.Questions = append(req.Questions, info)
			}
		}
	}

	m.pending[key] = &pendingQuestion{
		InstanceID:  instanceID,
		SessionID:   sessionID,
		Request:     req,
		FirstSeenAt: now,
		LastSeenAt:  now,
	}
	m.mu.Unlock()

	log.Printf("[question-mgr] new question from event: instance=%s session=%s request=%s", instanceID, sessionID, requestID)

	m.publish(QuestionEvent{
		Type:       "new",
		InstanceID: instanceID,
		SessionID:  sessionID,
		RequestID:  requestID,
		Request:    &req,
		Timestamp:  now,
	})
}

// handleResolvedQuestionFromEvent 从事件中处理已解决的问题
func (m *QuestionManager) handleResolvedQuestionFromEvent(instanceID, sessionID string, data map[string]any) {
	requestID, _ := data["id"].(string)
	if requestID == "" {
		requestID, _ = data["requestID"].(string)
	}
	if requestID == "" {
		return
	}

	key := permissionKey(instanceID, sessionID, requestID)

	m.mu.Lock()
	q, ok := m.pending[key]
	if ok {
		delete(m.pending, key)
	}
	m.mu.Unlock()

	if ok && q != nil {
		log.Printf("[question-mgr] resolved question from event: instance=%s session=%s request=%s", instanceID, sessionID, requestID)
		m.publish(QuestionEvent{
			Type:       "resolved",
			InstanceID: instanceID,
			SessionID:  sessionID,
			RequestID:  requestID,
			Timestamp:  time.Now(),
		})
	}
}

// startPolling 轮询模式 - 兼容无 EventStreamManager 的场景
func (m *QuestionManager) startPolling(ctx context.Context) {
	log.Println("[question-mgr] starting in polling mode")
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	m.pollAllInstances(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.closeCh:
			return
		case <-ticker.C:
			m.pollAllInstances(ctx)
		}
	}
}

// Close stops polling and closes all subscriber channels.
func (m *QuestionManager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.closeCh)
	m.mu.Unlock()

	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for id, ch := range m.subs {
		close(ch)
		delete(m.subs, id)
	}
}

// ListPending returns pending question requests, optionally filtered.
func (m *QuestionManager) ListPending(instanceID, sessionID string) []*adapter.QuestionRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make([]*adapter.QuestionRequest, 0)
	for _, q := range m.pending {
		if instanceID != "" && q.InstanceID != instanceID {
			continue
		}
		if sessionID != "" && q.SessionID != sessionID {
			continue
		}
		cp := q.Request
		results = append(results, &cp)
	}
	return results
}

// Reply forwards user answers to OpenCode and removes the request from the
// pending set.
func (m *QuestionManager) Reply(ctx context.Context, instanceID, sessionID, requestID string, answers []adapter.QuestionAnswer) error {
	caller, ok := m.adapter.(QuestionCaller)
	if !ok {
		return fmt.Errorf("adapter %T does not support question operations", m.adapter)
	}

	baseURL, err := m.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		return fmt.Errorf("resolve instance base URL: %w", err)
	}

	if err := caller.ReplyQuestion(ctx, baseURL, sessionID, requestID, answers); err != nil {
		return fmt.Errorf("reply question: %w", err)
	}

	key := permissionKey(instanceID, sessionID, requestID)
	m.mu.Lock()
	delete(m.pending, key)
	m.mu.Unlock()

	m.publish(QuestionEvent{
		Type:       "resolved",
		InstanceID: instanceID,
		SessionID:  sessionID,
		RequestID:  requestID,
		Answers:    answers,
		Timestamp:  time.Now(),
	})

	return nil
}

// Reject rejects a question request and removes it from the pending set.
func (m *QuestionManager) Reject(ctx context.Context, instanceID, sessionID, requestID string) error {
	caller, ok := m.adapter.(QuestionCaller)
	if !ok {
		return fmt.Errorf("adapter %T does not support question operations", m.adapter)
	}

	baseURL, err := m.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		return fmt.Errorf("resolve instance base URL: %w", err)
	}

	if err := caller.RejectQuestion(ctx, baseURL, sessionID, requestID); err != nil {
		return fmt.Errorf("reject question: %w", err)
	}

	key := permissionKey(instanceID, sessionID, requestID)
	m.mu.Lock()
	delete(m.pending, key)
	m.mu.Unlock()

	m.publish(QuestionEvent{
		Type:       "rejected",
		InstanceID: instanceID,
		SessionID:  sessionID,
		RequestID:  requestID,
		Timestamp:  time.Now(),
	})

	return nil
}

// =============================================================================
// Internal
// =============================================================================

func (m *QuestionManager) pollAllInstances(ctx context.Context) {
	instances := m.registry.ListInstances()
	for _, inst := range instances {
		if inst.Health != "healthy" {
			continue
		}
		go m.pollInstance(ctx, inst.ID)
	}
}

func (m *QuestionManager) pollInstance(ctx context.Context, instanceID string) {
	caller, ok := m.adapter.(QuestionCaller)
	if !ok {
		return
	}

	baseURL, err := m.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		return
	}

	requests, err := m.fetchPending(ctx, caller, instanceID, baseURL)
	if err != nil {
		log.Printf("[question-mgr] poll instance=%s failed: %v", instanceID, err)
		return
	}

	seen := make(map[string]bool, len(requests))
	now := time.Now()

	for _, req := range requests {
		key := permissionKey(instanceID, req.SessionID, req.ID)
		seen[key] = true

		m.mu.Lock()
		existing, ok := m.pending[key]
		if ok {
			existing.LastSeenAt = now
			existing.Request = req
			m.mu.Unlock()
			continue
		}

		m.pending[key] = &pendingQuestion{
			InstanceID:  instanceID,
			SessionID:   req.SessionID,
			Request:     req,
			FirstSeenAt: now,
			LastSeenAt:  now,
		}
		m.mu.Unlock()

		m.publish(QuestionEvent{
			Type:       "new",
			InstanceID: instanceID,
			SessionID:  req.SessionID,
			RequestID:  req.ID,
			Request:    &req,
			Timestamp:  now,
		})
	}

	m.mu.Lock()
	expired := make([]string, 0)
	for key, q := range m.pending {
		if q.InstanceID != instanceID {
			continue
		}
		if !seen[key] {
			expired = append(expired, key)
		}
	}
	m.mu.Unlock()

	for _, key := range expired {
		m.mu.Lock()
		q := m.pending[key]
		delete(m.pending, key)
		m.mu.Unlock()
		if q == nil {
			continue
		}
		m.publish(QuestionEvent{
			Type:       "expired",
			InstanceID: q.InstanceID,
			SessionID:  q.SessionID,
			RequestID:  q.Request.ID,
			Timestamp:  time.Now(),
		})
	}
}

func (m *QuestionManager) fetchPending(ctx context.Context, caller QuestionCaller, instanceID, baseURL string) ([]adapter.QuestionRequest, error) {
	allCaller, ok := m.adapter.(interface {
		GetAllPendingQuestionRequests(ctx context.Context, baseURL, directory, workspaceID string) ([]adapter.QuestionRequest, error)
	})
	if ok {
		reqs, err := allCaller.GetAllPendingQuestionRequests(ctx, baseURL, "", "")
		if err == nil {
			return reqs, nil
		}
		log.Printf("[question-mgr] unscoped endpoint failed for %s: %v", instanceID, err)
	}
	return nil, nil
}

func (m *QuestionManager) publish(evt QuestionEvent) {
	m.subsMu.RLock()
	defer m.subsMu.RUnlock()
	for _, ch := range m.subs {
		select {
		case ch <- evt:
		default:
			log.Printf("[question-mgr] dropping event for subscriber (buffer full)")
		}
	}
}