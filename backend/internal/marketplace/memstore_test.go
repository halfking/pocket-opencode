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
