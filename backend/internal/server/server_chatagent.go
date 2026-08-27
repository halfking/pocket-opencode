package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/chatagent"
)

const maxCustomAgentsPerWorkspace = 100

// handleChatAgentsList 列出所有角色（内置 + 当前 workspace 的自定义角色）。
// GET /api/chat-agents?department=engineering
func (s *Server) handleChatAgentsList(w http.ResponseWriter, r *http.Request) {
	if s.chatAgentStore == nil {
		writeError(w, http.StatusServiceUnavailable, "chat agent store not configured")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	workspaceID := s.workspaceIDFromRequest(r)
	department := r.URL.Query().Get("department")

	agents, err := s.chatAgentStore.List(r.Context(), workspaceID, department)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"agents": agents})
}

// handleChatAgentsGet 获取单个角色详情。
// GET /api/chat-agents/:id
func (s *Server) handleChatAgentsGet(w http.ResponseWriter, r *http.Request) {
	if s.chatAgentStore == nil {
		writeError(w, http.StatusServiceUnavailable, "chat agent store not configured")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/chat-agents/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	workspaceID := s.workspaceIDFromRequest(r)
	agent, err := s.chatAgentStore.Get(r.Context(), workspaceID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	writeJSON(w, http.StatusOK, agent)
}

// handleChatAgentsCreate 创建自定义角色。
// POST /api/chat-agents
// Body: { name, description, department, emoji?, color?, system_prompt }
func (s *Server) handleChatAgentsCreate(w http.ResponseWriter, r *http.Request) {
	if s.chatAgentStore == nil {
		writeError(w, http.StatusServiceUnavailable, "chat agent store not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	workspaceID := s.workspaceIDFromRequest(r)
	userID := s.userIDFromRequest(r)

	// 检查配额
	count, err := s.chatAgentStore.CountCustom(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if count >= maxCustomAgentsPerWorkspace {
		writeError(w, http.StatusForbidden, fmt.Sprintf("custom agent limit reached (%d)", maxCustomAgentsPerWorkspace))
		return
	}

	var body struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Description  string `json:"description"`
		Department   string `json:"department"`
		Emoji        string `json:"emoji"`
		Color        string `json:"color"`
		SystemPrompt string `json:"system_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	if body.Name == "" || body.Department == "" || body.SystemPrompt == "" {
		writeError(w, http.StatusBadRequest, "name, department, system_prompt are required")
		return
	}

	// 生成 ID（如果未提供）
	if body.ID == "" {
		body.ID = fmt.Sprintf("custom-%s-%d", sanitizeID(body.Name), nowUnix())
	}

	agent := &chatagent.Agent{
		ID:           body.ID,
		WorkspaceID:  workspaceID,
		Name:         body.Name,
		Description:  body.Description,
		Department:   body.Department,
		Emoji:        body.Emoji,
		Color:        body.Color,
		SystemPrompt: body.SystemPrompt,
		IsBuiltin:    false,
	}

	if err := s.chatAgentStore.Create(r.Context(), agent); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 审计
	s.writeAudit(r, "chat_agent.create", true, fmt.Sprintf("agent_id=%s name=%q", agent.ID, agent.Name))

	writeJSON(w, http.StatusCreated, agent)
}

// handleChatAgentsUpdate 更新自定义角色（仅 isBuiltin=false）。
// PUT /api/chat-agents/:id
// Body: 同 POST（局部更新）
func (s *Server) handleChatAgentsUpdate(w http.ResponseWriter, r *http.Request) {
	if s.chatAgentStore == nil {
		writeError(w, http.StatusServiceUnavailable, "chat agent store not configured")
		return
	}
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/chat-agents/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	workspaceID := s.workspaceIDFromRequest(r)

	var body struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Department   string `json:"department"`
		Emoji        string `json:"emoji"`
		Color        string `json:"color"`
		SystemPrompt string `json:"system_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	// 先获取现有角色
	existing, err := s.chatAgentStore.Get(r.Context(), workspaceID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	// 局部更新
	if body.Name != "" {
		existing.Name = body.Name
	}
	if body.Description != "" {
		existing.Description = body.Description
	}
	if body.Department != "" {
		existing.Department = body.Department
	}
	if body.Emoji != "" {
		existing.Emoji = body.Emoji
	}
	if body.Color != "" {
		existing.Color = body.Color
	}
	if body.SystemPrompt != "" {
		existing.SystemPrompt = body.SystemPrompt
	}

	if err := s.chatAgentStore.Update(r.Context(), workspaceID, existing); err != nil {
		if strings.Contains(err.Error(), "builtin") {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// 审计
	s.writeAudit(r, "chat_agent.update", true, fmt.Sprintf("agent_id=%s", id))

	writeJSON(w, http.StatusOK, existing)
}

// handleChatAgentsDelete 删除自定义角色（仅 isBuiltin=false）。
// DELETE /api/chat-agents/:id
func (s *Server) handleChatAgentsDelete(w http.ResponseWriter, r *http.Request) {
	if s.chatAgentStore == nil {
		writeError(w, http.StatusServiceUnavailable, "chat agent store not configured")
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/chat-agents/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	workspaceID := s.workspaceIDFromRequest(r)

	if err := s.chatAgentStore.Delete(r.Context(), workspaceID, id); err != nil {
		if strings.Contains(err.Error(), "builtin") {
			writeError(w, http.StatusForbidden, err.Error())
		} else if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// 审计
	s.writeAudit(r, "chat_agent.delete", true, fmt.Sprintf("agent_id=%s", id))

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// sanitizeID 从名称生成 URL 安全的 ID 片段。
func sanitizeID(name string) string {
	// 简化版：去掉空格、转小写、只保留字母数字
	out := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	var safe strings.Builder
	for _, r := range out {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			safe.WriteRune(r)
		}
	}
	result := safe.String()
	if len(result) > 20 {
		result = result[:20]
	}
	return result
}
