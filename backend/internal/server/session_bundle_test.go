package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/mcp"
)

func TestClassifyLane_CurrentWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sess.jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := classifyLane(p, ""); got != "current" {
		t.Fatalf("got %q", got)
	}
}

func TestClassifyLane_HistoricalWhenMissingFileAndGwID(t *testing.T) {
	if got := classifyLane("/no/such/session.jsonl", "gw-1"); got != "historical" {
		t.Fatalf("got %q", got)
	}
}

func TestClassifyLane_RemapsHostHome(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".cursor", "x.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POCKET_DISK_HOME", dir)
	host := "/Users/someone/.cursor/x.jsonl"
	if got := classifyLane(host, ""); got != "current" {
		t.Fatalf("got %q", got)
	}
}

func TestAccSessionToRow_UsesMetadata(t *testing.T) {
	meta, _ := json.Marshal(accMeta{AgentKind: "cursor", AgentSessionID: "abc", GwSessionID: "gw"})
	row := accSessionToRow(mcp.AccSession{
		SessionID:    "acc-1",
		Input:        "hello title",
		InputTokens:  3,
		OutputTokens: 7,
		Metadata:     meta,
	})
	if row.Title != "hello title" || row.AgentKind != "cursor" || row.AgentSession != "abc" {
		t.Fatalf("%+v", row)
	}
	if row.TokensIn != 3 || row.TokensOut != 7 {
		t.Fatalf("tokens %+v", row)
	}
}

// 回归（2026-10-22 实测本机 ACC）：ACC 的 acc_list_sessions 把 id /
// agent_session_id / gw_session_id / dispatch_id 放在**顶层**，从不返回
// "metadata"。旧实现只读 metadata ⇒ agent_session_id 取不到、退到
// row.SessionID（ACC 也不返回 "session_id"，是空串）⇒ session-bundle 里
// 会话 id 是空串（实测 `{"current":[{"id":"", ...}]}`），会话根本点不开。
//
// 这里用真实抓到的 ACC 响应锁死映射。
func TestAccSessionToRow_UsesTopLevelACCFields(t *testing.T) {
	row := accSessionToRow(mcp.AccSession{
		ID:             "156b2830-98f8-4f04-9678-1ee373dea05e",
		TaskID:         "acs-macbook-30b:dispatch_b4e8f48b",
		GatewayType:    "custom-agent",
		Input:          "ReClaw code-review/start: wf-ec",
		Output:         "run failed: prompt_failed",
		AgentSessionID: "ses_ece2e86ffffeY1EcUq2uB2OtVP",
		GwSessionID:    "gw_dispatch_65dcd3c3",
		DispatchID:     "dispatch_65dcd3c3",
		AgentKind:      "pi",
		CreatedAt:      "2026-10-12T20:32:55.469521Z",
	})
	if row.ID != "156b2830-98f8-4f04-9678-1ee373dea05e" {
		t.Errorf("ID = %q, want the ACC top-level id", row.ID)
	}
	if row.AgentSession != "ses_ece2e86ffffeY1EcUq2uB2OtVP" {
		t.Errorf("AgentSession = %q, want ses_ece2e86...", row.AgentSession)
	}
	if row.GwSessionID != "gw_dispatch_65dcd3c3" {
		t.Errorf("GwSessionID = %q", row.GwSessionID)
	}
	if row.AgentKind != "pi" {
		t.Errorf("AgentKind = %q", row.AgentKind)
	}
	if row.StartedAt != "2026-10-12T20:32:55.469521Z" {
		t.Errorf("StartedAt = %q, want created_at value", row.StartedAt)
	}
	if row.Title != "ReClaw code-review/start: wf-ec" {
		t.Errorf("Title = %q", row.Title)
	}
}

