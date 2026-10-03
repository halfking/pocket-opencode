// internal/finance/pg_isolated_test.go
package finance

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

const financeTestSchemaPrefix = "finance_test_"

// testPGDSN 只认 POCKET_TEST_POSTGRES_DSN。
//
// 原实现还会在它为空时回退去读 **POCKET_POSTGRES_DSN**——那是服务自己用的
// 生产连接串。于是「本机跑服务时顺手 go test ./...」会在零额外配置的情况下
// 把 finance 的建表迁移与测试数据直接写进生产 schema，而测试照报 ok。
// 这里删掉那个兜底：要跑就必须显式给出测试 DSN。
func testPGDSN() string {
	return os.Getenv("POCKET_TEST_POSTGRES_DSN")
}

// newIsolatedPGPool 返回钉在一次性 schema 上的连接池。
//
// 为什么必须隔离：本仓库的惯例是同一个 DSN 既喂服务也喂测试，所以 DSN 的
// search_path 完全可能就是生产 schema。原来的实现直接在那个连接上
// NewPGStore()，而它会执行建表迁移——测试数据因此落进生产表
// （生产库 opencode_pocket.finance_transactions 就是这样被建出来的）。
//
// 做法与 task / identity / vault / meeting 等 15 个 PG 测试助手一致：
// 自己生成 schema → CREATE → 把 search_path 钉上去 → cleanup 只 DROP 自己
// 生成的这一���个。
func newIsolatedPGPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := testPGDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping finance PG test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	schema := financeTestSchemaPrefix + hex.EncodeToString(suffix[:])

	rootCfg := cfg.Copy()
	delete(rootCfg.ConnConfig.RuntimeParams, "search_path")
	rootPool, err := pgxpool.NewWithConfig(ctx, rootCfg)
	if err != nil {
		t.Skipf("pgx connect (root): %v", err)
	}
	if perr := rootPool.Ping(ctx); perr != nil {
		rootPool.Close()
		t.Skipf("PostgreSQL not reachable: %v", perr)
	}
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}

	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("create test pool: %v", err)
	}
	if perr := pool.Ping(ctx); perr != nil {
		pool.Close()
		t.Fatalf("ping: %v", perr)
	}

	t.Cleanup(func() {
		pool.Close()
		// 纵深防御：只 DROP 自己生成的那一个 schema 名。
		if !strings.HasPrefix(schema, financeTestSchemaPrefix) {
			return
		}
		cctx, ccancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer ccancel()
		if _, err := rootPool.Exec(cctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Logf("cleanup: drop schema %s: %v", schema, err)
		}
		rootPool.Close()
	})
	return pool
}
