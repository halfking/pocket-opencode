package quota

// pg_store_test.go — integration tests for PG-backed budget store.
//
// Mirrors lobster/store_test.go: isolated schema per test, skipped when
// POCKET_TEST_POSTGRES_DSN is unset so `go test ./...` stays green in CI
// environments without PG.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testDSN() string {
	// 只认测试专用 DSN。回退读 POCKET_POSTGRES_DSN 会让本地 `go test ./...`
	// 零配置地打到生产库——实测已在生产库留下 meeting_test_* 残留 schema。
	return os.Getenv("POCKET_TEST_POSTGRES_DSN")
}

func newTestPGStore(t *testing.T) (*PGStore, func()) {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping quota PG integration test")
	}
	ctx := context.Background()
	// rootPool 不带 search_path：所有 DDL/DROP 都显式带 schema 名，不依赖它。
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	rootCfg := cfg.Copy()
	delete(rootCfg.ConnConfig.RuntimeParams, "search_path")
	rootPool, err := pgxpool.NewWithConfig(ctx, rootCfg)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	// Isolated schema so concurrent test runs don't collide.
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("rand: %v", err)
	}
	schema := "quota_test_" + hex.EncodeToString(suffix)
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	// 覆盖式设置 search_path，不能往 DSN 字符串后面拼 `&search_path=`：
	// 本仓库的惯例是同一个 DSN 既喂服务也喂测试，DSN 里往往已经带了
	// search_path（生产 schema）。拼接会得到两个同名参数，pgx 取第一个，
	// 于是隔离静默失效、NewPGStore 的建表迁移直接落到那个 schema 里。
	// 献祭 schema 实测：修复前 quota_budgets 被建到了 sacrificial_prod，
	// 且 TestPGStore_BudgetsFor_AcceptsZeroPeriod 因此失败。
	scopedCfg := cfg.Copy()
	scopedCfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	scopedPool, err := pgxpool.NewWithConfig(ctx, scopedCfg)
	if err != nil {
		t.Fatalf("create scoped pool: %v", err)
	}
	cleanup := func() {
		scopedPool.Close()
		// 纵深防御：只 DROP 自己生成的那一个 schema 名。
		if !strings.HasPrefix(schema, "quota_test_") {
			rootPool.Close()
			return
		}
		_, _ = rootPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
	}
	store, err := NewPGStore(scopedPool)
	if err != nil {
		cleanup()
		t.Fatalf("NewPGStore: %v", err)
	}
	return store, cleanup
}

func TestPGStore_BudgetsFor_FiltersByPeriod(t *testing.T) {
	s, cleanup := newTestPGStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()

	mustSetPG(t, s, Budget{WorkspaceID: "ws-a", Kind: "cost_usd", Limit: 100, PeriodStart: now.Add(-time.Hour), PeriodEnd: now.Add(time.Hour)})
	mustSetPG(t, s, Budget{WorkspaceID: "ws-a", Kind: "tokens", Limit: 1000, PeriodStart: now.Add(time.Hour), PeriodEnd: now.Add(2 * time.Hour)})

	got, err := s.BudgetsFor(ctx, "ws-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 budget in window, got %d", len(got))
	}
	if got[0].Kind != "cost_usd" {
		t.Fatalf("expected cost_usd budget, got %q", got[0].Kind)
	}
}

func TestPGStore_BudgetsFor_AcceptsZeroPeriod(t *testing.T) {
	s, cleanup := newTestPGStore(t)
	defer cleanup()
	ctx := context.Background()

	mustSetPG(t, s, Budget{WorkspaceID: "ws-a", Kind: "tokens", Limit: 1000})

	got, _ := s.BudgetsFor(ctx, "ws-a", time.Now())
	if len(got) != 1 {
		t.Fatalf("zero-period budget must always apply, got %d", len(got))
	}
}

func TestPGStore_RejectsEmptyWorkspace(t *testing.T) {
	s, cleanup := newTestPGStore(t)
	defer cleanup()

	if err := s.Set(context.Background(), Budget{Kind: "tokens"}); err == nil {
		t.Fatal("expected error for empty workspace_id")
	}
}

func TestPGStore_NilPoolErrors(t *testing.T) {
	if _, err := NewPGStore(nil); err == nil {
		t.Fatal("NewPGStore(nil) must error")
	}
}

func mustSetPG(t *testing.T, s *PGStore, b Budget) {
	t.Helper()
	if err := s.Set(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}