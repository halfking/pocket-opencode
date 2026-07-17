package agent

// adapter_opencode.go — 把现有 OpenCodeHTTPAdapter 包装为 AgentAdapter
//
// 设计：
//   - AgentSession ← OpenCodeSession 字段映射
//   - AgentMessage ← OpenCodeMessage（把 OpenCode 的 info{role} + parts[] 转为 ContentBlock[]）
//   - AgentEvent ← OpenCodeEvent（从 SSE 事件中提取 sessionID）
//   - 实现 PermissionCapable / QuestionCapable（用 type assertion 探测）
//
// 不变：
//   - OpenCodeHTTPAdapter 本身（internal/adapter/opencode_http.go）原样保留
//   - 错误用 OpenCodeError 透传，在 Server 层用 errors.As 提取

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
)

// OpenCodeAdapter 把 internal/adapter.OpenCodeHTTPAdapter 包装为 agent.AgentAdapter。
type OpenCodeAdapter struct {
	http *adapter.OpenCodeHTTPAdapter
}

// NewOpenCodeAdapter 构造。
func NewOpenCodeAdapter(http *adapter.OpenCodeHTTPAdapter) *OpenCodeAdapter {
	return &OpenCodeAdapter{http: http}
}

// AdapterType 实现 AgentAdapter。
func (a *OpenCodeAdapter) AdapterType() string { return "opencode" }

// Capabilities 声明 OpenCode 实际支持的能力子集。
//
// OpenCode 不实现 ACP 的 session/list / session/delete / authenticate /
// session/set_mode 等方法，所以这些 capability = false。
func (a *OpenCodeAdapter) Capabilities(ctx context.Context, ref AgentRef) (*AgentCapabilities, error) {
	return &AgentCapabilities{
		ListSessions:    true,
		Permission:      true,
		Question:        true,
		Streaming:       true,
		LoadSession:     false,
		DeleteSession:   true,
		SetMode:         false,
		SetConfigOption: false,
		PromptImage:     true,
		PromptAudio:     false,
		PromptEmbedCtx:  false,
		MCPHTTP:         false,
		MCPSSE:          false,
	}, nil
}

// HealthCheck 调 OpenCode /global/health。
func (a *OpenCodeAdapter) HealthCheck(ctx context.Context, ref AgentRef) error {
	return a.http.HealthCheck(ctx, ref.Target)
}

// ListSessions 调 OpenCode /session（OpenCode 不支持 cursor，limit/order 忽略）。
func (a *OpenCodeAdapter) ListSessions(ctx context.Context, ref AgentRef, opts ListOptions) ([]AgentSession, error) {
	ocSessions, err := a.http.ListSessions(ctx, ref.Target)
	if err != nil {
		return nil, translateOpenCodeError(err)
	}
	out := make([]AgentSession, 0, len(ocSessions))
	for _, s := range ocSessions {
		out = append(out, ocToAgentSession(s))
	}
	return out, nil
}

// CreateSession 调 OpenCode /session/new。
func (a *OpenCodeAdapter) CreateSession(ctx context.Context, ref AgentRef, req *CreateSessionRequest) (*AgentSession, error) {
	ocReq := &adapter.CreateSessionRequest{}
	// OpenCode 的 Model 字段是嵌套结构；这里传 nil（OpenCode 用默认）
	_ = nilStr(req.Agent)
	_ = nilStr(req.Model)
	// OpenCode HTTP 不直接接受 title；title 通过 SetSessionMetadata 间接设置
	info, err := a.http.CreateSession(ctx, ref.Target, ocReq)
	if err != nil {
		return nil, translateOpenCodeError(err)
	}
	createdAt := timeUnixMsToTime(info.Time.Created)
	return &AgentSession{
		ID:         info.ID,
		Title:      req.Title,
		Status:     "idle",
		Agent:      "opencode",
		WorkingDir: req.WorkingDir,
		CreatedAt:  createdAt,
		UpdatedAt:  createdAt,
		Metadata: map[string]any{
			"parentID":  strFromAny(info.ParentID),
			"projectID": info.ProjectID,
			"opencode":  info,
		},
	}, nil
}

// LoadSession 不支持（OpenCode 没有 reload session API）。
func (a *OpenCodeAdapter) LoadSession(ctx context.Context, ref AgentRef, sessionID string) (*AgentSession, error) {
	return nil, NewCapabilityError("loadSession")
}

// DeleteSession 调 OpenCode /session/:id DELETE。
func (a *OpenCodeAdapter) DeleteSession(ctx context.Context, ref AgentRef, sessionID string) error {
	if err := a.http.DeleteSession(ctx, ref.Target, sessionID); err != nil {
		return translateOpenCodeError(err)
	}
	return nil
}

