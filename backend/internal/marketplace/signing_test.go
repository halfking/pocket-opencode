package marketplace

// signing_test.go — 签名链路纯内存单测（不连 PG、不依赖 testDSN）。
//
// 覆盖：canonical JSON（key 排序/确定性/无 HTML 转义/无换行）、SigningPayload
// 分隔符、ParsePublicKey 编码矩阵、SignVersion + VerifySignature 正反例、
// verifyWithKeyID 的 root 分支（root 不查库，可在 pool 为 nil 时安全测试）。
// publisher_signing_keys 表相关分支留给 PG 集成测试。

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// testSigningManifest 构造字段齐全的 manifest（含 HTML 敏感字符，用于验证
// canonical 输出不做转义）。
func testSigningManifest() Manifest {
	return Manifest{
		Version:       "1.2.3",
		Description:   "test <skill> & package",
		Digest:        "sha256:deadbeef",
		Licenses:      []string{"MIT"},
		Dependencies:  []Dependency{{PackageID: "ws/dep", Version: "0.9.0"}},
		Permissions:   []string{"fs.read", "net.fetch"},
		Compatibility: map[string]string{"os": "linux", "arch": "arm64"},
		Runtime:       "wasm",
	}
}

func generateTestKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	return pub, priv
}

// TestCanonicalizeManifest_KeyOrder 验证 object key 按字典序递归排序：
// 顶层字段与嵌套 Compatibility map 都必须有序，且为紧凑格式。
func TestCanonicalizeManifest_KeyOrder(t *testing.T) {
	got, err := CanonicalizeManifest(testSigningManifest())
	if err != nil {
		t.Fatalf("CanonicalizeManifest: %v", err)
	}
	// 顶层 sorted: compatibility < dependencies < description < digest <
	// licenses < permissions < runtime < version；Compatibility 内 a < os。
	want := `{"compatibility":{"arch":"arm64","os":"linux"},` +
		`"dependencies":[{"package_id":"ws/dep","version":"0.9.0"}],` +
		`"description":"test <skill> & package","digest":"sha256:deadbeef",` +
		`"licenses":["MIT"],"permissions":["fs.read","net.fetch"],` +
		`"runtime":"wasm","version":"1.2.3"}`
	if string(got) != want {
		t.Fatalf("canonical mismatch:\n got: %s\nwant: %s", got, want)
	}
}

// TestCanonicalizeManifest_Deterministic 验证同一 manifest 两次 canonical 化
// 字节完全一致（map 遍历序随机，若未排序则大概率抖动）。
func TestCanonicalizeManifest_Deterministic(t *testing.T) {
	m := testSigningManifest()
	m.Compatibility = map[string]string{
		"os": "linux", "arch": "arm64", "gpu": "none", "libc": "glibc", "vendor": "generic",
	}
	first, err := CanonicalizeManifest(m)
	if err != nil {
		t.Fatalf("first canonicalize: %v", err)
	}
	for i := 0; i < 8; i++ {
		again, err := CanonicalizeManifest(m)
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("canonical output not deterministic:\n %s\n %s", first, again)
		}
	}
}

// TestCanonicalizeManifest_NoHTMLEscape_NoNewline 验证输出不含 HTML 转义
// （\u003c 等）与换行符，特殊字符按原始 UTF-8 字节出现。
func TestCanonicalizeManifest_NoHTMLEscape_NoNewline(t *testing.T) {
	got, err := CanonicalizeManifest(testSigningManifest())
	if err != nil {
		t.Fatalf("CanonicalizeManifest: %v", err)
	}
	for _, bad := range []string{`\u003c`, `\u003e`, `\u0026`, "\n"} {
		if bytes.Contains(got, []byte(bad)) {
			t.Errorf("canonical output contains %q: %s", bad, got)
		}
	}
	if !bytes.Contains(got, []byte("<skill> & package")) {
		t.Errorf("canonical output lost raw special chars: %s", got)
	}
}

