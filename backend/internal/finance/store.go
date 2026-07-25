// internal/finance/store.go
package finance

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Store 记账存储（内存实现），提供线程安全的交易和预算管理
type Store struct {
	mu           sync.RWMutex
	counter      atomic.Int64
	transactions map[string]*Transaction
	budgets      map[string]*Budget
}

// NewStore 创建新的记账存储实例
func NewStore() *Store {
	return &Store{
		transactions: make(map[string]*Transaction),
		budgets:      make(map[string]*Budget),
	}
}

// Create 创建新的交易记录
// 返回创建的交易对象或验证错误
func (s *Store) Create(req CreateTransactionRequest) (*Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Amount <= 0 {
		return nil, fmt.Errorf("amount must be positive, got: %f", req.Amount)
	}
	if req.Type != TransactionTypeIncome && req.Type != TransactionTypeExpense {
		return nil, fmt.Errorf("type must be income or expense, got: %s", req.Type)
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

// Get 根据 ID 获取交易记录
// 返回交易的副本以避免外部修改
func (s *Store) Get(id string) (*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tx, ok := s.transactions[id]
	if !ok {
		return nil, fmt.Errorf("transaction not found: %s", id)
	}
	
	// 返回副本以避免竞态条件
	txCopy := *tx
	if len(tx.Tags) > 0 {
		txCopy.Tags = make([]string, len(tx.Tags))
		copy(txCopy.Tags, tx.Tags)
	}
	return &txCopy, nil
}

// List 列出所有交易记录
// 返回交易列表的副本
func (s *Store) List() ([]*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Transaction, 0, len(s.transactions))
	for _, tx := range s.transactions {
		txCopy := *tx
		if len(tx.Tags) > 0 {
			txCopy.Tags = make([]string, len(tx.Tags))
			copy(txCopy.Tags, tx.Tags)
		}
		result = append(result, &txCopy)
	}
	return result, nil
}

// Delete 删除指定 ID 的交易记录
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.transactions[id]; !ok {
		return fmt.Errorf("transaction not found: %s", id)
	}
	delete(s.transactions, id)
	return nil
}

// GetStats 获取统计数据，支持按月份和分类筛选
func (s *Store) GetStats(query StatsQuery) (*MonthlyStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &MonthlyStats{
		Month:      query.Month,
		ByCategory: make(map[string]float64),
	}

	for _, tx := range s.transactions {
		// 按月份筛选
		if query.Month != "" {
			txMonth := tx.CreatedAt.Format("2006-01")
			if txMonth != query.Month {
				continue
			}
		}
		
		// 按分类筛选
		if query.Category != "" && tx.Category != query.Category {
			continue
		}

		if tx.Type == TransactionTypeIncome {
			stats.TotalIncome += tx.Amount
		} else {
			stats.TotalExpense += tx.Amount
		}
		
		// 所有分类都记录到 ByCategory
		stats.ByCategory[tx.Category] += tx.Amount
		stats.Count++
	}

	stats.Balance = stats.TotalIncome - stats.TotalExpense
	return stats, nil
}