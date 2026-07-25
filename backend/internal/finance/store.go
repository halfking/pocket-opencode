// internal/finance/store.go
package finance

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Store 记账存储（内存实现）
type Store struct {
	mu           sync.RWMutex
	counter      atomic.Int64
	transactions map[string]*Transaction
	budgets      map[string]*Budget
}

func NewStore() *Store {
	return &Store{
		transactions: make(map[string]*Transaction),
		budgets:      make(map[string]*Budget),
	}
}

func (s *Store) Create(req CreateTransactionRequest) (*Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if req.Type != "income" && req.Type != "expense" {
		return nil, fmt.Errorf("type must be income or expense")
	}
	if req.Category == "" {
		req.Category = "其他"
	}

	id := s.counter.Add(1)
	tx := &Transaction{
		ID:        fmt.Sprintf("txn_%d_%d", time.Now().UnixNano(), id),
		Type:      req.Type,
		Amount:    req.Amount,
		Category:  req.Category,
		Note:      req.Note,
		Tags:      req.Tags,
		ProjectID: req.ProjectID,
		Source:    "manual",
		CreatedAt: time.Now(),
	}

	s.transactions[tx.ID] = tx
	return tx, nil
}

func (s *Store) Get(id string) (*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tx, ok := s.transactions[id]
	if !ok {
		return nil, fmt.Errorf("transaction not found: %s", id)
	}
	return tx, nil
}

func (s *Store) List() ([]*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Transaction
	for _, tx := range s.transactions {
		result = append(result, tx)
	}
	return result, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.transactions[id]; !ok {
		return fmt.Errorf("transaction not found: %s", id)
	}
	delete(s.transactions, id)
	return nil
}

func (s *Store) GetStats(query StatsQuery) (*MonthlyStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &MonthlyStats{
		Month:      query.Month,
		ByCategory: make(map[string]float64),
	}

	for _, tx := range s.transactions {
		if query.Category != "" && tx.Category != query.Category {
			continue
		}

		if tx.Type == "income" {
			stats.TotalIncome += tx.Amount
		} else {
			stats.TotalExpense += tx.Amount
			stats.ByCategory[tx.Category] += tx.Amount
		}
		stats.Count++
	}

	stats.Balance = stats.TotalIncome - stats.TotalExpense
	return stats, nil
}