// internal/meeting/types.go
package meeting

import "time"

// Meeting 会议记录
type Meeting struct {
	ID           string       `json:"id"`
	OwnerID      string       `json:"owner_id,omitempty"`
	WorkspaceID  string       `json:"workspace_id,omitempty"`
	Title        string       `json:"title"`
	Duration     int          `json:"duration"` // 秒
	RecordingURL string       `json:"recording_url,omitempty"`
	Transcript   string       `json:"transcript,omitempty"`
	Summary      string       `json:"summary,omitempty"`
	KeyDecisions []string     `json:"key_decisions,omitempty"`
	ActionItems  []ActionItem `json:"action_items,omitempty"`
	Tags         []string     `json:"tags,omitempty"`
	ProjectID    string       `json:"project_id,omitempty"`
	Status       string       `json:"status"` // recording / transcribing / summarizing / done / failed
	// ★ 2026-10-07 补齐：前端 syncMeetingMetadata 一直在发这三个字段，
	// 而旧结构体既没有它们、请求体也没有对应项 ⇒ json.Decode 静默丢弃，
	// 录音结束后同步上来的地点/参与者/笔记关联**从来没进过库**。
	Location     string    `json:"location,omitempty"`
	Participants []string  `json:"participants,omitempty"`
	NoteID       string    `json:"note_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ActionItem 待办事项
type ActionItem struct {
	Owner    string `json:"owner,omitempty"`
	Task     string `json:"task"`
	Deadline string `json:"deadline,omitempty"`
}

// CreateMeetingRequest 创建会议请求。
//
// ★ 2026-10-07 扩容：原来这里**只有 Title 一个字段**，而客户端
// （frontend/src/api/meetings.ts 的 MeetingSyncPayload）发的是 10 个字段。
// Go 的 json.Decode 默认静默忽略未知字段，于是 9/10 的同步数据被丢掉
// （客户端的 id 也被丢掉 ⇒ 每次重试都新建一行 ⇒ 重复会议）。
//
// 字段与 JSON 名一一对应，单位按客户端实际发送的来：
//   - StartedAt 是 **Unix 毫秒**（前端 const now = Date.now()，meetings-store.ts:82）
//   - DurationMs 是 **毫秒**，落到存储的 Duration 是**秒**
//
// 两者由 handler 做换算，这里保持"客户端口径"以免换算散落多处。
type CreateMeetingRequest struct {
	ID                string   `json:"id,omitempty"`
	Title             string   `json:"title"`
	Location          string   `json:"location,omitempty"`
	Participants      []string `json:"participants,omitempty"`
	StartedAt         int64    `json:"startedAt,omitempty"` // Unix 毫秒
	DurationMs        int64    `json:"durationMs,omitempty"`
	Summary           string   `json:"summary,omitempty"`
	RefinedTranscript string   `json:"refinedTranscript,omitempty"`
	NoteID            string   `json:"noteId,omitempty"`
	Status            string   `json:"status,omitempty"`
}
