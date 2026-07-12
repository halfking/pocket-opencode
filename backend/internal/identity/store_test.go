package identity

// store_test.go — integration tests for the Identity Core.
//
// These tests need a live PostgreSQL instance. Set POCKET_TEST_POSTGRES_DSN
// (or POCKET_POSTGRES_DSN) to run them; otherwise they are skipped so that
// `go test ./...` stays green on machines without a DB.
//
// Each subtest runs in an isolated schema that is dropped on cleanup, so
// parallel test runs are safe. The schema is created via
// `CREATE SCHEMA identity_test_<random>; SET search_path TO ...` and the store
// is pointed at it through a per-test pool.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

// newTestStore provisions an isolated schema + pool + Store for one test.
// Returns the store and a cleanup func.
func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping identity integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect root pool: %v", err)
	}

	// Random schema name to isolate this test.
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "identity_test_" + hex.EncodeToString(b)

	if _, err := rootPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}

	// Open a second pool pinned to this schema via search_path.
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		rootPool.Close()
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
		t.Fatalf("create test pool: %v", err)
	}

	store, err := New(pool)
	if err != nil {
		pool.Close()
		rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
		t.Fatalf("New store: %v", err)
	}

	cleanup := func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
	}
	return store, cleanup
}

func TestCreateAndGetWorkspace(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	ws := &Workspace{ID: "ws_a", OwnerID: "u1", Name: "我的随身公司"}
	if err := s.CreateDefaultWorkspace(ctx, ws); err != nil {
		t.Fatalf("CreateDefaultWorkspace: %v", err)
	}

	got, err := s.GetWorkspace(ctx, "ws_a")
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got.OwnerID != "u1" || got.Type != "default" {
		t.Errorf("unexpected workspace: %+v", got)
	}

	// Owner should be auto-added as a member.
	m, err := s.GetMember(ctx, "ws_a", "u1")
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if m == nil || m.Role != RoleOwner {
		t.Errorf("owner membership missing or wrong role: %+v", m)
	}
}

func TestGetWorkspace_NotFound(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	_, err := s.GetWorkspace(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestInviteCap(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	ws := &Workspace{ID: "ws_cap", OwnerID: "owner1", Name: "cap test"}
	if err := s.CreateDefaultWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}

	// Invite exactly MaxInvitees (3) — should all succeed.
	for i := 1; i <= MaxInvitees; i++ {
		uid := fmt.Sprintf("invitee%d", i)
		if err := s.AddMember(ctx, &Member{WorkspaceID: "ws_cap", UserID: uid, Role: RoleInvitee}); err != nil {
			t.Fatalf("invite %s: %v", uid, err)
		}
	}

	// The (MaxInvitees+1)-th invite should hit ErrInviteeLimit.
	err := s.AddMember(ctx, &Member{WorkspaceID: "ws_cap", UserID: "invitee4", Role: RoleInvitee})
	if !errors.Is(err, ErrInviteeLimit) {
		t.Errorf("expected ErrInviteeLimit on 4th invitee, got %v", err)
	}

	// Re-inviting an existing invitee should NOT error (idempotent).
	if err := s.AddMember(ctx, &Member{WorkspaceID: "ws_cap", UserID: "invitee1", Role: RoleInvitee}); err != nil {
		t.Errorf("re-invite existing member should be idempotent, got %v", err)
	}
}

func TestRemoveMember_OwnerProtected(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	ws := &Workspace{ID: "ws_rm", OwnerID: "owner1", Name: "rm test"}
	if err := s.CreateDefaultWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}

	// Removing the owner should fail (ErrNotFound — the WHERE clause excludes owner).
	if err := s.RemoveMember(ctx, "ws_rm", "owner1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing owner should fail, got %v", err)
	}

	// Invite then remove a normal member.
	if err := s.AddMember(ctx, &Member{WorkspaceID: "ws_rm", UserID: "inv1", Role: RoleInvitee}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMember(ctx, "ws_rm", "inv1"); err != nil {
		t.Fatalf("RemoveMember inv1: %v", err)
	}
	got, _ := s.GetMember(ctx, "ws_rm", "inv1")
	if got != nil {
		t.Errorf("member should be gone, got %+v", got)
	}
}

func TestListWorkspacesForUser(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// u_owner owns one workspace and is invited to another.
	if err := s.CreateDefaultWorkspace(ctx, &Workspace{ID: "ws_own", OwnerID: "u_owner", Name: "owned"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDefaultWorkspace(ctx, &Workspace{ID: "ws_other", OwnerID: "u_other", Name: "other"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(ctx, &Member{WorkspaceID: "ws_other", UserID: "u_owner", Role: RoleInvitee}); err != nil {
		t.Fatal(err)
	}

	wss, err := s.ListWorkspacesForUser(ctx, "u_owner")
	if err != nil {
		t.Fatalf("ListWorkspacesForUser: %v", err)
	}
	if len(wss) != 2 {
		t.Errorf("expected 2 workspaces for u_owner, got %d", len(wss))
	}
}

func TestUpsertAndDeleteDevice(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.CreateDefaultWorkspace(ctx, &Workspace{ID: "ws_dev", OwnerID: "u1", Name: "dev"}); err != nil {
		t.Fatal(err)
	}

	d := &Device{
		ID: "dev1", UserID: "u1", WorkspaceID: "ws_dev",
		Fingerprint: "fp1", PushToken: "tokA", OS: "ios",
	}
	if err := s.UpsertDevice(ctx, d); err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}
	// Upsert again with a new token — should update, not duplicate.
	d.PushToken = "tokB"
	if err := s.UpsertDevice(ctx, d); err != nil {
		t.Fatal(err)
	}
	devs, err := s.ListDevices(ctx, "ws_dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0].PushToken != "tokB" {
		t.Errorf("expected 1 device with tokB, got %+v", devs)
	}

	if err := s.DeleteDevice(ctx, "dev1"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	devs, _ = s.ListDevices(ctx, "ws_dev")
	if len(devs) != 0 {
		t.Errorf("expected 0 devices after delete, got %d", len(devs))
	}

	if err := s.DeleteDevice(ctx, "dev1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting again should be ErrNotFound, got %v", err)
	}
}

func TestEnsureDefaultWorkspace_Idempotent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	ws1, err := s.EnsureDefaultWorkspace(ctx, "u_new")
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	if ws1.ID != "ws_u_new" {
		t.Errorf("expected ws_u_new, got %s", ws1.ID)
	}
	// Second call must return the existing one, not error or duplicate.
	ws2, err := s.EnsureDefaultWorkspace(ctx, "u_new")
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if ws2.ID != ws1.ID {
		t.Errorf("idempotent Ensure returned different id: %s vs %s", ws1.ID, ws2.ID)
	}
	// Touch time so go vet/staticcheck doesn't complain about ws2 unused.
	_ = ws2.CreatedAt > 0
	_ = time.Now()
}
