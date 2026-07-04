package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
)

// =============================================================================
// Phase V3: 移动端真实会话交互 API
// =============================================================================
//
// 路由：
//   GET    /api/mobile/sessions/{id}/event?instance_id=xxx&after=N
//        → SSE 转发 OpenCode 上游事件
//   GET    /api/mobile/sessions/{id}/messages?instance_id=xxx&limit=50
//        → 历史消息回填（用于 SSE 断线期间）
//   POST   /api/mobile/sessions?instance_id=xxx
//        → 新建会话（支持 parentID=fork）
//   POST   /api/mobile/sessions/{id}/prompt?instance_id=xxx
//        → 发送用户 prompt（异步，返回 messageID）
//   POST   /api/mobile/sessions/{id}/interrupt?instance_id=xxx
//        → 中断当前 agent 循环
//
// 所有路由要求 requiresAuth（已在 mux 注册时包装）。

// handleMobileSessionRouter 分发 /api/mobile/sessions/...
//   /api/mobile/sessions                   POST 创建
//   /api/mobile/sessions/{id}/event        GET SSE
//   /api/mobile/sessions/{id}/messages     GET 历史
//   /api/mobile/sessions/{id}/prompt       POST 发送
//   /api/mobile/sessions/{id}/interrupt    POST 中断
func (s *Server) handleMobileSessionRouter(w http.ResponseWriter, r *http.Request) {
	if s.opencode == nil || s.registry == nil {
		http.Error(w, "opencode adapter not configured", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/mobile/sessions")
	path = strings.Trim(path, "/")
	if path == "" {
		// /api/mobile/sessions (无子路径) → 创建
		if r.Method == http.MethodPost {
			s.handleMobileSessionCreate(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	sessionID := parts[0]
	suffix := ""
	if len(parts) == 2 {
		suffix = parts[1]
	}

	switch suffix {
	case "event":
		s.handleMobileSessionEvent(w, r, sessionID)
	case "messages":
		s.handleMobileSessionMessages(w, r, sessionID)
	case "prompt":
		s.handleMobileSessionPrompt(w, r, sessionID)
	case "interrupt":
		s.handleMobileSessionInterrupt(w, r, sessionID)
	default:
		http.Error(w, "not found: "+suffix, http.StatusNotFound)
	}
}

// handleMobileSessionCreate POST /api/mobile/sessions?instance_id=xxx
// body: { title?, parentID?, agent?, model? }
func (s *Server) handleMobileSessionCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}
	apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	var req struct {
		Title    string                 `json:"title"`
		ParentID *string                `json:"parentID,omitempty"`
		Agent    *string                `json:"agent,omitempty"`
		Model    map[string]interface{} `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}

	// CreateSessionRequest 不直接接受 Title/ParentID（按 OpenCode schema），
	// 客户端语义：title 暂作 metadata，parentID 透传为 location.workspaceID 之外的字段保留。
	// 这里把 Title 放进 metadata。
	_ = req.Title // 当前 OpenCode POST /api/session 不接受 title，由后续 update 补充
	_ = req.ParentID

	payload := &adapter.CreateSessionRequest{
		Agent: req.Agent,
	}

	info, err := s.opencode.CreateSession(r.Context(), apiBaseURL, payload)
	if err != nil {
		http.Error(w, "create session: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleMobileSessionEvent GET /api/mobile/sessions/{id}/event?instance_id=xxx
// SSE：转发 OpenCode 上游 /api/event 的事件流（带 session 过滤）。
func (s *Server) handleMobileSessionEvent(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}
	apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// SSE 头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // 禁用 nginx buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// 订阅上游
	directory := r.URL.Query().Get("directory")
	workspaceID := r.URL.Query().Get("workspaceID")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	events, cleanup, err := s.opencode.SubscribeEvents(ctx, apiBaseURL, directory, workspaceID)
	if err != nil {
		// 写一行 SSE 错误再 flush
		fmt.Fprintf(w, "event: error\ndata: {\"error\":\"%s\"}\n\n", err.Error())
		flusher.Flush()
		return
	}
	defer cleanup()

	// 心跳定时器
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	// 写一个 connected 事件
	fmt.Fprintf(w, "event: server.connected\ndata: {\"sessionId\":\"%s\"}\n\n", sessionID)
	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case evt, ok := <-events:
			if !ok {
				fmt.Fprint(w, "event: upstream.closed\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			// 过滤：只转发与该 session 有关的事件。
			// 兼容两种 envelope：raw.durable.aggregateID 或 raw.sessionID
			if !eventBelongsToSession(evt, sessionID) {
				continue
			}
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			evtType := eventTypeOf(evt)
			if evtType != "" {
				fmt.Fprintf(w, "event: %s\n", evtType)
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// eventBelongsToSession 判断上游事件是否归属于指定 session
//
// OpenCode V1 envelope（参考 ~/workspace/ai/opencodenew/packages/protocol/src/event.ts）：
//   { id, type, location?, data, ... }
// V2 envelope：{ id, type, data, durable: { aggregateID, seq, ... } }
//
// 优先用 durable.aggregateID，其次 location.sessionID，再次 type 前缀兜底。
func eventBelongsToSession(evt adapter.OpenCodeEvent, sessionID string) bool {
	if sessionID == "" {
		return true
	}

	// V2: durable.aggregateID
	if data, ok := evt.Data.(map[string]interface{}); ok {
		if durable, ok := data["durable"].(map[string]interface{}); ok {
			if aggID, ok := durable["aggregateID"].(string); ok && aggID == sessionID {
				return true
			}
		}
		if sid, ok := data["sessionID"].(string); ok && sid == sessionID {
			return true
		}
	}

	// V1: location.sessionID
	if evt.Location != nil {
		if sid, ok := evt.Location["sessionID"].(string); ok && sid == sessionID {
			return true
		}
	}

	// 兜底：所有 session.* 类型都转发（V1 全局事件流没有 sessionID 字段）
	t := evt.Type
	if strings.HasPrefix(t, "session.") {
		return true
	}
	return false
}

// eventTypeOf 提取事件类型字符串
func eventTypeOf(evt adapter.OpenCodeEvent) string {
	return evt.Type
}

// handleMobileSessionMessages GET /api/mobile/sessions/{id}/messages?instance_id=xxx&limit=50
func (s *Server) handleMobileSessionMessages(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}
	apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	order := r.URL.Query().Get("order")
	if order == "" {
		order = "asc"
	}

	msgs, err := s.opencode.GetMessages(r.Context(), apiBaseURL, sessionID, limit, order)
	if err != nil {
		http.Error(w, "get messages: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessionId": sessionID,
		"messages":  msgs,
		"total":     len(msgs),
	})
}

// handleMobileSessionPrompt POST /api/mobile/sessions/{id}/prompt?instance_id=xxx
// body: { text, agent?, model? }
// 返回: { messageID }
func (s *Server) handleMobileSessionPrompt(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}
	apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	var req struct {
		Text  string                 `json:"text"`
		Agent *string                `json:"agent,omitempty"`
		Model map[string]interface{} `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Text == "" {
		http.Error(w, "text required", http.StatusBadRequest)
		return
	}

	payload := &adapter.SendPromptRequest{
		Prompt: adapter.PromptPayload{
			Text:  req.Text,
			Agent: req.Agent,
		},
	}

	resp, err := s.opencode.SendPrompt(r.Context(), apiBaseURL, sessionID, payload)
	if err != nil {
		http.Error(w, "send prompt: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"messageID": resp.MessageID,
		"sessionID": sessionID,
	})
}

// handleMobileSessionInterrupt POST /api/mobile/sessions/{id}/interrupt?instance_id=xxx
func (s *Server) handleMobileSessionInterrupt(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}
	apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := s.opencode.InterruptSession(r.Context(), apiBaseURL, sessionID); err != nil {
		http.Error(w, "interrupt: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// =============================================================================
// 辅助
// =============================================================================

// drainAndClose 读取并丢弃剩余 body（用于 HTTP 连接复用）
func drainAndClose(body io.ReadCloser) {
	defer body.Close()
	_, _ = bufio.NewReader(body).Discard(1 << 16)
}