// internal/finance/store_comprehensive_test.go
package finance

import (
	"sync"
	"testing"
	"time"
)

func TestStore_ConcurrentAccess(t *testing.T) {
	s := NewStore()
	
	// Create initial transactions
	tx1, _ := s.Create(CreateTransactionRequest{Type: "expense", Amount: 100, Category: "餐饮"})
	tx2, _ := s.Create(CreateTransactionRequest{Type: "income", Amount: 5000, Category: "工资"})
	
	var wg sync.WaitGroup
	errors := make(chan error, 100)
	
	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Get(tx1.ID)
			if err != nil {
				errors <- err
			}
		}()
	}
	
	// Concurrent writes
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := s.Create(CreateTransactionRequest{
				Type:     "expense",
				Amount:   float64(n),
				Category: "测试",
			})
			if err != nil {
				errors <- err
			}
		}(i + 1)
	}
	
	// Concurrent stats
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.GetStats(StatsQuery{})
			if err != nil {
				errors <- err
			}
		}()
	}
	
	wg.Wait()
	close(errors)
	
	for err := range errors {
		t.Errorf("concurrent operation error: %v", err)
	}
	
	// Verify data integrity
	list, _ := s.List()
	if len(list) < 27 {
		t.Errorf("expected at least 27 transactions, got %d", len(list))
	}
	
	// Verify original transactions still exist
	got1, err := s.Get(tx1.ID)
	if err != nil {
		t.Errorf("tx1 lost during concurrent access: %v", err)
	}
	if got1.Amount != 100 {
		t.Errorf("tx1 amount corrupted: expected 100, got %f", got1.Amount)
	}
	
	got2, err := s.Get(tx2.ID)
	if err != nil {
		t.Errorf("tx2 lost during concurrent access: %v", err)
	}
	if got2.Amount != 5000 {
		t.Errorf("tx2 amount corrupted: expected 5000, got %f", got2.Amount)
	}
}

func TestStore_GetNonExistent(t *testing.T) {
	s := NewStore()
	_, err := s.Get("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent transaction")
	}
}

func TestStore_DeleteNonExistent(t *testing.T) {
	s := NewStore()
	err := s.Delete("nonexistent")
	if err == nil {
		t.Error("expected error when deleting non-existent transaction")
	}
}

func TestStore_NegativeAmount(t *testing.T) {
	s := NewStore()
	_, err := s.Create(CreateTransactionRequest{
		Type:     "expense",
		Amount:   -100,
		Category: "test",
	})
	if err == nil {
		t.Error("expected error for negative amount")
	}
}

func TestStore_MonthFiltering(t *testing.T) {
	s := NewStore()
	
	// Create transactions in different months
	tx1, _ := s.Create(CreateTransactionRequest{Type: "income", Amount: 10000, Category: "工资"})
	tx1.CreatedAt = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	s.transactions[tx1.ID] = tx1
	
	tx2, _ := s.Create(CreateTransactionRequest{Type: "expense", Amount: 100, Category: "餐饮"})
	tx2.CreatedAt = time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	s.transactions[tx2.ID] = tx2
	
	tx3, _ := s.Create(CreateTransactionRequest{Type: "expense", Amount: 200, Category: "交通"})
	tx3.CreatedAt = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	s.transactions[tx3.ID] = tx3
	
	// Query July stats
	stats, err := s.GetStats(StatsQuery{Month: "2026-07"})
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	
	if stats.TotalIncome != 10000 {
		t.Errorf("expected income 10000 for July, got %f", stats.TotalIncome)
	}
	if stats.TotalExpense != 100 {
		t.Errorf("expected expense 100 for July, got %f", stats.TotalExpense)
	}
	if stats.Count != 2 {
		t.Errorf("expected 2 transactions for July, got %d", stats.Count)
	}
	
	// Query August stats
	stats, err = s.GetStats(StatsQuery{Month: "2026-08"})
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	
	if stats.TotalExpense != 200 {
		t.Errorf("expected expense 200 for August, got %f", stats.TotalExpense)
	}
	if stats.Count != 1 {
		t.Errorf("expected 1 transaction for August, got %d", stats.Count)
	}
}

func TestStore_CategoryFiltering(t *testing.T) {
	s := NewStore()
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 100, Category: "餐饮"})
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 50, Category: "餐饮"})
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 200, Category: "交通"})
	
	stats, err := s.GetStats(StatsQuery{Category: "餐饮"})
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	
	if stats.TotalExpense != 150 {
		t.Errorf("expected expense 150 for 餐饮, got %f", stats.TotalExpense)
	}
	if stats.Count != 2 {
		t.Errorf("expected 2 transactions for 餐饮, got %d", stats.Count)
	}
}

func TestStore_IncomeInByCategory(t *testing.T) {
	s := NewStore()
	s.Create(CreateTransactionRequest{Type: "income", Amount: 10000, Category: "工资"})
	s.Create(CreateTransactionRequest{Type: "income", Amount: 5000, Category: "项目收入"})
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 100, Category: "餐饮"})
	
	stats, err := s.GetStats(StatsQuery{})
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	
	// Income categories should now be included
	if stats.ByCategory["工资"] != 10000 {
		t.Errorf("expected 工资 category 10000, got %f", stats.ByCategory["工资"])
	}
	if stats.ByCategory["项目收入"] != 5000 {
		t.Errorf("expected 项目收入 category 5000, got %f", stats.ByCategory["项目收入"])
	}
	if stats.ByCategory["餐饮"] != 100 {
		t.Errorf("expected 餐饮 category 100, got %f", stats.ByCategory["餐饮"])
	}
}

func TestStore_DataIsolation(t *testing.T) {
	s := NewStore()
	tx, _ := s.Create(CreateTransactionRequest{
		Type:     "expense",
		Amount:   100,
		Category: "餐饮",
		Tags:     []string{"lunch", "work"},
	})
	
	// Get transaction and modify it
	got, _ := s.Get(tx.ID)
	got.Amount = 999
	if len(got.Tags) > 0 {
		got.Tags[0] = "modified"
	}
	
	// Verify original is unchanged
	original, _ := s.Get(tx.ID)
	if original.Amount != 100 {
		t.Errorf("original transaction was modified: expected 100, got %f", original.Amount)
	}
	if len(original.Tags) > 0 && original.Tags[0] != "lunch" {
		t.Errorf("original tags were modified: expected lunch, got %s", original.Tags[0])
	}
}

func TestStore_EmptyCategory(t *testing.T) {
	s := NewStore()
	tx, err := s.Create(CreateTransactionRequest{
		Type:   "expense",
		Amount: 100,
		// Category is empty
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if tx.Category != "其他" {
		t.Errorf("expected default category 其他, got %s", tx.Category)
	}
}

func TestStore_DecimalPrecision(t *testing.T) {
	s := NewStore()
	
	// Test decimal precision
	tx, err := s.Create(CreateTransactionRequest{
		Type:     "expense",
		Amount:   38.50,
		Category: "餐饮",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if tx.Amount != 38.50 {
		t.Errorf("decimal precision lost: expected 38.50, got %f", tx.Amount)
	}
	
	// Test statistics with decimals
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 12.75, Category: "餐饮"})
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 5.25, Category: "餐饮"})
	
	stats, _ := s.GetStats(StatsQuery{})
	expected := 38.50 + 12.75 + 5.25
	if stats.TotalExpense != expected {
		t.Errorf("decimal sum incorrect: expected %f, got %f", expected, stats.TotalExpense)
	}
}
