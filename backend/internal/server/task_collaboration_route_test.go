package server

// Route + validation lock for the P3 collaboration endpoints.
//
// Same judgement rule as the learning and from-source route tests: with no
// store wired the endpoint answers 503, an unregistered one answers 404. A 404
// means the whole collaboration surface is unreachable and the UI would show a
// dead participant list on every task.
//
// The access rules themselves (who may read or write a work item) are pure and
// are tested in the task package; what is asserted here is that the HTTP layer
// wires them, that the pre-existing ACC run-event route is untouched, and that
// participant payloads are validated before anything is persisted.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/task"
)

func TestCollaborationRoutesAreRegistered(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	if srv.taskStore != nil {
		t.Skip("test server has a real task store; route-level assertion not applicable")
	}
	token := tokens[""]

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/tasks/t-1/participants", ""},
		{http.MethodPut, "/api/tasks/t-1/participants", `{"participants":[]}`},
		{http.MethodGet, "/api/tasks/t-1/activity", ""},
		{http.MethodPost, "/api/tasks/t-1/activity", `{"comment":"hi"}`},
		{http.MethodPost, "/api/tasks/t-1/delegate", `{"userId":"u-2"}`},
		{http.MethodGet, "/api/tasks/t-1/children", ""},
		{http.MethodPost, "/api/tasks/t-1/subtasks", `{"title":"child"}`},
		{http.MethodGet, "/api/tasks/t-1/approvals", ""},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)

		if rr.Code == http.StatusNotFound {
			t.Errorf("%s %s = 404: the route is not registered", c.method, c.path)
		}
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503 when taskStore is nil: %s",
				c.method, c.path, rr.Code, rr.Body.String())
		}
	}
}

// The ACC run-event projection keeps its own path. If the collaboration
// activity stream had been mounted on /events, this route would have been
// swallowed and the task detail page would lose its run history.
func TestRunEventsPathIsNotHijackedByActivityStream(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	if srv.taskStore != nil {
		t.Skip("test server has a real task store; route-level assertion not applicable")
	}
	req, _ := http.NewRequest(http.MethodGet, "/api/tasks/t-1/events", nil)
	req.Header.Set("Authorization", "Bearer "+tokens[""])
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusNotFound {
		t.Fatal("GET /api/tasks/{id}/events = 404: the ACC run-event route disappeared")
	}
}

func TestNormalizeParticipants(t *testing.T) {
	t.Run("defaults a missing role to assignee", func(t *testing.T) {
		got, err := normalizeParticipants([]task.Participant{{UserID: "u-1"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Role != task.RoleAssignee {
			t.Fatalf("got %+v, want one assignee", got)
		}
	})

	t.Run("trims ids and roles", func(t *testing.T) {
		got, err := normalizeParticipants([]task.Participant{{UserID: "  u-1  ", Role: "  owner  "}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0].UserID != "u-1" || got[0].Role != task.RoleOwner {
			t.Fatalf("got %+v, want trimmed owner", got[0])
		}
	})

	t.Run("a duplicate id keeps one row", func(t *testing.T) {
		// Two rows for one user would mean two notifications to the same
		// person on every event.
		got, err := normalizeParticipants([]task.Participant{
			{UserID: "u-1", Role: task.RoleAssignee},
			{UserID: "u-1", Role: task.RoleWatcher},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Role != task.RoleWatcher {
			t.Fatalf("got %+v, want a single watcher row", got)
		}
	})

	bad := []struct {
		name string
		in   []task.Participant
	}{
		{"blank user id", []task.Participant{{UserID: "   "}}},
		{"unknown role", []task.Participant{{UserID: "u-1", Role: "admin"}}},
		{"two owners", []task.Participant{
			{UserID: "u-1", Role: task.RoleOwner},
			{UserID: "u-2", Role: task.RoleOwner},
		}},
	}
	for _, c := range bad {
		if _, err := normalizeParticipants(c.in); err == nil {
			t.Errorf("%s: expected an error, got none", c.name)
		}
	}
}
