// internal/meeting/summarizer.go
package meeting

import (
	"fmt"
	"strings"
)

// MeetingSummary AI 总结结果
type MeetingSummary struct {
	Summary      string       `json:"summary"`
	KeyDecisions []string     `json:"key_decisions"`
	ActionItems  []ActionItem `json:"action_items"`
}

// SummarizeTranscript summarizes a meeting transcript using rule-based extraction.
// It extracts key decisions and action items from the transcript text.
// Returns error if transcript is empty or whitespace-only.
// Current implementation uses keyword matching; can be replaced with LLM in the future.
func SummarizeTranscript(transcript string, title string) (*MeetingSummary, error) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return nil, fmt.Errorf("transcript is empty")
	}
	if strings.TrimSpace(title) == "" {
		title = "Untitled Meeting"
	}

	lines := strings.Split(transcript, "\n")

	// 提取决策（包含"决定"、"同意"等关键词的行）
	var decisions []string
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "决定") || strings.Contains(lower, "同意") ||
			strings.Contains(lower, "就这么办") || strings.Contains(lower, "批准") {
			decisions = append(decisions, line)
		}
	}

	// 提取待办（包含"负责"、"我来"、"截止"等关键词的行）
	var items []ActionItem
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "负责") || strings.Contains(lower, "我来") {
			items = append(items, ActionItem{Task: line})
		}
		if strings.Contains(lower, "截止") {
			if len(items) > 0 {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					items[len(items)-1].Deadline = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	if len(decisions) == 0 {
		decisions = []string{"（未识别到明确的决策点）"}
	}
	if len(items) == 0 {
		items = []ActionItem{{Task: "（未识别到明确的待办事项）"}}
	}

	summary := fmt.Sprintf("会议《%s》共 %d 条消息，识别到 %d 个决策点和 %d 个待办事项。",
		title, len(lines), len(decisions), len(items))

	return &MeetingSummary{
		Summary:      summary,
		KeyDecisions: decisions,
		ActionItems:  items,
	}, nil
}