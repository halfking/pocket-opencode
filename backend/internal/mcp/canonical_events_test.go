package mcp

import "testing"

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
