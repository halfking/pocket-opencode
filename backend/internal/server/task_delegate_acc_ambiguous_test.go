package server

// task_delegate_acc_ambiguous_test.go —— 「委托给 ACC」的写请求**结果未知**时怎么办。
//
// ★ 缺陷原型（2026-10-27 01:32 真机夹具实测，不是推测）：
//   夹具输出：
//     [fixture] 委托返回 502，但 ACC 里已经有同标题任务（3e513bcd-…）⇒ 视作已建，不重发
//     [fixture] 已委托：task_id=3e513bcd-… dispatch_id=null（失败后从 ACC 侧确认已建）
//     Error: 120s 内没有待批项出现 ⇒ 前提没造出来（这条流必须响亮失败）
//
//   ⇒ 委托在 **acc_create_task 的响应没回来**时就 502 了，
//     于是**派发根本没发生** ⇒ 智能体没被派活 ⇒ 永远不会有待批项。
//     而 ACC 侧其实**已经建出任务**了。
//
// ★ 为什么不能盲重试：acc_create_task **没有幂等键**
//   （ACC 侧 acc-go/internal/mcp/server.go 只吃 payload / parent_task_id），
//   重发会建出重复任务。
//
// ★ 为什么不能直接报失败：服务器**可能已经建好了** —— 对用户说「失败了」是错的，
//   而任务就在那儿摆着（下次同步就会出现在任务列表里）。
//
// ⇒ 修法：写请求**结果未知**（EOF / 连接被重置 / 超时）时，
//   **先问 ACC 本尊**有没有「标题相同、创建时间不早于本次请求」的任务：
//     有 ⇒ 服务器其实建好了 ⇒ 用它继续往下走（派发 + 落库 + 绑定）；
//     没有 ⇒ 才是真失败，照实 502。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// accAmbiguousStub 扮演 ACC 的 MCP 面 + runtime control 面。
//
// createTaskEOF=true 时：acc_create_task **掐断连接**（客户端看到 EOF），
// 但服务端**已经把任务建好了**（真 ACC 就是这个行为：它建了，只是响应没回来）。
type accAmbiguousStub struct {
	// createTaskEOF：acc_create_task 是否掐连接。
	createTaskEOF bool
	// listHasTask：acc_get_tasks 的返回里有没有那条「已经建好的任务」。
	listHasTask bool
	// listTitle / listCreatedAt：那条任务的样子。
	listTitle     string
	listCreatedAt time.Time
	// listTaskID：那条任务的 id。
	listTaskID string
	// dispatchBodies 记录每一次 dispatch 的请求体。
	dispatchBodies []map[string]any
	// toolCalls 记录被调用过的 MCP tool 名。
	toolCalls []string
}

