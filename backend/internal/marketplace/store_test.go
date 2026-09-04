package marketplace

// store_test.go — PG integration tests for marketplace Store.
//
// These tests need a live PostgreSQL instance. Set POCKET_TEST_POSTGRES_DSN
// (or POCKET_POSTGRES_DSN) to run them; otherwise they are skipped.
//
// Each subtest runs in an isolated schema that is dropped on cleanup, so
// parallel test runs are safe.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
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

// newTestStore provisions an isolated schema + pool + Store for one test.
// Returns the store and a cleanup func.
func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping marketplace integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect root pool: %v", err)
	}

	// Random schema name to isolate this test.
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "marketplace_test_" + hex.EncodeToString(b)

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
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		rootPool.Close()
		t.Fatalf("create test pool: %v", err)
	}

	store := NewStore(pool)

	// Initialize marketplace tables in the isolated schema.
	if err := store.Init(ctx); err != nil {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
		t.Fatalf("Init store: %v", err)
	}

	cleanup := func() {
		pool.Close()
		_, _ = rootPool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		rootPool.Close()
	}
	return store, cleanup
}

// TestPGStore_Lifecycle 走完 submit → review → publish → install → rate
// 的完整生命周期，验证 PG Store 行为与 MemoryStore 一致。
func TestPGStore_Lifecycle(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// 1. Submit：首次提交创建 Package + draft Version。
	v, err := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1",
		Name:        "skill-x",
		Kind:        "skill",
		Version:     "1.0.0",
		Digest:      "sha256:abc",
		Manifest: Manifest{
			Version:     "1.0.0",
			Digest:      "sha256:abc",
			Permissions: []string{"fs.read", "net.fetch"},
		},
		Publisher:  "alice",
		Visibility: "workspace",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if v.Status != VersionDraft {
		t.Errorf("expected draft, got %s", v.Status)
	}

	// 2. Publish 在 approved 之前必须失败。
	if _, err := s.Publish(ctx, PublishCommand{WorkspaceID: "ws-1", VersionID: v.VersionID}); err == nil {
		t.Fatal("publish on draft must fail")
	} else if !errors.Is(err, ErrMarketplaceConflict) {
		t.Errorf("publish error: want conflict, got %v", err)
	}

	// 3. Review approved → publish → install。
	if err := s.Review(ctx, ReviewCommand{
		WorkspaceID: "ws-1", VersionID: v.VersionID, Reviewer: "bob", Approved: true,
	}); err != nil {
		t.Fatalf("Review approve: %v", err)
	}
	rel, err := s.Publish(ctx, PublishCommand{
		WorkspaceID: "ws-1", VersionID: v.VersionID, Channel: "stable",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if rel.Channel != "stable" {
		t.Errorf("channel = %q", rel.Channel)
	}

	inst, err := s.Install(ctx, InstallCommand{
		WorkspaceID: "ws-1", ReleaseID: rel.ReleaseID, InstalledBy: "carol",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if inst.InstallationID == "" || inst.ReleaseID != rel.ReleaseID {
		t.Errorf("install ref malformed: %+v", inst)
	}

	// 4. Rate 范围校验。
	if err := s.Rate(ctx, RatingCommand{
		WorkspaceID: "ws-1", ReleaseID: rel.ReleaseID, RatedBy: "carol", Score: 5,
	}); err != nil {
		t.Errorf("rate score=5: %v", err)
	}
	if err := s.Rate(ctx, RatingCommand{
		WorkspaceID: "ws-1", ReleaseID: rel.ReleaseID, RatedBy: "carol", Score: 7,
	}); err == nil {
		t.Error("rate score=7 must fail")
	} else if !errors.Is(err, ErrMarketplaceRateOutOfRange) {
		t.Errorf("rate error: want out-of-range, got %v", err)
	}
}

// TestPGStore_PublishCrossWorkspace 校验 Publish 的租户边界：调用方
// 不能用别人的 workspace_id 发布人家审核通过的版本。
func TestPGStore_PublishCrossWorkspace(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	v, err := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-owner", Name: "s", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "alice",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := s.Review(ctx, ReviewCommand{
		WorkspaceID: "ws-owner", VersionID: v.VersionID, Reviewer: "bob", Approved: true,
	}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	_, err = s.Publish(ctx, PublishCommand{WorkspaceID: "ws-attacker", VersionID: v.VersionID})
	if !errors.Is(err, ErrMarketplaceNotFound) {
		t.Fatalf("cross-workspace publish must return NotFound, got %v", err)
	}
}

// TestPGStore_InstallIdempotent 校验 Install 对同一 (workspace, release)
// 重复调用返回首次的 InstallationRef，不会插入第二行。验证唯一索引约束。
func TestPGStore_InstallIdempotent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	v, _ := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "s", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "alice",
	})
	_ = s.Review(ctx, ReviewCommand{WorkspaceID: "ws-1", VersionID: v.VersionID, Reviewer: "r", Approved: true})
	rel, err := s.Publish(ctx, PublishCommand{WorkspaceID: "ws-1", VersionID: v.VersionID})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	first, err := s.Install(ctx, InstallCommand{WorkspaceID: "ws-1", ReleaseID: rel.ReleaseID, InstalledBy: "u"})
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	second, err := s.Install(ctx, InstallCommand{WorkspaceID: "ws-1", ReleaseID: rel.ReleaseID, InstalledBy: "u"})
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if first.InstallationID != second.InstallationID {
		t.Errorf("idempotent install must reuse id, got %q vs %q", first.InstallationID, second.InstallationID)
	}
}

