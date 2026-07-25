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

	// /api/meetings/{id} — 获取或删除
	id := path
	switch r.Method {
	case http.MethodGet:
		m, err := s.meetingStore.Get(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(m)

	case http.MethodDelete:
		if err := s.meetingStore.Delete(id); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListMeetings(w http.ResponseWriter, r *http.Request) {
	meetings, err := s.meetingStore.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"meetings": meetings,
		"total":    len(meetings),
	})
}

func (s *Server) handleCreateMeeting(w http.ResponseWriter, r *http.Request) {
	var req meeting.CreateMeetingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Title == "" {
		req.Title = "未命名会议"
	}

	m, err := s.meetingStore.Create(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(m)
}

func (s *Server) handleTranscribeMeeting(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	m, err := s.meetingStore.Get(id)
	if err != nil {
		http.Error(w, "meeting not found", http.StatusNotFound)
		return
	}

	m.Status = "transcribing"
	s.meetingStore.Update(m)

	// 读取音频数据
	audioData, err := io.ReadAll(r.Body)
	if err != nil {
		m.Status = "failed"
		s.meetingStore.Update(m)
		http.Error(w, "failed to read audio: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// 使用 STT 转写（如果配置了）
	if s.transcriber != nil {
		result, err := s.transcriber.Transcribe(r.Context(), audioData, "meeting.wav")
		if err != nil {
			m.Status = "failed"
			s.meetingStore.Update(m)
			http.Error(w, "transcription failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		m.Transcript = result.Text
	} else {
		m.Transcript = "（STT 未配置，请设置 POCKET_GROQ_API_KEY）"
	}

	m.Status = "transcribed"
	s.meetingStore.Update(m)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":     "transcribed",
		"meeting_id": id,
	})
}

func (s *Server) handleSummarizeMeeting(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	m, err := s.meetingStore.Get(id)
	if err != nil {
		http.Error(w, "meeting not found", http.StatusNotFound)
		return
	}

	if m.Transcript == "" {
		http.Error(w, "meeting has no transcript, transcribe first", http.StatusBadRequest)
		return
	}

	m.Status = "summarizing"
	s.meetingStore.Update(m)

	summary, err := meeting.SummarizeTranscript(m.Transcript, m.Title)
	if err != nil {
		m.Status = "failed"
		s.meetingStore.Update(m)
		http.Error(w, "summarization failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	m.Summary = summary.Summary
	m.KeyDecisions = summary.KeyDecisions
	m.ActionItems = summary.ActionItems
	m.Status = "done"
	s.meetingStore.Update(m)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}