package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

// 会议实时分析（/api/meetings/{id}/analyze）的行为锁定（2026-10-07 轮）：
//   - 滚动增量：第二次调用只喂新增片段，网关收到 prior_summary；
//   - 无新增：unchanged=true 快返，零 LLM 调用；
//   - reset：游标清零重析；
//   - workspace 隔离：B 工作区 404；不写会议状态（summarize 的正式纪要
//     不被实时分析覆盖）。
func TestMeetingLiveAnalyzeRolling(t *testing.T) {
	// stub 网关：/v1/audio/analyze 回显收到的请求供断言。
	var calls atomic.Int32
	var lastReq llmgateway.AnalyzeRequest
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/analyze" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		_ = json.NewDecoder(r.Body).Decode(&lastReq)
		_, _ = w.Write([]byte(`{"summary":"合并后的全文摘要","key_points":["k"],"decisions":[],"action_items":[{"text":"出清单","owner":"张三"}],"open_questions":[],"hints":["追问排班时间"],"topics":["预算"]}`))
	}))
	t.Cleanup(gw.Close)

	srv, tokens := newWorkspaceIsolationServer(t)
	h := srv.Handler()
	t.Cleanup(func() { meetingAnalyzeGateway = nil })
	meetingAnalyzeGateway = func(_ *Server, _, _ string) (*llmgateway.Client, bool) {
		return llmgateway.NewClient(gw.URL, "sk-test"), true
	}

	m, err := srv.meetingStore.CreateScoped(meeting.CreateMeetingRequest{Title: "live analyze"}, "shared-user", "ws-a")
	if err != nil {
		t.Fatalf("create meeting: %v", err)
	}
	m.Transcript = "张三: 讨论二季度预算"
	if err := srv.meetingStore.UpdateScoped(m, "shared-user", "ws-a"); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}

	// 第一轮：全量。
	rr := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+m.ID+"/analyze", tokens["ws-a"], `{"style":"meeting"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("first analyze status=%d body=%s", rr.Code, rr.Body.String())
	}
	var first struct {
		Unchanged       bool                     `json:"unchanged"`
		TranscriptRunes int                      `json:"transcript_runes"`
		Analyze         llmgateway.AnalyzeResult `json:"analyze"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if first.Unchanged || first.TranscriptRunes == 0 || first.Analyze.Summary == "" {
		t.Fatalf("first = %+v", first)
	}
	if lastReq.Transcript != "张三: 讨论二季度预算" || lastReq.PriorSummary != "" {
		t.Fatalf("first req = %+v", lastReq)
	}

	// 追加转写 → 第二轮只喂新增，prior_summary 用上轮结果。
	m.Transcript += "\n李四: 排班系统下周定"
	if err := srv.meetingStore.UpdateScoped(m, "shared-user", "ws-a"); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	rr = serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+m.ID+"/analyze", tokens["ws-a"], `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("second analyze status=%d body=%s", rr.Code, rr.Body.String())
	}
	if lastReq.PriorSummary != "合并后的全文摘要" {
		t.Fatalf("rolling prior summary not carried: %+v", lastReq)
	}
	if !strings.Contains(lastReq.Transcript, "排班系统") || strings.Contains(lastReq.Transcript, "二季度预算") {
		t.Fatalf("second req should carry only the new segment, got %q", lastReq.Transcript)
	}

	// 无新增 → unchanged 快返，LLM 计数不涨。
	before := calls.Load()
	rr = serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+m.ID+"/analyze", tokens["ws-a"], `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("unchanged analyze status=%d", rr.Code)
	}
	var third struct {
		Unchanged bool `json:"unchanged"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &third)
	if !third.Unchanged {
		t.Fatalf("expected unchanged=true, body=%s", rr.Body.String())
	}
	if calls.Load() != before {
		t.Fatal("unchanged poll must not call the gateway")
	}

	// reset → 重新全量。
	rr = serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+m.ID+"/analyze", tokens["ws-a"], `{"reset":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("reset analyze status=%d", rr.Code)
	}
	if !strings.Contains(lastReq.Transcript, "二季度预算") {
		t.Fatalf("after reset the full transcript should be re-sent, got %q", lastReq.Transcript)
	}

	// 实时分析不落库：状态/摘要不被触碰（summarize 的正式纪要不被覆盖）。
	after, err := srv.meetingStore.GetScoped(m.ID, "shared-user", "ws-a")
	if err != nil {
		t.Fatalf("reload meeting: %v", err)
	}
	if after.Summary != "" || after.Status != m.Status {
		t.Fatalf("live analyze must be read-only on the meeting, got summary=%q status=%q", after.Summary, after.Status)
	}

	// workspace 隔离：ws-b 404。
	rr = serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+m.ID+"/analyze", tokens["ws-b"], `{}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace analyze status=%d, want 404", rr.Code)
	}
}

// 网关未配置（无 key）→ 503；无转写 → 400。
func TestMeetingLiveAnalyzeGuards(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	h := srv.Handler()
	t.Cleanup(func() { meetingAnalyzeGateway = nil })
	meetingAnalyzeGateway = nil // 生产路径：无 env/用户设置 → 网关未配置

	m, err := srv.meetingStore.CreateScoped(meeting.CreateMeetingRequest{Title: "guards"}, "shared-user", "ws-a")
	if err != nil {
		t.Fatalf("create meeting: %v", err)
	}
	// handler 的守卫顺序：转写检查（400）先于网关配置检查（503），
	// 所以无转写时先撞 400——先给转写再验网关守卫。
	m.Transcript = "有内容"
	if err := srv.meetingStore.UpdateScoped(m, "shared-user", "ws-a"); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}

	rr := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+m.ID+"/analyze", tokens["ws-a"], `{}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured gateway status=%d body=%s", rr.Code, rr.Body.String())
	}
	t.Cleanup(func() { meetingAnalyzeGateway = nil })
	meetingAnalyzeGateway = func(_ *Server, _, _ string) (*llmgateway.Client, bool) {
		return llmgateway.NewClient("http://127.0.0.1:1", "sk-test"), true
	}
	rr = serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+m.ID+"/analyze", tokens["ws-a"], `{}`)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("unreachable gateway should surface as 502, got %d", rr.Code)
	}

	// 没有转写的会议 → 400。
	m2, err := srv.meetingStore.CreateScoped(meeting.CreateMeetingRequest{Title: "empty"}, "shared-user", "ws-a")
	if err != nil {
		t.Fatalf("create empty meeting: %v", err)
	}
	rr = serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+m2.ID+"/analyze", tokens["ws-a"], `{}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("no-transcript analyze status=%d, want 400", rr.Code)
	}
	_ = context.Background()
}
