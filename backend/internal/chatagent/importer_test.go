package chatagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAgentFile(t *testing.T) {
	// 创建临时 .md 文件
	tmpDir := t.TempDir()
	deptDir := filepath.Join(tmpDir, "engineering")
	if err := os.Mkdir(deptDir, 0755); err != nil {
		t.Fatal(err)
	}

	mdPath := filepath.Join(deptDir, "engineering-test-agent.md")
	content := `---
name: 测试工程师
description: 这是一个测试角色
emoji: 🧪
color: green
---

# 测试工程师

你是**测试工程师**，专注软件质量保障。

## 你的身份与记忆

- **角色**：QA 工程师
- **个性**：细致、追求零缺陷
`
	if err := os.WriteFile(mdPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	agent, err := ParseAgentFile(mdPath)
	if err != nil {
		t.Fatalf("ParseAgentFile failed: %v", err)
	}

	// 验证字段
	if agent.ID != "engineering-test-agent" {
		t.Errorf("ID = %q, want %q", agent.ID, "engineering-test-agent")
	}
	if agent.Name != "测试工程师" {
		t.Errorf("Name = %q, want %q", agent.Name, "测试工程师")
	}
	if agent.Description != "这是一个测试角色" {
		t.Errorf("Description = %q", agent.Description)
	}
	if agent.Department != "engineering" {
		t.Errorf("Department = %q, want %q", agent.Department, "engineering")
	}
	if agent.Emoji != "🧪" {
		t.Errorf("Emoji = %q, want %q", agent.Emoji, "🧪")
	}
	if agent.Color != "green" {
		t.Errorf("Color = %q, want %q", agent.Color, "green")
	}
	if !agent.IsBuiltin {
		t.Error("IsBuiltin should be true")
	}
	if agent.WorkspaceID != "" {
		t.Errorf("WorkspaceID should be empty for builtin agent, got %q", agent.WorkspaceID)
	}

	// 验证 systemPrompt 是 Markdown 正文（不含 YAML）
	if !strings.Contains(agent.SystemPrompt, "你是**测试工程师**") {
		t.Errorf("SystemPrompt should contain markdown body, got: %s", agent.SystemPrompt)
	}
	if strings.Contains(agent.SystemPrompt, "name: 测试工程师") {
		t.Error("SystemPrompt should not contain YAML frontmatter")
	}
}

func TestParseAgentFile_MissingFrontmatter(t *testing.T) {
	tmpDir := t.TempDir()
	mdPath := filepath.Join(tmpDir, "invalid.md")
	if err := os.WriteFile(mdPath, []byte("# Just Markdown\nNo frontmatter"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ParseAgentFile(mdPath)
	if err == nil || !strings.Contains(err.Error(), "missing YAML frontmatter") {
		t.Errorf("expected missing frontmatter error, got %v", err)
	}
}

func TestParseAgentFile_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	mdPath := filepath.Join(tmpDir, "bad.md")
	content := `---
name: [unclosed list
---
body
`
	if err := os.WriteFile(mdPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ParseAgentFile(mdPath)
	if err == nil || !strings.Contains(err.Error(), "parse YAML") {
		t.Errorf("expected YAML parse error, got %v", err)
	}
}

func TestIsSkippedDir(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/repo/engineering/agent.md", false},
		{"/repo/.github/ISSUE_TEMPLATE/bug.md", true},
		{"/repo/.github/PULL_REQUEST_TEMPLATE.md", true},
		{"/repo/.vscode/settings.json", true},
		{"/repo/node_modules/some/pkg/README.md", true},
		{"/repo/.git/HEAD", true},
		{"/repo/.idea/workspace.xml", true},
		// 仓库根 .md 不应被祖先目录过滤掉（仍由文件名规则兜底）
		{"/repo/README.md", false},
	}
	for _, tc := range cases {
		if got := isSkippedDir(tc.path); got != tc.want {
			t.Errorf("isSkippedDir(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestImportBuiltin_SkipsAuxDirs 端到端验证：把 .github、.vscode 下的 .md
// 路径不会被 importBuiltin 当成角色解析。
func TestImportBuiltin_SkipsAuxDirs(t *testing.T) {
	tmpDir := t.TempDir()

	// 真实角色
	goodDir := filepath.Join(tmpDir, "engineering")
	mustMkdirAll(t, goodDir)
	good := filepath.Join(goodDir, "good.md")
	mustWrite(t, good, "---\nname: Good\n---\n# body\n")

	// 应被跳过：.github issue template（仓库里常见的"误判源"）
	badGithub := filepath.Join(tmpDir, ".github", "ISSUE_TEMPLATE", "bug.md")
	mustMkdirAll(t, filepath.Dir(badGithub))
	mustWrite(t, badGithub, "---\nname: BadGitHub\n---\nshould be skipped\n")

	// 应被跳过：node_modules 下的 .md
	badNM := filepath.Join(tmpDir, "node_modules", "pkg", "README.md")
	mustMkdirAll(t, filepath.Dir(badNM))
	mustWrite(t, badNM, "---\nname: BadNM\n---\nshould be skipped\n")

	// 应被跳过：全大写文件名（README.md）
	readme := filepath.Join(tmpDir, "README.md")
	mustWrite(t, readme, "# README\n")

	store, err := NewSQLiteStore(filepath.Join(tmpDir, "test.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if err := store.ImportBuiltinAgents(t.Context(), tmpDir); err != nil {
		t.Fatalf("ImportBuiltinAgents: %v", err)
	}

	agents, err := store.List(t.Context(), "", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("want 1 imported agent, got %d: %+v", len(agents), agentIDs(agents))
	}
	if agents[0].ID != "good" {
		t.Errorf("want id=good, got %q", agents[0].ID)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
}

func agentIDs(as []*Agent) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}
