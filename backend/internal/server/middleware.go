package server

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
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

// loggingMiddleware 请求日志中间件。
//
// ⚠️ 2026-10-17 审计补上了两个缺口（原实现只记 >500ms 的慢请求）：
//
//  1. **5xx 一条都不记**。一个「瞬间 500」（连库失败、反序列化炸了、
//     handler 直接返回错误）在原实现下**不产生任何日志行** —— 因为它
//     快。凡是需要事后追查的故障恰恰是这类「失败得很快」的，
//     结果是：慢请求查得到，**真正在报错的请求查不到**。
//     ⇒ 5xx / panic 一律落日志，且先于 SLOW 判定返回（一个又慢又 5xx 的
//     请求只记一条 ERROR，不会被拆成两行）。
//  2. **没有关联 ID**。`requestIDMiddleware` 是最外层（见 server.go 的组装
//     顺序 `requestIDMiddleware(requestBodyLimitMiddleware(handler))`），
//     所以本中间件执行时 ctx 里已经有 request_id，但原实现没打印它
//     ⇒ 日志和客户端看到的 `X-Request-ID` 对不上，只能靠时间戳猜。
//
// 保持不变的部分：只记「慢」与「错」，**不记正常请求** —— 200 的常规流量
// 打全量日志会把真正的错误淹掉，那是另一个量级的取舍，不在这里做。
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		duration := time.Since(start)

		// req= 取不到时（中间件顺序被改动、或单测直接调用）退化成 "-"，
		// 而不是打一个空字符串 —— 空串会让 grep `req=` 匹配到所有行。
		reqID := s.requestIDFromContext(r)
		if reqID == "" {
			reqID = "-"
		}

		// 服务端错误优先判定并返回：又慢又 5xx 只记一条，避免同一请求两行。
		if rw.statusCode >= http.StatusInternalServerError {
			log.Printf("[ERROR] %s %s - %d (%v) req=%s",
				r.Method, r.URL.Path, rw.statusCode, duration, reqID)
			return
		}

		// 慢请求日志（超过 slowRequestThreshold）
		if duration > s.slowRequestThreshold() {
			log.Printf("[SLOW] %s %s - %d (%v) req=%s",
				r.Method, r.URL.Path, rw.statusCode, duration, reqID)
		}
	})
}

// defaultSlowRequestThreshold 是「慢请求」的默认判据。
//
// 此前这个值硬编码在 if 里（500ms），运维改不了 —— 而不同部署的正常
// 上限差别很大：桌面本地跑 ASR 和生产网关不是一个量级。
// 留成可配置不是为了「配置项越多越好」，而是因为不可改的阈值会在
// 两种方向上都失效：调高了漏掉真慢的，调低了被正常请求刷屏。
const defaultSlowRequestThreshold = 500 * time.Millisecond

// slowRequestThreshold 读 POCKET_SLOW_REQUEST_MS；非法/未设置时回落到默认值。
//
// ⚠️ 每次请求都读一次环境变量是刻意的：它没有热路径开销问题（os.Getenv
// 就是一次 map 查找），而缓存它意味着测试里改 env 之后要另找重置入口。
//
// ⚠️ 本键**故意不走** internal/config（仓内大多数 POCKET_* 走那里）：
// check-env-example 门会把它列进「在 .env.example 但 config 不读」的 note 一栏。
// 留在这里是因为它是**日志阈值**而不是业务配置 —— 它不影响任何请求的成败，
// 不该混进决定服务行为的配置结构体里。该门对 note 不判红，故不构成违规。
func (s *Server) slowRequestThreshold() time.Duration {
	raw := os.Getenv("POCKET_SLOW_REQUEST_MS")
	if raw == "" {
		return defaultSlowRequestThreshold
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		// 配错了就回默认，并把配错这件事说出来 —— 静默回落到一个
		// 没人知道的阈值，正是本次审计在修的那类「悄悄生效的默认值」。
		log.Printf("[WARN] POCKET_SLOW_REQUEST_MS=%q 非法（需正整数毫秒），沿用 %v", raw, defaultSlowRequestThreshold)
		return defaultSlowRequestThreshold
	}
	return time.Duration(ms) * time.Millisecond
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
