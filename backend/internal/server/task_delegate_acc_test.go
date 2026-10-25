package server

// task_delegate_acc_test.go —— 「委托给 ACC」这条链的判据。
//
// ★ 2026-10-25（审计轮实测）：移动端只发 {title, description, kind}，
// 而端点要求 run_id ⇒ 每一次点击都是 400。这个功能**从来没通过过**。
//
// 本文件钉住三件事：
//  1. 不带 run_id 时，端点**自己**把任务建到 ACC 并派发出去（不再 400）；
//  2. 派发请求里**必须**带 source_ref + correlation_id
//     —— 少了它们，智能体的权限门会 fail-closed（missing correlation triple），
//     任务在 ACC 侧跑不起来（实测：带了就 running，不带就 permission_denied）；
//  3. 派发成功后**本地任务行必须写进 ACC 绑定**
//     —— 这是「停止」能真的到达 ACC 的前提（SetACCBinding 此前没有生产写入者）。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/acchttp"
	"github.com/halfking/pocket-opencode/backend/internal/accruntime"
	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// accDelegateStub 同时扮演 ACC 的 MCP 面与 runtime control 面。
type accDelegateStub struct {
	mu sync.Mutex
	// dispatchBodies 记录每一次 dispatch 的请求体。
	dispatchBodies []map[string]any
	// toolCalls 记录被调用过的 MCP tool 名。
	toolCalls []string
}

func (s *accDelegateStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runtime/runtimes":
			// 一台 runtime，推上来一个可派单的智能体。
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"runtime_id":"acs-test-01","host_id":"ac-test","agent_inventory":[{"agent_id":"opencode","kind":"opencode","state":"running"}]}]}`))
			return
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runtime/dispatch":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.dispatchBodies = append(s.dispatchBodies, body)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"command_id":"cmd-1","data":{"dispatch_id":"disp-9","run_id":"run-9","task_id":"acs-test-01:acc-task-9","status":"created","agent_id":"opencode"}}`))
			return
		case r.Method == http.MethodPost:
			// MCP JSON-RPC 面
			var rpc map[string]any
			_ = json.NewDecoder(r.Body).Decode(&rpc)
			method, _ := rpc["method"].(string)
			switch method {
			case "initialize":
				w.Header().Set("mcp-session-id", "sess-1")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"stub"}}}`))
				return
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
				return
			case "tools/call":
				params, _ := rpc["params"].(map[string]any)
				name, _ := params["name"].(string)
				s.toolCalls = append(s.toolCalls, name)
				text := "{}"
				if name == "acc_create_task" {
					text = `{"id":"acc-task-9","tenant_id":"tenant-a","kind":"agent_task/opencode","title":"t","description":"d","phase":"queue"}`
				}
				payload, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": 2,
					"result": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}},
				})
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(payload)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *accDelegateStub) dispatchCalls() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, len(s.dispatchBodies))
	copy(out, s.dispatchBodies)
	return out
}

func (s *accDelegateStub) calledTools() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.toolCalls))
	copy(out, s.toolCalls)
	return out
}

// newDelegateServer 组一个只挂了 task store + ACC 两个客户端的 server。
func newDelegateServer(t *testing.T, store *task.Store, stubURL string) (*Server, map[string]string) {
	t.Helper()
	srv, _, _, tokens := newMobileRouteServer(t)
	srv.taskStore = store
	accClient, err := accruntime.New(acchttp.Config{
		BaseURL: stubURL, APIKey: "acc-test-key", Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("accruntime.New: %v", err)
	}
	srv.SetACCRuntime(accClient)
	srv.mcpClient = mcp.NewClient(stubURL, "test-key", true)
	return srv, tokens
}

// 不带 run_id（App 的真实调用形态）⇒ 必须成功，且必须真的建了 ACC 任务、
// 派了单、把绑定写进了本地任务行。
func TestDelegateTask_WithoutRunID_CreatesDispatchesAndBinds(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()

	stub := &accDelegateStub{}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()

	srv, tokens := newDelegateServer(t, store, ts.URL)

	req := mobileRequest(http.MethodPost, "/api/tasks/delegate", tokens["ws-a"],
		`{"title":"跑一件很慢的事","description":"反复 10 次","kind":"agent_task/opencode"}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	localID, _ := body["task_id"].(string)
	if localID == "" {
		t.Fatalf("response has no task_id: %s", rr.Body.String())
	}
	if got, _ := body["acc_task_id"].(string); got != "acc-task-9" {
		t.Fatalf("acc_task_id = %q, want acc-task-9", got)
	}
	if got, _ := body["dispatch_id"].(string); got != "disp-9" {
		t.Fatalf("dispatch_id = %q, want disp-9", got)
	}
	if got, _ := body["run_id"].(string); got != "run-9" {
		t.Fatalf("run_id = %q, want run-9", got)
	}

	// ACC 侧确实建了任务
	tools := stub.calledTools()
	if !containsStr(tools, "acc_create_task") {
		t.Fatalf("ACC tools called = %v, want acc_create_task", tools)
	}

	// 派发请求必须带 source_ref + correlation_id
	calls := stub.dispatchCalls()
	if len(calls) != 1 {
		t.Fatalf("dispatch calls = %d, want 1", len(calls))
	}
	d := calls[0]
	if s, _ := d["source_ref"].(string); strings.TrimSpace(s) == "" {
		t.Fatalf("dispatch body has no source_ref: %v", d)
	}
	if s, _ := d["correlation_id"].(string); strings.TrimSpace(s) == "" {
		t.Fatalf("dispatch body has no correlation_id: %v", d)
	}
	if s, _ := d["agent_id"].(string); s != "opencode" {
		t.Fatalf("dispatch agent_id = %q, want opencode", s)
	}
	if s, _ := d["runtime_id"].(string); s != "acs-test-01" {
		t.Fatalf("dispatch runtime_id = %q, want acs-test-01", s)
	}

	// ★ 本地任务行必须带绑定 —— 这是「停止」能到达 ACC 的前提。
	got, err := store.GetTaskScoped(t.Context(), localID, "ws-a")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if got.Source != "acc" {
		t.Fatalf("task source = %q, want acc", got.Source)
	}
	if got.ACCDispatchID != "disp-9" {
		t.Fatalf("task acc_dispatch_id = %q, want disp-9", got.ACCDispatchID)
	}
	if got.ACCRunID != "run-9" {
		t.Fatalf("task acc_run_id = %q, want run-9", got.ACCRunID)
	}
	if got.ACCTaskID != "acc-task-9" {
		t.Fatalf("task acc_task_id = %q, want acc-task-9", got.ACCTaskID)
	}
}

