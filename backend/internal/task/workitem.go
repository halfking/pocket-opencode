package task

// Collaboration surface for work items (docs/学习muse/03-架构方案.md §4).
//
// The tenancy rule is the same as the rest of this package: workspace_id is
// part of every key and every statement, so a cross-tenant task id reads as
// "not found" rather than leaking another tenant's participants.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Participant roles on a work item.
const (
	RoleOwner    = "owner"
	RoleAssignee = "assignee"
	RoleWatcher  = "watcher"
)

// ValidParticipantRole reports whether r is an accepted role.
func ValidParticipantRole(r string) bool {
	switch r {
	case RoleOwner, RoleAssignee, RoleWatcher:
		return true
	default:
		return false
	}
}

// Participant is one person on a work item.
type Participant struct {
	UserID    string `json:"userId"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"createdAt"`
}

// Work-item event types. These are the input to the notification mapping in
// docs/学习muse/03-架构方案.md §4.2 — adding a type there means adding a
// notification kind here.
const (
	EventCreated       = "created"
	EventAssigned      = "assigned"
	EventStatusChanged = "status_changed"
	EventDueChanged    = "due_changed"
	EventComment       = "comment"
	EventCompleted     = "completed"
	EventReminded      = "reminded"
	// EventChildAdded records a sub-task appearing under this work item. It is
	// a real event type rather than a synthetic comment, so the activity feed
	// can label it correctly and a future "who added this" audit does not have
	// to string-match a message.
	EventChildAdded = "child_added"
)

// ValidEventType reports whether t is an accepted event type.
func ValidEventType(t string) bool {
	switch t {
	case EventCreated, EventAssigned, EventStatusChanged, EventDueChanged,
		EventComment, EventCompleted, EventReminded, EventChildAdded:
		return true
	default:
		return false
	}
}

