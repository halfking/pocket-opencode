package task

import (
	"context"
	"testing"
)

func TestFindTaskIDBySessionID_LatestAttachWins(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustCreate(t, s, "t-old", "ws-owner", "old")
	mustCreate(t, s, "t-new", "ws-owner", "new")
	if err := s.AttachSessionScoped(ctx, SessionLink{
		TaskID: "t-old", InstanceID: "disk-cursor", SessionID: "sess-1", Role: "primary",
	}, "ws-owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachSessionScoped(ctx, SessionLink{
		TaskID: "t-new", InstanceID: "disk-cursor", SessionID: "sess-1", Role: "primary",
	}, "ws-owner"); err != nil {
		t.Fatal(err)
	}
	got, err := s.FindTaskIDBySessionID(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "t-new" {
		t.Fatalf("got %q want t-new", got)
	}
	missing, err := s.FindTaskIDBySessionID(ctx, "no-such")
	if err != nil || missing != "" {
		t.Fatalf("missing=%q err=%v", missing, err)
	}
}
