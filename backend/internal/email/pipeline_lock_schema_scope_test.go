package email

// pipeline_lock_schema_scope_test.go —— 每日流水线锁的 **schema 分片**判据。
//
// ## 缺陷背景（2026-10-03 实测）
//
// PG 的会话级 advisory lock 作用域是**数据库**，与 search_path 无关。而本仓库
// 多个 pocketd 实例连的是同一个 postgres 库、各自钉在不同 schema 上：
//
//	pid 61356  schema=opencode_pocket  :18099   （生产）
//	pid 69476  schema=rssdemo_test     :18190   （另一个实例）
//
// 于是每天 08:00 两边抢同一把锁。实测当天生产整轮被跳过：
// `[email/pipeline] 每日定时流水线跨进程锁已被其它实例持有，本轮跳过`，
// 重要邮件提醒积压到 pending_high=35。抢到锁的那个实例跑的是另一个 schema
// 的数据，对生产毫无意义——纯粹是跨租户误伤。
//
// ## 三条边界
//
//  1. 同库 + **同** schema：必须仍然互斥（这是这把锁的全部意义，不能被分片改掉）；
//  2. 同库 + **不同** schema：必须互不干扰（这才是本次要修的）；
//  3. 键的构造本身：schema 前缀真的进了哈希输入（不需要数据库，任何环境都跑）。
//
// ## 负控（实测可转红）
//
//  - 把 dailyPipelineLockKey 的 `schema + ":"` 前缀去掉（即退回固定键）
//    → TestDailyPipelineLock_DifferentSchemasAreIndependent **转红**
//      （第二个 pool 拿到 busy），同时 TestDailyPipelineLock_LockKeyCarriesSchema
//      转红。
//  - 只把前缀去掉、保留 release 里的常量解锁（键对不上）
//    → TestDailyPipelineLock_SameSchemaStillExcludes 转红或
//      ReleaseDoesNotLeakIntoPool 转红（解锁失败走兜底销毁连接）。

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── 判据 3：键的构造（不需要数据库） ──────────────────────────────────

// fakeSchemaRow 只实现 pgx.Row，够 Scan 用。
type fakeSchemaRow struct {
	schema string
	err    error
	got    string // 记录实际执行的 SQL，用来钉住 COALESCE 那条
}

func (r *fakeSchemaRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return fmt.Errorf("fakeSchemaRow: expected 1 scan target, got %d", len(dest))
	}
	p, ok := dest[0].(*string)
	if !ok {
		return fmt.Errorf("fakeSchemaRow: expected *string scan target, got %T", dest[0])
	}
	*p = r.schema
	return nil
}

// fakeSchemaQuerier 记录调用次数，防止「查询根本没发就被判为通过」。
type fakeSchemaQuerier struct {
	row   *fakeSchemaRow
	calls int
	sql   string
}

func (q *fakeSchemaQuerier) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	q.calls++
	q.sql = sql
	return q.row
}

// TestDailyPipelineLock_LockKeyCarriesSchema —— 键必须真的带上 schema。
//
// 不需要数据库，因此在任何环境都执行（包括没有测试库的机器）。这是防
// 「判据恒真」的主力：后面两条真库用例在没配 DSN 时会 skip，若只有它们
// 守着前缀这件事，删掉前缀就会一路绿灯通过 CI。
func TestDailyPipelineLock_LockKeyCarriesSchema(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   string
	}{
		{"生产 schema", "opencode_pocket", "opencode_pocket:" + DailyPipelineLockKey},
		{"另一个租户的 schema", "rssdemo_test", "rssdemo_test:" + DailyPipelineLockKey},
		// search_path 为空时 current_schema() 返回 NULL，SQL 里的
		// COALESCE 把它落到 public（= 未做 schema 隔离的旧部署）。
		{"NULL 落到 public", "public", "public:" + DailyPipelineLockKey},
		{"测试自建 schema", "email_ws_test_0a1b2c3d", "email_ws_test_0a1b2c3d:" + DailyPipelineLockKey},
	}
	for _, c := range cases {
		q := &fakeSchemaQuerier{row: &fakeSchemaRow{schema: c.schema}}
		got, err := dailyPipelineLockKey(context.Background(), q)
		if err != nil {
			t.Errorf("%s: dailyPipelineLockKey returned err %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: lock key = %q, want %q", c.name, got, c.want)
		}
		if q.calls != 1 {
			t.Errorf("%s: schema query ran %d times, want exactly 1", c.name, q.calls)
		}
		if !strings.Contains(strings.ToUpper(q.sql), "CURRENT_SCHEMA") {
			t.Errorf("%s: schema query was %q, want it to read current_schema() — "+
				"the key must come from the connection that actually holds the lock", c.name, q.sql)
		}
		if !strings.Contains(strings.ToUpper(q.sql), "COALESCE") {
			t.Errorf("%s: schema query was %q, want COALESCE around current_schema(): "+
				"without it a NULL (empty search_path) makes pgx fail the scan and the "+
				"whole lock degrades to Unavailable", c.name, q.sql)
		}
	}
}

