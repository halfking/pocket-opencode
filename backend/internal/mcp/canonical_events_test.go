package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseCanonicalTaskResult(t *testing.T) {
	got, err := ParseCanonicalTaskResult(`{"data":{"task_id":"task-1","run_id":"run-1","operation_id":"op-1","status":"accepted"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskID != "task-1" || got.RunID != "run-1" || got.OperationID != "op-1" {
		t.Fatalf("got %#v", got)
	}
}

func TestParseCanonicalTaskResultFailsClosed(t *testing.T) {
	if _, err := ParseCanonicalTaskResult(`{"task_id":"task-1"}`); err == nil {
		t.Fatal("missing run_id must fail")
	}
}

func TestListRunEventsUsesMCPRootAndCursor(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":[{"event_id":"e1","event_type":"run.created","run_id":"run-1","sequence":1}]}`))
	}))
	defer ts.Close()
	client := NewClientWithAuth(ts.URL+"/api/v2/mcp", "secret", "tenant", nil, false)
	events, err := client.ListRunEvents(t.Context(), "run-1", 7)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v2/orchestration/runs/run-1/events?after=7" {
		t.Fatalf("path=%q", gotPath)
	}
	if len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("events=%#v", events)
	}
}
