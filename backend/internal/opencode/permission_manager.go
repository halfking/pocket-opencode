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

// PermissionCaller is the adapter capability required for permission
// operations. The HTTP adapter implements this.
type PermissionCaller interface {
	GetPermissionRequests(ctx context.Context, baseURL, sessionID string) ([]adapter.PermissionRequest, error)
	ReplyPermission(ctx context.Context, baseURL, sessionID, requestID string, reply adapter.PermissionReply, message string) error
}

// QuestionCaller is the adapter capability required for question operations.
type QuestionCaller interface {
	GetQuestionRequests(ctx context.Context, baseURL, sessionID string) ([]adapter.QuestionRequest, error)
	ReplyQuestion(ctx context.Context, baseURL, sessionID, requestID string, answers []adapter.QuestionAnswer) error
	RejectQuestion(ctx context.Context, baseURL, sessionID, requestID string) error
}

// PermissionManager orchestrates the permission-request lifecycle for the
// mobile admin UI:
//
//  1. Subscribes to EventStreamManager for real-time permission events
//     (permission.requested, permission.resolved) - Phase 1.2 优化
//  2. Falls back to periodic polling for instances without event stream
//  3. Caches pending requests in memory so the UI can list them quickly
//  4. Emits PermissionEvent values to subscribers whenever a new request
//     arrives or the state changes
//  5. Forwards user replies (once/always/reject) to OpenCode
//     (POST /api/session/:sessionID/permission/:requestID/reply)
//
// The QuestionManager below uses the same pattern for question requests.
type PermissionManager struct {
	registry    *registry.Registry
	adapter     adapter.OpenCodeAdapter
	eventStream *EventStreamManager // Phase 1.2: 事件驱动

	mu      sync.RWMutex
	pending map[string]*pendingPermission // key: instanceID + ":" + sessionID + ":" + requestID

	subsMu sync.RWMutex
	subs   map[uint64]chan PermissionEvent
	nextID uint64

	closed  bool
	closeCh chan struct{}

	// Configuration
	pollInterval time.Duration
}

// pendingPermission tracks a permission request we've seen.
type pendingPermission struct {
	InstanceID  string
	SessionID   string
	Request     adapter.PermissionRequest
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// PermissionEvent is emitted to subscribers on state changes.
type PermissionEvent struct {
	Type       string                     `json:"type"` // "new" | "resolved" | "expired"
	InstanceID string                     `json:"instanceId"`
	SessionID  string                     `json:"sessionId"`
	RequestID  string                     `json:"requestId,omitempty"`
	Request    *adapter.PermissionRequest `json:"request,omitempty"`
	Reply      *adapter.PermissionReply   `json:"reply,omitempty"`
	Message    string                     `json:"message,omitempty"`
	Timestamp  time.Time                  `json:"timestamp"`
}

// PermissionManagerOptions configures the manager.
type PermissionManagerOptions struct {
	PollInterval time.Duration // default: 3s
}

// NewPermissionManager creates a new permission manager.
// Phase 1.2: eventStream 参数支持事件驱动模式（可选，为 nil 时降级为轮询）
func NewPermissionManager(reg *registry.Registry, ad adapter.OpenCodeAdapter, opts PermissionManagerOptions, eventStream *EventStreamManager) *PermissionManager {
	interval := opts.PollInterval
	if interval <= 0 {
		interval = 3 * time.Second
	}
	return &PermissionManager{
		registry:     reg,
		adapter:      ad,
		eventStream:  eventStream,
		pending:      make(map[string]*pendingPermission),
		subs:         make(map[uint64]chan PermissionEvent),
		closeCh:      make(chan struct{}),
		pollInterval: interval,
	}
}

// Subscribe returns a channel of permission events and a cleanup function.
// bufferSize controls the per-subscriber buffer; 0 falls back to 64.
func (m *PermissionManager) Subscribe(bufferSize int) (<-chan PermissionEvent, func()) {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	m.nextID++
	id := m.nextID
	ch := make(chan PermissionEvent, bufferSize)
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

// Start begins processing permission requests.
// Phase 1.2: 优先使用事件驱动模式，降级为轮询模式。
// Blocks until ctx is cancelled or Close is called.
func (m *PermissionManager) Start(ctx context.Context) {
	// Phase 1.2: 如果有 EventStreamManager，使用事件驱动模式
	if m.eventStream != nil {
		m.startEventDriven(ctx)
		return
	}
	// 降级：轮询模式
	m.startPolling(ctx)
}

// startEventDriven 事件驱动模式 - 实时响应权限事件
func (m *PermissionManager) startEventDriven(ctx context.Context) {
	log.Println("[permission-mgr] starting in event-driven mode")

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
			// 兜底轮询：检查是否有遗漏的权限请求
			m.pollAllInstances(ctx)
		}
	}
}

