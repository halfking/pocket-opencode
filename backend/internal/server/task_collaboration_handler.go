package server

// Collaboration endpoints for work items (docs/学习muse/03-架构方案.md §4,
// phase P3).
//
// Routes (all dispatched from handleTaskOperations, all requiring the local
// task store):
//
//	GET  /api/tasks/{id}/participants   名单
//	PUT  /api/tasks/{id}/participants   整份替换名单（PUT 语义）
//	GET  /api/tasks/{id}/activity       活动流（协作事件，不是 ACC 运行事件）
//	POST /api/tasks/{id}/activity       追加一条评论事件
//	POST /api/tasks/{id}/delegate       委派：落 owner / participants + 发通知
//
// Two naming notes, both forced by routes that already exist:
//
//   - `GET /api/tasks/{id}/events` is the **ACC run event** projection
//     (handleTaskRunEvents). The collaboration activity stream therefore lives
//     at `/activity` rather than stealing that path.
//   - `POST /api/tasks/delegate` already means "create this task through ACC".
//     Person-to-person delegation is scoped to the work item, so it is
//     `/api/tasks/{id}/delegate`.
//
// The actor is always taken from the authenticated claims. A userId in a body
// names *who to delegate to*, never *who is asking*.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

const (
	// maxCommentLen bounds one activity comment. The column is JSONB, so the
	// real limit is the request body, but an unbounded comment is a cheap way
	// to push a 5 MB row into every participant's activity feed.
	maxCommentLen = 2000
	// clientEventIDPrefix namespaces client-supplied event ids so they can
	// never collide with the server-generated ones the reminder hub writes.
	clientEventIDPrefix = "client-"
)

// collaborationContext loads the work item and its participants once, so each
// handler does not repeat the same three error paths. It returns the task on
// success; on failure it has already written the response.
func (s *Server) collaborationContext(w http.ResponseWriter, r *http.Request, taskID string) (*task.Task, []task.Participant, string, bool) {
	wsID := s.workspaceIDFromRequest(r)
	t, err := s.taskStore.GetTaskScoped(r.Context(), taskID, wsID)
	if err != nil || t == nil {
		writeError(w, http.StatusNotFound, "task not found")
		return nil, nil, "", false
	}
	parts, err := s.taskStore.ListParticipants(r.Context(), taskID, wsID)
	if err != nil {
		// A missing participant list must not read as "no participants", which
		// would silently turn every private task into a world-readable one.
		writeError(w, http.StatusInternalServerError, "read participants failed")
		return nil, nil, "", false
	}
	return t, parts, wsID, true
}

// handleTaskParticipants serves GET and PUT /api/tasks/{id}/participants.
func (s *Server) handleTaskParticipants(w http.ResponseWriter, r *http.Request, taskID string) {
	t, parts, _, ok := s.collaborationContext(w, r, taskID)
	if !ok {
		return
	}
	actor := s.userIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		if !task.CanReadWorkItem(t, parts, actor) {
			writeError(w, http.StatusForbidden, "not a participant of this work item")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"participants": parts})

	case http.MethodPut:
		if !task.CanWriteWorkItem(t, parts, actor) {
			writeError(w, http.StatusForbidden, "only the owner or a participant may change the participant set")
			return
		}
		var body struct {
			Participants []task.Participant `json:"participants"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		clean, err := normalizeParticipants(body.Participants)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.taskStore.SetParticipants(r.Context(), taskID, s.workspaceIDFromRequest(r), clean); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// This is the structural edit. It deliberately fires no notification:
		// adding a watcher is not news, and the action worth telling people
		// about is delegation, which has its own endpoint.
		writeJSON(w, http.StatusOK, map[string]any{"participants": clean})

	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or PUT only")
	}
}

// normalizeParticipants validates a PUT payload: no blank ids, valid roles, and
// at most one owner. It returns the list with ids and roles trimmed.
func normalizeParticipants(in []task.Participant) ([]task.Participant, error) {
	out := make([]task.Participant, 0, len(in))
	seen := make(map[string]bool, len(in))
	owners := 0
	for _, p := range in {
		id := strings.TrimSpace(p.UserID)
		if id == "" {
			return nil, fmt.Errorf("participant userId is required")
		}
		role := strings.TrimSpace(p.Role)
		if role == "" {
			role = task.RoleAssignee
		}
		if !task.ValidParticipantRole(role) {
			return nil, fmt.Errorf("invalid participant role %q", p.Role)
		}
		if seen[id] {
			// Last write wins on role, but a duplicate row would otherwise
			// produce two notifications to the same person.
			for i := range out {
				if out[i].UserID == id {
					out[i].Role = role
				}
			}
			if role == task.RoleOwner {
				owners++
			}
			continue
		}
		seen[id] = true
		if role == task.RoleOwner {
			owners++
		}
		out = append(out, task.Participant{UserID: id, Role: role})
	}
	if owners > 1 {
		return nil, fmt.Errorf("at most one participant may have the owner role")
	}
	return out, nil
}

// handleTaskActivity serves GET and POST /api/tasks/{id}/activity.
func (s *Server) handleTaskActivity(w http.ResponseWriter, r *http.Request, taskID string) {
	t, parts, wsID, ok := s.collaborationContext(w, r, taskID)
	if !ok {
		return
	}
	actor := s.userIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		if !task.CanReadWorkItem(t, parts, actor) {
			writeError(w, http.StatusForbidden, "not a participant of this work item")
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		events, err := s.taskStore.ListEvents(r.Context(), taskID, wsID, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events})

	case http.MethodPost:
		if !task.CanWriteWorkItem(t, parts, actor) {
			writeError(w, http.StatusForbidden, "only the owner or a participant may comment")
			return
		}
		var body struct {
			Comment string `json:"comment"`
			EventID string `json:"eventId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		comment := strings.TrimSpace(body.Comment)
		if comment == "" {
			writeError(w, http.StatusBadRequest, "comment is required")
			return
		}
		if len([]rune(comment)) > maxCommentLen {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("comment must be at most %d characters", maxCommentLen))
			return
		}
		ev, err := s.appendWorkItemEvent(r.Context(), taskID, wsID, task.EventComment, actor,
			task.EventPayload{TaskTitle: t.Title, Comment: comment},
			strings.TrimSpace(body.EventID))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.dispatchWorkItemNotification(r.Context(), t, parts, ev)
		writeJSON(w, http.StatusCreated, map[string]any{"event": ev})

	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

