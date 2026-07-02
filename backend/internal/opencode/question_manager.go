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
//  1. Periodically polls each instance for pending question requests
//     (GET /api/session/:sessionID/question)
//  2. Caches pending requests in memory
//  3. Emits QuestionEvent values to subscribers
//  4. Forwards user answers (or rejections) to OpenCode
//     (POST /api/session/:sessionID/question/:requestID/{reply,reject})
type QuestionManager struct {
	registry *registry.Registry
	adapter  adapter.OpenCodeAdapter

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
func NewQuestionManager(reg *registry.Registry, ad adapter.OpenCodeAdapter, opts QuestionManagerOptions) *QuestionManager {
	interval := opts.PollInterval
	if interval <= 0 {
		interval = 3 * time.Second
	}
	return &QuestionManager{
		registry:     reg,
		adapter:      ad,
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

// Start begins polling all known instances for question requests.
func (m *QuestionManager) Start(ctx context.Context) {
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