// WorkItemEvent is one entry in a work item's activity stream. EventID is the
// idempotency key: the same logical event (e.g. "reminder:1790000000") must
// carry the same id so a repeated scheduler tick cannot duplicate a row.
type WorkItemEvent struct {
	WorkspaceID string          `json:"workspaceId"`
	TaskID      string          `json:"taskId"`
	EventID     string          `json:"eventId"`
	EventType   string          `json:"eventType"`
	ActorUserID string          `json:"actorUserId,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	CreatedAt   int64           `json:"createdAt"`
}

// SetParticipants replaces the participant set of a work item inside one
// transaction. Participants that disappear from the list are deleted, so the
// caller owns the whole set (PUT semantics) and a stale assignee cannot keep
// receiving notifications.
func (s *Store) SetParticipants(ctx context.Context, taskID, wsID string, parts []Participant) error {
	workspaceID := normalizeWorkspace(wsID)
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("set participants: task id is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("set participants: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT true FROM tasks WHERE id = $1 AND workspace_id = $2`, taskID, workspaceID).Scan(&exists); err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("set participants: task not found: %s", taskID)
		}
		return fmt.Errorf("set participants: lookup task: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM work_item_participants WHERE workspace_id = $1 AND task_id = $2`,
		workspaceID, taskID); err != nil {
		return fmt.Errorf("set participants: clear: %w", err)
	}
	now := time.Now().Unix()
	for _, p := range parts {
		userID := strings.TrimSpace(p.UserID)
		if userID == "" {
			continue
		}
		role := p.Role
		if !ValidParticipantRole(role) {
			role = RoleAssignee
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO work_item_participants (workspace_id, task_id, user_id, role, created_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (workspace_id, task_id, user_id) DO UPDATE SET role = EXCLUDED.role
		`, workspaceID, taskID, userID, role, now); err != nil {
			return fmt.Errorf("set participants: insert %s: %w", userID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("set participants: commit: %w", err)
	}
	return nil
}

// ListParticipants returns the participants of a work item, owner first and
// then alphabetically so the UI order is stable across requests.
func (s *Store) ListParticipants(ctx context.Context, taskID, wsID string) ([]Participant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.user_id, p.role, p.created_at
		FROM work_item_participants p
		JOIN tasks t ON t.id = p.task_id AND t.workspace_id = p.workspace_id
		WHERE p.workspace_id = $1 AND p.task_id = $2
	`, normalizeWorkspace(wsID), taskID)
	if err != nil {
		return nil, fmt.Errorf("list participants: %w", err)
	}
	defer rows.Close()

	out := []Participant{}
	for rows.Next() {
		var p Participant
		if err := rows.Scan(&p.UserID, &p.Role, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("list participants: scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list participants: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Role != out[j].Role {
			return out[i].Role == RoleOwner
		}
		return out[i].UserID < out[j].UserID
	})
	return out, nil
}

// ListParticipantsForTasks returns the participants of many work items in one
// query, keyed by task id. Tasks with no participants are simply absent from
// the map, which is the same thing an empty list means.
//
// It exists so a caller can apply CanReadWorkItem to a list of work items
// without an N+1: the rule needs each item's own participant set, and a goal
// can have dozens of children.
func (s *Store) ListParticipantsForTasks(ctx context.Context, wsID string, taskIDs []string) (map[string][]Participant, error) {
	out := map[string][]Participant{}
	ids := make([]string, 0, len(taskIDs))
	seen := make(map[string]struct{}, len(taskIDs))
	for _, id := range taskIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.task_id, p.user_id, p.role
		FROM work_item_participants p
		JOIN tasks t ON t.id = p.task_id AND t.workspace_id = p.workspace_id
		WHERE p.workspace_id = $1 AND p.task_id = ANY($2)
	`, normalizeWorkspace(wsID), ids)
	if err != nil {
		return nil, fmt.Errorf("list participants for tasks: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var taskID, userID, role string
		if err := rows.Scan(&taskID, &userID, &role); err != nil {
			return nil, fmt.Errorf("list participants for tasks: scan: %w", err)
		}
		out[taskID] = append(out[taskID], Participant{UserID: userID, Role: role})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list participants for tasks: %w", err)
	}
	return out, nil
}

// SyncAssigneeParticipants keeps `tasks.assignees` and the participant rows in
// agreement inside one transaction.
//
// The two lists used to be independent. An assignee who was not also a
// participant could not open a private work item (CanReadWorkItem asks both)
// and received no notification at all, so being assigned something could look
// like the assignment never happened.
//
// The rules, chosen so that neither list can silently destroy the other:
//
//   - Every assignee is added with role `assignee`. ON CONFLICT DO NOTHING, so
//     a person who is already the owner or a watcher keeps that role.
//   - The owner is always present.
//   - A participant whose role is `assignee` and who is no longer assigned is
//     removed. Watchers are never removed: they were added on purpose, and
//     nothing on the task row records that intent, so guessing would drop real
//     subscribers.
//
// It is deliberately not SetParticipants: that is PUT semantics for the
// explicit participants endpoint, and a read-modify-write of the whole set
// would lose a concurrent delegation.
func (s *Store) SyncAssigneeParticipants(ctx context.Context, taskID, wsID, ownerID string, assignees []string) error {
	id := strings.TrimSpace(taskID)
	if id == "" {
		return fmt.Errorf("sync assignees: task id is required")
	}
	workspaceID := normalizeWorkspace(wsID)
	owner := strings.TrimSpace(ownerID)

	names := make([]string, 0, len(assignees)+1)
	seen := make(map[string]struct{}, len(assignees)+1)
	for _, a := range assignees {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		names = append(names, a)
	}
	if owner != "" {
		if _, dup := seen[owner]; !dup {
			seen[owner] = struct{}{}
			names = append(names, owner)
		}
	}
	// A work item with no participants at all is a legitimate state; the two
	// statements below are then no-ops rather than errors.
	if len(names) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sync assignees: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT true FROM tasks WHERE id = $1 AND workspace_id = $2`, id, workspaceID).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("sync assignees: task not found: %s", id)
		}
		return fmt.Errorf("sync assignees: lookup task: %w", err)
	}

	now := time.Now().Unix()
	// The owner keeps the owner role; everyone else arrives as an assignee.
	if _, err := tx.Exec(ctx, `
		INSERT INTO work_item_participants (workspace_id, task_id, user_id, role, created_at)
		SELECT $1, $2, u, CASE WHEN u = $4 THEN 'owner' ELSE 'assignee' END, $5
		FROM unnest($3::text[]) AS u
		ON CONFLICT (workspace_id, task_id, user_id) DO NOTHING
	`, workspaceID, id, names, owner, now); err != nil {
		return fmt.Errorf("sync assignees: add: %w", err)
	}
	// Drop the ones who are no longer assigned. `role = 'assignee'` is what
	// keeps watchers and the owner out of this statement.
	if _, err := tx.Exec(ctx, `
		DELETE FROM work_item_participants
		WHERE workspace_id = $1 AND task_id = $2 AND role = 'assignee'
		  AND NOT (user_id = ANY($3::text[]))
	`, workspaceID, id, names); err != nil {
		return fmt.Errorf("sync assignees: remove: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sync assignees: commit: %w", err)
	}
	return nil
}

