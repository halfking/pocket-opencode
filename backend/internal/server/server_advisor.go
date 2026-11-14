package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/advisor"
)

// handleMeetingAdvisor 处理会议动态参谋请求
//
// POST /api/meetings/:id/advisor
// 请求体: {"text": "新的转写文本"}
// 响应: {"suggestions": [...]}
func (s *Server) handleMeetingAdvisor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	// 从URL提取meeting ID
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	meetingID := ""
	if len(pathParts) >= 3 && pathParts[0] == "api" && pathParts[1] == "meetings" {
		meetingID = pathParts[2]
	}

	if meetingID == "" {
		writeError(w, http.StatusBadRequest, "meeting ID required")
		return
	}

	// 检查动态参谋是否启用
	if s.advisorClient == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":     false,
			"suggestions": []advisor.Suggestion{},
		})
		return
	}

	// 解析请求体
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 调用动态参谋
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	suggestions, err := s.advisorClient.OnTranscriptUpdate(ctx, req.Text)
	if err != nil {
		// 参谋失败不阻塞，返回空建议
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":     true,
			"suggestions": []advisor.Suggestion{},
			"error":       err.Error(),
		})
		return
	}

	// 返回建议
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     true,
		"suggestions": suggestions,
	})
}

// handleAdvisorHealth 检查动态参谋状态
//
// GET /api/advisor/health
func (s *Server) handleAdvisorHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	available := s.advisorClient != nil
	
	writeJSON(w, http.StatusOK, map[string]any{
		"available": available,
		"kxmemory":  s.cfg.KxMemoryBaseURL != "",
	})
}
