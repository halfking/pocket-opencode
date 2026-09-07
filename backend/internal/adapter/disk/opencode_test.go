package disk

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func writeOpencodeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".local", "share", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE session (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL DEFAULT '',
  parent_id TEXT,
  slug TEXT NOT NULL DEFAULT '',
  directory TEXT NOT NULL DEFAULT '/tmp/demo',
  title TEXT NOT NULL,
  version TEXT NOT NULL DEFAULT '1',
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  time_archived INTEGER,
  model TEXT,
  cost REAL NOT NULL DEFAULT 0,
  tokens_input INTEGER NOT NULL DEFAULT 0,
  tokens_output INTEGER NOT NULL DEFAULT 0,
  tokens_reasoning INTEGER NOT NULL DEFAULT 0,
  tokens_cache_read INTEGER NOT NULL DEFAULT 0,
  tokens_cache_write INTEGER NOT NULL DEFAULT 0
);
INSERT INTO session (id, title, time_created, time_updated, time_archived, model, tokens_input)
VALUES ('ses-live', 'live task', 1000, 2000, NULL, 'kimi-k2', 3);
INSERT INTO session (id, title, time_created, time_updated, time_archived, model)
VALUES ('ses-arch', 'old task', 1000, 1500, 1800, 'glm-5');
INSERT INTO session (id, parent_id, title, time_created, time_updated)
VALUES ('ses-child', 'ses-live', 'child', 1000, 2000);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT);
INSERT INTO message (id, session_id, time_created, time_updated, data)
VALUES ('m1', 'ses-live', 1000, 1000, '{"role":"user","time":{"created":1000},"content":"hello opencode"}');
`)
	if err != nil {
		t.Fatalf("seed sqlite: %v", err)
	}
	return home
}

func TestOpencodeListSkipsChildrenAndMarksArchived(t *testing.T) {
	home := writeOpencodeHome(t)
	a := NewWithHome(home)
	ctx := context.Background()

	sessions, err := a.ListSessions(ctx, LocatorOpencode)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("want parent sessions only, got %+v", sessions)
	}
	byID := map[string]string{}
	for _, s := range sessions {
		byID[s.ID] = s.Status
	}
	if byID["ses-arch"] != "archived" {
		t.Errorf("ses-arch status=%q", byID["ses-arch"])
	}
	if _, ok := byID["ses-child"]; ok {
		t.Fatal("child session must not appear")
	}

	msgs, err := a.GetMessages(ctx, LocatorOpencode, "ses-live", 0, "asc")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages=%d", len(msgs))
	}
}
