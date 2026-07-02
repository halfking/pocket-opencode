package auth

import (
	"net/http"
	"strings"
)

// Middleware 是可选的中间件骨架（当前未使用，预留给 Phase 1 后期）。
// 目前 server_assistant.go 的 handlers 直接调用 requireUserIDFromAuth。
func Middleware(signer *Signer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		tok := strings.TrimSpace(auth[len("Bearer "):])
		claims, err := signer.Parse(tok)
		if err != nil || claims.UserID == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		// 可选：把 claims 塞到 context，暂时不需要
		next.ServeHTTP(w, r)
	})
}
