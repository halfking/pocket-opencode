// internal/meeting/types.go
package meeting

import "time"

// Meeting 会议记录
type Meeting struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Duration     int          `json:"duration"`      // 秒
	RecordingURL string       `json:"recording_url,omitempty"`
	Transcript   string       `json:"transcript,omitempty"`
	Summary      string       `json:"summary,omitempty"`
	KeyDecisions []string     `json:"key_decisions,omitempty"`
	ActionItems  []ActionItem `json:"action_items,omitempty"`
	Tags         []string     `json:"tags,omitempty"`
	ProjectID    string       `json:"project_id,omitempty"`
	Status       string       `json:"status"` // recording / transcribing / summarizing / done / failed
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// ActionItem 待办事项
type ActionItem struct {
	Owner    string `json:"owner,omitempty"`
	Task     string `json:"task"`
	Deadline string `json:"deadline,omitempty"`
}

// CreateMeetingRequest 创建会议请求
type CreateMeetingRequest struct {
	Title string `json:"title"`
}