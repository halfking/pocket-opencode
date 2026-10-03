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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// dailyPipelineLockTestSchemaPrefix 必须与 store_workspace_test.go 里 helper
// 现场生成的 schema 前缀一致（`"email_ws_test_" + hex`）。
//
// 【为什么从"不是 public"收紧到"必须带这个前缀"】原来的判据只有
// `schema != "public" && schema != ""`。在本仓库这有个**具体盲区**：
// 生产 schema 叫 `opencode_pocket`，而 `public` 恰恰是那个空的诱饵 schema。
// 于是最该被抓住的那种退化——helper 不再建自己的 schema、search_path 直接
// 落在 DSN 自带的**生产** schema 上——会让 `current_schema()` 返回
// `opencode_pocket`，旧判据放行，测试对着生产库加 advisory lock 并报绿。
// 要求前缀等于要求"这个 schema 是本次测试自己建出来的"，与
// pg_test_isolation_guard_test.go 里对本文件的登记理由（"确实隔离"）一致。
const dailyPipelineLockTestSchemaPrefix = "email_ws_test_"

// dailyPipelineLockSchemaIsIsolated 判定一个 schema 名是否满足隔离要求。
//
// 抽成函数而不是内联进 t.Fatalf 的条件，有两个理由：
//  1. 这条判据自己需要一个**不需要数据库**的用例钉住。内联在需要真库的
//     断言里，它就只在有库时才会被执行到——而恰恰是没有库的那些环境
//     最需要有人告诉你判据长什么样。
//  2. 判据一旦是能被人一眼读完的表达式，改它的人看得见自己在改什么。
func dailyPipelineLockSchemaIsIsolated(schema string) bool {
	if schema == "" || schema == "public" {
		return false
	}
	return strings.HasPrefix(schema, dailyPipelineLockTestSchemaPrefix)
}

// 判据自检（需要真库）：如果把上面任何一条的核心断言换成一个恒真的表达式，
// 这组用例会绿。这里证明夹具确实落在**本次测试自建的** schema 上。
func TestDailyPipelineLock_TestHarnessIsActuallyIsolated(t *testing.T) {
	// 无 DSN 时**跳过**，不是失败。
	//
	// 【2026-10-03 本轮更正】这一版原先在这里 t.Fatal，理由写在 round28
	// handoff 里：「静默 skip 会让同文件其余断言全部变成恒真」。那个担心是真的，
	// 但这个补救是错的，代价比它防住的东西大得多：
	//   · store_workspace_test.go 的包约定明写「否则 skip，好让没有数据库的机器
	//     上 go test ./... 保持绿」。这一条测试单方面推翻了它；
	//   · 实际后果是**任何**没有测试库的机器（含 CI、含任何新克隆、含任何
	//     没配 POCKET_TEST_POSTGRES_DSN 的同事）上 `go test ./...` 恒红。
	//     一个恒红的门槛会被整体忽略，那时被牺牲的不只是这一个文件，而是整套
	//     测试的红绿语义——这正是「用假红换假绿」的典型赔率；
	//   · 更关键的是它在无 DSN 时**提供的保护是零**：此时本文件其余用例
	//     同样 skip，没有任何断言会变恒真，恒红只是噪声。它唯一"生效"的
	//     配置，恰恰是它本来要保护的那批断言真正在跑的配置——而在那里它本来就绿。
	//
	// 「不许静默变恒真」这件事改由两个**不需要数据库**的用例承担：
	// TestDailyPipelineLock_SchemaIsolationPredicate（钉住判据本身）与
	// TestDailyPipelineLock_DBBackedTestsStayWired（钉住那 5 条用例仍然接着
	// 真 store、没有被偷偷改成 skip）。那两个在所有环境都执行，覆盖面
	// 严格大于原来这一条。
	if testDSN() == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; the advisory-lock assertions " +
			"skip along with the rest of this file. Anti-vacuity is covered by " +
			"the DB-free tests in this same file, which run in every environment.")
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
	if !dailyPipelineLockSchemaIsIsolated(schema) {
		t.Fatalf("test store is running in schema %q, which is not a schema this "+
			"test run created (want prefix %q) — the lock tests would then "+
			"advisory-lock whatever schema the DSN happens to point at, i.e. "+
			"possibly the production one", schema, dailyPipelineLockTestSchemaPrefix)
	}
}