// 顶层字段优先于 metadata：两者都在时以顶层为准（ACC 现版本的事实面）。
func TestAccSessionToRow_TopLevelWinsOverMetadata(t *testing.T) {
	meta, _ := json.Marshal(accMeta{AgentKind: "cursor", AgentSessionID: "meta-ses", GwSessionID: "meta-gw"})
	row := accSessionToRow(mcp.AccSession{
		ID:             "top-1",
		AgentSessionID: "top-ses",
		GwSessionID:    "top-gw",
		AgentKind:      "pi",
		Metadata:       meta,
	})
	if row.AgentSession != "top-ses" {
		t.Errorf("AgentSession = %q, want top-ses", row.AgentSession)
	}
	if row.GwSessionID != "top-gw" {
		t.Errorf("GwSessionID = %q, want top-gw", row.GwSessionID)
	}
	if row.AgentKind != "pi" {
		t.Errorf("AgentKind = %q, want pi", row.AgentKind)
	}
}

// mcpBundleStub 是 accSessionsForTask 判据用的最小 MCP 桩：
// 按 task_id 形态返回不同的会话行，并记录被查询过的 task_id。
type mcpBundleStub struct {
	mu       sync.Mutex
	queries  []string
	// rows 按 task_id 精确匹配返回；allRows 兜底扫描返回。
	rows    map[string][]mcp.AccSession
	allRows []mcp.AccSession
}

func (s *mcpBundleStub) handler() http.Handler {
	// mcp.Client 直接 POST 到 baseURL（不带子路径），所以桩必须挂在 "/"。
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc map[string]any
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		switch rpc["method"] {
		case "initialize":
			w.Header().Set("mcp-session-id", "sess-bundle")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"stub"}}}`))
			return
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/call":
			params, _ := rpc["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			taskID, _ := args["task_id"].(string)
			s.mu.Lock()
			s.queries = append(s.queries, taskID)
			limit := 50
			if v, ok := args["limit"].(float64); ok && int(v) > 0 {
				limit = int(v)
			}
			var text string
			if taskID == "" {
				rows := s.allRows
				if len(rows) > limit {
					rows = rows[:limit]
				}
				text = marshalRows(rows)
			} else {
				text = marshalRows(s.rows[taskID])
			}
			s.mu.Unlock()
			payload, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 2,
				"result": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}},
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
}

func marshalRows(rows []mcp.AccSession) string {
	if rows == nil {
		rows = []mcp.AccSession{}
	}
	b, _ := json.Marshal(rows)
	return string(b)
}

func newBundleServer(t *testing.T, stubURL, dispatchRuntime string) *Server {
	t.Helper()
	srv := &Server{}
	srv.mcpClient = mcp.NewClient(stubURL, "test-key", true)
	srv.cfg.ACCDispatchRuntimeID = dispatchRuntime
	return srv
}

// 判据①：裸 id（acc_link 形态）与前缀 id（dispatch 形态）都要查、都要出。
func TestAccSessionsForTask_MergesBareAndRuntimePrefixed(t *testing.T) {
	stub := &mcpBundleStub{rows: map[string][]mcp.AccSession{
		"acc-task-9": {
			{ID: "row-bare", TaskID: "acc-task-9", Input: "native session"},
		},
		"rt-1:acc-task-9": {
			{ID: "row-dispatch", TaskID: "rt-1:acc-task-9", Input: "dispatch session"},
		},
	}}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()
	srv := newBundleServer(t, ts.URL, "rt-1")

	rows := srv.accSessionsForTask(context.Background(), []string{"acc-task-9"})
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (bare + prefixed merged)", len(rows))
	}
	ids := map[string]bool{}
	for _, r := range rows {
		ids[r.ID] = true
	}
	if !ids["row-bare"] || !ids["row-dispatch"] {
		t.Fatalf("missing rows: %v", ids)
	}
	stub.mu.Lock()
	q := append([]string(nil), stub.queries...)
	stub.mu.Unlock()
	if len(q) != 2 || q[0] != "acc-task-9" || q[1] != "rt-1:acc-task-9" {
		t.Fatalf("queries = %v, want [acc-task-9 rt-1:acc-task-9]", q)
	}
}

// 判据②：裸/前缀都查不到时，按 ":taskID" 后缀兜底扫描全量 ——
// 覆盖 handoff 到配置外 runtime 的会话。
func TestAccSessionsForTask_SuffixScanFallback(t *testing.T) {
	stub := &mcpBundleStub{
		rows: map[string][]mcp.AccSession{},
		allRows: []mcp.AccSession{
			{ID: "row-handoff", TaskID: "runtime-211-e:acc-task-77", Input: "handoff session"},
			{ID: "row-unrelated", TaskID: "other-task", Input: "not ours"},
		},
	}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()
	srv := newBundleServer(t, ts.URL, "rt-1")

	rows := srv.accSessionsForTask(context.Background(), []string{"acc-task-77"})
	if len(rows) != 1 || rows[0].ID != "row-handoff" {
		t.Fatalf("rows = %+v, want [row-handoff]", rows)
	}
}

// 判据③：完全无关的任务不误捞 —— 兜底扫描只在后缀精确匹配时收行。
func TestAccSessionsForTask_NoFalsePositives(t *testing.T) {
	stub := &mcpBundleStub{
		allRows: []mcp.AccSession{
			{ID: "row-x", TaskID: "prefix-acc-task-88-not-suffix", Input: "decoy"},
		},
	}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()
	srv := newBundleServer(t, ts.URL, "rt-1")

	rows := srv.accSessionsForTask(context.Background(), []string{"acc-task-88"})
	if len(rows) != 0 {
		t.Fatalf("rows = %+v, want empty (no suffix match)", rows)
	}
}

// companionOpenStub 是「能打开指定 agent session」的最小 companion 桩。
type companionOpenStub struct {
	// openIDs 返回 200；其余 404。
	openIDs map[string]bool
}

func (s *companionOpenStub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/v1/native/sessions/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
			return
		}
		id := strings.TrimPrefix(r.URL.Path, prefix)
		if s.openIDs[id] {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + id + `","title":"stub title","kind":"opencode"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"native_session_not_found"}`))
	})
}

