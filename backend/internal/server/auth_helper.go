package server

import (
	"context"
	"net/http"
	"strings"
)

type authClaimsContextKey struct{}

// authClaims 是中间件注入到 request context 的身份信息。
type authClaims struct {
	UserID      string
	Role        string
	WorkspaceID string
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
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var token string

		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		}
		if token == "" && (r.URL.Path == "/ws" || r.URL.Path == "/plugin/ws" || strings.Contains(r.URL.Path, "/event")) {
			token = strings.TrimSpace(r.URL.Query().Get("token"))
		}
		if token == "" {
			s.writeStructuredError(w, r, http.StatusUnauthorized, CodeUnauthenticated, "missing authorization token")
			return
		}
		if s.jwtSigner == nil {
			s.writeStructuredError(w, r, http.StatusInternalServerError, CodeUpstreamUnavailable, "JWT signer not configured")
			return
		}

		claims, err := s.jwtSigner.Parse(token)
		if err != nil || claims.UserID == "" {
			s.writeStructuredError(w, r, http.StatusUnauthorized, CodeUnauthenticated, "invalid or expired token")
			return
		}

		ctx := context.WithValue(r.Context(), authClaimsContextKey{}, &authClaims{
			UserID:      claims.UserID,
			Role:        claims.Role,
			WorkspaceID: claims.WorkspaceID,
		})
		r.Header.Set("X-User-ID", claims.UserID)
		r.Header.Set("X-Tenant-ID", claims.WorkspaceID)
		r.Header.Set("X-User-Role", claims.Role)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}
