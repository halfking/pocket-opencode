package server

// Source → task / learning one-click endpoints (P2, docs/学习muse/05 §3).
//
//	POST /api/tasks/from-source   {sourceKind, sourceId, type?, title?}
//
// Turns "this note / email / RSS item / meeting" into a work item with origin
// provenance, so a todo spotted while reading lands in the same list the user
// already manages. For a meeting it converts the meeting's action items
// (one task per item) — the link that was previously broken: meeting todos only
// existed in the mobile app's local SQLite table and never reached PG.

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/meeting"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// sourceOriginKinds is the closed set of provenance values the endpoint
// accepts, mirrored by task.ValidOriginKind on the write side.
var sourceOriginKinds = map[string]bool{
	task.OriginNote:    true,
	task.OriginEmail:   true,
	task.OriginRSS:     true,
	task.OriginMeeting: true,
}

type fromSourceRequest struct {
	SourceKind string `json:"sourceKind"`
	SourceID   string `json:"sourceId"`
	// Type is the work classification; empty defaults to other.
	Type string `json:"type"`
	// Title overrides the derived title. For a meeting it is ignored (one task
	// per action item).
	Title string `json:"title"`
}

func (s *Server) handleTaskFromSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req fromSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Validation before the store check: a malformed request is a client bug
	// in every deployment, and reporting it as 503 (remote-only mode) would
	// send the caller looking for a configuration problem instead.
	req.SourceKind = strings.TrimSpace(req.SourceKind)
	req.SourceID = strings.TrimSpace(req.SourceID)
	if !sourceOriginKinds[req.SourceKind] {
		writeError(w, http.StatusBadRequest, "sourceKind must be one of note|email|rss|meeting")
		return
	}
	if req.SourceID == "" {
		writeError(w, http.StatusBadRequest, "sourceId is required")
		return
	}
	typ := req.Type
	if typ == "" {
		typ = task.TypeOther
	}
	if !task.ValidType(typ) {
		writeError(w, http.StatusBadRequest, "invalid task type")
		return
	}
	if s.taskStore == nil {
		writeError(w, http.StatusServiceUnavailable, "local task store not configured (remote-only mode)")
		return
	}

	wsID := s.workspaceIDFromRequest(r)
	userID := s.userIDFromRequest(r)
	now := time.Now()

	if req.SourceKind == task.OriginMeeting {
		s.createTasksFromMeeting(w, r, wsID, userID, req.SourceID, typ)
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = req.SourceID
	}
	created := &task.Task{
		ID:          "task-" + generateUUID(),
		Title:       title,
		Description: "来自" + req.SourceKind + "：" + req.SourceID,
		Status:      "active",
		Priority:    "normal",
		Type:        typ,
		OwnerID:     userID,
		Visibility:  task.VisibilityPrivate,
		OriginKind:  req.SourceKind,
		OriginRef:   req.SourceID,
		WorkspaceID: wsID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.taskStore.CreateTask(r.Context(), created); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordOriginEvent(r, wsID, userID, created)
	s.broadcastTaskEvent("task_created", created)
	writeJSON(w, http.StatusCreated, map[string]any{"tasks": []task.Task{*created}})
}

// createTasksFromMeeting converts a meeting's action items into tasks. It is
// idempotent per (meeting, action text): re-running the conversion after a
// sync must not double the list, so an existing task with the same origin_ref
// and title is reused instead of duplicated.
func (s *Server) createTasksFromMeeting(w http.ResponseWriter, r *http.Request, wsID, userID, meetingID, typ string) {
	if s.learningSources == nil {
		writeError(w, http.StatusServiceUnavailable, "source resolver not configured")
		return
	}
	m, err := s.learningSources.ActionItemsForMeeting(meetingID, userID, wsID)
	if err != nil {
		writeError(w, http.StatusNotFound, "meeting not found")
		return
	}

	existing, err := s.taskStore.ListTasksScoped(r.Context(), wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	seen := map[string]bool{}
	for _, t := range existing {
		if t.OriginKind == task.OriginMeeting && t.OriginRef == meetingID {
			seen[strings.TrimSpace(t.Title)] = true
		}
	}

	now := time.Now()
	out := []task.Task{}
	skipped := 0
	for _, item := range m.ActionItems {
		title := strings.TrimSpace(item.Task)
		if title == "" {
			// An action item without text is not actionable; skipping it is
			// better than creating a nameless task.
			skipped++
			continue
		}
		if seen[title] {
			skipped++
			continue
		}
		created := &task.Task{
			ID:          "task-" + generateUUID(),
			Title:       title,
			Description: meetingActionDescription(m.Title, item),
			Status:      "active",
			Priority:    "normal",
			Type:        typ,
			OwnerID:     userID,
			Visibility:  task.VisibilityPrivate,
			OriginKind:  task.OriginMeeting,
			OriginRef:   meetingID,
			Tags:        []string{"meeting"},
			WorkspaceID: wsID,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.taskStore.CreateTask(r.Context(), created); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		seen[title] = true
		out = append(out, *created)
		s.recordOriginEvent(r, wsID, userID, created)
		s.broadcastTaskEvent("task_created", created)
	}

	writeJSON(w, http.StatusCreated, map[string]any{"tasks": out, "skipped": skipped})
}

// recordOriginEvent appends the "created from X" entry to the work item's
// activity stream, so the provenance is visible in the UI and in the audit
// trail rather than living only in a database column.
func (s *Server) recordOriginEvent(r *http.Request, wsID, userID string, t *task.Task) {
	if s.taskStore == nil || t == nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{
		"originKind": t.OriginKind,
		"originRef":  t.OriginRef,
	})
	// EventID is derived from the provenance, so converting the same meeting
	// twice does not double the activity entry.
	eventID := "origin:" + t.OriginKind + ":" + t.OriginRef
	_ = s.taskStore.AppendEvent(r.Context(), task.WorkItemEvent{
		WorkspaceID: wsID,
		TaskID:      t.ID,
		EventID:     eventID,
		EventType:   task.EventCreated,
		ActorUserID: userID,
		Payload:     payload,
		CreatedAt:   t.CreatedAt.Unix(),
	})
}

// meetingActionDescription preserves the action item's owner and deadline,
// which the meeting LLM already extracted. They used to be flattened into a
// "负责人：xxx" string in the mobile client; keeping them as data means the
// task can later be assigned for real (P3) instead of parsed back out of text.
func meetingActionDescription(meetingTitle string, item meeting.ActionItem) string {
	var b strings.Builder
	if meetingTitle != "" {
		b.WriteString("会议《")
		b.WriteString(meetingTitle)
		b.WriteString("》待办")
	}
	if owner := strings.TrimSpace(item.Owner); owner != "" {
		if b.Len() > 0 {
			b.WriteString(" · ")
		}
		b.WriteString("负责人：")
		b.WriteString(owner)
	}
	if deadline := strings.TrimSpace(item.Deadline); deadline != "" {
		if b.Len() > 0 {
			b.WriteString(" · ")
		}
		b.WriteString("截止：")
		b.WriteString(deadline)
	}
	return b.String()
}
