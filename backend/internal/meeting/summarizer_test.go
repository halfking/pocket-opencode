// internal/meeting/summarizer_test.go
package meeting

import (
	"strings"
	"testing"
)

func TestSummarize(t *testing.T) {
	transcript := `张三: 我们讨论一下Q3的AI功能优先级
李四: 我建议优先做会议总结功能，用户需求很大
张三: 同意，那API升级推迟到下一轮Sprint
王五: 好的，我来负责会议总结模块的设计
张三: 截止日期定在7月28日
李四: 收到`

	summary, err := SummarizeTranscript(transcript, "Sprint Planning")
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	if summary.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(summary.KeyDecisions) == 0 {
		t.Error("expected at least one key decision")
	}
	if len(summary.ActionItems) == 0 {
		t.Error("expected at least one action item")
	}
}

func TestSummarize_EmptyTranscript(t *testing.T) {
	_, err := SummarizeTranscript("", "Empty Meeting")
	if err == nil {
		t.Error("expected error for empty transcript")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' in error message, got: %v", err)
	}
}

func TestSummarize_WhitespaceOnlyTranscript(t *testing.T) {
	_, err := SummarizeTranscript("   \n\t  ", "Whitespace Meeting")
	if err == nil {
		t.Error("expected error for whitespace-only transcript")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' in error message, got: %v", err)
	}
}

func TestSummarize_NoKeywords(t *testing.T) {
	transcript := `张三: 今天天气不错
李四: 是啊，适合出去走走`

	summary, err := SummarizeTranscript(transcript, "闲聊")
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if summary.Summary == "" {
		t.Error("expected non-empty summary")
	}
	// 没有关键词时，应该返回占位信息
	if len(summary.KeyDecisions) == 0 {
		t.Error("expected fallback decision message")
	}
	if len(summary.ActionItems) == 0 {
		t.Error("expected fallback action item message")
	}
}

func TestSummarize_EmptyTitle(t *testing.T) {
	transcript := `张三: 同意推进这个方案
李四: 我来负责实施`

	summary, err := SummarizeTranscript(transcript, "")
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	// Empty title should be handled gracefully
	if summary.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if !strings.Contains(summary.Summary, "Untitled Meeting") {
		t.Errorf("expected 'Untitled Meeting' in summary for empty title, got: %s", summary.Summary)
	}
}

func TestSummarize_ComplexScenario(t *testing.T) {
	transcript := `张三: 我们今天要决定Q3的重点项目
李四: 建议优先做用户反馈最多的三个功能
张三: 同意，就这么办
王五: 我来负责功能1的开发
赵六: 我负责功能2
张三: 批准这个方案，截止日期是8月15日
李四: 收到，我负责功能3的设计`

	summary, err := SummarizeTranscript(transcript, "Q3 Planning")
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	// Should identify multiple decisions
	if len(summary.KeyDecisions) < 2 {
		t.Errorf("expected at least 2 key decisions, got %d", len(summary.KeyDecisions))
	}

	// Should identify multiple action items
	if len(summary.ActionItems) < 3 {
		t.Errorf("expected at least 3 action items, got %d", len(summary.ActionItems))
	}
}

func TestSummarize_SingleLineTranscript(t *testing.T) {
	transcript := `张三: 同意这个提案`

	summary, err := SummarizeTranscript(transcript, "Quick Decision")
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	if summary.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(summary.KeyDecisions) == 0 {
		t.Error("expected at least one key decision")
	}
}