// internal/chat_summary/summarizer.go
package chat_summary

import (
	"fmt"
	"strings"
)

// Summarizer 聊天摘要生成器
type Summarizer struct{}

// Summarize 从聚合结果生成摘要
func (s *Summarizer) Summarize(result *AggregateResult, channelName string) *ChatSummary {
	if result == nil {
		return &ChatSummary{
			Summary:      "无效的聚合结果",
			MessageCount: 0,
		}
	}

	if result.MessageCount == 0 {
		return &ChatSummary{
			Summary:      "该时间段内没有消息",
			MessageCount: 0,
		}
	}

	// 提取关键信息
	var decisions []string
	var actionItems []ActionItem
	var links []string

	for _, msg := range result.Messages {
		lower := strings.ToLower(msg.Content)
		if strings.Contains(lower, "决定") || strings.Contains(lower, "同意") {
			decisions = append(decisions, msg.Sender+": "+msg.Content)
		}
		if strings.Contains(lower, "负责") || strings.Contains(lower, "我来") {
			actionItems = append(actionItems, ActionItem{
				Task:  msg.Content,
				Owner: msg.Sender,
			})
		}
		if strings.HasPrefix(msg.Content, "http") {
			links = append(links, msg.Content)
		}
	}

	participants := strings.Join(result.Participants, ", ")

	summary := fmt.Sprintf("「%s」共 %d 条消息，参与人：%s。",
		channelName, result.MessageCount, participants)

	if len(decisions) > 0 {
		summary += fmt.Sprintf("识别到 %d 个决策点。", len(decisions))
	}
	if len(actionItems) > 0 {
		summary += fmt.Sprintf("识别到 %d 个待办事项。", len(actionItems))
	}

	return &ChatSummary{
		Summary:      summary,
		KeyDecisions: decisions,
		ActionItems:  actionItems,
		Links:        links,
		MessageCount: result.MessageCount,
	}
}