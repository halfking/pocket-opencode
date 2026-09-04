package redclaw

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newTestAdminClient(t *testing.T, srv *httptest.Server) *AdminAuthClient {
	t.Helper()
	c, err := NewAdminAuthClient(AdminAuthClientConfig{
		AdminURL:     srv.URL,
		AuthAgentURL: srv.URL,
		Secret:       "test-secret-32bytes-min-aaaaaaaaaa",
		TenantID:     "default",
		TimeoutSec:   2,
	})
	if err != nil {
		t.Fatalf("NewAdminAuthClient: %v", err)
	}
	return c
}

func TestNewAdminAuthClient_RequiresBaseURLAndSecret(t *testing.T) {
	if _, err := NewAdminAuthClient(AdminAuthClientConfig{Secret: "x"}); err == nil {
		t.Fatal("expected error when AdminURL empty")
	}
	if _, err := NewAdminAuthClient(AdminAuthClientConfig{AdminURL: "http://x"}); err == nil {
		t.Fatal("expected error when Secret empty")
	}
}

func TestAdminAuthClient_Login_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/login" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-secret-32bytes-min-aaaaaaaaaa" {
			t.Errorf("bad auth header: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Tenant-ID") != "default" {
			t.Errorf("bad tenant header: %s", r.Header.Get("X-Tenant-ID"))
		}
		var body LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body.EmployeeID != "alice" || body.Password != "pwd" {
			t.Errorf("bad creds: %+v", body)
		}
		_ = json.NewEncoder(w).Encode(LoginResult{
			Token:              "tok-xyz",
			MustChangePassword: false,
			Employee: &EmployeeInfo{
				ID:    "user-1",
				Name:  "Alice",
				Role:  "employee",
				Email: "alice@example.com",
			},
		})
	}))
	defer srv.Close()

	c := newTestAdminClient(t, srv)
	res, err := c.Login(context.Background(), "alice", "pwd")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.Token != "tok-xyz" {
		t.Errorf("bad token: %q", res.Token)
	}
	if res.Employee == nil || res.Employee.ID != "user-1" {
		t.Errorf("bad employee: %+v", res.Employee)
	}
}

func TestAdminAuthClient_Login_InvalidCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(redClawAdminError{Error: struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{Code: "unauthorized", Message: "bad pwd"}})
	}))
	defer srv.Close()

	c := newTestAdminClient(t, srv)
	_, err := c.Login(context.Background(), "alice", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestAdminAuthClient_Login_5xxUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := newTestAdminClient(t, srv)
	_, err := c.Login(context.Background(), "alice", "pwd")
	if !errors.Is(err, ErrRedClawUnavailable) {
		t.Fatalf("expected ErrRedClawUnavailable, got %v", err)
	}
}

func TestAdminAuthClient_Login_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // 立即关闭模拟网络不可达

	c := newTestAdminClient(t, srv)
	_, err := c.Login(context.Background(), "alice", "pwd")
	if !errors.Is(err, ErrRedClawUnavailable) {
		t.Fatalf("expected ErrRedClawUnavailable, got %v", err)
	}
}

func TestAdminAuthClient_Login_EmptyArgsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called")
	}))
	defer srv.Close()
	c := newTestAdminClient(t, srv)
	if _, err := c.Login(context.Background(), "", "pwd"); err == nil {
		t.Error("expected error on empty employeeID")
	}
	if _, err := c.Login(context.Background(), "alice", ""); err == nil {
		t.Error("expected error on empty password")
	}
}

func TestAdminAuthClient_Me_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/me" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Me 端点应使用用户 token 鉴权，而非 Secret
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer user-tok") {
			t.Errorf("Me should use user token, got: %s", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(EmployeeInfo{ID: "user-1", Role: "admin"})
	}))
	defer srv.Close()
	c := newTestAdminClient(t, srv)
	res, err := c.Me(context.Background(), "user-tok")
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if res.Role != "admin" {
		t.Errorf("bad role: %s", res.Role)
	}
}

func TestAdminAuthClient_ChangePassword_Success(t *testing.T) {
	var gotBody ChangePasswordRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/change-password" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := newTestAdminClient(t, srv)
	if err := c.ChangePassword(context.Background(), "tok", "old1234", "new1234"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if gotBody.OldPassword != "old1234" || gotBody.NewPassword != "new1234" {
		t.Errorf("bad body: %+v", gotBody)
	}
}

func TestAdminAuthClient_ChangePassword_WeakPassword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(redClawAdminError{Error: struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{Code: "weak_password", Message: "<12 chars"}})
	}))
	defer srv.Close()
	c := newTestAdminClient(t, srv)
	err := c.ChangePassword(context.Background(), "tok", "old", "new")
	if err == nil || !strings.Contains(err.Error(), "weak password") {
		t.Fatalf("expected weak password error, got %v", err)
	}
}

