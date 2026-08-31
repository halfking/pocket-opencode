package chatagent

import (
	"context"
	"path/filepath"
	"testing"
)

// newSeedTestStore 每个用例独立的临时 SQLite 库（无 PG 依赖）。
func newSeedTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "chat_agents.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return store
}

// 全新库首启应自动灌入内嵌专家库种子。
func TestEnsureBuiltinAgents_EmptyDBSeedsEmbedded(t *testing.T) {
	store := newSeedTestStore(t)
	ctx := context.Background()

	if err := EnsureBuiltinAgents(ctx, store, ""); err != nil {
		t.Fatalf("EnsureBuiltinAgents: %v", err)
	}

	all, err := store.List(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("embedded seed produced no agents")
	}
	for _, a := range all {
		if !a.IsBuiltin {
			t.Errorf("seeded agent %s should be builtin", a.ID)
		}
		if a.WorkspaceID != "" {
			t.Errorf("seeded agent %s should have empty workspace_id", a.ID)
		}
		if a.SystemPrompt == "" {
			t.Errorf("seeded agent %s has empty system_prompt", a.ID)
		}
	}
}

// 已有内置角色时幂等跳过，不得重复灌入（避免覆盖用户的维护性修改）。
func TestEnsureBuiltinAgents_Idempotent(t *testing.T) {
	store := newSeedTestStore(t)
	ctx := context.Background()

	// 预置一个内置角色（模拟已初始化或已维护过的库）
	if err := store.Create(ctx, &Agent{
		ID: "kept-builtin", WorkspaceID: "", Name: "保留", Department: "x", SystemPrompt: "keep", IsBuiltin: true,
	}); err != nil {
		t.Fatal(err)
	}

	if err := EnsureBuiltinAgents(ctx, store, ""); err != nil {
		t.Fatalf("EnsureBuiltinAgents: %v", err)
	}

	all, err := store.List(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("expected seed skipped (1 agent), got %d", len(all))
	}
}

// repoPath 指向有效角色目录时优先仓库导入；路径无效时回落内嵌种子。
func TestEnsureBuiltinAgents_RepoFallbackToEmbedded(t *testing.T) {
	store := newSeedTestStore(t)
	ctx := context.Background()

	// 无效路径 → 回落内嵌种子
	if err := EnsureBuiltinAgents(ctx, store, "/definitely/not/a/repo"); err != nil {
		t.Fatalf("EnsureBuiltinAgents with bad repo path: %v", err)
	}
	all, err := store.List(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("bad repo path should fall back to embedded seed")
	}
}
