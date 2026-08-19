package auth

import (
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// sharedSecret is the 32-byte HS256 secret used by every project's
// IDENTITY_SHARED_SECRET in tests. Real deploys inject this via env.
const sharedSecret = "openpocket-test-shared-secret-32-bytes!!"

// legacySecret is the local openpocket POCKET_JWT_SECRET — used to test
// the backward-compatible legacy fallback path.
const legacySecret = "openpocket-test-legacy-secret-32-bytes!!"

// withSharedSecret sets IDENTITY_SHARED_SECRET (and clears the
// multi-issuer cache) for the duration of t.
func withSharedSecret(t *testing.T) {
	t.Helper()
	prev := os.Getenv(envIdentitySharedSecret)
	t.Setenv(envIdentitySharedSecret, sharedSecret)
	// Force a fresh load on next VerifyToken call.
	multiIssuerMu.Lock()
	multiIssuerLoaded = false
	multiIssuerDisabled = false
	multiIssuerMu.Unlock()
	t.Cleanup(func() {
		multiIssuerMu.Lock()
		multiIssuerLoaded = false
		multiIssuerDisabled = false
		multiIssuerMu.Unlock()
		_ = prev // t.Setenv handles restore
	})
}

// mintMultiIssuerForTest builds a token using identity-go's signer so
// the verifier accepts it without an extra hop through env config.
func mintMultiIssuerForTest(t *testing.T, issuer, subject, audience, secret string, ttl time.Duration, extra jwt.MapClaims) string {
	t.Helper()
	if extra == nil {
		extra = jwt.MapClaims{}
	}
	now := time.Now()
	extra["iss"] = issuer
	extra["sub"] = subject
	extra["aud"] = audience
	extra["iat"] = now.Unix()
	extra["exp"] = now.Add(ttl).Unix()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, extra)
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return signed
}

func newTestSigner(t *testing.T) *Signer {
	t.Helper()
	s, err := NewSigner(legacySecret, time.Hour)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return s
}

func TestVerifyToken_LegacyOnly_NoSharedSecret(t *testing.T) {
	// Ensure no shared-secret env: disable cache, clear env, set short-lived sentinel.
	multiIssuerMu.Lock()
	multiIssuerDisabled = true
	multiIssuerLoaded = false
	multiIssuerMu.Unlock()
	t.Cleanup(func() {
		multiIssuerMu.Lock()
		multiIssuerDisabled = false
		multiIssuerLoaded = false
		multiIssuerMu.Unlock()
	})
	os.Unsetenv(envIdentitySharedSecret)

	signer := newTestSigner(t)
	tok, err := signer.SignWithWorkspace("user-1", "admin", "ws_user-1")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	res, err := VerifyToken(signer, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.UserID != "user-1" || res.Role != "admin" || res.WorkspaceID != "ws_user-1" {
		t.Fatalf("unexpected: %+v", res)
	}
}

func TestVerifyToken_OwnIssuerSharedSecret(t *testing.T) {
	withSharedSecret(t)
	signer := newTestSigner(t)

	tok, err := signer.SignWithWorkspace("user-pocket", "tenant_admin", "ws_pocket")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	res, err := VerifyToken(signer, tok)
	if err != nil {
		t.Fatalf("verify own issuer: %v", err)
	}
	if res.UserID != "user-pocket" || res.WorkspaceID != "ws_pocket" || res.Issuer != "pocket" {
		t.Fatalf("unexpected: %+v", res)
	}
}

func TestVerifyToken_AcceptsForeignIssuer(t *testing.T) {
	withSharedSecret(t)
	signer := newTestSigner(t)

	tok := mintMultiIssuerForTest(t, "llm-gateway", "u-gw", "pocket-api", sharedSecret, time.Hour, jwt.MapClaims{
		"user_id":   "u-gw",
		"tenant_id": "tenant-gw",
		"roles":     []any{"tenant_admin"},
	})

	res, err := VerifyToken(signer, tok)
	if err != nil {
		t.Fatalf("verify foreign: %v", err)
	}
	if res.Issuer != "llm-gateway" || res.UserID != "u-gw" || res.Role != "tenant_admin" || res.TenantID != "tenant-gw" {
		t.Fatalf("unexpected foreign result: %+v", res)
	}
}

func TestVerifyToken_RejectsWrongAudience(t *testing.T) {
	withSharedSecret(t)
	signer := newTestSigner(t)

	tok := mintMultiIssuerForTest(t, "llm-gateway", "u", "llm-gateway-api", sharedSecret, time.Hour, jwt.MapClaims{
		"user_id": "u",
	})

	if _, err := VerifyToken(signer, tok); err == nil {
		t.Fatalf("expected rejection for wrong audience")
	}
}

func TestVerifyToken_RejectsExpired(t *testing.T) {
	withSharedSecret(t)
	signer := newTestSigner(t)

	tok := mintMultiIssuerForTest(t, "pocket", "u", "pocket-api", sharedSecret, -1*time.Minute, nil)

	if _, err := VerifyToken(signer, tok); err == nil {
		t.Fatalf("expected rejection for expired token")
	}
}

func TestVerifyToken_RejectsAsmIssuer(t *testing.T) {
	withSharedSecret(t)
	signer := newTestSigner(t)

	// ai-session-manager must never be a recognized identity provider.
	tok := mintMultiIssuerForTest(t, "asm", "u-asm", "pocket-api", sharedSecret, time.Hour, jwt.MapClaims{
		"user_id": "u-asm",
	})

	if _, err := VerifyToken(signer, tok); err == nil {
		t.Fatalf("expected rejection for asm issuer (allowlist forbids)")
	}
}

func TestVerifyToken_RejectsWrongSecret(t *testing.T) {
	withSharedSecret(t)
	signer := newTestSigner(t)

	tok := mintMultiIssuerForTest(t, "pocket", "u", "pocket-api", "rogue-secret-not-in-allowlist", time.Hour, nil)

	if _, err := VerifyToken(signer, tok); err == nil {
		t.Fatalf("expected rejection for token signed with wrong secret")
	}
}

func TestVerifyToken_RejectsEmptyToken(t *testing.T) {
	signer := newTestSigner(t)
	if _, err := VerifyToken(signer, ""); err == nil {
		t.Fatalf("expected error for empty token")
	}
}

func TestVerifyToken_AcceptsLegacyTokenEvenWithSharedSecret(t *testing.T) {
	withSharedSecret(t)
	signer := newTestSigner(t)

	// A token signed with the legacy (per-repo) secret should NOT pass
	// identity-go's allowlist (it has a different secret), so the legacy
	// fallback path inside VerifyToken must rescue it.
	tok, err := signer.SignWithWorkspace("user-legacy", "user", "ws_legacy")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	res, err := VerifyToken(signer, tok)
	if err != nil {
		t.Fatalf("expected legacy fallback to succeed: %v", err)
	}
	if res.UserID != "user-legacy" {
		t.Fatalf("legacy fallback returned wrong user: %+v", res)
	}
}