// ── 判据 1 与 2：真库对照 ───────────────────────────────────────────

// newPoolPinnedToSchema 在同一个库上另起一个池，并把它钉到指定 schema。
//
// ownsSchema 为 true 时 cleanup 会 DROP 这个 schema，为 false 时**不动**它
// ——复用别人（newWorkspaceTestStore）建的 schema 时必须传 false，否则两处
// cleanup 抢着 DROP 同一个 schema，其中一次必然报错，而报错发生在 t.Logf
// 里，测试照样绿。
func newPoolPinnedToSchema(t *testing.T, baseDSN, schema string, ownsSchema bool) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	if ownsSchema {
		rootCfg, err := pgxpool.ParseConfig(baseDSN)
		if err != nil {
			t.Fatalf("parse dsn: %v", err)
		}
		delete(rootCfg.ConnConfig.RuntimeParams, "search_path")
		rootPool, err := pgxpool.NewWithConfig(ctx, rootCfg)
		if err != nil {
			t.Fatalf("root pool: %v", err)
		}
		if _, err := rootPool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema); err != nil {
			rootPool.Close()
			t.Fatalf("create schema %s: %v", schema, err)
		}
		rootPool.Close()
	}
	cfg, err := pgxpool.ParseConfig(baseDSN)
	if err != nil {
		t.Fatalf("parse dsn (scoped): %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("scoped pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if !ownsSchema {
			return
		}
		rootCfg, err := pgxpool.ParseConfig(baseDSN)
		if err != nil {
			t.Logf("cleanup: parse dsn: %v", err)
			return
		}
		delete(rootCfg.ConnConfig.RuntimeParams, "search_path")
		rootPool, err := pgxpool.NewWithConfig(context.Background(), rootCfg)
		if err != nil {
			t.Logf("cleanup: root pool: %v", err)
			return
		}
		defer rootPool.Close()
		// 必须用**根**连接 DROP：被钉在目标 schema 上的连接无法删掉它自己。
		if _, err := rootPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Logf("cleanup: drop schema %s: %v", schema, err)
		}
	})
	return pool
}

