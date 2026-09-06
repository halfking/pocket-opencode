// internal/finance/idempotency_created_test.go
package finance

import "testing"

func TestCreateScopedWithStatus_CreatedFlag(t *testing.T) {
	s := NewStore()

	req := CreateTransactionRequest{
		Type:     "expense",
		Amount:   50,
		Category: "餐饮",
		Source:   "invoice",
		NoteRef:  "invoice:inv_1",
	}

	// 首次入账：created=true
	tx1, created1, err := s.CreateScopedWithStatus(req, "user1", "default")
	if err != nil {
		t.Fatalf("first CreateScopedWithStatus: %v", err)
	}
	if !created1 {
		t.Errorf("first call: expected created=true, got false")
	}
	if tx1.Amount != 50 || tx1.NoteRef != "invoice:inv_1" {
		t.Errorf("first call: unexpected tx data=%+v", tx1)
	}

	// 幂等键重复：created=false，返回同一记录
	tx2, created2, err := s.CreateScopedWithStatus(req, "user1", "default")
	if err != nil {
		t.Fatalf("second CreateScopedWithStatus: %v", err)
	}
	if created2 {
		t.Errorf("second call: expected created=false, got true")
	}
	if tx2.ID != tx1.ID {
		t.Errorf("second call: expected same ID %s, got %s", tx1.ID, tx2.ID)
	}

	// 不同 workspace：新建，created=true
	tx3, created3, err := s.CreateScopedWithStatus(req, "user1", "ws-other")
	if err != nil {
		t.Fatalf("other workspace CreateScopedWithStatus: %v", err)
	}
	if !created3 {
		t.Errorf("other workspace: expected created=true, got false")
	}
	if tx3.ID == tx1.ID {
		t.Errorf("other workspace: should create new tx, got same ID=%s", tx3.ID)
	}

	// 空幂等键：每次都新建
	emptyReq := CreateTransactionRequest{Type: "income", Amount: 100, Category: "工资"}
	tx4, created4, err := s.CreateScopedWithStatus(emptyReq, "user1", "default")
	if err != nil {
		t.Fatalf("empty NoteRef first: %v", err)
	}
	if !created4 {
		t.Errorf("empty NoteRef first: expected created=true, got false")
	}
	tx5, created5, err := s.CreateScopedWithStatus(emptyReq, "user1", "default")
	if err != nil {
		t.Fatalf("empty NoteRef second: %v", err)
	}
	if !created5 {
		t.Errorf("empty NoteRef second: expected created=true, got false")
	}
	if tx5.ID == tx4.ID {
		t.Errorf("empty NoteRef: should create separate tx, got same ID=%s", tx5.ID)
	}
}
