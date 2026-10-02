package email

// pipeline_lock_test.go — 每日定时流水线的跨进程互斥锁（需求 1/4 的定时那一半）。
//
// ## 缺陷背景
//
// 三个 pocketd 实例（18099 / 18077 / 18100）共享同一个 PG schema，各自在本进程
// 里排了同一点的每日流水线。进程内的 emailPipelineMu（server 包）与 scheduler 的
// sync.Once 都是**进程内**互斥，拦不住它们，于是同一点会跑 N 轮。实测伤害不是
// "慢一点"：重要邮件提醒的 MarkEmailsNotified 标记写在整个推送循环**之后**，而
// notifications 表除主键外没有唯一约束，于是同一封邮件被推 N 份。
//
// ## 这组用例守的四条边界
//
//  1. 锁是**会话级**的：同一条 pool 的两条不同连接必须互斥（否则它只是个
//     进程内 map，多实例照样各跑各的）。
//  2. 独立 pool（= 两个进程）之间同样互斥。
//  3. release 之后锁真的还回去了，且**没有把带锁的连接归还进池子**——后者是
//     唯一会造成永久性故障的写法，所以单列一条用例。
//  4. 没有连接池时返回 Unavailable 而不是 Busy：两种"拿不到"对调用方的
//     含义完全相反（前者照跑、后者跳过），混在一起会让一次 DB 抖动把每日
//     流水线永久静默掉。
//
// ## 负控（实测，见 docs/handoff/2026-10-03-round28-*.md）
//
//  - releaseDailyPipelineLock 跳过 unlock 直接 conn.Release()
//    → TestDailyPipelineLock_ReleaseDoesNotLeakIntoPool 转红。
//  - pg_try_advisory_lock 换成 pg_advisory_lock（改成排队而非 Try）
//    → TestDailyPipelineLock_SecondAcquireIsBusy 转红（会一直阻塞到超时）。
//  - 把 DailyPipelineLockUnavailable 当成 Busy 返回
//    → TestDailyPipelineLock_NoPoolIsUnavailableNotBusy 转红。

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// lockProbeOnFreshConn 在一条"刚取出来"的连接上直接问 PG：这把锁此刻被谁拿着。
//
// ⚠ 这条**只**在"持锁的那条连接还活着、没被归还进池子"的前提下有效。
// PG 的会话级 advisory lock 是**可重入**的（实测见
// diag_advisory_reentrant_test.go：同一会话连续 lock 两次都返回 true，
// unlock 一次之后第三次 lock 仍返回 true），所以一旦持锁连接已经 Release，
// 池子极可能把**同一条物理连接**发给探针，同会话再次 lock 自然成功 ——
// 探针于是永远返回 true，"有没有泄漏"这个问题它答不了。
// 判泄漏要用 advisoryLockHolders（直接问 pg_locks 系统表）。
func lockProbeOnFreshConn(t *testing.T, store *Store) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := store.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire probe conn: %v", err)
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, DailyPipelineLockKey,
	).Scan(&got); err != nil {
		t.Fatalf("probe lock: %v", err)
	}
	// 探针自己拿到锁就必须还回去，否则会污染后续断言。
	if got {
		var unlocked bool
		if err := conn.QueryRow(ctx,
			`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, DailyPipelineLockKey,
		).Scan(&unlocked); err != nil {
			t.Fatalf("probe unlock: %v", err)
		}
		if !unlocked {
			t.Fatal("probe acquired the lock but pg_advisory_unlock reported false")
		}
	}
	return got
}

// advisoryLockHolders 直接问 pg_locks：这把 daily-pipeline 锁此刻被几个会话持有。
//
// 这是唯一能穿透可重入的判据。实测（diag_advisory_reentrant_test.go）：同会话
// lock 3 次、unlock 1 次之后，pg_locks 里仍然只有 1 行 —— 行数不随重入计数增长。
// 于是「0 行」等价于"没有任何会话持有它"，包括"没有任何一条带锁连接被留在池里"。
//
// key 的编码：hashtextextended 返回 bigint，PG 把它拆成 classid(高 32 位) 与
// objid(低 32 位) 存进 pg_locks，所以要按同样的拆法反查，否则会数到别人的锁。
func advisoryLockHolders(t *testing.T, store *Store) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := store.pool.Query(ctx, `
		SELECT pid
		  FROM pg_locks
		 WHERE locktype = 'advisory'
		   AND classid = ((hashtextextended($1, 0) >> 32) & 4294967295)
		   AND objid   =  (hashtextextended($1, 0) &  4294967295)
		   AND objsubid = 1
		   AND granted`, DailyPipelineLockKey)
	if err != nil {
		t.Fatalf("query pg_locks: %v", err)
	}
	defer rows.Close()
	n := 0
	var pids []int32
	for rows.Next() {
		var pid int32
		if err := rows.Scan(&pid); err != nil {
			t.Fatalf("scan pg_locks: %v", err)
		}
		n++
		pids = append(pids, pid)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pg_locks: %v", err)
	}
	if n > 0 {
		t.Logf("  daily-pipeline lock currently held by backend pid(s) %v", pids)
	}
	return n
}

func TestDailyPipelineLock_SecondAcquireIsBusy(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	ctx := context.Background()
	release, state, err := store.TryLockDailyPipeline(ctx)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if state != DailyPipelineLockAcquired {
		t.Fatalf("first acquire state = %v, want acquired", state)
	}
	if release == nil {
		t.Fatal("acquired must return a non-nil release func")
	}
	defer release()

	// 自检：先证明"锁确实在某个会话里"，否则下面那个探针的 false 无从解释。
	if n := advisoryLockHolders(t, store); n != 1 {
		t.Fatalf("while held: pg_locks shows %d holders, want exactly 1", n)
	}

	// 同一 pool 的**另一条连接**。会话级锁按会话划分，所以这条必须拿不到。
	// 若这里拿到，说明实现退化成了进程内状态，多实例保护是假的。
	if lockProbeOnFreshConn(t, store) {
		t.Fatal("a second session could take the lock while this one holds it; " +
			"the lock is not session-scoped, so it cannot protect multiple pocketd instances")
	}

	release2, state2, err2 := store.TryLockDailyPipeline(ctx)
	if err2 != nil {
		t.Fatalf("second acquire should not error, got %v", err2)
	}
	if state2 != DailyPipelineLockBusy {
		if release2 != nil {
			release2()
		}
		t.Fatalf("second acquire state = %v, want busy", state2)
	}
	if release2 != nil {
		t.Fatal("busy must not return a release func (nothing was locked by us)")
	}
}

func TestDailyPipelineLock_ReleaseMakesItReacquirable(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	ctx := context.Background()
	release, state, err := store.TryLockDailyPipeline(ctx)
	if err != nil || state != DailyPipelineLockAcquired {
		t.Fatalf("acquire: state=%v err=%v", state, err)
	}
	release()

	// 释放后必须能立刻再拿到——否则一轮跑完就把当天锁死了。
	release2, state2, err2 := store.TryLockDailyPipeline(ctx)
	if err2 != nil {
		t.Fatalf("re-acquire: %v", err2)
	}
	if state2 != DailyPipelineLockAcquired {
		t.Fatalf("re-acquire after release: state = %v, want acquired", state2)
	}
	release2()
}

// 这条是整组用例里最要紧的：advisory lock 是**会话级**的，连接带着它回到池子，
// 下一个借用者（可能是几小时后的另一轮流水线）会继承这把锁，于是每日流水线
// 被永久锁死，且没有任何错误日志指向原因。所以 release 失败时必须销毁连接。
//
// 判据用 pg_locks 而不是"再试一次 lock"：后者会被可重入打败（见上面的注释），
// 负控下实测恒绿 —— 这条用例是本轮返工过一次才真正有牙齿的。
func TestDailyPipelineLock_ReleaseDoesNotLeakIntoPool(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	ctx := context.Background()
	if n := advisoryLockHolders(t, store); n != 0 {
		t.Fatalf("test starts with %d pre-existing holders of the daily pipeline lock; "+
			"another test leaked it", n)
	}

	release, state, err := store.TryLockDailyPipeline(ctx)
	if err != nil || state != DailyPipelineLockAcquired {
		t.Fatalf("acquire: state=%v err=%v", state, err)
	}
	// 持锁期间必须恰好一个会话持有——先证明"看得见"，再证明"释放后看不见"。
	if n := advisoryLockHolders(t, store); n != 1 {
		t.Fatalf("while held: pg_locks shows %d holders, want exactly 1. "+
			"If this is 0 the pg_locks key match is wrong and the release "+
			"assertion below would be vacuously true.", n)
	}

	release()

	if n := advisoryLockHolders(t, store); n != 0 {
		t.Fatalf("after release: %d session(s) still hold the daily pipeline lock — "+
			"a connection carrying a session-level advisory lock was returned to the "+
			"pool, so the next borrower inherits it and the daily pipeline is "+
			"permanently deadlocked", n)
	}
}

// 跨 pool = 跨进程。真实场景是三个 pocketd 各有自己的连接池。
func TestDailyPipelineLock_IndependentPoolsAreMutuallyExclusive(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	// 从同一 DSN 起第二个池：不同物理连接 = 不同会话 = 另一个 pocketd。
	// schema 名从库里反查（不自己造一个），这样两个池必定落在同一 schema 上，
	// 模拟的正是"多个进程连同一个库"。
	dsn := testDSN()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	var schema string
	if err := store.pool.QueryRow(context.Background(),
		`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	if schema == "public" || schema == "" {
		t.Fatalf("refusing to run cross-pool lock test in schema %q", schema)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool2, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("second pool: %v", err)
	}
	defer pool2.Close()
	store2, err := NewStore(pool2)
	if err != nil {
		t.Fatalf("NewStore for second pool: %v", err)
	}

	release, state, err := store.TryLockDailyPipeline(context.Background())
	if err != nil || state != DailyPipelineLockAcquired {
		t.Fatalf("first pool acquire: state=%v err=%v", state, err)
	}
	defer release()

	release2, state2, err2 := store2.TryLockDailyPipeline(context.Background())
	if err2 != nil {
		t.Fatalf("second pool acquire should not error: %v", err2)
	}
	if state2 != DailyPipelineLockBusy {
		if release2 != nil {
			release2()
		}
		t.Fatalf("second pool state = %v, want busy — two pocketd instances sharing "+
			"one database would both run the daily pipeline", state2)
	}
	if release2 != nil {
		t.Fatal("busy must not return a release func")
	}
}

// "拿不到锁"有两种性质相反的成因，调用方必须能区分：
// Busy = 别人在跑（跳过）；Unavailable = 机制不可用（照跑）。
// 混为一谈的后果是一次数据库抖动就让每日流水线永久静默。
func TestDailyPipelineLock_NoPoolIsUnavailableNotBusy(t *testing.T) {
	var nilStore *Store
	release, state, err := nilStore.TryLockDailyPipeline(context.Background())
	if err != nil {
		t.Fatalf("nil store must not surface an error (it is not a failure), got %v", err)
	}
	if state != DailyPipelineLockUnavailable {
		t.Fatalf("nil store state = %v, want unavailable", state)
	}
	if release != nil {
		t.Fatal("unavailable must not return a release func")
	}
	empty := &Store{}
	release2, state2, err2 := empty.TryLockDailyPipeline(context.Background())
	if err2 != nil {
		t.Fatalf("empty store must not error, got %v", err2)
	}
	if state2 != DailyPipelineLockUnavailable {
		t.Fatalf("empty store state = %v, want unavailable", state2)
	}
	if release2 != nil {
		t.Fatal("unavailable must not return a release func")
	}
}

func TestDailyPipelineLock_StateStringIsDistinct(t *testing.T) {
	// 日志里要靠这三个词区分成因。三个都印成同一句话等于没区分。
	want := map[DailyPipelineLockState]string{
		DailyPipelineLockAcquired:    "acquired",
		DailyPipelineLockBusy:        "busy",
		DailyPipelineLockUnavailable: "unavailable",
	}
	seen := map[string]bool{}
	for state, s := range want {
		if state.String() != s {
			t.Errorf("state %d String() = %q, want %q", int(state), state.String(), s)
		}
		if seen[state.String()] {
			t.Errorf("duplicate state string %q — the log line can no longer tell "+
				"'another instance is running' from 'the lock mechanism is broken'", state.String())
		}
		seen[state.String()] = true
	}
}

// 判据自检：如果把上面任何一条的核心断言换成一个恒真的表达式，这组用例会绿。
// 这里保证 DSN 门控真的在工作、且 schema 隔离真的建立了。
func TestDailyPipelineLock_TestHarnessIsActuallyIsolated(t *testing.T) {
	if os.Getenv("POCKET_TEST_POSTGRES_DSN") == "" {
		t.Fatal("this file is meant to run against a real PostgreSQL; " +
			"set POCKET_TEST_POSTGRES_DSN (silently skipping would make every " +
			"assertion above vacuously true)")
	}
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	if store == nil || store.pool == nil {
		t.Fatal("newWorkspaceTestStore returned a store without a pool")
	}
	var schema string
	if err := store.pool.QueryRow(context.Background(),
		`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	if schema == "public" || schema == "" {
		t.Fatalf("test store is running in schema %q — the lock tests would then "+
			"advisory-lock the production schema", schema)
	}
}
