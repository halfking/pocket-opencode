package accjwt

// accjwt_test.go — the signing contract ACC's /api/v2/* surface enforces.
//
// The regression behind this package (measured against the live local ACC
// on 2026-10-22): ACC does NOT accept a static bearer token on that
// surface. A static token is answered with 401 "invalid bearer token",
// which is indistinguishable from "ACC is down" and cannot be acted on.
// Only an HS256-signed service JWT passes.
//
// These tests pin the wire contract (claim names, header, signature
// algorithm) and the failure modes, so a refactor cannot silently change
// the token into something ACC rejects.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMintProducesVerifiableHS256Token(t *testing.T) {
	const secret = "control-secret-abc"
	now := time.Unix(1_700_000_000, 0)

	tok, err := Mint(Options{
		Secret:          secret,
		Subject:         "pocketd:ac-30",
		Username:        "pocketd",
		TenantID:        "tenant-a",
		ServiceIdentity: true,
		Now:             now,
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}

	// Header must be {"alg":"HS256","typ":"JWT"} — ACC verifies the alg.
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var header map[string]any
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		t.Fatalf("parse header: %v", err)
	}
	if header["alg"] != "HS256" {
		t.Errorf("alg = %v, want HS256", header["alg"])
	}
	if header["typ"] != "JWT" {
		t.Errorf("typ = %v, want JWT", header["typ"])
	}

	// Signature must verify against the shared secret.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if parts[2] != want {
		t.Errorf("signature mismatch:\n got %s\nwant %s", parts[2], want)
	}

	// Claims must carry the names ACC reads.
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		t.Fatalf("parse claims: %v", err)
	}
	for key, want := range map[string]any{
		"sub":       "pocketd:ac-30",
		"username":  "pocketd",
		"tenant_id": "tenant-a",
		"type":      "agent",
		"role":      "agent",
	} {
		if got := claims[key]; got != want {
			t.Errorf("claim %q = %v, want %v", key, got, want)
		}
	}
	if claims["nbf"] == nil || claims["iat"] == nil || claims["exp"] == nil {
		t.Errorf("time claims missing: %v", claims)
	}
	// nbf is backdated to tolerate clock skew.
	if nbf, _ := claims["nbf"].(float64); int64(nbf) != now.Add(-30*time.Second).Unix() {
		t.Errorf("nbf = %v, want %v", nbf, now.Add(-30*time.Second).Unix())
	}
}

func TestMintRejectsMissingInputs(t *testing.T) {
	if _, err := Mint(Options{Subject: "x"}); !errors.Is(err, ErrNoSecret) {
		t.Errorf("no secret: err = %v, want ErrNoSecret", err)
	}
	if _, err := Mint(Options{Secret: "s"}); err == nil {
		t.Error("no subject: expected error")
	}
	// A non-service identity without a tenant is rejected by ACC with
	// "missing tenant"; fail at mint time so the operator sees which
	// configuration field is missing.
	if _, err := Mint(Options{Secret: "s", Subject: "x"}); err == nil {
		t.Error("no tenant for a non-service identity: expected error")
	}
	// A service identity does not need a tenant.
	if _, err := Mint(Options{Secret: "s", Subject: "x", ServiceIdentity: true}); err != nil {
		t.Errorf("service identity without tenant: %v", err)
	}
}

func TestSignerReturnsFreshTokens(t *testing.T) {
	s := NewSigner(Options{Secret: "s", Subject: "pocketd", ServiceIdentity: true})
	a, err := s.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	b, err := s.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if a == "" || b == "" {
		t.Fatal("signer returned an empty token")
	}
	// Two mints at the same second may legitimately be identical; the
	// point is that neither is empty and both parse as three segments.
	for _, tok := range []string{a, b} {
		if len(strings.Split(tok, ".")) != 3 {
			t.Errorf("token %q is not a 3-segment JWT", tok)
		}
	}
}

func TestNilSignerReportsMissingSecret(t *testing.T) {
	var s *Signer
	if _, err := s.Token(); !errors.Is(err, ErrNoSecret) {
		t.Errorf("nil signer: err = %v, want ErrNoSecret", err)
	}
}
