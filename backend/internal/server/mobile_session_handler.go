package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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

// resolveMobileInstance enforces the mobile control-plane boundary before any
// upstream call. Read operations may use explicitly shared instances; write
// operations require an instance owned by the caller's workspace.
func (s *Server) resolveMobileInstance(w http.ResponseWriter, r *http.Request, instanceID string, writable bool) (string, string, bool) {
	workspaceID, ok := s.requireMobileWorkspace(w, r)
	if !ok {
		return "", "", false
	}
	if instanceID == "" {
		s.writeStructuredError(w, r, http.StatusBadRequest, CodeInvalidRequest, "instance_id is required")
		return "", "", false
	}
	var (
		apiBaseURL string
		err        error
	)
	if writable {
		apiBaseURL, err = s.registry.GetWritableInstanceAPIBaseForWorkspace(workspaceID, instanceID)
	} else {
		apiBaseURL, err = s.registry.GetInstanceAPIBaseForWorkspace(workspaceID, instanceID)
	}
	if err != nil {
		s.writeResourceNotFound(w, r)
		return "", "", false
	}
	return apiBaseURL, workspaceID, true
}

// handleMobileSessionRouter dispatches /api/mobile/sessions/...
//
//	/api/mobile/sessions                   POST 创建 | GET 列表
//	/api/mobile/sessions/search            GET 搜索 (Phase 2.2)
//	/api/mobile/sessions/{id}              DELETE 删除 (Phase 2.1)
//	/api/mobile/sessions/{id}/event        GET SSE
//	/api/mobile/sessions/{id}/messages     GET 历史
//	/api/mobile/sessions/{id}/summary      GET 摘要 (Phase 2.3)
//	/api/mobile/sessions/{id}/prompt       POST 发送
//	/api/mobile/sessions/{id}/interrupt    POST 中断
func (s *Server) handleMobileSessionRouter(w http.ResponseWriter, r *http.Request) {
	if s.opencode == nil || s.registry == nil {
		s.writeStructuredError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable, "opencode adapter not configured")
		return
	}
	if _, ok := s.requireMobileWorkspace(w, r); !ok {
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
		s.writeStructuredError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
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
		s.writeResourceNotFound(w, r)
	}
}

// handleMobileSessionCreate POST /api/mobile/sessions?instance_id=xxx
// body: { title?, parentID?, agent?, model? }
//
// Supports offline replay: when the Idempotency-Key header is present, a
// retry of the same create returns the original upstream session instead of
// creating a duplicate (SEC-06 mobile outbox drain).
func (s *Server) handleMobileSessionCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeStructuredError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	apiBaseURL, workspaceID, ok := s.resolveMobileInstance(w, r, instanceID, true)
	if !ok {
		return
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey != "" && s.mobileCreates != nil {
		if cached, hit := s.mobileCreates.Get(workspaceID, instanceID, idempotencyKey); hit {
			w.Header().Set("Idempotency-Replayed", "true")
			writeJSON(w, http.StatusOK, cached)
			return
		}
	}

	var req struct {
		Title    string                 `json:"title"`
		ParentID *string                `json:"parentID,omitempty"`
		Agent    *string                `json:"agent,omitempty"`
		Model    map[string]interface{} `json:"model,omitempty"`
	}
	if r.ContentLength != 0 && !s.decodeJSONBody(w, r, &req) {
		return
	}

	// CreateSessionRequest does not accept Title/ParentID in the OpenCode
	// schema. Bind the new upstream session to the authenticated workspace.
	_ = req.Title
	_ = req.ParentID
	payload := &adapter.CreateSessionRequest{Agent: req.Agent}
	payload.Location = &adapter.LocationRefRef{WorkspaceID: &workspaceID}

	info, err := s.opencode.CreateSession(r.Context(), apiBaseURL, payload)
	if err != nil {
		s.writeStructuredError(w, r, http.StatusBadGateway, CodeUpstreamUnavailable,
			"create session: "+err.Error())
		return
	}
	if idempotencyKey != "" && s.mobileCreates != nil {
		s.mobileCreates.Put(workspaceID, instanceID, idempotencyKey, info)
	}
	writeJSON(w, http.StatusOK, info)
}

