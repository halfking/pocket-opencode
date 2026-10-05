package server

// mobile_approval_acc_gate_test.go — Pocket↔ACC 审批互锁（fail-closed）的
// production 路由级测试。
//
// 契约（与 agent-companion ErrPermissionGateUnavailable 一致）：
//   - 绑定 task（acc_dispatch_id 非空）的权限回复必须先命中 ACC permission
//     endpoint；ACC 404/501/5xx/网络失败 → 本地 reject + 502
//     acc_gate_unavailable；
//   - 绑定查询失败（taskStore 缺失等）→ 同样 fail-closed；
//   - 未绑定 task → 原行为完全不变（不触 ACC）；
//   - ACC 成功 → 继续原本地转发。
//
// taskStore 为具体类型 *task.Store，绑定数据需要真实 PG；沿用仓库既有
// POCKET_TEST_POSTGRES_DSN 约定，无 DSN 时跳过（audit_pg_test.go 同款）。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/acchttp"
	"github.com/halfking/pocket-opencode/backend/internal/accruntime"
	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
	"github.com/halfking/pocket-opencode/backend/internal/task"
	"github.com/jackc/pgx/v5/pgxpool"
)

// accPermissionStub 是 ACC /api/v2/runtime/commands/{id}/permission 的
// httptest 桩：按 status 回应并记录最后一次调用。
type accPermissionStub struct {
	mu         sync.Mutex
	status     int
	calls      int
	lastMethod string
	lastPath   string
	lastBody   map[string]any
}

func (s *accPermissionStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.calls++
		s.lastMethod = r.Method
		s.lastPath = r.URL.Path
		s.lastBody = nil
		_ = json.NewDecoder(r.Body).Decode(&s.lastBody)
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

func (s *accPermissionStub) snapshot() (calls int, method, path string, body map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.lastMethod, s.lastPath, s.lastBody
}

// accGateAdapter 在 mobileRouteAdapter 之上补齐 PermissionCaller /
// QuestionCaller，使 ReplyForWorkspace 走到 hasPending（not pending → 409）
// 而不是 adapter 能力断言失败。用于证明 gate 放行后到达了本地转发阶段。
type accGateAdapter struct {
	*mobileRouteAdapter
}

func (a *accGateAdapter) GetPermissionRequests(context.Context, string, string) ([]adapter.PermissionRequest, error) {
	return nil, nil
}
func (a *accGateAdapter) ReplyPermission(context.Context, string, string, string, adapter.PermissionReply, string) error {
	return nil
}
func (a *accGateAdapter) GetQuestionRequests(context.Context, string, string) ([]adapter.QuestionRequest, error) {
	return nil, nil
}
func (a *accGateAdapter) ReplyQuestion(context.Context, string, string, string, []adapter.QuestionAnswer) error {
	return nil
}
func (a *accGateAdapter) RejectQuestion(context.Context, string, string, string) error {
	return nil
}

// newACCGateTaskStore 建一个隔离 schema 的任务 store（无 DSN 跳过）。
func newACCGateTaskStore(t *testing.T) (*task.Store, func()) {
	t.Helper()
	// 只认测试专用 DSN。**不要**加「为空时回退读 POCKET_POSTGRES_DSN」——
	// 那是服务自己的生产连接串，而本文件建随机 schema 并 CREATE TABLE，
	// 回退之后会在**生产库**上建表。本文件第 15 行的注释与下面这行 Skip 文案
	// 一直写的是「只认 POCKET_TEST_POSTGRES_DSN」，是那个循环与它们自相矛盾。
	// 同一处毛病 2026-10-06 在 internal/flashcards/seed_pg_test.go 犯过一次，
	// 被 TestPGTestsNeverTargetTheProductionSchema 规则 1 判红（该规则无豁免出口）。
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping mobile approval ACC gate PG integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		rootPool.Close()
		t.Fatalf("rand: %v", err)
	}
	schema := "acc_gate_test_" + hex.EncodeToString(suffix)
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
		t.Fatalf("scoped pool: %v", err)
	}
	store, err := task.NewStore(pool)
	if err != nil {
		pool.Close()
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
		t.Fatalf("task.NewStore: %v", err)
	}
	return store, func() {
		pool.Close()
		_, _ = rootPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
	}
}

