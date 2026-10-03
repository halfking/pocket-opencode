package scheduledtask

// diag_claimdue_race_test.go — 一次性诊断：多实例并发 ClaimDue 会不会拿到同一行。
//
// ## 为什么要查
//
// round28 给 email 每日定时流水线加了跨进程 advisory lock，并顺带发现
// `POCKET_SCHEDULER_ADVISORY_LOCK`（config.go:195/340）是**死配置**——全仓零
// 消费方。store.go:329-332 的注释也写着「callers should also wrap the
// scheduler tick in a single pg_advisory_lock」。据此我上一轮写下了
// 「scheduledtask 的 dispatcher 同样没有跨进程锁」。
//
// **但那是推断，不是实测。** ClaimDue 用的是 `FOR UPDATE SKIP LOCKED` +
// 立刻把 next_run_at 推到 $1 + GREATEST(300, timeout+60)。这本身可能就是
// 租约式的跨进程保护——两个实例即使同时 scan，也可能因为行已被第一个 UPDATE
// 掉出候选集而拿到不同行。SKIP LOCKED 的语义恰好就是「被别的事务锁住的行直接
// 跳过」，而 UPDATE...RETURNING 在同一语句里完成了 claim 与改期。
//
// 若实测证明它已经安全，那 round28 的说法必须更正：scheduledtask 不是
// 「缺锁」，而是**已经用租约做了跨进程保护，而那个 advisory lock 开关是
// 一个没人用的多余配置**。这两种定性对下一步（要不要给它加锁）完全不同。
//
// 门控：POCKET_DIAG_CLAIM_RACE=1

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagClaimDueRace(t *testing.T) {
	if os.Getenv("POCKET_DIAG_CLAIM_RACE") != "1" {
		t.Skip("set POCKET_DIAG_CLAIM_RACE=1 to run")
	}
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 隔离 schema，两个 pool 都钉在它上面 —— 模拟两个 pocketd 连同一个库。
	suffix := time.Now().UnixNano()
	schema := "claim_race_diag_" + itoa(suffix)
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("root pool: %v", err)
	}
	defer root.Close()
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = root.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	})

	mk := func() *pgxpool.Pool {
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			t.Fatalf("parse dsn: %v", err)
		}
		cfg.ConnConfig.RuntimeParams["search_path"] = schema
		p, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("pool: %v", err)
		}
		return p
	}
	poolA, poolB := mk(), mk()
	defer poolA.Close()
	defer poolB.Close()

	storeA, err := NewStore(ctx, poolA)
	if err != nil {
		t.Fatalf("NewStore A: %v", err)
	}
	storeB, err := NewStore(ctx, poolB)
	if err != nil {
		t.Fatalf("NewStore B: %v", err)
	}

	// 造 1 个此刻到期的任务：两个实例若都拿到，就是重复派发。
	if _, err := poolA.Exec(ctx, `
		INSERT INTO scheduled_tasks
		  (id, workspace_id, user_id, name, kind, schedule_kind, schedule_expr,
		   timezone, payload, enabled, next_run_at, lease_until, run_count,
		   max_runs, cooldown_sec, timeout_sec, created_at, updated_at)
		VALUES ('t-race-1','ws','u','race','noop','daily','09:00','+08:00',
		        '{}'::jsonb, TRUE, $1, 0, 0, 0, 0, 300, $1, $1)`, time.Now().Unix()); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	// 两个实例在同一瞬间 scan。
	//
	// 关键：必须留下"两个 ClaimDue 的执行窗口真的重叠"的证据。否则
	// "没有重复"可能只是因为它们恰好串行执行了（pool 懒建连接，第一个调用
	// 建连接的几毫秒里第二个还在建），那这个结论就是无效的——它证明的是
	// "串行时不重复"，不是"并发时不重复"。
	var wg sync.WaitGroup
	got := make([][]string, 2)
	errs := make([]error, 2)
	window := make([][2]int64, 2) // unix nano: [begin, end]
	start := make(chan struct{})
	for i, st := range []*Store{storeA, storeB} {
		wg.Add(1)
		go func(i int, st *Store) {
			defer wg.Done()
			<-start
			window[i][0] = time.Now().UnixNano()
			tasks, err := st.ClaimDue(ctx, time.Now().Unix(), 4)
			errs[i] = err
			window[i][1] = time.Now().UnixNano()
			for _, t := range tasks {
				got[i] = append(got[i], t.ID)
			}
		}(i, st)
	}
	close(start)
	wg.Wait()

	for i := range errs {
		if errs[i] != nil {
			t.Errorf("instance %d ClaimDue: %v", i, errs[i])
		}
	}
	t.Logf("instance A claimed: %v  window=%dus", got[0], (window[0][1]-window[0][0])/1000)
	t.Logf("instance B claimed: %v  window=%dus", got[1], (window[1][1]-window[1][0])/1000)

	// 两个执行窗口必须真的交叠，否则并发结论不成立。
	overlapped := window[0][0] < window[1][1] && window[1][0] < window[0][1]
	if !overlapped {
		t.Fatalf("the two ClaimDue calls did NOT overlap "+
			"(A=[%d,%d] B=[%d,%d]); this run proves nothing about concurrency",
			window[0][0], window[0][1], window[1][0], window[1][1])
	}
	t.Logf("the two ClaimDue windows DID overlap — concurrency was real")

	overlap := false
	for _, id := range got[0] {
		for _, id2 := range got[1] {
			if id == id2 {
				overlap = true
			}
		}
	}
	if overlap {
		t.Errorf("BOTH instances claimed the same task: A=%v B=%v — ClaimDue is NOT "+
			"cross-process safe, so a per-tick advisory lock is genuinely required",
			got[0], got[1])
	} else {
		t.Logf("VERDICT: no overlap — ClaimDue's SKIP LOCKED + immediate next_run_at " +
			"bump already serialises across processes. A per-tick advisory lock is " +
			"NOT required; POCKET_SCHEDULER_ADVISORY_LOCK is a redundant unused config.")
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
