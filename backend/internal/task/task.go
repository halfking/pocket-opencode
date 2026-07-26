package task

import "time"

type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	// WorkspaceID is the tenant boundary. The column has existed since S0-A
	// but the model ignored it, so every read/write spanned all tenants.
	// Server handlers set it from the authenticated claims; empty falls back
	// to DefaultWorkspaceID at the store layer.
	WorkspaceID  string `json:"workspaceId"`
	WorkstreamID string `json:"workstreamId"`
	// Source identifies which task system the row came from. Phase 5 unifies
	// three sources into one view: "acc" (ACC system via MCP), "opencode"
	// (per-instance HTTP), "local" (this Postgres store). Defaults to "local".
	Source           string    `json:"source"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	PendingApprovals int       `json:"pendingApprovals"`
	SessionCount     int       `json:"sessionCount"`
}

// DefaultWorkspaceID matches the `DEFAULT 'default'` on tasks.workspace_id, so
// rows written before S0-A stay reachable for single-tenant deployments.
const DefaultWorkspaceID = "default"

// TaskUpdate holds optional fields for PATCH /api/tasks/{id}.
// Pointer fields are nil when not provided (so we can distinguish "set to empty" from "not set").
type TaskUpdate struct {
	Title        *string `json:"title"`
	Description  *string `json:"description"`
	Status       *string `json:"status"`
	Priority     *string `json:"priority"`
	WorkstreamID *string `json:"workstreamId"`
}
