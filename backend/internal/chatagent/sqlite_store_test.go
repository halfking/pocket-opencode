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

	// Cannot modify builtin
	if err := store.Update(ctx, "ws-1", &Agent{
		ID: "builtin-1", Name: "X", Department: "test", SystemPrompt: "x",
	}); err == nil {
		t.Error("expected builtin update to fail")
	}

	// Cannot delete builtin
	if err := store.Delete(ctx, "ws-1", "builtin-1"); err == nil {
		t.Error("expected builtin delete to fail")
	}

	// Delete custom OK
	if err := store.Delete(ctx, "ws-1", "custom-1"); err != nil {
		t.Errorf("delete custom failed: %v", err)
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
