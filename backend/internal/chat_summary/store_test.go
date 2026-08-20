// internal/chat_summary/store_test.go
package chat_summary

import (
	"testing"
	"time"
)

func TestStore_CRUD(t *testing.T) {
	s := NewStore()

	cs := &ChatSummary{
		Channel:     "feishu",
		ChannelID:   "group_123",
		Summary:     "Test summary",
		PeriodStart: time.Now().Add(-1 * time.Hour),
		PeriodEnd:   time.Now(),
	}

	err := s.Create(cs)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if cs.ID == "" {
		t.Error("expected non-empty ID")
	}

	got, err := s.Get(cs.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Summary != "Test summary" {
		t.Errorf("summary mismatch")
	}

	results, _ := s.List("group_123", 10)
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}

	s.Delete(cs.ID)
	_, err = s.Get(cs.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestStore_ListByChannel(t *testing.T) {
	s := NewStore()
	s.Create(&ChatSummary{Channel: "feishu", ChannelID: "group_a", Summary: "A1"})
	s.Create(&ChatSummary{Channel: "feishu", ChannelID: "group_a", Summary: "A2"})
	s.Create(&ChatSummary{Channel: "slack", ChannelID: "chan_b", Summary: "B1"})

	results, _ := s.List("group_a", 10)
	if len(results) != 2 {
		t.Errorf("expected 2 results for group_a, got %d", len(results))
	}

	results, _ = s.List("chan_b", 10)
	if len(results) != 1 {
		t.Errorf("expected 1 result for chan_b, got %d", len(results))
	}
}

func TestStore_CreateNil(t *testing.T) {
	s := NewStore()
	err := s.Create(nil)
	if err == nil {
		t.Error("expected error when creating nil summary")
	}
}

func TestStore_GetEmptyID(t *testing.T) {
	s := NewStore()
	_, err := s.Get("")
	if err == nil {
		t.Error("expected error when getting with empty id")
	}
}

func TestStore_DeleteEmptyID(t *testing.T) {
	s := NewStore()
	err := s.Delete("")
	if err == nil {
		t.Error("expected error when deleting with empty id")
	}
}

func TestStore_ListNegativeLimit(t *testing.T) {
	s := NewStore()
	s.Create(&ChatSummary{ChannelID: "test", Summary: "Test"})
	results, err := s.List("", -5)
	if err != nil {
		t.Fatalf("List should handle negative limit: %v", err)
	}
	// Should default to 20
	if len(results) > 20 {
		t.Errorf("expected max 20 results with negative limit, got %d", len(results))
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := NewStore()
	done := make(chan bool)

	// Concurrent writes
	for i := 0; i < 10; i++ {
		go func(n int) {
			s.Create(&ChatSummary{
				ChannelID: "concurrent",
				Summary:   "Test",
			})
			done <- true
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		go func() {
			s.List("concurrent", 10)
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	results, _ := s.List("concurrent", 100)
	if len(results) != 10 {
		t.Errorf("expected 10 summaries after concurrent writes, got %d", len(results))
	}
}