package agent

// adapter_acp_stdio.go — 真实 ACP stdio adapter
//
// 用途：通过 StdioTransport 连接任何实现 ACP JSON-RPC 2.0 over stdio 的 agent。
// 适用于 Codex、Claude Code、Gemini CLI 等。

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// ACPStdioAdapter 实现 AgentAdapter，基于 stdio JSON-RPC 2.0 transport。
type ACPStdioAdapter struct {
	mu         sync.Mutex
	transports map[AgentRef]Transport // 每个 agent ref 一个 transport
}

// NewACPStdioAdapter 构造。
func NewACPStdioAdapter() *ACPStdioAdapter {
	return &ACPStdioAdapter{
		transports: make(map[AgentRef]Transport),
	}
}

// AdapterType 实现 AgentAdapter。
func (a *ACPStdioAdapter) AdapterType() string {
	return "acp-stdio"
}

// getOrCreateTransport 获取或创建 transport（懒加载）。
func (a *ACPStdioAdapter) getOrCreateTransport(ctx context.Context, ref AgentRef) (Transport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if tr, ok := a.transports[ref]; ok {
		return tr, nil
	}

	// 创建新 transport
	tr := NewStdioTransport(TransportConfig{
		AgentPath: ref.Target, // Target 是可执行文件路径
		AgentArgs: []string{},
	})

	if err := tr.Start(ctx); err != nil {
		return nil, NewUnreachableError(err)
	}

	a.transports[ref] = tr
	return tr, nil
}

// Capabilities 实现 AgentAdapter。
func (a *ACPStdioAdapter) Capabilities(ctx context.Context, ref AgentRef) (*AgentCapabilities, error) {
	// 所有 ACP agents 都支持完整协议
	return &AgentCapabilities{
		LoadSession:        true,
		ListSessions:       true,
		DeleteSession:      true,
		SetMode:            true,
		SetConfigOption:    false,
		PromptImage:        true,
		PromptAudio:        true,
		PromptEmbedCtx:     true,
		MCPHTTP:            false,
		MCPSSE:             false,
		Permission:         true,
		Question:           true,
		Streaming:          true,
	}, nil
}

// HealthCheck 实现 AgentAdapter。
func (a *ACPStdioAdapter) HealthCheck(ctx context.Context, ref AgentRef) error {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return err
	}

	// 调用 ACP initialize（幂等）
	var result map[string]any
	if err := tr.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "pocketd",
			"version": "1.0.0",
		},
	}, &result); err != nil {
		return NewUnreachableError(err)
	}

	return nil
}

// ListSessions 实现 AgentAdapter。
func (a *ACPStdioAdapter) ListSessions(ctx context.Context, ref AgentRef, opts ListOptions) ([]AgentSession, error) {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return nil, err
	}

	var result struct {
		Sessions []map[string]any `json:"sessions"`
	}

	if err := tr.Call(ctx, "session/list", map[string]any{
		"limit": opts.Limit,
		"order": opts.Order,
	}, &result); err != nil {
		return nil, NewProtocolError(fmt.Errorf("session/list failed: %w", err))
	}

	sessions := make([]AgentSession, 0, len(result.Sessions))
	for _, s := range result.Sessions {
		sessions = append(sessions, AgentSession{
			ID:     getString(s, "id"),
			Title:  getString(s, "title"),
			Status: getString(s, "status"),
		})
	}

	return sessions, nil
}

// CreateSession 实现 AgentAdapter。
func (a *ACPStdioAdapter) CreateSession(ctx context.Context, ref AgentRef, req *CreateSessionRequest) (*AgentSession, error) {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := tr.Call(ctx, "session/new", map[string]any{
		"title":      req.Title,
		"agent":      req.Agent,
		"model":      req.Model,
		"workingDir": req.WorkingDir,
	}, &result); err != nil {
		return nil, NewProtocolError(fmt.Errorf("session/new failed: %w", err))
	}

	return &AgentSession{
		ID:     getString(result, "id"),
		Title:  getString(result, "title"),
		Status: "idle",
	}, nil
}

