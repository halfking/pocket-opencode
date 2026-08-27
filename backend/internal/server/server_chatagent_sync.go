package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/chatagent"
)

// handleChatAgentSyncUpload 上传自定义角色列表到云端。
// POST /api/chat-agents/sync/upload
// Body: { "version": int64, "agents": [{ ... }] }
// Response 200: { "version": int64, "uploaded_count": int, "conflict": false }
// Response 409 (version conflict): { "version": int64, "conflict": true, "server_version": int64 }
func (s *Server) handleChatAgentSyncUpload(w http.ResponseWriter, r *http.Request) {
	if s.chatAgentSync == nil {
		writeError(w, http.StatusServiceUnavailable, "chat agent sync not configured (requires PostgreSQL)")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	workspaceID := s.workspaceIDFromRequest(r)
	userID := s.userIDFromRequest(r)

	var payload chatagent.SyncPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	result, err := s.chatAgentSync.Upload(r.Context(), workspaceID, userID, &payload)
	if err != nil {
		if errors.Is(err, chatagent.ErrSyncConflict) {
			// 409 Conflict with structured body
			writeJSON(w, http.StatusConflict, result)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 审计
	s.auditGateway(r, "chat_agent.sync_upload", workspaceID,
		fmt.Sprintf("version=%d uploaded=%d", result.Version, result.UploadedCount), true)

	writeJSON(w, http.StatusOK, result)
}

// handleChatAgentSyncDownload 从云端拉取自定义角色列表。
// GET /api/chat-agents/sync/download
// Response: { "version": int64, "agents": [...] }
// 若服务端无记录，返回 version=0 + 空 agents 数组。
func (s *Server) handleChatAgentSyncDownload(w http.ResponseWriter, r *http.Request) {
	if s.chatAgentSync == nil {
		writeError(w, http.StatusServiceUnavailable, "chat agent sync not configured (requires PostgreSQL)")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	workspaceID := s.workspaceIDFromRequest(r)
	userID := s.userIDFromRequest(r)

	payload, err := s.chatAgentSync.Download(r.Context(), workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 审计
	s.auditGateway(r, "chat_agent.sync_download", workspaceID,
		fmt.Sprintf("version=%d agents=%d", payload.Version, len(payload.Agents)), true)

	writeJSON(w, http.StatusOK, payload)
}

// handleChatAgentSyncStatus 查询云端同步状态（用于 UI 角标显示）。
// GET /api/chat-agents/sync/status
// Response: { "has_remote": bool, "server_version": int64, "server_updated_at": int64, "agent_count": int }
func (s *Server) handleChatAgentSyncStatus(w http.ResponseWriter, r *http.Request) {
	if s.chatAgentSync == nil {
		writeError(w, http.StatusServiceUnavailable, "chat agent sync not configured (requires PostgreSQL)")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	workspaceID := s.workspaceIDFromRequest(r)
	userID := s.userIDFromRequest(r)

	status, err := s.chatAgentSync.Status(r.Context(), workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, status)
}
