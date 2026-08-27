package server

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestBodyLimitMiddlewareRejectsOversizedBody(t *testing.T) {
	handler := requestBodyLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err == nil {
			t.Fatal("expected oversized body read to fail")
		}
		if _, ok := err.(*http.MaxBytesError); !ok {
			t.Fatalf("expected MaxBytesError, got %T: %v", err, err)
		}
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(strings.Repeat("x", maxRequestBodyBytes+1)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
}

func TestRequestBodyLimitMiddlewareAllowsAudioLimit(t *testing.T) {
	handler := requestBodyLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if len(body) != 5 {
			t.Fatalf("expected 5 bytes, got %d", len(body))
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/stt/transcribe", strings.NewReader("12345"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

// /api/llm/stream 多模态允许超过通用 2 MiB 限额，否则带图请求会被通用中间件
// 直接拦截——与后端 handler 自身的 image size 校验重复且不一致。
func TestRequestBodyLimitMiddlewareAllowsChatMultimodalBody(t *testing.T) {
	// 模拟 4 张 6 MiB 的图片，最坏情况下请求体 ~32 MiB（超出 2 MiB 通用限制）。
	body := strings.Repeat("x", (maxChatBodyBytes - 1<<10))
	handler := requestBodyLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Fatalf("read body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/llm/stream", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

// 防止 /api/llm/stream 的多模态限额被回滚到通用 2 MiB：超出 chat 限额的
// 请求仍必须被拒绝并产出 MaxBytesError，避免静默截断破坏流式响应头。
func TestRequestBodyLimitMiddlewareRejectsOversizedChatBody(t *testing.T) {
	handler := requestBodyLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err == nil {
			t.Fatal("expected oversized body read to fail")
		} else if _, ok := err.(*http.MaxBytesError); !ok {
			t.Fatalf("expected MaxBytesError, got %T: %v", err, err)
		}
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/llm/stream", strings.NewReader(strings.Repeat("y", maxChatBodyBytes+1)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
}
func TestRecoveryMiddleware(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	wrapped := recoveryMiddleware(handler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestResponseWriter(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	})

	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}
	handler.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/", nil))

	if rw.statusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rw.statusCode)
	}
}

func TestResponseWriterDefaultStatus(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}
	handler.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/", nil))

	if rw.statusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", rw.statusCode)
	}
}

// hijackableRecorder 模拟同时支持 Hijack/Flush 的底层 ResponseWriter
// （httptest.ResponseRecorder 两者都不支持，不能直接用）。
type hijackableRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (r *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	r.hijacked = true
	return nil, nil, nil
}

func (r *hijackableRecorder) Flush() {}

// TestResponseWriterPreservesHijacker 回归测试：loggingMiddleware 的
// responseWriter 包装层必须透传 http.Hijacker，否则 /ws 的 gorilla
// Upgrade 会得到 "response does not implement http.Hijacker" 500。
func TestResponseWriterPreservesHijacker(t *testing.T) {
	inner := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	rw := &responseWriter{ResponseWriter: inner}

	if _, ok := interface{}(rw).(http.Hijacker); !ok {
		t.Fatal("responseWriter must implement http.Hijacker for WebSocket upgrades")
	}
	if _, ok := interface{}(rw).(http.Flusher); !ok {
		t.Fatal("responseWriter must implement http.Flusher for SSE streaming")
	}

	if _, _, err := rw.Hijack(); err != nil {
		t.Fatalf("Hijack passthrough failed: %v", err)
	}
	if !inner.hijacked {
		t.Fatal("Hijack did not reach the underlying ResponseWriter")
	}
}

// TestResponseWriterHijackErrorOnNonHijackableUnderlying 验证底层不支持时
// 返回错误而不是 panic。
func TestResponseWriterHijackErrorOnNonHijackableUnderlying(t *testing.T) {
	rw := &responseWriter{ResponseWriter: httptest.NewRecorder()}
	if _, _, err := rw.Hijack(); err == nil {
		t.Fatal("expected error when underlying ResponseWriter lacks Hijacker")
	}
}
