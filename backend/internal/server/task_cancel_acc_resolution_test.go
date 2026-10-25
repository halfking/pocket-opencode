package server

// task_cancel_acc_resolution_test.go — 「停止」在**生产实况**下能不能真的到达 ACC。
//
// ★ 2026-10-25（审计轮实测）：生产里任务的 acc_dispatch_id / acc_holder_id 两列
// **从来没被写过**（SetACCBinding 没有生产调用方）⇒ 取消路径永远走「未绑定 ⇒
// 仅本地取消」⇒ 本地卡片变成 cancelled，ACC 那边的派单**照跑不误**。
//
// 数据其实一直在：run 绑定里有 ACC 的 run id，ACC 的运行事件里 `dispatch.*` 带
// dispatch_id、`dispatch.leased/started` 的 actor_id 就是租约持有方。
// 本文件钉住「从事件解出派单 ⇒ 取消真的打到 ACC」这条路径。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// accEventsStub 是一份 ACC 运行事件的桩：一条 task.created（绑定所需）、
// 一条 dispatch.created、一条 dispatch.leased（带租约持有方）。
func accEventsStub(t *testing.T, runID, taskID, dispatchID, holderID string) *httptest.Server {
	t.Helper()
	events := []map[string]any{
		{"event_id": "e1", "event_type": "task.created", "tenant_id": "tenant-a",
			"run_id": runID, "task_id": taskID, "sequence": 1, "actor_type": "user", "actor_id": "pocketd"},
		{"event_id": "e2", "event_type": "dispatch.created", "tenant_id": "tenant-a",
			"run_id": runID, "task_id": taskID, "sequence": 2, "dispatch_id": dispatchID,
			"actor_type": "user", "actor_id": "pocketd"},
		{"event_id": "e3", "event_type": "dispatch.leased", "tenant_id": "tenant-a",
			"run_id": runID, "task_id": taskID, "sequence": 3, "dispatch_id": dispatchID,
			"actor_type": "agent", "actor_id": holderID, "new_state": "leased"},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"events": events})
	}))
}

// seedTaskWithRunBinding 建一个**只有 run 绑定**的任务 —— 这就是生产实况：
// acc_dispatch_id / acc_holder_id 两列是空的。
func seedTaskWithRunBinding(t *testing.T, store *task.Store, taskID, runID, accTaskID string) {
	t.Helper()
	ctx := context.Background()
	events := []task.RunProjectionEvent{{
		EventID: "e1", EventType: "task.created", TenantID: "tenant-a",
		RunID: runID, TaskID: accTaskID, Sequence: 1, Raw: []byte(`{"event_id":"e1"}`),
	}}
	if err := store.BindVerifiedRun(ctx, task.TaskRunBinding{
		WorkspaceID: "ws-a", TenantID: "tenant-a", TaskID: accTaskID, RunID: runID,
	}, "resolved dispatch", events); err != nil {
		t.Fatalf("BindVerifiedRun: %v", err)
	}
}

// 生产实况：任务只有 run 绑定 ⇒ 取消必须先从 ACC 事件里解出派单，
// 再带上当前 fencing token 去取消 —— 而且**真的**打到 ACC。
func TestCancelTask_ResolvesDispatchFromRunEventsAndCancelsAtACC(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusOK, commandFencingToken: 7}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store

	events := accEventsStub(t, "run-1", "acc-task-1", "disp-from-events", "runtime-1")
	defer events.Close()
	srv.mcpClient = mcp.NewClient(events.URL, "test-key", true)

	seedTaskWithRunBinding(t, store, "acc-task-1", "run-1", "acc-task-1")

	rr := cancelTask(srv, tokens["ws-a"], "acc-task-1", `{"reason":"stop it"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := seededStatus(t, store, "acc-task-1"); got != "cancelled" {
		t.Fatalf("local status = %q, want cancelled", got)
	}

	reqs := stub.requests()
	if len(reqs) != 2 {
		t.Fatalf("ACC calls = %+v, want 2 (GET command then POST cancel)", reqs)
	}
	if reqs[0].Method != http.MethodGet || reqs[0].Path != "/api/v2/runtime/commands/disp-from-events" {
		t.Fatalf("first ACC call = %s %s, want GET the resolved command", reqs[0].Method, reqs[0].Path)
	}
	if reqs[1].Method != http.MethodPost || reqs[1].Path != "/api/v2/runtime/commands/disp-from-events/cancel" {
		t.Fatalf("second ACC call = %s %s, want POST the cancel", reqs[1].Method, reqs[1].Path)
	}
	if reqs[1].Body["holder_id"] != "runtime-1" {
		t.Fatalf("cancel holder_id = %v, want the holder from the leased event", reqs[1].Body["holder_id"])
	}
	token, ok := reqs[1].Body["fencing_token"].(float64)
	if !ok || uint64(token) != 7 {
		t.Fatalf("cancel fencing_token = %v, want 7", reqs[1].Body["fencing_token"])
	}
}

// ACC 事件里**没有**派单（任务刚建、还没派出去）⇒ 没有东西可停 ⇒ 只能本地取消，
// 而且绝不能去碰 ACC（否则会对着一个不存在的派单发取消）。
func TestCancelTask_NoDispatchInEventsStaysLocalOnly(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusOK}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store

	events := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"events": []map[string]any{{
			"event_id": "e1", "event_type": "task.created", "tenant_id": "tenant-a",
			"run_id": "run-1", "task_id": "acc-task-2", "sequence": 1,
		}}})
	}))
	defer events.Close()
	srv.mcpClient = mcp.NewClient(events.URL, "test-key", true)

	seedTaskWithRunBinding(t, store, "acc-task-2", "run-1", "acc-task-2")

	rr := cancelTask(srv, tokens["ws-a"], "acc-task-2", `{"reason":"nothing dispatched"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := seededStatus(t, store, "acc-task-2"); got != "cancelled" {
		t.Fatalf("local status = %q, want cancelled", got)
	}
	if calls, _, _, _ := stub.snapshot(); calls != 0 {
		t.Fatalf("no dispatch in events ⇒ ACC must not be called, calls=%d", calls)
	}
}

// 纯函数判据：取**最后**一条带 dispatch_id 的事件（一次 run 可能有多次派单尝试），
// 持有方取最后一条 agent 角色事件的 actor_id。
func TestAccDispatchFromEvents_TakesLatestDispatchAndAgentActor(t *testing.T) {
	events := []mcp.AccRunEvent{
		{EventType: "task.created", ActorType: "user", ActorID: "pocketd"},
		{EventType: "dispatch.created", DispatchID: "disp-1", ActorType: "user", ActorID: "pocketd"},
		{EventType: "dispatch.leased", DispatchID: "disp-1", ActorType: "agent", ActorID: "runtime-a"},
		{EventType: "dispatch.created", DispatchID: "disp-2", ActorType: "user", ActorID: "pocketd"},
		{EventType: "dispatch.leased", DispatchID: "disp-2", ActorType: "agent", ActorID: "runtime-b"},
	}
	ref, ok := accDispatchFromEvents(events)
	if !ok {
		t.Fatal("expected a dispatch reference")
	}
	if ref.DispatchID != "disp-2" {
		t.Errorf("DispatchID = %q, want disp-2 (the latest attempt)", ref.DispatchID)
	}
	if ref.HolderID != "runtime-b" {
		t.Errorf("HolderID = %q, want runtime-b", ref.HolderID)
	}
	if _, ok := accDispatchFromEvents([]mcp.AccRunEvent{{EventType: "task.created"}}); ok {
		t.Error("a run without any dispatch event must not yield a reference")
	}
}
