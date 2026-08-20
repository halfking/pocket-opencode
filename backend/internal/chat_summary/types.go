// internal/chat_summary/types.go
package chat_summary

import "time"

// ChatSummary 聊天摘要
type ChatSummary struct {
	ID           string       `json:"id"`
	OwnerID      string       `json:"owner_id,omitempty"`
	WorkspaceID  string       `json:"workspace_id,omitempty"`
	Channel      string       `json:"channel"`       // feishu / telegram / slack
	ChannelID    string       `json:"channel_id"`     // 群组/频道 ID
	ChannelName  string       `json:"channel_name,omitempty"`
	PeriodStart  time.Time    `json:"period_start"`
	PeriodEnd    time.Time    `json:"period_end"`
	MessageCount int          `json:"message_count"`
	Summary      string       `json:"summary"`
	KeyDecisions []string     `json:"key_decisions,omitempty"`
	ActionItems  []ActionItem `json:"action_items,omitempty"`
	Links        []string     `json:"links,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
}

// ActionItem 待办事项
type ActionItem struct {
	Task  string `json:"task"`
	Owner string `json:"owner,omitempty"`
}

// CreateSummaryRequest 创建摘要请求
type CreateSummaryRequest struct {
	Channel     string    `json:"channel"`
	ChannelID   string    `json:"channel_id"`
	Messages    []Message `json:"messages"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
}

// Message 聊天消息
type Message struct {
	Sender    string    `json:"sender"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	MsgType   string    `json:"msg_type,omitempty"` // text / image / file / link
}