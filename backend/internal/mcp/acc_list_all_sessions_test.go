package mcp

// acc_list_all_sessions_test.go — ListAllSessions 必须**不带** task_id
// 调 acc_list_sessions。
//
// 为什么：ACC 的 canonical task 列表在本机实测是空的（/api/v2/canonical/tasks
// → tasks:[]），而 agent-companion 上报回来的会话里带着自己的 task_id
// （形如 "acs-macbook-30b:dispatch_…"）。于是「按 task_id 查会话」这条路
// 在 pocketd 里永远查不到东西 —— ACC 里有会话，pocketd 却一个也列不出来。
//
// 这条 tool 不带过滤时返回租户内全量，才是能真正列出 agent-companion
// 会话的入口。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListAllSessions_CallsToolWithoutTaskID(t *testing.T) {
	var gotTool string
	var gotArgs map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			ID     int64  `json:"id"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			w.Header().Set("mcp-session-id", "sess-test")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		case "tools/call":
			gotTool = req.Params.Name
			gotArgs = req.Params.Arguments
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"content": []map[string]string{{
					"type": "text",
					"text": `[{"id":"156b2830-98f8-4f04-9678-1ee373dea05e","task_id":"acs-macbook-30b:dispatch_b4e8f48b","agent_session_id":"ses_ece2e86ffffeY1EcUq2uB2OtVP","gw_session_id":"gw_dispatch_65dcd3c3","dispatch_id":"dispatch_65dcd3c3","input":"ReClaw code-review/start: wf-ec","output":"run failed: prompt_failed","created_at":"2026-10-12T20:32:55.469521Z"}]`,
				}}},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		}
	}))
	defer srv.Close()

	c := NewClientWithAuth(srv.URL, "secret", "tenant-a", []string{"sessions"}, false)
	sessions, err := c.ListAllSessions(t.Context(), 50)
	if err != nil {
		t.Fatalf("ListAllSessions: %v", err)
	}
	if gotTool != ToolListSessions {
		t.Errorf("tool = %q, want %q", gotTool, ToolListSessions)
	}
	if _, hasTaskID := gotArgs["task_id"]; hasTaskID {
		t.Errorf("arguments must NOT carry task_id (that filters to nothing): %+v", gotArgs)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1", len(sessions))
	}
	// 顶层字段必须被解析出来，而不是像以前那样丢成空串。
	if sessions[0].ID == "" {
		t.Error("session id is empty — ACC top-level id was dropped")
	}
	if sessions[0].AgentSessionID != "ses_ece2e86ffffeY1EcUq2uB2OtVP" {
		t.Errorf("AgentSessionID = %q", sessions[0].AgentSessionID)
	}
	if sessions[0].DispatchID != "dispatch_65dcd3c3" {
		t.Errorf("DispatchID = %q", sessions[0].DispatchID)
	}
}

// limit 超过 ACC 的硬上限（200）要被夹住，不能原样发出去。
func TestListAllSessions_ClampsLimitTo200(t *testing.T) {
	var gotArgs map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			ID     int64  `json:"id"`
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			w.Header().Set("mcp-session-id", "sess-test")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		case "tools/call":
			gotArgs = req.Params.Arguments
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"content": []map[string]string{{"type": "text", "text": `[]`}}},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		}
	}))
	defer srv.Close()

	c := NewClientWithAuth(srv.URL, "secret", "tenant-a", []string{"sessions"}, false)
	if _, err := c.ListAllSessions(t.Context(), 5000); err != nil {
		t.Fatalf("ListAllSessions: %v", err)
	}
	limit, ok := gotArgs["limit"].(float64)
	if !ok {
		t.Fatalf("limit arg missing: %+v", gotArgs)
	}
	if limit != 200 {
		t.Errorf("limit = %v, want 200 (ACC caps at 200)", limit)
	}
}