// 判据④（PLAN.md §1 语义）：ACC 行能被 companion 打开 ⇒ current，
// 即使 session_path 在 pocketd 本机不存在（dispatch 会话的正文在执行
// runtime 的 companion 里）。
func TestTaskSessionBundle_CompanionOpenableRowBecomesCurrent(t *testing.T) {
	// ACC ledger：一条带 runtime 前缀 task_id 的派单会话（session_path 空、
	// gw_session_id 有 ⇒ 按 classifyLane 落 historical）。
	accStub := &mcpBundleStub{rows: map[string][]mcp.AccSession{
		"rt-9:task-b1": {{
			ID: "row-d1", TaskID: "rt-9:task-b1", GwSessionID: "gw_x",
			AgentSessionID: "ses_live1", Input: "dispatch probe",
		}},
	}}
	accTS := httptest.NewServer(accStub.handler())
	defer accTS.Close()
	compStub := &companionOpenStub{openIDs: map[string]bool{"ses_live1": true}}
	compTS := httptest.NewServer(compStub.handler())
	defer compTS.Close()

	srv := newBundleServer(t, accTS.URL, "rt-9")
	srv.SetCompanionClients(map[string]*CompanionClient{
		"rt-9": NewCompanionClient(compTS.URL, ""),
	})

	rr := httptest.NewRecorder()
	srv.handleTaskSessionBundle(rr, httptest.NewRequest(http.MethodGet, "/api/tasks/task-b1/session-bundle", nil), "task-b1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Current    []sessionBundleRow `json:"current"`
		Historical []sessionBundleRow `json:"historical"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Current) != 1 || len(out.Historical) != 0 {
		t.Fatalf("current=%d historical=%d, want 1/0", len(out.Current), len(out.Historical))
	}
	if out.Current[0].ID != "row-d1" || out.Current[0].AgentSession != "ses_live1" {
		t.Fatalf("row = %+v", out.Current[0])
	}
}

// 判据⑤：companion 打不开（会话不在）⇒ 维持 historical，不误升。
func TestTaskSessionBundle_UnopenableRowStaysHistorical(t *testing.T) {
	accStub := &mcpBundleStub{rows: map[string][]mcp.AccSession{
		"rt-9:task-b2": {{
			ID: "row-h1", TaskID: "rt-9:task-b2", GwSessionID: "gw_y",
			AgentSessionID: "ses_gone", Input: "expired dispatch",
		}},
	}}
	accTS := httptest.NewServer(accStub.handler())
	defer accTS.Close()
	compStub := &companionOpenStub{openIDs: map[string]bool{}} // 什么都打不开
	compTS := httptest.NewServer(compStub.handler())
	defer compTS.Close()

	srv := newBundleServer(t, accTS.URL, "rt-9")
	srv.SetCompanionClients(map[string]*CompanionClient{
		"rt-9": NewCompanionClient(compTS.URL, ""),
	})

	rr := httptest.NewRecorder()
	srv.handleTaskSessionBundle(rr, httptest.NewRequest(http.MethodGet, "/api/tasks/task-b2/session-bundle", nil), "task-b2")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var out struct {
		Current    []sessionBundleRow `json:"current"`
		Historical []sessionBundleRow `json:"historical"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Current) != 0 || len(out.Historical) != 1 {
		t.Fatalf("current=%d historical=%d, want 0/1", len(out.Current), len(out.Historical))
	}
}