// GetMessages 调 OpenCode /session/:id/messages。
func (a *OpenCodeAdapter) GetMessages(ctx context.Context, ref AgentRef, sessionID string, opts ListOptions) ([]AgentMessage, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	order := opts.Order
	if order == "" {
		order = "desc"
	}
	ocMsgs, err := a.http.GetMessages(ctx, ref.Target, sessionID, limit, order)
	if err != nil {
		return nil, translateOpenCodeError(err)
	}
	out := make([]AgentMessage, 0, len(ocMsgs))
	for _, m := range ocMsgs {
		out = append(out, ocToAgentMessage(sessionID, m))
	}
	return out, nil
}

// SendPrompt 调 OpenCode /session/:id/prompt。
func (a *OpenCodeAdapter) SendPrompt(ctx context.Context, ref AgentRef, sessionID string, req *SendPromptRequest) (*SendPromptResult, error) {
	// OpenCode 的 SendPromptRequest.Parts 是 []PromptPart
	// 把简单文本 + 多 ContentBlock 都转成 parts
	parts := make([]adapter.PromptPart, 0)
	if req.Text != "" {
		parts = append(parts, adapter.PromptPart{Type: "text", Text: req.Text})
	}
	for _, p := range req.Parts {
		parts = append(parts, adapter.PromptPart{
			Type: p.Type,
			Text: p.Text,
			URL:  p.URL,
		})
	}

	ocReq := &adapter.SendPromptRequest{Parts: parts}
	resp, err := a.http.SendPrompt(ctx, ref.Target, sessionID, ocReq)
	if err != nil {
		return nil, translateOpenCodeError(err)
	}
	return &SendPromptResult{
		MessageID: resp.MessageID,
		Enqueued:  resp.Enqueued,
	}, nil
}

// InterruptSession 调 OpenCode /session/:id/interrupt。
func (a *OpenCodeAdapter) InterruptSession(ctx context.Context, ref AgentRef, sessionID string) error {
	if err := a.http.InterruptSession(ctx, ref.Target, sessionID); err != nil {
		return translateOpenCodeError(err)
	}
	return nil
}

// SetSessionMode 不支持（OpenCode 无 mode API）。
func (a *OpenCodeAdapter) SetSessionMode(ctx context.Context, ref AgentRef, sessionID, modeID string) error {
	return NewCapabilityError("setMode")
}

// SubscribeEvents 包装 OpenCode SSE 事件流为 AgentEvent。
func (a *OpenCodeAdapter) SubscribeEvents(ctx context.Context, ref AgentRef) (<-chan AgentEvent, func(), error) {
	src, cleanup, err := a.http.SubscribeEvents(ctx, ref.Target, "", "")
	if err != nil {
		return nil, cleanup, translateOpenCodeError(err)
	}
	out := make(chan AgentEvent, 32)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-src:
				if !ok {
					return
				}
				out <- ocToAgentEvent(evt)
			}
		}
	}()
	return out, cleanup, nil
}

// 编译期断言：实现 PermissionCapable / QuestionCapable。
var (
	_ AgentAdapter      = (*OpenCodeAdapter)(nil)
	_ PermissionCapable = (*OpenCodeAdapter)(nil)
	_ QuestionCapable   = (*OpenCodeAdapter)(nil)
)

// ListPendingPermissions 实现 PermissionCapable。
func (a *OpenCodeAdapter) ListPendingPermissions(ctx context.Context, ref AgentRef, sessionID string) ([]PermissionRequest, error) {
	// OpenCode 的 GetAllPendingPermissionRequests 需要 (instanceURL, directory, workspaceID)
	// 简化为全局扫描（directory="" 匹配所有）
	perms, err := a.http.GetAllPendingPermissionRequests(ctx, ref.Target, "", "")
	if err != nil {
		return nil, translateOpenCodeError(err)
	}
	out := make([]PermissionRequest, 0, len(perms))
	for _, p := range perms {
		out = append(out, PermissionRequest{
			ID:        p.ID,
			SessionID: p.SessionID,
			Tool:      p.Action, // OpenCode 的 Action 字段（"bash"/"edit" 等）
			Action:    p.Action,
			Options: []PermissionOption{
				{ID: "once", Label: "once"},
				{ID: "always", Label: "always"},
				{ID: "reject", Label: "reject"},
			},
			Metadata: map[string]any{
				"resources": p.Resources,
				"save":      p.Save,
				"source":    p.Source,
				"raw":       p,
			},
		})
	}
	return out, nil
}

