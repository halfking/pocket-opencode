package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) handleTaskSessionDetail(w http.ResponseWriter, r *http.Request, taskID, sessionID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	title := sessionID
	if s.companion != nil {
		if sess, err := s.companion.GetSession("", sessionID); err == nil && sess.Title != "" {
			title = sess.Title
		}
	}
	writeSessionJSON(w, map[string]any{
		"id": sessionID, "taskId": taskID, "title": title,
		"tokens": map[string]any{"input": nil, "cacheRead": nil, "output": nil},
	})
}

func (s *Server) handleTaskSessionTranscript(w http.ResponseWriter, r *http.Request, taskID, sessionID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.companion == nil {
		http.Error(w, "companion not configured", http.StatusServiceUnavailable)
		return
	}
	kind := r.URL.Query().Get("kind")
	types := r.URL.Query().Get("types")
	sess, msgs, err := s.companion.GetTranscript(kind, sessionID, types)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	_ = taskID
	writeSessionJSON(w, map[string]any{"session": sess, "messages": msgs})
}

func (s *Server) handleExtractTitle(w http.ResponseWriter, r *http.Request, taskID, sessionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	title := sessionID
	if s.companion != nil {
		_, msgs, err := s.companion.GetTranscript("", sessionID, "user")
		if err == nil {
			for _, m := range msgs {
				if t := strings.TrimSpace(m.Text); t != "" {
					runes := []rune(t)
					if len(runes) > 80 {
						t = string(runes[:80])
					}
					title = t
					break
				}
			}
		}
	}
	_ = taskID
	writeSessionJSON(w, map[string]any{"title": title})
}

func (s *Server) handleSessionSummarize(w http.ResponseWriter, r *http.Request, taskID, sessionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	summary := ""
	if s.companion != nil {
		_, msgs, err := s.companion.GetTranscript("", sessionID, "user,assistant")
		if err == nil {
			var b strings.Builder
			for _, m := range msgs {
				if m.Text == "" {
					continue
				}
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(m.Type)
				b.WriteString(": ")
				b.WriteString(m.Text)
				if b.Len() > 1500 {
					break
				}
			}
			summary = b.String()
			if len([]rune(summary)) > 400 {
				summary = string([]rune(summary)[:400]) + "…"
			}
		}
	}
	if summary == "" {
		summary = "暂无摘要"
	}
	_ = taskID
	writeSessionJSON(w, map[string]any{"summary": summary})
}

func writeSessionJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
