package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/usersetting"
)

func TestUserSettingsLWWConflict(t *testing.T) {
	srv, token := newTestServerWithAuth(t)
	mem := usersetting.NewMemStore()
	srv.SetUserSettingsStore(mem)
	if _, err := mem.Put(usersetting.Record{
		UserID: "test-user", WorkspaceID: "test-workspace",
		Namespace: "app_prefs", ID: "default",
		Payload:   json.RawMessage(`{"theme":"dark"}`),
		UpdatedAt: 20,
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/user-settings/app_prefs/default",
		bytes.NewBufferString(`{"payload":{"theme":"light"},"updatedAt":10}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserSettingsNewerClientApplies(t *testing.T) {
	srv, token := newTestServerWithAuth(t)
	mem := usersetting.NewMemStore()
	srv.SetUserSettingsStore(mem)

	req := httptest.NewRequest(http.MethodPut, "/api/user-settings/chat_settings/default",
		bytes.NewBufferString(`{"payload":{"defaultModel":"glm-5.2"},"updatedAt":30}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/user-settings", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var body struct {
		Settings []usersetting.Record `json:"settings"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Settings) != 1 || body.Settings[0].UpdatedAt != 30 {
		t.Fatalf("unexpected list: %+v", body.Settings)
	}
}
