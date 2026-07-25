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
		http.Error(w, `{"error":"invalid request: `+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	// 从 JWT 上下文提取租户信息
	claims := extractClaims(r)
	if claims != nil {
		tenantCtx := redclaw.ExtractTenantContext(claims)
		if req.TenantID == "" {
			req.TenantID = tenantCtx.TenantID
		}
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

	resp, err := s.redclawBridge.KnowledgeSearch(req)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// extractClaims 从请求上下文中提取 JWT claims（简化版）
func extractClaims(r *http.Request) map[string]interface{} {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		return nil
	}
	return map[string]interface{}{
		"sub":       userID,
		"tenant_id": r.Header.Get("X-Tenant-ID"),
	}
}