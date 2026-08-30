package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContract_GetRemoteTasksAccGetTasks(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		methods = append(methods, req.Method)
		if req.Method == "initialize" {
			w.Header().Set("mcp-session-id", "sess-1")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n"))
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if req.Method != "tools/call" {
			t.Fatalf("unexpected method %q", req.Method)
		}
		params, _ := req.Params.(map[string]interface{})
		if params["name"] != "acc_get_tasks" {
			t.Fatalf("tool name = %v", params["name"])
		}
		args, _ := params["arguments"].(map[string]interface{})
		if args["status"] != "running" || args["limit"] != float64(2) {
			t.Fatalf("arguments = %+v", args)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"[running] task-1: Ship (owner: alice)\\nNo tasks found.\"}]}}\n\n"))
	}))
	defer srv.Close()

	tasks, err := NewClient(srv.URL, "api-key", false).GetRemoteTasks(context.Background(), "running", 2)
	if err != nil {
		t.Fatalf("GetRemoteTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "task-1" || tasks[0].Title != "Ship" || tasks[0].Status != "running" || tasks[0].Owner != "alice" {
		t.Fatalf("tasks = %+v", tasks)
	}
	if len(methods) != 3 || methods[0] != "initialize" || methods[1] != "notifications/initialized" || methods[2] != "tools/call" {
		t.Fatalf("MCP handshake methods = %v", methods)
	}
}

func TestContract_GetRemoteTasksRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req JSONRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "initialize" {
			w.Header().Set("mcp-session-id", "sess-err")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":1,\"error\":{\"code\":-32001,\"message\":\"denied\"}}\n\n"))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "key", false).GetRemoteTasks(context.Background(), "", 1)
	if err == nil || !strings.Contains(err.Error(), "acc_get_tasks failed") {
		t.Fatalf("expected wrapped acc_get_tasks error, got %v", err)
	}
}
