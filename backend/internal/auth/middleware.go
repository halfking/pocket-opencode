package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const ClaimsContextKey contextKey = "auth_claims"

// Middleware validates JWT tokens from the Authorization header and injects
// claims into the request context for downstream handlers.
// Returns 401 Unauthorized if the token is missing, invalid, or expired.
func Middleware(signer *Signer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "Unauthorized: missing or invalid Authorization header", http.StatusUnauthorized)
			return
		}
		tok := strings.TrimSpace(auth[len("Bearer "):])
		if tok == "" {
			http.Error(w, "Unauthorized: empty token", http.StatusUnauthorized)
			return
		}
		claims, err := signer.Parse(tok)
		if err != nil {
			http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
			return
		}
		if claims.UserID == "" {
			http.Error(w, "Unauthorized: invalid claims", http.StatusUnauthorized)
			return
		}
		// Inject claims into context for downstream handlers
		ctx := context.WithValue(r.Context(), ClaimsContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetClaims extracts Claims from the request context.
// Returns nil if claims are not present (middleware not applied or auth failed).
func GetClaims(ctx context.Context) *Claims {
	claims, ok := ctx.Value(ClaimsContextKey).(*Claims)
	if !ok {
		return nil
	}
	return claims
}
