package accruntime

// client_test.go — httptest 全覆盖：
//   - 请求头断言（Authorization / Idempotency-Key）
//   - 请求体断言（permission triple + decision、cancel reason）
//   - 404/501 → ErrGateUnavailable（fail-closed 契约）
//   - 500 → 普通包装错误（不得误判为 gate 不可用）
//   - 网络失败 → 包装错误且不泄漏 API key
//   - 成功路径（permission / cancel / approvals inbox / approve / reject）
//   - 校验错误（缺 triple / 缺 command_id / reject 缺 reason）不触网

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/acchttp"
)

const testAPIKey = "secret-token-XYZ"

func newTestClient(t *testing.T, ts *httptest.Server) *Client {
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

func validDecision() PermissionDecision {
	return PermissionDecision{
		DispatchID:    "disp-1",
		ToolCallID:    "call-9",
		SourceRef:     "pocket://ws-a/task-7",
		CorrelationID: "corr-77",
		OptionID:      "once",
		Allow:         true,
		Reason:        "trusted operator",
	}
}

func TestAnswerPermission_SuccessSendsTripleAndDecision(t *testing.T) {
	var (
		gotPath           string
		gotMethod         string
		gotAuth           string
		gotTenant         string
		gotIdempotencyKey string
		gotBody           map[string]any
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotAuth = r.Header.Get("Authorization")
		gotTenant = r.Header.Get("X-Pocket-Tenant")
		gotIdempotencyKey = r.Header.Get("Idempotency-Key")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	if err := c.AnswerPermission(context.Background(), validDecision()); err != nil {
		t.Fatalf("AnswerPermission: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v2/runtime/commands/disp-1/permission" {
		t.Fatalf("method/path = %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer "+testAPIKey {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotTenant != "tenant-42" {
		t.Errorf("X-Pocket-Tenant = %q", gotTenant)
	}
	if gotIdempotencyKey != "" {
		t.Errorf("permission must not carry a cancel Idempotency-Key, got %q", gotIdempotencyKey)
	}
	if gotBody["tool_call_id"] != "call-9" || gotBody["source_ref"] != "pocket://ws-a/task-7" || gotBody["correlation_id"] != "corr-77" {
		t.Errorf("triple not forwarded: %v", gotBody)
	}
	dec, ok := gotBody["decision"].(map[string]any)
	if !ok {
		t.Fatalf("decision object missing: %v", gotBody)
	}
	if dec["option_id"] != "once" || dec["allow"] != true || dec["reason"] != "trusted operator" {
		t.Errorf("decision payload wrong: %v", dec)
	}
}

func TestAnswerPermission_MissingTripleFailsBeforeHTTP(t *testing.T) {
	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	for _, mutate := range []func(*PermissionDecision){
		func(d *PermissionDecision) { d.DispatchID = "" },
		func(d *PermissionDecision) { d.ToolCallID = " " },
		func(d *PermissionDecision) { d.SourceRef = "" },
		func(d *PermissionDecision) { d.CorrelationID = "" },
	} {
		d := validDecision()
		mutate(&d)
		err := c.AnswerPermission(context.Background(), d)
		if err == nil {
			t.Fatalf("expected validation error for %+v", d)
		}
		if strings.Contains(err.Error(), "accruntime: answer permission") {
			t.Errorf("validation error must not be wrapped as transport error: %v", err)
		}
	}
	if hit {
		t.Fatal("server must not be contacted for invalid decisions")
	}
}

func TestAnswerPermission_Gate404And501MapToErrGateUnavailable(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusNotImplemented} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_, _ = io.WriteString(w, `{"error":"not mounted"}`)
		}))
		c := newTestClient(t, ts)
		err := c.AnswerPermission(context.Background(), validDecision())
		ts.Close()
		if err == nil {
			t.Fatalf("HTTP %d: expected error", code)
		}
		if !errors.Is(err, ErrGateUnavailable) {
			t.Fatalf("HTTP %d: want ErrGateUnavailable, got %v", code, err)
		}
	}
}

func TestAnswerPermission_HTTP500IsGenericError(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	err := c.AnswerPermission(context.Background(), validDecision())
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrGateUnavailable) {
		t.Fatalf("500 must not be classified as gate unavailable: %v", err)
	}
	if calls != 1 {
		t.Errorf("POST must not retry; calls=%d", calls)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should carry status context: %v", err)
	}
}

func TestAnswerPermission_NetworkFailureWrapsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := ts.URL
	ts.Close() // 关闭后必然连接失败

	c, err := New(acchttp.Config{BaseURL: base, APIKey: testAPIKey, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	err = c.AnswerPermission(context.Background(), validDecision())
	if err == nil {
		t.Fatal("expected network error")
	}
	if errors.Is(err, ErrGateUnavailable) {
		t.Fatalf("network failure must not look like gate-unavailable: %v", err)
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("error leaks API key: %v", err)
	}
}

func TestAnswerPermission_NilClientFails(t *testing.T) {
	var c *Client
	if err := c.AnswerPermission(context.Background(), validDecision()); err == nil {
		t.Fatal("expected nil client error")
	}
}

func TestCancelCommand_SendsReasonAndIdempotencyKey(t *testing.T) {
	var (
		gotPath           string
		gotMethod         string
		gotIdempotencyKey string
		gotBody           map[string]string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotIdempotencyKey = r.Header.Get("Idempotency-Key")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true,"data":{}}`)
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	if err := c.CancelCommand(context.Background(), "disp-9", "holder-1", "operator aborted"); err != nil {
		t.Fatalf("CancelCommand: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v2/runtime/commands/disp-9/cancel" {
		t.Fatalf("method/path = %s %s", gotMethod, gotPath)
	}
	if gotIdempotencyKey != "pocket-cancel-disp-9" {
		t.Errorf("Idempotency-Key = %q, want pocket-cancel-disp-9", gotIdempotencyKey)
	}
	if gotBody["reason"] != "operator aborted" {
		t.Errorf("reason body = %v", gotBody)
	}
	if gotBody["holder_id"] != "holder-1" {
		t.Errorf("holder_id body = %v", gotBody)
	}
}

func TestCancelCommand_RequiresCommandID(t *testing.T) {
	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	if err := c.CancelCommand(context.Background(), "", "holder-1", "x"); err == nil {
		t.Fatal("expected validation error")
	}
	if hit {
		t.Fatal("server must not be contacted")
	}
}

func TestCancelCommand_HTTPErrorWraps(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, "invalid transition")
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	err := c.CancelCommand(context.Background(), "disp-1", "holder-1", "late")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("error leaks API key: %v", err)
	}
}

func TestListApprovals_ObjectEnvelope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/approvals" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"ok":true,"count":1,"approvals":[`+
			`{"task_id":"t-1","title":"Ship","kind":"review","phase":"review","requested_by":"alice",`+
			`"question":"ok?","requested_at":"2026-09-12T00:00:00Z","pending_requests":2}]}`)
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	items, err := c.ListApprovals(context.Background())
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d approvals", len(items))
	}
	a := items[0]
	if a.TaskID != "t-1" || a.Title != "Ship" || a.Kind != "review" || a.Phase != "review" ||
		a.RequestedBy != "alice" || a.Question != "ok?" || a.PendingRequests != 2 {
		t.Fatalf("approval decoded wrong: %+v", a)
	}
	if a.RequestedAt.IsZero() {
		t.Fatalf("RFC3339 requested_at should parse: %+v", a)
	}
}

