package notification

import "time"

type EventType string

const (
	EventTaskCompleted EventType = "task.completed"
	EventTaskBlocked   EventType = "task.blocked"
	EventTaskPending   EventType = "task.pending_approval"
	// 飞书事件回调（m.kxpms.cn/callback/feishu）
	EventFeishuMessage EventType = "feishu.message" // 飞书消息事件
	EventFeishuDoc     EventType = "feishu.doc"     // 飞书云文档/多维表事件
)

type Event struct {
	Type      EventType `json:"type"`
	TaskID    string    `json:"taskId"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
	// 飞书事件等扩展场景用 Payload 传原始 JSON
	Payload string `json:"payload,omitempty"`
}