// ReplyPermission 实现 PermissionCapable。
func (a *OpenCodeAdapter) ReplyPermission(ctx context.Context, ref AgentRef, sessionID, requestID string, reply PermissionDecision) error {
	// OpenCode 的 ReplyPermission 需要 instanceID, sessionID, requestID
	// 但 PermissionDecision 的 OptionID 映射到 OpenCode 的 reply enum
	var ocReply adapter.PermissionReply
	switch reply.OptionID {
	case "allow_once":
		ocReply = adapter.PermissionReplyOnce
	case "allow_always":
		ocReply = adapter.PermissionReplyAlways
	case "deny":
		ocReply = adapter.PermissionReplyReject
	default:
		return NewBadRequestError(400, "unknown permission option", nil)
	}
	if err := a.http.ReplyPermission(ctx, ref.Target, sessionID, requestID, ocReply, reply.Message); err != nil {
		return translateOpenCodeError(err)
	}
	return nil
}

// ListPendingQuestions 实现 QuestionCapable。
func (a *OpenCodeAdapter) ListPendingQuestions(ctx context.Context, ref AgentRef, sessionID string) ([]Question, error) {
	// OpenCode 的 GetAllPendingQuestionRequests 返回 []QuestionRequest
	// 每个 QuestionRequest 含多个 QuestionInfo → 我们扁平化成 []Question
	rawList, err := a.http.GetAllPendingQuestionRequests(ctx, ref.Target, "", "")
	if err != nil {
		return nil, translateOpenCodeError(err)
	}
	var out []Question
	for _, q := range rawList {
		out = append(out, ocToAgentQuestion(q)...)
	}
	return out, nil
}

