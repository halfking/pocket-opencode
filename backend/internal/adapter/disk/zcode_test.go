package disk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeZcodeHome(t *testing.T, sessID, content string, archived bool) string {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, ".zcode", "cli", "agents")
	if archived {
		root = filepath.Join(home, ".zcode", "session-archive")
	}
	dir := filepath.Join(root, sessID, "agent_1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "transcript.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return home
}

const sampleZcodeJSONL = `{"type":"turn_started","timestamp":"2026-09-07T00:00:00.000Z","payload":{"input":"audit the gateway"}}
{"type":"model_network_status","payload":{"model":{"id":"kimi-k2"}}}
{"type":"model_streaming","payload":{"delta":"hello "}}
{"type":"model_streaming","payload":{"delta":"world"}}
`

func TestZcodeActiveAndArchived(t *testing.T) {
	home := writeZcodeHome(t, "sess_active", sampleZcodeJSONL, false)
	archRoot := filepath.Join(home, ".zcode", "session-archive", "sess_old", "agent_1")
	if err := os.MkdirAll(archRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archRoot, "transcript.jsonl"), []byte(sampleZcodeJSONL), 0o644); err != nil {
		t.Fatal(err)
	}

	a := NewWithHome(home)
	ctx := context.Background()
	sessions, err := a.ListSessions(ctx, LocatorZcode)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(sessions))
	}
	byID := map[string]string{}
	for _, s := range sessions {
		byID[s.ID] = s.Status
	}
	if byID["sess_old"] != "archived" {
		t.Errorf("archived status=%q", byID["sess_old"])
	}
	if byID["sess_active"] == "archived" {
		t.Errorf("active session marked archived")
	}

	tasks, err := a.ListRemoteTasks(ctx, LocatorZcode, "archived", 10)
	if err != nil {
		t.Fatalf("ListRemoteTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "sess_old" {
		t.Fatalf("archived tasks=%+v", tasks)
	}
}
