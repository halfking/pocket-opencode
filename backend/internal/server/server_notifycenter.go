package server

// server_notifycenter.go — S0-E Notification Center HTTP handlers.
//
// Routes:
//   GET  /api/notifications              列出当前 workspace 的通知 inbox
//   POST /api/notifications/mark-read    标记已读（id 或全部）
//   GET  /api/notifications/rules        列出规则
//   POST /api/notifications/rules        创建/更新规则
//
// 查询参数：
//   GET /api/notifications?limit=50&unread=1
//
// 通知的产生不在这些 handler 里——业务模块（task/email/agent/...）通过
// notifySvc.Dispatch(event) 主动推送。这些 handler 只负责读 + 规则管理。

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
)

// handleNotifications: GET /api/notifications
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if s.notifyStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notification center not configured")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	wsID := s.workspaceIDFromRequest(r)
	limit, _ := parseIntDefault(r.URL.Query().Get("limit"), 50)
	unread := 0
	if r.URL.Query().Get("unread") == "1" {
		unread = 1
	}
	notifs, err := s.notifyStore.ListNotifications(r.Context(), wsID, limit, unread)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list notifications: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": notifs})
}

// handleNotificationOps: POST /api/notifications/mark-read
func (s *Server) handleNotificationOps(w http.ResponseWriter, r *http.Request) {
	if s.notifyStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notification center not configured")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/notifications/")
	if path == "mark-read" && r.Method == http.MethodPost {
		var body struct {
			ID string `json:"id"` // 空 = 全部已读
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		wsID := s.workspaceIDFromRequest(r)
		if err := s.notifyStore.MarkRead(r.Context(), wsID, body.ID); err != nil {
			if errors.Is(err, notifycenter.ErrNotFound) {
				writeError(w, http.StatusNotFound, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "marked"})
		return
	}
	writeError(w, http.StatusNotFound, "unknown subpath: "+path)
}

// handleNotificationRules: GET/POST /api/notifications/rules
func (s *Server) handleNotificationRules(w http.ResponseWriter, r *http.Request) {
	if s.notifyStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notification center not configured")
		return
	}
	wsID := s.workspaceIDFromRequest(r)
	switch r.Method {
	case http.MethodGet:
		rules, err := s.notifyStore.ListRules(r.Context(), wsID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
	case http.MethodPost:
		var body notifycenter.Rule
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		body.WorkspaceID = wsID
		if body.ID == "" {
			writeError(w, http.StatusBadRequest, "id required")
			return
		}
		if err := s.notifyStore.UpsertRule(r.Context(), &body); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, body)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}
