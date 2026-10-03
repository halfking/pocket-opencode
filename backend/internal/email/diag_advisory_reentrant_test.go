package email

// diag_advisory_reentrant_test.go — 一次性诊断：确认 PG 会话级 advisory lock 的
// 可重入语义，并找出「这把锁此刻被谁持有」的可查询判据。
//
// 为什么需要它：pipeline_lock_test.go 的第一版探针用
// `pg_try_advisory_lock` 直接问「锁还在不在」，负控下（release 跳过 unlock
// 直接把连接归还池子）本该转红，实测**全绿**。假设是 PG 会话级锁可重入：
// 探针从池里拿回**同一条**连接，lock() 又成功，于是「有泄漏」被判成「干净」。
//
// 门控：POCKET_DIAG_ADVISORY_REENTRANT=1

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestDiagAdvisoryReentrant(t *testing.T) {
	if os.Getenv("POCKET_DIAG_ADVISORY_REENTRANT") != "1" {
		t.Skip("set POCKET_DIAG_ADVISORY_REENTRANT=1 to run")
	}
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// ---- 1. 同一会话连续 lock 两次 ----
	conn, err := store.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	connPID := conn.Conn().PgConn().PID()
	var first, second, unlockOnce, unlockTwice bool
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1,0))`, DailyPipelineLockKey).Scan(&first); err != nil {
		t.Fatalf("lock1: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1,0))`, DailyPipelineLockKey).Scan(&second); err != nil {
		t.Fatalf("lock2: %v", err)
	}
	t.Logf("SAME SESSION: first=%v second=%v  (second==true => REENTRANT,朴素探针恒真)",
		first, second)

	// unlock 一次后是否还持锁 —— 这才是可重入的决定性证据
	if err := conn.QueryRow(ctx,
		`SELECT pg_advisory_unlock(hashtextextended($1,0))`, DailyPipelineLockKey).Scan(&unlockOnce); err != nil {
		t.Fatalf("unlock1: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1,0))`, DailyPipelineLockKey).Scan(&unlockTwice); err != nil {
		t.Fatalf("lock3: %v", err)
	}
	t.Logf("after ONE unlock, re-lock=%v  (true => 计数仍>0，可重入坐实)", unlockTwice)
	conn.Release()

	// ---- 2. 别的会话拿不到 ----
	probe, err := store.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire probe: %v", err)
	}
	var other bool
	if err := probe.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1,0))`, DailyPipelineLockKey).Scan(&other); err != nil {
		t.Fatalf("probe: %v", err)
	}
	t.Logf("OTHER SESSION while conn still holds: got=%v (want false)", other)
	probe.Release()

	// ---- 3. pg_locks 能不能穿透可重入地回答「现在谁持锁」 ----
	type row struct {
		Locktype string
		Classid  int64
		Objid    int64
		Objsubid int32
		Granted  bool
		Mode     string
		Pid      int32
		Count    int64
	}
	rows, err := store.pool.Query(ctx, `
		SELECT locktype, classid::bigint, objid::bigint, objsubid, granted, mode, pid
		  FROM pg_locks
		 WHERE locktype = 'advisory'
		   AND classid = ((hashtextextended($1,0) >> 32) & 4294967295)
		   AND objid   =  (hashtextextended($1,0) & 4294967295)
		 ORDER BY pid`, DailyPipelineLockKey)
	if err != nil {
		t.Fatalf("pg_locks: %v", err)
	}
	n := 0
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Locktype, &r.Classid, &r.Objid, &r.Objsubid, &r.Granted, &r.Mode, &r.Pid); err != nil {
			t.Fatalf("scan: %v", err)
		}
		n++
		t.Logf("  pg_locks row: type=%s classid=%d objid=%d objsubid=%d granted=%v mode=%s pid=%d",
			r.Locktype, r.Classid, r.Objid, r.Objsubid, r.Granted, r.Mode, r.Pid)
	}
	rows.Close()
	t.Logf("pg_locks matching rows = %d (1 => 锁确实泄漏在某个会话里；0 => 干净)", n)
	t.Logf("holding backend pid = %d", connPID)
}