// 派发目标不可用（没有任何 runtime 推上来可派单的智能体）⇒ 明确失败，
// 绝不能「假装成功」返回一个没有 ACC 侧实体的本地任务。
func TestDelegateTask_NoDispatchTarget_FailsClosed(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v2/runtime/runtimes" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"runtime_id":"acs-test-01","agent_inventory":[{"agent_id":"opencode","state":"denied_by_policy"}]}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	srv, tokens := newDelegateServer(t, store, ts.URL)
	req := mobileRequest(http.MethodPost, "/api/tasks/delegate", tokens["ws-a"], `{"title":"没有目标的活儿"}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code == http.StatusOK {
		t.Fatalf("status = 200, want a failure; body=%s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "dispatchable") {
		t.Fatalf("body = %s, want it to mention the missing dispatch target", rr.Body.String())
	}
}

// accTaskIDFromCreate 的形状解析：ACC 返回的是 work item（id），老的
// canonical contract 用 task_id，data 信封也要认。
func TestAccTaskIDFromCreate(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"work item id", `{"id":"acc-1","phase":"queue"}`, "acc-1"},
		{"canonical task_id", `{"task_id":"acc-2"}`, "acc-2"},
		{"data envelope", `{"data":{"id":"acc-3"}}`, "acc-3"},
		{"data task_id", `{"data":{"task_id":"acc-4"}}`, "acc-4"},
		{"empty", `{}`, ""},
		{"garbage", `not json`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accTaskIDFromCreate(tc.raw); got != tc.want {
				t.Fatalf("accTaskIDFromCreate(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func containsStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
