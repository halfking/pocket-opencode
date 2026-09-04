package marketplace

// signing_pg_test.go — 签名强制闸门的 PG 集成测试。
//
// 覆盖 ADR §4.4 的两态语义：
//   - root 公钥未配置 → Submit/Publish 行为与既有流程完全一致（仅记录）；
//   - root 公钥已配置 → Submit 必须携带可验签的签名，Publish 在行锁内重验
//     签名与依赖可解析，失败一律拒绝（fail-closed）。
//
// 需要 POCKET_TEST_POSTGRES_DSN；未设置时自动 skip（harness 同 store_test.go）。

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
)

// mustKeypair 生成测试用 ed25519 密钥对。
func mustKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	return pub, priv
}

// mustRootKey 启用 store 的签名强制（root 公钥），返回 root 私钥供签名。
func mustRootKey(t *testing.T, s *Store) ed25519.PrivateKey {
	t.Helper()
	pub, priv := mustKeypair(t)
	if err := s.SetRootPublicKey(fmt.Sprintf("%x", pub)); err != nil {
		t.Fatalf("SetRootPublicKey: %v", err)
	}
	return priv
}

// submitSigned 走完 submit → review(approved)，返回 versionID。
func submitSigned(t *testing.T, s *Store, ws, name, version string, m Manifest, digest, sig, keyID string) string {
	t.Helper()
	v, err := s.Submit(context.Background(), SubmitRequest{
		WorkspaceID: ws, Name: name, Kind: "skill", Version: version,
		Digest: digest, Manifest: m, Publisher: "alice",
		Signature: sig, SigningKeyID: keyID,
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := s.Review(context.Background(), ReviewCommand{
		WorkspaceID: ws, VersionID: v.VersionID, Reviewer: "bob", Approved: true,
	}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	return v.VersionID
}

// TestPGSigning_DisabledByDefault 未配置 root 公钥时，无签名提交/发布必须照常
// 成功——"仅记录"语义不阻断既有流程。
func TestPGSigning_DisabledByDefault(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if s.RootKeyConfigured() {
		t.Fatal("fresh store must not have root key configured")
	}
	v, err := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "unsigned", Kind: "skill", Version: "1.0.0",
		Digest: "d", Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "alice",
	})
	if err != nil {
		t.Fatalf("unsigned submit must succeed when signing disabled: %v", err)
	}
	_ = s.Review(ctx, ReviewCommand{WorkspaceID: "ws-1", VersionID: v.VersionID, Reviewer: "r", Approved: true})
	if _, err := s.Publish(ctx, PublishCommand{WorkspaceID: "ws-1", VersionID: v.VersionID}); err != nil {
		t.Fatalf("publish unsigned must succeed when signing disabled: %v", err)
	}
}

// TestPGSigning_EnforcedSubmit root 公钥已配置时：
// 无签名 → ErrSignatureMissing；伪造签名 → ErrSignatureInvalid；
// publisher key 合法签名 → 成功；密钥吊销后 → ErrSigningKeyRevoked。
func TestPGSigning_EnforcedSubmit(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustRootKey(t, s)

	m := Manifest{Version: "1.0.0", Digest: "sha256:abc", Permissions: []string{"fs.read"}}

	// 无签名。
	_, err := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "nosig", Kind: "skill", Version: "1.0.0",
		Digest: "sha256:abc", Manifest: m, Publisher: "alice",
	})
	if !errors.Is(err, ErrSignatureMissing) {
		t.Fatalf("unsigned submit: want ErrSignatureMissing, got %v", err)
	}

	// 注册 publisher key 后伪造签名。
	pub, priv := mustKeypair(t)
	if err := s.RegisterPublisherKey(ctx, "alice", "k1", fmt.Sprintf("%x", pub), AlgEd25519); err != nil {
		t.Fatalf("RegisterPublisherKey: %v", err)
	}
	_, err = s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "forged", Kind: "skill", Version: "1.0.0",
		Digest: "sha256:abc", Manifest: m, Publisher: "alice",
		Signature: "AAAA", SigningKeyID: "k1",
	})
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("forged submit: want ErrSignatureInvalid, got %v", err)
	}

	// 合法签名。
	sig, err := SignVersion(priv, m, "sha256:abc")
	if err != nil {
		t.Fatalf("SignVersion: %v", err)
	}
	if _, err := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "signed", Kind: "skill", Version: "1.0.0",
		Digest: "sha256:abc", Manifest: m, Publisher: "alice",
		Signature: sig, SigningKeyID: "k1",
	}); err != nil {
		t.Fatalf("valid signed submit: %v", err)
	}

	// 吊销后再提交（新版本名避开唯一键）→ ErrSigningKeyRevoked。
	if err := s.RevokePublisherKey(ctx, "alice", "k1"); err != nil {
		t.Fatalf("RevokePublisherKey: %v", err)
	}
	_, err = s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "after-revoke", Kind: "skill", Version: "1.0.0",
		Digest: "sha256:abc", Manifest: m, Publisher: "alice",
		Signature: sig, SigningKeyID: "k1",
	})
	if !errors.Is(err, ErrSigningKeyRevoked) {
		t.Fatalf("revoked-key submit: want ErrSigningKeyRevoked, got %v", err)
	}
}

