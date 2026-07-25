package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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