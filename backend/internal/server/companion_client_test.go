package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests pin the WIRE contract with agent-companion, not just our own
// struct definitions. The failure this guards against is concrete: the
// client is constructed, returns HTTP 200, and unmarshals into a
// zero-valued struct because a field name drifted — the caller then renders
// "0 sessions" on a host that has hundreds, with no error anywhere.
//
// companion is the authority for sessions/runs; if its envelope changes,
// this test is where that must surface.

func newCompanionTestClient(t *testing.T, h http.HandlerFunc) (*CompanionClient, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	c := NewCompanionClient(srv.URL, "test-secret")
	if c == nil {
		t.Fatal("NewCompanionClient returned nil for a non-empty URL")
	}
	return c, srv.Close
}

// The list envelope is the load-bearing one: sessions + total + meta.
func TestCompanionClient_ListNativeSessions_ParsesEnvelope(t *testing.T) {
	var gotAuth, gotKind string
	c, done := newCompanionTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mobile/native/sessions" {
			t.Errorf("path = %q, want /api/mobile/native/sessions", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotKind = r.URL.Query().Get("kind")
		w.Header().Set("Content-Type", "application/json")
		// Shaped exactly like the live service's mmcode rows.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sessions": []map[string]any{{
				"id":         "mvs_3e839a8d27f64737ab4a37772ffd074e",
				"kind":       "mmcode",
				"agent":      "MiniMax Code",
				"driverKind": "mmcode",
				"resumable":  true,
				"title":      "优化会议录音转写、总结与日程规划",
				"project":    "/w/openpocket",
				"tags":       []string{"started", "pi-agent"},
				"updatedAt":  1791350993,
				"source":     "runtime_state_db",
			}},
			"total":  500,
			"limit":  20,
			"offset": 0,
			"meta": map[string]any{
				"scanned": true, "scannedAt": "2026-10-07T13:29:58Z",
				"sessions": 2872, "lastRefreshMs": 2805,
			},
		})
	})
	defer done()

	list, err := c.ListNativeSessions("mmcode", 20, 0)
	if err != nil {
		t.Fatalf("ListNativeSessions: %v", err)
	}
	if gotAuth != "Bearer test-secret" {
		t.Errorf("Authorization = %q, want the bearer token", gotAuth)
	}
	if gotKind != "mmcode" {
		t.Errorf("kind query = %q, want mmcode", gotKind)
	}
	if list.Total != 500 {
		t.Errorf("Total = %d, want 500", list.Total)
	}
	if len(list.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(list.Sessions))
	}
	s := list.Sessions[0]
	if s.ID != "mvs_3e839a8d27f64737ab4a37772ffd074e" {
		t.Errorf("ID = %q", s.ID)
	}
	if s.Agent != "MiniMax Code" {
		t.Errorf("Agent = %q, want \"MiniMax Code\"", s.Agent)
	}
	if s.DriverKind != "mmcode" {
		t.Errorf("DriverKind = %q, want mmcode — the operate key", s.DriverKind)
	}
	if !s.Resumable {
		t.Error("Resumable = false")
	}
	if s.Project != "/w/openpocket" {
		t.Errorf("Project = %q", s.Project)
	}
	if s.Source != "runtime_state_db" {
		t.Errorf("Source = %q, want runtime_state_db", s.Source)
	}
	if len(s.Tags) != 2 || s.Tags[0] != "started" {
		t.Errorf("Tags = %v, want [started pi-agent]", s.Tags)
	}
	// scanned must survive: an empty list with scanned=false means "not
	// indexed yet", which must be distinguishable from "no sessions".
	if !list.Meta.Scanned {
		t.Error("Meta.Scanned = false, want true")
	}
	if list.Meta.Sessions != 2872 {
		t.Errorf("Meta.Sessions = %d, want 2872", list.Meta.Sessions)
	}
}

func TestCompanionClient_ListNativeSessions_PropagatesError(t *testing.T) {
	// A 502 from companion (e.g. ACC not configured) must surface, not be
	// swallowed into an empty list — an empty list and a failed list are
	// different facts for the caller.
	c, done := newCompanionTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"acctask: http 404"}`))
	})
	defer done()

	if _, err := c.ListNativeSessions("mmcode", 0, 0); err == nil {
		t.Fatal("expected an error for a 502 response, got nil")
	}
}

func TestCompanionClient_NilIsNotConfigured(t *testing.T) {
	var c *CompanionClient
	if _, err := c.ListNativeSessions("mmcode", 0, 0); err == nil {
		t.Fatal("a nil client must report \"not configured\" rather than panic")
	}
	if NewCompanionClient("  ", "s") != nil {
		t.Error("blank URL must yield a nil client so callers can skip it")
	}
}

func TestCompanionClient_SessionLinksParses(t *testing.T) {
	c, done := newCompanionTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mobile/session-links" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"links":[{"kind":"mmcode","sessionId":"mvs_x","taskId":"tsk_1","projectId":"p1","updatedAt":1791350993}],"total":1}`))
	})
	defer done()

	links, err := c.ListSessionLinks()
	if err != nil {
		t.Fatalf("ListSessionLinks: %v", err)
	}
	if len(links) != 1 || links[0].TaskID != "tsk_1" || links[0].SessionID != "mvs_x" {
		t.Errorf("links = %+v, want the session↔task binding", links)
	}
}

