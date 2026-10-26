package server

// ACC 派单审批面的判据。
//
// 这条面（GET /api/acc/approvals + POST /api/acc/approvals/{id}/decision）
// 存在的唯一理由：ACC 上**没有** command 级 permission 端点（2026-10-26
// 实测：GET/POST /api/v2/runtime/commands/{id}/permission 都 404），
// agent-companion 于是把待批项停在自己的干预队列里等人应答。
// 没有这层，App 里就是一个永远动不了的任务。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeCompanionServer stands in for agent-companion's /api/v1 face.
type fakeCompanionServer struct {
	items        []map[string]any
	listCalls    int
	decisionBody map[string]any
	decisionPath string
	decisionCode int
	listCode     int
}

func (f *fakeCompanionServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/interventions", func(w http.ResponseWriter, r *http.Request) {
		f.listCalls++
		if f.listCode != 0 {
			w.WriteHeader(f.listCode)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": f.items, "total": len(f.items)})
	})
	mux.HandleFunc("/api/v1/interventions/", func(w http.ResponseWriter, r *http.Request) {
		f.decisionPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&f.decisionBody)
		code := f.decisionCode
		if code == 0 {
			code = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "int-1", "status": "resolved", "decidedBy": "pocketd",
			"title": "write /tmp/x", "optionId": "once",
		})
	})
	return mux
}

// 判据1：companion 没配置 ⇒ 503。**不能**返回 200 + 空列表 ——
// 「审批面没接线」和「没有待批项」是两件事，渲染成同一个就看不出集成坏了。
func TestACCApprovalsFailsClosedWhenCompanionIsNotConfigured(t *testing.T) {
	srv := &Server{}
	rr := httptest.NewRecorder()
	srv.handleACCApprovals(rr, httptest.NewRequest(http.MethodGet, "/api/acc/approvals", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", rr.Code, rr.Body.String())
	}
}

// 判据2：GET 把 daemon 的待批项原样透出（字段名不能被吃掉 ——
// camelCase 读成 snake_case 会得到一行全零的、用户看不懂也答不了的记录）。
func TestACCApprovalsListsCompanionInterventions(t *testing.T) {
	fake := &fakeCompanionServer{items: []map[string]any{
		{
			"id": "int-abc", "runId": "run-1", "sessionId": "ses-1",
			"agentKind": "opencode", "toolCallId": "call-1",
			"title": "mkdir -p /tmp/x", "kind": "other", "status": "pending",
			"createdAt": "2026-10-26T00:41:15Z",
			"options": []map[string]any{
				{"id": "once", "title": "Allow once", "kind": "allow_once"},
				{"id": "reject", "title": "Reject", "kind": "reject_once"},
			},
		},
	}}
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()

	srv := &Server{}
	srv.SetCompanionClient(NewCompanionClient(ts.URL, "secret"))
	rr := httptest.NewRecorder()
	srv.handleACCApprovals(rr, httptest.NewRequest(http.MethodGet, "/api/acc/approvals", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Approvals []accApprovalItem `json:"approvals"`
		Count     int               `json:"count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count != 1 || len(out.Approvals) != 1 {
		t.Fatalf("approvals = %+v", out)
	}
	it := out.Approvals[0]
	if it.ID != "int-abc" || it.Title != "mkdir -p /tmp/x" || it.ToolCallID != "call-1" {
		t.Fatalf("fields lost: %+v", it)
	}
	if len(it.Options) != 2 || it.Options[0].ID != "once" {
		t.Fatalf("options lost: %+v", it.Options)
	}
}

// 判据3：POST 应答 ⇒ option_id 原样送到 daemon。
func TestACCApprovalDecisionForwardsTheOptionID(t *testing.T) {
	fake := &fakeCompanionServer{}
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()

	srv := &Server{}
	srv.SetCompanionClient(NewCompanionClient(ts.URL, "secret"))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/acc/approvals/int-abc/decision",
		strings.NewReader(`{"option_id":"once","reason":"ok"}`))
	srv.handleACCApprovalOps(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if fake.decisionBody["optionId"] != "once" {
		t.Fatalf("option id not forwarded: %+v", fake.decisionBody)
	}
	if fake.decisionPath != "/api/v1/interventions/int-abc/decision" {
		t.Fatalf("decision path = %s", fake.decisionPath)
	}
}

// 判据4：缺 option_id ⇒ 400（不把空决策发下去）。
func TestACCApprovalDecisionRequiresAnOptionID(t *testing.T) {
	fake := &fakeCompanionServer{}
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()

	srv := &Server{}
	srv.SetCompanionClient(NewCompanionClient(ts.URL, "secret"))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/acc/approvals/int-abc/decision",
		strings.NewReader(`{"reason":"no option"}`))
	srv.handleACCApprovalOps(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if fake.decisionBody != nil {
		t.Fatalf("a decision without an option must not reach the daemon: %+v", fake.decisionBody)
	}
}

// 判据5：daemon 读不到 ⇒ 502。**不能**吞成 200 空列表 —— 那会让 App
// 显示「没有待批项」，而真相是「审批面挂了」。
func TestACCApprovalsReportsUpstreamFailure(t *testing.T) {
	fake := &fakeCompanionServer{listCode: http.StatusInternalServerError}
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()

	srv := &Server{}
	srv.SetCompanionClient(NewCompanionClient(ts.URL, "secret"))
	rr := httptest.NewRecorder()
	srv.handleACCApprovals(rr, httptest.NewRequest(http.MethodGet, "/api/acc/approvals", nil))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body=%s)", rr.Code, rr.Body.String())
	}
}
