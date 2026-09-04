package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestPGAuditStore creates an isolated schema for a server audit integration
// test. POCKET_TEST_POSTGRES_DSN takes precedence; POCKET_POSTGRES_DSN is a
// compatible fallback so all PG test helpers follow the same convention.
func newTestPGAuditStore(t *testing.T) (*redclaw.PGAuditStore, func()) {
	t.Helper()
	dsn := ""
	for _, key := range []string{"POCKET_TEST_POSTGRES_DSN", "POCKET_POSTGRES_DSN"} {
		if value := os.Getenv(key); value != "" {
			dsn = value
			break
		}
	}
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN or POCKET_POSTGRES_DSN not set; skipping server audit PG integration test")
	}

	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		rootPool.Close()
		t.Fatalf("rand: %v", err)
	}
	schema := "server_audit_test_" + hex.EncodeToString(suffix)
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}

	scopedCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	scopedCfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	scopedPool, err := pgxpool.NewWithConfig(ctx, scopedCfg)
	if err != nil {
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
		t.Fatalf("scoped pool: %v", err)
	}

	cleanup := func() {
		scopedPool.Close()
		_, _ = rootPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
	}
	store, err := redclaw.NewPGAuditStore(scopedPool)
	if err != nil {
		cleanup()
		t.Fatalf("NewPGAuditStore: %v", err)
	}
	var schemaTableExists bool
	if err := rootPool.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE n.nspname = $1 AND c.relname = 'audit_entries' AND c.relkind = 'r'
)`, schema).Scan(&schemaTableExists); err != nil {
		cleanup()
		t.Fatalf("check isolated audit table: %v", err)
	}
	if !schemaTableExists {
		cleanup()
		t.Fatalf("audit_entries was not created in isolated schema %q", schema)
	}
	return store, cleanup
}

func requirePGAuditEntries(t *testing.T, store *redclaw.PGAuditStore, query redclaw.AuditQuery) []*redclaw.AuditEntry {
	t.Helper()
	entries, err := store.Query(query)
	if err != nil {
		t.Fatalf("Query(%+v): %v", query, err)
	}
	return entries
}

func requireSinglePGAuditEntry(t *testing.T, store *redclaw.PGAuditStore, query redclaw.AuditQuery) *redclaw.AuditEntry {
	t.Helper()
	entries := requirePGAuditEntries(t, store, query)
	if len(entries) != 1 {
		t.Fatalf("Query(%+v) returned %d entries, want 1: %+v", query, len(entries), entries)
	}
	return entries[0]
}
