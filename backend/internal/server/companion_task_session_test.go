package server

// companion_task_session_test.go —— 「ACC 派单 → 原生会话 → 继续 / 改提示词」
// 的**路由级**测试。
//
// 判据纪律（与本轮其它守卫一致）：
//   · 判据打在**产品真会走的那个路由**上（GET /api/tasks/{id}/sessions 与
//     POST /api/tasks/{id}/sessions/{sid}/message），不是打在一个
//     "顺带被调到的 helper"上；
//   · 「链接自动挂上」必须能被观察到**持久化**（再读一次仍然只有一行），
//     否则测的是"返回体拼出来的"而不是"真的写进库了"；
//   · operate 的 kind 必须落在**查询参数**上 —— daemon 侧
//     operateNativeSession 只读 c.QueryParam("kind")，body 里的 kind 它不看。
//     ⇒ 判据要能在这个差别上变红。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// companionSessionStub 是 companion 面的 httptest 桩：只实现本测试需要的
// 三个面 —— run 历史、原生会话元信息、operate。
type companionSessionStub struct {
	mu sync.Mutex
	// runSessionID 是 run 历史里回报的 session_id。
	runSessionID string
	// runKind 是 run 历史里回报的原生智能体 kind。
	runKind string
	// resumable=false 时会话是只读观察会话。
	resumable bool
	// runCalls 统计读 run 历史的次数（幂等性观测）。
	runCalls int
	// operateCalls 记录每一次 operate 的路径（含查询参数）与 body。
	operateCalls []companionOperateCall
}

// companionOperateCall 是一次 operate 请求。
type companionOperateCall struct {
	Path string
	Kind string
	Body map[string]any
}

func (s *companionSessionStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/operate"):
			body := map[string]any{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.operateCalls = append(s.operateCalls, companionOperateCall{
				Path: r.URL.Path + "?" + r.URL.RawQuery,
				Kind: r.URL.Query().Get("kind"),
				Body: body,
			})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"runId": "run-op-1", "state": "queued",
				"agentKind": s.runKind, "nativeSessionId": s.runSessionID,
			})
		case strings.HasPrefix(r.URL.Path, "/api/v1/runs/"):
			s.runCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"run_id":      strings.TrimPrefix(r.URL.Path, "/api/v1/runs/"),
				"dispatch_id": strings.TrimPrefix(r.URL.Path, "/api/v1/runs/"),
				"session_id":  s.runSessionID,
				"kind":        s.runKind,
				"state":       "succeeded",
			})
		default:
			// GET /api/v1/native/sessions/{id}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": s.runSessionID, "kind": s.runKind, "agent": "OpenCode",
				"driverKind": s.runKind, "resumable": s.resumable, "title": "t",
			})
		}
	}
}

func (s *companionSessionStub) snapshot() (int, []companionOperateCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]companionOperateCall, len(s.operateCalls))
	copy(out, s.operateCalls)
	return s.runCalls, out
}

func newCompanionTaskSessionServer(t *testing.T, stub *companionSessionStub) (*Server, *task.Store, string) {
	t.Helper()
	store, cleanup := newACCGateTaskStore(t)
	t.Cleanup(cleanup)
	srv, _, _, tokens := newMobileRouteServer(t)
	srv.taskStore = store
	ts := httptest.NewServer(stub.handler())
	t.Cleanup(ts.Close)
	srv.SetCompanionClient(NewCompanionClient(ts.URL, ""))
	return srv, store, tokens["ws-a"]
}