// ReplyQuestion 实现 QuestionCapable。
//
// OpenCode 的 ReplyQuestion 接收 []QuestionAnswer（每个 QuestionAnswer 是
// []string 选项标签数组）。Agent 的 []QuestionAnswer 含多个 Question 的
// answers（map），这里简化为第一个非空 OptionID。
func (a *OpenCodeAdapter) ReplyQuestion(ctx context.Context, ref AgentRef, sessionID, requestID string, answers []QuestionAnswer) error {
	ocAnswers := make([]adapter.QuestionAnswer, 0, len(answers))
	for _, a := range answers {
		ocAnswers = append(ocAnswers, adapter.QuestionAnswer{
			firstNonEmpty(a.OptionIDs...),
		})
	}
	if err := a.http.ReplyQuestion(ctx, ref.Target, sessionID, requestID, ocAnswers); err != nil {
		return translateOpenCodeError(err)
	}
	return nil
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// RejectQuestion 实现 QuestionCapable。
func (a *OpenCodeAdapter) RejectQuestion(ctx context.Context, ref AgentRef, sessionID, requestID string) error {
	if err := a.http.RejectQuestion(ctx, ref.Target, sessionID, requestID); err != nil {
		return translateOpenCodeError(err)
	}
	return nil
}

// ---- 字段映射 helpers ----

// ocToAgentSession 把 OpenCodeSession 转 AgentSession。
func ocToAgentSession(s adapter.OpenCodeSession) AgentSession {
	return AgentSession{
		ID:     s.ID,
		Title:  s.Title,
		Status: openCodeStatusToAgent(s.Status),
		Agent:  "opencode",
	}
}

// ocToAgentMessage 把 OpenCodeMessage 转 AgentMessage。
func ocToAgentMessage(sessionID string, m adapter.OpenCodeMessage) AgentMessage {
	role := "assistant"
	parts := []ContentBlock{}

	// OpenCode 的消息结构：Data map 里有 info{role} + parts[]
	if info, ok := m.Data["info"].(map[string]any); ok {
		if r, ok := info["role"].(string); ok {
			role = r
		}
	}
	if partsRaw, ok := m.Data["parts"].([]any); ok {
		for _, p := range partsRaw {
			if pm, ok := p.(map[string]any); ok {
				parts = append(parts, ocPartToContentBlock(pm))
			}
		}
	}
	return AgentMessage{
		ID:        m.ID,
		SessionID: sessionID,
		Role:      role,
		Parts:     parts,
		Metadata:  m.Data,
	}
}

// ocPartToContentBlock 把 OpenCode 的 part map 转 ContentBlock。
func ocPartToContentBlock(p map[string]any) ContentBlock {
	cb := ContentBlock{MimeType: strFromAny(p["mime"])}
	if t, ok := p["type"].(string); ok {
		cb.Type = t
	}
	if text, ok := p["text"].(string); ok {
		cb.Text = text
	}
	if url, ok := p["url"].(string); ok {
		cb.URL = url
	}
	if data, ok := p["data"].(map[string]any); ok {
		cb.Data = data
	}
	return cb
}

// ocToAgentEvent 把 OpenCodeEvent 转 AgentEvent。
func ocToAgentEvent(e adapter.OpenCodeEvent) AgentEvent {
	var data map[string]any
	if m, ok := e.Data.(map[string]any); ok {
		data = m
	}
	return AgentEvent{
		Type:      e.Type,
		SessionID: extractSessionID(data),
		Data:      map[string]any{"raw": e.Data},
	}
}

// ocToAgentQuestion 把 OpenCode QuestionRequest 转 Agent Question。
//
// OpenCode 的 QuestionRequest.Questions 是 []QuestionInfo（每个问题独立 prompt）。
// 我们把每个 QuestionInfo 转成一个 Agent Question。
func ocToAgentQuestion(q adapter.QuestionRequest) []Question {
	out := make([]Question, 0, len(q.Questions))
	for i, info := range q.Questions {
		options := make([]QuestionOption, 0, len(info.Options))
		for _, opt := range info.Options {
			options = append(options, QuestionOption{
				ID:          opt.Label,
				Label:       opt.Label,
				Description: opt.Description,
			})
		}
		id := q.ID + "#" + info.Header
		if i == 0 {
			id = q.ID // 第一个问题保留原始 ID（简化前端 mapping）
		}
		out = append(out, Question{
			ID:        id,
			SessionID: q.SessionID,
			Prompt:    info.Question,
			Options:   options,
			Multi:     info.Multiple != nil && *info.Multiple,
			Metadata: map[string]any{
				"header": info.Header,
				"raw":    info,
			},
		})
	}
	return out
}

// ocPermissionOptions 转 PermissionOption。
func ocPermissionOptions(in []string) []PermissionOption {
	out := make([]PermissionOption, 0, len(in))
	for _, label := range in {
		out = append(out, PermissionOption{ID: label, Label: label})
	}
	return out
}

// openCodeStatusToAgent 把 OpenCode 状态字符串映射到 AgentStatus。
func openCodeStatusToAgent(s string) string {
	switch s {
	case "active":
		return "busy"
	case "idle", "":
		return "idle"
	default:
		return s
	}
}

// extractSessionID 从 OpenCode 事件 Data 提取 sessionID。
func extractSessionID(data map[string]any) string {
	if sid, ok := data["sessionID"].(string); ok {
		return sid
	}
	if info, ok := data["info"].(map[string]any); ok {
		if sid, ok := info["sessionID"].(string); ok {
			return sid
		}
	}
	return ""
}

// translateOpenCodeError 把 OpenCodeError 转 AgentError。
//
// 不重复发明 error 类型 — 直接重新包装，保留原始信息。
func translateOpenCodeError(err error) error {
	if err == nil {
		return nil
	}
	// 已是 AgentError 直接返回（避免重复包装）
	if _, ok := err.(*Error); ok {
		return err
	}
	var oe *adapter.OpenCodeError
	if errAs(err, &oe) {
		// OpenCodeError 的 Code 字段与 AgentError 对应：
		// OPENCODE_UNREACHABLE → AGENT_UNREACHABLE 等
		// 简化：直接重新包成 AgentError，保留 code/message/cause
		// 但保留 OpenCode-specific 状态码
		var ae *Error
		switch oe.Code {
		case "OPENCODE_UNREACHABLE":
			ae = NewUnreachableError(oe.Cause)
		case "OPENCODE_TIMEOUT":
			ae = NewTimeoutError(oe.Cause)
		case "OPENCODE_UPSTREAM":
			ae = NewUpstreamError(oe.StatusCode, oe.Message, oe.Cause)
		case "OPENCODE_BAD_REQUEST":
			ae = NewBadRequestError(oe.StatusCode, oe.Message, oe.Cause)
		default:
			ae = NewProtocolError(oe)
		}
		ae.StatusCode = oe.StatusCode
		return ae
	}
	// 兜底：未知错误当作协议错误
	return NewProtocolError(err)
}

// strFromAny 从 any 取 string（空字符串兜底）。
func strFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// nilStr 把空字符串转为 nil（用于 OpenCode optional 字段）。
func nilStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// timeUnixMsToTime 把 Unix 毫秒戳转 time.Time。
func timeUnixMsToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.Unix(ms/1000, (ms%1000)*1e6)
}

// errAs 是 errors.As 的简写（避免局部 alias 混乱）。
func errAs(err error, target any) bool {
	return errorsAs(err, target)
}

// errorsAs 在独立文件里 alias stdlib errors.As（避免每个文件重复 import）。
var errorsAs = func(err error, target any) bool { return stdErrorsAs(err, target) }

func stdErrorsAs(err error, target any) bool {
	for {
		if err == nil {
			return false
		}
		// 简化：用 type assertion
		if oe, ok := err.(*adapter.OpenCodeError); ok {
			if dst, ok := target.(**adapter.OpenCodeError); ok {
				*dst = oe
				return true
			}
		}
		// 拆包装
		type unwrapper interface{ Unwrap() error }
		if u, ok := err.(unwrapper); ok {
			err = u.Unwrap()
			continue
		}
		return false
	}
}

// 防止编译期 unused
var _ = sync.Mutex{}
var _ = json.Marshal
