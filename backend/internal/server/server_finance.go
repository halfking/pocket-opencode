// internal/server/server_finance.go
package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/finance"
)

func (s *Server) handleFinance(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListFinance(w, r)
	case http.MethodPost:
		s.handleCreateFinance(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleFinanceOps(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/api/finance/"):]

	switch path {
	case "parse":
		s.handleParseFinance(w, r)
		return
	case "stats":
		s.handleFinanceStats(w, r)
		return
	}

	// /api/finance/{id} — 获取或删除
	id := path
	if id == "" {
		http.Error(w, `{"error":"missing transaction id"}`, http.StatusBadRequest)
		return
	}

	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		tx, err := s.financeStore.GetScoped(id, uid, wsID)
		if err != nil {
			http.Error(w, `{"error":"transaction not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(tx); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	case http.MethodDelete:
		if err := s.financeStore.DeleteScoped(id, uid, wsID); err != nil {
			http.Error(w, `{"error":"transaction not found"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListFinance(w http.ResponseWriter, r *http.Request) {
	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	transactions, err := s.financeStore.ListScoped(uid, wsID)
	if err != nil {
		http.Error(w, `{"error":"failed to list transactions"}`, http.StatusInternalServerError)
		return
	}
	if transactions == nil {
		transactions = []*finance.Transaction{}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"transactions": transactions,
		"total":        len(transactions),
	}); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func (s *Server) handleCreateFinance(w http.ResponseWriter, r *http.Request) {
	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	var req finance.CreateTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	tx, err := s.financeStore.CreateScoped(req, uid, wsID)
	if err != nil {
		http.Error(w, `{"error":"failed to create transaction"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(tx); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func (s *Server) handleParseFinance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Text == "" {
		http.Error(w, `{"error":"text is required"}`, http.StatusBadRequest)
		return
	}

	recognizer := finance.NewRecognizer()
	result := recognizer.Parse(req.Text)
	if result == nil {
		http.Error(w, `{"error":"unable to parse finance text"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

func (s *Server) handleFinanceStats(w http.ResponseWriter, r *http.Request) {
	// Get authenticated user identity
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	query := finance.StatsQuery{
		Month:    r.URL.Query().Get("month"),
		Category: r.URL.Query().Get("category"),
	}
	// tz = 客户端时区偏移分钟数（东八区=480，-getTimezoneOffset()）。显式提供时
	// 服务端按用户本地日历月分桶，避免跨时区部署统计错位。
	if raw := strings.TrimSpace(r.URL.Query().Get("tz")); raw != "" {
		tz, err := strconv.Atoi(raw)
		if err != nil || tz < -720 || tz > 840 {
			http.Error(w, `{"error":"tz must be an integer offset in minutes (-720..840)"}`, http.StatusBadRequest)
			return
		}
		query.TZOffsetMinutes = &tz
	}

	stats, err := s.financeStore.GetStatsScoped(query, uid, wsID)
	if err != nil {
		http.Error(w, `{"error":"failed to get statistics"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}
