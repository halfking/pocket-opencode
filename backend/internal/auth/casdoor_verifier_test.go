package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// jwksServer serves a JWKS document for a set of RSA keys.
//
// The document is served from an httptest server so the test exercises
// the real fetch path: the key source reaches out over HTTP, caches what
// it read and refetches on a rotation.
type jwksServer struct {
	server *httptest.Server
	keys   map[string]*rsa.PrivateKey
	// hits counts the fetches, so a test can tell a cached read from a
	// refetch.
	hits int
}

func newJWKSServer(t *testing.T, keys map[string]*rsa.PrivateKey) *jwksServer {
	t.Helper()
	j := &jwksServer{keys: keys}
	j.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		j.hits++
		type jwk struct {
			Kty string `json:"kty"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		}
		set := struct {
			Keys []jwk `json:"keys"`
		}{}
		for kid, k := range j.keys {
			set.Keys = append(set.Keys, jwk{
				Kty: "RSA",
				Use: "sig",
				Alg: "RS256",
				Kid: kid,
				N:   base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
				E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(j.server.Close)
	return j
}

// mintCasdoorToken signs a token with one of the server's keys.
func mintCasdoorToken(t *testing.T, key *rsa.PrivateKey, kid, issuer, audience, subject string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":  subject,
		"name": "Test User",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iat":  time.Now().Unix(),
	}
	if issuer != "" {
		claims["iss"] = issuer
	}
	if audience != "" {
		claims["aud"] = audience
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	raw, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return raw
}

func newTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return k
}

// A token issued by Casdoor is accepted by VerifyToken.
func TestVerifyTokenAcceptsCasdoorToken(t *testing.T) {
	key := newTestRSAKey(t)
	jwks := newJWKSServer(t, map[string]*rsa.PrivateKey{"key-1": key})
	issuer := "https://casdoor.example.com"
	verifier := NewCasdoorVerifier(jwks.server.URL, issuer, "openpocket")

	raw := mintCasdoorToken(t, key, "key-1", issuer, "openpocket", "u-casdoor")

	res, err := VerifyTokenWithCasdoor(verifier, nil, raw)
	if err != nil {
		t.Fatalf("VerifyTokenWithCasdoor: %v", err)
	}
	if res.UserID != "u-casdoor" {
		t.Errorf("user id = %q, want u-casdoor", res.UserID)
	}
	if res.Issuer != issuer {
		t.Errorf("issuer = %q, want %q", res.Issuer, issuer)
	}
}

// A token signed by a key Casdoor does not publish is refused.
func TestVerifyTokenRejectsUnknownCasdoorKey(t *testing.T) {
	key := newTestRSAKey(t)
	other := newTestRSAKey(t)
	jwks := newJWKSServer(t, map[string]*rsa.PrivateKey{"key-1": key})
	issuer := "https://casdoor.example.com"
	verifier := NewCasdoorVerifier(jwks.server.URL, issuer, "openpocket")

	raw := mintCasdoorToken(t, other, "key-unknown", issuer, "openpocket", "u-casdoor")

	if _, err := VerifyTokenWithCasdoor(verifier, nil, raw); err == nil {
		t.Fatal("expected a token signed by an unpublished key to be refused")
	}
}

// A token issued for another application is refused even though the
// signature is good: one Casdoor application's token is not another
// application's credential.
func TestVerifyTokenRejectsWrongAudience(t *testing.T) {
	key := newTestRSAKey(t)
	jwks := newJWKSServer(t, map[string]*rsa.PrivateKey{"key-1": key})
	issuer := "https://casdoor.example.com"
	verifier := NewCasdoorVerifier(jwks.server.URL, issuer, "openpocket")

	raw := mintCasdoorToken(t, key, "key-1", issuer, "some-other-app", "u-casdoor")

	if _, err := VerifyTokenWithCasdoor(verifier, nil, raw); err == nil {
		t.Fatal("expected a token issued for another application to be refused")
	}
}

// A token from another issuer is refused.
func TestVerifyTokenRejectsWrongIssuer(t *testing.T) {
	key := newTestRSAKey(t)
	jwks := newJWKSServer(t, map[string]*rsa.PrivateKey{"key-1": key})
	verifier := NewCasdoorVerifier(jwks.server.URL, "https://casdoor.example.com", "openpocket")

	raw := mintCasdoorToken(t, key, "key-1", "https://evil.example.com", "openpocket", "u-casdoor")

	if _, err := VerifyTokenWithCasdoor(verifier, nil, raw); err == nil {
		t.Fatal("expected a token from another issuer to be refused")
	}
}

// An expired token is refused.
func TestVerifyTokenRejectsExpiredCasdoorToken(t *testing.T) {
	key := newTestRSAKey(t)
	jwks := newJWKSServer(t, map[string]*rsa.PrivateKey{"key-1": key})
	issuer := "https://casdoor.example.com"
	verifier := NewCasdoorVerifier(jwks.server.URL, issuer, "openpocket")

	claims := jwt.MapClaims{
		"sub": "u-casdoor",
		"iss": issuer,
		"aud": "openpocket",
		"exp": time.Now().Add(-time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "key-1"
	raw, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := VerifyTokenWithCasdoor(verifier, nil, raw); err == nil {
		t.Fatal("expected an expired token to be refused")
	}
}

// The algorithm confusion attack: a token that asks for HS256 must not be
// verified with the public key as a shared secret.
func TestVerifyTokenRejectsHS256CasdoorToken(t *testing.T) {
	key := newTestRSAKey(t)
	jwks := newJWKSServer(t, map[string]*rsa.PrivateKey{"key-1": key})
	issuer := "https://casdoor.example.com"
	verifier := NewCasdoorVerifier(jwks.server.URL, issuer, "openpocket")

	claims := jwt.MapClaims{
		"sub": "u-casdoor",
		"iss": issuer,
		"aud": "openpocket",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = "key-1"
	// Signed with the public key's modulus as the "secret" — the shape
	// of the classic algorithm confusion attack.
	pubN := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	raw, err := tok.SignedString([]byte(pubN))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := VerifyTokenWithCasdoor(verifier, nil, raw); err == nil {
		t.Fatal("expected an HS256 token to be refused by the casdoor verifier")
	}
}

// The key set is cached, and a rotated certificate is picked up through
// the forced refresh.
func TestCasdoorJWKSRotationIsPickedUp(t *testing.T) {
	key1 := newTestRSAKey(t)
	key2 := newTestRSAKey(t)
	jwks := newJWKSServer(t, map[string]*rsa.PrivateKey{"key-1": key1})
	issuer := "https://casdoor.example.com"
	verifier := NewCasdoorVerifier(jwks.server.URL, issuer, "openpocket")

	raw1 := mintCasdoorToken(t, key1, "key-1", issuer, "openpocket", "u-1")
	if _, err := VerifyTokenWithCasdoor(verifier, nil, raw1); err != nil {
		t.Fatalf("first token: %v", err)
	}
	hitsAfterFirst := jwks.hits

	// The same token is served from the cache.
	if _, err := VerifyTokenWithCasdoor(verifier, nil, raw1); err != nil {
		t.Fatalf("second token: %v", err)
	}
	if jwks.hits != hitsAfterFirst {
		t.Errorf("expected the key set to be cached, fetches went from %d to %d",
			hitsAfterFirst, jwks.hits)
	}

	// The certificate is rotated: the new key is published under a new
	// kid, and the token signed with it must be accepted.
	jwks.keys = map[string]*rsa.PrivateKey{"key-2": key2}
	raw2 := mintCasdoorToken(t, key2, "key-2", issuer, "openpocket", "u-2")
	if _, err := VerifyTokenWithCasdoor(verifier, nil, raw2); err != nil {
		t.Fatalf("rotated token: %v", err)
	}
}

// The local session token keeps working: the Casdoor path is an addition,
// not a replacement.
func TestVerifyTokenStillAcceptsLocalSessionToken(t *testing.T) {
	signer, err := NewSigner("local-secret-that-is-at-least-32-bytes-long", time.Hour)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	raw, err := signer.SignWithWorkspace("u-local", "user", "ws-1")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	res, err := VerifyTokenWithCasdoor(nil, signer, raw)
	if err != nil {
		t.Fatalf("VerifyTokenWithCasdoor: %v", err)
	}
	if res.UserID != "u-local" {
		t.Errorf("user id = %q, want u-local", res.UserID)
	}
	if res.WorkspaceID != "ws-1" {
		t.Errorf("workspace id = %q, want ws-1", res.WorkspaceID)
	}
}

// A verifier without a JWKS URL accepts nothing — it is not a verifier
// that waves tokens through.
func TestCasdoorVerifierDisabledWithoutJWKS(t *testing.T) {
	v := NewCasdoorVerifier("", "https://casdoor.example.com", "openpocket")
	if v.Enabled() {
		t.Error("expected a verifier without a jwks url to be disabled")
	}
	if _, err := VerifyTokenWithCasdoor(v, nil, "whatever"); err == nil {
		t.Error("expected a disabled verifier to refuse")
	}
}

// An oversized JWKS document is refused as oversized, not decoded as if
// it were a truncated prefix.
//
// Without the extra-byte check a document past the limit would be cut
// short, and the cut-off JSON would fail to parse — the failure would
// look like a broken key set instead of an oversized response.
func TestCasdoorJWKSRejectsOversizedDocument(t *testing.T) {
	key := newTestRSAKey(t)
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A document larger than the limit.
		_, _ = w.Write(make([]byte, maxCasdoorJWKSDocBytes+1024))
	}))
	defer big.Close()

	verifier := NewCasdoorVerifier(big.URL, "https://casdoor.example.com", "openpocket")
	_, err := verifier.Verify(mintCasdoorToken(t, key, "key-1",
		"https://casdoor.example.com", "openpocket", "u-1"))
	if err == nil {
		t.Fatal("expected an oversized jwks document to be refused")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected the error to name the size limit, got %v", err)
	}
}