// LoadSession 实现 AgentAdapter。
func (a *ACPStdioAdapter) LoadSession(ctx context.Context, ref AgentRef, sessionID string) (*AgentSession, error) {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := tr.Call(ctx, "session/load", map[string]any{
		"sessionId": sessionID,
	}, &result); err != nil {
		return nil, NewProtocolError(fmt.Errorf("session/load failed: %w", err))
	}

	return &AgentSession{
		ID:     getString(result, "id"),
		Title:  getString(result, "title"),
		Status: getString(result, "status"),
	}, nil
}

// DeleteSession 实现 AgentAdapter。
func (a *ACPStdioAdapter) DeleteSession(ctx context.Context, ref AgentRef, sessionID string) error {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return err
	}

	var result map[string]any
	if err := tr.Call(ctx, "session/delete", map[string]any{
		"sessionId": sessionID,
	}, &result); err != nil {
		return NewProtocolError(fmt.Errorf("session/delete failed: %w", err))
	}

	return nil
}

// GetMessages 实现 AgentAdapter。
func (a *ACPStdioAdapter) GetMessages(ctx context.Context, ref AgentRef, sessionID string, opts ListOptions) ([]AgentMessage, error) {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return nil, err
	}

	var result struct {
		Messages []map[string]any `json:"messages"`
	}

	if err := tr.Call(ctx, "session/messages", map[string]any{
		"sessionId": sessionID,
		"limit":     opts.Limit,
	}, &result); err != nil {
		return nil, NewProtocolError(fmt.Errorf("session/messages failed: %w", err))
	}

	messages := make([]AgentMessage, 0, len(result.Messages))
	for _, m := range result.Messages {
		messages = append(messages, AgentMessage{
			ID:        getString(m, "id"),
			SessionID: sessionID,
			Role:      getString(m, "role"),
			Parts:     []ContentBlock{}, // TODO: parse parts
		})
	}

	return messages, nil
}

// SendPrompt 实现 AgentAdapter。
func (a *ACPStdioAdapter) SendPrompt(ctx context.Context, ref AgentRef, sessionID string, req *SendPromptRequest) (*SendPromptResult, error) {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return nil, err
	}

	params := map[string]any{
		"sessionId": sessionID,
	}

	if req.Text != "" {
		params["text"] = req.Text
	}
	if len(req.Parts) > 0 {
		params["parts"] = req.Parts
	}

	var result map[string]any
	if err := tr.Call(ctx, "session/prompt", params, &result); err != nil {
		return nil, NewProtocolError(fmt.Errorf("session/prompt failed: %w", err))
	}

	return &SendPromptResult{
		MessageID: getString(result, "messageId"),
		Enqueued:  true,
	}, nil
}

// InterruptSession 实现 AgentAdapter。
func (a *ACPStdioAdapter) InterruptSession(ctx context.Context, ref AgentRef, sessionID string) error {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return err
	}

	var result map[string]any
	if err := tr.Call(ctx, "session/cancel", map[string]any{
		"sessionId": sessionID,
	}, &result); err != nil {
		return NewProtocolError(fmt.Errorf("session/cancel failed: %w", err))
	}

	return nil
}

// SetSessionMode 实现 AgentAdapter。
func (a *ACPStdioAdapter) SetSessionMode(ctx context.Context, ref AgentRef, sessionID, modeID string) error {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return err
	}

	var result map[string]any
	if err := tr.Call(ctx, "session/set_mode", map[string]any{
		"sessionId": sessionID,
		"modeId":    modeID,
	}, &result); err != nil {
		return NewProtocolError(fmt.Errorf("session/set_mode failed: %w", err))
	}

	return nil
}

