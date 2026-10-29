package server

// server_workspace_binding_test.go — 工作区成员绑定断言的 HTTP 面（2026-10-29）。
//
// ⚠ 这里测的是**不需要 PG** 的那半边：fail-closed 行为。
//   签发路径需要真实 identity store（成员资格判定要查库），
//   覆盖在 internal/identity/binding_store_test.go（需 POCKET_TEST_POSTGRES_DSN）。
//
// 📌 核心断言：**「证明不了」必须表现为拒绝，不能表现为通过**。
//   共享密钥没配 ⇒ 503（服务不可用），不是 200 带空断言。
//   未认证 ⇒ 401。
//   签名不对 ⇒ 401。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/identity"
)

// TestWorkspaceBindingFailsClosedWithoutSharedSecret — 共享密钥没配时不得签出断言。
//
// ⚠ 这是「服务间可信验证」的底线：没有可签名的密钥 ⇒ 无法证明成员资格
//
//	⇒ 不能给出一个「看起来像断言」的东西。
//	退化路径（发一个空断言/放行）会让调用方把「没证明」当成「证明了」。
func TestWorkspaceBindingFailsClosedWithoutSharedSecret(t *testing.T) {
	// ⚠ 显式清空共享密钥：本测试要证明的是「密钥缺失 ⇒ 拒绝」。
	t.Setenv("IDENTITY_SHARED_SECRET", "")

	srv, _, _, tokens := newMobileRouteServer(t)
	srv.SetIdentityStore(nil)
	h := srv.Handler()

	req := mobileRequest(http.MethodGet, "/api/workspaces/ws-a/binding", tokens["ws-a"], "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// identityStore 为 nil ⇒ 503（不是 200、不是 403）。
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%q)", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "binding") && strings.Contains(rr.Body.String(), "eyJ") {
		t.Fatalf("response leaked an assertion: %q", rr.Body.String())
	}
}

// TestAuthWorkspaceBindingRejectsMissingToken — 校验端点必须认证。
func TestAuthWorkspaceBindingRejectsMissingToken(t *testing.T) {
	t.Setenv("IDENTITY_SHARED_SECRET", "")
	srv, _, _, _ := newMobileRouteServer(t)
	h := srv.Handler()

	req := mobileRequest(http.MethodPost, "/api/auth/workspace-binding", "", `{"binding":"x"}`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%q)", rr.Code, rr.Body.String())
	}
}

// TestAuthWorkspaceBindingRejectsGarbageAssertion — 乱写的断言验不过。
func TestAuthWorkspaceBindingRejectsGarbageAssertion(t *testing.T) {
	// 配一把**真实**的共享密钥，让端点能走到验签这一步
	// （密钥缺失时是 503，那是「服务不可用」，不是「凭据不对」——
	//   两种失败必须能被区分，见下一个用例）。
	secret := []byte(strings.Repeat("d", 32))
	t.Setenv("IDENTITY_SHARED_SECRET", string(secret))

	srv, _, _, tokens := newMobileRouteServer(t)
	h := srv.Handler()

	req := mobileRequest(http.MethodPost, "/api/auth/workspace-binding", tokens["ws-a"], `{"binding":"not-a-jwt"}`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%q)", rr.Code, rr.Body.String())
	}
}

// TestAuthWorkspaceBindingDistinguishesUnconfiguredFromInvalid — 两种失败要分得开。
//
// ⚠ 「服务没配」与「凭据不对」是**运维要处理的两件不同的事**：
//
//	前者要补配置，后者要查入侵。合成一个错误码 ⇒ 运维只能靠猜。
func TestAuthWorkspaceBindingDistinguishesUnconfiguredFromInvalid(t *testing.T) {
	t.Setenv("IDENTITY_SHARED_SECRET", "")
	srv, _, _, tokens := newMobileRouteServer(t)
	h := srv.Handler()

	req := mobileRequest(http.MethodPost, "/api/auth/workspace-binding", tokens["ws-a"], `{"binding":"not-a-jwt"}`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when unconfigured (body=%q)", rr.Code, rr.Body.String())
	}
}

// TestAuthWorkspaceBindingRejectsNonPost — 本端点只接受 POST。
func TestAuthWorkspaceBindingRejectsNonPost(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	h := srv.Handler()

	req := mobileRequest(http.MethodGet, "/api/auth/workspace-binding", tokens["ws-a"], "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (body=%q)", rr.Code, rr.Body.String())
	}
}

// TestAuthWorkspaceBindingAcceptsValidAssertion — 有效断言验得过且回报正确范围。
//
// ⚠ 这一条用真实的签名器（本地密钥）走完整往返：
//
//	签 → 提交 → 验 → 拿回范围。这是「服务间可信验证」的主路径。
func TestAuthWorkspaceBindingAcceptsValidAssertion(t *testing.T) {
	secret := []byte(strings.Repeat("b", 32))
	signer, err := identity.NewBindingSignerWithSecret(secret)
	if err != nil {
		t.Fatalf("NewBindingSignerWithSecret: %v", err)
	}
	signed, _, err := signer.Mint(identity.Member{
		WorkspaceID: "ws-a",
		UserID:      "user-wsa",
		Role:        identity.RoleOwner,
	}, "tenant-a", 0)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	// 让端点用同一把钥匙验证。
	t.Setenv("IDENTITY_SHARED_SECRET", string(secret))
	srv, _, _, tokens := newMobileRouteServer(t)
	h := srv.Handler()

	body, _ := json.Marshal(map[string]string{
		"binding":      signed,
		"user_id":      "user-wsa",
		"workspace_id": "ws-a",
	})
	req := mobileRequest(http.MethodPost, "/api/auth/workspace-binding", tokens["ws-a"], string(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", rr.Code, rr.Body.String())
	}
	var got struct {
		Valid       bool   `json:"valid"`
		UserID      string `json:"user_id"`
		WorkspaceID string `json:"workspace_id"`
		TenantID    string `json:"tenant_id"`
		Role        string `json:"role"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%q", err, rr.Body.String())
	}
	if !got.Valid {
		t.Fatal("valid = false")
	}
	if got.UserID != "user-wsa" || got.WorkspaceID != "ws-a" {
		t.Fatalf("scope mismatch: %+v", got)
	}
	if got.TenantID != "tenant-a" {
		t.Fatalf("tenant = %q want tenant-a", got.TenantID)
	}
}

// TestAuthWorkspaceBindingRejectsScopeMismatch — 断言是真的但不覆盖请求范围时拒绝。
//
// ⚠ 这是「挪用」防线：拿自己在 ws-a 的断言去请求 ws-b。
func TestAuthWorkspaceBindingRejectsScopeMismatch(t *testing.T) {
	secret := []byte(strings.Repeat("c", 32))
	signer, err := identity.NewBindingSignerWithSecret(secret)
	if err != nil {
		t.Fatalf("NewBindingSignerWithSecret: %v", err)
	}
	signed, _, err := signer.Mint(identity.Member{
		WorkspaceID: "ws-a",
		UserID:      "user-wsa",
		Role:        identity.RoleOwner,
	}, "tenant-a", 0)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	t.Setenv("IDENTITY_SHARED_SECRET", string(secret))
	srv, _, _, tokens := newMobileRouteServer(t)
	h := srv.Handler()

	body, _ := json.Marshal(map[string]string{
		"binding":      signed,
		"user_id":      "user-wsa",
		"workspace_id": "ws-victim",
	})
	req := mobileRequest(http.MethodPost, "/api/auth/workspace-binding", tokens["ws-a"], string(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%q)", rr.Code, rr.Body.String())
	}
}
