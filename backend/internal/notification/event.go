package notification

import "time"

type EventType string

const (
	EventTaskCompleted EventType = "task.completed"
	EventTaskBlocked   EventType = "task.blocked"
	EventTaskPending   EventType = "task.pending_approval"
)

type Event struct {
	Type      EventType `json:"type"`
	TaskID    string    `json:"taskId"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}
