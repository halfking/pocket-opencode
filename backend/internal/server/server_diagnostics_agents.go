package server

import "net/http"

// handleDiagnosticsAgents 返回所有 ACP agent adapter 的健康状态。
//
// 用途：让运维/前端快速看到哪些 agent 在线、哪些不可达。
// 响应示例：
//
//	{
//	  "registered": [
//	    {"ref": {"type":"opencode","target":"http://localhost:4096"}, "type": "opencode"},
//	    {"ref": {"type":"acp-stdio","target":"/bin/codex"}, "type": "acp-stdio"}
//	  ],
//	  "health": {
//	    "opencode:http://localhost:4096": {"up": true},
//	    "acp-stdio:/bin/codex": {"up": false, "error": "agent unreachable"}
//	  }
//	}
func (s *Server) handleDiagnosticsAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.agents == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"registered": []any{},
			"health":     map[string]any{},
			"note":       "no agents registered (s.agents == nil)",
		})
		return
	}

	registered := make([]map[string]any, 0)
	for _, ra := range s.agents.All() {
		registered = append(registered, map[string]any{
			"ref":  ra.Ref,
			"type": ra.Adapter.AdapterType(),
		})
	}

	health := s.agents.HealthCheckAll(r.Context())
	healthOut := make(map[string]any, len(health))
	for ref, st := range health {
		key := ref.String()
		entry := map[string]any{"up": st.Up}
		if st.Error != "" {
			entry["error"] = st.Error
		}
		healthOut[key] = entry
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"registered": registered,
		"health":     healthOut,
	})
}
