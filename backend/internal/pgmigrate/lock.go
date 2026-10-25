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
func Acquire(ctx context.Context, pool *pgxpool.Pool, wait time.Duration) (*Lock, error) {
	if pool == nil {
		return nil, fmt.Errorf("pgmigrate: nil pool")
	}
	if wait <= 0 {
		wait = DefaultWait
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("pgmigrate: acquire connection: %w", err)
	}

	// The lock is scoped by the schema the pool is pinned to, so two different
	// schemas on the same database never block each other.
	var schema string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		conn.Release()
		return nil, fmt.Errorf("pgmigrate: read current_schema: %w", err)
	}
	if schema == "" {
		schema = "public"
	}
	key := "pocket:migrate:" + schema

	deadline := time.Now().Add(wait)
	for {
		var ok bool
		if err := conn.QueryRow(ctx,
			`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&ok); err != nil {
			conn.Release()
			return nil, fmt.Errorf("pgmigrate: try advisory lock %q: %w", key, err)
		}
		if ok {
			return &Lock{conn: conn, key: key}, nil
		}
		if time.Now().After(deadline) {
			conn.Release()
			return nil, fmt.Errorf("pgmigrate: schema %q is locked by another instance (waited %s)", schema, wait)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// Release drops the advisory lock and returns the connection to the pool.
// Calling it more than once is a no-op.
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
			log.Printf("[pgmigrate] release advisory lock %q failed: %v", l.key, err)
		}
		l.conn.Release()
		l.conn = nil
	})
}
