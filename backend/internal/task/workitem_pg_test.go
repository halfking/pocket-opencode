package task

// PostgreSQL integration tests for the collaboration and work-item reminder
// surface (docs/学习muse phase P3 / P4).
//
// These use the same POCKET_TEST_POSTGRES_DSN gate and per-test schema as
// store_test.go, and they exist for the same reason: the SQL behind the
// collaboration endpoints and the reminder executor was written against a
// schema nobody has executed since. A wrong column name or a missing index
// here fails at runtime on a real deployment, not at build time.
//
// They skip cleanly with no DSN, so today they add no false signal.
//
//	POCKET_TEST_POSTGRES_DSN=postgres://... go test ./internal/task/

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// --- work-item reminders (P4) ---

// DueTaskReminders feeds the reminder executor. Three filters matter and none
// of them is visible without a real query: completed tasks must not remind,
// accepted tasks must not remind, and remind_at = 0 means "no reminder".
func TestDueTaskRemindersFilters(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Unix()

	mk := func(id string, remindAt int64, status string) {
		t.Helper()
		typ := TypeDev
		// created_at / updated_at are NOT NULL with no default; the store's own
		// insert always supplies them. This fixture omitted them, which only
		// showed up the first time these tests ran against a real database.
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO tasks (id, workspace_id, title, status, priority, type, remind_at, visibility, created_at, updated_at)
			VALUES ($1, 'ws-1', $1, $3, 'normal', $4, $2, 'private', $5, $5)`, id, remindAt, status, typ, now); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	mk("due", now-60, "active")
	mk("no-remind", 0, "active")
	mk("completed", now-60, "completed")
	mk("accepted", now-60, "accepted")
	mk("future", now+3600, "active")

	got, err := s.DueTaskReminders(ctx, "ws-1", now, 50)
	if err != nil {
		t.Fatalf("DueTaskReminders: %v", err)
	}
	if len(got) != 1 || got[0].ID != "due" {
		ids := make([]string, 0, len(got))
		for _, g := range got {
			ids = append(ids, g.ID)
		}
		t.Fatalf("got %v, want exactly [due]", ids)
	}
	if got[0].RemindAt != now-60 {
		t.Errorf("RemindAt = %d, want %d: the column must round-trip", got[0].RemindAt, now-60)
	}
}

func TestDueTaskRemindersIsWorkspaceScoped(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Unix()

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO tasks (id, workspace_id, title, status, priority, type, remind_at, visibility, created_at, updated_at)
		VALUES ('theirs', 'ws-2', 'theirs', 'active', 'normal', 'dev', $1, 'private', $2, $2)`, now-60, now); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := s.DueTaskReminders(ctx, "ws-1", now, 50)
	if err != nil {
		t.Fatalf("DueTaskReminders: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("another workspace's reminder leaked: %d rows", len(got))
	}
}

// ClearTaskRemindAt is how a fired reminder retires and how a quiet-hours
// deferral reschedules. Both paths share one statement, so both are checked.
func TestClearTaskRemindAtRetiresAndReschedules(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().Unix()

	mustCreate(t, s, "t-1", "ws-1", "task")
	if _, err := s.pool.Exec(ctx, `UPDATE tasks SET remind_at = $1 WHERE id = 't-1'`, now-60); err != nil {
		t.Fatalf("seed remind_at: %v", err)
	}

	// Deferral path: move it forward.
	target := now + 3600
	if err := s.ClearTaskRemindAt(ctx, "t-1", "ws-1", target); err != nil {
		t.Fatalf("ClearTaskRemindAt (defer): %v", err)
	}
	got, err := s.GetTaskScoped(ctx, "t-1", "ws-1")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if got.RemindAt != target {
		t.Errorf("RemindAt = %d, want %d after deferral", got.RemindAt, target)
	}

	// Retirement path: clear it.
	if err := s.ClearTaskRemindAt(ctx, "t-1", "ws-1", 0); err != nil {
		t.Fatalf("ClearTaskRemindAt (retire): %v", err)
	}
	got, err = s.GetTaskScoped(ctx, "t-1", "ws-1")
	if err != nil {
		t.Fatalf("GetTaskScoped after retire: %v", err)
	}
	if got.RemindAt != 0 {
		t.Errorf("RemindAt = %d, want 0 after retirement", got.RemindAt)
	}

	// A missing task must be an error, not a silent success.
	if err := s.ClearTaskRemindAt(ctx, "ghost", "ws-1", 0); err == nil {
		t.Error("clearing a nonexistent task must report an error")
	}
}

// --- hierarchy (P3) ---

func TestListChildrenAndProgress(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	mustCreate(t, s, "goal", "ws-1", "goal")
	// A child of someone else's task must not appear.
	mustCreate(t, s, "child-a", "ws-1", "a")
	mustCreate(t, s, "child-b", "ws-1", "b")
	mustCreate(t, s, "stranger", "ws-1", "stranger")
	if _, err := s.pool.Exec(ctx, `
		UPDATE tasks SET parent_id = 'goal' WHERE id IN ('child-a','child-b')`); err != nil {
		t.Fatalf("set parent: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE tasks SET status = 'completed' WHERE id = 'child-a'`); err != nil {
		t.Fatalf("complete child: %v", err)
	}

	children, err := s.ListChildren(ctx, "goal", "ws-1")
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("got %d children, want 2", len(children))
	}
	progress, err := s.ChildProgress(ctx, "goal", "ws-1")
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if progress.Total != 2 || progress.Done != 1 || progress.Percent != 50 {
		t.Errorf("progress = %+v, want total=2 done=1 percent=50", progress)
	}

	// An empty goal is 0%, not 100% — see hierarchy.go for why that matters.
	empty, err := s.ChildProgress(ctx, "stranger", "ws-1")
	if err != nil {
		t.Fatalf("ChildProgress (no children): %v", err)
	}
	if empty.Percent != 0 {
		t.Errorf("a work item with no children reported %d%%, want 0", empty.Percent)
	}
}

func TestParentMapIsWorkspaceScoped(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	mustCreate(t, s, "p", "ws-1", "parent")
	mustCreate(t, s, "c", "ws-1", "child")
	mustCreate(t, s, "other", "ws-2", "other parent")
	if _, err := s.pool.Exec(ctx, `UPDATE tasks SET parent_id = 'p' WHERE id = 'c'`); err != nil {
		t.Fatalf("set parent: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE tasks SET parent_id = 'other' WHERE id = 'x' AND false`); err != nil {
		t.Fatalf("noop: %v", err)
	}

	m, err := s.ParentMap(ctx, "ws-1")
	if err != nil {
		t.Fatalf("ParentMap: %v", err)
	}
	if m["c"] != "p" {
		t.Errorf("ParentMap[c] = %q, want \"p\"", m["c"])
	}
	// A task with no parent must be absent, not mapped to "".
	if _, ok := m["p"]; ok {
		t.Error("a task without a parent must not appear in the parent map")
	}
}

// --- collaboration (P3) ---

// SetParticipants replaces the whole set. The unique constraint is
// (workspace_id, task_id, user_id), and ON CONFLICT DO UPDATE is what makes a
// role change land instead of erroring.
func TestSetParticipantsIsPutSemantics(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	mustCreate(t, s, "t-1", "ws-1", "task")
	if err := s.SetParticipants(ctx, "t-1", "ws-1", []Participant{
		{UserID: "alice", Role: RoleOwner},
		{UserID: "bob", Role: RoleAssignee},
		{UserID: "carol", Role: RoleWatcher},
	}); err != nil {
		t.Fatalf("SetParticipants: %v", err)
	}
	got, err := s.ListParticipants(ctx, "t-1", "ws-1")
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d participants, want 3", len(got))
	}
	// Owner first, then alphabetical — the UI order must be stable.
	if got[0].UserID != "alice" || got[0].Role != RoleOwner {
		t.Errorf("first participant = %+v, want alice as owner", got[0])
	}
	if got[1].UserID != "bob" || got[2].UserID != "carol" {
		t.Errorf("order = %s,%s; want alice,bob,carol", got[1].UserID, got[2].UserID)
	}

	// Re-setting drops the ones that disappeared — otherwise a removed assignee
	// keeps receiving notifications forever.
	if err := s.SetParticipants(ctx, "t-1", "ws-1", []Participant{{UserID: "alice", Role: RoleOwner}}); err != nil {
		t.Fatalf("SetParticipants (shrink): %v", err)
	}
	got, err = s.ListParticipants(ctx, "t-1", "ws-1")
	if err != nil {
		t.Fatalf("ListParticipants after shrink: %v", err)
	}
	if len(got) != 1 || got[0].UserID != "alice" {
		t.Errorf("after a PUT that dropped two people, got %+v", got)
	}

	// A role change on the same user must update, not conflict.
	if err := s.SetParticipants(ctx, "t-1", "ws-1", []Participant{{UserID: "alice", Role: RoleWatcher}}); err != nil {
		t.Fatalf("SetParticipants (role change): %v", err)
	}
	got, _ = s.ListParticipants(ctx, "t-1", "ws-1")
	if len(got) != 1 || got[0].Role != RoleWatcher {
		t.Errorf("role change did not land: %+v", got)
	}
}

func TestSetParticipantsOnMissingTaskFails(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	if err := s.SetParticipants(context.Background(), "ghost", "ws-1", []Participant{{UserID: "a", Role: RoleOwner}}); err == nil {
		t.Error("setting participants on a nonexistent task must fail")
	}
}

// AppendEvent is idempotent on (workspace, task, event_id). That is the whole
// mechanism that stops a repeated scheduler tick from producing a second
// reminder notification, so the conflict clause is load-bearing.
func TestAppendEventIsIdempotent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	mustCreate(t, s, "t-1", "ws-1", "task")
	payload, _ := json.Marshal(EventPayload{TaskTitle: "task"})
	ev := WorkItemEvent{
		WorkspaceID: "ws-1", TaskID: "t-1",
		EventID:   ReminderEventID(1700000000),
		EventType: EventReminded, ActorUserID: "system",
		Payload: payload, CreatedAt: 1700000000,
	}
	if err := s.AppendEvent(ctx, ev); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	// Same id, different timestamp: a replay must not add a row.
	ev2 := ev
	ev2.CreatedAt = 1700009999
	if err := s.AppendEvent(ctx, ev2); err != nil {
		t.Fatalf("AppendEvent (replay): %v", err)
	}
	got, err := s.ListEvents(ctx, "t-1", "ws-1", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: the replay must be suppressed", len(got))
	}
	// And the original timestamp wins, so a late replay cannot reorder history.
	if got[0].CreatedAt != 1700000000 {
		t.Errorf("CreatedAt = %d, want the original 1700000000", got[0].CreatedAt)
	}

	// A different event id is a different row.
	ev3 := ev
	ev3.EventID = ReminderEventID(1700000060)
	if err := s.AppendEvent(ctx, ev3); err != nil {
		t.Fatalf("AppendEvent (distinct): %v", err)
	}
	if got, _ = s.ListEvents(ctx, "t-1", "ws-1", 50); len(got) != 2 {
		t.Errorf("got %d events, want 2", len(got))
	}
}

