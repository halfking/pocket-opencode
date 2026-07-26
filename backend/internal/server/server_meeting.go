// internal/server/server_meeting.go
package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

func (s *Server) handleMeetings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListMeetings(w, r)
	case http.MethodPost:
		s.handleCreateMeeting(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMeetingOps(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/api/meetings/"):]

	// 处理子路径: /api/meetings/{id}/transcribe 或 /api/meetings/{id}/summarize
	if strings.Contains(path, "/") {
		parts := strings.SplitN(path, "/", 2)
		id := parts[0]
		action := parts[1]

		switch action {
		case "transcribe":
			s.handleTranscribeMeeting(w, r, id)
			return
		case "summarize":
			s.handleSummarizeMeeting(w, r, id)
			return
		}
	}

	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	// /api/meetings/{id} — 获取或删除
	id := path
	switch r.Method {
	case http.MethodGet:
		m, err := s.meetingStore.GetScoped(id, uid, wsID)
		if err != nil {
			http.Error(w, `{"error":"meeting not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(m); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	case http.MethodDelete:
		if err := s.meetingStore.DeleteScoped(id, uid, wsID); err != nil {
			http.Error(w, `{"error":"meeting not found"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListMeetings(w http.ResponseWriter, r *http.Request) {
	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	meetings, err := s.meetingStore.ListScoped(uid, wsID)
	if err != nil {
		http.Error(w, `{"error":"failed to list meetings"}`, http.StatusInternalServerError)
		return
	}
	if meetings == nil {
		meetings = []*meeting.Meeting{}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"meetings": meetings,
		"total":    len(meetings),
	}); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func (s *Server) handleCreateMeeting(w http.ResponseWriter, r *http.Request) {
	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	var req meeting.CreateMeetingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		req.Title = "未命名会议"
	}

	m, err := s.meetingStore.CreateScoped(req, uid, wsID)
	if err != nil {
		http.Error(w, `{"error":"failed to create meeting"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(m); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func (s *Server) handleTranscribeMeeting(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if id == "" {
		http.Error(w, `{"error":"missing meeting id"}`, http.StatusBadRequest)
		return
	}

	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	m, err := s.meetingStore.GetScoped(id, uid, wsID)
	if err != nil {
		http.Error(w, `{"error":"meeting not found"}`, http.StatusNotFound)
		return
	}

	m.Status = "transcribing"
	s.meetingStore.UpdateScoped(m, uid, wsID)

	// 读取音频数据
	r.Body = http.MaxBytesReader(w, r.Body, maxAudioBodyBytes)
	audioData, err := io.ReadAll(r.Body)
	if err != nil {
		m.Status = "failed"
		s.meetingStore.UpdateScoped(m, uid, wsID)
		http.Error(w, `{"error":"failed to read audio data"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(audioData) == 0 {
		m.Status = "failed"
		s.meetingStore.UpdateScoped(m, uid, wsID)
		http.Error(w, `{"error":"empty audio data"}`, http.StatusBadRequest)
		return
	}

	// 使用 STT 转写（如果配置了）
	if s.transcriber != nil {
		result, err := s.transcriber.Transcribe(r.Context(), audioData, "meeting.wav")
		if err != nil {
			m.Status = "failed"
			s.meetingStore.UpdateScoped(m, uid, wsID)
			http.Error(w, `{"error":"transcription failed"}`, http.StatusInternalServerError)
			return
		}
		m.Transcript = result.Text
	} else {
		m.Transcript = "（STT 未配置，请设置 POCKET_GROQ_API_KEY）"
	}

	m.Status = "transcribed"
	s.meetingStore.UpdateScoped(m, uid, wsID)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"status":     "transcribed",
		"meeting_id": id,
	}); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func (s *Server) handleSummarizeMeeting(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if id == "" {
		http.Error(w, `{"error":"missing meeting id"}`, http.StatusBadRequest)
		return
	}

	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	m, err := s.meetingStore.GetScoped(id, uid, wsID)
	if err != nil {
		http.Error(w, `{"error":"meeting not found"}`, http.StatusNotFound)
		return
	}

	if m.Transcript == "" {
		http.Error(w, `{"error":"meeting has no transcript, transcribe first"}`, http.StatusBadRequest)
		return
	}

	m.Status = "summarizing"
	s.meetingStore.UpdateScoped(m, uid, wsID)

	summary, err := meeting.SummarizeTranscript(m.Transcript, m.Title)
	if err != nil {
		m.Status = "failed"
		s.meetingStore.UpdateScoped(m, uid, wsID)
		http.Error(w, `{"error":"summarization failed"}`, http.StatusInternalServerError)
		return
	}

	m.Summary = summary.Summary
	m.KeyDecisions = summary.KeyDecisions
	m.ActionItems = summary.ActionItems
	m.Status = "done"
	s.meetingStore.UpdateScoped(m, uid, wsID)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(m); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}