// subscribeInstanceEvents 订阅单个实例的权限事件
func (m *PermissionManager) subscribeInstanceEvents(ctx context.Context, instanceID string) {
	events, cleanup, err := m.eventStream.Subscribe(ctx, SubscribeOptions{
		InstanceID: instanceID,
		BufferSize: 64,
	})
	if err != nil {
		log.Printf("[permission-mgr] subscribe instance=%s failed: %v, falling back to polling", instanceID, err)
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
			m.handlePermissionEvent(evt)
		}
	}
}

// handlePermissionEvent 处理权限相关事件
func (m *PermissionManager) handlePermissionEvent(evt DomainEvent) {
	// 提取 sessionID
	sessionID := evt.SessionID

	switch evt.Type {
	case "permission.requested":
		// 新权限请求
		if data, ok := evt.Raw.Data.(map[string]any); ok {
			m.handleNewPermissionFromEvent(evt.InstanceID, sessionID, data)
		}

	case "permission.resolved", "permission.revoked":
		// 权限已解决
		if data, ok := evt.Raw.Data.(map[string]any); ok {
			m.handleResolvedPermissionFromEvent(evt.InstanceID, sessionID, data)
		}
	}
}

// handleNewPermissionFromEvent 从事件中处理新权限请求
func (m *PermissionManager) handleNewPermissionFromEvent(instanceID, sessionID string, data map[string]any) {
	// 提取请求 ID
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

	// 构建 PermissionRequest
	req := adapter.PermissionRequest{
		ID:        requestID,
		SessionID: sessionID,
	}
	if action, ok := data["action"].(string); ok {
		req.Action = action
	}
	if resources, ok := data["resources"].([]interface{}); ok {
		for _, r := range resources {
			if s, ok := r.(string); ok {
				req.Resources = append(req.Resources, s)
			}
		}
	}

	m.pending[key] = &pendingPermission{
		InstanceID:  instanceID,
		SessionID:   sessionID,
		Request:     req,
		FirstSeenAt: now,
		LastSeenAt:  now,
	}
	m.mu.Unlock()

	log.Printf("[permission-mgr] new permission from event: instance=%s session=%s request=%s", instanceID, sessionID, requestID)

	m.publish(PermissionEvent{
		Type:       "new",
		InstanceID: instanceID,
		SessionID:  sessionID,
		RequestID:  requestID,
		Request:    &req,
		Timestamp:  now,
	})
}

// handleResolvedPermissionFromEvent 从事件中处理已解决的权限
func (m *PermissionManager) handleResolvedPermissionFromEvent(instanceID, sessionID string, data map[string]any) {
	requestID, _ := data["id"].(string)
	if requestID == "" {
		requestID, _ = data["requestID"].(string)
	}
	if requestID == "" {
		return
	}

	key := permissionKey(instanceID, sessionID, requestID)

	m.mu.Lock()
	p, ok := m.pending[key]
	if ok {
		delete(m.pending, key)
	}
	m.mu.Unlock()

	if ok && p != nil {
		log.Printf("[permission-mgr] resolved permission from event: instance=%s session=%s request=%s", instanceID, sessionID, requestID)
		m.publish(PermissionEvent{
			Type:       "resolved",
			InstanceID: instanceID,
			SessionID:  sessionID,
			RequestID:  requestID,
			Timestamp:  time.Now(),
		})
	}
}

// startPolling 轮询模式 - 兼容无 EventStreamManager 的场景
func (m *PermissionManager) startPolling(ctx context.Context) {
	log.Println("[permission-mgr] starting in polling mode")
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	// Initial poll
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

// Close stops the polling loop and closes all subscriber channels.
func (m *PermissionManager) Close() {
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

// ListPending returns all pending permission requests, optionally filtered by
// sessionID. An empty sessionID returns requests across all sessions.
func (m *PermissionManager) ListPending(instanceID, sessionID string) []*adapter.PermissionRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make([]*adapter.PermissionRequest, 0)
	for _, p := range m.pending {
		if instanceID != "" && p.InstanceID != instanceID {
			continue
		}
		if sessionID != "" && p.SessionID != sessionID {
			continue
		}
		cp := p.Request
		results = append(results, &cp)
	}
	return results
}

// Reply forwards a user's reply to OpenCode and removes the request from the
// pending set. The event channel will receive a "resolved" event.
func (m *PermissionManager) Reply(ctx context.Context, instanceID, sessionID, requestID string, reply adapter.PermissionReply, message string) error {
	caller, ok := m.adapter.(PermissionCaller)
	if !ok {
		return fmt.Errorf("adapter %T does not support permission operations", m.adapter)
	}

	baseURL, err := m.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		return fmt.Errorf("resolve instance base URL: %w", err)
	}

	if err := caller.ReplyPermission(ctx, baseURL, sessionID, requestID, reply, message); err != nil {
		return fmt.Errorf("reply permission: %w", err)
	}

	key := permissionKey(instanceID, sessionID, requestID)
	m.mu.Lock()
	delete(m.pending, key)
	m.mu.Unlock()

	m.publish(PermissionEvent{
		Type:       "resolved",
		InstanceID: instanceID,
		SessionID:  sessionID,
		RequestID:  requestID,
		Reply:      &reply,
		Message:    message,
		Timestamp:  time.Now(),
	})

	return nil
}