func currentSchemaOf(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var schema string
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(current_schema(), 'public')`).Scan(&schema); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	return schema
}

// TestDailyPipelineLock_DifferentSchemasAreIndependent —— 同一个库、两个不同
// schema 的实例必须能**同时**取到锁。
//
// 这就是本次修复的正向判据。修复前锁键是固定的 `email:daily-pipeline`，
// 第二个实例必然拿到 busy，生产因此整轮跳过（2026-10-03 实测）。
//
// 判据形态说明：断言的是**可观测后果**（第二个实例拿不拿得到锁），不是
// 「锁键字符串里含不含 schema」——后者只要在代码里加一行没用的拼接就会通过。
func TestDailyPipelineLock_DifferentSchemasAreIndependent(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; schema-scoping assertion skips")
	}
	ctx := context.Background()
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	otherSchema := dailyPipelineLockTestSchemaPrefix + "otherschema"
	otherPool := newPoolPinnedToSchema(t, dsn, otherSchema, true)
	otherStore, err := NewStore(otherPool)
	if err != nil {
		t.Fatalf("NewStore for the other schema: %v", err)
	}

	// 判据自检：两个 store 必须真的落在**不同** schema 上。
	//
	// 自检查的是 schema，**不是**锁键。曾经写成「两个锁键必须不同」——那等于把
	// 待测性质本身当成了前置条件：键的构造一坏，自检就先炸掉，于是负控永远
	// 看不到真正的行为断言（schema B 拿到 busy）红在哪，只看到一句夹具抱怨。
	// 负控（去掉 schema 前缀）必须打在「state = busy」那一行上，才说明判据
	// 真的在测跨 schema 不互斥。
	schemaA := currentSchemaOf(t, store.pool)
	schemaB := currentSchemaOf(t, otherStore.pool)
	if schemaA == schemaB {
		t.Fatalf("the two stores resolved to the SAME schema %q — the fixture did "+
			"not actually put them on different schemas, so this test would be "+
			"asserting the mutually-exclusive case instead", schemaA)
	}
	t.Logf("schema A = %q, lock key = %q", schemaA, testDailyPipelineLockKey(t, store))
	t.Logf("schema B = %q, lock key = %q", schemaB, testDailyPipelineLockKey(t, otherStore))

	releaseA, stateA, errA := store.TryLockDailyPipeline(ctx)
	if errA != nil {
		t.Fatalf("schema A acquire: %v", errA)
	}
	if stateA != DailyPipelineLockAcquired {
		t.Fatalf("schema A state = %v, want acquired", stateA)
	}
	defer releaseA()

	releaseB, stateB, errB := otherStore.TryLockDailyPipeline(ctx)
	if errB != nil {
		t.Fatalf("schema B acquire should not error, got %v", errB)
	}
	if stateB != DailyPipelineLockAcquired {
		if releaseB != nil {
			releaseB()
		}
		t.Fatalf("schema B state = %v, want acquired — an instance working on %s was "+
			"blocked by an instance working on a different schema of the same database. "+
			"PG advisory locks are database-scoped, so this only works if the lock key "+
			"carries the schema.", stateB, otherSchema)
	}
	releaseB()
}

// TestDailyPipelineLock_SameSchemaStillExcludes —— 分片不能把互斥改掉。
//
// 这是本次修复最容易犯的错：为了让不同 schema 不打架，把锁改成「各算各的」
// 却忘了同 schema 仍要共享同一把。没有这条对照，一个「每个实例用随机键」的
// 实现也能让上面那条转绿。
func TestDailyPipelineLock_SameSchemaStillExcludes(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; same-schema exclusion assertion skips")
	}
	ctx := context.Background()
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	// 第二个池钉到**同一个** schema：不同物理连接 = 不同会话 = 另一个进程。
	var schema string
	if err := store.pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	if !dailyPipelineLockSchemaIsIsolated(schema) {
		t.Fatalf("test store runs in schema %q, which this run did not create — the "+
			"exclusion assertion below would be advisory-locking some other schema, "+
			"possibly production", schema)
	}
	pool2 := newPoolPinnedToSchema(t, dsn, schema, false)
	store2, err := NewStore(pool2)
	if err != nil {
		t.Fatalf("NewStore for the same schema: %v", err)
	}

	release1, state1, err1 := store.TryLockDailyPipeline(ctx)
	if err1 != nil || state1 != DailyPipelineLockAcquired {
		t.Fatalf("first acquire: state=%v err=%v", state1, err1)
	}
	defer release1()

	release2, state2, err2 := store2.TryLockDailyPipeline(ctx)
	if err2 != nil {
		t.Fatalf("second acquire should not error, got %v", err2)
	}
	if state2 != DailyPipelineLockBusy {
		if release2 != nil {
			release2()
		}
		t.Fatalf("second acquire on the SAME schema: state = %v, want busy — two "+
			"pocketd instances on one schema would both run the daily pipeline and "+
			"push duplicate notifications", state2)
	}
	if release2 != nil {
		t.Fatal("busy must not return a release func")
	}
}
