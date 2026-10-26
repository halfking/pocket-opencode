package server

// companion_history_test.go —— 任务历史 / 生成文件读面的判据。
//
// ★ 判据打在**用户能操作的那个对象**上：不是"handler 被调用了"，而是
//   手机上真的能拿到 run 的字段、真的能看到产出的文件内容。
//
// ★ 判据要能变红：fake daemon 记录自己被打了多少次，测试断言它确实被
//   命中过。否则一个"永远返回空列表"的实现也能让这个测试变绿。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeCompanion is a stand-in for agent-companion's read face.
type fakeCompanion struct {
	hits  atomic.Int64
	files map[string][]byte
}

func (f *fakeCompanion) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.hits.Add(1)
	switch {
	case r.URL.Path == "/api/v1/runs":
		writeJSON(w, http.StatusOK, map[string]any{
			"runs": []map[string]any{{
				"run_id":      "dispatch-abc",
				"dispatch_id": "dispatch-abc",
				"task_id":     "task-xyz",
				"agent_id":    "mmcode",
				"kind":        "mmcode",
				"state":       "succeeded",
				"session_id":  "sess-1",
				"updates":     3,
				"last_event":  "message_chunk",
				"retained":    true,
				"started_at":  "2026-10-27T05:44:00Z",
				"ended_at":    "2026-10-27T05:44:30Z",
				"answer":      "已完成",
			}},
			"total": 1,
		})
	case strings.HasSuffix(r.URL.Path, "/artifacts") && strings.HasPrefix(r.URL.Path, "/api/v1/runs/"):
		writeJSON(w, http.StatusOK, map[string]any{
			"artifacts": []map[string]any{{
				"tool_call_id": "tc-1",
				"tool_title":   "write file",
				"tool_kind":    "execute",
				"content":      "hello",
				"at":           "2026-10-27T05:44:20Z",
			}},
			"count":     1,
			"dropped":   0,
			"truncated": false,
		})
	case r.URL.Path == "/api/v1/artifacts/dispatch-abc":
		writeJSON(w, http.StatusOK, map[string]any{
			"dispatch_id": "dispatch-abc",
			"files": []map[string]any{{
				"name":      "output.txt",
				"size":      11,
				"sha256":    "sha256:deadbeef",
				"read_path": "/api/v1/artifacts/dispatch-abc/output.txt",
			}},
			"count":  1,
			"exists": true,
		})
	case r.URL.Path == "/api/v1/artifacts/dispatch-abc/output.txt":
		w.Header().Set("X-Artifact-Sha256", "sha256:deadbeef")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("hello world"))
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such route: " + r.URL.Path})
	}
}

func newCompanionHistoryTestServer(t *testing.T) (*Server, string, *fakeCompanion) {
	t.Helper()
	fake := &fakeCompanion{}
	upstream := httptest.NewServer(fake)
	t.Cleanup(upstream.Close)

	srv, token := newTestServerWithAuth(t)
	srv.SetCompanionClient(NewCompanionClient(upstream.URL, "test-secret"))
	return srv, token, fake
}

