package task

import "time"

type Task struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	Status           string    `json:"status"`
	Priority         string    `json:"priority"`
	WorkstreamID     string    `json:"workstreamId"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	PendingApprovals  int       `json:"pendingApprovals"`
	SessionCount     int       `json:"sessionCount"`
}
