package disk

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleRolloutUUID = "11111111-2222-3333-4444-555555555555"

// sampleRollout 覆盖 Codex rollout 的全部主线行型，并故意带上 encrypted_content
// 以验证密文不会进入归一化结果。
const sampleRollout = `{"timestamp":"2026-08-20T09:00:00.000Z","type":"session_meta","payload":{"cwd":"/Users/x/proj","originator":"codex_cli_rs","git":{"branch":"feature/x"}}}
{"timestamp":"2026-08-20T09:00:01.000Z","type":"turn_context","payload":{"model":"gpt-5-codex"}}
{"timestamp":"2026-08-20T09:00:02.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"port wake to go"}]}}
{"timestamp":"2026-08-20T09:00:03.000Z","type":"response_item","payload":{"type":"reasoning","encrypted_content":"SECRET-CIPHERTEXT","summary":[{"text":"plan the port"}]}}
{"timestamp":"2026-08-20T09:00:04.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"on it"}]}}
{"timestamp":"2026-08-20T09:00:05.000Z","type":"response_item","payload":{"type":"function_call","call_id":"c1","name":"shell","arguments":"{\"command\":\"go build ./...\"}"}}
{"timestamp":"2026-08-20T09:00:06.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"build ok"}}
{"timestamp":"2026-08-20T09:00:07.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":4242}}}}
{"timestamp":"2026-08-20T09:00:08.000Z","type":"compacted","payload":{}}
`