func TestCompanionClient_GetRunUsesFlatSnapshotShape(t *testing.T) {
	// Pinned against the live daemon: GET /runs/:id answers a FLAT
	// snake_case snapshot. Decoding it as {"run": …} yields a
	// zero-valued struct with no error — the run simply vanishes.
	c, done := newCompanionTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"run_id":"run-1","dispatch_id":"d-1","agent_id":"opencode",` +
			`"kind":"opencode","state":"cancelled","session_id":"ses_x","updates":3,"retained":true}`))
	})
	defer done()

	run, err := c.GetRun("run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.RunID != "run-1" {
		t.Fatalf("RunID = %q — the flat snapshot was not decoded (envelope assumption?)", run.RunID)
	}
	if run.State != "cancelled" {
		t.Errorf("State = %q, want cancelled", run.State)
	}
	if run.Kind != "opencode" || run.SessionID != "ses_x" {
		t.Errorf("kind/session = %q/%q", run.Kind, run.SessionID)
	}
	if run.Updates != 3 {
		t.Errorf("Updates = %d, want 3", run.Updates)
	}
	if !run.Retained {
		t.Error("Retained = false, want true for a terminal history row")
	}
}

func TestCompanionClient_ListReadsHistory(t *testing.T) {
	// The "sync my task history" surface: a client that only ever polls
	// GetRun cannot discover runs it did not start.
	c, done := newCompanionTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/runs" {
			t.Errorf("path = %q, want /api/v1/runs", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "5" {
			t.Errorf("limit = %q, want 5", got)
		}
		_, _ = w.Write([]byte(`{"runs":[{"run_id":"run-a","state":"completed","kind":"opencode"},` +
			`{"run_id":"run-b","state":"cancelled","kind":"mmcode","retained":true}],"total":2}`))
	})
	defer done()

	runs, err := c.ListRuns(5)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	if runs[0].RunID != "run-a" || runs[1].RunID != "run-b" {
		t.Errorf("ids = %q,%q", runs[0].RunID, runs[1].RunID)
	}
	if runs[1].State != "cancelled" || runs[1].Kind != "mmcode" {
		t.Errorf("second row = %+v", runs[1])
	}
}

func TestCompanionClient_OperateUsesCamelCaseReceipt(t *testing.T) {
	// The operate response is camelCase, unlike the run reads. Sharing one
	// struct across both is how the receipt's RunID comes out empty.
	c, done := newCompanionTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/native/sessions/ses_x/operate" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"runId":"run-9","state":"queued","agentKind":"opencode","nativeSessionId":"ses_x"}`))
	})
	defer done()

	rec, err := c.OperateSession("opencode", "ses_x", "hello", "op-1")
	if err != nil {
		t.Fatalf("OperateSession: %v", err)
	}
	if rec.RunID != "run-9" {
		t.Errorf("RunID = %q, want run-9", rec.RunID)
	}
	if rec.State != "queued" || rec.AgentKind != "opencode" {
		t.Errorf("receipt = %+v", rec)
	}
}

func TestCompanionClient_OperatePropagatesError(t *testing.T) {
	// A refused dispatch must surface, not decode into a zero receipt.
	c, done := newCompanionTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"no drivable agent registered for kind \"mmcode\""}`))
	})
	defer done()

	if _, err := c.OperateSession("mmcode", "ses_x", "hi", ""); err == nil {
		t.Fatal("expected an error for a 409 response")
	}
}

// The disk- prefix is openpocket-local vocabulary that must be stripped
// before hitting companion, which speaks plain native kinds.
func TestNativeAgentKind_StripsDiskPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"disk-mmcode": "mmcode",
		"mmcode":      "mmcode",
		"  claude  ":  "claude",
		"":            "",
	} {
		if got := nativeAgentKind(in); got != want {
			t.Errorf("nativeAgentKind(%q) = %q, want %q", in, got, want)
		}
	}
}

// The regression this whole file exists for: before the wiring fix,
// NewCompanionClient was never called, so s.companion was always nil and
// every transcript/session read silently returned nothing.
func TestSetCompanionClient_WiresTheClient(t *testing.T) {
	s := &Server{}
	if s.companion != nil {
		t.Fatal("a fresh server must start with no companion client")
	}
	c := NewCompanionClient("http://127.0.0.1:8095", "secret")
	s.SetCompanionClient(c)
	if s.companion == nil {
		t.Fatal("SetCompanionClient did not attach the client")
	}
	// A nil client must be assignable without clearing a live one, so a
	// later config reload cannot silently disable the integration.
	s.SetCompanionClient(nil)
	if s.companion == nil {
		t.Error("SetCompanionClient(nil) cleared an existing client")
	}
}