// TestPGStore_InstallIdempotentConcurrent 验证并发 Install 的幂等性：
// 多个 goroutine 同时 install 同一 (workspace, release)，唯一索引约束
// 确保只有一行被插入，所有调用者都得到相同的 InstallationID。
func TestPGStore_InstallIdempotentConcurrent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	v, _ := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "s", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "alice",
	})
	_ = s.Review(ctx, ReviewCommand{WorkspaceID: "ws-1", VersionID: v.VersionID, Reviewer: "r", Approved: true})
	rel, err := s.Publish(ctx, PublishCommand{WorkspaceID: "ws-1", VersionID: v.VersionID})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	const concurrency = 10
	results := make([]InstallationRef, concurrency)
	errs := make([]error, concurrency)
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			ref, err := s.Install(ctx, InstallCommand{
				WorkspaceID: "ws-1", ReleaseID: rel.ReleaseID, InstalledBy: fmt.Sprintf("u%d", idx),
			})
			results[idx] = ref
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	// 所有调用都应成功。
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d failed: %v", i, err)
		}
	}

	// 所有 InstallationID 必须相同。
	firstID := results[0].InstallationID
	for i, ref := range results {
		if ref.InstallationID != firstID {
			t.Errorf("goroutine %d got different ID: %q vs %q", i, ref.InstallationID, firstID)
		}
	}
}

// TestPGStore_InstallPrivateReleaseCrossWorkspace 校验 Install 不能跨租户
// 装入"幽灵"版本：私有 visibility 的 package release 对外部 workspace 必须
// 返回 NotFound。
func TestPGStore_InstallPrivateReleaseCrossWorkspace(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	v, _ := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-owner", Name: "s", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "alice",
		Visibility: "workspace",
	})
	_ = s.Review(ctx, ReviewCommand{WorkspaceID: "ws-owner", VersionID: v.VersionID, Reviewer: "r", Approved: true})
	rel, _ := s.Publish(ctx, PublishCommand{WorkspaceID: "ws-owner", VersionID: v.VersionID})

	_, err := s.Install(ctx, InstallCommand{WorkspaceID: "ws-other", ReleaseID: rel.ReleaseID, InstalledBy: "u"})
	if !errors.Is(err, ErrMarketplaceNotFound) {
		t.Fatalf("private release cross-workspace install must return NotFound, got %v", err)
	}
}

// TestPGStore_ListVisibility 校验 ListPackages/ListReleases 的可见性规则：
//   - 本 workspace 所有包/发布；
//   - visibility='public' 的外部包/发布；
//   - 外部私有 visibility 必须不可见。
func TestPGStore_ListVisibility(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	privV, _ := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-A", Name: "priv", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "a", Visibility: "workspace",
	})
	_ = s.Review(ctx, ReviewCommand{WorkspaceID: "ws-A", VersionID: privV.VersionID, Reviewer: "r", Approved: true})
	_, _ = s.Publish(ctx, PublishCommand{WorkspaceID: "ws-A", VersionID: privV.VersionID})

	pubV, _ := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-A", Name: "pub", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "a", Visibility: "public",
	})
	_ = s.Review(ctx, ReviewCommand{WorkspaceID: "ws-A", VersionID: pubV.VersionID, Reviewer: "r", Approved: true})
	_, _ = s.Publish(ctx, PublishCommand{WorkspaceID: "ws-A", VersionID: pubV.VersionID})

	pkgs, err := s.ListPackages(ctx, "ws-B")
	if err != nil {
		t.Fatalf("ListPackages: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].Name != "pub" {
		t.Errorf("ws-B should see only public package, got %+v", pkgs)
	}

	releases, err := s.ListReleases(ctx, "ws-B")
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(releases) != 1 {
		t.Errorf("ws-B should see only public release, got %d", len(releases))
	}

	pkgsA, _ := s.ListPackages(ctx, "ws-A")
	if len(pkgsA) != 2 {
		t.Errorf("ws-A should see both packages, got %d", len(pkgsA))
	}
}

// TestPGStore_PublishRowLock 验证 Publish 的行锁机制：并发 Publish 同一
// version_id，只有一个成功插入 release，其他应收到 conflict 错误。
func TestPGStore_PublishRowLock(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	v, _ := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "s", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "alice",
	})
	_ = s.Review(ctx, ReviewCommand{WorkspaceID: "ws-1", VersionID: v.VersionID, Reviewer: "r", Approved: true})

	const concurrency = 5
	results := make([]ReleaseRef, concurrency)
	errs := make([]error, concurrency)
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			ref, err := s.Publish(ctx, PublishCommand{WorkspaceID: "ws-1", VersionID: v.VersionID})
			results[idx] = ref
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	// 只有一个成功，其他必须失败（conflict 或 NotFound）。
	successCount := 0
	for i, err := range errs {
		if err == nil {
			successCount++
		} else if !errors.Is(err, ErrMarketplaceConflict) && !errors.Is(err, ErrMarketplaceNotFound) {
			t.Errorf("goroutine %d unexpected error: %v", i, err)
		}
	}
	if successCount != 1 {
		t.Errorf("expected exactly 1 success, got %d", successCount)
	}
}
