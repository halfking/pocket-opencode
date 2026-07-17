package agent

// adapter_mock.go — 测试用 mock AgentAdapter
//
// 简单实现：所有 session/message 存在内存中，prompt 立即返回固定响应。
// 用于集成测试 handler 层逻辑（无需真实 agent）。

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// MockAgentAdapter 是用于测试的 AgentAdapter 实现。
//
// 行为：
//   - CreateSession 立即返回新 session（ID 自增）
//   - SendPrompt 把 Text / Parts 拼成 agent_message 推回 SubscribeEvents
//   - ListSessions 返回所有创建的 session
//   - 错误注入：让 tests 模拟 unreachable / timeout
type MockAgentAdapter struct {
	mu        sync.Mutex
	sessions  map[string]*AgentSession
	messages  map[string][]AgentMessage // sessionID → messages
	nextID    atomic.Int64
	nextMsgID atomic.Int64

	// 错误注入
	forceErr error // 非 nil 时所有方法返回这个错误
}

// NewMockAgentAdapter 构造。
func NewMockAgentAdapter() *MockAgentAdapter {
	return &MockAgentAdapter{
		sessions: make(map[string]*AgentSession),
		messages: make(map[string][]AgentMessage),
	}
}

// AdapterType 实现 AgentAdapter。
func (m *MockAgentAdapter) AdapterType() string { return "mock" }

// SetForceErr 注入错误（用于测试错误路径）。
// 传 nil 清除。
func (m *MockAgentAdapter) SetForceErr(err error) {
	m.mu.Lock()
	m.forceErr = err
	m.mu.Unlock()
}

func (m *MockAgentAdapter) getErr() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.forceErr
}

// Capabilities 实现 AgentAdapter（mock 全支持）。
func (m *MockAgentAdapter) Capabilities(ctx context.Context, ref AgentRef) (*AgentCapabilities, error) {
	if e := m.getErr(); e != nil {
		return nil, e
	}
	return &AgentCapabilities{
		ListSessions: true, LoadSession: true, DeleteSession: true,
		SetMode: true, SetConfigOption: true,
		PromptImage: true, PromptAudio: true, PromptEmbedCtx: true,
		MCPHTTP: true, MCPSSE: true,
		Permission: true, Question: true, Streaming: true,
	}, nil
}

// HealthCheck 始终 OK。
func (m *MockAgentAdapter) HealthCheck(ctx context.Context, ref AgentRef) error {
	return m.getErr()
}

// ListSessions 返回所有 mock session。
func (m *MockAgentAdapter) ListSessions(ctx context.Context, ref AgentRef, opts ListOptions) ([]AgentSession, error) {
	if e := m.getErr(); e != nil {
		return nil, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AgentSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, *s)
	}
	return out, nil
}

// CreateSession 生成新 session。
func (m *MockAgentAdapter) CreateSession(ctx context.Context, ref AgentRef, req *CreateSessionRequest) (*AgentSession, error) {
	if e := m.getErr(); e != nil {
		return nil, e
	}
	if req == nil {
		req = &CreateSessionRequest{}
	}
	id := fmt.Sprintf("sess_mock_%d", m.nextID.Add(1))
	now := time.Now()
	s := &AgentSession{
		ID:         id,
		Title:      req.Title,
		Status:     "idle",
		Agent:      "mock",
		WorkingDir: req.WorkingDir,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.messages[id] = nil
	m.mu.Unlock()
	return s, nil
}

// LoadSession 返回 session（如果存在）。
func (m *MockAgentAdapter) LoadSession(ctx context.Context, ref AgentRef, sessionID string) (*AgentSession, error) {
	if e := m.getErr(); e != nil {
		return nil, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return nil, NewBadRequestError(404, "session not found", nil)
	}
	out := *s
	return &out, nil
}

// DeleteSession 移除 session。
func (m *MockAgentAdapter) DeleteSession(ctx context.Context, ref AgentRef, sessionID string) error {
	if e := m.getErr(); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
	delete(m.messages, sessionID)
	return nil
}

// GetMessages 返回 session 累积的 messages（按时间倒序）。
func (m *MockAgentAdapter) GetMessages(ctx context.Context, ref AgentRef, sessionID string, opts ListOptions) ([]AgentMessage, error) {
	if e := m.getErr(); e != nil {
		return nil, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := m.messages[sessionID]
	out := make([]AgentMessage, len(msgs))
	copy(out, msgs)
	return out, nil
}

// SendPrompt 把消息存入 session 并广播"完成"事件。
func (m *MockAgentAdapter) SendPrompt(ctx context.Context, ref AgentRef, sessionID string, req *SendPromptRequest) (*SendPromptResult, error) {
	if e := m.getErr(); e != nil {
		return nil, e
	}
	// 把 user message 存入
	userMsgID := fmt.Sprintf("msg_user_%d", m.nextMsgID.Add(1))
	userMsg := AgentMessage{
		ID:        userMsgID,
		SessionID: sessionID,
		Role:      "user",
		Parts:     promptToContentBlocks(req),
		Timestamp: time.Now(),
	}

	// mock 立即回 assistant 消息
	asstMsgID := fmt.Sprintf("msg_asst_%d", m.nextMsgID.Add(1))
	asstMsg := AgentMessage{
		ID:        asstMsgID,
		SessionID: sessionID,
		Role:      "assistant",
		Parts: []ContentBlock{
			{Type: "text", Text: "echo: " + promptText(req)},
		},
		Timestamp: time.Now(),
	}

	m.mu.Lock()
	m.messages[sessionID] = append(m.messages[sessionID], userMsg, asstMsg)
	m.mu.Unlock()

	return &SendPromptResult{
		MessageID:  userMsgID,
		Enqueued:   true,
		StopReason: "end_turn",
	}, nil
}

// InterruptSession 标记 session 为 busy（mock 行为）。
func (m *MockAgentAdapter) InterruptSession(ctx context.Context, ref AgentRef, sessionID string) error {
	if e := m.getErr(); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sessionID]; ok {
		s.Status = "idle"
	}
	return nil
}

// SetSessionMode 记录 mode 到 session metadata。
func (m *MockAgentAdapter) SetSessionMode(ctx context.Context, ref AgentRef, sessionID, modeID string) error {
	if e := m.getErr(); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sessionID]; ok {
		if s.Metadata == nil {
			s.Metadata = make(map[string]any)
		}
		s.Metadata["mode"] = modeID
	}
	return nil
}

