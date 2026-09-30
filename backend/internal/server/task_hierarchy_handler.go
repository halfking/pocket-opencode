package server

// Parent → child work items and the task-domain approval read model
// (docs/学习muse/03-架构方案.md §4, Muse M1 / M4; phase P3 remainder).
//
// Routes:
//
//	GET  /api/tasks/{id}/children    直接子任务 + 派生进度
//	POST /api/tasks/{id}/subtasks    在该工作项下新建子任务
//	GET  /api/tasks/{id}/approvals   该工作项的审批投影（只读）
//
// A "goal" is not a new entity: it is an ordinary work item with children
// pointing at it via `parent_id`, and its progress is derived, never stored.
// The reply path for approvals stays upstream — the agent owns the request —
// so this endpoint only renders what the work item is waiting on.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// handleTaskChildren serves GET /api/tasks/{id}/children.
func (s *Server) handleTaskChildren(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	t, parts, wsID, ok := s.collaborationContext(w, r, taskID)
	if !ok {
		return
	}
	if !task.CanReadWorkItem(t, parts, s.userIDFromRequest(r)) {
		writeError(w, http.StatusForbidden, "not a participant of this work item")
		return
	}
	children, err := s.taskStore.ListChildren(r.Context(), taskID, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Reading the parent is not permission to read its children. A private sub
	// task belongs to its own owner, and the parent can be shared (or belong to
	// somebody else) while the child is not — so each child is filtered through
	// the same CanReadWorkItem rule that guards the parent. Without this, one
	// GET on a shared goal hands over every private child in full.
	ids := make([]string, 0, len(children))
	for _, c := range children {
		ids = append(ids, c.ID)
	}
	childParts, err := s.taskStore.ListParticipantsForTasks(r.Context(), wsID, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	reader := s.userIDFromRequest(r)
	visible := task.FilterReadableChildren(children, func(c task.Task) []task.Participant {
		return childParts[c.ID]
	}, reader)
	// Recompute from the children we just read instead of a second query: the
	// roll-up is a pure function over these statuses, and a second query could
	// disagree with this list if a child changed in between. Hidden children
	// are excluded from the count too — "3 of 5 done" would otherwise leak
	// the existence of children the reader may not open.
	statuses := make([]string, 0, len(visible))
	for _, c := range visible {
		statuses = append(statuses, c.Status)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"children": visible,
		"progress": task.RollUpProgress(taskID, statuses),
	})
}

// handleTaskSubtasks serves POST /api/tasks/{id}/subtasks.
func (s *Server) handleTaskSubtasks(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	parent, parts, wsID, ok := s.collaborationContext(w, r, taskID)
	if !ok {
		return
	}
	if !task.CanWriteWorkItem(parent, parts, s.userIDFromRequest(r)) {
		writeError(w, http.StatusForbidden, "only the owner or a participant may add sub-tasks")
		return
	}

	var body struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Type        string   `json:"type"`
		DueAt       int64    `json:"dueAt"`
		Assignees   []string `json:"assignees"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	// A child inherits the parent's classification unless the caller overrides
	// it, so "Ship P3" (dev) does not sprout "other" children by default.
	typ := strings.TrimSpace(body.Type)
	if typ == "" {
		typ = parent.Type
	}
	if typ == "" {
		typ = task.TypeOther
	}
	if !task.ValidType(typ) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid task type %q", body.Type))
		return
	}

	ownerID := s.userIDFromRequest(r)
	child := &task.Task{
		ID:          "task-" + generateUUID(),
		WorkspaceID: wsID,
		Title:       title,
		Description: body.Description,
		// New work items cannot start completed; the parent's own rule.
		Status:     "active",
		Priority:   parent.Priority,
		Source:     "local",
		Type:       typ,
		TypeGroup:  task.TypeGroup(typ),
		OwnerID:    ownerID,
		Assignees:  body.Assignees,
		DueAt:      body.DueAt,
		ParentID:   taskID,
		Visibility: parent.Visibility,
	}
	if err := s.taskStore.CreateTask(r.Context(), child); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The creator is the child's owner, exactly as for a top-level task.
	if err := s.taskStore.SetParticipants(r.Context(), child.ID, wsID, []task.Participant{
		{UserID: ownerID, Role: task.RoleOwner},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Named assignees become participants, or they could not open the child and
	// would never hear about it.
	if err := s.taskStore.SyncAssigneeParticipants(r.Context(), child.ID, wsID, ownerID, body.Assignees); err != nil {
		s.Write(r, "task.subtask.participants_failed", "task:"+taskID, AuditFields{Success: false, Detail: err.Error()})
	}
	// The parent's activity stream records the new child, so the goal's
	// history explains where its progress came from. This event maps to no
	// notification kind, so appendWorkItemEvent's sibling dispatch is skipped
	// on purpose — see NotificationKind.
	if _, err := s.appendWorkItemEvent(r.Context(), taskID, wsID, task.EventChildAdded, ownerID,
		task.EventPayload{ChildID: child.ID, ChildTitle: title, TaskTitle: parent.Title}, ""); err != nil {
		// The child exists; losing the audit line is not worth failing the call.
		s.Write(r, "task.subtask.event_failed", "task:"+taskID, AuditFields{Success: false, Detail: err.Error()})
	}
	writeJSON(w, http.StatusCreated, child)
}

// handleTaskApprovals serves GET /api/tasks/{id}/approvals.
func (s *Server) handleTaskApprovals(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	t, parts, wsID, ok := s.collaborationContext(w, r, taskID)
	if !ok {
		return
	}
	if !task.CanReadWorkItem(t, parts, s.userIDFromRequest(r)) {
		writeError(w, http.StatusForbidden, "not a participant of this work item")
		return
	}
	approvals, err := s.taskStore.ListTaskApprovals(r.Context(), taskID, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pending := 0
	for _, a := range approvals {
		if a.Pending() {
			pending++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"approvals": approvals,
		"pending":   pending,
	})
}

// validateReparent rejects a parent_id change that would produce a loop or
// point outside the workspace. It returns a ready-to-write 400 body on
// failure, and ok=false when the caller should stop.
func (s *Server) validateReparent(r *http.Request, taskID, newParentID string) (msg string, ok bool) {
	child := strings.TrimSpace(taskID)
	parent := strings.TrimSpace(newParentID)
	if parent == "" || parent == child {
		// Empty clears the parent; self-parenting is caught by
		// WouldCreateCycle below, but short-circuiting here keeps the message
		// specific instead of the generic "cycle" wording.
		if parent == child {
			return "a work item cannot be its own parent", false
		}
		return "", true
	}
	wsID := s.workspaceIDFromRequest(r)
	// The new parent must exist in this workspace. ParentMap only contains
	// tasks that have a parent, so check existence separately.
	if _, err := s.taskStore.GetTaskScoped(r.Context(), parent, wsID); err != nil {
		return "parent work item not found in this workspace", false
	}
	parents, err := s.taskStore.ParentMap(r.Context(), wsID)
	if err != nil {
		// An unknown answer must not become a permission: fail the write.
		return "could not verify the parent chain", false
	}
	if task.WouldCreateCycle(child, parent, parents) {
		return "that parent would create a cycle in the work item tree", false
	}
	return "", true
}
