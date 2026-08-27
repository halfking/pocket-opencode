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
