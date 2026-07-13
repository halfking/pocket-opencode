package notifycenter

// store_test.go — PG-backed integration tests for the notifications Store.
// Same isolated-schema pattern as the other S0 stores.

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
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping notifycenter integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "nc_test_" + hex.EncodeToString(b)
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

func TestInsertAndListNotifications(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = s.InsertNotification(ctx, &Notification{
			ID: fmt.Sprintf("n%d", i), WorkspaceID: "ws1", Source: "task",
			Kind: "completed", Title: fmt.Sprintf("T%d", i), Body: "done",
		})
	}
	got, err := s.ListNotifications(ctx, "ws1", 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d, want 3", len(got))
	}
}

func TestListNotifications_UnreadOnly(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_ = s.InsertNotification(ctx, &Notification{ID: "n1", WorkspaceID: "ws", Title: "a"})
	_ = s.InsertNotification(ctx, &Notification{ID: "n2", WorkspaceID: "ws", Title: "b"})
	_ = s.MarkRead(ctx, "ws", "n1")

	all, _ := s.ListNotifications(ctx, "ws", 10, 0)
	unread, _ := s.ListNotifications(ctx, "ws", 10, 1)
	if len(all) != 2 {
		t.Errorf("all = %d, want 2", len(all))
	}
	if len(unread) != 1 || unread[0].ID != "n2" {
		t.Errorf("unread = %+v, want n2", unread)
	}
}

func TestMarkRead_All(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_ = s.InsertNotification(ctx, &Notification{ID: "n1", WorkspaceID: "ws"})
	_ = s.InsertNotification(ctx, &Notification{ID: "n2", WorkspaceID: "ws"})

	if err := s.MarkRead(ctx, "ws", ""); err != nil {
		t.Fatalf("MarkRead all: %v", err)
	}
	unread, _ := s.ListNotifications(ctx, "ws", 10, 1)
	if len(unread) != 0 {
		t.Errorf("expected 0 unread after mark-all, got %d", len(unread))
	}
}

func TestMarkRead_NotFound(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	if err := s.MarkRead(context.Background(), "ws", "nope"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpsertAndListRules(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	r1 := &Rule{
		ID: "r1", WorkspaceID: "ws", Source: "task", Kind: "completed",
		Channels: []string{"inbox", "websocket"}, Priority: "normal", Enabled: true,
	}
	if err := s.UpsertRule(ctx, r1); err != nil {
		t.Fatalf("UpsertRule: %v", err)
	}
	rules, _ := s.ListRules(ctx, "ws")
	if len(rules) != 1 || rules[0].ID != "r1" {
		t.Errorf("rules = %+v", rules)
	}
	if len(rules[0].Channels) != 2 {
		t.Errorf("channels = %+v", rules[0].Channels)
	}

	// Upsert again (update).
	r1.Priority = "high"
	_ = s.UpsertRule(ctx, r1)
	rules, _ = s.ListRules(ctx, "ws")
	if len(rules) != 1 || rules[0].Priority != "high" {
		t.Errorf("update failed: %+v", rules)
	}
}

func TestMatchRule_WildcardAndSpecific(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	_ = s.UpsertRule(ctx, &Rule{ID: "specific", WorkspaceID: "ws", Source: "task", Kind: "completed", Channels: []string{"inbox"}, Enabled: true})
	_ = s.UpsertRule(ctx, &Rule{ID: "wildcard", WorkspaceID: "ws", Source: "", Kind: "", Channels: []string{"inbox"}, Enabled: true})

	// Specific match preferred over wildcard.
	r, err := s.matchRule(ctx, "ws", "task", "completed")
	if err != nil {
		t.Fatalf("matchRule: %v", err)
	}
	if r.ID != "specific" {
		t.Errorf("expected specific rule, got %s", r.ID)
	}

	// Wildcard catches anything else.
	r, _ = s.matchRule(ctx, "ws", "email", "received")
	if r == nil || r.ID != "wildcard" {
		t.Errorf("expected wildcard, got %+v", r)
	}
}

func TestMatchRule_NoMatch(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	r, err := s.matchRule(context.Background(), "ws", "task", "completed")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r != nil {
		t.Errorf("expected nil on no match, got %+v", r)
	}
}

func TestListNotifications_WorkspaceIsolation(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_ = s.InsertNotification(ctx, &Notification{ID: "n1", WorkspaceID: "wsA"})
	_ = s.InsertNotification(ctx, &Notification{ID: "n2", WorkspaceID: "wsB"})

	a, _ := s.ListNotifications(ctx, "wsA", 10, 0)
	b, _ := s.ListNotifications(ctx, "wsB", 10, 0)
	if len(a) != 1 || a[0].ID != "n1" {
		t.Errorf("wsA = %+v", a)
	}
	if len(b) != 1 || b[0].ID != "n2" {
		t.Errorf("wsB = %+v", b)
	}
}