// TestPGSigning_SubmitDuplicateKeyRejected 重复注册同一 (publisher, key_id)
// 必须以 conflict 拒绝，防止静默换钥。
func TestPGSigning_SubmitDuplicateKeyRejected(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pub, _ := mustKeypair(t)
	raw := fmt.Sprintf("%x", pub)
	if err := s.RegisterPublisherKey(ctx, "alice", "k1", raw, AlgEd25519); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := s.RegisterPublisherKey(ctx, "alice", "k1", raw, AlgEd25519); !errors.Is(err, ErrMarketplaceConflict) {
		t.Fatalf("duplicate register: want ErrMarketplaceConflict, got %v", err)
	}
}

// TestPGSigning_PublishReverify 提交时签名有效、发布前密钥被吊销 → 发布闸门
// 重验必须拒绝（conflict 包装）。这是行锁内 fail-closed 的核心场景。
func TestPGSigning_PublishReverify(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	mustRootKey(t, s)

	pub, priv := mustKeypair(t)
	if err := s.RegisterPublisherKey(ctx, "alice", "k1", fmt.Sprintf("%x", pub), AlgEd25519); err != nil {
		t.Fatalf("RegisterPublisherKey: %v", err)
	}
	m := Manifest{Version: "1.0.0", Digest: "sha256:abc"}
	sig, _ := SignVersion(priv, m, "sha256:abc")

	versionID := submitSigned(t, s, "ws-1", "reverify", "1.0.0", m, "sha256:abc", sig, "k1")

	if err := s.RevokePublisherKey(ctx, "alice", "k1"); err != nil {
		t.Fatalf("RevokePublisherKey: %v", err)
	}
	_, err := s.Publish(ctx, PublishCommand{WorkspaceID: "ws-1", VersionID: versionID})
	if !errors.Is(err, ErrMarketplaceConflict) || !errors.Is(err, ErrSigningKeyRevoked) {
		t.Fatalf("publish after revoke: want conflict wrapping ErrSigningKeyRevoked, got %v", err)
	}
}