// TestDailyPipelineLock_SchemaIsolationPredicate —— 隔离判据的判据自检。
//
// **不需要数据库**，所以它在每一台机器上都跑，包括没有测试库的那些。
// 这正是它取代原自检的理由：原自检只在有库时才有意义，而"有库"是最容易
// 缺的那台机器配置。
//
// 负控：把 dailyPipelineLockSchemaIsIsolated 改回"只排除 public/空"，
// 本用例在 opencode_pocket 一行转红——而那正是生产 schema 名。
func TestDailyPipelineLock_SchemaIsolationPredicate(t *testing.T) {
	cases := []struct {
		schema string
		want   bool
		why    string
	}{
		{"public", false, "public 是本仓库的空诱饵 schema；判它隔离等于放行最常见的退化"},
		{"", false, "空 schema 说明 current_schema() 根本没读出来，隔离无从谈起"},
		{"opencode_pocket", false,
			"本仓库的生产 schema 名。旧判据（只排除 public/空）会放行它——" +
				"helper 一旦退化成不隔离、search_path 落在 DSN 自带的库上，" +
				"就会对着生产库加 advisory lock 并报绿"},
		{"email_ws_test_0a1b2c3d4e5f", true,
			"helper 现场生成的隔离 schema（store_workspace_test.go 的命名）"},
		{"email_ws_test_", true, "前缀判定只看前缀，不校验后缀形状"},
		{"public_email_ws_test_1", false,
			"前缀必须出现在开头；这是防「名字里含有测试字样就算隔离」的形式判定"},
		{"my_email_ws_test_1", false, "同上，锚定在前缀而不是子串"},
	}
	for _, c := range cases {
		if got := dailyPipelineLockSchemaIsIsolated(c.schema); got != c.want {
			t.Errorf("dailyPipelineLockSchemaIsIsolated(%q) = %v, want %v — %s",
				c.schema, got, c.want, c.why)
		}
	}
}

// dbBackedLockTests 是必须经由 newWorkspaceTestStore 拿到**真** store 的用例。
// 逐条列出来而不是统计数量：数量对得上但换了几条，护栏就失效了。
var dbBackedLockTests = []string{
	"SecondAcquireIsBusy",
	"ReleaseMakesItReacquirable",
	"ReleaseDoesNotLeakIntoPool",
	"IndependentPoolsAreMutuallyExclusive",
	"TestHarnessIsActuallyIsolated",
}

var lockTestFuncRe = regexp.MustCompile(`(?m)^func (TestDailyPipelineLock_\w+)\(t \*testing\.T\) \{`)

// ifLineRe 匹配一个 if 语句的**开头**（行首缩进 + if），用来回溯某个 t.Skip
// 由哪条 if 守着。
var ifLineRe = regexp.MustCompile(`(?m)^[\t ]+if\b[^\n]*$`)

// skipAt 返回 body 里第一个 t.Skip 的下标（没有则 -1）。
func skipAt(body string, from int) int {
	i := strings.Index(body[from:], "t.Skip(")
	if i < 0 {
		return -1
	}
	return from + i
}

// skipIsDSNGated 判断 idx 处的 t.Skip 是否被一条「DSN 为空」的 if 守着。
//
// 【为什么不能简单地"出现 t.Skip 就判红"】第一版就是那么写的，结果它把本文件
// 里**合法**的那条 skip 也判红了——TestHarnessIsActuallyIsolated 无 DSN 时
// 跳过，正是本轮要恢复的包约定。护栏把自己要保护的东西判成缺陷，第二次就会被
// 人加白名单绕过去，那它比没有更糟。
//
// 判据改成"这个 skip 有没有挂在一条判空的 if 上"：
//   · if testDSN() == "" { t.Skip(...) }      → 放行（无库跳过，有库必跑）
//   · t.Skip(...) 出现在任何 if 之前            → 判红（任何环境都不跑）
//
// 已知残留的宽松：一条与 DSN 无关但恰好形如 `if x == ""` 的 if 也能放行。
// 这里不追求完备——判据过宽的代价（放过退化）远小于过窄（逼人加白名单）。
// 真正兜底的是同文件那 5 条用例仍然必须调用 newWorkspaceTestStore(t)：
// 就算 skip 判据被绕过，接线那一关还在。
func skipIsDSNGated(body string, idx int) (bool, string) {
	head := body[:idx]
	loc := ifLineRe.FindAllStringIndex(head, -1)
	if len(loc) == 0 {
		return false, "it is the first statement of the function body"
	}
	last := head[loc[len(loc)-1][0]:loc[len(loc)-1][1]]
	if !strings.Contains(last, `== ""`) {
		return false, fmt.Sprintf("the nearest enclosing if is %q, which does not test for an empty DSN",
			strings.TrimSpace(last))
	}
	return true, ""
}

