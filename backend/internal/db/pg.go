// Package db provides a shared PostgreSQL connection pool for all pocketd
// modules. Phase 0 migrated the backend from per-module SQLite files to a
// single Postgres instance (shared with kxmemory), unifying the data layer.
//
// Stores receive the *pgxpool.Pool from main.go rather than opening their
// own connections. Each store owns its own migration (CREATE TABLE IF NOT
// EXISTS), run on construction — same pattern as the old SQLite stores.
//
// Schema isolation: when `schema` is non-empty, New creates the schema
// (CREATE SCHEMA IF NOT EXISTS) and pins every connection from the pool to
// that schema via `search_path`. Unqualified table names then resolve ONLY
// against that schema, so pocketd's migrations cannot collide with tables
// owned by other modules sharing the same database (e.g. `public.tasks`
// from the existing project-management stack on llm-gateway-pg's kaixuan DB).
package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// New opens a connection pool against the given DSN, creates `schema` if it
// does not exist, pins the pool's search_path to that schema, and pings it.
// `schema == ""` skips schema isolation (legacy public-only deployments).
func New(ctx context.Context, dsn, schema string) (*pgxpool.Pool, error) {
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

	if schema != "" {
		if !isValidIdent(schema) {
			return nil, fmt.Errorf("invalid schema name %q (must match [A-Za-z_][A-Za-z0-9_]*)", schema)
		}
		// Pin search_path per-connection via AfterConnect. RuntimeParams alone
		// can be overridden by client SET statements; AfterConnect runs once
		// per new connection before it's handed back to the pool. We do NOT
		// include `public` — shared PG instances may have `public.tasks` and
		// similar from other modules; mixing would cause FK conflicts.
		quotedSchema := quoteIdent(schema)
		cfg.ConnConfig.RuntimeParams["search_path"] = quotedSchema
		schemaCopy := quotedSchema
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s;", schemaCopy))
			return err
		}
	}

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

	if schema != "" {
		// CREATE SCHEMA must run on a connection whose search_path is NOT
		// pinned to that schema (would be self-referential). Use a one-shot
		// admin connection with default search_path.
		if err := createSchemaOnce(ctx, pool, schema); err != nil {
			pool.Close()
			return nil, fmt.Errorf("create schema %q: %w", schema, err)
		}
	}

	return pool, nil
}

// createSchemaOnce creates `schema` via a single connection. The connection's
// AfterConnect hook has already pinned search_path to `schema`, but CREATE
// SCHEMA cannot self-reference, so we temporarily reset to pg_catalog,public,
// create, then restore.
func createSchemaOnce(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET search_path TO pg_catalog, public;"); err != nil {
		return fmt.Errorf("reset search_path: %w", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", quoteIdent(schema))); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	// Restore pinned search_path so subsequent uses of this pooled conn still
	// resolve unqualified names against our schema.
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s;", quoteIdent(schema))); err != nil {
		return fmt.Errorf("restore search_path: %w", err)
	}
	return nil
}

// quoteIdent returns a safely quoted PostgreSQL identifier. Validation still
// rejects malformed names before this helper is called.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// isValidIdent guards against schema names that could break out of the quoted
// identifier (e.g. `foo; DROP TABLE users`). pgx uses identifier quoting
// internally but defense-in-depth here is cheap.
func isValidIdent(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	forbidden := map[string]struct{}{
		"public":             {},
		"pg_catalog":         {},
		"information_schema": {},
		"pg_toast":           {},
	}
	if _, ok := forbidden[strings.ToLower(s)]; ok {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
