package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/mcp"
)

func TestClassifyLane_CurrentWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sess.jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := classifyLane(p, ""); got != "current" {
		t.Fatalf("got %q", got)
	}
}

func TestClassifyLane_HistoricalWhenMissingFileAndGwID(t *testing.T) {
	if got := classifyLane("/no/such/session.jsonl", "gw-1"); got != "historical" {
		t.Fatalf("got %q", got)
	}
}

func TestClassifyLane_RemapsHostHome(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".cursor", "x.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POCKET_DISK_HOME", dir)
	host := "/Users/someone/.cursor/x.jsonl"
	if got := classifyLane(host, ""); got != "current" {
		t.Fatalf("got %q", got)
	}
}

func TestAccSessionToRow_UsesMetadata(t *testing.T) {
	meta, _ := json.Marshal(accMeta{AgentKind: "cursor", AgentSessionID: "abc", GwSessionID: "gw"})
	row := accSessionToRow(mcp.AccSession{
		SessionID:    "acc-1",
		Input:        "hello title",
		InputTokens:  3,
		OutputTokens: 7,
		Metadata:     meta,
	})
	if row.Title != "hello title" || row.AgentKind != "cursor" || row.AgentSession != "abc" {
		t.Fatalf("%+v", row)
	}
	if row.TokensIn != 3 || row.TokensOut != 7 {
		t.Fatalf("tokens %+v", row)
	}
}
