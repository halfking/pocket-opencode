package server

import (
	"net/http"
	"strings"
)

// requireAuth 中间件：验证 JWT，未认证返回 401。
//
// Phase 1 实现：从 Authorization: Bearer <JWT> 或查询参数 token 解析并验证 token。
// 验证失败时返回 401 Unauthorized，前端应重定向到登录页。
// 
// 支持两种token传递方式:
// 1. Authorization header: Bearer <token>
// 2. Query parameter: ?token=<token> (用于WebSocket连接)
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var token string
		
		// 优先从Authorization header获取token
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimSpace(auth[len("Bearer "):])
		}
		
		// 如果header中没有token，尝试从查询参数获取（用于WebSocket）
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		
		// 如果两处都没有token，返回401
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing authorization token")
			return
		}

		if s.jwtSigner == nil {
			writeError(w, http.StatusInternalServerError, "JWT signer not configured")
			return
		}

		claims, err := s.jwtSigner.Parse(token)
		if err != nil || claims.UserID == "" {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		// JWT 验证通过，继续处理请求
		// 注意：claims.UserID 可通过 s.userIDFromRequest(r) 在 handler 中获取
		next.ServeHTTP(w, r)
	}
}
