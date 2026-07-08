package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
)

// handleOpenCodeDispatch 让 ACC / 其他控制面直接通过 Pocket 在指定实例上创建会话并发送 prompt。
//
//	POST /api/opencode/dispatch
//	Body: {
//	  "instance_id": "discovered-local-4096",
//	  "working_directory": "/path/to/project",
//	  "agent": "build",
//	  "model": "claude-sonnet-4-6",
//	  "provider_id": "kaixuan",
//	  "prompt": "请分析并重构...",
//	  "task_id": "ACC-T204"
//	}
//
// 返回：{session_id, instance_id, accepted, task_id}
func (s *Server) handleOpenCodeDispatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.opencode == nil || s.registry == nil {
		http.Error(w, "opencode adapter not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		InstanceID       string `json:"instance_id"`
		WorkingDirectory string `json:"working_directory"`
		Agent            string `json:"agent"`
		Model            string `json:"model"`
		ProviderID       string `json:"provider_id,omitempty"`
		Prompt           string `json:"prompt"`
		TaskID           string `json:"task_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.InstanceID == "" || req.Prompt == "" {
		http.Error(w, "instance_id and prompt are required", http.StatusBadRequest)
		return
	}

	apiBaseURL, err := s.registry.GetInstanceAPIBase(req.InstanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// 1. Create session
	create := &adapter.CreateSessionRequest{}
	if req.Agent != "" {
		agent := req.Agent
		create.Agent = &agent
	}
	if req.Model != "" {
		provider := req.ProviderID
		if provider == "" {
			provider = "kaixuan"
		}
		create.Model = &adapter.ModelRefHTTP{ID: req.Model, ProviderID: provider}
	}
	if req.WorkingDirectory != "" {
		create.Location = &adapter.LocationRefRef{Directory: req.WorkingDirectory}
	}
	info, err := s.opencode.CreateSession(r.Context(), apiBaseURL, create)
	if err != nil {
		http.Error(w, fmt.Sprintf("create session failed: %v", err), http.StatusBadGateway)
		return
	}

	// 2. Send prompt
	sessionID := info.ID
	delivery := "single"
	payload := &adapter.SendPromptRequest{
		ID: &sessionID,
		Prompt: adapter.PromptPayload{
			Text: req.Prompt,
		},
		Delivery: &delivery,
	}
	if _, err := s.opencode.SendPrompt(r.Context(), apiBaseURL, info.ID, payload); err != nil {
		http.Error(w, fmt.Sprintf("send prompt failed: %v", err), http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"accepted":    true,
		"instance_id": req.InstanceID,
		"session_id":  info.ID,
		"task_id":     req.TaskID,
		"created_at":  time.Now().UTC().Format(time.RFC3339),
	})
}
