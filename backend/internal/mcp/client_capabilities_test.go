package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Capabilities 必须与实际可调用的 tool 集合同源。T1.2 双向化之后 pocketd
// 除 acc_get_tasks 还会调 ACC 的写 tool，因此 Write=true 且 Tools 必须列全
// ——这是「声明与实现不漂移」的护栏。
func TestCapabilities_AccBidirectional(t *testing.T) {
	c := NewClient("http://example.test", "k", false)
	caps := c.Capabilities()
	if caps.Connector != "acc" {
		t.Fatalf("expected acc connector, got %q", caps.Connector)
	}
	if !caps.Read {
		t.Fatalf("Read must be true")
	}
	if !caps.Write {
		t.Fatalf("Write must be true: T1.2 enables the ACC write tools")
	}
	if len(caps.Tools) == 0 || caps.Tools[0] != ToolGetTasks {
		t.Fatalf("Tools must start with %s, got %+v", ToolGetTasks, caps.Tools)
	}
	for _, want := range writeTools {
		found := false
		for _, got := range caps.Tools {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Tools must include write tool %s, got %+v", want, caps.Tools)
		}
	}
}

func TestCapabilities_NilClientDoesNotPanic(t *testing.T) {
	var c *Client
	caps := c.Capabilities()
	if caps.Read || caps.Write {
		t.Fatalf("nil client must report zero capabilities, got %+v", caps)
	}
	if caps.Connector != "" {
		t.Fatalf("nil client must not declare connector, got %q", caps.Connector)
	}
}

// newFakeACC 起一个最小 MCP over HTTP 服务：initialize 返回 mcp-session-id，
// tools/call 回显被调用的 tool 名。用于验证写方法确实打到了正确的 tool。
func newFakeACC(t *testing.T, result func(toolName string, args map[string]interface{}) map[string]interface{}) (*httptest.Server, *[]string) {
	t.Helper()
	called := make([]string, 0, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			ID     int64  `json:"id"`
			Params struct {
				Name      string                 `json:"name"`
				Arguments map[string]interface{} `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			w.Header().Set("mcp-session-id", "sess-test")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req.ID, "result": map[string]interface{}{},
			})
		case "tools/call":
			called = append(called, req.Params.Name)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req.ID,
				"result": result(req.Params.Name, req.Params.Arguments),
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": req.ID, "result": map[string]interface{}{},
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &called
}

func okResult(_ string, _ map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]string{{"type": "text", "text": `{"id":"wi-1"}`}},
	}
}

// 四个写方法必须各自打到 ACC 已注册的对应 tool 名，且把 args 原样透传。
func TestWriteTools_CallExpectedToolNames(t *testing.T) {
	srv, called := newFakeACC(t, okResult)
	c := NewClient(srv.URL, "token", false)
	ctx := context.Background()

	if _, err := c.CreateTask(ctx, map[string]interface{}{"title": "t"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := c.ClaimTask(ctx, map[string]interface{}{"task_id": "wi-1"}); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if _, err := c.CompleteTask(ctx, map[string]interface{}{"task_id": "wi-1"}); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	if _, err := c.ReportSession(ctx, nil); err != nil {
		t.Fatalf("ReportSession: %v", err)
	}

	want := []string{ToolCreateTask, ToolTaskClaim, ToolTaskComplete, ToolReportSession}
	if strings.Join(*called, ",") != strings.Join(want, ",") {
		t.Fatalf("expected tool calls %v, got %v", want, *called)
	}
}

// ACC 的 tool 级错误走 result.isError；写路径必须转成 Go error，
// 否则 "forbidden: scope tasks required" 会被当成成功返回值。
func TestWriteTools_ToolErrorBecomesGoError(t *testing.T) {
	srv, _ := newFakeACC(t, func(string, map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{
			"isError": true,
			"content": []map[string]string{{"type": "text", "text": "forbidden: scope tasks required"}},
		}
	})
	c := NewClient(srv.URL, "token", false)

	_, err := c.CreateTask(context.Background(), map[string]interface{}{"title": "t"})
	if err == nil {
		t.Fatalf("expected error when ACC returns isError=true")
	}
	if !strings.Contains(err.Error(), "forbidden") || !strings.Contains(err.Error(), ToolCreateTask) {
		t.Fatalf("error must carry tool name and ACC message, got %v", err)
	}
}

// nil client 上调写方法必须返回错误而不是 panic（mcpClient 在未配置 ACC 时为 nil）。
func TestWriteTools_NilClientReturnsError(t *testing.T) {
	var c *Client
	if _, err := c.CreateTask(context.Background(), nil); err == nil {
		t.Fatalf("nil client must return an error")
	}
}
