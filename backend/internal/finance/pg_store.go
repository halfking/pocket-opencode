// internal/finance/pg_store.go
package finance

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore PostgreSQL 记账存储实现
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore 创建 PostgreSQL 存储实例，自动执行 migration
func NewPGStore(ctx context.Context, pool *pgxpool.Pool) (*PGStore, error) {
	s := &PGStore{pool: pool}
	if err := s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("finance migration failed: %w", err)
	}
	return s, nil
}

func (s *PGStore) migrate(ctx context.Context) error {
	schema := `
CREATE TABLE IF NOT EXISTS finance_transactions (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL DEFAULT 'default',
  type TEXT NOT NULL CHECK (type IN ('income', 'expense')),
  amount NUMERIC(12,2) NOT NULL CHECK (amount > 0),
  category TEXT NOT NULL,
  note TEXT,
  tags JSONB,
  project_id TEXT,
  source TEXT NOT NULL DEFAULT 'manual',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_finance_txn_owner_ws ON finance_transactions(owner_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_finance_txn_created ON finance_transactions(created_at DESC);
	`
	_, err := s.pool.Exec(ctx, schema)
	return err
}

func (s *PGStore) CreateScoped(req CreateTransactionRequest, ownerID, workspaceID string) (*Transaction, error) {
	if req.Amount <= 0 {
		return nil, fmt.Errorf("amount must be positive, got: %f", req.Amount)
	}
	if req.Type != TransactionTypeIncome && req.Type != TransactionTypeExpense {
		return nil, fmt.Errorf("type must be income or expense, got: %s", req.Type)
	}
	if req.Category == "" {
		req.Category = "其他"
	}
	if ownerID == "" || workspaceID == "" {
		return nil, fmt.Errorf("owner_id and workspace_id are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx := &Transaction{
		ID:          fmt.Sprintf("txn_%d", time.Now().UnixNano()),
		OwnerID:     ownerID,
		WorkspaceID: workspaceID,
		Type:        req.Type,
		Amount:      req.Amount,
		Category:    req.Category,
		Note:        req.Note,
		Tags:        req.Tags,
		ProjectID:   req.ProjectID,
		Source:      req.Source,
		CreatedAt:   time.Now(),
	}
	if tx.Source == "" {
		tx.Source = "manual"
	}

	q := `INSERT INTO finance_transactions
		(id, owner_id, workspace_id, type, amount, category, note, tags, project_id, source, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	_, err := s.pool.Exec(ctx, q,
		tx.ID, tx.OwnerID, tx.WorkspaceID, tx.Type, tx.Amount,
		tx.Category, tx.Note, tx.Tags, tx.ProjectID, tx.Source, tx.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert transaction: %w", err)
	}
	return tx, nil
}

const financeTxSelectCols = `id, owner_id, workspace_id, type, amount, category,
	COALESCE(note, ''), COALESCE(tags, '[]'::jsonb) AS tags, COALESCE(project_id, ''), source, created_at`

func scanTransaction(row pgx.Row) (*Transaction, error) {
	var tx Transaction
	err := row.Scan(
		&tx.ID, &tx.OwnerID, &tx.WorkspaceID, &tx.Type, &tx.Amount,
		&tx.Category, &tx.Note, &tx.Tags, &tx.ProjectID, &tx.Source, &tx.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

func (s *PGStore) GetScoped(id, ownerID, workspaceID string) (*Transaction, error) {
	if ownerID == "" || workspaceID == "" {
		return nil, fmt.Errorf("owner_id and workspace_id are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	q := `SELECT ` + financeTxSelectCols + `
		FROM finance_transactions
		WHERE id=$1 AND owner_id=$2 AND workspace_id=$3`
	tx, err := scanTransaction(s.pool.QueryRow(ctx, q, id, ownerID, workspaceID))
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("transaction not found")
	}
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func (s *PGStore) ListScoped(ownerID, workspaceID string) ([]*Transaction, error) {
	if ownerID == "" || workspaceID == "" {
		return nil, fmt.Errorf("owner_id and workspace_id are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	q := `SELECT ` + financeTxSelectCols + `
		FROM finance_transactions
		WHERE owner_id=$1 AND workspace_id=$2
		ORDER BY created_at DESC LIMIT 500`
	rows, err := s.pool.Query(ctx, q, ownerID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*Transaction
	for rows.Next() {
		tx, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, tx)
	}
	return result, rows.Err()
}

func (s *PGStore) DeleteScoped(id, ownerID, workspaceID string) error {
	if ownerID == "" || workspaceID == "" {
		return fmt.Errorf("owner_id and workspace_id are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	q := `DELETE FROM finance_transactions WHERE id=$1 AND owner_id=$2 AND workspace_id=$3`
	tag, err := s.pool.Exec(ctx, q, id, ownerID, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("transaction not found")
	}
	return nil
}

func (s *PGStore) GetStatsScoped(query StatsQuery, ownerID, workspaceID string) (*MonthlyStats, error) {
	if ownerID == "" || workspaceID == "" {
		return nil, fmt.Errorf("owner_id and workspace_id are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stats := &MonthlyStats{
		Month:      query.Month,
		ByCategory: make(map[string]float64),
	}

	// 构建查询条件
	where := "owner_id=$1 AND workspace_id=$2"
	args := []interface{}{ownerID, workspaceID}
	if query.Month != "" {
		where += " AND to_char(created_at, 'YYYY-MM')=$3"
		args = append(args, query.Month)
	}
	if query.Category != "" {
		where += fmt.Sprintf(" AND category=$%d", len(args)+1)
		args = append(args, query.Category)
	}

	// 总计查询
	q := fmt.Sprintf(`
		SELECT 
			COALESCE(SUM(CASE WHEN type='income' THEN amount ELSE 0 END), 0) AS total_income,
			COALESCE(SUM(CASE WHEN type='expense' THEN amount ELSE 0 END), 0) AS total_expense,
			COUNT(*) AS count
		FROM finance_transactions
		WHERE %s
	`, where)
	err := s.pool.QueryRow(ctx, q, args...).Scan(&stats.TotalIncome, &stats.TotalExpense, &stats.Count)
	if err != nil {
		return nil, err
	}
	stats.Balance = stats.TotalIncome - stats.TotalExpense

	// 分类统计
	q2 := fmt.Sprintf(`
		SELECT category, SUM(amount) FROM finance_transactions
		WHERE %s
		GROUP BY category
	`, where)
	rows, err := s.pool.Query(ctx, q2, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var cat string
		var amt float64
		if err := rows.Scan(&cat, &amt); err != nil {
			return nil, err
		}
		stats.ByCategory[cat] = amt
	}

	return stats, rows.Err()
}
