package pgmigrate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	return newScopedPoolsWithMaxConns(t, 0)
}

// newScopedPoolsWithMaxConns 与 newScopedPools 同上，但**显式指定连接数上限**
// （maxConns <= 0 ⇒ 用 pgxpool 的默认值）。
//
// ⚠⚠ 为什么要显式指定（2026-10-28 §976）：
//
//	pgxpool 的默认上限是 **max(4, runtime.NumCPU())** ⇒ 同一份代码
//	在 16 核的开发机上上限 16、在 2 核的 GitHub runner 上限 4。
//	⇒ 「会不会死锁」取决于**跑在哪台机器上** ⇒ 量具依赖环境属性，不是被测行为变了。
//	⇒ 并发那条判据必须**把上限钉死**，否则它在开发机上恒绿、在 CI 上偶发红，
//	  变成一场靠 CPU 核数抽签的测试。
func newScopedPoolsWithMaxConns(t *testing.T, maxConns int32) (root *pgxpool.Pool, scoped *pgxpool.Pool, schema string) {
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
	if maxConns > 0 {
		cfg.MaxConns = maxConns
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
//
// ⚠⚠ 连接数上限**钉死成 instances**（2026-10-28 §976）：
//
//	这条判据要抓的正是「等待者把池容量吃光 ⇒ 持锁方的 DDL 借不到连接」。
//	若上限随 CPU 核数浮动（pgxpool 默认 max(4, NumCPU)），
//	它在 16 核开发机上**恒绿**、在 2 核 CI 上**恒红** ⇒ 变成靠核数抽签的测试。
//	⇒ 上限钉成 4 之后：旧实现在**任何**机器上都必红（可复现的回归护栏），
//	  新实现在任何机器上都必绿 ⇒ 这条判据从此与运行环境无关。
func TestConcurrentMigrationsDoNotDeadlock(t *testing.T) {
	const instances = 4
	root, pool, schema := newScopedPoolsWithMaxConns(t, instances)
	defer dropSchema(t, root, schema)

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

// TestWaitingAcquireDoesNotStarveThePool 是上面那条死锁的**直接判据**
// （2026-10-28 §976）。
//
// 形状：一个实例持着锁，另一个实例在等它。
// 判据：**等锁期间，池子必须持续能服务别的查询**。
//
// 旧实现在整个等待期攥着借来的连接不放 ⇒ 上限 2 的池被「持锁方 1 条 +
// 等待方 1 条」占满 ⇒ 下面这句 `pool.Exec` **借不到连接**（它会阻塞到超时）。
// 新实现把连接还回池子 ⇒ 池里始终有空位 ⇒ 这句能借到连接并跑完。
//
// ⚠⚠ 这条判据**写错过两版**，两版都恒绿，如实记录（见 §996）：
//
//	第 1 版：等待方预算(2s) < 检查窗口(3s)
//	  ⇒ 等待方在窗口结束前就还回连接 ⇒ 旧实现也能借到 ⇒ 恒绿（无鉴别力）。
//	第 2 版：只查一次 Exec 成不成功
//	  ⇒ 启动竞态：第一句 Exec 跑在等待方借到连接**之前** ⇒ 借到了 ⇒ 恒绿。
//	第 3 版（当前）：**在整个等待窗口里连续查**，一次失败即红。
//	  ⇒ 旧实现：B 借到 conn#2 之后**每一次** Exec 都借不到 ⇒ 必红。
//	  ⇒ 新实现：B 每 250ms 还一次 conn#2 ⇒ 每次都借得到 ⇒ 必绿。
func TestWaitingAcquireDoesNotStarveThePool(t *testing.T) {
	const (
		waiterBudget = 6 * time.Second // 等待方的预算：它必然等不到锁
		checkWindow  = 4 * time.Second // 检查窗口：必须**小于**等待方预算，
		//                              否则等待方先还连接，判据失去鉴别力
	)
	root, pool, schema := newScopedPoolsWithMaxConns(t, 2)
	defer dropSchema(t, root, schema)

	ctx := context.Background()
	lockA, err := Acquire(ctx, pool, 5*time.Second)
	if err != nil {
		t.Fatalf("first acquire must succeed: %v", err)
	}
	defer lockA.Release()

	// B 必然等不到锁（A 持着呢）⇒ 它会一路等到预算耗尽。
	waitErrCh := make(chan error, 1)
	go func() {
		_, werr := Acquire(ctx, pool, waiterBudget)
		waitErrCh <- werr
	}()

	// 判据本体：在整个检查窗口里，池子必须**一直**能干活。
	// 一次失败就红 —— 因为旧实现下窗口内的**每一次**尝试都会失败。
	deadline := time.Now().Add(checkWindow)
	for {
		execCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		_, execErr := pool.Exec(execCtx, `SELECT 1`)
		cancel()
		if execErr != nil {
			t.Fatalf("池子被等待中的 Acquire 饿死了（等锁期间连一条连接都借不到）：%v", execErr)
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// B 必须是因为「锁被别人拿着」而失败，而不是因为别的原因。
	select {
	case werr := <-waitErrCh:
		if werr == nil {
			t.Fatal("等待中的 Acquire 竟然成功了：A 还持着锁")
		}
		if !strings.Contains(werr.Error(), "locked by another instance") {
			t.Fatalf("等待中的 Acquire 应报「被别的实例锁着」，实际：%v", werr)
		}
	case <-time.After(waiterBudget + 5*time.Second):
		t.Fatal("等待中的 Acquire 既没成功也没失败：它卡住了")
	}
}

// TestAcquireRespectsContextCancellation 取消必须真的取消（2026-10-28 §976）。
//
// ⚠ **这条是「特性刻画」而不是本次修复的回归护栏**，如实标注：
// 旧实现在取消上的表现**本来就基本正确**（它每次循环都发一条查询，
// 而查询本身尊重 ctx ⇒ 取消最多让它多等一个 250ms 的睡眠）。
// 它并不是「恒真」的判据 —— 取消后它**确实**返回、确实带 context.Canceled。
//
// 本次改动给它补上的是两件**旧实现没有**的事：
//
//	① 取消时**主动**报错，而不是「碰巧」因为下一条查询失败才报错；
//	② 报的错是「acquire cancelled」，而**不是**「try advisory lock: …」
//	   —— 后者会把一次取消说成一次锁失败，让调用方以为「再等等就好」。
//
// 所以这里断言的是**错误归因**：取消必须是取消，不能被说成锁问题。
func TestAcquireRespectsContextCancellation(t *testing.T) {
	root, pool, schema := newScopedPoolsWithMaxConns(t, 2)
	defer dropSchema(t, root, schema)

	ctx := context.Background()
	lockA, err := Acquire(ctx, pool, 5*time.Second)
	if err != nil {
		t.Fatalf("first acquire must succeed: %v", err)
	}
	defer lockA.Release()

	cancelCtx, cancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() {
		_, werr := Acquire(cancelCtx, pool, 30*time.Second)
		errCh <- werr
	}()

	// 等 B 真的进入等待（它必然等不到锁）。
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case werr := <-errCh:
		if werr == nil {
			t.Fatal("ctx 被取消后 Acquire 竟然成功了")
		}
		if !errors.Is(werr, context.Canceled) {
			t.Fatalf("取消后应报 context.Canceled，实际：%v", werr)
		}
		// 归因：不能被说成「锁被别人拿着」（那会让调用方再等一个整轮预算）。
		if strings.Contains(werr.Error(), "locked by another instance") {
			t.Fatalf("取消被误报成「锁被别人拿着」：%v", werr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消后 Acquire 没有立刻返回：它无视了取消")
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
