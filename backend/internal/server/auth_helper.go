package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
)

type authClaimsContextKey struct{}

// authClaims 是中间件注入到 request context 的身份信息。
type authClaims struct {
	UserID      string
	Role        string
	WorkspaceID string
	TenantID    string
}

// claimsFromContext 从 request context 提取已认证的 claims。
// 如果 request 未经过 requireAuth 中间件，返回 nil。
func (s *Server) claimsFromContext(r *http.Request) *authClaims {
	v := r.Context().Value(authClaimsContextKey{})
	if v == nil {
		return nil
	}
	c, ok := v.(*authClaims)
	if !ok {
		return nil
	}
	return c
}

// requireAuth 中间件：验证 JWT，未认证返回 401。
//
// Authorization: Bearer <JWT> 是普通 HTTP 请求的唯一凭证来源。
// WebSocket 握手允许 /ws 和 /plugin/ws 使用 query token，因为浏览器 WebSocket
// API 无法设置 Authorization header；其它 HTTP 路由不接受 URL 中的 token。
//
// 验证路径：
//   1. IDENTITY_SHARED_SECRET 已配置 → identity-go 多 issuer 校验
//      (pocket / memora / llm-gateway / redclaw / acc 任意一枚签名 + aud=pocket-api 通过)
//   2. 否则降级到本地 jwtSigner.Parse（向后兼容窗口期）。
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var raw string

		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(authHeader, "Bearer ") {
			raw = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		}
		if raw == "" && (r.URL.Path == "/ws" || r.URL.Path == "/plugin/ws" || strings.Contains(r.URL.Path, "/event")) {
			raw = strings.TrimSpace(r.URL.Query().Get("token"))
		}
		if raw == "" {
			s.writeStructuredError(w, r, http.StatusUnauthorized, CodeUnauthenticated, "missing authorization token")
			return
		}
		if s.jwtSigner == nil {
			s.writeStructuredError(w, r, http.StatusInternalServerError, CodeUpstreamUnavailable, "JWT signer not configured")
			return
		}

		claims, err := auth.VerifyToken(s.jwtSigner, raw)
		if err != nil || claims == nil || strings.TrimSpace(claims.UserID) == "" {
			s.writeStructuredError(w, r, http.StatusUnauthorized, CodeUnauthenticated, "invalid or expired token")
			return
		}

		wsID := strings.TrimSpace(claims.WorkspaceID)
		// 注意：这里不把空 WorkspaceID 强制设为 "default"——下游
		// workspaceIDFromRequest 已经会把空值降级到 "default"，而某些路由
		// （如 mobile/sessions）需要在缺 workspace 时返回 400/CodeWorkspaceRequired
		// 来 fail-closed，所以保留原值让 handler 自己决定。
		ctx := context.WithValue(r.Context(), authClaimsContextKey{}, &authClaims{
			UserID:      claims.UserID,
			Role:        claims.Role,
			WorkspaceID: wsID,
			TenantID:    claims.TenantID,
		})
		r.Header.Set("X-User-ID", claims.UserID)
		r.Header.Set("X-Tenant-ID", wsID)
		r.Header.Set("X-User-Role", claims.Role)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}