// seedAccBoundTask 建一行绑了 ACC 派单的任务。
func seedAccBoundTask(t *testing.T, store *task.Store, taskID, dispatchID string) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateTask(ctx, &task.Task{
		ID: taskID, WorkspaceID: "ws-a", Title: "acc task", Status: "open",
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := store.SetACCBinding(ctx, "ws-a", taskID, task.Binding{
		TaskID: "acs-1", RunID: "run-1", DispatchID: dispatchID,
	}); err != nil {
		t.Fatalf("SetACCBinding: %v", err)
	}
}

func companionTaskRequest(method, target, token, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// listTaskSessions 调 GET /api/tasks/{id}/sessions 并解出链接行。
func listTaskSessions(t *testing.T, srv *Server, token, taskID string) []map[string]any {
	t.Helper()
	req := companionTaskRequest(http.MethodGet, "/api/tasks/"+taskID+"/sessions", token, "")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET sessions: status %d body=%q", rr.Code, rr.Body.String())
	}
	var body struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode sessions: %v body=%q", err, rr.Body.String())
	}
	return body.Sessions
}

// TestACCDispatchTask_GetsNativeSessionLinked 判据：
// 绑了 ACC 派单的任务，读会话列表 ⇒ 能拿到那次派单用的原生会话。
func TestACCDispatchTask_GetsNativeSessionLinked(t *testing.T) {
	stub := &companionSessionStub{runSessionID: "ses_acc_1", runKind: "opencode", resumable: true}
	srv, store, token := newCompanionTaskSessionServer(t, stub)
	seedAccBoundTask(t, store, "task-acc-1", "dispatch-1")

	rows := listTaskSessions(t, srv, token, "task-acc-1")
	if len(rows) != 1 {
		t.Fatalf("sessions = %d, want 1 (rows=%v)", len(rows), rows)
	}
	if got := rows[0]["sessionId"]; got != "ses_acc_1" {
		t.Fatalf("sessionId = %v, want ses_acc_1", got)
	}
	if got := rows[0]["native"]; got != true {
		t.Fatalf("native = %v, want true (原生会话没有 instance)", got)
	}
	if got := rows[0]["kind"]; got != "opencode" {
		t.Fatalf("kind = %v, want opencode", got)
	}

	// ★ 持久化：再读一次仍然只有一行，且**没有**再挂一次。
	//   只看第一次的返回体是不够的 —— 那可能只是"返回体拼出来的"。
	rows2 := listTaskSessions(t, srv, token, "task-acc-1")
	if len(rows2) != 1 {
		t.Fatalf("sessions (2nd read) = %d, want 1 (rows=%v)", len(rows2), rows2)
	}
	links, err := store.ListSessionsForTaskScoped(context.Background(), "task-acc-1", "ws-a")
	if err != nil {
		t.Fatalf("ListSessionsForTaskScoped: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("persisted links = %d, want 1", len(links))
	}
	if links[0].InstanceID != "" {
		t.Fatalf("link instance_id = %q, want empty (原生会话)", links[0].InstanceID)
	}
}

// TestTaskSessionMessage_OperatesLinkedSession 判据：
// 往已关联的原生会话投提示词 ⇒ 真的投出去了（daemon 收到 prompt 与 kind）。
func TestTaskSessionMessage_OperatesLinkedSession(t *testing.T) {
	stub := &companionSessionStub{runSessionID: "ses_acc_2", runKind: "opencode", resumable: true}
	srv, store, token := newCompanionTaskSessionServer(t, stub)
	seedAccBoundTask(t, store, "task-acc-2", "dispatch-2")
	// 先读一次 ⇒ 触发自动挂链（真实 App 也是先读列表再发）
	listTaskSessions(t, srv, token, "task-acc-2")

	req := companionTaskRequest(http.MethodPost,
		"/api/tasks/task-acc-2/sessions/ses_acc_2/message", token,
		`{"prompt":"只回复RESUMEOK"}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%q", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%q", err, rr.Body.String())
	}
	if got := body["runId"]; got != "run-op-1" {
		t.Fatalf("runId = %v, want run-op-1", got)
	}
	if got := body["prompt"]; got != "只回复RESUMEOK" {
		t.Fatalf("prompt = %v, want 只回复RESUMEOK", got)
	}

	_, calls := stub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("operate calls = %d, want 1", len(calls))
	}
	if got := calls[0].Body["prompt"]; got != "只回复RESUMEOK" {
		t.Fatalf("daemon got prompt = %v, want 只回复RESUMEOK", got)
	}
	// ★ kind 必须落在**查询参数**上：daemon 的 operateNativeSession 只读
	//   c.QueryParam("kind")，body 里的 kind 它不看。
	if calls[0].Kind != "opencode" {
		t.Fatalf("daemon got kind(query) = %q, want opencode (body-only 会被 daemon 丢掉)", calls[0].Kind)
	}
}

// TestTaskSessionMessage_RejectsUnlinkedSession 判据：
// 没挂到这个任务上的会话 ⇒ 404（否则这就是"给任意会话发消息"的后门）。
func TestTaskSessionMessage_RejectsUnlinkedSession(t *testing.T) {
	stub := &companionSessionStub{runSessionID: "ses_acc_3", runKind: "opencode", resumable: true}
	srv, store, token := newCompanionTaskSessionServer(t, stub)
	seedAccBoundTask(t, store, "task-acc-3", "dispatch-3")

	req := companionTaskRequest(http.MethodPost,
		"/api/tasks/task-acc-3/sessions/ses_someone_else/message", token,
		`{"prompt":"hello"}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 body=%q", rr.Code, rr.Body.String())
	}
	if _, calls := stub.snapshot(); len(calls) != 0 {
		t.Fatalf("daemon got %d operate calls, want 0 (未关联的会话不许被唤醒)", len(calls))
	}
}

// TestTaskSessionMessage_RejectsEmptyPrompt 判据：空提示词 ⇒ 400，且不投递。
func TestTaskSessionMessage_RejectsEmptyPrompt(t *testing.T) {
	stub := &companionSessionStub{runSessionID: "ses_acc_4", runKind: "opencode", resumable: true}
	srv, store, token := newCompanionTaskSessionServer(t, stub)
	seedAccBoundTask(t, store, "task-acc-4", "dispatch-4")
	listTaskSessions(t, srv, token, "task-acc-4")

	req := companionTaskRequest(http.MethodPost,
		"/api/tasks/task-acc-4/sessions/ses_acc_4/message", token, `{"prompt":"   "}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%q", rr.Code, rr.Body.String())
	}
	if _, calls := stub.snapshot(); len(calls) != 0 {
		t.Fatalf("daemon got %d operate calls, want 0", len(calls))
	}
}

// TestTaskSessionMessage_RejectsObserveOnlySession 判据：
// 只读观察会话（resumable=false）⇒ 409，如实说"不能投"，不是静默成功。
func TestTaskSessionMessage_RejectsObserveOnlySession(t *testing.T) {
	stub := &companionSessionStub{runSessionID: "ses_acc_5", runKind: "claude", resumable: false}
	srv, store, token := newCompanionTaskSessionServer(t, stub)
	seedAccBoundTask(t, store, "task-acc-5", "dispatch-5")
	listTaskSessions(t, srv, token, "task-acc-5")

	req := companionTaskRequest(http.MethodPost,
		"/api/tasks/task-acc-5/sessions/ses_acc_5/message", token, `{"prompt":"hi"}`)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 body=%q", rr.Code, rr.Body.String())
	}
	if _, calls := stub.snapshot(); len(calls) != 0 {
		t.Fatalf("daemon got %d operate calls, want 0", len(calls))
	}
}

// TestTaskSessionMessage_UnboundTaskHasNoSession 判据：
// 没绑 ACC 派单的任务 ⇒ 不会凭空长出链接（不编造会话）。
func TestTaskSessionMessage_UnboundTaskHasNoSession(t *testing.T) {
	stub := &companionSessionStub{runSessionID: "ses_acc_6", runKind: "opencode", resumable: true}
	srv, store, token := newCompanionTaskSessionServer(t, stub)
	ctx := context.Background()
	if err := store.CreateTask(ctx, &task.Task{
		ID: "task-plain", WorkspaceID: "ws-a", Title: "plain", Status: "open",
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	rows := listTaskSessions(t, srv, token, "task-plain")
	if len(rows) != 0 {
		t.Fatalf("sessions = %d, want 0 (未绑 ACC 派单就不该有原生会话链接)", len(rows))
	}
}
