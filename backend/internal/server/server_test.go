package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

func TestHealthz(t *testing.T) {
	cfg := config.Load()
	timeoutMS, _ := strconv.Atoi(cfg.OpenCodeTimeoutMS)
	if timeoutMS == 0 {
		timeoutMS = 5000
	}

	reg := registry.NewRegistry()
	configAdapter := adapter.NewOpenCodeConfigHTTPAdapter(timeoutMS)
	srv := New(cfg, adapter.NewStaticNPSAdapter(), adapter.NewOpenCodeHTTPAdapter(timeoutMS), nil, reg, configAdapter, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if string(body) != "ok" {
		t.Fatalf("expected ok, got %q", string(body))
	}
}

// withTestClaims 把已认证身份注入 request context，用于直接调用 handler 的
// 单元测试（绕过 requireAuth 中间件但保持 handler 的授权前提成立）。
func withTestClaims(r *http.Request, userID, role, workspaceID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), authClaimsContextKey{}, &authClaims{
		UserID:      userID,
		Role:        role,
		WorkspaceID: workspaceID,
	}))
}

// newTestServerWithAuth 构建带 JWT signer 的 Server，并返回可用于
// Authorization header 的 token。业务路由都在 requireAuth 之后，测试必须
// 携带真实 token 才能命中 handler。
func newTestServerWithAuth(t *testing.T) (*Server, string) {
	t.Helper()

	cfg := config.Load()
	timeoutMS, _ := strconv.Atoi(cfg.OpenCodeTimeoutMS)
	if timeoutMS == 0 {
		timeoutMS = 5000
	}

	// NewSigner 强制 secret ≥ 32 bytes。
	signer, err := auth.NewSigner("test-secret-for-unit-tests-0123456789", time.Hour)
	if err != nil {
		t.Fatalf("NewSigner failed: %v", err)
	}
	token, err := signer.SignWithWorkspace("test-user", "admin", "test-workspace")
	if err != nil {
		t.Fatalf("SignWithWorkspace failed: %v", err)
	}

	reg := registry.NewRegistry()
	configAdapter := adapter.NewOpenCodeConfigHTTPAdapter(timeoutMS)
	srv := New(cfg, adapter.NewStaticNPSAdapter(), adapter.NewOpenCodeHTTPAdapter(timeoutMS), nil, reg, configAdapter, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, signer, nil, nil, nil, nil, "")
	return srv, token
}

func TestInstances(t *testing.T) {
	srv, token := newTestServerWithAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if rr.Body.String() == "" {
		t.Fatal("expected instances payload")
	}
}

// TestInstancesUnauthenticated 回归测试：/api/instances 此前完全公开，
// 现在必须拒绝无凭证请求。
func TestInstancesUnauthenticated(t *testing.T) {
	srv, _ := newTestServerWithAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rr.Code)
	}
}

// TestQueryTokenRejectedOnPlainHTTP 回归测试：URL query 中的 token 只允许
// WebSocket / SSE 握手使用，普通 HTTP 路由不接受（避免 token 泄漏到日志、
// Referer 和浏览器历史）。
func TestQueryTokenRejectedOnPlainHTTP(t *testing.T) {
	srv, token := newTestServerWithAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/instances?token="+token, nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for query-string token on plain HTTP, got %d", rr.Code)
	}
}

// TestInstancesInvalidToken 回归测试：伪造/过期 token 必须被拒绝。
func TestInstancesInvalidToken(t *testing.T) {
	srv, _ := newTestServerWithAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-jwt")
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid token, got %d", rr.Code)
	}
}