// SubscribeEvents 实现 AgentAdapter。
//
// 通过 StdioTransport.Recv() 接收 agent 推送的 notifications（session/update
// 等），转换为 AgentEvent 流式推给前端。
//
// 设计：
//   - 每个 ref 一个独立 goroutine
//   - 用 transport.Recv() 拉帧，ParseFrame 分类
//   - 仅 notification 推给 events channel（response 已在 PendingCalls 内部处理）
//   - 上下文 cancel 自动停止 goroutine
//   - cleanup 函数取消订阅 + close channel
func (a *ACPStdioAdapter) SubscribeEvents(ctx context.Context, ref AgentRef) (<-chan AgentEvent, func(), error) {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return nil, nil, err
	}

	events := make(chan AgentEvent, 32)

	// 启动后台 goroutine：从 Recv 拉帧，转发到 events
		go func() {
		defer close(events)
		for {
			frame, err := tr.Recv(ctx)
			if err != nil {
				// ctx cancel 或 transport 关闭 → 退出
				return
			}
			// 解析 frame（id 对响应在 PendingCalls 已处理；这里只关心 notification）
			frameType, req, _, _, _ := ParseFrame(frame)
			if frameType != "notification" {
				continue
			}
			// req 包含 method + params
			ev := notificationToAgentEvent(req)
			if ev == nil {
				continue
			}
			select {
			case events <- *ev:
			case <-ctx.Done():
				return
			}
		}
	}()

	// cleanup 取消订阅（仅关闭 events channel，goroutine 由 ctx 触发退出）
	var closed bool
	cleanup := func() {
		if !closed {
			closed = true
			// 不 close(events)，让 goroutine 退出时自然 close
		}
	}
	return events, cleanup, nil
}

