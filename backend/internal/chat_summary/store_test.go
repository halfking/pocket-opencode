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