// TestSigningPayload 验证 payload = canonical ‖ 0x00 ‖ digest(ASCII)，
// 且 digest 变化会改变 payload（digest 单独入签，防 manifest.digest 偷换）。
func TestSigningPayload(t *testing.T) {
	m := testSigningManifest()
	digest := "sha256:cafe"
	canonical, err := CanonicalizeManifest(m)
	if err != nil {
		t.Fatalf("CanonicalizeManifest: %v", err)
	}

	payload, err := SigningPayload(m, digest)
	if err != nil {
		t.Fatalf("SigningPayload: %v", err)
	}
	if !bytes.HasPrefix(payload, canonical) {
		t.Fatalf("payload must start with canonical bytes")
	}
	if payload[len(canonical)] != 0x00 {
		t.Fatalf("payload separator at %d = %#x, want 0x00", len(canonical), payload[len(canonical)])
	}
	if string(payload[len(canonical)+1:]) != digest {
		t.Fatalf("payload suffix = %q, want %q", payload[len(canonical)+1:], digest)
	}
	if bytes.Count(payload, []byte{0x00}) != 1 {
		t.Fatalf("payload must contain exactly one 0x00 separator")
	}

	other, err := SigningPayload(m, "sha256:beef")
	if err != nil {
		t.Fatalf("SigningPayload(other digest): %v", err)
	}
	if bytes.Equal(payload, other) {
		t.Fatal("changing digest must change payload")
	}
}

// TestParsePublicKey 覆盖 base64 std/raw/url 与 hex 四种编码正例，以及
// 错误长度、非法编码、空串反例。64 hex 字符同时也是合法 base64 字母表，
// hex 正例顺带验证了"32 字节硬约束"能排除 base64 误解码分支。
func TestParsePublicKey(t *testing.T) {
	pub, priv := generateTestKey(t)
	_ = priv

	cases := []struct {
		name string
		raw  string
	}{
		{"base64 std", base64.StdEncoding.EncodeToString(pub)},
		{"base64 raw", base64.RawStdEncoding.EncodeToString(pub)},
		{"base64 url", base64.URLEncoding.EncodeToString(pub)},
		{"hex", hex.EncodeToString(pub)},
	}
	for _, tc := range cases {
		got, err := ParsePublicKey(tc.raw)
		if err != nil {
			t.Errorf("%s: ParsePublicKey: %v", tc.name, err)
			continue
		}
		if !bytes.Equal(got, pub) {
			t.Errorf("%s: got %x, want %x", tc.name, got, pub)
		}
	}

	// 错误长度：16 字节的合法 base64 也必须拒绝。
	short := make([]byte, 16)
	if _, err := rand.Read(short); err != nil {
		t.Fatalf("rand: %v", err)
	}
	for _, bad := range []struct {
		name string
		raw  string
	}{
		{"short base64", base64.StdEncoding.EncodeToString(short)},
		{"short hex", hex.EncodeToString(short)},
		{"not encoded", "!!!not-base64-or-hex!!!"},
		{"empty", ""},
		{"whitespace", "   "},
	} {
		if _, err := ParsePublicKey(bad.raw); !errors.Is(err, ErrInvalidPublicKey) {
			t.Errorf("%s: want ErrInvalidPublicKey, got %v", bad.name, err)
		}
	}
}

// TestSignAndVerify 覆盖签名/验签正例与各类篡改反例：改 digest、改 manifest
// 字段、换公钥、坏编码、空签名都必须得到 ErrSignatureInvalid。
func TestSignAndVerify(t *testing.T) {
	pub, priv := generateTestKey(t)
	otherPub, _ := generateTestKey(t)
	m := testSigningManifest()
	digest := "sha256:deadbeef"

	sig, err := SignVersion(priv, m, digest)
	if err != nil {
		t.Fatalf("SignVersion: %v", err)
	}
	if err := VerifySignature(pub, m, digest, sig); err != nil {
		t.Fatalf("VerifySignature(valid): %v", err)
	}

	// 篡改 digest。
	if err := VerifySignature(pub, m, "sha256:badc0de", sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("tampered digest: want ErrSignatureInvalid, got %v", err)
	}
	// 篡改 manifest 一个字段（omitempty 字段从有到无同样必须验签失败）。
	tampered := m
	tampered.Runtime = "python"
	if err := VerifySignature(pub, tampered, digest, sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("tampered manifest: want ErrSignatureInvalid, got %v", err)
	}
	tampered2 := m
	tampered2.Permissions = []string{"fs.read"}
	if err := VerifySignature(pub, tampered2, digest, sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("tampered manifest permissions: want ErrSignatureInvalid, got %v", err)
	}
	// 换一把公钥。
	if err := VerifySignature(otherPub, m, digest, sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("wrong public key: want ErrSignatureInvalid, got %v", err)
	}
	// 非法 base64。
	if err := VerifySignature(pub, m, digest, "!!!not-base64!!!"); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("garbage signature: want ErrSignatureInvalid, got %v", err)
	}
	// 空签名。
	if err := VerifySignature(pub, m, digest, ""); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("empty signature: want ErrSignatureInvalid, got %v", err)
	}
	// URL-safe base64 变体也应可验。
	if err := VerifySignature(pub, m, digest, base64.URLEncoding.EncodeToString(ed25519.Sign(priv, mustPayload(t, m, digest)))); err != nil {
		t.Errorf("url-encoded signature must verify: %v", err)
	}
}

