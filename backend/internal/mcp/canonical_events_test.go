package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseCanonicalTaskResult(t *testing.T) {
	got, err := ParseCanonicalTaskResult(`{"data":{"task_id":"task-1","run_id":"run-1","operation_id":"op-1","status":"accepted"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskID != "task-1" || got.RunID != "run-1" || got.OperationID != "op-1" {
		t.Fatalf("got %#v", got)
	}
}

func TestParseCanonicalTaskResultFailsClosed(t *testing.T) {
	if _, err := ParseCanonicalTaskResult(`{"run_id":"run-1"}`); err == nil {
		t.Fatal("missing task id must fail")
	}
}

func TestListRunEventsUsesMCPRootAndCursor(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":[{"event_id":"e1","event_type":"run.created","run_id":"run-1","sequence":1}]}`))
	}))
	defer ts.Close()
	client := NewClientWithAuth(ts.URL+"/api/v2/mcp", "secret", "tenant", nil, false)
	events, err := client.ListRunEvents(t.Context(), "run-1", 7)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v2/orchestration/runs/run-1/events?after=7" {
		t.Fatalf("path=%q", gotPath)
	}
	if len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("events=%#v", events)
	}
}

// ★ 2026-11-20（Phase D 前置修复验收）：长驻 run 的事件流实测 10.4MB——
// 通用 JSON 档 sizedread.MaxJSONBodyBytes（8MiB）把成功响应掐死 ⇒
// resolveACCDispatch 解不出 holder ⇒ 真机「停止任务」永远 fail-closed 502。
// 判据锁住 64MB 事件流专用上限：10MB（>8MiB 旧限）必须全量过，
// 超 64MB 时显式报错而不是静默截断。
// ⚠ 第一版修复把上限改在了 >=400 错误分支、单测 payload 只用 3MB——
// 假绿。两个坑都被这一版修掉：payload 必须 >8MiB 才能触达真限制。
func TestListRunEventsLargeResponseOK(t *testing.T) {
	big := make([]byte, 10<<20) // 10MB —— >8MiB 通用档（旧限必失败），<64MB 新档畅通
	for i := range big {
		big[i] = 'x'
	}
	payload := `{"events":[{"event_id":"e1","event_type":"run.created","run_id":"run-1","sequence":1,"data":"` + string(big) + `"}]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer ts.Close()
	client := NewClientWithAuth(ts.URL+"/api/v2/mcp", "secret", "tenant", nil, false)
	events, err := client.ListRunEvents(t.Context(), "run-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected 1 event")
	}
	if len(events[0].EventID) == 0 {
		t.Fatalf("response truncated / unusable: %#v", events[0])
	}
}

func TestListRunEventsOverCapFailsExplicitly(t *testing.T) {
	if testing.Short() {
		t.Skip("64MB payload")
	}
	payload := `{"events":[{"event_id":"e1","event_type":"run.created","run_id":"run-1","sequence":1,"data":"` + string(make([]byte, 64<<20)) + `"}]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer ts.Close()
	client := NewClientWithAuth(ts.URL+"/api/v2/mcp", "secret", "tenant", nil, false)
	if _, err := client.ListRunEvents(t.Context(), "run-1", 0); err == nil {
		t.Fatal("over-cap response must fail explicitly, not silently truncate")
	}
}