// SubscribeEvents 返回一个 channel（mock 实现：立即发送一个 done 事件）。
func (m *MockAgentAdapter) SubscribeEvents(ctx context.Context, ref AgentRef) (<-chan AgentEvent, func(), error) {
	if e := m.getErr(); e != nil {
		return nil, nil, e
	}
	ch := make(chan AgentEvent, 8)
	cleanup := func() { close(ch) }
	go func() {
		// 模拟流式事件：发一个 chunk + done
		ch <- AgentEvent{
			Type:      "message_chunk",
			SessionID: ref.Target,
			Timestamp: time.Now(),
			Data:      map[string]any{"text": "thinking..."},
		}
		ch <- AgentEvent{
			Type:      "done",
			SessionID: ref.Target,
			Timestamp: time.Now(),
		}
	}()
	return ch, cleanup, nil
}

// 编译期断言：实现 AgentAdapter + PermissionCapable + QuestionCapable
var (
	_ AgentAdapter      = (*MockAgentAdapter)(nil)
	_ PermissionCapable = (*MockAgentAdapter)(nil)
	_ QuestionCapable   = (*MockAgentAdapter)(nil)
)

// ---- Optional capabilities (mock) ----

// ListPendingPermissions 返回空（mock 无未决权限请求）。
func (m *MockAgentAdapter) ListPendingPermissions(ctx context.Context, ref AgentRef, sessionID string) ([]PermissionRequest, error) {
	if e := m.getErr(); e != nil {
		return nil, e
	}
	return nil, nil
}

// ReplyPermission mock 始终成功。
func (m *MockAgentAdapter) ReplyPermission(ctx context.Context, ref AgentRef, sessionID, requestID string, reply PermissionDecision) error {
	return m.getErr()
}

// ListPendingQuestions 返回空。
func (m *MockAgentAdapter) ListPendingQuestions(ctx context.Context, ref AgentRef, sessionID string) ([]Question, error) {
	if e := m.getErr(); e != nil {
		return nil, e
	}
	return nil, nil
}

// ReplyQuestion mock 始终成功。
func (m *MockAgentAdapter) ReplyQuestion(ctx context.Context, ref AgentRef, sessionID, requestID string, answers []QuestionAnswer) error {
	return m.getErr()
}

// RejectQuestion mock 始终成功。
func (m *MockAgentAdapter) RejectQuestion(ctx context.Context, ref AgentRef, sessionID, requestID string) error {
	return m.getErr()
}

// ---- helpers ----

// promptToContentBlocks 把 SendPromptRequest 转 ContentBlock 列表。
func promptToContentBlocks(req *SendPromptRequest) []ContentBlock {
	out := make([]ContentBlock, 0)
	if req.Text != "" {
		out = append(out, ContentBlock{Type: "text", Text: req.Text})
	}
	out = append(out, req.Parts...)
	return out
}

// promptText 提取 prompt 的纯文本（用于 echo）。
func promptText(req *SendPromptRequest) string {
	if req.Text != "" {
		return req.Text
	}
	for _, p := range req.Parts {
		if p.Type == "text" && p.Text != "" {
			return p.Text
		}
	}
	return ""
}
