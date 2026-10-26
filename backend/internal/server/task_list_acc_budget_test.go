package server

// task_list_acc_budget_test.go —— 任务列表接口**不能被远端拖死**。
//
// ★ 缺陷原型（2026-10-27 02:23 真机夹具现场，逐字）：
//     2026/10/27 02:23:03 [WARN] writeJSON: 响应体写出失败（status=200）：
//                     write tcp 127.0.0.1:18099->127.0.0.1:58394: i/o timeout
//     2026/10/27 02:23:03 [SLOW] GET /api/tasks - 200 (41.239039542s)
//     ⇒ 客户端侧：TypeError: fetch failed / SocketError: other side closed
//
// 根因：GET /api/tasks 是**三源聚合**（ACC + OpenCode + 本地 PG），
// 而 ACC 那一源是**同步**调 MCP 的，且**没有超时预算**
// （同一个函数里 OpenCode 那一源反而有 listSessionsTimeout ⇒ 不一致）。
// ⇒ ACC 一抖，任务列表接口就卡 41 秒，响应写不出去 ⇒ **App 主屏整个打不开**。
//
// 修法：远端源一律**有预算**（accTasksListTimeout = 5s）；
// 超了就跳过该源，本地源照常返回，ACC 的数据由后台 tasksync（15s 一跳）补齐。
//
// 判据：
//  1. ACC 源挂住 ⇒ 接口**按时**返回 200（不是 41 秒后才失败）；
//  2. 本地源的数据**照常**返回（远端挂住不能把本地数据也拖没）；
//  3. 预算本身是**常量**（钉住它，别被悄悄改成 30s）。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// accHangingStub：MCP 面正常握手，但 tools/call **挂住**（模拟 ACC 超时）。
func accHangingStub(hang time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
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
			// 挂住：客户端的预算到了就该被 ctx 掐断。
			select {
			case <-time.After(hang):
			case <-r.Context().Done():
			}
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// ① 判据：ACC 源挂住 ⇒ 列表接口按时返回 200。
func TestListTasks_AccSourceHangs_DoesNotBlockTheList(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()

	ts := httptest.NewServer(accHangingStub(30 * time.Second))
	defer ts.Close()

	srv, tokens := newDelegateServer(t, store, ts.URL)
	// 先塞一条本地任务：远端挂住时它必须照常出现（判据②）。
	if err := store.CreateTask(t.Context(), &task.Task{
		ID:          "task-local-budget-1",
		Title:       "本地还在",
		Status:      "active",
		Priority:    "normal",
		WorkspaceID: "ws-a",
		Source:      "local",
		Type:        task.TypeOther,
		Visibility:  task.VisibilityPrivate,
		OwnerID:     "u-1",
		Tags:        []string{},
	}); err != nil {
		t.Fatalf("seed local task: %v", err)
	}

	start := time.Now()
	req := mobileRequest(http.MethodGet, "/api/tasks", tokens["ws-a"], "")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	elapsed := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（ACC 挂住不该把列表接口打死）；body=%s", rr.Code, rr.Body.String())
	}
	// 预算 5s + 余量。判据本身不能变成「等 41 秒也没事」。
	if elapsed > 15*time.Second {
		t.Fatalf("接口耗时 %s ⇒ 远端源没有预算（事故现场是 41.2s）", elapsed)
	}
	// ② 判据：本地源照常返回。
	var body struct {
		Tasks []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	found := false
	for _, tk := range body.Tasks {
		if tk.Title == "本地还在" {
			found = true
		}
	}
	if !found {
		t.Fatalf("本地任务没返回（远端挂住不该把本地数据也拖没）；body=%s", rr.Body.String())
	}
	t.Logf("ACC 源挂住时列表接口耗时 %s，本地源数据照常返回", elapsed)
}

// ③ 判据：预算常量本身要被钉住（别被悄悄改成 30s —— 事故就是 41s）。
func TestAccTasksListTimeoutIsBounded(t *testing.T) {
	if accTasksListTimeout <= 0 {
		t.Fatalf("accTasksListTimeout = %s，必须是正数", accTasksListTimeout)
	}
	if accTasksListTimeout > 10*time.Second {
		t.Fatalf("accTasksListTimeout = %s ⇒ 太大（客户端会在响应写出去之前超时）", accTasksListTimeout)
	}
}
