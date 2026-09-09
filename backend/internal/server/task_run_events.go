package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// handleTaskRunEvents projects canonical ACC history through Pocket. The
// binding and workspace are resolved server-side; clients cannot supply a run.
func (s *Server) handleTaskRunEvents(w http.ResponseWriter, r *http.Request, taskID string) {
	if s.taskStore == nil || s.mcpClient == nil {
		s.writeStructuredError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable, "ACC task/run projection unavailable")
		return
	}
	ws := s.workspaceIDFromRequest(r)
	binding, err := s.taskStore.GetTaskRunBinding(r.Context(), ws, taskID)
	if err != nil {
		s.writeStructuredError(w, r, http.StatusNotFound, CodeNotFound, "task/run binding not found")
		return
	}
	after := uint64(0)
	raw := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("after"))
	}
	if raw != "" {
		n, e := strconv.ParseUint(raw, 10, 64)
		if e != nil {
			s.writeStructuredError(w, r, http.StatusBadRequest, CodeInvalidRequest, "after must be an unsigned sequence")
			return
		}
		after = n
	}
	events, err := s.mcpClient.ListRunEvents(r.Context(), binding.RunID, after)
	if err != nil {
		s.writeStructuredError(w, r, http.StatusBadGateway, CodeUpstreamUnavailable, "ACC run events unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	for _, ev := range events {
		payload, _ := json.Marshal(ev)
		_, _ = w.Write([]byte("id: " + strconv.FormatUint(ev.Sequence, 10) + "\nevent: " + ev.EventType + "\ndata: " + string(payload) + "\n\n"))
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
