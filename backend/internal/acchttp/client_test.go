package acchttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 测试要点：
//   - Authorization / Tenant / Request ID 三个头由客户端自动注入；
//     调用方尝试覆盖时仍以服务端为准。
//   - GET 安全方法可重试 5xx，POST 非安全方法不重试。
//   - API key 不能出现在日志里（redactPath 不会泄漏 query）。
//   - ctx 取消立即终止。
//   - URL 拼接正确，path 解析避免重复 /。

func newTestClient(t *testing.T, ts *httptest.Server, retries int) *Client {
	t.Helper()
	cfg := Config{
		BaseURL:    ts.URL,
		APIKey:     "secret-token-XYZ",
		TenantID:   "tenant-42",
		Timeout:    2 * time.Second,
		MaxRetries: retries,
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestNewValidatesConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error for empty BaseURL")
	}
	if _, err := New(Config{BaseURL: "ftp://x"}); err == nil {
		t.Fatal("expected error for non-http(s) BaseURL")
	}
}

func TestDoInjectsAuthHeaders(t *testing.T) {
	var capturedAuth, capturedTenant, capturedReqID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedTenant = r.Header.Get("X-Pocket-Tenant")
		capturedReqID = r.Header.Get("X-Pocket-Request-ID")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer ts.Close()

	c := newTestClient(t, ts, 0)
	resp, err := c.Get(context.Background(), "/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if capturedAuth != "Bearer secret-token-XYZ" {
		t.Errorf("Authorization header = %q", capturedAuth)
	}
	if capturedTenant != "tenant-42" {
		t.Errorf("X-Pocket-Tenant = %q", capturedTenant)
	}
	if capturedReqID == "" || !strings.HasPrefix(capturedReqID, "req-") {
		t.Errorf("X-Pocket-Request-ID missing/malformed: %q", capturedReqID)
	}
	if resp.RequestID != capturedReqID {
		t.Errorf("Response.RequestID = %q, want %q", resp.RequestID, capturedReqID)
	}
}

func TestDoRejectsCallerHeaderOverride(t *testing.T) {
	var capturedAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := newTestClient(t, ts, 0)
	hdr := map[string]string{
		"Authorization":       "Bearer MALICIOUS",
		"X-Pocket-Tenant":     "evil",
		"X-Pocket-Request-ID": "evil",
	}
	if _, err := c.Get(context.Background(), "/probe", hdr); err != nil {
		t.Fatal(err)
	}
	if capturedAuth != "Bearer secret-token-XYZ" {
		t.Errorf("caller overrode Authorization: %q", capturedAuth)
	}
}

func TestDoRetries5xxOnSafeMethod(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if calls.Load() < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := newTestClient(t, ts, 3)
	_, err := c.Get(context.Background(), "/flaky", nil)
	if err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", calls.Load())
	}
}

func TestDoDoesNotRetryPost(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := newTestClient(t, ts, 3)
	body := map[string]string{"k": "v"}
	_, err := c.Post(context.Background(), "/create", nil, body)
	if err == nil {
		t.Fatal("expected error after 500")
	}
	if calls.Load() != 1 {
		t.Errorf("POST must not retry on 5xx; calls=%d", calls.Load())
	}
}

func TestDoReturns4xxWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "bad")
	}))
	defer ts.Close()

	c := newTestClient(t, ts, 3)
	_, err := c.Get(context.Background(), "/bad", nil)
	if err == nil {
		t.Fatal("expected 400 error")
	}
	if calls.Load() != 1 {
		t.Errorf("4xx must not retry; calls=%d", calls.Load())
	}
}

func TestDoRespectsContextCancel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 阻塞直到 ctx 取消。
		<-r.Context().Done()
	}))
	defer ts.Close()

	c := newTestClient(t, ts, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Get(ctx, "/slow", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "context") &&
		!strings.Contains(err.Error(), "deadline") {
		t.Fatalf("expected context error, got %v", err)
	}
}

func TestDoDecodeJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"hello": "world"})
	}))
	defer ts.Close()

	c := newTestClient(t, ts, 0)
	resp, err := c.Get(context.Background(), "/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := resp.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["hello"] != "world" {
		t.Errorf("got %v", got)
	}
}

func TestRedactPathStripsQuery(t *testing.T) {
	got := redactPath("https://x.example.com/foo?token=abc&secret=1")
	if strings.Contains(got, "token") || strings.Contains(got, "secret") {
		t.Fatalf("redactPath leaked query: %s", got)
	}
	if got != "/foo" {
		t.Fatalf("redactPath wrong: %s", got)
	}
}

func TestConcurrentRequestsGetUniqueRequestIDs(t *testing.T) {
	var (
		mu   sync.Mutex
		seen = map[string]int{}
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Pocket-Request-ID")
		mu.Lock()
		seen[id]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := newTestClient(t, ts, 0)
	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Get(context.Background(), "/probe", nil)
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != n {
		t.Errorf("expected %d unique request IDs, got %d", n, len(seen))
	}
}

// TestRedactPathStripsUserInfoAndFragment 验证 URL 中 UserInfo 与 Fragment
// 不被写入日志,避免内嵌凭据泄露。
func TestRedactPathStripsUserInfoAndFragment(t *testing.T) {
	got := redactPath("https://user:pass@acc.kxpms.cn/v1/x#frag")
	if strings.Contains(got, "user") || strings.Contains(got, "pass") || strings.Contains(got, "frag") {
		t.Fatalf("redactPath leaked userinfo/fragment: %q", got)
	}
	if got != "/v1/x" {
		t.Fatalf("redactPath wrong: %q", got)
	}
}

// TestResolveURLRejectsAbsolutePath 防止 caller 把外部 URL 当 path 传入
// 触发 SSRF / 跨域走私。
func TestResolveURLRejectsAbsolutePath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c, err := New(Config{BaseURL: ts.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), "https://evil.example.com/steal", nil); err == nil {
		t.Fatal("absolute path must be rejected")
	}
}

// TestReservedHeaderCaseInsensitive 验证 caller 用小写 header 名也
// 无法覆盖 Authorization / X-Pocket-Tenant / X-Pocket-Request-ID。
func TestReservedHeaderCaseInsensitive(t *testing.T) {
	var capturedAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := Config{BaseURL: ts.URL, APIKey: "real-token", TenantID: "tenant-1"}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{
		"authorization":       "Bearer ATTACKER",
		"x-pocket-tenant":     "evil",
		"x-pocket-request-id": "attacker-id",
	}
	if _, err := c.Get(context.Background(), "/probe", hdr); err != nil {
		t.Fatal(err)
	}
	if capturedAuth != "Bearer real-token" {
		t.Errorf("caller overrode Authorization (case-insensitive): %q", capturedAuth)
	}
}
