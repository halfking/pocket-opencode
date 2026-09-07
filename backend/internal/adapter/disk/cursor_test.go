package disk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeCursorHome(t *testing.T, sessionID, content string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".cursor", "projects", "demo", "agent-transcripts", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return home
}

const sampleCursorJSONL = `{"role":"user","message":{"content":[{"type":"text","text":"<timestamp>t</timestamp>\n<user_query>fix the login flow</user_query>"}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"working on it"}]}}
`

func TestCursorListAndTranscript(t *testing.T) {
	home := writeCursorHome(t, "abc-123", sampleCursorJSONL)
	a := NewWithHome(home)
	ctx := context.Background()

	if !IsLocator(LocatorCursor) {
		t.Fatal("disk://cursor must be a locator")
	}
	sessions, err := a.ListSessions(ctx, LocatorCursor)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "abc-123" {
		t.Fatalf("sessions=%+v", sessions)
	}
	if sessions[0].Title != "fix the login flow" {
		t.Errorf("title=%q", sessions[0].Title)
	}

	msgs, err := a.GetMessages(ctx, LocatorCursor, "abc-123", 0, "asc")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages=%d", len(msgs))
	}
}