// writeCodexHome 造一个假 home：<home>/.codex/sessions/2026/08/20/rollout-...jsonl
func writeCodexHome(t *testing.T, rollout string) (home, rolloutPath string) {
	t.Helper()
	home = t.TempDir()
	dir := filepath.Join(home, ".codex", "sessions", "2026", "08", "20")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rolloutPath = filepath.Join(dir, "rollout-2026-08-20T09-00-00-"+sampleRolloutUUID+".jsonl")
	if err := os.WriteFile(rolloutPath, []byte(rollout), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	return home, rolloutPath
}

// writeCodexStateDB 造一个最小 state_5.sqlite（列名与真实 Codex schema 一致）。
func writeCodexStateDB(t *testing.T, home, threadID, rolloutPath string) string {
	t.Helper()
	dbPath := filepath.Join(home, ".codex", "state_5.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open state db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE threads (
		id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL, cwd TEXT NOT NULL, title TEXT NOT NULL,
		name TEXT, tokens_used INTEGER NOT NULL DEFAULT 0, archived INTEGER NOT NULL DEFAULT 0,
		git_branch TEXT, model TEXT, source TEXT NOT NULL, created_at_ms INTEGER, updated_at_ms INTEGER)`); err != nil {
		t.Fatalf("create threads: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO threads
		(id, rollout_path, cwd, title, name, tokens_used, archived, git_branch, model, source, created_at_ms, updated_at_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		threadID, rolloutPath, "/Users/x/proj", "<environment_context>injected</environment_context>",
		"Named by user", 99, 0, "feature/x", "gpt-5", "vscode", 1755000000000, 1755000009000); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	return dbPath
}

func TestParseRollout(t *testing.T) {
	_, rolloutPath := writeCodexHome(t, sampleRollout)

	parsed, err := parseRollout(rolloutPath)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	if parsed.cwd != "/Users/x/proj" || parsed.gitBranch != "feature/x" {
		t.Errorf("session_meta fields lost: cwd=%q branch=%q", parsed.cwd, parsed.gitBranch)
	}
	if parsed.model != "gpt-5-codex" {
		t.Errorf("turn_context model lost: %q", parsed.model)
	}
	if parsed.source != "CLI" {
		t.Errorf("originator codex_cli_rs should map to CLI, got %q", parsed.source)
	}
	if parsed.tokensUsed != 4242 {
		t.Errorf("token_count lost: %d", parsed.tokensUsed)
	}
	if parsed.unknownLines != 0 {
		t.Errorf("all sample line types must be known, got %d unknown", parsed.unknownLines)
	}

	// user / assistant(thinking) / assistant("on it" + tool) / compact
	if len(parsed.messages) != 4 {
		for i, m := range parsed.messages {
			t.Logf("msg[%d] role=%s kind=%s text=%q thinking=%q tools=%d", i, m.Role, m.Kind, m.Text, m.Thinking, len(m.ToolCalls))
		}
		t.Fatalf("expected 4 messages, got %d", len(parsed.messages))
	}
	if parsed.messages[0].Role != RoleUser || parsed.messages[0].Text != "port wake to go" {
		t.Errorf("user message mismatch: %+v", parsed.messages[0])
	}
	if parsed.messages[1].Thinking != "plan the port" {
		t.Errorf("reasoning summary lost: %+v", parsed.messages[1])
	}
	host := parsed.messages[2]
	if host.Text != "on it" || len(host.ToolCalls) != 1 {
		t.Fatalf("tool call should attach to the preceding assistant message: %+v", host)
	}
	if host.ToolCalls[0].Name != "shell" || host.ToolCalls[0].Output != "build ok" {
		t.Errorf("function_call/_output pairing mismatch: %+v", host.ToolCalls[0])
	}
	if host.ToolCalls[0].InputPreview != "go build ./..." {
		t.Errorf("preview should decode the arguments JSON, got %q", host.ToolCalls[0].InputPreview)
	}
	if parsed.messages[3].Kind != KindCompactSummary {
		t.Errorf("compacted must map to CompactSummary: %+v", parsed.messages[3])
	}

	// 明文 summary 之外的 encrypted_content 绝不能出现在任何字段里
	for _, m := range parsed.messages {
		blob := m.Text + m.Thinking
		for _, tc := range m.ToolCalls {
			blob += tc.Input + tc.Output + tc.InputPreview
		}
		if strings.Contains(blob, "SECRET-CIPHERTEXT") {
			t.Fatalf("encrypted reasoning content leaked: %+v", m)
		}
	}
}

// response_item 完全缺席时退回 event_msg 流。
func TestParseRolloutEventFallback(t *testing.T) {
	rollout := `{"timestamp":"2026-08-20T09:00:00.000Z","type":"session_meta","payload":{"cwd":"/Users/x/proj","originator":"codex_vscode"}}
{"timestamp":"2026-08-20T09:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"only events here"}}
{"timestamp":"2026-08-20T09:00:02.000Z","type":"event_msg","payload":{"type":"agent_message","message":"reply from events"}}
`
	_, rolloutPath := writeCodexHome(t, rollout)
	parsed, err := parseRollout(rolloutPath)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	if len(parsed.messages) != 2 {
		t.Fatalf("expected the event_msg fallback stream, got %d messages", len(parsed.messages))
	}
	if parsed.messages[0].Text != "only events here" || parsed.messages[1].Text != "reply from events" {
		t.Errorf("fallback content mismatch: %+v", parsed.messages)
	}
	if parsed.source != "IDE extension" {
		t.Errorf("codex_vscode should map to IDE extension, got %q", parsed.source)
	}
}

func TestRolloutNativeID(t *testing.T) {
	cases := map[string]string{
		"rollout-2026-08-14T11-47-18-" + sampleRolloutUUID: sampleRolloutUUID,
		"rollout-short":     "rollout-short",
		"not-a-rollout":     "not-a-rollout",
		"rollout-XXXX-YYYY": "rollout-XXXX-YYYY",
	}
	for stem, want := range cases {
		if got := rolloutNativeID(stem); got != want {
			t.Errorf("rolloutNativeID(%q) = %q, want %q", stem, got, want)
		}
	}
}

// state DB 的快元数据必须只增强不覆盖：用户命名的标题赢，rollout 的
// source/model/tokens 赢；且读取过程不得改动 sqlite 文件（只读铁律）。
func TestCodexQuickMetaMergeAndReadOnly(t *testing.T) {
	home, rolloutPath := writeCodexHome(t, sampleRollout)
	dbPath := writeCodexStateDB(t, home, "thread-abc", rolloutPath)

	before, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat state db: %v", err)
	}

	a := NewWithHome(home)
	metas, err := a.ListSessionMetas(context.Background(), LocatorCodex)
	if err != nil {
		t.Fatalf("ListSessionMetas: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 codex session, got %d", len(metas))
	}
	meta := metas[0]
	if meta.Title != "Named by user" {
		t.Errorf("state DB name must win over derived title, got %q", meta.Title)
	}
	if meta.ID != "thread-abc" || meta.Key != "codex:thread-abc" {
		t.Errorf("state DB thread id must win: id=%q key=%q", meta.ID, meta.Key)
	}
	if meta.Source != "CLI" {
		t.Errorf("rollout originator is more precise than state source, got %q", meta.Source)
	}
	if meta.Model != "gpt-5-codex" {
		t.Errorf("rollout model must win, got %q", meta.Model)
	}
	if meta.TokensUsed != 4242 {
		t.Errorf("rollout token_count must win, got %d", meta.TokensUsed)
	}
	if meta.ProjectName != "proj" {
		t.Errorf("project name should be the cwd basename, got %q", meta.ProjectName)
	}
	if meta.MessageCount != 3 {
		t.Errorf("message count should only include real text messages, got %d", meta.MessageCount)
	}

	after, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat state db after read: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("state db must not be modified: size %d→%d mtime %v→%v",
			before.Size(), after.Size(), before.ModTime(), after.ModTime())
	}

	// transcript 既能按 rollout uuid 也能按 state 线程 id 找到会话
	for _, id := range []string{sampleRolloutUUID, "thread-abc"} {
		if _, msgs, err := a.readers[LocatorCodex].(*codexReader).transcript(id); err != nil || len(msgs) == 0 {
			t.Errorf("transcript(%q) failed: %d msgs, err=%v", id, len(msgs), err)
		}
	}
}

// 只读打开真实 ~/.codex/state_5.sqlite（含 WAL）：本机没有该文件时跳过。
// 这条用例验证「mode=ro 直开 / 拷 WAL 降级」策略对真实带 -wal 的库可用，
// 并确认读取不会写坏 agent 数据。
func TestReadThreadsAgainstRealStateDBIfPresent(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	dbPath := filepath.Join(home, ".codex", "state_5.sqlite")
	before, err := os.Stat(dbPath)
	if err != nil {
		t.Skipf("no local codex state db: %v", err)
	}

	rows, err := readThreads(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("readThreads on real state db: %v", err)
	}
	t.Logf("read %d codex threads read-only", len(rows))

	after, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat after read: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("real state db was modified by a read: size %d→%d mtime %v→%v",
			before.Size(), after.Size(), before.ModTime(), after.ModTime())
	}
}
