package vault

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

func vaultTestDSN() string {
	for _, key := range []string{"POCKET_TEST_POSTGRES_DSN", "POCKET_POSTGRES_DSN"} {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func newVaultTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := vaultTestDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN or POCKET_POSTGRES_DSN not set; skipping vault integration test")
	}

	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect root pool: %v", err)
	}
	nameBytes := make([]byte, 6)
	_, _ = rand.Read(nameBytes)
	schema := "vault_test_" + hex.EncodeToString(nameBytes)
	if _, err := rootPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		rootPool.Close()
		_ = dropVaultTestSchema(ctx, rootPool, schema)
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
		_ = dropVaultTestSchema(ctx, rootPool, schema)
		t.Fatalf("create test pool: %v", err)
	}

	store, err := NewStore(pool)
	if err != nil {
		pool.Close()
		_ = dropVaultTestSchema(ctx, rootPool, schema)
		rootPool.Close()
		t.Fatalf("create vault store: %v", err)
	}
	cleanup := func() {
		pool.Close()
		_ = dropVaultTestSchema(ctx, rootPool, schema)
		rootPool.Close()
	}
	return store, cleanup
}

func dropVaultTestSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	_, err := pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
	return err
}

func TestPutLatestConcurrentLeavesOneCurrent(t *testing.T) {
	store, cleanup := newVaultTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const writes = 24
	var wg sync.WaitGroup
	errs := make(chan error, writes)
	for i := 1; i <= writes; i++ {
		wg.Add(1)
		go func(version int) {
			defer wg.Done()
			errs <- store.PutLatest(ctx, "workspace-concurrent", "user-1", fmt.Sprintf("cipher-%d", version), version)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	var current int
	if err := store.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM vault_sync
		WHERE workspace_id = $1 AND user_id = $2 AND is_current
	`, "workspace-concurrent", "user-1").Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != 1 {
		t.Fatalf("current rows = %d, want 1", current)
	}
}

func TestVaultVersionsAreWorkspaceScoped(t *testing.T) {
	store, cleanup := newVaultTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.PutLatest(ctx, "workspace-a", "user-1", "cipher-a-v1", 1); err != nil {
		t.Fatalf("put workspace A v1: %v", err)
	}
	if err := store.PutLatest(ctx, "workspace-b", "user-1", "cipher-b-v1", 1); err != nil {
		t.Fatalf("put workspace B v1: %v", err)
	}
	if err := store.PutLatest(ctx, "workspace-a", "user-1", "cipher-a-v2", 2); err != nil {
		t.Fatalf("put workspace A v2: %v", err)
	}

	ciphertext, version, err := store.GetLatest(ctx, "workspace-a", "user-1")
	if err != nil || ciphertext != "cipher-a-v2" || version != 2 {
		t.Fatalf("workspace A latest = %q/%d, %v", ciphertext, version, err)
	}
	ciphertext, version, err = store.GetLatest(ctx, "workspace-b", "user-1")
	if err != nil || ciphertext != "cipher-b-v1" || version != 1 {
		t.Fatalf("workspace B latest = %q/%d, %v", ciphertext, version, err)
	}

	versions, err := store.ListVersions(ctx, "workspace-a", "user-1")
	if err != nil || len(versions) != 2 {
		t.Fatalf("workspace A versions = %#v, %v", versions, err)
	}
	versions, err = store.ListVersions(ctx, "workspace-b", "user-1")
	if err != nil || len(versions) != 1 || versions[0].Version != 1 {
		t.Fatalf("workspace B versions = %#v, %v", versions, err)
	}

	blob, err := store.GetByVersion(ctx, "workspace-a", "user-1", 1)
	if err != nil || blob != "cipher-a-v1" {
		t.Fatalf("workspace A v1 = %q, %v", blob, err)
	}
	blob, err = store.GetByVersion(ctx, "workspace-b", "user-1", 1)
	if err != nil || blob != "cipher-b-v1" {
		t.Fatalf("workspace B v1 = %q, %v", blob, err)
	}

	if err := store.MarkCurrent(ctx, "workspace-a", "user-1", 1); err != nil {
		t.Fatalf("restore workspace A v1: %v", err)
	}
	ciphertext, version, err = store.GetLatest(ctx, "workspace-a", "user-1")
	if err != nil || ciphertext != "cipher-a-v1" || version != 1 {
		t.Fatalf("workspace A restored latest = %q/%d, %v", ciphertext, version, err)
	}
	ciphertext, version, err = store.GetLatest(ctx, "workspace-b", "user-1")
	if err != nil || ciphertext != "cipher-b-v1" || version != 1 {
		t.Fatalf("workspace B latest after A restore = %q/%d, %v", ciphertext, version, err)
	}
}
