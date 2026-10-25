package accruntime

// ecosystem_test.go — ACC 生态读取面（agents / canonical tasks / projects）：
//   - 请求路径与 Authorization 头
//   - envelope 变体解码（{"agents":[…]} / {"data":[…]} / 裸数组）
//   - 外层是对象但认不出列表 ⇒ 空切片而不是错误
//   - 列表条目按原始 JSON 透传（不丢字段）

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/acchttp"
)

func newEcoClient(t *testing.T, ts *httptest.Server) *Client {
	t.Helper()
	c, err := New(acchttp.Config{
		BaseURL:  ts.URL,
		APIKey:   testAPIKey,
		TenantID: "tenant-42",
		Timeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestListAgents_DecodesACCEnvelope(t *testing.T) {
	var gotPath, gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agents":[
			{"id":"agent-companion-host","name":"agent-companion","status":"online","type":"agent"},
			{"id":"acs-local","name":"agent-companion","status":"online"}
		]}`))
	}))
	defer ts.Close()

	agents, err := newEcoClient(t, ts).ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if gotPath != "/api/v2/agents" {
		t.Errorf("path = %q, want /api/v2/agents", gotPath)
	}
	if gotAuth == "" {
		t.Error("Authorization header missing")
	}
	if len(agents) != 2 {
		t.Fatalf("len(agents) = %d, want 2", len(agents))
	}
	if agents[0].ID != "agent-companion-host" || agents[0].Status != "online" {
		t.Errorf("agents[0] = %+v", agents[0])
	}
	if agents[0].Name != "agent-companion" {
		t.Errorf("agents[0].Name = %q, want agent-companion", agents[0].Name)
	}
}

// ACC 在别的面用 "data" 包裹列表；两种都要认。
func TestListAgents_AcceptsDataEnvelope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"a1","name":"agent-companion","status":"offline"}]}`))
	}))
	defer ts.Close()

	agents, err := newEcoClient(t, ts).ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "a1" || agents[0].Status != "offline" {
		t.Fatalf("agents = %+v", agents)
	}
}

// 外层是对象但里面没有列表 ⇒ 空注册表，不是错误。
func TestListAgents_ObjectWithoutListIsEmptyNotError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"request_id":"x"}`))
	}))
	defer ts.Close()

	agents, err := newEcoClient(t, ts).ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("len(agents) = %d, want 0", len(agents))
	}
}

func TestListAgents_BareArray(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"x1","name":"probe"}]`))
	}))
	defer ts.Close()

	agents, err := newEcoClient(t, ts).ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "x1" {
		t.Fatalf("agents = %+v", agents)
	}
}

// canonical tasks 按原始 JSON 透传：ACC 加字段时 pocketd 自动跟随，
// 且不丢任何字段（不抄一份会过期的字段表）。
func TestListCanonicalTasks_PassesRawJSONThrough(t *testing.T) {
	var gotPath, gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(`{"tasks":[
			{"id":"t1","title":"ReClaw code-review","business_state":"running","future_field":42}
		],"data":[],"page":{"limit":5}}`))
	}))
	defer ts.Close()

	tasks, err := newEcoClient(t, ts).ListCanonicalTasks(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListCanonicalTasks: %v", err)
	}
	if gotPath != "/api/v2/canonical/tasks" {
		t.Errorf("path = %q, want /api/v2/canonical/tasks", gotPath)
	}
	if gotQuery != "5" {
		t.Errorf("limit query = %q, want 5", gotQuery)
	}
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	var decoded map[string]any
	if err := json.Unmarshal(tasks[0], &decoded); err != nil {
		t.Fatalf("unmarshal raw task: %v", err)
	}
	if decoded["id"] != "t1" {
		t.Errorf("task id = %v, want t1", decoded["id"])
	}
	// 未来字段必须原样透传 —— 这是透传而非抄字段表的意义。
	if decoded["future_field"] != float64(42) {
		t.Errorf("future_field = %v, want 42 (raw passthrough lost it)", decoded["future_field"])
	}
}

func TestListCanonicalTasks_EmptyIsEmptyNotError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tasks":[],"data":[],"page":{"limit":50}}`))
	}))
	defer ts.Close()

	tasks, err := newEcoClient(t, ts).ListCanonicalTasks(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListCanonicalTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("len(tasks) = %d, want 0", len(tasks))
	}
}