func TestListApprovals_LenientShapes(t *testing.T) {
	cases := map[string]string{
		"bare array":     `[{"task_id":"t-2"}]`,
		"data envelope":  `{"data":[{"task_id":"t-3"}]}`,
		"items envelope": `{"items":[{"task_id":"t-4"}]}`,
		"empty inbox":    `{"ok":true,"count":0,"approvals":[]}`,
		"unknown object": `{"ok":true,"count":0}`,
		"epoch millis":   `{"approvals":[{"task_id":"t-5","requested_at":1757635200000}]}`,
		"epoch seconds":  `{"approvals":[{"task_id":"t-6","requested_at":1757635200}]}`,
		"null requested": `{"approvals":[{"task_id":"t-7","requested_at":null}]}`,
	}
	for name, body := range cases {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		c := newTestClient(t, ts)
		items, err := c.ListApprovals(context.Background())
		ts.Close()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		switch name {
		case "empty inbox", "unknown object":
			if len(items) != 0 {
				t.Errorf("%s: expected empty inbox, got %+v", name, items)
			}
		case "epoch millis":
			if len(items) != 1 || items[0].RequestedAt.IsZero() {
				t.Errorf("%s: epoch ms should parse: %+v", name, items)
			}
		case "epoch seconds":
			if len(items) != 1 || items[0].RequestedAt.IsZero() {
				t.Errorf("%s: epoch s should parse: %+v", name, items)
			}
		default:
			if len(items) != 1 || items[0].TaskID == "" {
				t.Errorf("%s: expected one approval, got %+v", name, items)
			}
		}
	}
}

func TestListApprovals_NetworkFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := ts.URL
	ts.Close()

	c, err := New(acchttp.Config{BaseURL: base, APIKey: testAPIKey, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListApprovals(context.Background()); err == nil {
		t.Fatal("expected error")
	} else if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("error leaks API key: %v", err)
	}
}

func TestApproveAndReject_PathsAndBodies(t *testing.T) {
	var gotPath string
	var gotBody map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	if err := c.Approve(context.Background(), "t-1", "looks good"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if gotPath != "/api/v2/approvals/t-1/approve" {
		t.Fatalf("approve path = %s", gotPath)
	}
	if gotBody["comment"] != "looks good" {
		t.Fatalf("approve body = %v", gotBody)
	}

	if err := c.Reject(context.Background(), "t-1", "bad evidence"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if gotPath != "/api/v2/approvals/t-1/reject" {
		t.Fatalf("reject path = %s", gotPath)
	}
	if gotBody["reason"] != "bad evidence" {
		t.Fatalf("reject body = %v", gotBody)
	}
}

func TestApproveAndReject_Validation(t *testing.T) {
	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	if err := c.Approve(context.Background(), "", "x"); err == nil {
		t.Fatal("approve without task id must fail")
	}
	if err := c.Reject(context.Background(), "t-1", ""); err == nil {
		t.Fatal("reject without reason must fail (ACC mandates it)")
	}
	if err := c.Reject(context.Background(), "", "no"); err == nil {
		t.Fatal("reject without task id must fail")
	}
	if hit {
		t.Fatal("server must not be contacted for invalid input")
	}
}

func TestHTTPStatusFromError_LastMatchWins(t *testing.T) {
	wrapped := errors.New("acchttp: retries exhausted: acchttp: HTTP 503: first acchttp: HTTP 502: x")
	// 多段匹配时取最后一个（最后一次尝试）。
	code, ok := httpStatusFromError(wrapped)
	if !ok || code != 502 {
		t.Fatalf("got %d %v", code, ok)
	}
	if _, ok := httpStatusFromError(errors.New("plain")); ok {
		t.Fatal("plain error must not yield a status")
	}
}
