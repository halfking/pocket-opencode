package server

// Route-level lock for the work-item write guard (consolidation §5.1 第三条).
//
// PATCH and DELETE /api/tasks/{id} used to be workspace-scoped only — a plain
// member of the same workspace could modify or delete another person's private
// work item. Both verbs now go through task.CanWriteWorkItem (owner or
// participant); these tests drive the real HTTP routes against a real task
// store, so a regression reads as the historical 200 instead of 403.
//
// Needs PostgreSQL (POCKET_TEST_POSTGRES_DSN), mirroring the C8 harness in
// server_auth_extended_test.go; it skips cleanly without one. The access rule
// itself is pure and is unit-tested in the task package (access_test.go); what
// is asserted here is that the HTTP layer actually applies it.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

const writeGuardWorkspace = "test-workspace"

// newWorkItemGuardServer builds a Server whose taskStore points at a real
// per-test-schema store, plus a token per actor. The signer secret matches the
// one newTestServerWithAuth uses, so minted tokens authenticate against the
// server's own signer.
func newWorkItemGuardServer(t *testing.T) (*Server, *task.Store, map[string]string) {
	t.Helper()
	pool := mustTestPool(t)
	store, err := task.NewStore(pool)
	if err != nil {
		t.Fatalf("task.NewStore: %v", err)
	}
	srv, _ := newTestServerWithAuth(t)
	srv.taskStore = store

	signer, err := auth.NewSigner("test-secret-for-unit-tests-0123456789", time.Hour)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	tokens := make(map[string]string)
	for user, role := range map[string]string{
		"alice": "admin",  // owns wtg-alice
		"bob":   "member", // same workspace, on nobody's participant list
		"carol": "member", // owns wtg-carol
	} {
		tok, err := signer.SignWithWorkspace(user, role, writeGuardWorkspace)
		if err != nil {
			t.Fatalf("sign token for %s: %v", user, err)
		}
		tokens[user] = tok
	}
	return srv, store, tokens
}

func seedWriteGuardTasks(t *testing.T, store *task.Store) {
	t.Helper()
	ctx := context.Background()
	seed := []*task.Task{
		{ID: "wtg-alice", WorkspaceID: writeGuardWorkspace, Title: "alice 私有项", Status: "active", Priority: "normal", Visibility: task.VisibilityPrivate, OwnerID: "alice"},
		{ID: "wtg-carol", WorkspaceID: writeGuardWorkspace, Title: "carol 私有项", Status: "active", Priority: "normal", Visibility: task.VisibilityPrivate, OwnerID: "carol"},
	}
	for _, tk := range seed {
		if err := store.CreateTask(ctx, tk); err != nil {
			t.Fatalf("CreateTask %s: %v", tk.ID, err)
		}
	}
	if err := store.SetParticipants(ctx, "wtg-alice", writeGuardWorkspace, []task.Participant{
		{UserID: "alice", Role: task.RoleOwner},
	}); err != nil {
		t.Fatalf("seed wtg-alice participants: %v", err)
	}
	if err := store.SetParticipants(ctx, "wtg-carol", writeGuardWorkspace, []task.Participant{
		{UserID: "carol", Role: task.RoleOwner},
		{UserID: "alice", Role: task.RoleAssignee},
	}); err != nil {
		t.Fatalf("seed wtg-carol participants: %v", err)
	}
}

