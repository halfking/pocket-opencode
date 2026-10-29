package identity

// binding_test.go — 工作区成员绑定断言的签名与验证（2026-10-29）。
//
// ⚠ 这些是**纯逻辑**测试（不依赖 PG）：断言的签发、验签、边界判定
//   都在内存里完成。store 侧的成员资格判定另有集成测试（需 PG）。
//
// 📌 每条用例都要能回答一个具体的越权假设：
//   「攻击者能伪造/重放/挪用一份断言吗？」

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kaixuan/identity-go/token"
)

func testSigner(t *testing.T) *BindingSigner {
	t.Helper()
	secret := []byte(strings.Repeat("k", 32))
	signer, err := NewBindingSignerWithSecret(secret)
	if err != nil {
		t.Fatalf("NewBindingSignerWithSecret: %v", err)
	}
	return signer
}

func testMember() Member {
	return Member{
		WorkspaceID: "ws_alice",
		UserID:      "u_alice",
		Role:        RoleOwner,
	}
}

// TestBindingRoundTrip — 签出来的断言能被验回来，字段一致。
func TestBindingRoundTrip(t *testing.T) {
	signer := testSigner(t)
	signed, assertion, err := signer.Mint(testMember(), "tenant_a", time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	got, err := signer.Verify(signed)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UserID != assertion.UserID || got.WorkspaceID != assertion.WorkspaceID {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, assertion)
	}
	if got.TenantID != "tenant_a" {
		t.Fatalf("tenant = %q want tenant_a", got.TenantID)
	}
	if got.Role != BindingRoleOwner {
		t.Fatalf("role = %q want owner", got.Role)
	}
	if got.Issuer != BindingIssuer || got.Audience != BindingAudience {
		t.Fatalf("issuer/audience = %q/%q want %q/%q", got.Issuer, got.Audience, BindingIssuer, BindingAudience)
	}
}

