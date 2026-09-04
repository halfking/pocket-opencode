package server

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"
)

// responseWriter 包装 http.ResponseWriter 以捕获状态码
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Hijack 透传给底层 ResponseWriter。
// gorilla/websocket 的 Upgrade 要求 http.Hijacker；不透传会把 /ws 等长连接
// 升级打成 500（response does not implement http.Hijacker）。
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("responseWriter: underlying %T does not implement http.Hijacker", rw.ResponseWriter)
	}
	return h.Hijack()
}

// Flush 透传：SSE / chunked 响应依赖 Flusher 逐段推送。
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 透传底层 writer：http.ResponseController 经 Unwrap 链穿透包装层，
// longLivedPathMiddleware 对 SSE 路径的 SetWriteDeadline(time.Time{})（豁免
// http.Server 30s WriteTimeout）才能到达真正的连接。缺了它该调用静默失败、
// 30s 写死线仍然生效——表现为 SSE 首帧之后任何跨过 30s 的下一次写都会掐断
// 连接（2026-09-05 实测：auto 回退链 20s 首帧正常、40s 第二帧写失败、
// 请求 ctx 被 canceled，整条 45s 预算的回退链永远走不完）。
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// loggingMiddleware 请求日志中间件
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		duration := time.Since(start)

		// 慢请求日志（超过 500ms）
		if duration > 500*time.Millisecond {
			log.Printf("[SLOW] %s %s - %d (%v)", r.Method, r.URL.Path, rw.statusCode, duration)
		}
	})
}

// recoveryMiddleware 崩溃恢复中间件
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("[PANIC] %s %s: %v", r.Method, r.URL.Path, err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
