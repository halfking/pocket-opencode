package chatagent

import (
	"context"
	"os"
	"testing"
)

func TestSQLiteStore_CRUD(t *testing.T) {
	tmp, err := os.CreateTemp("", "chatagent-test-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	store, err := NewSQLiteStore(tmp.Name())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Create builtin + custom
	if err := store.Create(ctx, &Agent{
		ID: "builtin-1", WorkspaceID: "", Name: "内置测试", Department: "test",
		SystemPrompt: "builtin prompt", IsBuiltin: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, &Agent{
		ID: "custom-1", WorkspaceID: "ws-1", Name: "我的角色", Department: "test",
		SystemPrompt: "custom prompt", IsBuiltin: false,
	}); err != nil {
		t.Fatal(err)
	}

	// List (should see both)
	list, err := store.List(ctx, "ws-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 agents (builtin + custom), got %d", len(list))
	}

	// Get single
	got, err := store.Get(ctx, "ws-1", "custom-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "我的角色" {
		t.Errorf("got Name=%q", got.Name)
	}

	// CountCustom
	count, err := store.CountCustom(ctx, "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("CountCustom = %d, want 1 (builtin not counted)", count)
	}

	// Update custom
	got.Name = "我的角色（更新）"
	if err := store.Update(ctx, "ws-1", got); err != nil {
		t.Fatal(err)
	}
	got2, _ := store.Get(ctx, "ws-1", "custom-1")
	if got2.Name != "我的角色（更新）" {
		t.Errorf("update failed: %q", got2.Name)
	}

	// Builtin 可维护：允许修改与删除（专家库维护语义）
	if err := store.Update(ctx, "ws-1", &Agent{
		ID: "builtin-1", Name: "内置（维护更新）", Department: "test", SystemPrompt: "x",
	}); err != nil {
		t.Errorf("builtin update should be allowed: %v", err)
	}
	if err := store.Delete(ctx, "ws-1", "builtin-1"); err != nil {
		t.Errorf("builtin delete should be allowed: %v", err)
	}

	// Delete custom OK
	if err := store.Delete(ctx, "ws-1", "custom-1"); err != nil {
		t.Errorf("delete custom failed: %v", err)
	}
}

// TestSQLiteStore_MarketplaceFields 验证 marketplace_id / skill_refs /
// publisher / version / tags 五个市场化字段能正确写入与读回，包括 NULL
// 默认值和 JSON 数组的反序列化。
func TestSQLiteStore_MarketplaceFields(t *testing.T) {
	tmp, err := os.CreateTemp("", "chatagent-mkt-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	store, err := NewSQLiteStore(tmp.Name())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// 1. 不带 marketplace 字段的角色：所有字段应为空字符串 / 空切片。
	if err := store.Create(ctx, &Agent{
		ID: "plain-1", WorkspaceID: "ws-mkt", Name: "普通角色", Department: "test",
		SystemPrompt: "x", IsBuiltin: false,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "ws-mkt", "plain-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MarketplaceID != "" || got.Publisher != "" || got.Version != "" {
		t.Errorf("expected empty optional fields, got marketplace=%q publisher=%q version=%q",
			got.MarketplaceID, got.Publisher, got.Version)
	}
	if got.SkillRefs == nil || len(got.SkillRefs) != 0 {
		t.Errorf("expected empty SkillRefs slice, got %#v", got.SkillRefs)
	}
	if got.Tags == nil || len(got.Tags) != 0 {
		t.Errorf("expected empty Tags slice, got %#v", got.Tags)
	}

	// 2. 完整 marketplace 字段：应能 round-trip。
	installed := &Agent{
		ID:            "mkt-1",
		WorkspaceID:   "ws-mkt",
		Name:          "市场角色",
		Department:    "test",
		SystemPrompt:  "p",
		IsBuiltin:     false,
		MarketplaceID: "ws-remote/cool-agent",
		SkillRefs:     []string{"skill-a", "skill-b"},
		Publisher:     "alice@org",
		Version:       "1.2.3",
		Tags:          []string{"productivity", "code"},
	}
	if err := store.Create(ctx, installed); err != nil {
		t.Fatal(err)
	}
	got2, err := store.Get(ctx, "ws-mkt", "mkt-1")
	if err != nil {
		t.Fatal(err)
	}
	if got2.MarketplaceID != installed.MarketplaceID {
		t.Errorf("MarketplaceID: got %q want %q", got2.MarketplaceID, installed.MarketplaceID)
	}
	if got2.Publisher != installed.Publisher || got2.Version != installed.Version {
		t.Errorf("publisher/version mismatch: got %q/%q want %q/%q",
			got2.Publisher, got2.Version, installed.Publisher, installed.Version)
	}
	if len(got2.SkillRefs) != 2 || got2.SkillRefs[0] != "skill-a" || got2.SkillRefs[1] != "skill-b" {
		t.Errorf("SkillRefs mismatch: %#v", got2.SkillRefs)
	}
	if len(got2.Tags) != 2 || got2.Tags[0] != "productivity" {
		t.Errorf("Tags mismatch: %#v", got2.Tags)
	}

	// 3. Update：清空 MarketplaceID / 替换 Tags / 替换 SkillRefs。
	installed.MarketplaceID = ""
	installed.Tags = []string{"updated"}
	installed.SkillRefs = []string{}
	if err := store.Update(ctx, "ws-mkt", installed); err != nil {
		t.Fatal(err)
	}
	got3, err := store.Get(ctx, "ws-mkt", "mkt-1")
	if err != nil {
		t.Fatal(err)
	}
	if got3.MarketplaceID != "" {
		t.Errorf("MarketplaceID should be cleared, got %q", got3.MarketplaceID)
	}
	if len(got3.Tags) != 1 || got3.Tags[0] != "updated" {
		t.Errorf("Tags should be replaced: %#v", got3.Tags)
	}
	if got3.SkillRefs == nil || len(got3.SkillRefs) != 0 {
		t.Errorf("SkillRefs should be cleared: %#v", got3.SkillRefs)
	}

	// 4. List：两个角色均应可见，且 marketplace_id 列对 plain-1 仍为 NULL。
	list, err := store.List(ctx, "ws-mkt", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("List: expected 2 agents, got %d", len(list))
	}
}

func TestSQLiteStore_ImportBuiltin(t *testing.T) {
	tmp, err := os.CreateTemp("", "chatagent-import-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	store, err := NewSQLiteStore(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	// ImportBuiltinAgents 接受 repoPath；用 ParseAgentFile 写一个临时角色文件
	importDir, err := os.MkdirTemp("", "chatagent-import-src-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(importDir)

	content := `---
name: 测试角色
description: test
emoji: 🧪
---

# 测试角色

body content
`
	if err := os.WriteFile(importDir+"/test-agent.md", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := store.ImportBuiltinAgents(context.Background(), importDir); err != nil {
		t.Fatalf("ImportBuiltinAgents: %v", err)
	}

	// 再次调用应幂等
	if err := store.ImportBuiltinAgents(context.Background(), importDir); err != nil {
		t.Fatalf("ImportBuiltinAgents (2nd): %v", err)
	}

	list, _ := store.List(context.Background(), "", "")
	if len(list) != 1 {
		t.Errorf("expected 1 imported agent, got %d", len(list))
	}
}
