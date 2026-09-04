package marketplace

// blob_test.go — 内容寻址 blob 存取的 PG 集成测试。
//
// 复用 store_test.go 的 newTestStore harness：需要 POCKET_TEST_POSTGRES_DSN
// （或 POCKET_POSTGRES_DSN），否则整组测试自动 skip。每个测试运行在独立
// schema 中，可安全并行。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// blobDigestOf 计算内容的 sha256 hex（测试内构造合法 digest 用）。
func blobDigestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// TestPGBlob_PutGetRoundTrip 往返：Put 后 Get 回相同内容，meta 的
// digest（规范化小写）/size/content_type 一致；contentType 留空落库为
// application/octet-stream。
func TestPGBlob_PutGetRoundTrip(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	content := []byte("hello openpocket blob")
	// 大写 + sha256: 前缀，验证入参归一化。
	declared := "SHA256:" + strings.ToUpper(blobDigestOf(content))

	meta, err := s.PutBlob(ctx, declared, content, "application/zip")
	if err != nil {
		t.Fatalf("PutBlob: %v", err)
	}
	if meta.Digest != blobDigestOf(content) {
		t.Errorf("digest not normalized: got %q", meta.Digest)
	}
	if meta.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", meta.Size, len(content))
	}
	if meta.ContentType != "application/zip" {
		t.Errorf("content_type = %q", meta.ContentType)
	}
	if meta.CreatedAt.IsZero() {
		t.Error("created_at is zero")
	}

	gotMeta, gotContent, err := s.GetBlob(ctx, declared) // Get 同样接受带前缀大写
	if err != nil {
		t.Fatalf("GetBlob: %v", err)
	}
	if string(gotContent) != string(content) {
		t.Errorf("content mismatch: got %q", gotContent)
	}
	if gotMeta.Digest != meta.Digest || gotMeta.Size != meta.Size || gotMeta.ContentType != meta.ContentType {
		t.Errorf("meta mismatch: put=%+v get=%+v", meta, gotMeta)
	}

	// contentType 留空 → 默认 application/octet-stream。
	defMeta, err := s.PutBlob(ctx, "sha256:"+blobDigestOf([]byte("default-ct")), []byte("default-ct"), "")
	if err != nil {
		t.Fatalf("PutBlob empty content type: %v", err)
	}
	if defMeta.ContentType != "application/octet-stream" {
		t.Errorf("default content_type = %q", defMeta.ContentType)
	}
}

