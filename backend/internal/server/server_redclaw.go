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

	// User comes from JWT. Tenant: single-tenant pocketd maps every
	// authenticated workspace onto POCKET_REDCLAW_TENANT_ID (pocket
	// workspace ids like ws_user-admin are not RedClaw tenants).
	claims := s.claimsFromContext(r)
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	req.TenantID = s.redclawTenantID(claims)
	req.UserID = claims.UserID

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

	claims := s.claimsFromContext(r)
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	req.TenantID = s.redclawTenantID(claims)

	resp, err := s.redclawBridge.KnowledgeSearch(req)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// redclawTenantID maps an authenticated pocket workspace onto the RedClaw
// tenant this pocketd is wired to. Single-tenant deploys always use
// POCKET_REDCLAW_TENANT_ID so JWT workspace ids never 403 the bridge.
func (s *Server) redclawTenantID(claims *authClaims) string {
	if s.cfg.RedClawTenantID != "" {
		return s.cfg.RedClawTenantID
	}
	if claims != nil {
		return claims.WorkspaceID
	}
	return ""
}