func TestACCRunsListsTheTaskHistory(t *testing.T) {
	srv, token, fake := newCompanionHistoryTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/acc/runs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Source string `json:"source"`
		Count  int    `json:"count"`
		Runs   []struct {
			RunID      string `json:"run_id"`
			DispatchID string `json:"dispatch_id"`
			TaskID     string `json:"task_id"`
			Kind       string `json:"kind"`
			State      string `json:"state"`
			Retained   bool   `json:"retained"`
			StartedAt  string `json:"started_at"`
			EndedAt    string `json:"ended_at"`
			Answer     string `json:"answer"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fake.hits.Load() == 0 {
		t.Fatal("the companion was never read ⇒ the assertion below would pass on an empty list")
	}
	if out.Count != 1 || len(out.Runs) != 1 {
		t.Fatalf("runs: %+v", out)
	}
	row := out.Runs[0]
	// The linkage to ACC is what makes a history row usable: without the
	// dispatch id a user cannot tie the row to the task ACC knows about.
	if row.RunID != "dispatch-abc" || row.DispatchID != "dispatch-abc" {
		t.Errorf("run/dispatch id: %+v", row)
	}
	if row.TaskID != "task-xyz" {
		t.Errorf("task_id: %q", row.TaskID)
	}
	if row.State != "succeeded" {
		t.Errorf("state: %q", row.State)
	}
	if row.StartedAt == "" || row.EndedAt == "" {
		t.Errorf("timestamps: %+v", row)
	}
	if row.Answer != "已完成" {
		t.Errorf("answer: %q", row.Answer)
	}
}

func TestACCRunArtifactsJoinsToolsAndFiles(t *testing.T) {
	srv, token, _ := newCompanionHistoryTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/acc/runs/dispatch-abc/artifacts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		RunID     string           `json:"run_id"`
		Artifacts []map[string]any `json:"artifacts"`
		Count     int              `json:"count"`
		Truncated bool             `json:"truncated"`
		Files     []struct {
			Name     string `json:"name"`
			Size     int64  `json:"size"`
			SHA256   string `json:"sha256"`
			ReadPath string `json:"read_path"`
		} `json:"files"`
		FileCount int `json:"file_count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.RunID != "dispatch-abc" {
		t.Errorf("run_id: %q", out.RunID)
	}
	if out.Count != 1 || len(out.Artifacts) != 1 {
		t.Fatalf("artifacts: %+v", out.Artifacts)
	}
	if out.Artifacts[0]["tool_call_id"] != "tc-1" {
		t.Errorf("artifact: %+v", out.Artifacts[0])
	}
	// The produced FILES are the "生成的文件" the requirement names.
	if out.FileCount != 1 || len(out.Files) != 1 {
		t.Fatalf("files: %+v", out.Files)
	}
	if out.Files[0].Name != "output.txt" {
		t.Errorf("file name: %q", out.Files[0].Name)
	}
	if out.Files[0].SHA256 == "" {
		t.Errorf("the produced file carries no digest: %+v", out.Files[0])
	}
}

func TestACCArtifactBytesAreProxied(t *testing.T) {
	srv, token, _ := newCompanionHistoryTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/acc/artifacts/dispatch-abc/output.txt", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	// The mobile http client parses JSON only, so the bytes must arrive
	// inside a JSON envelope — a bare text/plain body would make the
	// App's fetch fail to decode.
	var out struct {
		DispatchID string `json:"dispatch_id"`
		Name       string `json:"name"`
		Content    string `json:"content"`
		SHA256     string `json:"sha256"`
		Size       int    `json:"size"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if out.Content != "hello world" {
		t.Fatalf("content: %q", out.Content)
	}
	// The digest must travel with the bytes — it is what makes the
	// completion's content_hash checkable.
	if out.SHA256 != "sha256:deadbeef" {
		t.Errorf("digest: %q", out.SHA256)
	}
	if out.Name != "output.txt" || out.DispatchID != "dispatch-abc" {
		t.Errorf("identity: %+v", out)
	}
	if out.Size != len("hello world") {
		t.Errorf("size: %d", out.Size)
	}
}

// TestACCRunsWithoutCompanionIsServiceUnavailable pins the shape: a host
// with no companion wired must say so. An empty list would render as
// "this host never ran anything", which is a different claim.
func TestACCRunsWithoutCompanionIsServiceUnavailable(t *testing.T) {
	srv, token := newTestServerWithAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/acc/runs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "companion_not_configured") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// TestACCRunRoutesRequireAuth keeps the new read faces behind the same
// user auth as the rest of the mobile surface: they expose what the
// host's agents produced.
func TestACCRunRoutesRequireAuth(t *testing.T) {
	srv, _, _ := newCompanionHistoryTestServer(t)
	for _, path := range []string{"/api/acc/runs", "/api/acc/runs/x/artifacts", "/api/acc/artifacts/x/y"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d want 401", path, rr.Code)
		}
	}
}
