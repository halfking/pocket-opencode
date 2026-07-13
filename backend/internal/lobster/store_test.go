package lobster

// store_test.go — integration tests for the Lobster Vault sync store.
//
// Same PG-backed pattern as identity/store_test.go: isolated schema per test,
// skipped when POCKET_TEST_POSTGRES_DSN is unset so `go test ./...` stays green.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

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

func newTestStore(t *testing.T) (*SyncStore, func()) {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping lobster integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "lobster_test_" + hex.EncodeToString(b)
	if _, err := rootPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
		t.Fatalf("test pool: %v", err)
	}
	store, err := NewSyncStore(pool)
	if err != nil {
		pool.Close()
		rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
		t.Fatalf("NewSyncStore: %v", err)
	}
	return store, func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
	}
}

func TestPushAndPull(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Push one asset.
	m := &AssetMirror{
		ID: "ast_1", WorkspaceID: "ws1", Kind: "note",
		ClientRev: 1, CipherBlob: "ENCRYPTED_BLOB_1", UpdatedAt: 1000,
	}
	res, err := s.Push(ctx, m)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res.ServerRev != 1 {
		t.Errorf("server_rev = %d, want 1", res.ServerRev)
	}
	if res.Conflict {
		t.Error("first push should not conflict")
	}

	// Pull since 0 should return the pushed asset.
	pulled, err := s.Pull(ctx, "ws1", 0, 10)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(pulled) != 1 || pulled[0].ID != "ast_1" {
		t.Errorf("pulled = %+v", pulled)
	}

	// Pull since 1 should return nothing.
	pulled, _ = s.Pull(ctx, "ws1", 1, 10)
	if len(pulled) != 0 {
		t.Errorf("pull since=1 should be empty, got %d", len(pulled))
	}

	// LatestServerRev should be 1.
	rev, _ := s.LatestServerRev(ctx, "ws1")
	if rev != 1 {
		t.Errorf("latest = %d, want 1", rev)
	}
}

func TestPushConflict_OlderClientRev(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Device A pushes rev 1.
	_, _ = s.Push(ctx, &AssetMirror{
		ID: "ast_c", WorkspaceID: "ws", Kind: "note", ClientRev: 1, CipherBlob: "A_v1", UpdatedAt: 1,
	})
	// Device B pushes rev 5 (newer).
	_, _ = s.Push(ctx, &AssetMirror{
		ID: "ast_c", WorkspaceID: "ws", Kind: "note", ClientRev: 5, CipherBlob: "B_v5", UpdatedAt: 2,
	})
	// Device A now pushes rev 2 (stale — B already advanced to rev 5).
	res, err := s.Push(ctx, &AssetMirror{
		ID: "ast_c", WorkspaceID: "ws", Kind: "note", ClientRev: 2, CipherBlob: "A_v2", UpdatedAt: 3,
	})
	if err != nil {
		t.Fatalf("stale push: %v", err)
	}
	if !res.Conflict {
		t.Error("expected conflict on stale push")
	}
	if res.PrevBlob != "B_v5" {
		t.Errorf("prev_blob = %q, want B_v5", res.PrevBlob)
	}
	// Server rev should advance past the stale client rev.
	if res.ServerRev <= 5 {
		t.Errorf("server_rev = %d, want > 5", res.ServerRev)
	}
}

func TestPull_WorkspaceIsolation(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = s.Push(ctx, &AssetMirror{ID: "a1", WorkspaceID: "ws_A", Kind: "note", ClientRev: 1, CipherBlob: "x"})
	_, _ = s.Push(ctx, &AssetMirror{ID: "a2", WorkspaceID: "ws_B", Kind: "note", ClientRev: 1, CipherBlob: "y"})

	pulledA, _ := s.Pull(ctx, "ws_A", 0, 10)
	pulledB, _ := s.Pull(ctx, "ws_B", 0, 10)
	if len(pulledA) != 1 || pulledA[0].ID != "a1" {
		t.Errorf("ws_A pulled = %+v", pulledA)
	}
	if len(pulledB) != 1 || pulledB[0].ID != "a2" {
		t.Errorf("ws_B pulled = %+v", pulledB)
	}
}

func TestGetMirror_Missing(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	m, err := s.GetMirror(context.Background(), "nope")
	if err != nil {
		t.Errorf("expected nil error on miss, got %v", err)
	}
	if m != nil {
		t.Errorf("expected nil on miss, got %+v", m)
	}
}
