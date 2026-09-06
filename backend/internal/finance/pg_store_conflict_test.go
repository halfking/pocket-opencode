// internal/finance/pg_store_conflict_test.go
package finance

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPGStore_ConflictRecovery 验证 ON CONFLICT 后回查失败时的错误处理
func TestPGStore_ConflictRecovery(t *testing.T) {
	dsn := testPGDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping PG conflict recovery test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	s, err := NewPGStore(ctx, pool)
	if err != nil {
		t.Fatalf("NewPGStore: %v", err)
	}

	// 清理测试数据
	_, _ = pool.Exec(ctx, `DELETE FROM finance_transactions WHERE note_ref LIKE 'test-conflict-%'`)

	req := CreateTransactionRequest{
		Type:     "expense",
		Amount:   100,
		Category: "测试",
		NoteRef:  "test-conflict-orphan",
		Source:   "manual",
	}

	// 首次插入成功
	tx1, created1, err := s.CreateScopedWithStatus(req, "test-user", "test-ws")
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !created1 {
		t.Errorf("first insert: expected created=true, got false")
	}

	// 立即删除该记录（模拟并发删除窗口）
	_, err = pool.Exec(ctx, `DELETE FROM finance_transactions WHERE id=$1`, tx1.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	// 再次插入相同 note_ref：预检回查不到（已删除），插入应该成功
	tx2, created2, err := s.CreateScopedWithStatus(req, "test-user", "test-ws")
	if err != nil {
		t.Fatalf("second insert after delete: %v", err)
	}
	if !created2 {
		t.Errorf("second insert: expected created=true (record was deleted), got false")
	}
	if tx2.ID == tx1.ID {
		t.Errorf("second insert: should generate new ID after delete, got same ID=%s", tx2.ID)
	}

	// 清理
	_, _ = pool.Exec(ctx, `DELETE FROM finance_transactions WHERE note_ref='test-conflict-orphan'`)
}

// TestPGStore_ConcurrentConflictRetry 验证真实并发冲突时的回查成功路径
func TestPGStore_ConcurrentConflictRetry(t *testing.T) {
	dsn := testPGDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping PG concurrent conflict test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	s, err := NewPGStore(ctx, pool)
	if err != nil {
		t.Fatalf("NewPGStore: %v", err)
	}

	// 清理测试数据
	noteRef := "test-conflict-concurrent-" + time.Now().Format("20060102150405")
	_, _ = pool.Exec(ctx, `DELETE FROM finance_transactions WHERE note_ref=$1`, noteRef)

	req := CreateTransactionRequest{
		Type:     "expense",
		Amount:   200,
		Category: "测试",
		NoteRef:  noteRef,
		Source:   "manual",
	}

	// 模拟并发：先直接插入一条记录
	directTx := &Transaction{
		ID:          "direct-insert-" + time.Now().Format("20060102150405.000"),
		OwnerID:     "test-user",
		WorkspaceID: "test-ws",
		Type:        req.Type,
		Amount:      req.Amount,
		Category:    req.Category,
		Note:        "direct insert to simulate conflict",
		Tags:        []string{},
		Source:      req.Source,
		NoteRef:     noteRef,
		CreatedAt:   time.Now(),
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO finance_transactions
			(id, owner_id, workspace_id, type, amount, category, note, tags, project_id, source, note_ref, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, directTx.ID, directTx.OwnerID, directTx.WorkspaceID, directTx.Type, directTx.Amount,
		directTx.Category, directTx.Note, directTx.Tags, directTx.ProjectID, directTx.Source,
		directTx.NoteRef, directTx.CreatedAt)
	if err != nil {
		t.Fatalf("direct insert: %v", err)
	}

	// 现在用 CreateScopedWithStatus 插入相同 note_ref：应该走预检幂等路径或冲突回查路径
	result, created, err := s.CreateScopedWithStatus(req, "test-user", "test-ws")
	if err != nil {
		t.Fatalf("CreateScopedWithStatus after conflict: %v", err)
	}
	if created {
		t.Errorf("expected created=false (conflict), got true")
	}
	if result.ID != directTx.ID {
		t.Errorf("expected existing record ID=%s, got %s", directTx.ID, result.ID)
	}

	// 清理
	_, _ = pool.Exec(ctx, `DELETE FROM finance_transactions WHERE note_ref=$1`, noteRef)
}
