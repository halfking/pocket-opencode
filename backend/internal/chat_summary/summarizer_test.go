// internal/chat_summary/summarizer_test.go
package chat_summary

import (
	"testing"
	"time"
)

func TestSummarizer_Summarize(t *testing.T) {
	s := &Summarizer{}

	result := &AggregateResult{
		Messages: []Message{
			{Sender: "张三", Content: "我决定用Go重写后端", Timestamp: time.Now()},
			{Sender: "李四", Content: "同意，我来负责数据库层", Timestamp: time.Now()},
			{Sender: "张三", Content: "https://github.com/example", Timestamp: time.Now()},
		},
		MessageCount: 3,
		Participants: []string{"张三", "李四"},
		PeriodStart:  time.Now().Add(-1 * time.Hour),
		PeriodEnd:    time.Now(),
	}

	summary := s.Summarize(result, "后端讨论组")

	if summary.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(summary.KeyDecisions) == 0 {
		t.Error("expected at least one decision")
	}
	if len(summary.ActionItems) == 0 {
		t.Error("expected at least one action item")
	}
	if len(summary.Links) == 0 {
		t.Error("expected at least one link")
	}
}

func TestSummarizer_Empty(t *testing.T) {
	s := &Summarizer{}
	result := &AggregateResult{MessageCount: 0}
	summary := s.Summarize(result, "空群组")
	if summary.Summary != "该时间段内没有消息" {
		t.Errorf("expected '该时间段内没有消息', got %s", summary.Summary)
	}
}

func TestSummarizer_NilResult(t *testing.T) {
	s := &Summarizer{}
	summary := s.Summarize(nil, "测试")
	if summary == nil {
		t.Fatal("expected non-nil summary for nil result")
	}
	if summary.MessageCount != 0 {
		t.Errorf("expected 0 message count for nil result, got %d", summary.MessageCount)
	}
}

func TestSummarizer_EmptyMessages(t *testing.T) {
	s := &Summarizer{}
	result := &AggregateResult{
		Messages:     []Message{},
		MessageCount: 0,
		Participants: []string{},
	}
	summary := s.Summarize(result, "测试")
	if summary.Summary != "该时间段内没有消息" {
		t.Errorf("expected empty message summary, got %s", summary.Summary)
	}
}