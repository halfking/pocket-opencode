package pgmigrate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newScopedPools 建一对指向**同一随机 schema** 的 pool（模拟两个 pocketd
// 实例连同一个库），返回 root pool（建/删 schema）、scoped pool 与 schema 名。
//
// 只读 POCKET_TEST_POSTGRES_DSN，不回退 POCKET_POSTGRES_DSN —— 回退会让
// 零配置的 `go test ./...` 打到生产库（与 internal/calendar/store_pg_test.go
// 同款理由）。
func newScopedPools(t *testing.T) (root *pgxpool.Pool, scoped *pgxpool.Pool, schema string) {
	t.Helper()
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping pgmigrate PG integration test")
	}
	ctx := context.Background()
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		root.Close()
		t.Fatalf("rand: %v", err)
	}
	schema = "pgmigrate_test_" + hex.EncodeToString(suffix)
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		root.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_, _ = root.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		root.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	scoped, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		_, _ = root.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		root.Close()
		t.Fatalf("scoped pool: %v", err)
	}
	return root, scoped, schema
}

func dropSchema(t *testing.T, root *pgxpool.Pool, schema string) {
	t.Helper()
	ctx := context.Background()
	if _, err := root.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
		t.Logf("drop schema %s: %v", schema, err)
	}
	root.Close()
}

// migrationBatch 是**复刻真实死锁形状**的 DDL 批次：一条多语句 simple
// query，先建两张表再分别 ALTER —— PostgreSQL 会把它当成单个隐式事务，
// 批次里的锁一直持有到提交。
//
// 这正是 2026-10-25 实测把 18099 打死的那条路径（task store 的
// CREATE tasks / CREATE task_run_bindings / ALTER task_run_bindings /
// ALTER tasks ...）。
const migrationBatch = `
CREATE TABLE IF NOT EXISTS mig_a (
	id TEXT PRIMARY KEY,
	v TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS mig_b (
	id TEXT PRIMARY KEY,
	v TEXT NOT NULL DEFAULT ''
);
ALTER TABLE mig_a ADD COLUMN IF NOT EXISTS extra_a TEXT NOT NULL DEFAULT '';
ALTER TABLE mig_b ADD COLUMN IF NOT EXISTS extra_b TEXT NOT NULL DEFAULT '';
ALTER TABLE mig_a ADD COLUMN IF NOT EXISTS extra_a2 TEXT NOT NULL DEFAULT '';
ALTER TABLE mig_b ADD COLUMN IF NOT EXISTS extra_b2 TEXT NOT NULL DEFAULT '';
`

// TestAcquireIsExclusive 是这条修复的**核心判据**：同一个 schema 上，
// 第二个实例拿不到锁，直到第一个实例释放。
//
// 修复前根本没有这层互斥，两个实例同时跑 DDL 会互持 ACCESS EXCLUSIVE 锁
// ⇒ 40P01 死锁 ⇒ 受害实例启动即死（18099 的实测症状）。
func TestAcquireIsExclusive(t *testing.T) {
	root, poolA, schema := newScopedPools(t)
	defer dropSchema(t, root, schema)

	ctx := context.Background()
	lockA, err := Acquire(ctx, poolA, 5*time.Second)
	if err != nil {
		t.Fatalf("first acquire must succeed: %v", err)
	}

	// 第二个实例（同 schema）必须在 A 持有期间拿不到锁。
	// 用短 wait 逼它失败 —— 失败信息本身就是判据。
	if _, err := Acquire(ctx, poolA, 500*time.Millisecond); err == nil {
		lockA.Release()
		t.Fatal("second acquire succeeded while the first still held the lock: the schema lock is not exclusive")
	} else if !strings.Contains(err.Error(), "locked by another instance") {
		lockA.Release()
		t.Fatalf("unexpected error for the contended acquire: %v", err)
	}

	// A 释放后，B 必须能拿到（否则这个锁会把后续实例永久饿死）。
	lockA.Release()
	lockB, err := Acquire(ctx, poolA, 5*time.Second)
	if err != nil {
		t.Fatalf("acquire after release must succeed: %v", err)
	}
	lockB.Release()
}

// TestConcurrentMigrationsDoNotDeadlock 复现现场：多个实例并发对同一 schema
// 跑迁移批次。持锁期间只有一方在跑 DDL ⇒ 不会有 40P01。
func TestConcurrentMigrationsDoNotDeadlock(t *testing.T) {
	root, pool, schema := newScopedPools(t)
	defer dropSchema(t, root, schema)

	const instances = 4
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		errs    []error
		started = make(chan struct{}, instances)
	)
	for i := 0; i < instances; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock, err := Acquire(context.Background(), pool, 30*time.Second)
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			defer lock.Release()
			started <- struct{}{}
			_, err = pool.Exec(context.Background(), migrationBatch)
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	// 让所有 goroutine 同时起跑，最大化并发窗口。
	for i := 0; i < instances; i++ {
		select {
		case <-started:
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for instances to start")
		}
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent migration failed (a 40P01 deadlock here is the bug being fixed): %v", err)
		}
	}

	// 迁移必须真的落了地（不能是"没报错但也没建表"）。
	for _, table := range []string{"mig_a", "mig_b"} {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2`,
			schema, table).Scan(&n); err != nil {
			t.Fatalf("inspect %s: %v", table, err)
		}
		if n == 0 {
			t.Fatalf("table %s was not created by the concurrent migrations", table)
		}
	}
}

// TestLockIsScopedPerSchema 锁的 key 必须含 schema：不同 schema 不互相阻塞。
func TestLockIsScopedPerSchema(t *testing.T) {
	root, poolA, schemaA := newScopedPools(t)

	// 第二个 schema（复用同一个 root pool，另一个 scoped pool）。
	ctx := context.Background()
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		root.Close()
		t.Fatalf("rand: %v", err)
	}
	schemaB := "pgmigrate_test_b_" + hex.EncodeToString(suffix)
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+schemaB); err != nil {
		root.Close()
		t.Fatalf("create schema B: %v", err)
	}
	defer func() {
		_, _ = root.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schemaA+" CASCADE")
		_, _ = root.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schemaB+" CASCADE")
		root.Close()
	}()
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schemaB + ",public"
	poolB, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("scoped pool B: %v", err)
	}
	defer poolB.Close()

	lockA, err := Acquire(ctx, poolA, 5*time.Second)
	if err != nil {
		t.Fatalf("acquire on schema A: %v", err)
	}
	defer lockA.Release()

	// schema B 的锁必须**立刻**拿到（不被 A 阻塞）。
	start := time.Now()
	lockB, err := Acquire(ctx, poolB, 2*time.Second)
	if err != nil {
		t.Fatalf("acquire on schema B must not be blocked by schema A: %v", err)
	}
	defer lockB.Release()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("schema B acquire took %s: the lock is not schema-scoped", elapsed)
	}
}