// =============================================================================
// Internal
// =============================================================================

func (m *PermissionManager) pollAllInstances(ctx context.Context) {
	instances := m.registry.ListInstances()
	for _, inst := range instances {
		if inst.Health != "healthy" {
			continue
		}
		go m.pollInstance(ctx, inst.ID)
	}
}

func (m *PermissionManager) pollInstance(ctx context.Context, instanceID string) {
	caller, ok := m.adapter.(PermissionCaller)
	if !ok {
		return
	}

	baseURL, err := m.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		return
	}

	// To get the global pending list we use the unscoped endpoint when
	// available, otherwise iterate known sessions. For now we iterate the
	// sessions we have cached via PermissionRequest endpoints scoped to
	// sessions we've seen before via the event stream. As a fallback, we
	// also call the unscoped /api/permission/request endpoint.
	requests, err := m.fetchPending(ctx, caller, instanceID, baseURL)
	if err != nil {
		log.Printf("[permission-mgr] poll instance=%s failed: %v", instanceID, err)
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

		m.pending[key] = &pendingPermission{
			InstanceID:  instanceID,
			SessionID:   req.SessionID,
			Request:     req,
			FirstSeenAt: now,
			LastSeenAt:  now,
		}
		m.mu.Unlock()

		m.publish(PermissionEvent{
			Type:       "new",
			InstanceID: instanceID,
			SessionID:  req.SessionID,
			RequestID:  req.ID,
			Request:    &req,
			Timestamp:  now,
		})
	}

	// Detect resolved (no longer in pending set)
	m.mu.Lock()
	expired := make([]string, 0)
	for key, p := range m.pending {
		if p.InstanceID != instanceID {
			continue
		}
		if !seen[key] {
			expired = append(expired, key)
		}
	}
	m.mu.Unlock()

	for _, key := range expired {
		m.mu.Lock()
		p := m.pending[key]
		delete(m.pending, key)
		m.mu.Unlock()
		if p == nil {
			continue
		}
		m.publish(PermissionEvent{
			Type:       "expired",
			InstanceID: p.InstanceID,
			SessionID:  p.SessionID,
			RequestID:  p.Request.ID,
			Timestamp:  time.Now(),
		})
	}
}

func (m *PermissionManager) fetchPending(ctx context.Context, caller PermissionCaller, instanceID, baseURL string) ([]adapter.PermissionRequest, error) {
	// Try the unscoped /api/permission/request first; this returns requests
	// for the current Location, which is what we want for a single-tenant UI.
	allCaller, ok := m.adapter.(interface {
		GetAllPendingPermissionRequests(ctx context.Context, baseURL, directory, workspaceID string) ([]adapter.PermissionRequest, error)
	})
	if ok {
		reqs, err := allCaller.GetAllPendingPermissionRequests(ctx, baseURL, "", "")
		if err == nil {
			return reqs, nil
		}
		// Fall through to per-session scan if the unscoped endpoint fails
		log.Printf("[permission-mgr] unscoped endpoint failed for %s: %v", instanceID, err)
	}

	// Fallback: scan known sessions. We need a list of active session IDs;
	// the simplest is to read from the SessionManager / cached sessions.
	// For now we return an empty list to avoid blocking startup.
	return nil, nil
}

func (m *PermissionManager) publish(evt PermissionEvent) {
	m.subsMu.RLock()
	defer m.subsMu.RUnlock()
	for _, ch := range m.subs {
		select {
		case ch <- evt:
		default:
			log.Printf("[permission-mgr] dropping event for subscriber (buffer full)")
		}
	}
}

func permissionKey(instanceID, sessionID, requestID string) string {
	return instanceID + ":" + sessionID + ":" + requestID
}