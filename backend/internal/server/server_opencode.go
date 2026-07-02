package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/halfking/pocket-opencode/backend/internal/opencode"
)

// handleOpenCodeSessions 处理 OpenCode 会话列表请求
// GET /api/opencode/sessions?instance_id=xxx&status=busy|idle|all&limit=20
func (s *Server) handleOpenCodeSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.opencodeManager == nil {
		http.Error(w, "opencode manager not configured", http.StatusServiceUnavailable)
		return
	}

	instanceID := r.URL.Query().Get("instance_id")
	statusFilter := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")

	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	var sessions []*opencode.CachedSession
	var err error

	if instanceID != "" {
		// 获取指定实例的会话
		sessions, err = s.opencodeManager.GetSessions(r.Context(), instanceID)
	} else {
		// 获取所有实例的会话
		sessions, err = s.opencodeManager.GetAllSessions(r.Context())
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 应用状态过滤
	if statusFilter != "" && statusFilter != "all" {
		filtered := make([]*opencode.CachedSession, 0)
		for _, s := range sessions {
			if s.Status == statusFilter {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	// 应用限制
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessions": sessions,
		"total":    len(sessions),
	})
}

// handleOpenCodeSessionHistory 处理会话历史请求
// GET /api/opencode/sessions/{session_id}/history?limit=100
func (s *Server) handleOpenCodeSessionHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.opencodeManager == nil {
		http.Error(w, "opencode manager not configured", http.StatusServiceUnavailable)
		return
	}

	// 从路径提取 session_id: /api/opencode/sessions/{id}/history
	path := r.URL.Path[len("/api/opencode/sessions/"):]
	sessionID := ""
	for i, ch := range path {
		if ch == '/' {
			sessionID = path[:i]
			break
		}
	}

	if sessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	history, err := s.opencodeManager.GetSessionHistory(r.Context(), sessionID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessionId": sessionID,
		"timeline":  history,
		"total":     len(history),
	})
}

// handleOpenCodeSessionSummary 处理会话摘要请求
// GET /api/opencode/sessions/{session_id}/summary?instance_id=xxx
func (s *Server) handleOpenCodeSessionSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.opencodeManager == nil {
		http.Error(w, "opencode manager not configured", http.StatusServiceUnavailable)
		return
	}

	// 从路径提取 session_id
	path := r.URL.Path[len("/api/opencode/sessions/"):]
	sessionID := ""
	for i, ch := range path {
		if ch == '/' {
			sessionID = path[:i]
			break
		}
	}

	if sessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}

	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}

	summary, err := s.opencodeManager.GetSessionSummary(r.Context(), instanceID, sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessionId": sessionID,
		"summary":   summary,
	})
}

// handleOpenCodeInstanceStats 处理实例统计请求
// GET /api/opencode/instances/{instance_id}/stats
func (s *Server) handleOpenCodeInstanceStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.opencodeManager == nil {
		http.Error(w, "opencode manager not configured", http.StatusServiceUnavailable)
		return
	}

	// 从路径提取 instance_id
	path := r.URL.Path[len("/api/opencode/instances/"):]
	instanceID := ""
	for i, ch := range path {
		if ch == '/' {
			instanceID = path[:i]
			break
		}
	}

	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}

	sessions, err := s.opencodeManager.GetSessions(r.Context(), instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 统计各状态的会话数
	stats := map[string]int{
		"total":  len(sessions),
		"busy":   0,
		"idle":   0,
		"retry":  0,
		"other":  0,
	}

	for _, s := range sessions {
		switch s.Status {
		case "busy":
			stats["busy"]++
		case "idle":
			stats["idle"]++
		case "retry":
			stats["retry"]++
		default:
			stats["other"]++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"instanceId": instanceID,
		"stats":      stats,
	})
}

// handleOpenCodeRefreshCache 处理缓存刷新请求
// POST /api/opencode/cache/refresh?instance_id=xxx
func (s *Server) handleOpenCodeRefreshCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.opencodeManager == nil {
		http.Error(w, "opencode manager not configured", http.StatusServiceUnavailable)
		return
	}

	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}

	s.opencodeManager.InvalidateCache(instanceID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "cache invalidated",
	})
}