// TestPGSigning_PublishDependencies root 公钥已配置时发布闸门要求依赖可解析：
// 缺失依赖 → conflict 聚合错误；依赖已提交（精确版本）→ 成功。
func TestPGSigning_PublishDependencies(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	root := mustRootKey(t, s)

	// 依赖包 lib-a@2.0.0：签名后提交，留在 draft 也应可被解析器看到
	// （依赖解析只读行，不要求依赖本身已发布——发布语义由顶层版本闸门把关）。
	libManifest := Manifest{Version: "2.0.0", Digest: "sha256:lib"}
	libSig, _ := SignVersion(root, libManifest, "sha256:lib")
	if _, err := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "lib-a", Kind: "skill", Version: "2.0.0",
		Digest: "sha256:lib", Manifest: libManifest, Publisher: "alice",
		Signature: libSig, SigningKeyID: RootKeyID,
	}); err != nil {
		t.Fatalf("submit lib-a: %v", err)
	}

	// 主包依赖 lib-a@9.9.9（不存在）→ 发布被拒且错误聚合可判别。
	badManifest := Manifest{Version: "1.0.0", Digest: "sha256:app",
		Dependencies: []Dependency{{PackageID: "ws-1/lib-a", Version: "9.9.9"}}}
	badSig, _ := SignVersion(root, badManifest, "sha256:app")
	badID := submitSigned(t, s, "ws-1", "app-bad", "1.0.0", badManifest, "sha256:app", badSig, RootKeyID)
	_, err := s.Publish(ctx, PublishCommand{WorkspaceID: "ws-1", VersionID: badID})
	if !errors.Is(err, ErrMarketplaceConflict) || !errors.Is(err, ErrDependenciesUnresolved) {
		t.Fatalf("publish missing dep: want conflict wrapping ErrDependenciesUnresolved, got %v", err)
	}

	// 主包依赖 lib-a@2.0.0（存在）→ 发布成功。
	goodManifest := Manifest{Version: "1.0.0", Digest: "sha256:app",
		Dependencies: []Dependency{{PackageID: "ws-1/lib-a", Version: "2.0.0"}}}
	goodSig, _ := SignVersion(root, goodManifest, "sha256:app")
	goodID := submitSigned(t, s, "ws-1", "app-good", "1.0.0", goodManifest, "sha256:app", goodSig, RootKeyID)
	if _, err := s.Publish(ctx, PublishCommand{WorkspaceID: "ws-1", VersionID: goodID}); err != nil {
		t.Fatalf("publish with resolvable dep: %v", err)
	}
}

// TestPGSigning_VerifyVersionPaths 直接驱动 VerifyVersion 的四条路径：
// 未签名 / publisher key 有效 / root key 有效（配置前后）/ 密钥吊销。
func TestPGSigning_VerifyVersionPaths(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// 未签名版本。
	plain, err := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "plain", Kind: "skill", Version: "1.0.0",
		Digest: "d", Manifest: Manifest{Version: "1.0.0", Digest: "d"}, Publisher: "alice",
	})
	if err != nil {
		t.Fatalf("submit plain: %v", err)
	}
	if err := s.VerifyVersion(ctx, plain.VersionID); !errors.Is(err, ErrSignatureMissing) {
		t.Fatalf("plain verify: want ErrSignatureMissing, got %v", err)
	}

	// publisher key 签名。
	pub, priv := mustKeypair(t)
	if err := s.RegisterPublisherKey(ctx, "alice", "k1", fmt.Sprintf("%x", pub), AlgEd25519); err != nil {
		t.Fatalf("RegisterPublisherKey: %v", err)
	}
	m := Manifest{Version: "2.0.0", Digest: "sha256:m"}
	sig, _ := SignVersion(priv, m, "sha256:m")
	signed, err := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "pk-signed", Kind: "skill", Version: "2.0.0",
		Digest: "sha256:m", Manifest: m, Publisher: "alice",
		Signature: sig, SigningKeyID: "k1",
	})
	if err != nil {
		t.Fatalf("submit signed: %v", err)
	}
	if err := s.VerifyVersion(ctx, signed.VersionID); err != nil {
		t.Fatalf("publisher-key verify: %v", err)
	}

	// root 签名但 root 未配置 → ErrSigningUnavailable。
	rootM := Manifest{Version: "3.0.0", Digest: "sha256:r"}
	rootPub, rootPriv := mustKeypair(t)
	rootSig, _ := SignVersion(rootPriv, rootM, "sha256:r")
	rootVer, err := s.Submit(ctx, SubmitRequest{
		WorkspaceID: "ws-1", Name: "root-signed", Kind: "skill", Version: "3.0.0",
		Digest: "sha256:r", Manifest: rootM, Publisher: "platform",
		Signature: rootSig, SigningKeyID: RootKeyID,
	})
	if err != nil {
		t.Fatalf("submit root-signed (enforcement off): %v", err)
	}
	if err := s.VerifyVersion(ctx, rootVer.VersionID); !errors.Is(err, ErrSigningUnavailable) {
		t.Fatalf("root verify without root key: want ErrSigningUnavailable, got %v", err)
	}

	// 配置 root 公钥后同一版本可通过；吊销 publisher key 后转 ErrSigningKeyRevoked。
	if err := s.SetRootPublicKey(fmt.Sprintf("%x", rootPub)); err != nil {
		t.Fatalf("SetRootPublicKey: %v", err)
	}
	if err := s.VerifyVersion(ctx, rootVer.VersionID); err != nil {
		t.Fatalf("root verify with root key: %v", err)
	}

	if err := s.RevokePublisherKey(ctx, "alice", "k1"); err != nil {
		t.Fatalf("RevokePublisherKey: %v", err)
	}
	if err := s.VerifyVersion(ctx, signed.VersionID); !errors.Is(err, ErrSigningKeyRevoked) {
		t.Fatalf("verify after revoke: want ErrSigningKeyRevoked, got %v", err)
	}
}

