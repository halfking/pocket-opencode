package chatagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// setupTestStore 起一个**隔离 schema** 里的 PG store。
//
// ## 为什么必须隔离（2026-10-02 实测出来的，不是理论风险）
//
// 原来这里是 `pgxpool.New(ctx, dbURL)` —— **原样继承 DSN 的 `search_path`**。
// 于是这个包里所有 unqualified 名字都按调用方的 search_path 解析，而
// PG 的规则是：当前 schema 找不到就**回落到 public**。
//
// 具体到本机：`search_path=opencode_pocket` 里**没有** `chat_agents` 表，
// 所以它落到了 `public.chat_agents`。而 `cmd/pocketd/main.go:1473`
// `initChatAgentStores` 在 `pool != nil` 时正是用 PG store —— 也就是说
// **`public.chat_agents` 就是应用自己正在用的那张表**（`data/chat_agents.sqlite`
// 是 PG 不可用时的 SQLite fallback，最后修改停在 2026-09-30 03:19）。
//
// 于是这个 helper 每次跑都会对**应用正在用的表**做两件事：
//   1. `store.Init` → `CREATE TABLE IF NOT EXISTS`（改的是生产表结构）
//   2. 下面那条 `DELETE FROM chat_agents WHERE ...`（删的是生产行）
//
// 实测症状：`TestStore_List_WorkspaceIsolation` 报
// `ws-a should see 2 agents (custom-a + builtin), got 278` —— 它看见了 278 行
// 自己从没建过的数据。
//
// ## 修法
//
// 自己生成一个 schema，把 `search_path` **只**指向它（不追加 public）：
// 这样既隔离了写入，又让「引用一张不存在的表」变成**报错**而不是静默落到
// public —— 静默回落正是这个缺陷的根因。收尾只 DROP 自己那个 schema。
//
// 同一个模式见 `internal/email/store_workspace_test.go:61`。
func setupTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()

	// 从环境变量读取数据库连接字符串
	dbURL := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dbURL == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set")
	}

	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	schema := "chatagent_test_" + hex.EncodeToString(buf)

	rootPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		rootPool.Close()
		t.Fatalf("create schema %s: %v", schema, err)
	}
	// pgxpool.New 只解析 DSN 不建连：DSN 已设但 PG 不可达时要到 Ping 才能发现。
	// 连接失败与环境未设 DSN 同待遇 t.Skip，避免预置问题污染 CI。
	pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
	if perr := rootPool.Ping(pingCtx); perr != nil {
		pingCancel()
		rootPool.Close()
		t.Skipf("PostgreSQL not reachable: %v", perr)
	}
	pingCancel()

	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	// **只**指向自己的 schema：不追加 public，让「表不存在」报错而不是回落。
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
		t.Fatalf("scoped pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, derr := rootPool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); derr != nil {
			t.Logf("drop schema %s: %v", schema, derr)
		}
		rootPool.Close()
	})

	store := NewStore(pool)
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// 隔离 schema 是本轮自己建的，理论上没有历史数据；仍清一次以保证重复跑幂等。
	if _, err := pool.Exec(ctx, "DELETE FROM chat_agents"); err != nil {
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

func TestStore_Update_BuiltinAllowed(t *testing.T) {
	store, ctx := setupTestStore(t)

	builtin := &Agent{
		ID: "builtin", WorkspaceID: "", Name: "内置", Department: "test", SystemPrompt: "orig", IsBuiltin: true,
	}
	if err := store.Create(ctx, builtin); err != nil {
		t.Fatal(err)
	}

	// 内置角色允许维护性修改（专家库可维护）
	builtin.Name = "Modified"
	builtin.SystemPrompt = "updated"
	if err := store.Update(ctx, "ws-any", builtin); err != nil {
		t.Fatalf("builtin modify should be allowed, got %v", err)
	}

	got, err := store.Get(ctx, "ws-any", "builtin")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Modified" || got.SystemPrompt != "updated" {
		t.Errorf("builtin update not persisted: %+v", got)
	}
	if !got.IsBuiltin {
		t.Error("builtin flag should be preserved after update")
	}
}

func TestStore_Delete_BuiltinAllowed(t *testing.T) {
	store, ctx := setupTestStore(t)

	builtin := &Agent{
		ID: "builtin", WorkspaceID: "", Name: "内置", Department: "test", SystemPrompt: "x", IsBuiltin: true,
	}
	if err := store.Create(ctx, builtin); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx, "ws-any", "builtin"); err != nil {
		t.Fatalf("builtin delete should be allowed, got %v", err)
	}

	if _, err := store.Get(ctx, "ws-any", "builtin"); err == nil {
		t.Error("deleted builtin still exists")
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
