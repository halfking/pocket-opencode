package chatagent

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func setupTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	
	// 尝试连接测试 PostgreSQL（需要手动配置 POCKET_TEST_DB_URL）
	dbURL := "postgres://localhost/pocket_test?sslmode=disable"
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("PostgreSQL not available (set POCKET_TEST_DB_URL): %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	store := NewStore(pool)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// 清空测试数据
	if _, err := pool.Exec(ctx, "DELETE FROM chat_agents WHERE id LIKE 'test-%' OR id LIKE 'custom-%' OR id = 'builtin' OR id = 'builtin-agent'"); err != nil {
		t.Logf("cleanup warning: %v", err)
	}

	return store, ctx
}

func TestStore_CreateAndGet(t *testing.T) {
	store, ctx := setupTestStore(t)

	agent := &Agent{
		ID:           "test-agent-1",
		WorkspaceID:  "ws-test",
		Name:         "测试助手",
		Description:  "这是测试",
		Department:   "test",
		Emoji:        "🧪",
		SystemPrompt: "你是测试助手",
		IsBuiltin:    false,
	}

	if err := store.Create(ctx, agent); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Get 应该能找到
	got, err := store.Get(ctx, "ws-test", "test-agent-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Name != "测试助手" || got.Department != "test" {
		t.Errorf("Get returned wrong agent: %+v", got)
	}
	if got.CreatedAt == 0 || got.UpdatedAt == 0 {
		t.Error("timestamps not set")
	}
}

func TestStore_BuiltinAgentGloballyVisible(t *testing.T) {
	store, ctx := setupTestStore(t)

	// 创建内置角色（workspace_id 为空）
	builtin := &Agent{
		ID:           "builtin-agent",
		WorkspaceID:  "",
		Name:         "内置角色",
		Department:   "general",
		SystemPrompt: "builtin prompt",
		IsBuiltin:    true,
	}
	if err := store.Create(ctx, builtin); err != nil {
		t.Fatalf("Create builtin failed: %v", err)
	}

	// 任意 workspace 都应该能看到
	got, err := store.Get(ctx, "ws-other", "builtin-agent")
	if err != nil {
		t.Fatalf("Get builtin from other workspace failed: %v", err)
	}
	if got.Name != "内置角色" {
		t.Errorf("builtin not visible to other workspace")
	}
}

func TestStore_List_WorkspaceIsolation(t *testing.T) {
	store, ctx := setupTestStore(t)

	// ws-a 的自定义角色
	_ = store.Create(ctx, &Agent{
		ID: "custom-a", WorkspaceID: "ws-a", Name: "A的角色", Department: "test", SystemPrompt: "a",
	})
	// ws-b 的自定义角色
	_ = store.Create(ctx, &Agent{
		ID: "custom-b", WorkspaceID: "ws-b", Name: "B的角色", Department: "test", SystemPrompt: "b",
	})
	// 内置角色
	_ = store.Create(ctx, &Agent{
		ID: "builtin", WorkspaceID: "", Name: "内置", Department: "test", SystemPrompt: "builtin", IsBuiltin: true,
	})

	// ws-a 查询应该看到自己的 + 内置，不应看到 ws-b 的
	list, err := store.List(ctx, "ws-a", "")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ws-a should see 2 agents (custom-a + builtin), got %d", len(list))
	}
	names := make(map[string]bool)
	for _, a := range list {
		names[a.Name] = true
	}
	if !names["A的角色"] || !names["内置"] {
		t.Errorf("ws-a missing expected agents: %+v", names)
	}
	if names["B的角色"] {
		t.Error("ws-a should not see ws-b's custom agent")
	}
}

func TestStore_Update_BuiltinRefused(t *testing.T) {
	store, ctx := setupTestStore(t)

	builtin := &Agent{
		ID: "builtin", WorkspaceID: "", Name: "内置", Department: "test", SystemPrompt: "orig", IsBuiltin: true,
	}
	if err := store.Create(ctx, builtin); err != nil {
		t.Fatal(err)
	}

	// 尝试更新内置角色应该被拒绝
	builtin.Name = "Modified"
	err := store.Update(ctx, "", builtin)
	if err == nil || !strings.Contains(err.Error(), "builtin agent cannot be modified") {
		t.Errorf("expected builtin modify error, got %v", err)
	}
}

func TestStore_Delete_BuiltinRefused(t *testing.T) {
	store, ctx := setupTestStore(t)

	builtin := &Agent{
		ID: "builtin", WorkspaceID: "", Name: "内置", Department: "test", SystemPrompt: "x", IsBuiltin: true,
	}
	if err := store.Create(ctx, builtin); err != nil {
		t.Fatal(err)
	}

	err := store.Delete(ctx, "", "builtin")
	if err == nil || !strings.Contains(err.Error(), "builtin agent cannot be deleted") {
		t.Errorf("expected builtin delete error, got %v", err)
	}
}

func TestStore_Update_CustomAgent(t *testing.T) {
	store, ctx := setupTestStore(t)

	custom := &Agent{
		ID: "custom", WorkspaceID: "ws-1", Name: "原名", Department: "test", SystemPrompt: "orig",
	}
	if err := store.Create(ctx, custom); err != nil {
		t.Fatal(err)
	}

	// 更新自定义角色应该成功
	custom.Name = "新名字"
	custom.SystemPrompt = "updated prompt"
	if err := store.Update(ctx, "ws-1", custom); err != nil {
		t.Fatalf("Update custom agent failed: %v", err)
	}

	got, _ := store.Get(ctx, "ws-1", "custom")
	if got.Name != "新名字" || got.SystemPrompt != "updated prompt" {
		t.Errorf("Update not persisted: %+v", got)
	}
}

func TestStore_Delete_CustomAgent(t *testing.T) {
	store, ctx := setupTestStore(t)

	custom := &Agent{
		ID: "custom", WorkspaceID: "ws-1", Name: "待删除", Department: "test", SystemPrompt: "x",
	}
	if err := store.Create(ctx, custom); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx, "ws-1", "custom"); err != nil {
		t.Fatalf("Delete custom agent failed: %v", err)
	}

	// Get 应该找不到
	_, err := store.Get(ctx, "ws-1", "custom")
	if err == nil {
		t.Error("deleted agent still exists")
	}
}

func TestStore_CountCustom(t *testing.T) {
	store, ctx := setupTestStore(t)

	_ = store.Create(ctx, &Agent{ID: "c1", WorkspaceID: "ws-1", Name: "1", Department: "x", SystemPrompt: "x"})
	_ = store.Create(ctx, &Agent{ID: "c2", WorkspaceID: "ws-1", Name: "2", Department: "x", SystemPrompt: "x"})
	_ = store.Create(ctx, &Agent{ID: "builtin", WorkspaceID: "", Name: "b", Department: "x", SystemPrompt: "x", IsBuiltin: true})

	count, err := store.CountCustom(ctx, "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("CountCustom = %d, want 2 (builtin should not be counted)", count)
	}
}
