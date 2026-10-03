package server

// Route + validation lock for POST /api/tasks/from-source (P2).
//
// The judgement is the same as the learning route test: with no store wired the
// endpoint answers 503, an unregistered one answers 404. A 404 here means the
// whole "source → task" pipeline is unreachable and the client would show a
// dead button on every note/email/meeting detail page.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaskFromSourceRouteIsRegistered(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	if srv.taskStore != nil {
		t.Skip("test server has a real task store; route-level assertion not applicable")
	}
	token := tokens[""]

	req, _ := http.NewRequest(http.MethodPost, "/api/tasks/from-source",
		strings.NewReader(`{"sourceKind":"email","sourceId":"em-1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code == http.StatusNotFound {
		t.Fatalf("POST /api/tasks/from-source = 404: the route is not registered")
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST /api/tasks/from-source = %d, want 503 when taskStore is nil: %s",
			rr.Code, rr.Body.String())
	}
}

// Validation runs before the store is touched, so these cases are observable
// even in the store-less test server. They are the guard against a client
// inventing an origin domain ("originKind must be one of ...").
func TestTaskFromSourceRejectsBadInput(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	token := tokens[""]

	cases := map[string]string{
		"unknown source kind": `{"sourceKind":"sms","sourceId":"x"}`,
		"missing source id":   `{"sourceKind":"email"}`,
		"invalid task type":   `{"sourceKind":"email","sourceId":"e1","type":"nonsense"}`,
		"broken json":         `{`,
	}
	for name, body := range cases {
		req, _ := http.NewRequest(http.MethodPost, "/api/tasks/from-source", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: POST /api/tasks/from-source = %d, want 400 (body %s)", name, rr.Code, body)
		}
	}
}

func TestTaskFromSourceRejectsNonPost(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	token := tokens[""]

	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPatch} {
		req, _ := http.NewRequest(method, "/api/tasks/from-source", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/tasks/from-source = %d, want 405", method, rr.Code)
		}
	}
}
