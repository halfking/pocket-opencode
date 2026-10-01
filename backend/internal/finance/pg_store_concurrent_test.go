package finance

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestPGStore_CreateScoped_Concurrent 并发同一 note_ref 双写：验证 ON CONFLICT
// DO NOTHING 命中后 RowsAffected==0 的回查分支——所有并发调用方都拿到同一条
// 记录、库中只有一行、无 error。此前该分支只有内存版测试 + SQL 语义推演。
// 需要 PG；无测试 DSN 时 skip。所有 PG 访问都经 newIsolatedPGPool，
// 绝不落到生产 schema 上。
func TestPGStore_CreateScoped_Concurrent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := newIsolatedPGPool(t)

	store, err := NewPGStore(ctx, pool)
	if err != nil {
		t.Fatalf("NewPGStore: %v", err)
	}

	const (
		owner = "user-conc"
		ws    = "ws-conc"
		nref  = "note:conc_1"
		// 隔离：压测前清掉同键残留（隔离 schema 不可用时靠 owner/ws + 键前缀隔离）
	)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx,
			`DELETE FROM finance_transactions WHERE owner_id=$1 AND workspace_id=$2`, owner, ws)
	})
	if _, err := pool.Exec(ctx,
		`DELETE FROM finance_transactions WHERE owner_id=$1 AND workspace_id=$2`, owner, ws); err != nil {
		t.Fatalf("cleanup pre: %v", err)
	}

	const n = 32
	req := CreateTransactionRequest{
		Type:     "expense",
		Amount:   88.5,
		Category: "餐饮",
		Source:   "auto",
		NoteRef:  nref,
	}

	start := make(chan struct{})
	results := make(chan struct {
		id  string
		err error
	}, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tx, err := store.CreateScoped(req, owner, ws)
			if err != nil {
				results <- struct {
					id  string
					err error
				}{id: "", err: err}
				return
			}
			results <- struct {
				id  string
				err error
			}{id: tx.ID, err: nil}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	ids := map[string]int{}
	for r := range results {
		if r.err != nil {
			t.Fatalf("concurrent CreateScoped error: %v", r.err)
		}
		ids[r.id]++
	}
	if len(ids) != 1 {
		t.Fatalf("expected all callers to observe the same transaction, got %d distinct ids: %v", len(ids), ids)
	}

	list, err := store.ListScoped(owner, ws)
	if err != nil {
		t.Fatalf("ListScoped: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected exactly 1 row after %d concurrent creates, got %d", n, len(list))
	}
	t.Logf("concurrent create: %d callers -> 1 row (%s), all observed same id", n, list[0].ID)
}
