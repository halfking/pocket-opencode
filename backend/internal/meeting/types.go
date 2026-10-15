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
	// ID 是**客户端提供的**会议 ID（可选）。
	//
	// 为什么要它：前端在 `local_meetings` 里已经有一个稳定 ID（`meeting-<ts>-<rand>`），
	// 并且用它去调 `/api/meetings/:id/refine`。而服务端此前**永远**自己发号
	// （`mtg_<nano>_<seq>`）并忽略请求里的 `id` ⇒ 客户端手里的 ID 在库里不存在，
	// 精校链在生产必然 404（`server_meeting_ingest.go` 的 fail-closed 守卫是对的，
	// 它只是让这个缺口以「诚实的方式」暴露出来）。
	//
	// 空值 ⇒ 保持旧行为（服务端发号）。
	//
	// ⚠ 它**不是**跨用户可写的句柄：撞到别的 owner/workspace 已占用的 ID 时，
	//   服务端必须退回自己发号，绝不覆盖（见 `PGStore.CreateScoped` 的唯一冲突重试）。
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
