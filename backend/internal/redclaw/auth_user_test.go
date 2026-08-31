package redclaw

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewAuthClient_NilOnEmptyBaseURL(t *testing.T) {
	c, err := NewAuthClient(AuthClientConfig{Secret: "x"})
	if err == nil {
		t.Fatalf("expected error when BaseURL empty, got nil")
	}
	if c != nil {
		t.Fatal("expected nil client when BaseURL empty")
	}
}

func TestAuthClient_RegisterUser_Success(t *testing.T) {
	var got RegisterUserRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/users/register" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if got2 := r.Header.Get("Authorization"); got2 != "Bearer test-secret" {
			t.Errorf("unexpected auth header: %s", got2)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ac, err := NewAuthClient(AuthClientConfig{
		BaseURL:    srv.URL,
		Secret:     "test-secret",
		TenantID:   "default",
		TimeoutSec: 2,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// RegisterUser is fail-soft: must not panic and not block on response.
	ac.RegisterUser(context.Background(), RegisterUserRequest{
		Email:    "a@b.c",
		Username: "alice",
		UserID:   "user-1",
		// TenantID 留空 → 由 client 自动注入
	})
	// 给 fire goroutine 一点时间完成（同步实现无需等待，但留出兜底）
	time.Sleep(50 * time.Millisecond)
	if got.Email != "a@b.c" || got.Username != "alice" {
		t.Errorf("unexpected request body: %+v", got)
	}
	if got.TenantID != "default" {
		t.Errorf("tenant id not injected: %q", got.TenantID)
	}
}

func TestAuthClient_HTTPError_DoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":500,"message":"oops"}`))
	}))
	defer srv.Close()

	ac, err := NewAuthClient(AuthClientConfig{BaseURL: srv.URL, Secret: "s", TenantID: "default", TimeoutSec: 2})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// fail-soft：HTTP 500 也不应 panic / 阻塞
	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("unexpected panic on HTTP error: %v", rec)
		}
	}()
	ac.RegisterUser(context.Background(), RegisterUserRequest{Email: "a@b.c", UserID: "u1"})
	ac.ResetPassword(context.Background(), ResetPasswordRequest{Email: "a@b.c", UserID: "u1"})
	ac.VerifyCodeLogin(context.Background(), VerifyCodeLoginRequest{Email: "a@b.c", UserID: "u1"})
}

func TestAuthClient_NilClientSafe(t *testing.T) {
	var ac *AuthClient
	// nil 调用应全部为 no-op，不 panic。
	ac.RegisterUser(context.Background(), RegisterUserRequest{})
	ac.ResetPassword(context.Background(), ResetPasswordRequest{})
	ac.VerifyCodeLogin(context.Background(), VerifyCodeLoginRequest{})
}

func TestAuthClient_AllMethodsDispatch(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ac, err := NewAuthClient(AuthClientConfig{BaseURL: srv.URL, Secret: "s", TenantID: "default", TimeoutSec: 2})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ac.RegisterUser(context.Background(), RegisterUserRequest{Email: "a@b.c", UserID: "u1"})
	ac.ResetPassword(context.Background(), ResetPasswordRequest{Email: "a@b.c", UserID: "u1"})
	ac.VerifyCodeLogin(context.Background(), VerifyCodeLoginRequest{Email: "a@b.c", UserID: "u1"})
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Errorf("expected 3 requests, got %d", got)
	}
}

func TestAuthClient_MissingEmailOrUserID_NoCall(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ac, err := NewAuthClient(AuthClientConfig{BaseURL: srv.URL, Secret: "s", TenantID: "default", TimeoutSec: 2})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ac.RegisterUser(context.Background(), RegisterUserRequest{Email: "a@b.c"})   // 缺 user_id
	ac.ResetPassword(context.Background(), ResetPasswordRequest{UserID: "u1"})   // 缺 email
	ac.VerifyCodeLogin(context.Background(), VerifyCodeLoginRequest{Email: "x"}) // 缺 user_id
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("expected 0 requests when fields missing, got %d", got)
	}
}