// notificationToAgentEvent 把 ACP session/update notification 转 AgentEvent。
//
// ACP notification 格式：
//
//	{
//	  "jsonrpc": "2.0",
//	  "method": "session/update",
//	  "params": {
//	    "sessionId": "sess_xxx",
//	    "update": {
//	      "sessionUpdate": "user_message_chunk" | "agent_message_chunk" | ...
//	      // 其他字段根据 sessionUpdate 类型而定
//	    }
//	  }
//	}
func notificationToAgentEvent(req *Request) *AgentEvent {
	if req == nil || req.Method != "session/update" {
		return nil
	}
	// params 是 json.RawMessage
	if len(req.Params) == 0 {
		return nil
	}
	var p struct {
		SessionID string         `json:"sessionId"`
		Update    map[string]any `json:"update"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil
	}
	ev := &AgentEvent{
		Type:      "session_update",
		SessionID: p.SessionID,
		Timestamp: time.Now(),
		Data:      p.Update,
	}
	return ev
}

// Close 关闭所有 transports。
func (a *ACPStdioAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for ref, tr := range a.transports {
		if err := tr.Close(); err != nil {
			// log error but continue closing other transports
			log.Printf("close transport %s: %v", ref, err)
		}
	}

	a.transports = make(map[AgentRef]Transport)
	return nil
}

// 编译期断言
var _ AgentAdapter = (*ACPStdioAdapter)(nil)

// ---- PermissionCapable 实现 ----

// ListPendingPermissions 列出指定 session 的待处理权限请求。
//
// ACP 协议：通过 JSON-RPC 调用 `session/permission/list`（agent-specific）
// 或累积从 `session/update` notification 收到的权限请求。简化实现：直接
// 调用 list 方法，agent 不支持时返回空切片（非错误）。
func (a *ACPStdioAdapter) ListPendingPermissions(ctx context.Context, ref AgentRef, sessionID string) ([]PermissionRequest, error) {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return nil, err
	}
	var result struct {
		Permissions []map[string]any `json:"permissions"`
	}
	if err := tr.Call(ctx, "session/permission/list", map[string]any{
		"sessionId": sessionID,
	}, &result); err != nil {
		// 协议未实现 → 返回空
		return nil, nil
	}
	perms := make([]PermissionRequest, 0, len(result.Permissions))
	for _, p := range result.Permissions {
		perms = append(perms, permissionRequestFromMap(p))
	}
	return perms, nil
}

// ReplyPermission 回复权限请求。
func (a *ACPStdioAdapter) ReplyPermission(ctx context.Context, ref AgentRef, sessionID, requestID string, reply PermissionDecision) error {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return err
	}
	if err := tr.Call(ctx, "session/permission/reply", map[string]any{
		"sessionId": sessionID,
		"requestId": requestID,
		"decision":  reply,
	}, nil); err != nil {
		return NewProtocolError(err)
	}
	return nil
}

// ---- QuestionCapable 实现 ----

// ListPendingQuestions 列出指定 session 的待处理问题。
func (a *ACPStdioAdapter) ListPendingQuestions(ctx context.Context, ref AgentRef, sessionID string) ([]Question, error) {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return nil, err
	}
	var result struct {
		Questions []map[string]any `json:"questions"`
	}
	if err := tr.Call(ctx, "session/question/list", map[string]any{
		"sessionId": sessionID,
	}, &result); err != nil {
		return nil, nil
	}
	qs := make([]Question, 0, len(result.Questions))
	for _, q := range result.Questions {
		qs = append(qs, questionFromMap(q))
	}
	return qs, nil
}

// ReplyQuestion 回复问题。
func (a *ACPStdioAdapter) ReplyQuestion(ctx context.Context, ref AgentRef, sessionID, requestID string, answers []QuestionAnswer) error {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return err
	}
	if err := tr.Call(ctx, "session/question/reply", map[string]any{
		"sessionId": sessionID,
		"requestId": requestID,
		"answers":   answers,
	}, nil); err != nil {
		return NewProtocolError(err)
	}
	return nil
}

// RejectQuestion 拒绝回答问题。
func (a *ACPStdioAdapter) RejectQuestion(ctx context.Context, ref AgentRef, sessionID, requestID string) error {
	tr, err := a.getOrCreateTransport(ctx, ref)
	if err != nil {
		return err
	}
	if err := tr.Call(ctx, "session/question/reject", map[string]any{
		"sessionId": sessionID,
		"requestId": requestID,
	}, nil); err != nil {
		return NewProtocolError(err)
	}
	return nil
}

// ---- 字段映射辅助函数 ----

// permissionRequestFromMap 把 JSON map 转 PermissionRequest。
func permissionRequestFromMap(m map[string]any) PermissionRequest {
	pr := PermissionRequest{
		ID:       getString(m, "id"),
		Action:   getString(m, "action"),
		Reason:   getString(m, "reason"),
		Metadata: m,
	}
	if optsRaw, ok := m["options"].([]any); ok {
		for _, o := range optsRaw {
			if om, ok := o.(map[string]any); ok {
				pr.Options = append(pr.Options, PermissionOption{
					ID:          getString(om, "id"),
					Label:       getString(om, "label"),
					Description: getString(om, "description"),
				})
			}
		}
	}
	return pr
}

// questionFromMap 把 JSON map 转 Question。
func questionFromMap(m map[string]any) Question {
	q := Question{
		ID:       getString(m, "id"),
		Prompt:   getString(m, "prompt"),
		Metadata: m,
	}
	if optsRaw, ok := m["options"].([]any); ok {
		for _, o := range optsRaw {
			if om, ok := o.(map[string]any); ok {
				q.Options = append(q.Options, QuestionOption{
					ID:          getString(om, "id"),
					Label:       getString(om, "label"),
					Description: getString(om, "description"),
					Preview:     getString(om, "preview"),
				})
			}
		}
	}
	if multi, ok := m["multi"].(bool); ok {
		q.Multi = multi
	}
	return q
}

// 编译期断言
var (
	_ PermissionCapable = (*ACPStdioAdapter)(nil)
	_ QuestionCapable   = (*ACPStdioAdapter)(nil)
)