func writeGuardRequest(t *testing.T, srv *Server, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

// The core regression: before the guard, a same-workspace member could PATCH
// someone else's private work item and got 200. Now 403, and the row must be
// unchanged — a guard that 403s but still writes would be worse than none.
func TestTaskWriteGuardBlocksPlainMemberPatch(t *testing.T) {
	srv, store, tokens := newWorkItemGuardServer(t)
	seedWriteGuardTasks(t, store)

	rr := writeGuardRequest(t, srv, http.MethodPatch, "/api/tasks/wtg-alice", tokens["bob"], `{"title":"bob 改的"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bob PATCH someone else's private work item = %d, want 403: %s", rr.Code, rr.Body.String())
	}

	rr = writeGuardRequest(t, srv, http.MethodGet, "/api/tasks/wtg-alice", tokens["alice"], "")
	if rr.Code != http.StatusOK {
		t.Fatalf("owner GET after rejected PATCH = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var got task.Task
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if got.Title != "alice 私有项" {
		t.Fatalf("title = %q, want the untouched original — the rejected PATCH must not have written", got.Title)
	}
}

func TestTaskWriteGuardBlocksPlainMemberDelete(t *testing.T) {
	srv, store, tokens := newWorkItemGuardServer(t)
	seedWriteGuardTasks(t, store)

	rr := writeGuardRequest(t, srv, http.MethodDelete, "/api/tasks/wtg-alice", tokens["bob"], "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bob DELETE someone else's private work item = %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if _, err := store.GetTaskScoped(context.Background(), "wtg-alice", writeGuardWorkspace); err != nil {
		t.Fatalf("work item must survive the rejected DELETE, got: %v", err)
	}
}

func TestTaskWriteGuardAllowsOwnerAndParticipant(t *testing.T) {
	srv, store, tokens := newWorkItemGuardServer(t)
	seedWriteGuardTasks(t, store)

	rr := writeGuardRequest(t, srv, http.MethodPatch, "/api/tasks/wtg-alice", tokens["alice"], `{"title":"alice 改名"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("owner PATCH = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	// alice is not wtg-carol's owner, only a participant — writes are owner-
	// or-participant, and this is the half the old workspace-only check got
	// right by accident. Lock it so a later tightening cannot silently drop
	// participants.
	rr = writeGuardRequest(t, srv, http.MethodPatch, "/api/tasks/wtg-carol", tokens["alice"], `{"title":"参与者改名"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("participant PATCH = %d, want 200: %s", rr.Code, rr.Body.String())
	}
}

func TestTaskWriteGuardOwnerDelete(t *testing.T) {
	srv, store, tokens := newWorkItemGuardServer(t)
	seedWriteGuardTasks(t, store)

	rr := writeGuardRequest(t, srv, http.MethodDelete, "/api/tasks/wtg-carol", tokens["carol"], "")
	if rr.Code != http.StatusOK {
		t.Fatalf("owner DELETE = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	rr = writeGuardRequest(t, srv, http.MethodGet, "/api/tasks/wtg-carol", tokens["carol"], "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("GET after owner DELETE = %d, want 404: %s", rr.Code, rr.Body.String())
	}
}

func TestTaskWriteGuardUnknownTaskIs404(t *testing.T) {
	srv, store, tokens := newWorkItemGuardServer(t)
	seedWriteGuardTasks(t, store)

	rr := writeGuardRequest(t, srv, http.MethodPatch, "/api/tasks/wtg-missing", tokens["alice"], `{"title":"x"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("PATCH unknown task = %d, want 404: %s", rr.Code, rr.Body.String())
	}
	rr = writeGuardRequest(t, srv, http.MethodDelete, "/api/tasks/wtg-missing", tokens["alice"], "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("DELETE unknown task = %d, want 404: %s", rr.Code, rr.Body.String())
	}
}

// Route-registration lock, same judgement rule as
// TestCollaborationRoutesAreRegistered: with no store wired the verbs must
// answer 503 (not 404), so the guard work above cannot silently rot the route.
// Needs no database — the 503 fires before any store access.
func TestTaskWriteRoutesStayRegistered(t *testing.T) {
	srv, token := newTestServerWithAuth(t)

	rr := writeGuardRequest(t, srv, http.MethodPatch, "/api/tasks/wtg-alice", token, `{"title":"x"}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("PATCH with nil store = %d, want 503 (route must stay registered): %s", rr.Code, rr.Body.String())
	}
	rr = writeGuardRequest(t, srv, http.MethodDelete, "/api/tasks/wtg-alice", token, "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("DELETE with nil store = %d, want 503 (route must stay registered): %s", rr.Code, rr.Body.String())
	}
}