func TestAdminAuthClient_Logout_Idempotent(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized) // 已失效
	}))
	defer srv.Close()
	c := newTestAdminClient(t, srv)
	if err := c.Logout(context.Background(), "tok"); err != nil {
		t.Errorf("Logout on 401 should be nil, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestAdminAuthClient_Logout_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := newTestAdminClient(t, srv)
	if err := c.Logout(context.Background(), "tok"); err == nil {
		t.Error("expected error on 5xx")
	}
}

func TestAdminAuthClient_SsoLoginURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := newTestAdminClient(t, srv)
	got := c.SsoLoginURL("abc-state", "https://app.example.com/cb")
	if !strings.Contains(got, "/api/v1/sso/login") {
		t.Errorf("missing sso path: %s", got)
	}
	if !strings.Contains(got, "external_state=abc-state") {
		t.Errorf("missing external_state: %s", got)
	}
	if u, err := url.Parse(got); err != nil || u.Query().Get("state") != "" {
		t.Errorf("legacy state param must not be sent: %s", got)
	}
	if !strings.Contains(got, "redirect_url=") {
		t.Errorf("missing redirect_url: %s", got)
	}
	// 空 state 时 external_state 参数整体省略。
	if got := c.SsoLoginURL("", ""); strings.Contains(got, "external_state") {
		t.Errorf("empty state must omit external_state: %s", got)
	}
}

func TestAdminAuthClient_SsoCallback_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/sso/callback") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("code") != "idp-code" {
			t.Errorf("bad code: %s", r.URL.Query().Get("code"))
		}
		// 真实 auth-agent 的响应形状：{jwt, claims, next, external_state}。
		_ = json.NewEncoder(w).Encode(SsoCallbackResult{
			JWT:           "sso-tok",
			Claims:        SsoCallbackClaims{Sub: "user-sso", Name: "Alice", Tenant: "default"},
			Next:          "/",
			ExternalState: "relay-nonce",
		})
	}))
	defer srv.Close()
	c := newTestAdminClient(t, srv)
	res, err := c.SsoCallback(context.Background(), "idp-code", "state-1")
	if err != nil {
		t.Fatalf("SsoCallback: %v", err)
	}
	if res.JWT != "sso-tok" || res.Claims.Sub != "user-sso" || res.ExternalState != "relay-nonce" {
		t.Errorf("bad sso result: %+v", res)
	}
}

func TestAdminAuthClient_SsoCallback_EmptyJWTRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 模拟异常 upstream：200 但缺 jwt —— 必须当作 upstream 错误。
		_ = json.NewEncoder(w).Encode(map[string]any{"claims": map[string]any{"sub": "u1"}})
	}))
	defer srv.Close()
	c := newTestAdminClient(t, srv)
	if _, err := c.SsoCallback(context.Background(), "idp-code", "state-1"); !errors.Is(err, ErrRedClawUnavailable) {
		t.Fatalf("empty jwt must be ErrRedClawUnavailable, got %v", err)
	}
}

func TestAdminAuthClient_Login_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond) // 超过 client 的 2s timeout？不会。改用 50ms timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, _ := NewAdminAuthClient(AdminAuthClientConfig{
		AdminURL:   srv.URL,
		Secret:     "test-secret-32bytes-min-aaaaaaaaaa",
		TenantID:   "default",
		TimeoutSec: 0, // 默认 10s
	})
	_ = c
	// timeout 由 http.Client 控制；这里仅做烟雾测试，不真等 10s。
	// 用 ctx 取消来模拟上层 timeout。
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Login(ctx, "alice", "pwd")
	if err == nil {
		t.Error("expected context-deadline error")
	}
	// 验证错误至少包含 ErrRedClawUnavailable
	if !errors.Is(err, ErrRedClawUnavailable) && !strings.Contains(err.Error(), "context") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAdminAuthClient_parseAdminError_TenantMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(redClawAdminError{Error: struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{Code: "tenant_mismatch", Message: "X"}})
	}))
	defer srv.Close()
	c := newTestAdminClient(t, srv)
	_, err := c.Login(context.Background(), "alice", "pwd")
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected ErrTenantMismatch, got %v", err)
	}
}

// Sanity: io.Discard 在导入列表里没用上,移除。
var _ = io.Discard
