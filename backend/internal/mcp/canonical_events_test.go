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

// ★ 2026-11-20（Phase D 前置修复验收）：长驻 run 的事件流已 10.2MB，旧 1MB
// LimitReader 把响应截断 ⇒ JSON 解析失败 ⇒ resolveACCDispatch 解不出 holder
// ⇒ 真机「停止任务」永远 fail-closed 502。这两个判据锁住 64MB 新上限行为：
// 正常回读要全量过，超限时**显式报错**而不是拿半截 JSON 静默出错。
func TestListRunEventsLargeResponseOK(t *testing.T) {
	big := make([]byte, 3<<20) // 3MB —— 旧 1MB 上限必截断，新 64MB 上限畅通
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
