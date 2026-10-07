package server

import (
	"os"
	"testing"
)

// LIVE end-to-end check against the running agent-companion daemon.
//
// Skipped unless AC_E2E_COMPANION_URL is set, so it never runs in CI:
//
//	AC_E2E_COMPANION_URL=http://127.0.0.1:8095 \
//	AC_E2E_COMPANION_SECRET=<AC_API_SECRET> \
//	go test ./internal/server/ -run TestLiveCompanion -v
//
// Why this exists alongside the httptest-based contract tests: those prove
// our struct tags agree with fixtures WE wrote. That is not evidence that
// the two services agree — the failure they cannot catch is a field rename
// on the companion side, which yields HTTP 200 plus a zero-valued struct
// and renders as "0 sessions" on a host with hundreds of them.
//
// The specific regression behind it: CompanionURL was read into config but
// never used to construct a client, so s.companion was always nil and the
// whole transcript/session read path was dead code that failed silently.
func TestLiveCompanionSessionInventory(t *testing.T) {
	url := os.Getenv("AC_E2E_COMPANION_URL")
	if url == "" {
		t.Skip("set AC_E2E_COMPANION_URL to run the live companion check")
	}
	c := NewCompanionClient(url, os.Getenv("AC_E2E_COMPANION_SECRET"))
	if c == nil {
		t.Fatal("nil client")
	}

	list, err := c.ListNativeSessions("mmcode", 10, 0)
	if err != nil {
		t.Fatalf("ListNativeSessions(mmcode): %v", err)
	}
	t.Logf("mmcode sessions: total=%d returned=%d scanned=%v", list.Total, len(list.Sessions), list.Meta.Scanned)
	if !list.Meta.Scanned {
		t.Error("companion reports scanned=false — the store has not indexed yet")
	}
	if list.Total == 0 {
		t.Fatal("no mmcode sessions from the live daemon — the SQLite source is not reaching the API")
	}
	s := list.Sessions[0]
	t.Logf("first: id=%s kind=%s agent=%s resumable=%v project=%s", s.ID, s.Kind, s.Agent, s.Resumable, s.Project)
	if s.ID == "" || s.Kind != "mmcode" {
		t.Fatalf("unexpected first session: %+v", s)
	}
	if s.Agent == "" || s.Title == "" {
		t.Errorf("session missing display fields: %+v", s)
	}

	// The session↔task join must at least be readable (it may be empty
	// when ACC is not wired; that is a different fact from "broken").
	links, err := c.ListSessionLinks()
	if err != nil {
		t.Errorf("ListSessionLinks: %v", err)
	}
	t.Logf("session links: %d", len(links))

	// Task history: the field names here were wrong until a live run
	// proved it, so the assertions are on decoded values, not just err.
	runs, err := c.ListRuns(5)
	if err != nil {
		t.Errorf("ListRuns: %v", err)
		return
	}
	t.Logf("runs: %d", len(runs))
	for _, r := range runs {
		if r.RunID == "" {
			t.Fatalf("run row decoded to an empty RunID: %+v — field names drifted again", r)
		}
		t.Logf("  run=%s state=%s kind=%s retained=%v", r.RunID[:16], r.State, r.Kind, r.Retained)
	}
}