// TestDailyPipelineLock_DBBackedTestsStayWired —— 「不许静默变恒真」的正面判据。
//
// **不需要数据库。** 它回答的是原来那条自检真正想问、却在无库时完全没回答的问题：
// 那 5 条需要真库的用例，是不是还接着那个会自建隔离 schema 的夹具？
//
// 两种退化都会让它转红：
//   · 有人把某条用例的 newWorkspaceTestStore 换成自己 new 一个裸 pool（不隔离）；
//   · 有人在用例开头加一句 t.Skip，于是它在任何环境下都不再断言任何东西。
func TestDailyPipelineLock_DBBackedTestsStayWired(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(".", "pipeline_lock_test.go"))
	if err != nil {
		t.Fatalf("read own source: %v", err)
	}
	text := string(src)

	// 把每个用例的函数体切出来：从 func 行到下一个顶层 func 行（或文件尾）。
	bodies := map[string]string{}
	locs := lockTestFuncRe.FindAllStringSubmatchIndex(text, -1)
	if len(locs) == 0 {
		t.Fatal("no TestDailyPipelineLock_* functions found in this file — " +
			"the regexp is wrong, not the tests")
	}
	for i, m := range locs {
		name := text[m[2]:m[3]]
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		bodies[name] = text[m[1]:end]
	}

	for _, short := range dbBackedLockTests {
		name := "TestDailyPipelineLock_" + short
		body, ok := bodies[name]
		if !ok {
			t.Errorf("%s is gone. Either drop it from dbBackedLockTests with a "+
				"reason, or restore it: it is the only thing standing between a "+
				"refactor and five silently-vacuous assertions", name)
			continue
		}
		if !strings.Contains(body, "newWorkspaceTestStore(t)") {
			t.Errorf("%s no longer goes through newWorkspaceTestStore — it must "+
				"run against the isolated schema the helper creates, not a bare pool", name)
		}
		if idx := skipAt(body, 0); idx >= 0 {
			if guarded, why := skipIsDSNGated(body, idx); !guarded {
				t.Errorf("%s has a t.Skip that is not gated on an empty DSN (%s). "+
					"An advisory-lock assertion that skips in every environment is "+
					"indistinguishable from one that passes", name, why)
			}
		}
	}

	// 夹具本身仍必须由测试专用 DSN 门控。
	ws, err := os.ReadFile(filepath.Join(".", "store_workspace_test.go"))
	if err != nil {
		t.Fatalf("read helper source: %v", err)
	}
	wsText := string(ws)
	if !strings.Contains(wsText, `os.Getenv("POCKET_TEST_POSTGRES_DSN")`) {
		t.Error("testDSN() no longer reads POCKET_TEST_POSTGRES_DSN — the lock " +
			"tests would start hitting whatever database the machine happens to have")
	}
	// 「不得回退读生产 DSN」这条不变量**故意不在这里断言**：要断言它就得在
	// 源码里写出那个被禁的字面量，而仓库级护栏的规则 1 是源码级 grep，会把
	// 这条断言本身当成「测试读了生产 DSN」。它已挪到
	// internal/server/pg_test_isolation_guard_test.go 的
	// TestEmailWorkspaceHelperNeverFallsBackToProductionDSN —— 那是护栏自己
	// 的文件（豁免自身），且仓库级不变量本就该由仓库级护栏钉。
	if !strings.Contains(wsText, dailyPipelineLockTestSchemaPrefix) {
		t.Errorf("the helper no longer builds schemas named %q*, but the "+
			"isolation predicate in this file requires that prefix. 两侧必须一起改，"+
			"否则自检会因为错误的原因转红",
			dailyPipelineLockTestSchemaPrefix)
	}
}
