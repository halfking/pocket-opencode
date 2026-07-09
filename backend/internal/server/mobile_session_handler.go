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
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
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
//   /api/mobile/sessions                   POST 创建 | GET 列表
//   /api/mobile/sessions/search            GET 搜索 (Phase 2.2)
//   /api/mobile/sessions/{id}              DELETE 删除 (Phase 2.1)
//   /api/mobile/sessions/{id}/event        GET SSE
//   /api/mobile/sessions/{id}/messages     GET 历史
//   /api/mobile/sessions/{id}/summary      GET 摘要 (Phase 2.3)
//   /api/mobile/sessions/{id}/prompt       POST 发送
//   /api/mobile/sessions/{id}/interrupt    POST 中断
func (s *Server) handleMobileSessionRouter(w http.ResponseWriter, r *http.Request) {
	if s.opencode == nil || s.registry == nil {
		http.Error(w, "opencode adapter not configured", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/mobile/sessions")
	path = strings.Trim(path, "/")

	// 处理 /api/mobile/sessions (无子路径)
	if path == "" {
		if r.Method == http.MethodPost {
			s.handleMobileSessionCreate(w, r)
			return
		}
		if r.Method == http.MethodGet {
			s.handleMobileSessionList(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 处理 /api/mobile/sessions/search
	if path == "search" && r.Method == http.MethodGet {
		s.handleMobileSessionSearch(w, r)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	sessionID := parts[0]
	suffix := ""
	if len(parts) == 2 {
		suffix = parts[1]
	}

	// Phase 2.1: 支持 DELETE /api/mobile/sessions/{id}
	if suffix == "" && r.Method == http.MethodDelete {
		s.handleMobileSessionDelete(w, r, sessionID)
		return
	}

	switch suffix {
	case "event":
		s.handleMobileSessionEvent(w, r, sessionID)
	case "messages":
		s.handleMobileSessionMessages(w, r, sessionID)
	case "summary":
		s.handleMobileSessionSummary(w, r, sessionID) // Phase 2.3
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

// handleMobileSessionList GET /api/mobile/sessions?instance_id=xxx
// 返回会话列表
func (s *Server) handleMobileSessionList(w http.ResponseWriter, r *http.Request) {
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

	sessions, err := s.opencode.ListSessions(r.Context(), apiBaseURL)
	if err != nil {
		http.Error(w, "list sessions: "+err.Error(), http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":  sessions,
		"total": len(sessions),
	})
}

// handleMobileSessionSearch GET /api/mobile/sessions/search?q=keyword&instance_id=xxx
// Phase 2.2: 搜索会话
func (s *Server) handleMobileSessionSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "missing search query 'q'", http.StatusBadRequest)
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

	sessions, err := s.opencode.ListSessions(r.Context(), apiBaseURL)
	if err != nil {
		http.Error(w, "list sessions: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 搜索匹配的会话
	queryLower := strings.ToLower(query)
	results := make([]adapter.OpenCodeSession, 0)
	for _, sess := range sessions {
		if strings.Contains(strings.ToLower(sess.Title), queryLower) ||
			strings.Contains(strings.ToLower(sess.ID), queryLower) {
			results = append(results, sess)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":  results,
		"query": query,
		"total": len(results),
	})
}

// handleMobileSessionSummary GET /api/mobile/sessions/{id}/summary?instance_id=xxx
// Phase 2.3: 会话摘要
func (s *Server) handleMobileSessionSummary(w http.ResponseWriter, r *http.Request, sessionID string) {
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

	// 获取会话标题
	title, err := s.opencode.GetSessionSummary(r.Context(), apiBaseURL, sessionID)
	if err != nil {
		http.Error(w, "get summary: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 获取消息历史
	msgs, err := s.opencode.GetMessages(r.Context(), apiBaseURL, sessionID, 20, "desc")
	if err != nil {
		// 如果获取消息失败，返回标题作为摘要
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"sessionID":    sessionID,
			"title":        title,
			"summary":      title,
			"messageCount": 0,
		})
		return
	}

	// 统计消息类型
	userCount, assistantCount, toolCount := 0, 0, 0
	for _, msg := range msgs {
		switch msg.Type {
		case "user":
			userCount++
		case "assistant":
			assistantCount++
		case "tool":
			toolCount++
		}
	}

	summary := title
	if userCount > 0 || assistantCount > 0 {
		summary += fmt.Sprintf(" (用户消息: %d, AI回复: %d", userCount, assistantCount)
		if toolCount > 0 {
			summary += fmt.Sprintf(", 工具调用: %d", toolCount)
		}
		summary += ")"
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessionID":    sessionID,
		"title":        title,
		"summary":      summary,
		"messageCount": len(msgs),
	})
}

// handleMobileSessionEvent GET /api/mobile/sessions/{id}/event?instance_id=xxx
// SSE：通过 EventStreamManager 共享连接转发事件（优化：复用上游 SSE 连接）。
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

	// 优先使用 EventStreamManager 共享连接（Phase 1.1 优化）
	if s.eventMgr != nil {
		s.handleMobileSessionEventViaManager(w, r, sessionID, instanceID)
		return
	}

	// 降级：直接调用 adapter 建立独立连接（兼容模式）
	s.handleMobileSessionEventDirect(w, r, sessionID, instanceID)
}

// handleMobileSessionEventViaManager 通过 EventStreamManager 共享连接转发事件
func (s *Server) handleMobileSessionEventViaManager(w http.ResponseWriter, r *http.Request, sessionID, instanceID string) {
	// SSE 头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// 通过 EventStreamManager 订阅（共享上游连接）
	events, cleanup, err := s.eventMgr.Subscribe(ctx, opencode.SubscribeOptions{
		InstanceID: instanceID,
		BufferSize: 128,
	})
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: {\"error\":\"%s\"}\n\n", err.Error())
		flusher.Flush()
		return
	}
	defer cleanup()

	// 心跳定时器
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	// 写 connected 事件
	fmt.Fprintf(w, "event: server.connected\ndata: {\"sessionId\":\"%s\"}\n\n", sessionID)
	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case domainEvt, ok := <-events:
			if !ok {
				fmt.Fprint(w, "event: upstream.closed\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			// 过滤：只转发与该 session 有关的事件
			if !eventBelongsToSession(domainEvt.Raw, sessionID) {
				continue
			}
			data, err := json.Marshal(domainEvt.Raw)
			if err != nil {
				continue
			}
			evtType := domainEvt.Type
			if evtType != "" {
				fmt.Fprintf(w, "event: %s\n", evtType)
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// handleMobileSessionEventDirect 直接调用 adapter 建立独立连接（兼容模式）
func (s *Server) handleMobileSessionEventDirect(w http.ResponseWriter, r *http.Request, sessionID, instanceID string) {
	apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// SSE 头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

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
		Parts: []adapter.PromptPart{{Type: "text", Text: req.Text}},
	}
	if req.Agent != nil && *req.Agent != "" {
		payload.Agent = req.Agent
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

// handleMobileSessionDelete DELETE /api/mobile/sessions/{id}?instance_id=xxx
// Phase 2.1: 删除指定会话
func (s *Server) handleMobileSessionDelete(w http.ResponseWriter, r *http.Request, sessionID string) {
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

	if err := s.opencode.DeleteSession(r.Context(), apiBaseURL, sessionID); err != nil {
		http.Error(w, "delete session: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 清理本地缓存
	if s.opencodeManager != nil {
		s.opencodeManager.InvalidateCache(instanceID)
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