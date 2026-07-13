package agentbridge

// store_test.go — PG-backed integration tests for the agents Store.
// Same isolated-schema pattern as identity/lobster. Skipped without DSN.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func pgDSN() string {
	for _, k := range []string{"POCKET_TEST_POSTGRES_DSN", "POCKET_POSTGRES_DSN"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping agentbridge integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "ab_test_" + hex.EncodeToString(b)
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
	store, err := New(pool)
	if err != nil {
		pool.Close()
		rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
		t.Fatalf("New: %v", err)
	}
	return store, func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
	}
}

func TestCreateAndGetAgent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	a := &Agent{ID: "a1", WorkspaceID: "ws1", InstanceID: "inst1", Name: "dev-agent", Capabilities: []string{"code", "test"}}
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(ctx, "a1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "dev-agent" || got.Role != RoleGeneric || got.Status != StatusUnknown {
		t.Errorf("agent = %+v", got)
	}
	if len(got.Capabilities) != 2 || got.Capabilities[0] != "code" {
		t.Errorf("capabilities = %+v", got.Capabilities)
	}
}

func TestGetAgent_NotFound(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	_, err := s.Get(context.Background(), "nope")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListByWorkspace(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_ = s.Create(ctx, &Agent{ID: "a1", WorkspaceID: "wsA", InstanceID: "i1", Name: "n1"})
	_ = s.Create(ctx, &Agent{ID: "a2", WorkspaceID: "wsA", InstanceID: "i2", Name: "n2"})
	_ = s.Create(ctx, &Agent{ID: "a3", WorkspaceID: "wsB", InstanceID: "i3", Name: "n3"})

	aAgents, _ := s.ListByWorkspace(ctx, "wsA")
	if len(aAgents) != 2 {
		t.Errorf("wsA count = %d, want 2", len(aAgents))
	}
	bAgents, _ := s.ListByWorkspace(ctx, "wsB")
	if len(bAgents) != 1 {
		t.Errorf("wsB count = %d, want 1", len(bAgents))
	}
}

func TestCreateAgent_CapEnforced(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create up to the cap.
	for i := 0; i < MaxAgentsPerWorkspace; i++ {
		if err := s.Create(ctx, &Agent{
			ID: fmt.Sprintf("a%d", i), WorkspaceID: "wsCap", InstanceID: "i", Name: "n",
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	// One more should fail.
	err := s.Create(ctx, &Agent{ID: "over", WorkspaceID: "wsCap", InstanceID: "i", Name: "n"})
	if err != ErrLimitReached {
		t.Errorf("expected ErrLimitReached, got %v", err)
	}
}

func TestUpdateStatus(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_ = s.Create(ctx, &Agent{ID: "a1", WorkspaceID: "ws", InstanceID: "i", Name: "n"})

	if err := s.UpdateStatus(ctx, "a1", StatusOnline); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ := s.Get(ctx, "a1")
	if got.Status != StatusOnline {
		t.Errorf("status = %s, want online", got.Status)
	}
}

func TestDeleteAgent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_ = s.Create(ctx, &Agent{ID: "a1", WorkspaceID: "ws", InstanceID: "i", Name: "n"})

	if err := s.Delete(ctx, "a1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "a1"); err != ErrNotFound {
		t.Errorf("after delete, Get should be ErrNotFound, got %v", err)
	}
	if err := s.Delete(ctx, "a1"); err != ErrNotFound {
		t.Errorf("delete again should be ErrNotFound, got %v", err)
	}
}
