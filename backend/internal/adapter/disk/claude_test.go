package disk

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleClaudeJSONL 覆盖 Claude Code 转录里所有需要特殊处理的行型：
// custom-title / 注入型 user / 真实 user / assistant 多 block 聚合 /
// tool_result 回填 / 压缩边界 / 已知跳过类型 / 未知类型 / 坏 JSON / sidechain。
const sampleClaudeJSONL = `{"type":"custom-title","customTitle":"Port Wake to Go"}
{"type":"user","cwd":"/Users/x/proj","gitBranch":"main","timestamp":"2026-08-20T10:00:00.000Z","message":{"content":"<system-reminder>injected noise</system-reminder>"}}
{"type":"user","timestamp":"2026-08-20T10:00:01.000Z","message":{"content":"hello from user"}}
{"type":"assistant","timestamp":"2026-08-20T10:00:02.000Z","message":{"id":"m1","model":"claude-sonnet-4","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2},"content":[{"type":"thinking","thinking":"pondering"},{"type":"text","text":"hi there"},{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"ls -la"}}]}}
{"type":"user","timestamp":"2026-08-20T10:00:03.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":[{"type":"text","text":"file listing"}]}]}}
{"type":"assistant","timestamp":"2026-08-20T10:00:04.000Z","message":{"id":"m2","model":"<synthetic>","content":[{"type":"text","text":"second turn"}]}}
{"type":"system","subtype":"compact_boundary","timestamp":"2026-08-20T10:00:05.000Z"}
{"type":"queue-operation","timestamp":"2026-08-20T10:00:06.000Z"}
{"type":"brand-new-line-type","timestamp":"2026-08-20T10:00:07.000Z"}
not json at all
{"type":"user","isSidechain":true,"timestamp":"2026-08-20T10:00:08.000Z","message":{"content":"sidechain text"}}
`

// writeClaudeHome 造一个假 home：<home>/.claude/projects/<slug>/<id>.jsonl
func writeClaudeHome(t *testing.T, sessionID, content string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-Users-x-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	return home
}

func TestParseClaudeJSONL(t *testing.T) {
	home := writeClaudeHome(t, "ses-1", sampleClaudeJSONL)
	path := filepath.Join(home, ".claude", "projects", "-Users-x-proj", "ses-1.jsonl")

	parsed, err := parseClaudeJSONL(path, false)
	if err != nil {
		t.Fatalf("parseClaudeJSONL: %v", err)
	}

	if parsed.title != "Port Wake to Go" {
		t.Errorf("custom-title must win, got %q", parsed.title)
	}
	if parsed.cwd != "/Users/x/proj" || parsed.gitBranch != "main" {
		t.Errorf("envelope fields lost: cwd=%q branch=%q", parsed.cwd, parsed.gitBranch)
	}
	if parsed.model != "claude-sonnet-4" {
		t.Errorf("model must ignore <synthetic>, got %q", parsed.model)
	}
	if parsed.tokensUsed != 17 {
		t.Errorf("tokens = input+output+cache_creation = 17, got %d", parsed.tokensUsed)
	}
	if parsed.unknownLines != 2 {
		t.Errorf("unknown lines = 1 unknown type + 1 bad json = 2, got %d", parsed.unknownLines)
	}
	if parsed.createdAt == 0 || parsed.updatedAt <= parsed.createdAt {
		t.Errorf("created/updated not derived: %d / %d", parsed.createdAt, parsed.updatedAt)
	}

	// sidechain 行被跳过，tool_result 行本身不产生消息 → 5 条
	if len(parsed.messages) != 5 {
		for i, m := range parsed.messages {
			t.Logf("msg[%d] role=%s kind=%s text=%q tools=%d", i, m.Role, m.Kind, m.Text, len(m.ToolCalls))
		}
		t.Fatalf("expected 5 messages, got %d", len(parsed.messages))
	}
	for i, m := range parsed.messages {
		if m.Seq != i {
			t.Errorf("seq must be backfilled in order: msg[%d].Seq=%d", i, m.Seq)
		}
		if strings.Contains(m.Text, "sidechain text") {
			t.Errorf("sidechain content leaked into mainline: %q", m.Text)
		}
	}

	if parsed.messages[0].Kind != KindMeta {
		t.Errorf("injected <system-reminder> user line must be Meta, got %s", parsed.messages[0].Kind)
	}
	if parsed.messages[1].Kind != KindText || parsed.messages[1].Text != "hello from user" {
		t.Errorf("real user message mismatch: %+v", parsed.messages[1])
	}

	assistant := parsed.messages[2]
	if assistant.Role != RoleAssistant || assistant.Text != "hi there" {
		t.Errorf("assistant text block mismatch: %+v", assistant)
	}
	if assistant.Thinking != "pondering" {
		t.Errorf("thinking block lost: %q", assistant.Thinking)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistant.ToolCalls))
	}
	tc := assistant.ToolCalls[0]
	if tc.ID != "tu1" || tc.Name != "Bash" {
		t.Errorf("tool call identity mismatch: %+v", tc)
	}
	if tc.Output != "file listing" {
		t.Errorf("tool_result must be backfilled onto the tool_use, got %q", tc.Output)
	}
	if tc.InputPreview != "ls -la" {
		t.Errorf("preview should pick the command field, got %q", tc.InputPreview)
	}
	if parsed.messages[4].Kind != KindCompactSummary {
		t.Errorf("compact_boundary must map to CompactSummary, got %s", parsed.messages[4].Kind)
	}

	// 只读铁律：解析不得改动源文件
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("source transcript disappeared: %v", err)
	}
}

