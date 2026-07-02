// Package db provides a shared PostgreSQL connection pool for all pocketd
// modules. Phase 0 migrated the backend from per-module SQLite files to a
// single Postgres instance (shared with kxmemory), unifying the data layer.
//
// Stores receive the *pgxpool.Pool from main.go rather than opening their
// own connections. Each store owns its own migration (CREATE TABLE IF NOT
// EXISTS), run on construction — same pattern as the old SQLite stores.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// New opens a connection pool against the given DSN and pings it.
// The pool is sized for a single-user assistant workload; tune via the DSN
// params (pool_max_conns etc.) if needed.
func New(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, fmt.Errorf("postgres DSN is empty (set POCKET_POSTGRES_DSN)")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres DSN: %w", err)
	}
	// Sensible defaults for the assistant workload.
	if cfg.MaxConns == 0 {
		cfg.MaxConns = 10
	}
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return pool, nil
}