// TestBindingRejectsForeignSignature — 另一把密钥签的断言验不过。
//
// ⚠ 这是「伪造」的核心防线：断言的价值全在签名上。
func TestBindingRejectsForeignSignature(t *testing.T) {
	foreign, err := NewBindingSignerWithSecret([]byte(strings.Repeat("x", 32)))
	if err != nil {
		t.Fatalf("NewBindingSignerWithSecret: %v", err)
	}
	signed, _, err := foreign.Mint(testMember(), "tenant_a", time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if _, err := testSigner(t).Verify(signed); !errors.Is(err, ErrBindingInvalid) {
		t.Fatalf("foreign signature accepted: err = %v", err)
	}
}

// TestBindingRejectsTamperedPayload — 改载荷就验不过。
func TestBindingRejectsTamperedPayload(t *testing.T) {
	signer := testSigner(t)
	signed, _, err := signer.Mint(testMember(), "tenant_a", time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	parts := strings.Split(signed, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}
	// 换一个 workspace 声明，但保留原签名。
	claims := &token.Claims{}
	if err := claims.UnmarshalJSON([]byte(`{"iss":"pocket","sub":"u_alice","aud":"workspace-binding","workspace_id":"ws_victim"}`)); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	tampered := parts[0] + "." + parts[1] + "." + parts[2]
	// 直接改 payload 段（换 base64 载荷），签名段不动。
	_ = tampered
	raw := decodeSegment(t, parts[1])
	raw = strings.Replace(raw, `"ws_alice"`, `"ws_victim"`, 1)
	forged := parts[0] + "." + encodeSegment(t, raw) + "." + parts[2]
	if _, err := signer.Verify(forged); !errors.Is(err, ErrBindingInvalid) {
		t.Fatalf("tampered payload accepted: err = %v", err)
	}
}

// TestBindingRejectsWrongAudience — 别的受众的断言不能当绑定用。
//
// ⚠ 断言证明的是「此主体在此工作区有成员资格」，
//
//	它不该被转发去当别的服务的通行证。
func TestBindingRejectsWrongAudience(t *testing.T) {
	secret := []byte(strings.Repeat("k", 32))
	signer, err := NewBindingSignerWithSecret(secret)
	if err != nil {
		t.Fatalf("NewBindingSignerWithSecret: %v", err)
	}
	issuer := token.Issuer{Name: BindingIssuer, Secret: secret}
	signed, err := token.SignHS256(issuer, &token.Claims{
		Subject:  "u_alice",
		UserID:   "u_alice",
		Audience: "some-other-service",
		Extra:    map[string]any{"workspace_id": "ws_alice"},
	}, time.Minute)
	if err != nil {
		t.Fatalf("SignHS256: %v", err)
	}
	if _, err := signer.Verify(signed); !errors.Is(err, ErrBindingInvalid) {
		t.Fatalf("wrong audience accepted: err = %v", err)
	}
}

// TestBindingExpired — 过期断言被拒。
func TestBindingExpired(t *testing.T) {
	signer := testSigner(t)
	// 签一个**已经过期**的断言（生效时刻在过去 2 分钟，失效时刻在过去 1 分钟）。
	now := time.Now()
	expired, _, err := signer.MintWithExpiry(testMember(), "tenant_a",
		now.Add(-2*time.Minute), now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("MintWithExpiry: %v", err)
	}
	if _, err := signer.Verify(expired); !errors.Is(err, ErrBindingExpired) {
		t.Fatalf("expired binding accepted: err = %v", err)
	}
	// 一个**尚未生效**的断言也必须被拒（且不能报成「过期」——
	// 两者是不同的问题：一个是时间没到，一个是时间过了）。
	future, _, err := signer.MintWithExpiry(testMember(), "tenant_a",
		now.Add(time.Minute), now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("MintWithExpiry (future): %v", err)
	}
	if _, err := signer.Verify(future); err == nil {
		t.Fatal("not-yet-valid binding accepted")
	} else if errors.Is(err, ErrBindingExpired) {
		t.Fatalf("not-yet-valid reported as expired: %v", err)
	}
}

// TestBindingScopeMismatch — 断言是真的，但不覆盖请求的工作区时必须拒绝。
//
// ⚠ 这是「挪用」防线：拿自己在 ws_a 的有效断言去进 ws_b。
func TestBindingScopeMismatch(t *testing.T) {
	signer := testSigner(t)
	signed, _, err := signer.Mint(testMember(), "tenant_a", time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if _, err := signer.VerifyFor(signed, "u_alice", "ws_alice"); err != nil {
		t.Fatalf("matching scope rejected: %v", err)
	}
	if _, err := signer.VerifyFor(signed, "u_alice", "ws_other"); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("workspace mismatch accepted: err = %v", err)
	}
	if _, err := signer.VerifyFor(signed, "u_mallory", "ws_alice"); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("user mismatch accepted: err = %v", err)
	}
}

// TestBindingRequiresBothIdentifiers — 只匹配一个标识不够。
//
// ⚠ 同一个人可以加入多个工作区；同名的账号可以在不同租户下。
//
//	只匹配 user_id 会让 A 的断言进到 A 的另一个工作区；
//	只匹配 workspace_id 会让 A 的断言被别的租户同名工作区接受。
func TestBindingRequiresBothIdentifiers(t *testing.T) {
	signer := testSigner(t)
	signed, _, err := signer.Mint(testMember(), "tenant_a", time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	assertion, err := signer.Verify(signed)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if assertion.Covers("u_alice", "") {
		t.Fatal("empty workspace matched")
	}
	if assertion.Covers("", "ws_alice") {
		t.Fatal("empty user matched")
	}
	if !assertion.Covers(" u_alice ", " ws_alice ") {
		t.Fatal("surrounding whitespace should be trimmed")
	}
}

// TestBindingNilSignerFailsClosed — 没有签名器时一律失败，不得放行。
//
// ⚠ 「无法验证」必须表现为拒绝，不能表现为通过。
func TestBindingNilSignerFailsClosed(t *testing.T) {
	var signer *BindingSigner
	if _, _, err := signer.Mint(testMember(), "tenant_a", time.Minute); err == nil {
		t.Fatal("nil signer minted an assertion")
	}
	if _, err := signer.Verify("anything"); err == nil {
		t.Fatal("nil signer verified an assertion")
	}
	if _, err := signer.VerifyFor("anything", "u", "ws"); err == nil {
		t.Fatal("nil signer authorized a scope")
	}
}

// TestBindingRejectsEmptySubject — 空用户/空工作区签不出来。
func TestBindingRejectsEmptySubject(t *testing.T) {
	signer := testSigner(t)
	if _, _, err := signer.Mint(Member{WorkspaceID: "ws_a"}, "t", time.Minute); err == nil {
		t.Fatal("minted an assertion without a user")
	}
	if _, _, err := signer.Mint(Member{UserID: "u_a"}, "t", time.Minute); err == nil {
		t.Fatal("minted an assertion without a workspace")
	}
}

func decodeSegment(t *testing.T, seg string) string {
	t.Helper()
	raw, err := base64RawURLDecode(seg)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	return raw
}

func encodeSegment(t *testing.T, raw string) string {
	t.Helper()
	return base64RawURLEncode([]byte(raw))
}

// base64RawURLDecode / base64RawURLEncode are local test helpers so the
// tampering test can rewrite a payload segment without pulling in
// production helpers that would only exist for the test.
func base64RawURLDecode(s string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func base64RawURLEncode(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}