// newACCGateServer 复用 newMobileRouteServer 的注册表/token 装配，替换
// adapter 与 permMgr/quesMgr（PermissionCaller 能力桩），并注入指向 stub
// 的 accRuntime。
func newACCGateServer(t *testing.T, stub *accPermissionStub) (*Server, *accGateAdapter, map[string]string) {
	t.Helper()
	srv, _, _, tokens := newMobileRouteServer(t)
	ad := &accGateAdapter{mobileRouteAdapter: &mobileRouteAdapter{}}
	srv.opencode = ad
	srv.permMgr = opencode.NewPermissionManager(srv.registry, ad, opencode.PermissionManagerOptions{PollInterval: time.Hour}, nil)
	srv.quesMgr = opencode.NewQuestionManager(srv.registry, ad, opencode.QuestionManagerOptions{PollInterval: time.Hour}, nil)
	accClient, err := accruntime.New(acchttp.Config{
		BaseURL: stubServerURL(t, stub),
		APIKey:  "acc-test-key",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("accruntime.New: %v", err)
	}
	srv.SetACCRuntime(accClient)
	return srv, ad, tokens
}

// stubServerURL 包一层 httptest.Server 生命周期管理。
func stubServerURL(t *testing.T, stub *accPermissionStub) string {
	t.Helper()
	ts := httptest.NewServer(stub.handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

// seedBoundTask 建立 task + session link（owned-a/sess-1）+ 可选 ACC 绑定。
func seedBoundTask(t *testing.T, store *task.Store, taskID string, binding *task.Binding) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateTask(ctx, &task.Task{ID: taskID, WorkspaceID: "ws-a", Title: "gate", Status: "open"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := store.AttachSessionScoped(ctx, task.SessionLink{
		TaskID: taskID, InstanceID: "owned-a", SessionID: "sess-1", Role: "primary",
	}, "ws-a"); err != nil {
		t.Fatalf("AttachSessionScoped: %v", err)
	}
	if binding != nil {
		if err := store.SetACCBinding(ctx, "ws-a", taskID, *binding); err != nil {
			t.Fatalf("SetACCBinding: %v", err)
		}
	}
}

func postPermissionReply(srv *Server, token, body string) *httptest.ResponseRecorder {
	req := mobileRequest(http.MethodPost, "/api/mobile/approvals/permission/per-1/reply", token, body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

func decodeStructured(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%q", err, rr.Body.String())
	}
	return body
}

func requireCode(t *testing.T, body map[string]any, want string) {
	t.Helper()
	if got, _ := body["code"].(string); got != want {
		t.Fatalf("code = %q, want %q (body=%v)", got, want, body)
	}
}

func TestReplyMobilePermission_ACCGateFailClosedOn404(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusNotFound}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store
	srv.auditStore = redclaw.NewAuditStore()
	seedBoundTask(t, store, "task-gate-1", &task.Binding{
		TaskID: "acc-9", DispatchID: "disp-7", SourceRef: "ref-1", CorrelationID: "corr-1",
	})

	rr := postPermissionReply(srv, tokens["ws-a"],
		`{"instance_id":"owned-a","session_id":"sess-1","decision":"once"}`)

	// ACC gate 不可用 → 502 acc_gate_unavailable，绝不放行本地转发。
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	body := decodeStructured(t, rr)
	requireCode(t, body, "acc_gate_unavailable")
	if body["acc_dispatch_id"] != "disp-7" || body["acc_task_id"] != "acc-9" {
		t.Fatalf("binding audit fields missing: %v", body)
	}

	// ACC 必须收到 triple + decision 映射（once → allow=true, option_id=once）。
	calls, method, path, stubBody := stub.snapshot()
	if calls != 1 || method != http.MethodPost || path != "/api/v2/runtime/commands/disp-7/permission" {
		t.Fatalf("ACC call = %d %s %s", calls, method, path)
	}
	if stubBody["tool_call_id"] != "per-1" || stubBody["source_ref"] != "ref-1" || stubBody["correlation_id"] != "corr-1" {
		t.Fatalf("triple not forwarded: %v", stubBody)
	}
	dec, _ := stubBody["decision"].(map[string]any)
	if dec == nil || dec["option_id"] != "once" || dec["allow"] != true {
		t.Fatalf("decision mapping wrong: %v", dec)
	}

	// 审计必须有一条 success=false 的 acc_gate_unavailable。
	entries, err := srv.auditStore.Query(redclaw.AuditQuery{Action: "mobile.approval.acc_gate_unavailable"})
	if err != nil || len(entries) != 1 {
		t.Fatalf("acc_gate_unavailable audit entries = %d err=%v", len(entries), err)
	}
	if entries[0].Success {
		t.Fatal("fail-closed audit must be success=false")
	}
	if entries[0].TenantID != "ws-a" {
		t.Fatalf("audit tenant = %q, want ws-a", entries[0].TenantID)
	}
}

func TestReplyMobilePermission_ACCGateFailClosedOn501And500(t *testing.T) {
	for _, tc := range []struct {
		status    int
		wantClass string
	}{
		{http.StatusNotImplemented, "gate_unavailable"},
		{http.StatusInternalServerError, "acc_error"},
	} {
		store, cleanup := newACCGateTaskStore(t)
		stub := &accPermissionStub{status: tc.status}
		srv, _, tokens := newACCGateServer(t, stub)
		srv.taskStore = store
		srv.auditStore = redclaw.NewAuditStore()
		seedBoundTask(t, store, "task-gate-501", &task.Binding{
			DispatchID: "disp-x", SourceRef: "r", CorrelationID: "c",
		})

		rr := postPermissionReply(srv, tokens["ws-a"],
			`{"instance_id":"owned-a","session_id":"sess-1","decision":"always"}`)
		if rr.Code != http.StatusBadGateway {
			t.Fatalf("HTTP %d: status = %d, want 502", tc.status, rr.Code)
		}
		body := decodeStructured(t, rr)
		requireCode(t, body, "acc_gate_unavailable")
		if body["gate_class"] != tc.wantClass {
			t.Fatalf("HTTP %d: gate_class = %v, want %s", tc.status, body["gate_class"], tc.wantClass)
		}
		cleanup()
	}
}

func TestReplyMobilePermission_ACCGateSuccessContinuesLocalForward(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusOK}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store
	srv.auditStore = redclaw.NewAuditStore()
	seedBoundTask(t, store, "task-gate-ok", &task.Binding{
		DispatchID: "disp-ok", SourceRef: "r", CorrelationID: "c",
	})

	rr := postPermissionReply(srv, tokens["ws-a"],
		`{"instance_id":"owned-a","session_id":"sess-1","decision":"once"}`)

	// gate 放行 → handler 到达本地转发；请求未在 permMgr pending →
	// 409 approval_expired（而非 502 acc_gate_unavailable）。
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (local forward attempted); body=%s", rr.Code, rr.Body.String())
	}
	requireCode(t, decodeStructured(t, rr), CodeApprovalExpired)
	if calls, _, _, _ := stub.snapshot(); calls != 1 {
		t.Fatalf("ACC must have answered once, got %d", calls)
	}
	if entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "mobile.approval.acc_gate_answered"}); len(entries) != 1 {
		t.Fatalf("acc_gate_answered audit missing: %d", len(entries))
	}
}

