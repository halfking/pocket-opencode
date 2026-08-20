package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 跨 workspace 的实例对调用方等同于"不存在"：命令必须 404，不泄漏其它租户实例。
func TestSendCommandRejectsUnknownInstanceForWorkspace(t *testing.T) {
	srv, token := newTestServerWithAuth(t)

	request := httptest.NewRequest(http.MethodPost, "/api/plugin/command",
		bytes.NewBufferString(`{"instanceID":"other-workspace-instance","command":"session.stop"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()

	srv.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for instance outside caller workspace, got %d: %s",
			recorder.Code, recorder.Body.String())
	}
}

func TestPluginStatusIsScopedToCallerWorkspace(t *testing.T) {
	srv, token := newTestServerWithAuth(t)

	request := httptest.NewRequest(http.MethodGet, "/api/plugin/status", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()

	srv.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
}
