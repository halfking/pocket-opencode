package marketplace

import (
	"context"
	"errors"
	"testing"
)

// TestMemoryStore_Lifecycle 走完 submit → review → publish → install → rate
// 的完整生命周期,并断言每个阶段的状态变化与错误路径。
func TestMemoryStore_Lifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// 1. Submit：首次提交创建 Package + draft Version。
	v, err := store.Submit(ctx, SubmitRequest{
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
	if _, err := store.Publish(ctx, PublishCommand{WorkspaceID: "ws-1", VersionID: v.VersionID}); err == nil {
		t.Fatal("publish on draft must fail")
	} else if !errors.Is(err, ErrMarketplaceConflict) {
		t.Errorf("publish error: want conflict, got %v", err)
	}

	// 3. Reject 路径：拒绝后状态必须为 rejected，再 publish 仍应失败。
	if err := store.Review(ctx, ReviewCommand{
		WorkspaceID: "ws-1",
		VersionID:   v.VersionID,
		Reviewer:    "bob",
		Approved:    false,
		Comment:     "permission list too broad",
	}); err != nil {
		t.Fatalf("Review reject: %v", err)
	}
	if _, err := store.Publish(ctx, PublishCommand{WorkspaceID: "ws-1", VersionID: v.VersionID}); err == nil {
		t.Fatal("publish on rejected must fail")
	}

	// 4. 提交新版本 → review approved → publish → install → rate。
	v2, err := store.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1",
		Name:        "skill-x",
		Kind:        "skill",
		Version:     "1.1.0",
		Digest:      "sha256:def",
		Manifest:    Manifest{Version: "1.1.0", Digest: "sha256:def"},
		Publisher:   "alice",
	})
	if err != nil {
		t.Fatalf("Submit v2: %v", err)
	}
	if err := store.Review(ctx, ReviewCommand{
		WorkspaceID: "ws-1", VersionID: v2.VersionID, Reviewer: "bob", Approved: true,
	}); err != nil {
		t.Fatalf("Review approve: %v", err)
	}
	rel, err := store.Publish(ctx, PublishCommand{
		WorkspaceID: "ws-1", VersionID: v2.VersionID, Channel: "stable",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if rel.Channel != "stable" {
		t.Errorf("channel = %q", rel.Channel)
	}

	inst, err := store.Install(ctx, InstallCommand{
		WorkspaceID: "ws-1", ReleaseID: rel.ReleaseID, InstalledBy: "carol",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if inst.InstallationID == "" || inst.ReleaseID != rel.ReleaseID {
		t.Errorf("install ref malformed: %+v", inst)
	}

	// 5. Rate 范围校验。
	if err := store.Rate(ctx, RatingCommand{
		WorkspaceID: "ws-1", ReleaseID: rel.ReleaseID, RatedBy: "carol", Score: 5,
	}); err != nil {
		t.Errorf("rate score=5: %v", err)
	}
	if err := store.Rate(ctx, RatingCommand{
		WorkspaceID: "ws-1", ReleaseID: rel.ReleaseID, RatedBy: "carol", Score: 7,
	}); err == nil {
		t.Error("rate score=7 must fail")
	} else if !errors.Is(err, ErrMarketplaceRateOutOfRange) {
		t.Errorf("rate error: want out-of-range, got %v", err)
	}

	// 6. List 校验。
	pkgs, err := store.ListPackages(ctx, "ws-1")
	if err != nil || len(pkgs) != 1 {
		t.Errorf("ListPackages: err=%v len=%d", err, len(pkgs))
	}
	releases, err := store.ListReleases(ctx, "ws-1")
	if err != nil || len(releases) != 1 {
		t.Errorf("ListReleases: err=%v len=%d", err, len(releases))
	}
	versions, err := store.ListVersions(ctx, "ws-1", pkgs[0].PackageID)
	if err != nil || len(versions) != 2 {
		t.Errorf("ListVersions: err=%v len=%d", err, len(versions))
	}

	// 7. workspace 隔离：另一个 workspace 看不到。
	if pkgs2, _ := store.ListPackages(ctx, "ws-2"); len(pkgs2) != 0 {
		t.Errorf("workspace isolation failed: %d packages visible to ws-2", len(pkgs2))
	}

	// 8. Revoke 之后不允许再 publish（已发布版本状态变为 revoked）。
	if err := store.Revoke(ctx, RevokeCommand{
		WorkspaceID: "ws-1", ReleaseID: rel.ReleaseID, Reason: "suppressed", RevokedBy: "admin",
	}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := store.Publish(ctx, PublishCommand{WorkspaceID: "ws-1", VersionID: v2.VersionID}); err == nil {
		t.Error("publish after revoke must fail")
	}
}

// TestMemoryStore_VisibilityValidation 验证 SubmitRequest 必填字段校验。
func TestMemoryStore_RequiredFields(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	cases := []SubmitRequest{
		{Name: "x", Kind: "skill", Version: "1", Digest: "d"},
		{WorkspaceID: "w", Name: "x", Kind: "skill", Version: "1"},
		{WorkspaceID: "w", Name: "x", Kind: "skill", Digest: "d"},
		{WorkspaceID: "w", Name: "x", Version: "1", Digest: "d"},
	}
	for i, req := range cases {
		if _, err := store.Submit(ctx, req); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

// TestMemoryStore_InstallUnknownRelease 校验 install 不存在的 release 返回
// ErrMarketplaceNotFound,避免误装入"幽灵"版本。
func TestMemoryStore_InstallUnknownRelease(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	_, err := store.Install(ctx, InstallCommand{
		WorkspaceID: "ws-1", ReleaseID: "ghost", InstalledBy: "u",
	})
	if !errors.Is(err, ErrMarketplaceNotFound) {
		t.Fatalf("expected ErrMarketplaceNotFound, got %v", err)
	}
}

// errorIs 已被标准库 errors.Is 替代,这里保留旧函数名仅为向后兼容(避免
// 在重构时反复改名)。新代码请直接使用 errors.Is。
func errorIs(err, target error) bool { return errors.Is(err, target) }

// TestMemoryStore_PublishCrossWorkspace 校验 Publish 的租户边界：调用方
// 不能用别人的 workspace_id 发布人家审核通过的版本。memory store 必须与
// pg Store 行为一致。
func TestMemoryStore_PublishCrossWorkspace(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	v, err := store.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-owner", Name: "s", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "alice",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := store.Review(ctx, ReviewCommand{
		WorkspaceID: "ws-owner", VersionID: v.VersionID, Reviewer: "bob", Approved: true,
	}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	_, err = store.Publish(ctx, PublishCommand{WorkspaceID: "ws-attacker", VersionID: v.VersionID})
	if !errors.Is(err, ErrMarketplaceNotFound) {
		t.Fatalf("cross-workspace publish must return NotFound, got %v", err)
	}
}

// TestMemoryStore_InstallIdempotent 校验 Install 对同一 (workspace, release)
// 重复调用返回首次的 InstallationRef,不会插入第二行。
func TestMemoryStore_InstallIdempotent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	v, _ := store.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "s", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "alice",
	})
	_ = store.Review(ctx, ReviewCommand{WorkspaceID: "ws-1", VersionID: v.VersionID, Reviewer: "r", Approved: true})
	rel, err := store.Publish(ctx, PublishCommand{WorkspaceID: "ws-1", VersionID: v.VersionID})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	first, err := store.Install(ctx, InstallCommand{WorkspaceID: "ws-1", ReleaseID: rel.ReleaseID, InstalledBy: "u"})
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	second, err := store.Install(ctx, InstallCommand{WorkspaceID: "ws-1", ReleaseID: rel.ReleaseID, InstalledBy: "u"})
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if first.InstallationID != second.InstallationID {
		t.Errorf("idempotent install must reuse id, got %q vs %q", first.InstallationID, second.InstallationID)
	}
}

// TestMemoryStore_InstallPrivateReleaseCrossWorkspace 校验 Install 不能跨租户
// 装入"幽灵"版本：私有 visibility 的 package release 对外部 workspace 必须
// 返回 NotFound。
func TestMemoryStore_InstallPrivateReleaseCrossWorkspace(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	v, _ := store.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-owner", Name: "s", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "alice",
		Visibility: "workspace",
	})
	_ = store.Review(ctx, ReviewCommand{WorkspaceID: "ws-owner", VersionID: v.VersionID, Reviewer: "r", Approved: true})
	rel, _ := store.Publish(ctx, PublishCommand{WorkspaceID: "ws-owner", VersionID: v.VersionID})
	_, err := store.Install(ctx, InstallCommand{WorkspaceID: "ws-other", ReleaseID: rel.ReleaseID, InstalledBy: "u"})
	if !errors.Is(err, ErrMarketplaceNotFound) {
		t.Fatalf("private release cross-workspace install must return NotFound, got %v", err)
	}
}