// 无 custom-title 时用首条真实用户消息推导标题，注入内容不参与。
func TestParseClaudeJSONLTitleFallback(t *testing.T) {
	content := `{"type":"user","timestamp":"2026-08-20T10:00:00.000Z","message":{"content":"<environment_context>ignored</environment_context>"}}
{"type":"user","timestamp":"2026-08-20T10:00:01.000Z","message":{"content":"  make   the  title from   this  "}}
`
	home := writeClaudeHome(t, "ses-2", content)
	parsed, err := parseClaudeJSONL(filepath.Join(home, ".claude", "projects", "-Users-x-proj", "ses-2.jsonl"), false)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.title != "make the title from this" {
		t.Errorf("title should come from the first real user message, whitespace-compacted; got %q", parsed.title)
	}
}

// 适配器层：列表 + 消息映射（V1 信封）+ ToMobile 可渲染。
func TestAdapterClaudeReadPaths(t *testing.T) {
	home := writeClaudeHome(t, "ses-1", sampleClaudeJSONL)
	a := NewWithHome(home)
	ctx := context.Background()

	if err := a.HealthCheck(ctx, LocatorClaude); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}

	sessions, err := a.ListSessions(ctx, LocatorClaude)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "ses-1" || sessions[0].Title != "Port Wake to Go" {
		t.Fatalf("unexpected session list: %+v", sessions)
	}
	if sessions[0].TimeUpdated == 0 {
		t.Errorf("TimeUpdated must back the mobile sync cursor")
	}

	title, err := a.GetSessionSummary(ctx, LocatorClaude, "ses-1")
	if err != nil || title != "Port Wake to Go" {
		t.Errorf("GetSessionSummary = %q, %v", title, err)
	}

	resp, err := a.GetSessionMessages(ctx, LocatorClaude, "ses-1", 0, "", "")
	if err != nil {
		t.Fatalf("GetSessionMessages: %v", err)
	}
	if len(resp.Data) != 5 {
		t.Fatalf("expected 5 mapped messages, got %d", len(resp.Data))
	}

	// 复用既有 V1 信封 → 移动端视图的映射，验证 disk 会话可被现有前端渲染
	mobile := resp.Data[2].ToMobile()
	if mobile == nil {
		t.Fatal("assistant message must map to a mobile message")
	}
	if mobile.Role != "assistant" || mobile.Text != "hi there" {
		t.Errorf("mobile mapping mismatch: %+v", mobile)
	}
	var sawReasoning, sawTool bool
	for _, part := range mobile.Content {
		switch part.Type {
		case "reasoning":
			sawReasoning = part.Text == "pondering"
		case "tool":
			sawTool = part.Name == "Bash" && part.State == "completed" && part.Output == "file listing"
			if cmd, _ := part.Input["command"].(string); cmd != "ls -la" {
				t.Errorf("tool input should stay structured, got %+v", part.Input)
			}
		}
	}
	if !sawReasoning || !sawTool {
		t.Errorf("reasoning/tool parts missing: %+v", mobile.Content)
	}

	// desc + limit 取最新一条
	desc, err := a.GetMessages(ctx, LocatorClaude, "ses-1", 1, "desc")
	if err != nil {
		t.Fatalf("GetMessages desc: %v", err)
	}
	if len(desc) != 1 {
		t.Fatalf("limit=1 must return 1 message, got %d", len(desc))
	}
	if got := desc[0].ID; got != "msg_ses-1_4" {
		t.Errorf("desc order must start at the newest message, got %q", got)
	}

	// GetSessionContext 只保留压缩边界之后的消息（本样本里边界是最后一条）
	compacted, err := a.GetSessionContext(ctx, LocatorClaude, "ses-1")
	if err != nil {
		t.Fatalf("GetSessionContext: %v", err)
	}
	if len(compacted) != 0 {
		t.Errorf("context after the last compact boundary should be empty, got %d", len(compacted))
	}
}
