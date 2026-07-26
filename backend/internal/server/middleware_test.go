package server

import (
	"io"
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
