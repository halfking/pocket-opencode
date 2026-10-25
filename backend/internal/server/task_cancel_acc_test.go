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
	stub := &accPermissionStub{status: http.StatusOK, commandFencingToken: 7}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store
	seedBoundTask(t, store, "task-cancel-1", &task.Binding{
		TaskID: "acc-9", DispatchID: "disp-7", SourceRef: "ref-1", CorrelationID: "corr-1", HolderID: "runtime-1",
	})

	rr := cancelTask(srv, tokens["ws-a"], "task-cancel-1", `{"reason":"mobile cancel"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := seededStatus(t, store, "task-cancel-1"); got != "cancelled" {
		t.Fatalf("local status = %q, want cancelled", got)
	}
	// ★ 取消前会先 GET 一次命令取**当前** fencing token（ACC 的 holder
	// 围栏要求它是当前那把；不带 ⇒ 409 lease_fenced，派单最后落
	// agent_busy 而不是 cancelled）。
	reqs := stub.requests()
	if len(reqs) != 2 {
		t.Fatalf("ACC calls = %d (%+v), want 2: GET command then POST cancel", len(reqs), reqs)
	}
	if reqs[0].Method != http.MethodGet || reqs[0].Path != "/api/v2/runtime/commands/disp-7" {
		t.Fatalf("first ACC call = %s %s, want GET the command", reqs[0].Method, reqs[0].Path)
	}
	if reqs[1].Method != http.MethodPost || reqs[1].Path != "/api/v2/runtime/commands/disp-7/cancel" {
		t.Fatalf("second ACC call = %s %s, want POST the cancel", reqs[1].Method, reqs[1].Path)
	}
	token, ok := reqs[1].Body["fencing_token"].(float64)
	if !ok || uint64(token) != stub.commandFencingToken {
		t.Fatalf("cancel body fencing_token = %v, want %d", reqs[1].Body["fencing_token"], stub.commandFencingToken)
	}
	if reqs[1].Body["holder_id"] != "runtime-1" {
		t.Fatalf("cancel body holder_id = %v", reqs[1].Body["holder_id"])
	}
}

func TestCancelTask_ACCFailureIsFailClosed(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusInternalServerError}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store
	seedBoundTask(t, store, "task-cancel-2", &task.Binding{
		TaskID: "acc-9", DispatchID: "disp-7", SourceRef: "ref-1", CorrelationID: "corr-1", HolderID: "runtime-1",
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
		TaskID: "acc-9", DispatchID: "disp-7", SourceRef: "ref-1", CorrelationID: "corr-1", HolderID: "runtime-1",
	})

	rr := cancelTask(srv, tokens["ws-a"], "task-cancel-4", `{}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if got := seededStatus(t, store, "task-cancel-4"); got != "open" {
		t.Fatalf("local status = %q, want unchanged open", got)
	}
}

func TestCancelTask_BoundWithoutHolderIsFailClosed(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusOK}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store
	seedBoundTask(t, store, "task-cancel-5", &task.Binding{
		TaskID: "acc-9", DispatchID: "disp-7", SourceRef: "ref-1", CorrelationID: "corr-1",
	})

	rr := cancelTask(srv, tokens["ws-a"], "task-cancel-5", `{}`)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	body := decodeStructured(t, rr)
	requireCode(t, body, "acc_cancel_unavailable")
	if body["gate_class"] != "binding_incomplete" {
		t.Fatalf("gate_class = %v", body["gate_class"])
	}
	if calls, _, _, _ := stub.snapshot(); calls != 0 {
		t.Fatalf("holder-less cancel must not reach ACC, calls=%d", calls)
	}
	if got := seededStatus(t, store, "task-cancel-5"); got != "open" {
		t.Fatalf("local status = %q, want unchanged open", got)
	}
}

// ACC 说这条命令**已经**被取消 ⇒ 操作员的意图已达成 ⇒ 不该再发一次取消
// （再发会撞上 invalid_state_transition，然后被当成「ACC 不可用」⇒ 502，
// 而任务其实早就停了）。本地仍要落 cancelled。
func TestCancelTask_AlreadyCancelledAtACCDoesNotResendCancel(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusOK, commandStatus: "cancelled"}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store
	seedBoundTask(t, store, "task-cancel-6", &task.Binding{
		TaskID: "acc-9", DispatchID: "disp-7", SourceRef: "ref-1", CorrelationID: "corr-1", HolderID: "runtime-1",
	})

	rr := cancelTask(srv, tokens["ws-a"], "task-cancel-6", `{"reason":"already stopped"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := seededStatus(t, store, "task-cancel-6"); got != "cancelled" {
		t.Fatalf("local status = %q, want cancelled", got)
	}
	reqs := stub.requests()
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet {
		t.Fatalf("ACC calls = %+v, want exactly one GET (no re-cancel)", reqs)
	}
}

// 取 token 失败（ACC 读命令 500）⇒ **不自己拍板**：照常发起取消（不带
// token），由 ACC 裁决；ACC 拒绝就 fail-closed（502，本地不动）。
func TestCancelTask_FencingTokenReadFailureStillLetsACCDecide(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusInternalServerError}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store
	seedBoundTask(t, store, "task-cancel-7", &task.Binding{
		TaskID: "acc-9", DispatchID: "disp-7", SourceRef: "ref-1", CorrelationID: "corr-1", HolderID: "runtime-1",
	})

	rr := cancelTask(srv, tokens["ws-a"], "task-cancel-7", `{"reason":"x"}`)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	body := decodeStructured(t, rr)
	requireCode(t, body, "acc_cancel_unavailable")
	if got := seededStatus(t, store, "task-cancel-7"); got != "open" {
		t.Fatalf("local status = %q, want unchanged open", got)
	}
}
