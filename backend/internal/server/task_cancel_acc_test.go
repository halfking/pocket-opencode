package server

// task_cancel_acc_test.go — POST /api/tasks/{id}/cancel 的 ACC 互锁测试。
// 复用 mobile_approval_acc_gate_test.go 的 stub/store/server 基座：
//
//	绑定 + ACC 200   → 先 CancelCommand（Idempotency-Key）→ 本地 cancelled；
//	绑定 + ACC 失败  → 502 acc_cancel_unavailable，本地状态不动（fail-closed）；
//	未绑定           → 不触 ACC，仅本地取消；
//	绑定但无 accRuntime → 503（装配错误）。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/task"
)

func cancelTask(srv *Server, token, taskID, body string) *httptest.ResponseRecorder {
	req := mobileRequest(http.MethodPost, "/api/tasks/"+taskID+"/cancel", token, body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

func seededStatus(t *testing.T, store *task.Store, taskID string) string {
	t.Helper()
	got, err := store.GetTaskScoped(context.Background(), taskID, "ws-a")
	if err != nil {
		t.Fatalf("GetTaskScoped: %v", err)
	}
	return got.Status
}

func TestCancelTask_ACCSuccessThenLocalCancelled(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusOK}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store
	seedBoundTask(t, store, "task-cancel-1", &task.Binding{
		TaskID: "acc-9", DispatchID: "disp-7", SourceRef: "ref-1", CorrelationID: "corr-1",
	})

	rr := cancelTask(srv, tokens["ws-a"], "task-cancel-1", `{"reason":"mobile cancel"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := seededStatus(t, store, "task-cancel-1"); got != "cancelled" {
		t.Fatalf("local status = %q, want cancelled", got)
	}
	calls, method, path, _ := stub.snapshot()
	if calls != 1 || method != http.MethodPost || path != "/api/v2/runtime/commands/disp-7/cancel" {
		t.Fatalf("ACC cancel call = %d %s %s", calls, method, path)
	}
}

func TestCancelTask_ACCFailureIsFailClosed(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusInternalServerError}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store
	seedBoundTask(t, store, "task-cancel-2", &task.Binding{
		TaskID: "acc-9", DispatchID: "disp-7", SourceRef: "ref-1", CorrelationID: "corr-1",
	})

	rr := cancelTask(srv, tokens["ws-a"], "task-cancel-2", `{"reason":"x"}`)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	body := decodeStructured(t, rr)
	requireCode(t, body, "acc_cancel_unavailable")
	if body["acc_dispatch_id"] != "disp-7" {
		t.Fatalf("binding audit fields missing: %v", body)
	}
	if got := seededStatus(t, store, "task-cancel-2"); got != "open" {
		t.Fatalf("local status = %q, want unchanged open", got)
	}
}

func TestCancelTask_UnboundLocalOnly(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusOK}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store
	seedBoundTask(t, store, "task-cancel-3", nil)

	rr := cancelTask(srv, tokens["ws-a"], "task-cancel-3", `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := seededStatus(t, store, "task-cancel-3"); got != "cancelled" {
		t.Fatalf("local status = %q, want cancelled", got)
	}
	if calls, _, _, _ := stub.snapshot(); calls != 0 {
		t.Fatalf("unbound task must not touch ACC, calls=%d", calls)
	}
}

func TestCancelTask_BoundWithoutRuntimeIs503(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	srv, _, _, tokens := newMobileRouteServer(t)
	srv.taskStore = store
	seedBoundTask(t, store, "task-cancel-4", &task.Binding{
		TaskID: "acc-9", DispatchID: "disp-7", SourceRef: "ref-1", CorrelationID: "corr-1",
	})

	rr := cancelTask(srv, tokens["ws-a"], "task-cancel-4", `{}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if got := seededStatus(t, store, "task-cancel-4"); got != "open" {
		t.Fatalf("local status = %q, want unchanged open", got)
	}
}
