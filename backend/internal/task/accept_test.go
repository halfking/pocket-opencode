package task

// accept_test.go — PG-backed integration tests for AcceptTaskScoped.
//
// Slice 1 (RED → GREEN): a `completed` task transitions to `accepted` with
// actor, evidence bundle, and timestamp set. The returned task reflects them.
//
// Skipped without POCKET_TEST_POSTGRES_DSN, mirroring the rest of task tests.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// AcceptTaskScoped on a completed task transitions to `accepted` and records
// actor, bundle, and timestamp. The returned task carries the new state.
func TestAcceptTaskScoped_CompletedTaskTransitionsToAccepted(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	created := &Task{
		ID:          "t-accept-1",
		WorkspaceID: "ws-owner",
		Title:       "accept me",
		Status:      "open",
		Priority:    "normal",
	}
	if err := s.CreateTask(context.Background(), created); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Force the task into completed via the linearized completion path so the
	// test exercises the realistic pre-condition. CompleteTaskScoped will
	// return ErrPendingApprovals if a pending row exists; with a fresh task
	// there are none.
	completed, err := s.CompleteTaskScoped(context.Background(), created.ID, "ws-owner", TaskUpdate{})
	if err != nil {
		t.Fatalf("seed completed task: %v", err)
	}
	if completed.Status != "completed" {
		t.Fatalf("pre-condition: task should be completed, got %s", completed.Status)
	}

	bundle := EvidenceBundle{
		Note: "ran smoke test, output matches expected",
		References: []EvidenceReference{
			{Kind: "url", URI: "https://ci.example/job/123", Label: "CI run"},
			{Kind: "audit_event", URI: "audit:abc-123", Label: "verification"},
		},
	}

	accepted, err := s.AcceptTaskScoped(context.Background(), created.ID, "ws-owner", "actor-7", bundle)
	if err != nil {
		t.Fatalf("AcceptTaskScoped: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Fatalf("status: got %q, want %q", accepted.Status, "accepted")
	}
	if accepted.AcceptedBy == nil || *accepted.AcceptedBy != "actor-7" {
		var got string
		if accepted.AcceptedBy != nil {
			got = *accepted.AcceptedBy
		}
		t.Fatalf("accepted_by: got %q, want %q", got, "actor-7")
	}
	if accepted.AcceptedAt == nil || *accepted.AcceptedAt == 0 {
		t.Fatalf("accepted_at: got nil/zero, want a non-zero unix second")
	}
	if accepted.EvidenceBundle == nil ||
		len(accepted.EvidenceBundle.References) != 2 ||
		accepted.EvidenceBundle.Note != bundle.Note {
		t.Fatalf("evidence_bundle round-trip mismatch: %+v", accepted.EvidenceBundle)
	}
}

// Slice 2 (RED → GREEN): a non-completed task is rejected with
// ErrTaskNotCompletable and its status is unchanged. Covers `open`,
// `running`, and `accepted` (already-accepted must not be silently
// overwritten — `accepted` is terminal).
func TestAcceptTaskScoped_NonCompletedIsRejected(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	cases := []struct {
		name        string
		initialStat string
	}{
		{"open", "open"},
		{"running", "running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := "t-rej-" + tc.name
			if err := s.CreateTask(context.Background(), &Task{
				ID: id, WorkspaceID: "ws", Title: "x", Status: tc.initialStat, Priority: "normal",
			}); err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			_, err := s.AcceptTaskScoped(context.Background(), id, "ws", "actor", EvidenceBundle{})
			if !errors.Is(err, ErrTaskNotCompletable) {
				t.Fatalf("want ErrTaskNotCompletable, got %v", err)
			}
			got, _ := s.GetTaskScoped(context.Background(), id, "ws")
			if got.Status != tc.initialStat {
				t.Fatalf("status mutated on rejection: got %q, want %q", got.Status, tc.initialStat)
			}
		})
	}

	// Already-accepted case: drive a task through completed → accepted first,
	// then assert a second accept is rejected with ErrTaskNotCompletable and
	// does not overwrite accepted_at/_by.
	t.Run("already_accepted", func(t *testing.T) {
		id := "t-rej-already"
		if err := s.CreateTask(context.Background(), &Task{
			ID: id, WorkspaceID: "ws", Title: "x", Status: "open", Priority: "normal",
		}); err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		if _, err := s.CompleteTaskScoped(context.Background(), id, "ws", TaskUpdate{}); err != nil {
			t.Fatalf("complete: %v", err)
		}
		first, err := s.AcceptTaskScoped(context.Background(), id, "ws", "actor-1", EvidenceBundle{Note: "first"})
		if err != nil {
			t.Fatalf("first accept: %v", err)
		}
		firstAt := *first.AcceptedAt
		firstBy := *first.AcceptedBy

		// Force the clock forward by writing directly to the accepted_* cols is
		// not portable, so just retry with a different actor. If second accept
		// succeeded, accepted_by would mutate.
		time.Sleep(1 * time.Second)
		_, err = s.AcceptTaskScoped(context.Background(), id, "ws", "actor-2", EvidenceBundle{Note: "second"})
		if !errors.Is(err, ErrTaskNotCompletable) {
			t.Fatalf("second accept want ErrTaskNotCompletable, got %v", err)
		}
		again, _ := s.GetTaskScoped(context.Background(), id, "ws")
		if again.Status != "accepted" {
			t.Fatalf("status mutated: %q", again.Status)
		}
		if *again.AcceptedBy != firstBy {
			t.Fatalf("accepted_by mutated: %q → %q", firstBy, *again.AcceptedBy)
		}
		if *again.AcceptedAt != firstAt {
			t.Fatalf("accepted_at mutated: %d → %d", firstAt, *again.AcceptedAt)
		}
	})
}

