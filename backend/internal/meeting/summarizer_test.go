// internal/meeting/summarizer_test.go
package meeting

import (
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