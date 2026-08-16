package task

import (
	"context"
	"time"
)

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

// ApprovalKind identifies the upstream workflow that created an approval gate.
type ApprovalKind string

const (
	ApprovalKindPermission ApprovalKind = "permission"
	ApprovalKindQuestion   ApprovalKind = "question"
)

// ApprovalState is the task-local materialized state of an upstream request.
type ApprovalState string

const (
	ApprovalStatePending  ApprovalState = "pending"
	ApprovalStateApproved ApprovalState = "approved"
	ApprovalStateRejected ApprovalState = "rejected"
	ApprovalStateAnswered ApprovalState = "answered"
	ApprovalStateExpired  ApprovalState = "expired"
	ApprovalStateFailed   ApprovalState = "failed"
	ApprovalStateResolved ApprovalState = "resolved"
)

// ApprovalProjectionEvent is an idempotent, versioned update from an approval
// lifecycle observer. WorkspaceID is always derived by the caller from trusted
// instance ownership, never a client supplied value.
type ApprovalProjectionEvent struct {
	WorkspaceID string
	InstanceID  string
	SessionID   string
	RequestID   string
	Kind        ApprovalKind
	State       ApprovalState
	Version     int64
	Decision    string
}

// ApprovalProjectionWriter lets event observers update the task-domain view
// without depending on the Store's database implementation.
type ApprovalProjectionWriter interface {
	ApplyApprovalProjection(context.Context, ApprovalProjectionEvent) error
}
