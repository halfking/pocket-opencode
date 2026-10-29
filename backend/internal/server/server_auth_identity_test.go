package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAuthIdentityReturnsServerDerivedOwnership — GET /api/auth/identity
//
// 这个端点是「服务间可信验证」的落点：客户端恢复会话后用它把
// **客户端自报的** workspaceId 换成**服务端从已验证 JWT 推导**的归属。
// 因此它的正确性直接决定本地配置表的归属键是否可信。
//
// 必须覆盖：
//  1. 带 workspace 的 token → 返回该 user/workspace
//  2. 不带 workspace 的 token → 回退 "default"（与 workspaceIDFromRequest 同口径）
//  3. 无 token → 401（调用方必须能区分「没证明」与「会话死了」）
func TestAuthIdentityReturnsServerDerivedOwnership(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	h := srv.Handler()

	type want struct {
		userID      string
		workspaceID string
	}
	cases := []struct {
		name  string
		token string
		want  want
}{
		{
			name:  "workspace from verified jwt",
			token: tokens["ws-a"],
			want:  want{userID: "user-wsa", workspaceID: "ws-a"},
		},
		{
			name:  "different workspace stays different",
			token: tokens["ws-b"],
			want:  want{userID: "user-wsb", workspaceID: "ws-b"},
		},
		{
			// 空 workspace 回退 "default"：本地归属键与服务端作用域
			// 必须算出同一个 workspace，否则同一条数据会写两遍。
			name:  "missing workspace falls back to default",
			token: tokens[""],
			want:  want{userID: "user-", workspaceID: "default"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := mobileRequest(http.MethodGet, "/api/auth/identity", tc.token, "")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%q)", rr.Code, rr.Body.String())
			}
			var got struct {
				UserID      string `json:"user_id"`
				WorkspaceID string `json:"workspace_id"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v body=%q", err, rr.Body.String())
			}
			if got.UserID != tc.want.userID {
				t.Errorf("user_id = %q, want %q", got.UserID, tc.want.userID)
			}
			if got.WorkspaceID != tc.want.workspaceID {
				t.Errorf("workspace_id = %q, want %q", got.WorkspaceID, tc.want.workspaceID)
			}
		})
	}
}

// TestAuthIdentityRejectsMissingOrInvalidToken — 归属证明必须 fail-closed。
//
// 客户端恢复会话时用本端点重新确立归属。若未认证时也返回 200，
// 客户端会把「未认领的历史行」挂到一个错误身份上 ⇒ 跨账号泄漏。
func TestAuthIdentityRejectsMissingOrInvalidToken(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	h := srv.Handler()

	for _, tc := range []struct {
		name  string
		token string
	}{
		{name: "no token", token: ""},
		{name: "garbage token", token: "not-a-jwt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := mobileRequest(http.MethodGet, "/api/auth/identity", tc.token, "")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body=%q)", rr.Code, rr.Body.String())
			}
		})
	}
}

// TestAuthIdentityRejectsNonGet — 本端点是只读证明端点。
func TestAuthIdentityRejectsNonGet(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	h := srv.Handler()

	req := mobileRequest(http.MethodPost, "/api/auth/identity", tokens["ws-a"], "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (body=%q)", rr.Code, rr.Body.String())
	}
}
