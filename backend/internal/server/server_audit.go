package server

import (
	"encoding/json"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

func (s *Server) handleAuditLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.auditStore == nil {
		http.Error(w, "audit not configured", http.StatusServiceUnavailable)
		return
	}

	query := redclaw.AuditQuery{
		TenantID: r.URL.Query().Get("tenant_id"),
		UserID:   r.URL.Query().Get("user_id"),
		Action:   r.URL.Query().Get("action"),
	}

	entries, err := s.auditStore.Query(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"entries": entries,
		"total":   len(entries),
	})
}