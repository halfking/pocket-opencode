package task

import "time"

type Task struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	Status           string    `json:"status"`
	Priority         string    `json:"priority"`
	WorkstreamID     string    `json:"workstreamId"`
	// Source identifies which task system the row came from. Phase 5 unifies
	// three sources into one view: "acc" (ACC system via MCP), "opencode"
	// (per-instance HTTP), "local" (this Postgres store). Defaults to "local".
	Source           string    `json:"source"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	PendingApprovals  int       `json:"pendingApprovals"`
	SessionCount     int       `json:"sessionCount"`
}