// TestPGSigning_PublisherKeyListLifecycle 覆盖 ListPublisherKeys（HTTP 列表
// 端点的 store 基座，ADR §10）：注册多把密钥 → 列表按序返回（公钥回传为
// base64）→ 吊销后 status/revoked_at 正确 → 未知 publisher 返回空切片。
func TestPGSigning_PublisherKeyListLifecycle(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pub1, _ := mustKeypair(t)
	pub2, _ := mustKeypair(t)

	if err := s.RegisterPublisherKey(ctx, "alice", "k1", base64.StdEncoding.EncodeToString(pub1), AlgEd25519); err != nil {
		t.Fatalf("register k1: %v", err)
	}
	if err := s.RegisterPublisherKey(ctx, "alice", "k2", base64.StdEncoding.EncodeToString(pub2), AlgEd25519); err != nil {
		t.Fatalf("register k2: %v", err)
	}

	keys, err := s.ListPublisherKeys(ctx, "alice")
	if err != nil {
		t.Fatalf("ListPublisherKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("want 2 keys, got %d: %+v", len(keys), keys)
	}
	if keys[0].KeyID != "k1" || keys[1].KeyID != "k2" {
		t.Errorf("keys not ordered by key_id: %+v", keys)
	}
	for _, k := range keys {
		if k.PublisherID != "alice" || k.Status != "active" || k.Alg != AlgEd25519 {
			t.Errorf("key %s malformed: %+v", k.KeyID, k)
		}
		if k.RevokedAt != nil {
			t.Errorf("active key %s has revoked_at: %+v", k.KeyID, k)
		}
	}
	want1 := base64.StdEncoding.EncodeToString(pub1)
	if keys[0].PublicKey != want1 {
		t.Errorf("k1 public key roundtrip: want %q, got %q", want1, keys[0].PublicKey)
	}

	// 吊销 k1：行保留，status 转 revoked 且 revoked_at 落值。
	if err := s.RevokePublisherKey(ctx, "alice", "k1"); err != nil {
		t.Fatalf("RevokePublisherKey: %v", err)
	}
	keys, err = s.ListPublisherKeys(ctx, "alice")
	if err != nil {
		t.Fatalf("ListPublisherKeys after revoke: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("revoked row must be retained: got %d keys", len(keys))
	}
	if keys[0].KeyID != "k1" || keys[0].Status != "revoked" || keys[0].RevokedAt == nil {
		t.Errorf("k1 not marked revoked: %+v", keys[0])
	}
	if keys[1].Status != "active" || keys[1].RevokedAt != nil {
		t.Errorf("k2 must stay active: %+v", keys[1])
	}

	// 未知 publisher → 空切片，无错误。
	empty, err := s.ListPublisherKeys(ctx, "nobody")
	if err != nil || len(empty) != 0 {
		t.Errorf("unknown publisher: want empty, got %d keys err=%v", len(empty), err)
	}
}