// TestPGBlob_PutIdempotent 同 digest 二次 Put：返回相同 meta，且表里
// 仍只有一行（主键去重）。
func TestPGBlob_PutIdempotent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	content := []byte("idempotent payload")
	digest := blobDigestOf(content)

	first, err := s.PutBlob(ctx, digest, content, "text/plain")
	if err != nil {
		t.Fatalf("first PutBlob: %v", err)
	}
	second, err := s.PutBlob(ctx, "sha256:"+strings.ToUpper(digest), content, "text/plain")
	if err != nil {
		t.Fatalf("second PutBlob: %v", err)
	}
	if first.Digest != second.Digest || first.Size != second.Size ||
		first.ContentType != second.ContentType || !first.CreatedAt.Equal(second.CreatedAt) {
		t.Errorf("idempotent put meta mismatch: first=%+v second=%+v", first, second)
	}

	// 表里仍只有一行（同包测试可直接查 pool）。
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM marketplace_blobs`).Scan(&n); err != nil {
		t.Fatalf("count blobs: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 blob row after duplicate put, got %d", n)
	}

	// Get 返回的仍是首次上传的内容。
	_, got, err := s.GetBlob(ctx, digest)
	if err != nil {
		t.Fatalf("GetBlob: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch after duplicate put: %q", got)
	}
}

// TestPGBlob_DigestMismatch digest 与内容不符 / digest 格式非法 →
// ErrBlobDigestMismatch。
func TestPGBlob_DigestMismatch(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	content := []byte("mismatch payload")
	wrongDigest := blobDigestOf([]byte("other content"))
	if _, err := s.PutBlob(ctx, wrongDigest, content, ""); !errors.Is(err, ErrBlobDigestMismatch) {
		t.Errorf("wrong digest: want ErrBlobDigestMismatch, got %v", err)
	}

	cases := []struct {
		name   string
		digest string
	}{
		{"non hex", strings.Repeat("z", 64)},
		{"too short", blobDigestOf(content)[:63]},
		{"too long", blobDigestOf(content) + "aa"},
		{"empty", ""},
	}
	for _, tc := range cases {
		if _, err := s.PutBlob(ctx, tc.digest, content, ""); !errors.Is(err, ErrBlobDigestMismatch) {
			t.Errorf("%s: want ErrBlobDigestMismatch, got %v", tc.name, err)
		}
	}
}

// TestPGBlob_TooLarge 超过 MaxBlobSize → ErrBlobTooLarge。
// 直接构造 64MiB+1 的内容（sha256 校验在大内容上也就百毫秒级，可接受）。
func TestPGBlob_TooLarge(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	big := make([]byte, MaxBlobSize+1) // 64 MiB + 1，全零
	if _, err := s.PutBlob(ctx, blobDigestOf(big), big, ""); !errors.Is(err, ErrBlobTooLarge) {
		t.Fatalf("oversize: want ErrBlobTooLarge, got %v", err)
	}

	// 恰好 64MiB 应当通过（不落库验证内容，只验证不报 TooLarge）。
	exact := make([]byte, MaxBlobSize)
	if _, err := s.PutBlob(ctx, blobDigestOf(exact), exact, ""); err != nil {
		t.Fatalf("exact-max blob rejected: %v", err)
	}
}

// TestPGBlob_GetNotFound 不存在或格式非法的 digest：
//   - 合法格式但无行 → ErrMarketplaceNotFound；
//   - 非法格式 → ErrBlobDigestMismatch。
func TestPGBlob_GetNotFound(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	absent := strings.Repeat("ab", 32) // 合法 64 位 hex，但从未上传
	if _, _, err := s.GetBlob(ctx, absent); !errors.Is(err, ErrMarketplaceNotFound) {
		t.Errorf("absent digest: want ErrMarketplaceNotFound, got %v", err)
	}
	if _, _, err := s.GetBlob(ctx, "nothex"); !errors.Is(err, ErrBlobDigestMismatch) {
		t.Errorf("malformed digest: want ErrBlobDigestMismatch, got %v", err)
	}
}

// TestPGBlob_GetByRelease 走完 submit → review → publish → PutBlob →
// GetBlobByRelease 完整链路，并覆盖可见性矩阵：
//   - 同 workspace 私有 release 可下载；
//   - 跨 workspace 私有包 → ErrMarketplaceNotFound（不泄露存在性）；
//   - public 包跨 workspace 可下载；
//   - release 存在但 blob 未上传 → ErrMarketplaceNotFound。
func TestPGBlob_GetByRelease(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// digest 带 sha256: 前缀 + 大写，覆盖版本 digest 归一化路径。
	privContent := []byte("private release payload")
	privDigest := "sha256:" + strings.ToUpper(blobDigestOf(privContent))
	pubContent := []byte("public release payload")
	pubDigest := "sha256:" + strings.ToUpper(blobDigestOf(pubContent))

	submit := func(ws, name, digest, visibility string) PackageVersion {
		t.Helper()
		v, err := s.Submit(ctx, SubmitRequest{
			WorkspaceID: ws, Name: name, Kind: "skill", Version: "1.0.0",
			Digest:    digest,
			Manifest:  Manifest{Version: "1.0.0", Digest: digest},
			Publisher: "alice", Visibility: visibility,
		})
		if err != nil {
			t.Fatalf("Submit %s: %v", name, err)
		}
		if err := s.Review(ctx, ReviewCommand{
			WorkspaceID: ws, VersionID: v.VersionID, Reviewer: "bob", Approved: true,
		}); err != nil {
			t.Fatalf("Review %s: %v", name, err)
		}
		if _, err := s.Publish(ctx, PublishCommand{
			WorkspaceID: ws, VersionID: v.VersionID, Channel: "stable",
		}); err != nil {
			t.Fatalf("Publish %s: %v", name, err)
		}
		return v
	}

	_ = submit("ws-owner", "priv", privDigest, "workspace")
	_ = submit("ws-owner", "pub", pubDigest, "public")

	if _, err := s.PutBlob(ctx, privDigest, privContent, "application/zip"); err != nil {
		t.Fatalf("PutBlob priv: %v", err)
	}
	if _, err := s.PutBlob(ctx, pubDigest, pubContent, ""); err != nil {
		t.Fatalf("PutBlob pub: %v", err)
	}

	// 同 workspace：私有 release 可下载。
	privReleases, err := s.ListReleases(ctx, "ws-owner")
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	var privRelID string
	for _, r := range privReleases {
		if strings.HasPrefix(r.VersionID, "ws-owner/priv@") {
			privRelID = r.ReleaseID
		}
	}
	if privRelID == "" {
		t.Fatalf("private release not found: %+v", privReleases)
	}
	meta, got, err := s.GetBlobByRelease(ctx, "ws-owner", privRelID)
	if err != nil {
		t.Fatalf("GetBlobByRelease same workspace: %v", err)
	}
	if string(got) != string(privContent) {
		t.Errorf("private release content mismatch: %q", got)
	}
	if meta.Digest != blobDigestOf(privContent) {
		t.Errorf("release blob digest = %q, want normalized %q", meta.Digest, blobDigestOf(privContent))
	}
	if meta.Size != int64(len(privContent)) || meta.ContentType != "application/zip" {
		t.Errorf("release blob meta malformed: %+v", meta)
	}

	// public 包跨 workspace 可下载。
	pubReleases, err := s.ListReleases(ctx, "ws-other")
	if err != nil {
		t.Fatalf("ListReleases ws-other: %v", err)
	}
	if len(pubReleases) != 1 {
		t.Fatalf("ws-other should see exactly the public release, got %+v", pubReleases)
	}
	_, gotPub, err := s.GetBlobByRelease(ctx, "ws-other", pubReleases[0].ReleaseID)
	if err != nil {
		t.Fatalf("GetBlobByRelease public cross-workspace: %v", err)
	}
	if string(gotPub) != string(pubContent) {
		t.Errorf("public release content mismatch: %q", gotPub)
	}

	// 私有包跨 workspace → NotFound（不泄露存在性）。
	if _, _, err := s.GetBlobByRelease(ctx, "ws-other", privRelID); !errors.Is(err, ErrMarketplaceNotFound) {
		t.Errorf("private release cross-workspace: want ErrMarketplaceNotFound, got %v", err)
	}

	// 不存在的 release → NotFound。
	if _, _, err := s.GetBlobByRelease(ctx, "ws-owner", "no-such-release"); !errors.Is(err, ErrMarketplaceNotFound) {
		t.Errorf("unknown release: want ErrMarketplaceNotFound, got %v", err)
	}

	// release 存在但 blob 未上传 → NotFound。
	blobless := submit("ws-owner", "blobless", "sha256:"+blobDigestOf([]byte("never uploaded")), "public")
	bloblessReleases, err := s.ListReleases(ctx, "ws-other")
	if err != nil {
		t.Fatalf("ListReleases after blobless publish: %v", err)
	}
	var bloblessRelID string
	for _, r := range bloblessReleases {
		if r.VersionID == blobless.VersionID {
			bloblessRelID = r.ReleaseID
		}
	}
	if bloblessRelID == "" {
		t.Fatalf("blobless release not found: %+v", bloblessReleases)
	}
	if _, _, err := s.GetBlobByRelease(ctx, "ws-other", bloblessRelID); !errors.Is(err, ErrMarketplaceNotFound) {
		t.Errorf("release without blob: want ErrMarketplaceNotFound, got %v", err)
	}
}
