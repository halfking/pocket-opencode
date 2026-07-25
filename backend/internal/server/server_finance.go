// internal/server/server_finance.go
package server

import (
	"encoding/json"
	"net/http"

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

	switch r.Method {
	case http.MethodGet:
		tx, err := s.financeStore.Get(id)
		if err != nil {
			http.Error(w, `{"error":"transaction not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(tx); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	case http.MethodDelete:
		if err := s.financeStore.Delete(id); err != nil {
			http.Error(w, `{"error":"transaction not found"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListFinance(w http.ResponseWriter, r *http.Request) {
	transactions, err := s.financeStore.List()
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
	var req finance.CreateTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	tx, err := s.financeStore.Create(req)
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
	query := finance.StatsQuery{
		Month:    r.URL.Query().Get("month"),
		Category: r.URL.Query().Get("category"),
	}

	stats, err := s.financeStore.GetStats(query)
	if err != nil {
		http.Error(w, `{"error":"failed to get statistics"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}