// TestMemoryStore_ListVisibility 校验 ListPackages/ListReleases 的可见性规则：
//   - 本 workspace 所有包/发布；
//   - visibility='public' 的外部包/发布；
//   - 外部私有 visibility 必须不可见。
func TestMemoryStore_ListVisibility(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	privV, _ := store.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-A", Name: "priv", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "a", Visibility: "workspace",
	})
	_ = store.Review(ctx, ReviewCommand{WorkspaceID: "ws-A", VersionID: privV.VersionID, Reviewer: "r", Approved: true})
	_, _ = store.Publish(ctx, PublishCommand{WorkspaceID: "ws-A", VersionID: privV.VersionID})

	pubV, _ := store.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-A", Name: "pub", Kind: "skill", Version: "1.0.0", Digest: "d",
		Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "a", Visibility: "public",
	})
	_ = store.Review(ctx, ReviewCommand{WorkspaceID: "ws-A", VersionID: pubV.VersionID, Reviewer: "r", Approved: true})
	_, _ = store.Publish(ctx, PublishCommand{WorkspaceID: "ws-A", VersionID: pubV.VersionID})

	pkgs, err := store.ListPackages(ctx, "ws-B")
	if err != nil {
		t.Fatalf("ListPackages: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].Name != "pub" {
		t.Errorf("ws-B should see only public package, got %+v", pkgs)
	}

	releases, err := store.ListReleases(ctx, "ws-B")
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(releases) != 1 {
		t.Errorf("ws-B should see only public release, got %d", len(releases))
	}

	pkgsA, _ := store.ListPackages(ctx, "ws-A")
	if len(pkgsA) != 2 {
		t.Errorf("ws-A should see both packages, got %d", len(pkgsA))
	}
}
