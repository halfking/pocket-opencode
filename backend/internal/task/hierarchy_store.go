package task

// Store reads for the parent → child hierarchy and for the task-domain view
// of upstream approvals (docs/学习muse/03-架构方案.md §4, Muse M1 / M4).
//
// The pure decisions — how progress is computed, whether a re-parent would
// loop — live in hierarchy.go and are unit-tested without a database. This
// file only supplies the rows those functions consume.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ListChildren returns the direct children of a work item, oldest first so
// the list reads like a plan rather than shuffling between requests.
func (s *Store) ListChildren(ctx context.Context, taskID, wsID string) ([]Task, error) {
	workspaceID := normalizeWorkspace(wsID)
	id := strings.TrimSpace(taskID)
	if id == "" {
		return nil, fmt.Errorf("list children: task id is required")
	}
	rows, err := s.pool.Query(ctx, `SELECT `+taskColumns+`
		FROM tasks
		WHERE workspace_id = $1 AND parent_id = $2
		ORDER BY created_at ASC, id ASC`, workspaceID, id)
	if err != nil {
		return nil, fmt.Errorf("list children: %w", err)
	}
	defer rows.Close()

	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("list children: scan: %w", err)
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list children: %w", err)
	}
	return out, nil
}

// ChildProgress rolls a parent's direct children up into a progress summary.
// The arithmetic is RollUpProgress's; this only gathers the statuses.
func (s *Store) ChildProgress(ctx context.Context, taskID, wsID string) (GoalProgress, error) {
	id := strings.TrimSpace(taskID)
	if id == "" {
		return GoalProgress{}, fmt.Errorf("child progress: task id is required")
	}
	rows, err := s.pool.Query(ctx,
		`SELECT status FROM tasks WHERE workspace_id = $1 AND parent_id = $2`,
		normalizeWorkspace(wsID), id)
	if err != nil {
		return GoalProgress{}, fmt.Errorf("child progress: %w", err)
	}
	defer rows.Close()

	statuses := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return GoalProgress{}, fmt.Errorf("child progress: scan: %w", err)
		}
		statuses = append(statuses, s)
	}
	if err := rows.Err(); err != nil {
		return GoalProgress{}, fmt.Errorf("child progress: %w", err)
	}
	return RollUpProgress(id, statuses), nil
}

// ParentMap returns id → parent_id for every nested work item in a workspace.
// It is the input WouldCreateCycle walks, and it is deliberately workspace
// scoped: a parent in another tenant is not a valid parent, and mixing tenants
// into the map would make a cross-tenant id look like a legitimate ancestor.
func (s *Store) ParentMap(ctx context.Context, wsID string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, parent_id FROM tasks WHERE workspace_id = $1 AND parent_id <> ''`,
		normalizeWorkspace(wsID))
	if err != nil {
		return nil, fmt.Errorf("parent map: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var id, parent string
		if err := rows.Scan(&id, &parent); err != nil {
			return nil, fmt.Errorf("parent map: scan: %w", err)
		}
		out[id] = parent
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("parent map: %w", err)
	}
	return out, nil
}

// DueTaskReminders returns unfinished work items whose remind_at has passed,
// oldest reminder first. This is the producer side of the `reminded` event:
// until P4, remind_at was stored and validated but nothing ever fired it.
func (s *Store) DueTaskReminders(ctx context.Context, wsID string, now int64, limit int) ([]Task, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT `+taskColumns+`
		FROM tasks
		WHERE workspace_id = $1
		  AND remind_at > 0
		  AND remind_at <= $2
		  AND status NOT IN ('completed', 'accepted')
		ORDER BY remind_at ASC
		LIMIT $3`, normalizeWorkspace(wsID), now, limit)
	if err != nil {
		return nil, fmt.Errorf("due task reminders: %w", err)
	}
	defer rows.Close()

	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("due task reminders: scan: %w", err)
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("due task reminders: %w", err)
	}
	return out, nil
}

// ClearTaskRemindAt retires a one-shot reminder after it has fired, or moves it
// when quiet hours defer it. The two are one statement on purpose: whichever
// value wins, the next scheduler tick must not re-evaluate the old one.
func (s *Store) ClearTaskRemindAt(ctx context.Context, taskID, wsID string, nextRemindAt int64) error {
	id := strings.TrimSpace(taskID)
	if id == "" {
		return fmt.Errorf("clear remind_at: task id is required")
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE tasks SET remind_at = $1, updated_at = $2 WHERE id = $3 AND workspace_id = $4`,
		nextRemindAt, time.Now().Unix(), id, normalizeWorkspace(wsID))
	if err != nil {
		return fmt.Errorf("clear remind_at: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("clear remind_at: task not found: %s", id)
	}
	return nil
}

// TaskApproval is one row of task_approval_projections: an upstream approval
// request, materialized onto a work item. The reply path lives upstream (the
// agent owns the request), so this is a read model — the client renders what
// the task is waiting on, it does not answer here.
type TaskApproval struct {
	InstanceID string        `json:"instanceId"`
	SessionID  string        `json:"sessionId"`
	RequestID  string        `json:"requestId"`
	Kind       ApprovalKind  `json:"kind"`
	State      ApprovalState `json:"state"`
	Decision   string        `json:"decision,omitempty"`
	Version    int64         `json:"version"`
	CreatedAt  int64         `json:"createdAt"`
	UpdatedAt  int64         `json:"updatedAt"`
}

// Pending reports whether the task is still blocked on this approval.
func (a TaskApproval) Pending() bool { return a.State == ApprovalStatePending }

// ListTaskApprovals returns a work item's approval projections, newest first.
// The join to tasks is what keeps a foreign task id from reading as "no
// approvals" instead of "not found" — the workspace check alone is not enough
// because task_id is not globally unique across tenants.
func (s *Store) ListTaskApprovals(ctx context.Context, taskID, wsID string) ([]TaskApproval, error) {
	id := strings.TrimSpace(taskID)
	if id == "" {
		return nil, fmt.Errorf("list task approvals: task id is required")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.instance_id, p.session_id, p.request_id, p.kind, p.state, p.decision, p.version, p.created_at, p.updated_at
		FROM task_approval_projections p
		JOIN tasks t ON t.id = p.task_id AND t.workspace_id = p.workspace_id
		WHERE p.workspace_id = $1 AND p.task_id = $2
		ORDER BY p.updated_at DESC, p.request_id ASC`, normalizeWorkspace(wsID), id)
	if err != nil {
		return nil, fmt.Errorf("list task approvals: %w", err)
	}
	defer rows.Close()

	out := []TaskApproval{}
	for rows.Next() {
		var a TaskApproval
		if err := rows.Scan(&a.InstanceID, &a.SessionID, &a.RequestID, &a.Kind, &a.State,
			&a.Decision, &a.Version, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("list task approvals: scan: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list task approvals: %w", err)
	}
	return out, nil
}
