// Package pgmigrate provides a cross-process serialization primitive for
// PostgreSQL schema migrations.
//
// # Why this exists
//
// Every pocketd store runs its own idempotent DDL batch at boot
// (CREATE TABLE IF NOT EXISTS / ALTER TABLE ... ADD COLUMN IF NOT EXISTS /
// CREATE INDEX IF NOT EXISTS). Those batches are sent as **one multi-statement
// simple query**, which PostgreSQL executes inside a **single implicit
// transaction** — so every lock the batch takes (ACCESS EXCLUSIVE on each
// touched table) is held until the whole batch commits.
//
// Two pocketd instances that boot at the same time against the **same schema**
// therefore interleave like this (measured on 2026-10-25, 18097 + 18099 both
// starting against schema `opencode_pocket`):
//
//	instance A: ALTER TABLE tasks ...            → holds ACCESS EXCLUSIVE on `tasks`
//	instance B: ALTER TABLE task_run_bindings... → holds ACCESS EXCLUSIVE on `task_run_bindings`
//	instance A: wants ACCESS EXCLUSIVE on `task_run_bindings` → waits for B
//	instance B: wants ACCESS EXCLUSIVE on `tasks`              → waits for A
//	⇒ PostgreSQL aborts one of them: ERROR: deadlock detected (SQLSTATE 40P01)
//
// The victim dies during startup (`task migrate: ERROR: deadlock detected`),
// so the instance never comes up — and the symptom ("one instance silently
// missing") points nowhere near the real cause.
//
// # What this package does
//
// Acquire takes a **session-level advisory lock keyed by the schema** on a
// dedicated connection. The lock is held for the whole migration phase, so
// only one pocketd instance runs DDL against a given schema at a time; the
// others wait and then run their (idempotent) DDL afterwards.
//
// The lock is session-level on purpose: if the process dies, PostgreSQL drops
// the lock with the session — there is no stale lock to clean up.
package pgmigrate

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultWait bounds how long Acquire waits for a peer instance to finish its
// migration phase. Migrations are idempotent DDL and finish in well under a
// second; the budget only exists so a stuck peer cannot block boot forever.
const DefaultWait = 60 * time.Second

// Lock is a held advisory lock. Release is idempotent.
type Lock struct {
	conn *pgxpool.Conn
	key  string
	once sync.Once
}

// Acquire takes the schema-scoped migration lock.
//
// It returns an error when the lock cannot be taken within wait; the caller
// decides whether that is fatal. On success the returned Lock must be
// released (Release is idempotent and safe to call from a defer).
//
// # 为什么等待期**不占**池容量（2026-10-28 §976）
//
// 旧实现在整个等待期一直攥着从池里借来的那条连接，直到拿到锁才归还。
// 后果是**池容量被等待者吃掉**：
//
//	实例 A（拿到锁的那一方）：占着 1 条连接跑迁移批次
//	实例 B/C/D（等待的三方）：各占着 1 条连接空转等锁
//	⇒ 池被占满 ⇒ A 跑 DDL 时**再也借不到第二条连接**
//	⇒ A 的 DDL 永远等不到连接 ⇒ 释放锁的 defer 永远不执行
//	⇒ B/C/D 的等待预算耗尽后退出 ⇒ 谁都不放锁 ⇒ **真死锁**
//
// 这个死锁在 2026-10-28 的 CI 上第一次现身：GitHub runner 是 2 核 ⇒
// pgxpool 的默认上限是 max(4, NumCPU) = 4 ⇒ 4 个并发实例刚好把池占满；
// 而 16 核的开发机上上限是 16 ⇒ 同一份代码本地恒绿、CI 恒红。
// ⇒ 「会不会死锁」取决于**跑在哪台机器上**，那是量具依赖环境属性，不是被测行为变了。
//
// 修法：**拿不到锁就把连接还回池子**，等待期不占容量。
// 于是等待期最多短暂占 1 条（做 try 查询的那一瞬），
// 持锁方 1 条 + 跑 DDL 1 条 ⇒ 池只要有 2 条就够，与实例数、CPU 数**都无关**。
func Acquire(ctx context.Context, pool *pgxpool.Pool, wait time.Duration) (*Lock, error) {
	if pool == nil {
		return nil, fmt.Errorf("pgmigrate: nil pool")
	}
	if wait <= 0 {
		wait = DefaultWait
	}
	deadline := time.Now().Add(wait)

	// schema 只在第一次尝试时读一次，之后**不再重读**：
	// 锁的 key 必须全进程一致，否则两个实例可能算出两把不同的锁 ⇒ 互斥静默失效。
	var (
		schema string
		key    string
	)
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("pgmigrate: acquire cancelled: %w", err)
		}
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return nil, fmt.Errorf("pgmigrate: acquire connection: %w", err)
		}

		if schema == "" {
			// The lock is scoped by the schema the pool is pinned to, so two
			// different schemas on the same database never block each other.
			if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
				conn.Release()
				return nil, fmt.Errorf("pgmigrate: read current_schema: %w", err)
			}
			if schema == "" {
				schema = "public"
			}
			key = "pocket:migrate:" + schema
		}

		var ok bool
		if err := conn.QueryRow(ctx,
			`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&ok); err != nil {
			conn.Release()
			return nil, fmt.Errorf("pgmigrate: try advisory lock %q: %w", key, err)
		}
		if ok {
			return &Lock{conn: conn, key: key}, nil
		}

		// ★ 拿不到锁 ⇒ 把连接**还回池子**（见函数注释：等待期不占池容量）。
		//   会话级 advisory lock 只有在**拿到**时才挂在这条连接上，
		//   没拿到就归还 ⇒ 池里不会躺着一条「带着锁却没人知道」的连接。
		conn.Release()

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("pgmigrate: schema %q is locked by another instance (waited %s)", schema, wait)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("pgmigrate: acquire cancelled: %w", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Release drops the advisory lock and returns the connection to the pool.
// Calling it more than once is a no-op.
//
// ⚠ 解锁失败时**不能**把连接放回池子（2026-10-28 §976）：
//
//	会话级 advisory lock 挂在**连接**上。解锁失败 ⇒ 这条连接上**还挂着那把锁**。
//	若把它放回池里，下一个拿到它的业务查询会带着一把「没人知道谁持有的」锁
//	⇒ 同 schema 的别的实例再也拿不到锁（等满 DefaultWait 后放弃）
//	⇒ 迁移互斥**静默失效**（不是报错，是不互斥了）。
//	⇒ 正确处置：把连接从池里**摘掉**（关闭会话 ⇒ PG 自动释放会话级锁）。
func (l *Lock) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.conn == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := l.conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, l.key); err != nil {
			log.Printf("[pgmigrate] release advisory lock %q failed: %v; dropping the connection so the session-level lock dies with it", l.key, err)
			// Hijack 把连接从池里摘走并关闭 ⇒ PG 随会话结束释放这把锁。
			// 形态与 internal/email/pipeline_lock.go 的 releaseDailyPipelineLock 一致
			// （那边也是「解锁失败就销毁连接」），不是裸丢弃返回值。
			raw := l.conn.Hijack()
			_ = raw.Close(context.Background())
			l.conn = nil
			return
		}
		l.conn.Release()
		l.conn = nil
	})
}