// handleMobileSessionList GET /api/mobile/sessions?instance_id=xxx&since=N
// 返回会话列表。since（Unix 毫秒）存在时仅返回 time.updated > since 的行，
// 供移动端增量同步；响应行是同步视图（id/title/status/timeUpdatedMs），
// 与内部 adapter 结构解耦。
func (s *Server) handleMobileSessionList(w http.ResponseWriter, r *http.Request) {
	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}
	apiBaseURL, _, ok := s.resolveMobileInstance(w, r, instanceID, false)
	if !ok {
		return
	}

	since := int64(0)
	if rawSince := r.URL.Query().Get("since"); rawSince != "" {
		parsed, err := strconv.ParseInt(rawSince, 10, 64)
		if err != nil || parsed < 0 {
			s.writeStructuredError(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"since must be a non-negative Unix millisecond timestamp")
			return
		}
		since = parsed
	}

	sessions, err := s.opencode.ListSessions(r.Context(), apiBaseURL)
	if err != nil {
		http.Error(w, "list sessions: "+err.Error(), http.StatusBadGateway)
		return
	}

	rows := make([]map[string]any, 0, len(sessions))
	for _, sess := range sessions {
		if since > 0 && sess.TimeUpdated <= since {
			continue
		}
		rows = append(rows, map[string]any{
			"id":            sess.ID,
			"title":         sess.Title,
			"status":        sess.Status,
			"timeUpdatedMs": sess.TimeUpdated,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":         rows,
		"total":        len(rows),
		"sinceMs":      since,
		"serverTimeMs": time.Now().UnixMilli(),
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
	apiBaseURL, _, ok := s.resolveMobileInstance(w, r, instanceID, false)
	if !ok {
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
	apiBaseURL, _, ok := s.resolveMobileInstance(w, r, instanceID, false)
	if !ok {
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

	// 统计消息类型（经移动端视图归一化：V1 信封没有顶层 type/role，
	// 直接读 msg.Type 会恒为空）
	userCount, assistantCount, toolCount := 0, 0, 0
	for i := range msgs {
		mm := msgs[i].ToMobile()
		if mm == nil {
			continue
		}
		switch mm.Role {
		case "user":
			userCount++
		case "assistant":
			assistantCount++
			for _, c := range mm.Content {
				if c.Type == "tool" {
					toolCount++
				}
			}
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
		InstanceID:  instanceID,
		WorkspaceID: s.workspaceIDFromRequest(r),
		Directory:   r.URL.Query().Get("directory"),
		BufferSize:  128,
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
	apiBaseURL, workspaceID, ok := s.resolveMobileInstance(w, r, instanceID, false)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// The workspace is derived only from authenticated claims. A client query
	// parameter must never alter the upstream event subscription scope.
	directory := r.URL.Query().Get("directory")
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
//
//	{ id, type, location?, data, ... }
//
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

	// A global event without a provable session id is not safe to forward.
	// In particular, shared upstream instances can emit events belonging to
	// another workspace/session.
	return false
}

// eventTypeOf 提取事件类型字符串
func eventTypeOf(evt adapter.OpenCodeEvent) string {
	return evt.Type
}

// handleMobileSessionMessages GET /api/mobile/sessions/{id}/messages?instance_id=xxx&limit=50
func (s *Server) handleMobileSessionMessages(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		s.writeStructuredError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	apiBaseURL, _, ok := s.resolveMobileInstance(w, r, instanceID, false)
	if !ok {
		return
	}

	limit := 50
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > 100 {
			s.writeStructuredError(w, r, http.StatusBadRequest, CodeInvalidRequest,
				"limit must be an integer between 1 and 100")
			return
		}
		limit = parsed
	}
	order := r.URL.Query().Get("order")
	if order == "" {
		order = "asc"
	}
	if order != "asc" && order != "desc" {
		s.writeStructuredError(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"order must be asc or desc")
		return
	}

	msgs, err := s.opencode.GetMessages(r.Context(), apiBaseURL, sessionID, limit, order)
	if err != nil {
		http.Error(w, "get messages: "+err.Error(), http.StatusBadGateway)
		return
	}
	// V1 {info,parts} → 移动端 {id,role,text,content}；无法识别的行跳过
	rows := make([]adapter.MobileMessage, 0, len(msgs))
	for i := range msgs {
		if mm := msgs[i].ToMobile(); mm != nil {
			rows = append(rows, *mm)
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessionId": sessionID,
		"messages":  rows,
		"total":     len(rows),
	})
}

// handleMobileSessionPrompt POST /api/mobile/sessions/{id}/prompt?instance_id=xxx
// body: { text, agent?, model? }
// 返回: { messageID }
func (s *Server) handleMobileSessionPrompt(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		s.writeStructuredError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	apiBaseURL, _, ok := s.resolveMobileInstance(w, r, instanceID, true)
	if !ok {
		return
	}

	var req struct {
		Text  string                 `json:"text"`
		Agent *string                `json:"agent,omitempty"`
		Model map[string]interface{} `json:"model,omitempty"`
	}
	if !s.decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		s.writeStructuredError(w, r, http.StatusBadRequest, CodeInvalidRequest, "text is required")
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
		s.writeStructuredError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	apiBaseURL, _, ok := s.resolveMobileInstance(w, r, instanceID, true)
	if !ok {
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
	if r.Method != http.MethodDelete {
		s.writeStructuredError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	apiBaseURL, _, ok := s.resolveMobileInstance(w, r, instanceID, true)
	if !ok {
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