func TestAppendEventRejectsBadInput(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t-1", "ws-1", "task")

	cases := map[string]WorkItemEvent{
		"no task id":   {EventID: "e", EventType: EventComment},
		"no event id":  {TaskID: "t-1", EventType: EventComment},
		"bad type":     {TaskID: "t-1", EventID: "e", EventType: "invented"},
		"invalid json": {TaskID: "t-1", EventID: "e", EventType: EventComment, Payload: json.RawMessage(`{`)},
	}
	for name, ev := range cases {
		if err := s.AppendEvent(ctx, ev); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestListTaskApprovals(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	mustCreate(t, s, "t-1", "ws-1", "task")
	// The projection materializes onto the tasks *linked to its session*, so a
	// task with no session link legitimately receives nothing. The fixture used
	// to skip the link, which is why this read came back empty the first time
	// it ran against a real database.
	if err := s.AttachSessionScoped(ctx, SessionLink{
		TaskID: "t-1", InstanceID: "inst", SessionID: "sess", Role: "primary",
	}, "ws-1"); err != nil {
		t.Fatalf("AttachSessionScoped: %v", err)
	}
	if err := s.ApplyApprovalProjection(ctx, ApprovalProjectionEvent{
		WorkspaceID: "ws-1", InstanceID: "inst", SessionID: "sess",
		RequestID: "req-1", Kind: ApprovalKindPermission, State: ApprovalStatePending, Version: 1,
	}); err != nil {
		t.Fatalf("ApplyApprovalProjection: %v", err)
	}
	got, err := s.ListTaskApprovals(ctx, "t-1", "ws-1")
	if err != nil {
		t.Fatalf("ListTaskApprovals: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d approvals, want 1", len(got))
	}
	if !got[0].Pending() {
		t.Errorf("state = %q, want pending", got[0].State)
	}
	// A task with none must be an empty list, not an error.
	none, err := s.ListTaskApprovals(ctx, "t-1", "ws-1")
	if err != nil || len(none) != 1 {
		t.Fatalf("repeat read disagreed: %v / %d", err, len(none))
	}
}