// Slice 4 (RED → GREEN): a late `pending` approval projection event must not
// reopen an accepted task. This extends the existing "no reopening of
// completed" invariant to the new terminal `accepted` status. We attach a
// session to an accepted task, send a pending projection event for that
// session, and assert: status stays `accepted`, no pending projection row is
// inserted for the task.
func TestAcceptTask_LatePendingDoesNotReopenAccepted(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	created := &Task{
		ID:          "t-accept-pend",
		WorkspaceID: "ws",
		Title:       "t",
		Status:      "open",
		Priority:    "normal",
	}
	if err := s.CreateTask(context.Background(), created); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := s.CompleteTaskScoped(context.Background(), created.ID, "ws", TaskUpdate{}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := s.AcceptTaskScoped(context.Background(), created.ID, "ws", "actor", EvidenceBundle{Note: "ok"}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	// Attach a session so a late projection event has a target.
	link := &SessionLink{
		TaskID: created.ID,
		InstanceID: "inst-1", SessionID: "sess-1",
		Role: "owner",
	}
	if err := s.AttachSessionScoped(context.Background(), *link, "ws"); err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	// Now fire a pending projection event for that session. The anti-
	// regression guard must keep the accepted task closed.
	if err := s.ApplyApprovalProjection(context.Background(), ApprovalProjectionEvent{
		WorkspaceID: "ws", InstanceID: "inst-1", SessionID: "sess-1",
		RequestID: "r-1", Kind: ApprovalKindPermission, State: ApprovalStatePending, Version: 1,
	}); err != nil {
		t.Fatalf("ApplyApprovalProjection: %v", err)
	}
	again, err := s.GetTaskScoped(context.Background(), created.ID, "ws")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if again.Status != "accepted" {
		t.Fatalf("late pending reopened accepted task: status=%q", again.Status)
	}
	if again.PendingApprovals != 0 {
		t.Fatalf("late pending inflated pending_approvals: %d", again.PendingApprovals)
	}
}

// Slice 5 (RED → GREEN): cross-workspace rejection. AcceptTaskScoped must
// not succeed against a task owned by another workspace, even with the right
// task id. The error must not be the "not completable" sentinel — it must
// look like "task not found" so a tenant boundary probe gets no information.
func TestAcceptTaskScoped_CrossWorkspaceIsNotFound(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	if err := s.CreateTask(context.Background(), &Task{
		ID: "t-other", WorkspaceID: "ws-other", Title: "x", Status: "open", Priority: "normal",
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	_, err := s.AcceptTaskScoped(context.Background(), "t-other", "ws-attacker", "actor", EvidenceBundle{})
	if err == nil {
		t.Fatalf("expected an error for cross-workspace accept, got nil")
	}
	if errors.Is(err, ErrTaskNotCompletable) {
		t.Fatalf("cross-workspace must not surface ErrTaskNotCompletable (info leak); got %v", err)
	}
}