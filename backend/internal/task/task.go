package task

import (
	"context"
	"errors"
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
	// Acceptance evidence fields. All three are nil until a task moves through
	// the dedicated AcceptTaskScoped path; PATCH never sets them.
	// See docs/superpowers/specs/2026-08-17-task-acceptance-evidence-design.md.
	AcceptedAt     *int64          `json:"acceptedAt,omitempty"`
	AcceptedBy     *string         `json:"acceptedBy,omitempty"`
	EvidenceBundle *EvidenceBundle `json:"evidenceBundle,omitempty"`
}

// EvidenceBundle is the structured payload accepted via POST /api/tasks/{id}/accept.
// References are opaque pointers — the server does not fetch them — and may carry
// an optional client-computed SHA256 digest.
type EvidenceBundle struct {
	Note       string              `json:"note,omitempty"`
	References []EvidenceReference `json:"references"`
}

// EvidenceReference describes one piece of evidence pointing at a target.
// Kind is one of url | task | session | note | audit_event.
type EvidenceReference struct {
	Kind   string `json:"kind"`
	URI    string `json:"uri,omitempty"`
	Label  string `json:"label,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// ErrTaskNotCompletable is returned by AcceptTaskScoped when the target task is
// not in the `completed` state. The HTTP handler maps this to 409.
var ErrTaskNotCompletable = errors.New("task not in completed state")

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
