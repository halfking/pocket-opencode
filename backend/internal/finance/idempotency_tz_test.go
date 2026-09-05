// internal/finance/idempotency_tz_test.go
package finance

import (
	"testing"
	"time"
)

func TestStore_CreateScoped_NoteRefIdempotent(t *testing.T) {
	s := NewStore()

	req := CreateTransactionRequest{
		Type:     "expense",
		Amount:   32,
		Category: "交通",
		Source:   "auto",
		NoteRef:  "note:n_1",
	}
	first, err := s.CreateScoped(req, "u1", "default")
	if err != nil {
		t.Fatalf("first CreateScoped: %v", err)
	}

	// 同幂等键再次入账：返回既有记录，不新增
	second, err := s.CreateScoped(req, "u1", "default")
	if err != nil {
		t.Fatalf("second CreateScoped: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected same transaction, got %s vs %s", first.ID, second.ID)
	}

	list, err := s.ListScoped("u1", "default")
	if err != nil {
		t.Fatalf("ListScoped: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 transaction after duplicate create, got %d", len(list))
	}

	// 空幂等键不受唯一约束：每次都新建
	if _, err := s.CreateScoped(CreateTransactionRequest{
		Type: "expense", Amount: 9, Category: "餐饮",
	}, "u1", "default"); err != nil {
		t.Fatalf("empty NoteRef create: %v", err)
	}
	other, err := s.CreateScoped(CreateTransactionRequest{
		Type: "expense", Amount: 5, Category: "餐饮", Source: "auto", NoteRef: "note:n_2",
	}, "u1", "default")
	if err != nil {
		t.Fatalf("other NoteRef create: %v", err)
	}
	if other.ID == first.ID {
		t.Errorf("different NoteRef should create new transaction")
	}

	// 幂等键按 owner/workspace 隔离
	if _, err := s.CreateScoped(req, "u2", "default"); err != nil {
		t.Fatalf("other owner create: %v", err)
	}

	// GetByNoteRefScoped 命中与未命中
	got, err := s.GetByNoteRefScoped("note:n_1", "u1", "default")
	if err != nil || got == nil || got.ID != first.ID {
		t.Errorf("GetByNoteRefScoped hit: got=%v err=%v", got, err)
	}
	miss, err := s.GetByNoteRefScoped("note:n_x", "u1", "default")
	if err != nil || miss != nil {
		t.Errorf("GetByNoteRefScoped miss: got=%v err=%v", miss, err)
	}
}

func TestStore_GetStatsScoped_TimezoneBucketing(t *testing.T) {
	s := NewStore()
	loc := time.FixedZone("UTC+8", 8*60*60)
	// 2026-07-01 00:30 (UTC+8) = 2026-06-30 16:30 UTC
	t1 := time.Date(2026, 6, 30, 16, 30, 0, 0, time.UTC)
	// 2026-07-10 12:00 UTC：任何时区都属于 7 月
	t2 := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	utc8 := 480
	for _, tx := range []*Transaction{
		{ID: "a", OwnerID: "u1", WorkspaceID: "default", Type: "expense", Amount: 100, Category: "餐饮", CreatedAt: t1.In(loc)},
		{ID: "b", OwnerID: "u1", WorkspaceID: "default", Type: "expense", Amount: 50, Category: "交通", CreatedAt: t2},
	} {
		s.transactions[tx.ID] = tx
	}

	// 客户端在 UTC+8：t1 属于 7 月 → 两笔都计入
	tz8, err := s.GetStatsScoped(StatsQuery{Month: "2026-07", TZOffsetMinutes: &utc8}, "u1", "default")
	if err != nil {
		t.Fatalf("stats tz+8: %v", err)
	}
	if tz8.Count != 2 {
		t.Errorf("UTC+8 July count: expected 2, got %d", tz8.Count)
	}

	// 客户端在 UTC：t1 属于 6 月 → 只计 t2
	utc0 := 0
	tz0, err := s.GetStatsScoped(StatsQuery{Month: "2026-07", TZOffsetMinutes: &utc0}, "u1", "default")
	if err != nil {
		t.Fatalf("stats utc: %v", err)
	}
	if tz0.Count != 1 || tz0.TotalExpense != 50 {
		t.Errorf("UTC July: expected count=1 expense=50, got count=%d expense=%f", tz0.Count, tz0.TotalExpense)
	}

	// 不传时区：保持旧行为（按时间自带时区分桶），t1 (UTC+8 表示) 属于 7 月
	legacy, err := s.GetStatsScoped(StatsQuery{Month: "2026-07"}, "u1", "default")
	if err != nil {
		t.Fatalf("stats legacy: %v", err)
	}
	if legacy.Count != 2 {
		t.Errorf("legacy July count: expected 2, got %d", legacy.Count)
	}
}
