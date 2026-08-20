// internal/server/server_chat_summary.go
package server

import (
	"encoding/json"
	"net/http"
	"time"

	cs "github.com/halfking/pocket-opencode/backend/internal/chat_summary"
)

func (s *Server) handleChatSummaries(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListChatSummaries(w, r)
	case http.MethodPost:
		s.handleCreateChatSummary(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleChatSummaryOps(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/chat-summaries/"):]
	if id == "" {
		http.Error(w, `{"error":"missing summary id"}`, http.StatusBadRequest)
		return
	}

	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		summary, err := s.chatSummaryStore.GetScoped(id, uid, wsID)
		if err != nil {
			http.Error(w, `{"error":"summary not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(summary); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	case http.MethodDelete:
		if err := s.chatSummaryStore.DeleteScoped(id, uid, wsID); err != nil {
			http.Error(w, `{"error":"summary not found"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListChatSummaries(w http.ResponseWriter, r *http.Request) {
	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	channelID := r.URL.Query().Get("channel_id")

	summaries, err := s.chatSummaryStore.ListScoped(channelID, uid, wsID, 20)
	if err != nil {
		http.Error(w, `{"error":"failed to list summaries"}`, http.StatusInternalServerError)
		return
	}

	if summaries == nil {
		summaries = []*cs.ChatSummary{}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"summaries": summaries,
		"total":     len(summaries),
	}); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func (s *Server) handleCreateChatSummary(w http.ResponseWriter, r *http.Request) {
	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	var req cs.CreateSummaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Channel == "" || len(req.Messages) == 0 {
		http.Error(w, `{"error":"channel and messages are required"}`, http.StatusBadRequest)
		return
	}

	// 设置时间范围
	periodStart := req.PeriodStart
	periodEnd := req.PeriodEnd
	if periodEnd.IsZero() {
		periodEnd = time.Now()
	}
	if periodStart.IsZero() {
		periodStart = periodEnd.Add(-24 * time.Hour)
	}

	// 聚合消息
	aggregator := &cs.Aggregator{}
	result := aggregator.Aggregate(req.Messages, periodStart, periodEnd)

	// 生成摘要
	summarizer := &cs.Summarizer{}
	summary := summarizer.Summarize(result, req.Channel)

	// 填充元数据
	summary.Channel = req.Channel
	summary.ChannelID = req.ChannelID
	summary.PeriodStart = periodStart
	summary.PeriodEnd = periodEnd

	// 保存with ownership
	if err := s.chatSummaryStore.CreateScoped(summary, uid, wsID); err != nil {
		http.Error(w, `{"error":"failed to create summary"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(summary); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}