func TestListProjects_DecodesEnvelope(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"projects":[{"id":"p1","name":"redclaw"}]}`))
	}))
	defer ts.Close()

	projects, err := newEcoClient(t, ts).ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if gotPath != "/api/v2/projects" {
		t.Errorf("path = %q, want /api/v2/projects", gotPath)
	}
	if len(projects) != 1 {
		t.Fatalf("len(projects) = %d, want 1", len(projects))
	}
	var decoded map[string]any
	if err := json.Unmarshal(projects[0], &decoded); err != nil {
		t.Fatalf("unmarshal raw project: %v", err)
	}
	if decoded["id"] != "p1" {
		t.Errorf("project id = %v, want p1", decoded["id"])
	}
}

// HTTP 错误必须如实冒泡，不能变成"空列表"——否则调用方会把
// 「ACC 挂了」读成「注册表里没有 agent」。
func TestListAgents_HTTPErrorIsNotEmptyList(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	if _, err := newEcoClient(t, ts).ListAgents(context.Background()); err == nil {
		t.Fatal("want error on HTTP 500, got nil")
	}
}

// ── 运行时清单面（2026-10-25 实测新增）───────────────────────────────
//
// ACC 的**注册表**（/api/v2/agents）在 acc-go 容器重启后是空的，而运行时清单
// （/api/v2/runtime/runtimes/{id}/agents，由 ACC 回连 daemon 读面）是满的 ——
// 只读注册表会得出「一个智能体都没有」的结论。

// 运行时清单读到的条目是**契约形状**（agent_id/kind/status），必须折进
// 注册表形状（id/type/status），否则下游拿到的 type/status 整片为空。
func TestListRuntimeAgents_NormalizesContractShape(t *testing.T) {
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/runtime/runtimes":
			_, _ = w.Write([]byte(`{"data":[{"runtime_id":"acs-ac30-01"}]}`))
		case "/api/v2/runtime/runtimes/acs-ac30-01/agents":
			_, _ = w.Write([]byte(`{"agents":[
				{"agent_id":"mmcode-81930","kind":"mmcode","status":"running",
				 "communication_mode":"runtime_observe_only","pid":81930,
				 "last_active_at":"2026-10-25T10:17:56+08:00",
				 "capabilities":["ide","agent_runtime"]}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	agents, err := newEcoClient(t, ts).ListRuntimeAgents(context.Background())
	if err != nil {
		t.Fatalf("ListRuntimeAgents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("len(agents) = %d, want 1 (%v)", len(agents), paths)
	}
	a := agents[0]
	if a.ID != "mmcode-81930" {
		t.Errorf("ID = %q, want mmcode-81930", a.ID)
	}
	if a.Type != "mmcode" {
		t.Errorf("Type = %q, want mmcode", a.Type)
	}
	if a.Status != "running" {
		t.Errorf("Status = %q, want running", a.Status)
	}
	if a.LastSeenAt != "2026-10-25T10:17:56+08:00" {
		t.Errorf("LastSeenAt = %q", a.LastSeenAt)
	}
	if len(a.Capabilities) != 2 {
		t.Errorf("Capabilities = %v", a.Capabilities)
	}
}

// 一个 runtime 的读面不通（没广告 api_endpoint / 机器下线）**不能**把整张表
// 清空：其它 runtime 的智能体仍然要列得出来。
func TestListRuntimeAgents_SkipsUnreachableRuntime(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/runtime/runtimes":
			_, _ = w.Write([]byte(`{"data":[
				{"runtime_id":"acs-ac30-01"},{"runtime_id":"acs-offline-30"}
			]}`))
		case "/api/v2/runtime/runtimes/acs-ac30-01/agents":
			_, _ = w.Write([]byte(`{"agents":[{"agent_id":"opencode","kind":"opencode","status":"idle"}]}`))
		default:
			http.Error(w, "runtime_read_face_unreachable", http.StatusBadGateway)
		}
	}))
	defer ts.Close()

	agents, err := newEcoClient(t, ts).ListRuntimeAgents(context.Background())
	if err != nil {
		t.Fatalf("ListRuntimeAgents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("len(agents) = %d, want 1 (the reachable runtime only)", len(agents))
	}
	if agents[0].ID != "opencode" {
		t.Errorf("ID = %q, want opencode", agents[0].ID)
	}
}
