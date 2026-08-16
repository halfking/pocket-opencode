package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

func TestPendingApprovalsForTaskFailsClosedWithoutManagers(t *testing.T) {
	srv := &Server{}
	if _, err := srv.pendingApprovalsForTask(context.Background(), "task-1", "workspace-a"); err == nil {
		t.Fatal("expected completion gate to reject when approval managers are unavailable")
	}
}

func TestIsValidTaskStatus(t *testing.T) {
	for _, status := range []string{"active", "blocked", "completed"} {
		if !isValidTaskStatus(status) {
			t.Errorf("expected %q to be accepted", status)
		}
	}
	for _, status := range []string{"", "open", "pending", "deleted"} {
		if isValidTaskStatus(status) {
			t.Errorf("expected %q to be rejected", status)
		}
	}
}

func TestTaskStatusUpdateError(t *testing.T) {
	completed := "completed"
	blocked := "blocked"
	invalid := "pending"

	for name, current := range map[string]*task.Task{
		"no_status_change":            {PendingApprovals: 1},
		"blocked_is_allowed":          {PendingApprovals: 1},
		"completed_without_approvals": {PendingApprovals: 0},
	} {
		var requested *string
		switch name {
		case "blocked_is_allowed":
			requested = &blocked
		case "completed_without_approvals":
			requested = &completed
		}
		if status, message := taskStatusUpdateError(current, requested); status != 0 || message != "" {
			t.Errorf("%s: expected allowed update, got status=%d message=%q", name, status, message)
		}
	}

	if status, message := taskStatusUpdateError(&task.Task{PendingApprovals: 1}, &completed); status != 409 || message != "task has pending approvals" {
		t.Errorf("pending approvals: status=%d message=%q", status, message)
	}
	if status, message := taskStatusUpdateError(&task.Task{}, &invalid); status != 400 || message != "invalid task status" {
		t.Errorf("invalid status: status=%d message=%q", status, message)
	}
}

func TestAuditTaskStatusChangeRecordsWorkspaceScopedMetadata(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	req := httptest.NewRequest("PATCH", "/api/tasks/task-1", nil)
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), requestIDContextKey{}, "request_abcdefgh"),
		authClaimsContextKey{},
		&authClaims{UserID: "user-1", WorkspaceID: "workspace-a"},
	))

	srv.auditTaskStatusChange(req, &task.Task{
		ID:               "task-1",
		Status:           "completed",
		PendingApprovals: 0,
	}, "active")

	entries, err := srv.auditStore.Query(redclaw.AuditQuery{TenantID: "workspace-a"})
	if err != nil {
		t.Fatalf("query audit store: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one audit entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Action != "task.status.changed" || entry.UserID != "user-1" || entry.Resource != "task:task-1" || !entry.Success {
		t.Fatalf("unexpected audit entry: %+v", entry)
	}
	for _, field := range []string{"from=active", "to=completed", "pending_approvals=0", "request_id=request_abcdefgh"} {
		if !strings.Contains(entry.Detail, field) {
			t.Errorf("audit detail missing %q: %s", field, entry.Detail)
		}
	}
}