// handleTaskDelegate serves POST /api/tasks/{id}/delegate. Delegation is the
// one collaboration action that is *about* another person, so it is the one
// that must be atomic in spirit: owner, participants and the activity stream
// move together, and the assignee is notified.
func (s *Server) handleTaskDelegate(w http.ResponseWriter, r *http.Request, taskID string) {
	t, parts, wsID, ok := s.collaborationContext(w, r, taskID)
	if !ok {
		return
	}
	actor := s.userIDFromRequest(r)
	if !task.CanWriteWorkItem(t, parts, actor) {
		writeError(w, http.StatusForbidden, "only the owner or a participant may delegate")
		return
	}

	var body struct {
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	target := strings.TrimSpace(body.UserID)
	if target == "" {
		writeError(w, http.StatusBadRequest, "userId is required")
		return
	}
	role := strings.TrimSpace(body.Role)
	if role == "" {
		role = task.RoleAssignee
	}
	if !task.ValidParticipantRole(role) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid role %q", body.Role))
		return
	}

	// Refuse to hand a work item to someone who cannot open it. Without this
	// check an owner could delegate onto a private task and the new owner
	// would be bounced by their own first read.
	existing, err := s.taskStore.GetTaskScoped(r.Context(), taskID, wsID)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	targetParts := append(append([]task.Participant{}, parts...), task.Participant{UserID: target, Role: role})
	if !task.CanReadWorkItem(existing, targetParts, target) {
		writeError(w, http.StatusBadRequest, "cannot delegate to a user who cannot read this work item; widen visibility first")
		return
	}

	// 1. participants: replace this user's entry, or append them.
	next := make([]task.Participant, 0, len(parts)+1)
	replaced := false
	for _, p := range parts {
		if strings.TrimSpace(p.UserID) == target {
			next = append(next, task.Participant{UserID: target, Role: role})
			replaced = true
			continue
		}
		// Delegating ownership demotes the previous owner to an assignee
		// instead of leaving two owners in the participant table.
		if role == task.RoleOwner && p.Role == task.RoleOwner {
			next = append(next, task.Participant{UserID: p.UserID, Role: task.RoleAssignee})
			continue
		}
		next = append(next, p)
	}
	if !replaced {
		next = append(next, task.Participant{UserID: target, Role: role})
	}
	if err := s.taskStore.SetParticipants(r.Context(), taskID, wsID, next); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 2. owner_id mirrors the owner role, so access checks and the list view
	//    agree on who is in charge.
	if role == task.RoleOwner && t.OwnerID != target {
		ownerID := target
		if _, err := s.taskStore.UpdateTaskScoped(r.Context(), taskID, wsID, task.TaskUpdate{OwnerID: &ownerID}); err != nil {
			// The participant table is already correct; a stale owner_id would
			// misreport who is in charge, so surface it rather than hide it.
			writeError(w, http.StatusInternalServerError, "set owner: "+err.Error())
			return
		}
		t.OwnerID = target
	}

	// 3. activity stream + notification.
	ev, err := s.appendWorkItemEvent(r.Context(), taskID, wsID, task.EventAssigned, actor,
		task.EventPayload{UserID: target, TaskTitle: t.Title}, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.dispatchWorkItemNotification(r.Context(), t, next, ev)

	s.Write(r, "task.delegated", "task:"+taskID, AuditFields{
		Success: true,
		Detail:  "to=" + target + " role=" + role,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"participants": next,
		"ownerId":      t.OwnerID,
		"event":        ev,
	})
}

// appendWorkItemEvent writes one activity entry. When clientEventID is empty a
// server id is generated; a client-supplied one is namespaced so a mobile retry
// cannot collide with, or overwrite, a server-written event.
func (s *Server) appendWorkItemEvent(ctx context.Context, taskID, wsID, eventType, actor string, p task.EventPayload, clientEventID string) (task.WorkItemEvent, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return task.WorkItemEvent{}, fmt.Errorf("encode event payload: %w", err)
	}
	eventID := ""
	if clientEventID != "" {
		if !strings.HasPrefix(clientEventID, clientEventIDPrefix) || len(clientEventID) > 80 {
			return task.WorkItemEvent{}, fmt.Errorf("eventId must start with %q and be at most 80 characters", clientEventIDPrefix)
		}
		eventID = clientEventID
	} else {
		eventID = eventType + "-" + generateUUID()
	}
	ev := task.WorkItemEvent{
		WorkspaceID: wsID,
		TaskID:      taskID,
		EventID:     eventID,
		EventType:   eventType,
		ActorUserID: actor,
		Payload:     raw,
	}
	if err := s.taskStore.AppendEvent(ctx, ev); err != nil {
		return task.WorkItemEvent{}, err
	}
	return ev, nil
}

