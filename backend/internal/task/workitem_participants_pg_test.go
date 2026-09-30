package task

// B-3 store half: the batch participant read that lets the /children endpoint
// apply CanReadWorkItem to every child in one query.
//
// Needs a live PostgreSQL instance (POCKET_TEST_POSTGRES_DSN); see
// workitem_pg_test.go for the same gate and schema harness. It skips cleanly
// without one.

import (
	"context"
	"strings"
	"testing"
)

func TestListParticipantsForTasks(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	mustCreate(t, s, "child-a", "ws-1", "a")
	mustCreate(t, s, "child-b", "ws-1", "b")
	mustCreate(t, s, "child-none", "ws-1", "c")
	mustCreate(t, s, "other-ws", "ws-2", "d")

	if err := s.SetParticipants(ctx, "child-a", "ws-1", []Participant{
		{UserID: "alice", Role: RoleOwner},
		{UserID: "bob", Role: RoleAssignee},
	}); err != nil {
		t.Fatalf("set participants: %v", err)
	}
	if err := s.SetParticipants(ctx, "child-b", "ws-1", []Participant{
		{UserID: "carol", Role: RoleWatcher},
	}); err != nil {
		t.Fatalf("set participants: %v", err)
	}
	// A participant row in another workspace must never answer for a task in
	// this one, even when the task id matches.
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO work_item_participants (workspace_id, task_id, user_id, role, created_at)
		VALUES ('ws-2', 'other-ws', 'dave', 'assignee', 0)`); err != nil {
		t.Fatalf("seed foreign participant: %v", err)
	}

	got, err := s.ListParticipantsForTasks(ctx, "ws-1", []string{"child-a", "child-b", "child-none"})
	if err != nil {
		t.Fatalf("ListParticipantsForTasks: %v", err)
	}
	if len(got["child-a"]) != 2 {
		t.Errorf("child-a participants = %+v, want 2 rows", got["child-a"])
	}
	if len(got["child-b"]) != 1 || got["child-b"][0].UserID != "carol" {
		t.Errorf("child-b participants = %+v, want carol", got["child-b"])
	}
	// A task with no participants is absent, which reads the same as an empty
	// list to the caller.
	if len(got["child-none"]) != 0 {
		t.Errorf("child-none participants = %+v, want none", got["child-none"])
	}

	// Cross-workspace isolation: the same task id under another tenant returns
	// that tenant's rows and never this one's.
	foreign, err := s.ListParticipantsForTasks(ctx, "ws-2", []string{"other-ws"})
	if err != nil {
		t.Fatalf("ListParticipantsForTasks (ws-2): %v", err)
	}
	if len(foreign["other-ws"]) != 1 || foreign["other-ws"][0].UserID != "dave" {
		t.Errorf("ws-2 participants = %+v, want dave", foreign["other-ws"])
	}
}

// --- B-9: assignees and participants are two views of one fact ---

// The defect: an assignee who was not a participant could not open a private
// work item and received no notification, so the assignment looked like it
// never happened. SyncAssigneeParticipants is the reconciler; the rules that
// matter are who is added, who is removed, and whose role is never clobbered.
func TestSyncAssigneeParticipants(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	mustCreate(t, s, "t-1", "ws-1", "a")
	if err := s.SetParticipants(ctx, "t-1", "ws-1", []Participant{
		{UserID: "alice", Role: RoleOwner},
		{UserID: "watcher", Role: RoleWatcher},
	}); err != nil {
		t.Fatalf("seed participants: %v", err)
	}

	// bob and carol get assigned; alice is re-listed as an assignee, which
	// must not demote her from owner.
	if err := s.SyncAssigneeParticipants(ctx, "t-1", "ws-1", "alice",
		[]string{"bob", "carol", "alice", "  ", "bob"}); err != nil {
		t.Fatalf("sync: %v", err)
	}

	got, err := s.ListParticipants(ctx, "t-1", "ws-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	roles := map[string]string{}
	for _, p := range got {
		roles[p.UserID] = p.Role
	}
	for user, want := range map[string]string{
		"alice": RoleOwner, "bob": RoleAssignee, "carol": RoleAssignee, "watcher": RoleWatcher,
	} {
		if roles[user] != want {
			t.Errorf("%s role = %q, want %q (all: %v)", user, roles[user], want, roles)
		}
	}

	// Un-assigning bob removes him, but the watcher stays: nothing on the task
	// row records that somebody was added on purpose, so the sync must not
	// guess.
	if err := s.SyncAssigneeParticipants(ctx, "t-1", "ws-1", "alice", []string{"carol"}); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	got, _ = s.ListParticipants(ctx, "t-1", "ws-1")
	roles = map[string]string{}
	for _, p := range got {
		roles[p.UserID] = p.Role
	}
	if _, ok := roles["bob"]; ok {
		t.Errorf("an un-assigned participant must be removed, got %v", roles)
	}
	if roles["watcher"] != RoleWatcher {
		t.Errorf("a watcher must survive an assignee sync, got %v", roles)
	}
	if roles["alice"] != RoleOwner {
		t.Errorf("the owner must stay the owner, got %v", roles)
	}
}

// A task in another workspace must not be reachable through the sync: the
// executor-style mistake of trusting a caller-supplied workspace would rewrite
// somebody else's participant list.
func TestSyncAssigneeParticipantsIsWorkspaceScoped(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	mustCreate(t, s, "t-1", "ws-1", "a")
	if err := s.SyncAssigneeParticipants(ctx, "t-1", "ws-2", "alice", []string{"bob"}); err == nil {
		t.Fatal("syncing a task outside the workspace must fail rather than write")
	}
	parts, err := s.ListParticipants(ctx, "t-1", "ws-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(parts) != 0 {
		t.Errorf("a rejected sync must not touch the task, got %+v", parts)
	}
}

// Edge shapes a handler can produce: no owner, no assignees, an empty id.
func TestSyncAssigneeParticipantsEdgeCases(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	mustCreate(t, s, "t-1", "ws-1", "a")

	if err := s.SyncAssigneeParticipants(ctx, "  ", "ws-1", "alice", []string{"bob"}); err == nil {
		t.Error("an empty task id must be rejected")
	}
	if err := s.SyncAssigneeParticipants(ctx, "missing", "ws-1", "alice", []string{"bob"}); err == nil {
		t.Error("an unknown task must be reported, not silently accepted")
	}
	// Nobody to add and nobody to remove: a no-op, not an error.
	if err := s.SyncAssigneeParticipants(ctx, "t-1", "ws-1", "", nil); err != nil {
		t.Errorf("an empty assignment set must be a no-op, got %v", err)
	}
}

// The batching helper must survive the shapes a caller can hand it: no ids, a
// single id, blanks and duplicates all have to produce a valid query.
func TestListParticipantsForTasksEdgeInputs(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	mustCreate(t, s, "child-a", "ws-1", "a")
	if err := s.SetParticipants(ctx, "child-a", "ws-1", []Participant{{UserID: "alice", Role: RoleOwner}}); err != nil {
		t.Fatalf("set participants: %v", err)
	}

	for _, ids := range [][]string{nil, {}, {"  "}, {"child-a", "child-a"}, {"child-a", " "}} {
		got, err := s.ListParticipantsForTasks(ctx, "ws-1", ids)
		if err != nil {
			t.Fatalf("ids %v: %v", ids, err)
		}
		// Blanks and duplicates are dropped, so a list that contains nothing
		// usable must behave exactly like an empty one — no error, no rows.
		usable := false
		for _, id := range ids {
			if strings.TrimSpace(id) != "" {
				usable = true
			}
		}
		want := 0
		if usable {
			want = 1
		}
		if len(got["child-a"]) != want {
			t.Errorf("ids %v: got %d participants, want %d", ids, len(got["child-a"]), want)
		}
	}
}