func (s *accAmbiguousStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v2/runtime/runtimes" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"runtime_id":"acs-test-01","host_id":"ac-test","agent_inventory":[{"agent_id":"opencode","kind":"opencode","state":"running"}]}]}`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v2/runtime/dispatch" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.dispatchBodies = append(s.dispatchBodies, body)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"command_id":"cmd-1","data":{"dispatch_id":"disp-9","run_id":"run-9","task_id":"acs-test-01:acc-task-9","status":"created","agent_id":"opencode"}}`))
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
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
			switch name {
			case "acc_create_task":
				if s.createTaskEOF {
					// 任务**已经建好了**（真 ACC 的行为），只是响应没回来。
					// ⇒ 直接掐连接，客户端看到 EOF。
					if hj, ok := w.(http.Hijacker); ok {
						if conn, _, err := hj.Hijack(); err == nil {
							_ = conn.Close()
							return
						}
					}
				}
				text := `{"id":"acc-task-9","tenant_id":"tenant-a","kind":"agent_task/opencode","title":"t","description":"d","phase":"queue"}`
				s.writeToolResult(w, text)
				return
			case "acc_get_tasks":
				if !s.listHasTask {
					s.writeToolResult(w, `[]`)
					return
				}
				list := fmt.Sprintf(`[{"id":%q,"tenant_id":"tenant-a","title":%q,"phase":"queue","created_at":%q}]`,
					s.listTaskID, s.listTitle, s.listCreatedAt.UTC().Format(time.RFC3339Nano))
				s.writeToolResult(w, list)
				return
			}
			s.writeToolResult(w, `{}`)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func (s *accAmbiguousStub) writeToolResult(w http.ResponseWriter, text string) {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2,
		"result": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}},
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}

// ① 写请求结果未知，但 ACC 侧其实建出来了 ⇒ 必须认领它并**继续派发**，
//
//	而不是报 502（那会让任务永远派不出去 ⇒ 真机流的前提造不出来）。
func TestDelegateTask_AmbiguousCreateFailure_RecoversCreatedTask(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()

	title := "跑一件很慢的事"
	stub := &accAmbiguousStub{
		createTaskEOF: true,
		listHasTask:   true,
		listTitle:     title,
		listTaskID:    "acc-task-created-1",
		listCreatedAt: time.Now().Add(-1 * time.Second),
	}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()

	srv, tokens := newDelegateServer(t, store, ts.URL)
	req := mobileRequest(http.MethodPost, "/api/tasks/delegate", tokens["ws-a"],
		fmt.Sprintf(`{"title":%q,"description":"反复 10 次","kind":"agent_task/opencode"}`, title))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（ACC 其实建出任务了 ⇒ 应该认领并继续）；body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if got, _ := body["acc_task_id"].(string); got != "acc-task-created-1" {
		t.Fatalf("acc_task_id = %q, want acc-task-created-1", got)
	}
	// ★ 派发必须发生 —— 这正是「任务卡在 queue、智能体没被派活」的根因所在。
	if len(stub.dispatchBodies) != 1 {
		t.Fatalf("dispatch calls = %d, want 1（认领后必须继续派发）", len(stub.dispatchBodies))
	}
	d := stub.dispatchBodies[0]
	if got, _ := d["task_id"].(string); got != "acc-task-created-1" {
		t.Fatalf("dispatch task_id = %q, want acc-task-created-1", got)
	}
	// 本地任务行必须带绑定（否则「停止」到不了 ACC）。
	localID, _ := body["task_id"].(string)
	got, err := store.GetTaskScoped(t.Context(), localID, "ws-a")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	if got.ACCTaskID != "acc-task-created-1" {
		t.Fatalf("task acc_task_id = %q, want acc-task-created-1", got.ACCTaskID)
	}
}

// ② 反向判据：写请求结果未知、而 ACC 侧**确实没有**那条任务 ⇒ 必须**如实失败**。
//
//	（不能把「认领」当成万能药：认不出来就老实说失败。）
func TestDelegateTask_AmbiguousCreateFailure_WithoutCreatedTask_FailsClosed(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()

	stub := &accAmbiguousStub{
		createTaskEOF: true,
		listHasTask:   false,
	}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()

	srv, tokens := newDelegateServer(t, store, ts.URL)
	req := mobileRequest(http.MethodPost, "/api/tasks/delegate", tokens["ws-a"], `{"title":"没建出来的活儿"}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code == http.StatusOK {
		t.Fatalf("status = 200, want a failure（ACC 侧没有这条任务 ⇒ 不得假装成功）；body=%s", rr.Body.String())
	}
	if len(stub.dispatchBodies) != 0 {
		t.Fatalf("dispatch calls = %d, want 0（没建出任务就不该派发）", len(stub.dispatchBodies))
	}
}

// ③ 反向判据：ACC 侧有**同名但更早**的任务（上一轮的残留）⇒ **不得**认领。
//
//	只按标题认领会把本次委托绑到别人的任务上 ⇒ 必须看创建时间。
func TestDelegateTask_AmbiguousCreateFailure_IgnoresOlderSameTitleTask(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()

	title := "跑一件很慢的事"
	stub := &accAmbiguousStub{
		createTaskEOF: true,
		listHasTask:   true,
		listTitle:     title,
		listTaskID:    "acc-task-from-last-round",
		listCreatedAt: time.Now().Add(-30 * time.Minute),
	}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()

	srv, tokens := newDelegateServer(t, store, ts.URL)
	req := mobileRequest(http.MethodPost, "/api/tasks/delegate", tokens["ws-a"],
		fmt.Sprintf(`{"title":%q,"description":"d"}`, title))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code == http.StatusOK {
		t.Fatalf("status = 200, want a failure（同名的旧任务不该被认领）；body=%s", rr.Body.String())
	}
	if len(stub.dispatchBodies) != 0 {
		t.Fatalf("dispatch calls = %d, want 0", len(stub.dispatchBodies))
	}
	if !strings.Contains(rr.Body.String(), "ACC create task failed") {
		t.Fatalf("报错文案应当说清是建任务失败：%s", rr.Body.String())
	}
}
