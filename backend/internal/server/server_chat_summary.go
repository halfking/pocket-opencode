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
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		summary, err := s.chatSummaryStore.Get(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(summary)

	case http.MethodDelete:
		if err := s.chatSummaryStore.Delete(id); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListChatSummaries(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channel_id")

	summaries, err := s.chatSummaryStore.List(channelID, 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"summaries": summaries,
		"total":     len(summaries),
	})
}

func (s *Server) handleCreateChatSummary(w http.ResponseWriter, r *http.Request) {
	var req cs.CreateSummaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Channel == "" || len(req.Messages) == 0 {
		http.Error(w, "channel and messages are required", http.StatusBadRequest)
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

	// 保存
	if err := s.chatSummaryStore.Create(summary); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(summary)
}