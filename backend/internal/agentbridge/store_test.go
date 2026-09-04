package agentbridge

// store_test.go — PG-backed integration tests for the agents Store.
// Same isolated-schema pattern as identity/lobster. Skipped without DSN.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
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
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
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

func TestCreateAgentCapConcurrent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const attempts = MaxAgentsPerWorkspace + 8
	var wg sync.WaitGroup
	results := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- s.Create(ctx, &Agent{
				ID: fmt.Sprintf("concurrent-agent-%d", i), WorkspaceID: "ws-concurrent", InstanceID: "i", Name: "n",
			})
		}(i)
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if err != ErrLimitReached {
			t.Fatalf("unexpected concurrent create error: %v", err)
		}
	}
	if successes != MaxAgentsPerWorkspace {
		t.Fatalf("successful creates = %d, want %d", successes, MaxAgentsPerWorkspace)
	}
	count, err := s.CountByWorkspace(ctx, "ws-concurrent")
	if err != nil || count != MaxAgentsPerWorkspace {
		t.Fatalf("stored agents = %d, %v; want %d", count, err, MaxAgentsPerWorkspace)
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

// TestGetScoped_TenantIsolation 验证 GetScoped 的 SQL 真的带上了
// workspace_id：同 ID 跨 workspace 必须 ErrNotFound，而非返回别人的 agent。
func TestGetScoped_TenantIsolation(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_ = s.Create(ctx, &Agent{ID: "a1", WorkspaceID: "wsOwner", InstanceID: "i1", Name: "owned"})

	got, err := s.GetScoped(ctx, "a1", "wsOwner")
	if err != nil {
		t.Fatalf("GetScoped same workspace: %v", err)
	}
	if got.Name != "owned" {
		t.Errorf("name = %s, want owned", got.Name)
	}

	if _, err := s.GetScoped(ctx, "a1", "wsAttacker"); err != ErrNotFound {
		t.Errorf("cross-workspace GetScoped should be ErrNotFound, got %v", err)
	}
}

// TestDeleteScoped_TenantIsolation 验证跨 workspace 删除不会生效。
func TestDeleteScoped_TenantIsolation(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_ = s.Create(ctx, &Agent{ID: "a1", WorkspaceID: "wsOwner", InstanceID: "i1", Name: "n"})

	if err := s.DeleteScoped(ctx, "a1", "wsAttacker"); err != ErrNotFound {
		t.Errorf("cross-workspace delete should be ErrNotFound, got %v", err)
	}
	// 仍然存在。
	if _, err := s.GetScoped(ctx, "a1", "wsOwner"); err != nil {
		t.Fatalf("agent must survive a cross-workspace delete: %v", err)
	}
	// 本 workspace 删除正常。
	if err := s.DeleteScoped(ctx, "a1", "wsOwner"); err != nil {
		t.Fatalf("DeleteScoped own workspace: %v", err)
	}
	if _, err := s.GetScoped(ctx, "a1", "wsOwner"); err != ErrNotFound {
		t.Errorf("after scoped delete, expected ErrNotFound, got %v", err)
	}
}

// TestUpdateStatusScoped_TenantIsolation 验证跨 workspace 改状态无效。
func TestUpdateStatusScoped_TenantIsolation(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_ = s.Create(ctx, &Agent{ID: "a1", WorkspaceID: "wsOwner", InstanceID: "i", Name: "n"})

	if err := s.UpdateStatusScoped(ctx, "a1", "wsAttacker", StatusOffline); err != nil {
		t.Fatalf("UpdateStatusScoped returned error: %v", err)
	}
	got, _ := s.GetScoped(ctx, "a1", "wsOwner")
	if got.Status == StatusOffline {
		t.Error("cross-workspace UpdateStatusScoped must not change status")
	}

	if err := s.UpdateStatusScoped(ctx, "a1", "wsOwner", StatusOnline); err != nil {
		t.Fatalf("UpdateStatusScoped own workspace: %v", err)
	}
	got, _ = s.GetScoped(ctx, "a1", "wsOwner")
	if got.Status != StatusOnline {
		t.Errorf("status = %s, want online", got.Status)
	}
}

// TestGetScoped_EmptyWorkspaceDefaults 验证空 workspace 归一到 "default"，
// 与 ListByWorkspace 的既有行为一致。
func TestGetScoped_EmptyWorkspaceDefaults(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_ = s.Create(ctx, &Agent{ID: "a1", WorkspaceID: "default", InstanceID: "i", Name: "n"})

	if _, err := s.GetScoped(ctx, "a1", ""); err != nil {
		t.Fatalf("empty workspace should resolve to default: %v", err)
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
