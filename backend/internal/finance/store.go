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
// Deprecated: Use CreateScoped for production code with proper ownership.
func (s *Store) Create(req CreateTransactionRequest) (*Transaction, error) {
	return s.CreateScoped(req, "", "")
}

// CreateScoped 创建新的交易记录with ownership
func (s *Store) CreateScoped(req CreateTransactionRequest, ownerID, workspaceID string) (*Transaction, error) {
	if req.Amount <= 0 {
		return nil, fmt.Errorf("amount must be positive, got: %f", req.Amount)
	}
	if req.Type != TransactionTypeIncome && req.Type != TransactionTypeExpense {
		return nil, fmt.Errorf("type must be income or expense, got: %s", req.Type)
	}
	if req.Category == "" {
		req.Category = "其他"
	}
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.counter.Add(1)
	tx := &Transaction{
		ID:          fmt.Sprintf("txn_%d_%d", time.Now().UnixNano(), id),
		OwnerID:     ownerID,
		WorkspaceID: workspaceID,
		Type:        req.Type,
		Amount:      req.Amount,
		Category:    req.Category,
		Note:        req.Note,
		Tags:        req.Tags,
		ProjectID:   req.ProjectID,
		Source:      "manual",
		CreatedAt:   time.Now(),
	}

	s.transactions[tx.ID] = tx
	return copyTransaction(tx), nil
}

// Get 根据 ID 获取交易记录
// 返回交易的副本以避免外部修改
// Deprecated: Use GetScoped for production code with ownership checks.
func (s *Store) Get(id string) (*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tx, ok := s.transactions[id]
	if !ok {
		return nil, fmt.Errorf("transaction not found: %s", id)
	}
	
	return copyTransaction(tx), nil
}

// GetScoped 根据 ID 和所有权获取交易记录
func (s *Store) GetScoped(id, ownerID, workspaceID string) (*Transaction, error) {
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	tx, ok := s.transactions[id]
	if !ok || tx.OwnerID != ownerID || tx.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("transaction not found")
	}
	
	return copyTransaction(tx), nil
}

// List 列出所有交易记录
// 返回交易列表的副本
// Deprecated: Use ListScoped for production code with ownership filtering.
func (s *Store) List() ([]*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Transaction, 0, len(s.transactions))
	for _, tx := range s.transactions {
		result = append(result, copyTransaction(tx))
	}
	return result, nil
}

// ListScoped 列出指定用户workspace的所有交易记录
func (s *Store) ListScoped(ownerID, workspaceID string) ([]*Transaction, error) {
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Transaction, 0)
	for _, tx := range s.transactions {
		if tx.OwnerID == ownerID && tx.WorkspaceID == workspaceID {
			result = append(result, copyTransaction(tx))
		}
	}
	return result, nil
}

// Delete 删除指定 ID 的交易记录
// Deprecated: Use DeleteScoped for production code with ownership checks.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.transactions[id]; !ok {
		return fmt.Errorf("transaction not found: %s", id)
	}
	delete(s.transactions, id)
	return nil
}

// DeleteScoped 删除指定 ID 的交易记录with ownership verification
func (s *Store) DeleteScoped(id, ownerID, workspaceID string) error {
	if ownerID == "" {
		return fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, ok := s.transactions[id]
	if !ok || tx.OwnerID != ownerID || tx.WorkspaceID != workspaceID {
		return fmt.Errorf("transaction not found")
	}
	delete(s.transactions, id)
	return nil
}

// GetStats 获取统计数据，支持按月份和分类筛选
// Deprecated: Use GetStatsScoped for production code with ownership filtering.
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
		
		stats.ByCategory[tx.Category] += tx.Amount
		stats.Count++
	}

	stats.Balance = stats.TotalIncome - stats.TotalExpense
	return stats, nil
}

// GetStatsScoped 获取指定用户workspace的统计数据
func (s *Store) GetStatsScoped(query StatsQuery, ownerID, workspaceID string) (*MonthlyStats, error) {
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &MonthlyStats{
		Month:      query.Month,
		ByCategory: make(map[string]float64),
	}

	for _, tx := range s.transactions {
		// Filter by ownership
		if tx.OwnerID != ownerID || tx.WorkspaceID != workspaceID {
			continue
		}

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
		
		stats.ByCategory[tx.Category] += tx.Amount
		stats.Count++
	}

	stats.Balance = stats.TotalIncome - stats.TotalExpense
	return stats, nil
}

// copyTransaction creates a deep copy of a transaction
func copyTransaction(tx *Transaction) *Transaction {
	if tx == nil {
		return nil
	}
	
	tagsCopy := make([]string, len(tx.Tags))
	copy(tagsCopy, tx.Tags)
	
	return &Transaction{
		ID:          tx.ID,
		OwnerID:     tx.OwnerID,
		WorkspaceID: tx.WorkspaceID,
		Type:        tx.Type,
		Amount:      tx.Amount,
		Category:    tx.Category,
		Note:        tx.Note,
		Tags:        tagsCopy,
		ProjectID:   tx.ProjectID,
		Source:      tx.Source,
		CreatedAt:   tx.CreatedAt,
	}
}