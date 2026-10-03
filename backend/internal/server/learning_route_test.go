package server

// Route-existence regression lock for the Learning Core
// (docs/学习muse/04-数据模型与API契约.md §2.2).
//
// The judgement is the same one the flashcards tests use: with no store
// injected, a *registered* route answers 503 ("not configured"), while an
// *unregistered* one answers 404. A 404 here means the whole learning module
// is unreachable — a client would render an empty hub with no error anywhere.
//
// The /api/learning/schedule route is the deliberate exception: it is a pure
// computation and must work with no store at all, so it is asserted to answer
// 200 with a real schedule.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLearningRoutesAreRegistered(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	if srv.learningService != nil {
		t.Skip("test server has a real learning service; route-level assertion not applicable")
	}
	token := tokens[""]

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req, _ := http.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}

	t.Run("collection and sub-routes answer 503, not 404", func(t *testing.T) {
		cases := []struct{ method, path, body string }{
			{http.MethodGet, "/api/learning/items", ""},
			{http.MethodPost, "/api/learning/items", `{"sourceKind":"email","sourceId":"e1","title":"t"}`},
			{http.MethodGet, "/api/learning/items/due", ""},
			{http.MethodPatch, "/api/learning/items/litem-1", `{"stage":"review"}`},
			{http.MethodGet, "/api/learning/reminders", ""},
			{http.MethodPost, "/api/learning/reminders", `{"kind":"daily_digest","ruleKind":"daily","ruleValue":"20:30","nextDueAt":1790000000}`},
			{http.MethodPost, "/api/learning/reminders/lrem-1/snooze", `{"minutes":30}`},
			{http.MethodPost, "/api/learning/reminders/lrem-1/ack", "{}"},
		}
		for _, tc := range cases {
			rr := do(tc.method, tc.path, tc.body)
			if rr.Code == http.StatusNotFound {
				t.Errorf("%s %s = 404: the route is not registered", tc.method, tc.path)
				continue
			}
			if rr.Code != http.StatusServiceUnavailable {
				t.Errorf("%s %s = %d, want 503 (learning service nil): %s", tc.method, tc.path, rr.Code, rr.Body.String())
			}
		}
	})

	t.Run("unknown sub-paths stay 404", func(t *testing.T) {
		for _, path := range []string{"/api/learning/nope", "/api/learning/items/x/y/z", "/api/learning/reminders/x/bogus"} {
			rr := do(http.MethodGet, path, "")
			if rr.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404 for an unknown sub-path", path, rr.Code)
			}
		}
	})
}

// The scheduler endpoint is store-free by design: a client can ask the server
// "what is the next state of this card" without any deployment having a
// database, and the answer must be a real schedule, not a stub.
func TestLearningScheduleWorksWithoutAStore(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	token := tokens[""]

	req, _ := http.NewRequest(http.MethodPost, "/api/learning/schedule",
		strings.NewReader(`{"state":2,"stability":10,"difficulty":5,"rating":3,"elapsedDays":10,"now":1790000000}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/learning/schedule = %d, want 200 without a store: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`"state":2`, `"stability"`, `"due":1790`} {
		if !strings.Contains(body, want) {
			t.Errorf("response %s does not contain %s", body, want)
		}
	}
}

func TestLearningScheduleRejectsOutOfRangeInput(t *testing.T) {
	srv, _, _, tokens := newMobileRouteServer(t)
	token := tokens[""]

	for name, body := range map[string]string{
		"rating too high": `{"state":2,"rating":9}`,
		"rating too low":  `{"state":2,"rating":0}`,
		"state too high":  `{"state":9,"rating":3}`,
		"state negative":  `{"state":-1,"rating":3}`,
	} {
		req, _ := http.NewRequest(http.MethodPost, "/api/learning/schedule", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: POST /api/learning/schedule = %d, want 400", name, rr.Code)
		}
	}
}