// 判据⑥：companionForSessionRow 的 runtime 解析 —— 前缀命中映射 → 对应
// 客户端；前缀不在映射 → 默认；裸 id → 默认。
func TestCompanionForSessionRow_Resolution(t *testing.T) {
	srv := &Server{}
	def := NewCompanionClient("http://default.test", "")
	rtA := NewCompanionClient("http://rt-a.test", "")
	srv.SetCompanionClient(def)
	srv.SetCompanionClients(map[string]*CompanionClient{"rt-a": rtA})

	if got := srv.companionForSessionRow(mcp.AccSession{TaskID: "rt-a:task-1"}); got != rtA {
		t.Error("prefixed task should resolve to rt-a client")
	}
	if got := srv.companionForSessionRow(mcp.AccSession{TaskID: "task-1"}); got != def {
		t.Error("bare task should resolve to default client")
	}
	if got := srv.companionForSessionRow(mcp.AccSession{TaskID: "rt-x:task-1"}); got != def {
		t.Error("unknown runtime should fall back to default client")
	}

	// ParseCompanionRuntimeMap 的解析判据。
	m := ParseCompanionRuntimeMap("rt-a=http://a.test, rt-b=http://b.test", "rt-a=sec-a")
	if len(m) != 2 {
		t.Fatalf("map size = %d, want 2", len(m))
	}
	if ParseCompanionRuntimeMap("", "") != nil {
		t.Error("empty spec should yield nil")
	}
}

// 判据⑦（PG 门控）：pocketd 委派任务的本地 id ≠ ACC canonical id ——
// bundle 必须经绑定解析出 acc_task_id 才能查到派单会话（2026-11-20 实测
// task-ed77f89b → 43cb68b8，不解析时 bundle 三段全空）。
func TestAccTaskCandidates_ResolvesACCBinding(t *testing.T) {
	store, cleanup := newACCGateTaskStore(t)
	defer cleanup()

	srv := &Server{taskStore: store}
	// 无绑定的任务：候选只有本地 id 本身。
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/task-local-1/session-bundle", nil)
	if got := srv.accTaskCandidates(req, "task-local-1"); len(got) != 1 || got[0] != "task-local-1" {
		t.Fatalf("candidates = %v, want [task-local-1]", got)
	}
}
