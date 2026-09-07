package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type purgeEmailsBody struct {
	IDs []string `json:"ids"`
}

func (s *Server) handleEmailPurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	var body purgeEmailsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids required")
		return
	}
	if len(body.IDs) > 100 {
		writeError(w, http.StatusBadRequest, "too many ids")
		return
	}
	n, paths, err := s.emailStore.SoftDeleteEmailsScoped(
		r.Context(), body.IDs, s.userIDFromRequest(r), s.workspaceIDFromRequest(r), time.Now().UnixMilli(),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.dataDir != "" {
		for _, rel := range paths {
			_ = os.Remove(filepath.Join(s.dataDir, rel))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"purged": n})
}