func mustPayload(t *testing.T, m Manifest, digest string) []byte {
	t.Helper()
	payload, err := SigningPayload(m, digest)
	if err != nil {
		t.Fatalf("SigningPayload: %v", err)
	}
	return payload
}

// TestStoreVerifyWithRootKey 覆盖 verifyWithKeyID 的 root 分支（不查 DB，
// pool 可为 nil）：未配置 root 公钥 → ErrSigningUnavailable；配置后 root
// 签名验签通过；SetRootPublicKey("") 清除配置后回到 unavailable。
func TestStoreVerifyWithRootKey(t *testing.T) {
	ctx := context.Background()
	pub, priv := generateTestKey(t)
	m := testSigningManifest()
	digest := "sha256:deadbeef"

	s := NewStore(nil) // root 分支不触碰 pool，nil 即可
	if s.RootKeyConfigured() {
		t.Fatal("fresh store must not have root key configured")
	}
	sig, err := SignVersion(priv, m, digest)
	if err != nil {
		t.Fatalf("SignVersion: %v", err)
	}
	if err := s.verifyWithKeyID(ctx, "platform", RootKeyID, m, digest, sig); !errors.Is(err, ErrSigningUnavailable) {
		t.Fatalf("unconfigured root: want ErrSigningUnavailable, got %v", err)
	}
	if err := s.VerifySubmission(ctx, "platform", m, digest, sig, RootKeyID); !errors.Is(err, ErrSigningUnavailable) {
		t.Errorf("VerifySubmission unconfigured root: want ErrSigningUnavailable, got %v", err)
	}

	if err := s.SetRootPublicKey(base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatalf("SetRootPublicKey: %v", err)
	}
	if !s.RootKeyConfigured() {
		t.Fatal("root key must be configured after SetRootPublicKey")
	}
	if err := s.verifyWithKeyID(ctx, "platform", RootKeyID, m, digest, sig); err != nil {
		t.Fatalf("root verify: %v", err)
	}

	// 非 root key_id 走 publisher 表路径，pool 为 nil 时必须报 pool 未配置
	//（而不是 panic），该分支的完整行为由 PG 集成测试覆盖。
	if err := s.verifyWithKeyID(ctx, "alice", "k-2026", m, digest, sig); err == nil ||
		strings.Contains(err.Error(), ErrSigningUnavailable.Error()) {
		t.Errorf("non-root key with nil pool: want pool error, got %v", err)
	}

	// 清除配置 → disabled。
	if err := s.SetRootPublicKey(""); err != nil {
		t.Fatalf("clear root key: %v", err)
	}
	if s.RootKeyConfigured() {
		t.Fatal("root key must be cleared by empty SetRootPublicKey")
	}
	if err := s.verifyWithKeyID(ctx, "platform", RootKeyID, m, digest, sig); !errors.Is(err, ErrSigningUnavailable) {
		t.Errorf("cleared root: want ErrSigningUnavailable, got %v", err)
	}
}

// TestVerifySubmissionMissing 验证 VerifySubmission 对缺签名 / 缺 key_id
// 的 fail-closed 行为（在触碰任何信任锚之前就拒绝）。
func TestVerifySubmissionMissing(t *testing.T) {
	ctx := context.Background()
	s := NewStore(nil)
	m := testSigningManifest()

	if err := s.VerifySubmission(ctx, "alice", m, "d", "", RootKeyID); !errors.Is(err, ErrSignatureMissing) {
		t.Errorf("empty sig: want ErrSignatureMissing, got %v", err)
	}
	if err := s.VerifySubmission(ctx, "alice", m, "d", "aaaa", ""); !errors.Is(err, ErrSignatureMissing) {
		t.Errorf("empty keyID: want ErrSignatureMissing, got %v", err)
	}
}
