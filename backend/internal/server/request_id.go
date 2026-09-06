package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
)

type requestIDContextKey struct{}

type correlationIDContextKey struct{}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(requestID) {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		// v5 契约（03 文档 §2）：X-Correlation-ID 端到端链路 ID；入口缺失时生成，透传不改。
		correlationID := r.Header.Get("X-Correlation-ID")
		if !requestIDPattern.MatchString(correlationID) {
			correlationID = newCorrelationID()
		}
		w.Header().Set("X-Correlation-ID", correlationID)
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		ctx = context.WithValue(ctx, correlationIDContextKey{}, correlationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err == nil {
		return hex.EncodeToString(bytes)
	}
	return generateUUID()
}

func newCorrelationID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err == nil {
		return "cor-" + hex.EncodeToString(bytes)
	}
	return "cor-" + generateUUID()
}
