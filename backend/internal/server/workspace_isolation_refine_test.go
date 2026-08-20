package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

// TestMeetingRefineWorkspaceIsolation covers audit P1-1: finalizeMeetingRefine
// used to hardcode workspace "default", so a refine issued by an S0-A client
// (workspace ws_<userID>) never applied Status="refined" to the real meeting and
// instead fabricated a stub row in the "default" workspace.
func TestMeetingRefineWorkspaceIsolation(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	h := srv.Handler()

	createMeeting := func(token, title string) *meeting.Meeting {
		t.Helper()
		rr := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings", token, `{"title":"`+title+`"}`)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create meeting status=%d body=%s", rr.Code, rr.Body.String())
		}
		var m meeting.Meeting
		if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode meeting: %v", err)
		}
		return &m
	}

	meetingA := createMeeting(tokens["ws-a"], "refine workspace A")
	if meetingA.WorkspaceID != "ws-a" || meetingA.Status != "recording" {
		t.Fatalf("seed meeting A=%+v, want workspace ws-a status recording", meetingA)
	}

	// (a)+(c) A refine finalized under the caller's real workspace must flip the
	// existing record to "refined" in that same workspace.
	refineResult := map[string]any{
		"refined_transcript": "张三: 我们下周发布。",
		"todos":              []any{},
	}
	srv.finalizeMeetingRefine(context.Background(), "shared-user", "ws-a", meetingA.ID, refineResult, meetingMetaIn{Title: "refine workspace A"})

	refined, err := srv.meetingStore.GetScoped(meetingA.ID, "shared-user", "ws-a")
	if err != nil {
		t.Fatalf("meeting A missing from ws-a after refine: %v", err)
	}
	if refined.Status != "refined" {
		t.Fatalf("meeting A status=%q after refine, want %q (P1-1 regression: status write went to another workspace)", refined.Status, "refined")
	}

	// (d) No spurious stub may appear in the legacy "default" workspace under
	// either the authenticated user or the legacy "local" owner.
	for _, owner := range []string{"shared-user", "local"} {
		strays, lerr := srv.meetingStore.ListScoped(owner, "default")
		if lerr != nil {
			t.Fatalf("list default workspace for owner %s: %v", owner, lerr)
		}
		if len(strays) != 0 {
			t.Fatalf("refine leaked %d meeting(s) into default workspace (owner=%s): %+v", len(strays), owner, strays)
		}
	}

	// The owning workspace must still hold exactly one meeting — no duplicate.
	own, err := srv.meetingStore.ListScoped("shared-user", "ws-a")
	if err != nil {
		t.Fatalf("list ws-a: %v", err)
	}
	if len(own) != 1 || own[0].ID != meetingA.ID {
		t.Fatalf("ws-a should hold exactly the original meeting, got %+v", own)
	}

	// A refine targeting a meeting that is not visible in the given workspace
	// must not create anything anywhere.
	srv.finalizeMeetingRefine(context.Background(), "shared-user", "ws-b", meetingA.ID, map[string]any{"refined_transcript": "x"}, meetingMetaIn{Title: "cross"})
	crossList, err := srv.meetingStore.ListScoped("shared-user", "ws-b")
	if err != nil {
		t.Fatalf("list ws-b: %v", err)
	}
	if len(crossList) != 0 {
		t.Fatalf("cross-workspace refine fabricated %d meeting(s) in ws-b: %+v", len(crossList), crossList)
	}
	stillRefined, err := srv.meetingStore.GetScoped(meetingA.ID, "shared-user", "ws-a")
	if err != nil || stillRefined.Title != "refine workspace A" {
		t.Fatalf("cross-workspace refine mutated the owning meeting: %+v err=%v", stillRefined, err)
	}
}

// TestMeetingRefineCrossWorkspaceHTTP is the bonus negative case: POST
// /api/meetings/{id}/refine signed for one workspace must 404 on another
// workspace's meeting, before any upstream refine work happens.
func TestMeetingRefineCrossWorkspaceHTTP(t *testing.T) {
	srv, tokens := newWorkspaceIsolationServer(t)
	h := srv.Handler()

	rr := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings", tokens["ws-b"], `{"title":"bob meeting"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create meeting status=%d body=%s", rr.Code, rr.Body.String())
	}
	var meetingB meeting.Meeting
	if err := json.Unmarshal(rr.Body.Bytes(), &meetingB); err != nil {
		t.Fatalf("decode meeting: %v", err)
	}

	refineBody := `{"segments":[{"speaker":"张三","text":"我们下周发布"}],"meta":{"title":"bob meeting"}}`

	cross := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+meetingB.ID+"/refine", tokens["ws-a"], refineBody)
	if cross.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace refine status=%d body=%s, want 404", cross.Code, cross.Body.String())
	}

	// Unknown meeting id in the caller's own workspace is also 404.
	missing := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/mtg_does_not_exist/refine", tokens["ws-a"], refineBody)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown meeting refine status=%d body=%s, want 404", missing.Code, missing.Body.String())
	}

	// The owner's meeting is untouched by the rejected cross-workspace attempt.
	owned, err := srv.meetingStore.GetScoped(meetingB.ID, "shared-user", "ws-b")
	if err != nil {
		t.Fatalf("owner meeting lookup: %v", err)
	}
	if owned.Status != "recording" {
		t.Fatalf("rejected cross-workspace refine mutated status to %q", owned.Status)
	}

	// The owner's refine passes the ownership gate and only then hits the
	// "llm not configured" degradation — proving the 404s above were scope
	// decisions, not a missing-dependency artifact.
	ownerRefine := serveWorkspaceJSON(t, h, http.MethodPost, "/api/meetings/"+meetingB.ID+"/refine", tokens["ws-b"], refineBody)
	if ownerRefine.Code != http.StatusServiceUnavailable {
		t.Fatalf("owner refine status=%d body=%s, want 503 (llm not configured)", ownerRefine.Code, ownerRefine.Body.String())
	}
}
