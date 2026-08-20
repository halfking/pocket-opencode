package server

import (
	"encoding/json"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// handleRedClawHealth RedClaw 健康检查代理
// GET /api/redclaw/health
func (s *Server) handleRedClawHealth(w http.ResponseWriter, r *http.Request) {
	if s.redclawBridge == nil {
		http.Error(w, `{"error":"RedClaw bridge not configured"}`, http.StatusServiceUnavailable)
		return
	}

	healthy := s.redclawBridge.HealthCheck()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"connected": healthy,
		"tenant_id": s.cfg.RedClawTenantID,
	})
}

// handleRedClawChat RedClaw LLM 对话代理
// POST /api/redclaw/chat
func (s *Server) handleRedClawChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.redclawBridge == nil {
		http.Error(w, `{"error":"RedClaw bridge not configured"}`, http.StatusServiceUnavailable)
		return
	}

	var req redclaw.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	// Force tenant and user from authenticated JWT; ignore request body values
	claims := s.claimsFromContext(r)
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	req.TenantID = claims.WorkspaceID
	req.UserID = claims.UserID

	// Validate against configured tenant if single-tenant deployment
	if s.cfg.RedClawTenantID != "" && req.TenantID != s.cfg.RedClawTenantID {
		http.Error(w, `{"error":"tenant mismatch"}`, http.StatusForbidden)
		return
	}

	resp, err := s.redclawBridge.Chat(req)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleRedClawKnowledgeSearch RedClaw 知识库检索代理
// POST /api/redclaw/knowledge/search
func (s *Server) handleRedClawKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.redclawBridge == nil {
		http.Error(w, `{"error":"RedClaw bridge not configured"}`, http.StatusServiceUnavailable)
		return
	}

	var req redclaw.KnowledgeSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.Query == "" {
		http.Error(w, `{"error":"query is required"}`, http.StatusBadRequest)
		return
	}

	// Force tenant from authenticated JWT
	claims := s.claimsFromContext(r)
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	req.TenantID = claims.WorkspaceID

	resp, err := s.redclawBridge.KnowledgeSearch(req)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