// dispatchWorkItemNotification fans an activity event out to the recipients
// defined in docs/学习muse/03-架构方案.md §4.2.
//
// Notification is a side effect, never a precondition: a failure here is logged
// and swallowed, because the comment and the delegation have already been
// persisted and reporting 500 would invite the client to retry a write that
// actually succeeded.
// notifyWorkItemStatusChange records a status transition in the activity
// stream and notifies the people §4.2 names.
//
// This is the producer that was missing. `NotificationKind` has mapped
// `status_changed` and `completed` to notification kinds since P3, and
// NotifyRecipients has decided who hears about them, but nothing ever wrote
// either event: a task could be completed by a teammate and every participant
// would be told nothing at all. The original requirement ("包括通知") was met on
// paper only.
//
// Both steps are best effort. The status is already persisted by the time this
// runs, so failing the request would make the client retry a write that
// succeeded; losing the notification is bad, losing the update is worse.
func (s *Server) notifyWorkItemStatusChange(ctx context.Context, before, after *task.Task, wsID string) {
	if before == nil || after == nil || s.taskStore == nil {
		return
	}
	eventType := task.StatusChangeEventType(before.Status, after.Status)
	if eventType == "" {
		return
	}
	// The actor is whoever is making the request, never a body field. When
	// there is no request context (a scheduler, a test) the owner is the best
	// available attribution; an empty actor would only cost one extra
	// notification, since NotifyRecipients then excludes nobody.
	ev, err := s.appendWorkItemEvent(ctx, after.ID, wsID, eventType, s.statusChangeActor(ctx, after),
		task.EventPayload{TaskTitle: after.Title, Status: after.Status}, "")
	if err != nil {
		log.Printf("[work_item] status change event for %s failed: %v", after.ID, err)
		return
	}
	parts, err := s.taskStore.ListParticipants(ctx, after.ID, wsID)
	if err != nil {
		log.Printf("[work_item] participants of %s unavailable, notifying owner only: %v", after.ID, err)
		parts = nil
	}
	s.dispatchWorkItemNotification(ctx, after, parts, ev)
}

// statusChangeActor is the user credited with the change. PATCH runs in the
// caller's request context, and the actor is deliberately not taken from the
// body.
func (s *Server) statusChangeActor(ctx context.Context, t *task.Task) string {
	if s == nil || t == nil {
		return ""
	}
	if id, ok := ctx.Value(actorContextKey{}).(string); ok {
		return strings.TrimSpace(id)
	}
	// No request context (a scheduler or a test): the owner is the best
	// available attribution, and an empty actor only costs one extra
	// notification.
	return strings.TrimSpace(t.OwnerID)
}

// actorContextKey carries the authenticated user id for helpers that are not
// handed the *http.Request.
type actorContextKey struct{}

// withActor records the authenticated user id on ctx.
func withActor(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, actorContextKey{}, userID)
}

func (s *Server) dispatchWorkItemNotification(ctx context.Context, t *task.Task, parts []task.Participant, ev task.WorkItemEvent) {
	if s.notifySvc == nil {
		return
	}
	kind := task.NotificationKind(ev.EventType)
	if kind == "" {
		return
	}
	var p task.EventPayload
	if len(ev.Payload) > 0 {
		// A malformed payload must not turn a successful write into an error.
		_ = json.Unmarshal(ev.Payload, &p)
	}
	title := task.NotificationTitle(ev, p)
	ownerID := t.OwnerID
	for _, userID := range task.NotifyRecipients(ev, parts, ownerID, p) {
		_, err := s.notifySvc.Dispatch(ctx, notifycenter.Event{
			WorkspaceID: ev.WorkspaceID,
			UserID:      userID,
			Source:      "work_item",
			Kind:        kind,
			Title:       title,
			Body:        title,
			Payload:     ev.Payload,
			Priority:    "normal",
		})
		if err != nil {
			log.Printf("[work_item] notify %s for %s (%s) failed: %v", userID, t.ID, kind, err)
		}
	}
}
