// internal/finance/store_test.go
package finance

import (
	"testing"
)

func TestStore_CRUD(t *testing.T) {
	s := NewStore()

	// Create expense
	tx, err := s.Create(CreateTransactionRequest{
		Type:     "expense",
		Amount:   38.00,
		Category: "餐饮",
		Note:     "午餐",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if tx.Type != "expense" {
		t.Errorf("expected expense, got %s", tx.Type)
	}
	if tx.Amount != 38.00 {
		t.Errorf("expected 38.00, got %f", tx.Amount)
	}

	// Create income
	_, err = s.Create(CreateTransactionRequest{
		Type:     "income",
		Amount:   5000.00,
		Category: "工资",
		Note:     "7月工资",
	})
	if err != nil {
		t.Fatalf("Create income failed: %v", err)
	}

	// List
	all, err := s.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 transactions, got %d", len(all))
	}

	// Get
	got, err := s.Get(tx.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Note != "午餐" {
		t.Errorf("expected note 午餐, got %s", got.Note)
	}

	// Delete
	err = s.Delete(tx.ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	all, err = s.List()
	if err != nil {
		t.Fatalf("List after delete failed: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 after delete, got %d", len(all))
	}
}

func TestStore_Stats(t *testing.T) {
	s := NewStore()
	s.Create(CreateTransactionRequest{Type: "income", Amount: 10000, Category: "工资"})
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 100, Category: "餐饮"})
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 50, Category: "交通"})

	stats, err := s.GetStats(StatsQuery{})
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats.TotalIncome != 10000 {
		t.Errorf("expected income 10000, got %f", stats.TotalIncome)
	}
	if stats.TotalExpense != 150 {
		t.Errorf("expected expense 150, got %f", stats.TotalExpense)
	}
	if stats.Balance != 9850 {
		t.Errorf("expected balance 9850, got %f", stats.Balance)
	}
	// Income categories are now included in ByCategory, so we expect 3 categories
	if len(stats.ByCategory) != 3 {
		t.Errorf("expected 3 categories (including income), got %d", len(stats.ByCategory))
	}
}

func TestStore_InvalidType(t *testing.T) {
	s := NewStore()
	_, err := s.Create(CreateTransactionRequest{Type: "invalid", Amount: 100, Category: "test"})
	if err == nil {
		t.Error("expected error for invalid type")
	}
}

func TestStore_InvalidAmount(t *testing.T) {
	s := NewStore()
	_, err := s.Create(CreateTransactionRequest{Type: "expense", Amount: 0, Category: "test"})
	if err == nil {
		t.Error("expected error for zero amount")
	}
}