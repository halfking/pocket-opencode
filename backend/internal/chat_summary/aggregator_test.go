// internal/chat_summary/aggregator_test.go
package chat_summary

import (
	"testing"
	"time"
)

func TestAggregator_Aggregate(t *testing.T) {
	now := time.Now()
	a := &Aggregator{}

	messages := []Message{
		{Sender: "张三", Content: "今天讨论一下API设计", Timestamp: now.Add(-30 * time.Minute)},
		{Sender: "李四", Content: "好的，我建议用REST", Timestamp: now.Add(-20 * time.Minute)},
		{Sender: "张三", Content: "同意，就这么办", Timestamp: now.Add(-10 * time.Minute)},
	}

	result := a.Aggregate(messages, now.Add(-1*time.Hour), now)

	if result.MessageCount != 3 {
		t.Errorf("expected 3 messages, got %d", result.MessageCount)
	}
	if len(result.Participants) != 2 {
		t.Errorf("expected 2 participants, got %d", len(result.Participants))
	}
}

func TestAggregator_EmptyResult(t *testing.T) {
	now := time.Now()
	a := &Aggregator{}

	result := a.Aggregate(nil, now, now)
	if result.MessageCount != 0 {
		t.Errorf("expected 0 messages, got %d", result.MessageCount)
	}
}

func TestAggregator_TimeFilter(t *testing.T) {
	now := time.Now()
	a := &Aggregator{}

	messages := []Message{
		{Sender: "A", Content: "old", Timestamp: now.Add(-3 * time.Hour)},
		{Sender: "B", Content: "recent", Timestamp: now.Add(-30 * time.Minute)},
	}

	result := a.Aggregate(messages, now.Add(-1*time.Hour), now)
	if result.MessageCount != 1 {
		t.Errorf("expected 1 message in time range, got %d", result.MessageCount)
	}
	if result.Messages[0].Content != "recent" {
		t.Errorf("expected 'recent', got %s", result.Messages[0].Content)
	}
}