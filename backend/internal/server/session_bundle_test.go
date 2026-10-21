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

// 回归（2026-10-22 实测本机 ACC）：ACC 的 acc_list_sessions 把 id /
// agent_session_id / gw_session_id / dispatch_id 放在**顶层**，从不返回
// "metadata"。旧实现只读 metadata ⇒ agent_session_id 取不到、退到
// row.SessionID（ACC 也不返回 "session_id"，是空串）⇒ session-bundle 里
// 会话 id 是空串（实测 `{"current":[{"id":"", ...}]}`），会话根本点不开。
//
// 这里用真实抓到的 ACC 响应锁死映射。
func TestAccSessionToRow_UsesTopLevelACCFields(t *testing.T) {
	row := accSessionToRow(mcp.AccSession{
		ID:             "156b2830-98f8-4f04-9678-1ee373dea05e",
		TaskID:         "acs-macbook-30b:dispatch_b4e8f48b",
		GatewayType:    "custom-agent",
		Input:          "ReClaw code-review/start: wf-ec",
		Output:         "run failed: prompt_failed",
		AgentSessionID: "ses_ece2e86ffffeY1EcUq2uB2OtVP",
		GwSessionID:    "gw_dispatch_65dcd3c3",
		DispatchID:     "dispatch_65dcd3c3",
		AgentKind:      "pi",
		CreatedAt:      "2026-10-12T20:32:55.469521Z",
	})
	if row.ID != "156b2830-98f8-4f04-9678-1ee373dea05e" {
		t.Errorf("ID = %q, want the ACC top-level id", row.ID)
	}
	if row.AgentSession != "ses_ece2e86ffffeY1EcUq2uB2OtVP" {
		t.Errorf("AgentSession = %q, want ses_ece2e86...", row.AgentSession)
	}
	if row.GwSessionID != "gw_dispatch_65dcd3c3" {
		t.Errorf("GwSessionID = %q", row.GwSessionID)
	}
	if row.AgentKind != "pi" {
		t.Errorf("AgentKind = %q", row.AgentKind)
	}
	if row.StartedAt != "2026-10-12T20:32:55.469521Z" {
		t.Errorf("StartedAt = %q, want created_at value", row.StartedAt)
	}
	if row.Title != "ReClaw code-review/start: wf-ec" {
		t.Errorf("Title = %q", row.Title)
	}
}

// 顶层字段优先于 metadata：两者都在时以顶层为准（ACC 现版本的事实面）。
func TestAccSessionToRow_TopLevelWinsOverMetadata(t *testing.T) {
	meta, _ := json.Marshal(accMeta{AgentKind: "cursor", AgentSessionID: "meta-ses", GwSessionID: "meta-gw"})
	row := accSessionToRow(mcp.AccSession{
		ID:             "top-1",
		AgentSessionID: "top-ses",
		GwSessionID:    "top-gw",
		AgentKind:      "pi",
		Metadata:       meta,
	})
	if row.AgentSession != "top-ses" {
		t.Errorf("AgentSession = %q, want top-ses", row.AgentSession)
	}
	if row.GwSessionID != "top-gw" {
		t.Errorf("GwSessionID = %q, want top-gw", row.GwSessionID)
	}
	if row.AgentKind != "pi" {
		t.Errorf("AgentKind = %q, want pi", row.AgentKind)
	}
}
