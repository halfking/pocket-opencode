package quota

// pg_store_test.go — integration tests for PG-backed budget store.
//
// Mirrors lobster/store_test.go: isolated schema per test, skipped when
// POCKET_TEST_POSTGRES_DSN is unset so `go test ./...` stays green in CI
// environments without PG.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testDSN() string {
	for _, k := range []string{"POCKET_TEST_POSTGRES_DSN", "POCKET_POSTGRES_DSN"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func newTestPGStore(t *testing.T) (*PGStore, func()) {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping quota PG integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	// Isolated schema so concurrent test runs don't collide.
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("rand: %v", err)
	}
	schema := "quota_test_" + hex.EncodeToString(suffix)
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_, _ = rootPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	scopedPool, err := pgxpool.NewWithConfig(ctx, cfg)
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
	store, err := NewPGStore(scopedPool)
	if err != nil {
		cleanup()
		t.Fatalf("NewPGStore: %v", err)
	}
	return store, cleanup
}

func TestPGStore_BudgetsFor_FiltersByPeriod(t *testing.T) {
	s, cleanup := newTestPGStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()

	mustSetPG(t, s, Budget{WorkspaceID: "ws-a", Kind: "cost_usd", Limit: 100, PeriodStart: now.Add(-time.Hour), PeriodEnd: now.Add(time.Hour)})
	mustSetPG(t, s, Budget{WorkspaceID: "ws-a", Kind: "tokens", Limit: 1000, PeriodStart: now.Add(time.Hour), PeriodEnd: now.Add(2 * time.Hour)})

	got, err := s.BudgetsFor(ctx, "ws-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 budget in window, got %d", len(got))
	}
	if got[0].Kind != "cost_usd" {
		t.Fatalf("expected cost_usd budget, got %q", got[0].Kind)
	}
}

func TestPGStore_BudgetsFor_AcceptsZeroPeriod(t *testing.T) {
	s, cleanup := newTestPGStore(t)
	defer cleanup()
	ctx := context.Background()

	mustSetPG(t, s, Budget{WorkspaceID: "ws-a", Kind: "tokens", Limit: 1000})

	got, _ := s.BudgetsFor(ctx, "ws-a", time.Now())
	if len(got) != 1 {
		t.Fatalf("zero-period budget must always apply, got %d", len(got))
	}
}

func TestPGStore_RejectsEmptyWorkspace(t *testing.T) {
	s, cleanup := newTestPGStore(t)
	defer cleanup()

	if err := s.Set(context.Background(), Budget{Kind: "tokens"}); err == nil {
		t.Fatal("expected error for empty workspace_id")
	}
}

func TestPGStore_NilPoolErrors(t *testing.T) {
	if _, err := NewPGStore(nil); err == nil {
		t.Fatal("NewPGStore(nil) must error")
	}
}

func mustSetPG(t *testing.T, s *PGStore, b Budget) {
	t.Helper()
	if err := s.Set(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}
