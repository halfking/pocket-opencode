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
	switch r.Method {
	case http.MethodGet:
		tx, err := s.financeStore.Get(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tx)

	case http.MethodDelete:
		if err := s.financeStore.Delete(id); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListFinance(w http.ResponseWriter, r *http.Request) {
	transactions, err := s.financeStore.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"transactions": transactions,
		"total":        len(transactions),
	})
}

func (s *Server) handleCreateFinance(w http.ResponseWriter, r *http.Request) {
	var req finance.CreateTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	tx, err := s.financeStore.Create(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(tx)
}

func (s *Server) handleParseFinance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	recognizer := finance.NewRecognizer()
	result := recognizer.Parse(req.Text)
	if result == nil {
		http.Error(w, "unable to parse finance text", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleFinanceStats(w http.ResponseWriter, r *http.Request) {
	query := finance.StatsQuery{
		Month:    r.URL.Query().Get("month"),
		Category: r.URL.Query().Get("category"),
	}

	stats, err := s.financeStore.GetStats(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}