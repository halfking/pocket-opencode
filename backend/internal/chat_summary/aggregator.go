// internal/chat_summary/aggregator.go
package chat_summary

import (
	"sort"
	"time"
)

// Aggregator 消息聚合器
type Aggregator struct{}

// AggregateResult 聚合结果
type AggregateResult struct {
	Messages     []Message
	MessageCount int
	PeriodStart  time.Time
	PeriodEnd    time.Time
	Participants []string
}

// Aggregate 按时间范围聚合消息
func (a *Aggregator) Aggregate(messages []Message, periodStart, periodEnd time.Time) *AggregateResult {
	var filtered []Message
	participantSet := make(map[string]bool)

	for _, msg := range messages {
		if (msg.Timestamp.IsZero() || !msg.Timestamp.Before(periodStart)) &&
			(msg.Timestamp.IsZero() || !msg.Timestamp.After(periodEnd)) {
			filtered = append(filtered, msg)
			if msg.Sender != "" {
				participantSet[msg.Sender] = true
			}
		}
	}

	// 按时间排序
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp.Before(filtered[j].Timestamp)
	})

	var participants []string
	for p := range participantSet {
		participants = append(participants, p)
	}
	sort.Strings(participants)

	return &AggregateResult{
		Messages:     filtered,
		MessageCount: len(filtered),
		PeriodStart:  periodStart,
		PeriodEnd:    periodEnd,
		Participants: participants,
	}
}