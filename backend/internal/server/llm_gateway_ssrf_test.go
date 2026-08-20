package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLLMGatewayConfigRejectsSSRFURLBeforeSaving(t *testing.T) {
	srv, token := newTestServerWithAuth(t)
	request := httptest.NewRequest(http.MethodPost, "/api/llm-gateway/config", bytes.NewBufferString(`{"baseURL":"http://127.0.0.1:1"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()

	srv.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
}
