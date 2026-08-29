package executors

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

type fakeRedClaw struct {
	chatCalls   int
	searchCalls int
	chatReq     redclaw.ChatRequest
}

func (f *fakeRedClaw) Chat(req redclaw.ChatRequest) (*redclaw.ChatResponse, error) {
	f.chatCalls++
	f.chatReq = req
	return &redclaw.ChatResponse{Message: redclaw.Message{Role: "assistant", Content: "ok"}}, nil
}
func (f *fakeRedClaw) KnowledgeSearch(redclaw.KnowledgeSearchRequest) (*redclaw.KnowledgeSearchResponse, error) {
	f.searchCalls++
	return &redclaw.KnowledgeSearchResponse{}, nil
}

func TestRedClawExecutorsScopeIdentityToTask(t *testing.T) {
	client := &fakeRedClaw{}
	e := NewRedClawChatExecutor(client)
	result, err := e.Execute(context.Background(), &scheduledtask.Task{UserID: "user-a", WorkspaceID: "workspace-a", Payload: json.RawMessage(`{"messages":[{"role":"user","content":"hello"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if client.chatCalls != 1 || client.chatReq.UserID != "user-a" || len(client.chatReq.Messages) != 1 {
		t.Fatalf("unexpected request: %+v", client.chatReq)
	}
	if !json.Valid(result.Output) {
		t.Fatalf("invalid output: %s", result.Output)
	}
	ke := NewRedClawKnowledgeExecutor(client)
	if _, err := ke.Execute(context.Background(), &scheduledtask.Task{UserID: "user-a", WorkspaceID: "workspace-a", Payload: json.RawMessage(`{"query":"q"}`)}); err != nil {
		t.Fatal(err)
	}
	if client.searchCalls != 1 {
		t.Fatalf("search calls = %d", client.searchCalls)
	}
}

func TestRedClawExecutorValidation(t *testing.T) {
	e := NewRedClawChatExecutor(&fakeRedClaw{})
	for _, payload := range []string{`{}`, `{"messages":[]}`, `not-json`} {
		if _, err := e.Execute(context.Background(), &scheduledtask.Task{UserID: "u", Payload: json.RawMessage(payload)}); err == nil {
			t.Errorf("payload %s should fail", payload)
		}
	}
}

type fakeACC struct {
	tenantID string
	tool     string
	args     map[string]interface{}
}

func (f *fakeACC) TenantID() string { return f.tenantID }
func (f *fakeACC) GetRemoteTasks(context.Context, string, int) ([]mcp.ParsedTask, error) {
	return []mcp.ParsedTask{{ID: "1", Title: "task"}}, nil
}
func (f *fakeACC) CreateTask(_ context.Context, args map[string]interface{}) (string, error) {
	f.tool = mcp.ToolCreateTask
	f.args = args
	return `{"id":"1"}`, nil
}
func (f *fakeACC) ClaimTask(_ context.Context, args map[string]interface{}) (string, error) {
	f.tool = mcp.ToolTaskClaim
	f.args = args
	return "claimed", nil
}
func (f *fakeACC) CompleteTask(_ context.Context, args map[string]interface{}) (string, error) {
	f.tool = mcp.ToolTaskComplete
	f.args = args
	return "complete", nil
}
func (f *fakeACC) ReportSession(_ context.Context, args map[string]interface{}) (string, error) {
	f.tool = mcp.ToolReportSession
	f.args = args
	return "reported", nil
}

func TestACCMCPExecutorAllowlistedTools(t *testing.T) {
	client := &fakeACC{tenantID: "w"}
	e := NewACCMCPExecutor(client)
	result, err := e.Execute(context.Background(), &scheduledtask.Task{UserID: "u", WorkspaceID: "w", Payload: json.RawMessage(`{"tool":"acc_create_task","args":{"title":"hello"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if client.tool != mcp.ToolCreateTask || string(result.Output) != `{"id":"1"}` {
		t.Fatalf("tool=%s output=%s", client.tool, result.Output)
	}
	for _, tool := range []string{"tools/call", "acc_unknown", ""} {
		_, err := e.Execute(context.Background(), &scheduledtask.Task{UserID: "u", WorkspaceID: "w", Payload: json.RawMessage(`{"tool":"` + tool + `"}`)})
		if err == nil {
			t.Errorf("tool %q should fail", tool)
		}
	}
	if _, err := e.Execute(context.Background(), &scheduledtask.Task{UserID: "u", WorkspaceID: "other", Payload: json.RawMessage(`{"tool":"acc_get_tasks"}`)}); err == nil {
		t.Fatal("workspace different from configured ACC tenant should fail")
	}
}

type fakeHTTPClient struct {
	req    *http.Request
	status int
	body   string
}

func (f *fakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	f.req = req
	return &http.Response{StatusCode: f.status, Body: ioNopCloser{Reader: strings.NewReader(f.body)}, Header: make(http.Header)}, nil
}

type ioNopCloser struct{ *strings.Reader }

func (ioNopCloser) Close() error { return nil }

func TestWebhookExecutorSignsAndRejectsUnsafeTargets(t *testing.T) {
	client := &fakeHTTPClient{status: 200, body: `{"ok":true}`}
	e := NewHTTPWebhookExecutor(client, 0)
	result, err := e.Execute(context.Background(), &scheduledtask.Task{Payload: json.RawMessage(`{"url":"https://example.com/hook","hmacSecret":"secret","body":{"x":1}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if client.req.Header.Get("X-Pocket-Signature") == "" || !json.Valid(result.Output) {
		t.Fatalf("request/output invalid: %+v %s", client.req, result.Output)
	}
	for _, target := range []string{"http://127.0.0.1/hook", "http://localhost/hook", "file:///tmp/x", "https://example.com/?x=1"} {
		_, err := e.Execute(context.Background(), &scheduledtask.Task{Payload: json.RawMessage(`{"url":"` + target + `"}`)})
		if err == nil {
			t.Errorf("unsafe target %q should fail", target)
		}
	}
}

func TestWebhookExecutorNon2xxFails(t *testing.T) {
	client := &fakeHTTPClient{status: 500, body: "nope"}
	e := NewHTTPWebhookExecutor(client, 0)
	_, err := e.Execute(context.Background(), &scheduledtask.Task{Payload: json.RawMessage(`{"url":"https://example.com/hook"}`)})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("error = %v", err)
	}
}