func TestReplyMobilePermission_RejectDecisionMapsToAllowFalse(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusOK}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store
	seedBoundTask(t, store, "task-gate-rej", &task.Binding{
		DispatchID: "disp-r", SourceRef: "r", CorrelationID: "c",
	})

	rr := postPermissionReply(srv, tokens["ws-a"],
		`{"instance_id":"owned-a","session_id":"sess-1","decision":"reject","message":"no"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	_, _, _, body := stub.snapshot()
	dec, _ := body["decision"].(map[string]any)
	if dec == nil || dec["allow"] != false || dec["option_id"] != "reject" {
		t.Fatalf("reject must map to allow=false option_id=reject: %v", dec)
	}
}

func TestReplyMobilePermission_UnboundTaskKeepsOriginalBehavior(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	stub := &accPermissionStub{status: http.StatusOK}
	srv, _, tokens := newACCGateServer(t, stub)
	srv.taskStore = store
	srv.auditStore = redclaw.NewAuditStore()
	// 有 task + session link，但没有任何 ACC 绑定。
	seedBoundTask(t, store, "task-unbound", nil)

	rr := postPermissionReply(srv, tokens["ws-a"],
		`{"instance_id":"owned-a","session_id":"sess-1","decision":"once"}`)

	// 原行为：不触 ACC，直接走本地转发（此处因 not pending → 409）。
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if calls, _, _, _ := stub.snapshot(); calls != 0 {
		t.Fatalf("unbound task must not touch ACC, calls=%d", calls)
	}
	if entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "mobile.approval.acc_gate_unavailable"}); len(entries) != 0 {
		t.Fatalf("unbound task must not log gate failures: %v", entries)
	}
}

func TestReplyMobilePermission_BindingLookupFailureFailsClosed(t *testing.T) {
	stub := &accPermissionStub{status: http.StatusOK}
	srv, _, tokens := newACCGateServer(t, stub)
	// accRuntime 已配置但 taskStore 缺失 = 无法判定绑定 → fail-closed。
	srv.taskStore = nil
	srv.auditStore = redclaw.NewAuditStore()

	rr := postPermissionReply(srv, tokens["ws-a"],
		`{"instance_id":"owned-a","session_id":"sess-1","decision":"once"}`)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	body := decodeStructured(t, rr)
	requireCode(t, body, "acc_gate_unavailable")
	if body["gate_class"] != "binding_lookup_failed" {
		t.Fatalf("gate_class = %v, want binding_lookup_failed", body["gate_class"])
	}
	if calls, _, _, _ := stub.snapshot(); calls != 0 {
		t.Fatalf("lookup failure must not reach ACC, calls=%d", calls)
	}
}

func TestReplyMobilePermission_AccRuntimeNilKeepsOriginalBehavior(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()
	srv, _, _, tokens := newMobileRouteServer(t)
	ad := &accGateAdapter{mobileRouteAdapter: &mobileRouteAdapter{}}
	srv.opencode = ad
	srv.permMgr = opencode.NewPermissionManager(srv.registry, ad, opencode.PermissionManagerOptions{PollInterval: time.Hour}, nil)
	srv.quesMgr = opencode.NewQuestionManager(srv.registry, ad, opencode.QuestionManagerOptions{PollInterval: time.Hour}, nil)
	// srv.accRuntime 保持 nil：未配置 ACC 集成。
	srv.taskStore = store
	seedBoundTask(t, store, "task-noacc", &task.Binding{DispatchID: "disp-1", SourceRef: "r", CorrelationID: "c"})

	rr := postPermissionReply(srv, tokens["ws-a"],
		`{"instance_id":"owned-a","session_id":"sess-1","decision":"once"}`)

	// accRuntime 未配置 → 绑定存在也不走 gate，原行为。
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}