// AppendEvent writes one activity-stream entry. It is idempotent on
// (workspace_id, task_id, event_id): a replayed or double-fired event does not
// create a second row, and a stale timestamp cannot reorder history.
func (s *Store) AppendEvent(ctx context.Context, ev WorkItemEvent) error {
	ev.WorkspaceID = normalizeWorkspace(ev.WorkspaceID)
	if strings.TrimSpace(ev.TaskID) == "" || strings.TrimSpace(ev.EventID) == "" {
		return fmt.Errorf("append event: task id and event id are required")
	}
	if !ValidEventType(ev.EventType) {
		return fmt.Errorf("append event: unsupported event type %q", ev.EventType)
	}
	createdAt := ev.CreatedAt
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}
	payload := ev.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return fmt.Errorf("append event: payload is not valid JSON")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO work_item_events (workspace_id, task_id, event_id, event_type, actor_user_id, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (workspace_id, task_id, event_id) DO NOTHING
	`, ev.WorkspaceID, ev.TaskID, ev.EventID, ev.EventType, ev.ActorUserID, payload, createdAt)
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

// ListEvents returns the newest-first activity stream of a work item.
func (s *Store) ListEvents(ctx context.Context, taskID, wsID string, limit int) ([]WorkItemEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.workspace_id, e.task_id, e.event_id, e.event_type, e.actor_user_id, e.payload, e.created_at
		FROM work_item_events e
		JOIN tasks t ON t.id = e.task_id AND t.workspace_id = e.workspace_id
		WHERE e.workspace_id = $1 AND e.task_id = $2
		ORDER BY e.created_at DESC, e.event_id DESC
		LIMIT $3
	`, normalizeWorkspace(wsID), taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	out := []WorkItemEvent{}
	for rows.Next() {
		var e WorkItemEvent
		var payload []byte
		if err := rows.Scan(&e.WorkspaceID, &e.TaskID, &e.EventID, &e.EventType, &e.ActorUserID, &payload, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("list events: scan: %w", err)
		}
		if len(payload) > 0 {
			e.Payload = payload
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return out, nil
}

// CountDueWorkItems counts unfinished work items whose due date falls on the
// local day that ends at nowSec (i.e. due today or already overdue). The
// learning digest uses it so "today's agenda" and "reviews due" come from one
// summary call instead of a client-side join.
func (s *Store) CountDueWorkItems(ctx context.Context, wsID, userID string, nowSec int64) (int, error) {
	const q = `
		SELECT count(*)
		FROM tasks
		WHERE workspace_id = $1
		  AND status <> 'completed'
		  AND due_at > 0
		  AND due_at <= $2
		  AND (
			$3 = '' OR owner_id = $3
			OR assignees @> to_jsonb(ARRAY[$3]::text[])
		  )`
	var n int
	if err := s.pool.QueryRow(ctx, q, normalizeWorkspace(wsID), nowSec, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count due work items: %w", err)
	}
	return n, nil
}
