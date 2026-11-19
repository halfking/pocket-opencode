package server

// task_transfer_test.go —— POST /api/tasks/{id}/transfer 的判据。
//
// 组合事务的每一步都有判据（见 task_transfer.go 注释）：旧派单必须真的
// 被取消（fail-closed：取消失败不重派）、重派必须复用同一 ACC 任务、
// 绑定必须换到新 run/dispatch、activity 流必须记 transferred。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/acchttp"
	"github.com/halfking/pocket-opencode/backend/internal/accruntime"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// transferStub 同时扮演 ACC 的 runtime control 面（命令读/取消/派发）。
type transferStub struct {
	mu sync.Mutex
	// commands[dispatchID] 返回的命令状态（缺省 running + fencing_token=7）。
	commands map[string]accCommandState
	// cancelledBody 记录最后一次 cancel 的请求体；cancelCalls 计数。
	cancelBody   map[string]any
	cancelCalls  int
	cancelCode   int
	dispatchBody map[string]any
}

type accCommandState struct {
	status       string
	fencingToken uint64
}

func (s *transferStub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/api/v2/runtime/commands/") && strings.HasSuffix(path, "/cancel") && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.cancelBody = body
			s.cancelCalls++
			if s.cancelCode != 0 {
				w.WriteHeader(s.cancelCode)
				_, _ = w.Write([]byte(`{"error":{"code":"lease_fenced"}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"data":{"dispatch_id":"disp-old","status":"cancelling"}}`))
			return
		case strings.HasPrefix(path, "/api/v2/runtime/commands/") && r.Method == http.MethodGet:
			id := strings.TrimPrefix(path, "/api/v2/runtime/commands/")
			st, ok := s.commands[id]
			if !ok {
				st = accCommandState{status: "leased", fencingToken: 7}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"data":{"dispatch_id":"` + id + `","status":"` + st.status + `","fencing_token":` + jsonUint(st.fencingToken) + `}}`))
			return
		case path == "/api/v2/runtime/dispatch" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.dispatchBody = body
			w.WriteHeader(http.StatusAccepted)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"command_id":"cmd-new","data":{"dispatch_id":"disp-new","run_id":"run-new","task_id":"acc-task-t1","status":"created","agent_id":"opencode"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
}

func (s *transferStub) cancelDone() (int, map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelCalls, s.cancelBody
}

func jsonUint(v uint64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// newTransferServer 组一个带 task store + ACC stub 的 server（PG 门控）。
func newTransferServer(t *testing.T, stubURL string) (*Server, string, func()) {
	t.Helper()
	store, cleanup := newACCGateTaskStore(t)
	srv := &Server{}
	srv.taskStore = store
	accClient, err := accruntime.New(acchttp.Config{
		BaseURL: stubURL, APIKey: "acc-test-key", Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("accruntime.New: %v", err)
	}
	srv.SetACCRuntime(accClient)
	return srv, "ws-transfer", cleanup
}

// 判据①（PG 门控）：移效组合事务全绿 —— 旧派单取消（带 holder+token）、
// 同一 ACC 任务重派、绑定换新、activity 记 transferred。
func TestTransferTask_HappyPath(t *testing.T) {
	stub := &transferStub{}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()
	srv, ws, cleanup := newTransferServer(t, ts.URL)
	defer cleanup()
	ctx := context.Background()

	const localID, accTaskID = "task-tr-1", "acc-task-t1"
	if err := srv.taskStore.CreateTask(ctx, &task.Task{
		ID: localID, Title: "transfer probe", Description: "the body",
		Status: "active", WorkspaceID: ws, Source: "acc", OwnerID: "user-transfer",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := srv.taskStore.SetACCBinding(ctx, ws, localID, task.Binding{
		TaskID: accTaskID, RunID: "run-old", DispatchID: "disp-old",
		SourceRef: "pocketd", CorrelationID: "pocket-task-tr-1", HolderID: "holder-old",
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+localID+"/transfer",
		strings.NewReader(`{"runtime_id":"rt-new","agent_id":"opencode","reason":"move to lab"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleTransferTask(rr, req, localID)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["acc_task_id"] != accTaskID {
		t.Errorf("acc_task_id = %v, want %v (same task re-dispatched)", out["acc_task_id"], accTaskID)
	}
	if out["dispatch_id"] != "disp-new" || out["run_id"] != "run-new" {
		t.Errorf("new binding ids = %v/%v, want disp-new/run-new", out["dispatch_id"], out["run_id"])
	}
	if out["correlation_id"] != "transfer-from-"+localID {
		t.Errorf("correlation_id = %v", out["correlation_id"])
	}
	if out["transferred_from"] != "disp-old" {
		t.Errorf("transferred_from = %v, want disp-old", out["transferred_from"])
	}

	// 取消必须带 holder + 当前 fencing token（不带会被 ACC lease_fenced）。
	calls, body := stub.cancelDone()
	if calls != 1 {
		t.Fatalf("cancel calls = %d, want 1", calls)
	}
	if body["holder_id"] != "holder-old" {
		t.Errorf("cancel holder = %v, want holder-old", body["holder_id"])
	}
	if body["fencing_token"] == nil || body["fencing_token"] == 0 {
		t.Errorf("cancel fencing_token = %v, want 7 (current token)", body["fencing_token"])
	}

	// 重派必须指向同一 ACC 任务，且幂等键绑定 (task, 旧dispatch)。
	if stub.dispatchBody["task_id"] != accTaskID {
		t.Errorf("dispatch task_id = %v, want %v", stub.dispatchBody["task_id"], accTaskID)
	}
	if !strings.Contains(stub.dispatchBody["idempotency_key"].(string), "pocket-transfer-"+localID) ||
		!strings.Contains(stub.dispatchBody["idempotency_key"].(string), "disp-old") {
		t.Errorf("idempotency_key = %v, want pocket-transfer-<task>-<oldDispatch>", stub.dispatchBody["idempotency_key"])
	}

	// 绑定换新：读回任务行，run/dispatch 已是新值。
	got, err := srv.taskStore.GetTaskScoped(ctx, localID, ws)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.ACCDispatchID != "disp-new" || got.ACCRunID != "run-new" {
		t.Errorf("binding after transfer = %s/%s, want disp-new/run-new", got.ACCDispatchID, got.ACCRunID)
	}
	if got.ACCTaskID != accTaskID {
		t.Errorf("acc_task_id after transfer = %s, want unchanged %s", got.ACCTaskID, accTaskID)
	}
	if got.Status != "active" {
		t.Errorf("status after transfer = %s, want active (transfer is not cancel)", got.Status)
	}

	// activity 流记 transferred。
	events, err := srv.taskStore.ListEvents(ctx, localID, ws, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.EventType == task.EventTransferred {
			found = true
		}
	}
	if !found {
		t.Errorf("no %q event in activity stream (%d events)", task.EventTransferred, len(events))
	}
}

// 判据②：旧派单已是终态 ⇒ 跳过取消直接重派（没有还在跑的副作用可停）。
func TestTransferTask_SkipsCancelWhenTerminal(t *testing.T) {
	stub := &transferStub{commands: map[string]accCommandState{
		"disp-old": {status: "completed", fencingToken: 9},
	}}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()
	srv, ws, cleanup := newTransferServer(t, ts.URL)
	defer cleanup()
	ctx := context.Background()

	const localID = "task-tr-2"
	if err := srv.taskStore.CreateTask(ctx, &task.Task{
		ID: localID, Title: "terminal probe", Status: "active",
		WorkspaceID: ws, Source: "acc", OwnerID: "user-transfer",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := srv.taskStore.SetACCBinding(ctx, ws, localID, task.Binding{
		TaskID: "acc-task-t2", RunID: "run-old", DispatchID: "disp-old",
		SourceRef: "pocketd", HolderID: "holder-old",
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+localID+"/transfer",
		strings.NewReader(`{"runtime_id":"rt-new"}`))
	srv.handleTransferTask(rr, req, localID)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if calls, _ := stub.cancelDone(); calls != 0 {
		t.Errorf("cancel calls = %d, want 0 (already terminal)", calls)
	}
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["old_cancelled"] != false {
		t.Errorf("old_cancelled = %v, want false", out["old_cancelled"])
	}
}

// 判据③：取消失败 ⇒ fail-closed 502，**不**重派（旧派单活着时重派=双跑）。
func TestTransferTask_FailsClosedWhenCancelUnavailable(t *testing.T) {
	stub := &transferStub{cancelCode: http.StatusConflict}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()
	srv, ws, cleanup := newTransferServer(t, ts.URL)
	defer cleanup()
	ctx := context.Background()

	const localID = "task-tr-3"
	if err := srv.taskStore.CreateTask(ctx, &task.Task{
		ID: localID, Title: "fail-closed probe", Status: "active",
		WorkspaceID: ws, Source: "acc", OwnerID: "user-transfer",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := srv.taskStore.SetACCBinding(ctx, ws, localID, task.Binding{
		TaskID: "acc-task-t3", DispatchID: "disp-old",
		SourceRef: "pocketd", HolderID: "holder-old",
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+localID+"/transfer",
		strings.NewReader(`{}`))
	srv.handleTransferTask(rr, req, localID)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 body=%s", rr.Code, rr.Body.String())
	}
	var env map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env["code"] != "acc_transfer_unavailable" {
		t.Errorf("code = %v, want acc_transfer_unavailable", env["code"])
	}
	if stub.dispatchBody != nil {
		t.Error("re-dispatch must NOT happen when cancel fails (double-run guard)")
	}
}

// 判据④：非 ACC 绑定任务 ⇒ 409（没有可移的派单）。
func TestTransferTask_RejectsUnboundTask(t *testing.T) {
	stub := &transferStub{}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()
	srv, ws, cleanup := newTransferServer(t, ts.URL)
	defer cleanup()
	ctx := context.Background()

	const localID = "task-tr-4"
	if err := srv.taskStore.CreateTask(ctx, &task.Task{
		ID: localID, Title: "local-only task", Status: "active",
		WorkspaceID: ws, OwnerID: "user-transfer",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+localID+"/transfer", strings.NewReader(`{}`))
	srv.handleTransferTask(rr, req, localID)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 body=%s", rr.Code, rr.Body.String())
	}
}
