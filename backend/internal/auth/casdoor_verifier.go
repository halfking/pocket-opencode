// The Casdoor token verifier.
//
// What this file is for
// ---------------------
//
// OpenPocket logs its users in through Casdoor (see
// internal/server/server_oidc.go): the browser is sent to the Casdoor
// authorize page, the callback exchanges the code for an access token,
// and the access token's userinfo gives the identity. That part is done.
//
// What was missing
// ----------------
//
// The token OpenPocket then **hands out** is minted locally: a session
// JWT signed with the service's own HS256 secret (`iss=pocket`). That is
// fine for a token OpenPocket issued and verifies itself.
//
// It is not fine for the other direction. A token issued by Casdoor —
// which is what every other service in the family issues after its own
// Casdoor login — is signed RS256 with Casdoor's per-application
// certificate and published through Casdoor's JWKS endpoint. OpenPocket
// could not verify such a token at all: the local verifier only knows
// HS256, and the shared-secret verifier (identity-go) is also HS256-only
// (third_party/identity-go/token/verify.go:43 forces HS256).
//
// So a Casdoor bearer token presented to OpenPocket's API was refused.
// That is the authentication island this file closes: OpenPocket becomes
// able to verify the tokens Casdoor issues, so a caller authenticated
// anywhere in the family is authenticated here too.
//
// What it does not do
// -------------------
//
// This file does **not** replace the local session token. The local token
// stays the everyday bearer token of the API; this is an additional
// accepted issuer, tried after the local one. Nothing is taken away, so
// a deployment can adopt it one environment at a time.
//
// The issuer and the audience are checked, so a token issued for another
// application is refused even though its signature is good.

package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/halfking/pocket-opencode/backend/internal/outbound"
)

// CasdoorIssuerName is the `iss` value of the tokens Casdoor issues.
//
// The family shares one Casdoor instance, so every application issues
// tokens with the same issuer and tells them apart by the audience. The
// issuer is therefore a configuration value and this constant is only the
// default used when none was named.
const DefaultCasdoorIssuer = "https://door.casdoor.com"

// CasdoorJWKSKeySource verifies RS256 tokens against a remote JWKS
// endpoint.
//
// The key set is cached with a TTL and refetched when a token names a kid
// the cache does not hold — that is the rotation case, and without the
// forced refetch a rotated certificate would reject every new token until
// the TTL expired.
//
// A failed refetch serves the stale key set rather than refusing every
// token: a JWKS endpoint that is briefly unreachable must not log
// everybody out. The very first fetch has no stale set to serve, so it
// fails closed.
type CasdoorJWKSKeySource struct {
	url     string
	client  *http.Client
	ttl     time.Duration
	fetchTO time.Duration

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
	// lastErr is the reason the last refresh failed.
	//
	// It is kept so that a failure can be reported as itself: without it
	// an unreachable endpoint and an oversized document both surface as
	// "no jwks key for kid", which sends the reader looking for a key
	// rotation that never happened.
	lastErr error
}

// NewCasdoorJWKSKeySource builds a caching key source for a JWKS URL.
//
// The client comes from the outbound package so that the request carries
// the platform's timeout discipline: a JWKS endpoint that never answers
// must not hold a goroutine open forever.
func NewCasdoorJWKSKeySource(url string) *CasdoorJWKSKeySource {
	return &CasdoorJWKSKeySource{
		url:     url,
		client:  outbound.NewClient(10 * time.Second),
		ttl:     10 * time.Minute,
		fetchTO: 5 * time.Second,
	}
}

// KeyFunc is the jwt.Keyfunc adapter for golang-jwt.
//
// It accepts only the RSA signing methods. A token that asks for HS256
// is refused here rather than being handed to the symmetric path: an
// algorithm confusion attack works by getting a verifier to treat a
// public key as a shared secret, and refusing every method but RSA is
// what closes it.
func (s *CasdoorJWKSKeySource) KeyFunc(t *jwt.Token) (any, error) {
	switch t.Method {
	case jwt.SigningMethodRS256, jwt.SigningMethodRS384, jwt.SigningMethodRS512:
	default:
		return nil, fmt.Errorf("auth: unexpected casdoor token signing method %v", t.Method.Alg())
	}
	kid, _ := t.Header["kid"].(string)
	if strings.TrimSpace(kid) == "" {
		return nil, errors.New("auth: casdoor token header missing kid")
	}
	if key, ok := s.lookup(kid, false); ok {
		return key, nil
	}
	// An unknown kid means the certificate was rotated: one forced
	// refresh covers it before the token is refused.
	if key, ok := s.lookup(kid, true); ok {
		return key, nil
	}
	if err := s.lastRefreshError(); err != nil {
		// The key is missing because the key set could not be read. Say
		// why, so the failure is not mistaken for a rotation.
		return nil, fmt.Errorf("auth: no jwks key for kid %q: %w", kid, err)
	}
	return nil, fmt.Errorf("auth: no jwks key for kid %q", kid)
}

// lastRefreshError returns the reason the last refresh failed, if any.
func (s *CasdoorJWKSKeySource) lastRefreshError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// lookup returns the key for a kid, refreshing the cache when it is
// stale or when a refresh is forced.
func (s *CasdoorJWKSKeySource) lookup(kid string, forceRefresh bool) (*rsa.PublicKey, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keys == nil || forceRefresh || time.Since(s.fetched) > s.ttl {
		if err := s.refreshLocked(); err != nil {
			// Serve the stale key set on a refresh failure rather than
			// hard-failing every request during a transient outage.
			if s.keys == nil {
				return nil, false
			}
		}
	}
	key, ok := s.keys[kid]
	return key, ok
}

// maxCasdoorJWKSDocBytes is the largest JWKS document the verifier reads.
//
// The document holds a handful of RSA public keys, so a megabyte is
// already generous. The limit is not a guess about the size: the reader
// reads one byte past it and refuses an oversized document, so a hostile
// or broken endpoint cannot stream an unbounded body into memory.
const maxCasdoorJWKSDocBytes = 1 << 20

// refreshLocked fetches the JWKS document. The caller holds the lock.
//
// A failure is recorded in lastErr and returned; the caller decides
// whether the stale key set can still serve the request.
func (s *CasdoorJWKSKeySource) refreshLocked() error {
	err := s.fetchLocked()
	s.lastErr = err
	return err
}

// fetchLocked does the actual fetch. The caller holds the lock.
func (s *CasdoorJWKSKeySource) fetchLocked() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.fetchTO)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("auth: fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth: fetch jwks: status %d", resp.StatusCode)
	}
	// Read one byte past the limit and refuse an oversized document:
	// without that check a truncated prefix would be decoded as if it
	// were the whole document, and the failure would look like a broken
	// key set rather than an oversized response.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCasdoorJWKSDocBytes+1))
	if err != nil {
		return fmt.Errorf("auth: read jwks: %w", err)
	}
	if len(body) > maxCasdoorJWKSDocBytes {
		return fmt.Errorf("auth: jwks document exceeds %d bytes", maxCasdoorJWKSDocBytes)
	}
	var set struct {
		Keys []casdoorJWK `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("auth: decode jwks: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") || k.N == "" || k.E == "" {
			continue
		}
		pub, err := k.rsaPublicKey()
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	s.keys = keys
	s.fetched = time.Now()
	return nil
}

// casdoorJWK is one key of the JWKS document.
type casdoorJWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// rsaPublicKey rebuilds the public key from its JWKS components.
func (k casdoorJWK) rsaPublicKey() (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("auth: jwks modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("auth: jwks exponent: %w", err)
	}
	var e uint32
	for _, b := range eBytes {
		e = e<<8 | uint32(b)
	}
	if e == 0 || len(nBytes) == 0 {
		return nil, errors.New("auth: jwks key has empty modulus or exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e)}, nil
}

// CasdoorTokenClaims is the shape of the claims OpenPocket reads out of
// a Casdoor token.
//
// The Casdoor token carries the user's identity: `sub` is the stable
// subject, `name` the display name and `email` the address. The identity
// is taken from the token itself — the Casdoor subject is the authority
// here, and OpenPocket does not keep a second copy of it that could
// disagree.
type CasdoorTokenClaims struct {
	jwt.RegisteredClaims
	Sub    string   `json:"sub"`
	Name   string   `json:"name"`
	Email  string   `json:"email"`
	Roles  []string `json:"roles,omitempty"`
	Tenant string   `json:"tenant,omitempty"`
}

// CasdoorVerifier verifies the tokens Casdoor issues.
//
// It is constructed once and reused: the key source holds the cache, and
// a verifier that refetched the key set per request would hammer the
// JWKS endpoint.
type CasdoorVerifier struct {
	keys     *CasdoorJWKSKeySource
	issuer   string
	audience string
}

// NewCasdoorVerifier builds a verifier for one issuer and audience.
//
// The audience is the Casdoor application the token was issued for. It
// is checked, so a token minted for a different application in the same
// Casdoor instance is refused: one application's token is not another
// application's credential.
func NewCasdoorVerifier(jwksURL, issuer, audience string) *CasdoorVerifier {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" {
		issuer = DefaultCasdoorIssuer
	}
	return &CasdoorVerifier{
		keys:     NewCasdoorJWKSKeySource(jwksURL),
		issuer:   issuer,
		audience: strings.TrimSpace(audience),
	}
}

// Enabled reports whether the verifier is configured.
//
// A verifier without a JWKS URL cannot verify anything, and the caller
// treats "not enabled" as "this issuer is not accepted" rather than
// attempting a verification that can only fail.
func (v *CasdoorVerifier) Enabled() bool {
	return v != nil && v.keys != nil && strings.TrimSpace(v.keys.url) != ""
}

// Verify checks the signature, the issuer, the audience and the lifetime
// of a Casdoor token, and returns its claims.
func (v *CasdoorVerifier) Verify(raw string) (*CasdoorTokenClaims, error) {
	if !v.Enabled() {
		return nil, errors.New("auth: casdoor verifier is not configured")
	}
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("auth: empty token")
	}
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithLeeway(30 * time.Second),
	}
	if v.audience != "" {
		opts = append(opts, jwt.WithAudience(v.audience))
	}
	claims := &CasdoorTokenClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, v.keys.KeyFunc, opts...)
	if err != nil {
		return nil, fmt.Errorf("auth: verify casdoor token: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("auth: invalid casdoor token")
	}
	if strings.TrimSpace(claims.Sub) == "" {
		return nil, errors.New("auth: casdoor token has no subject")
	}
	return claims, nil
}

// VerifyCasdoorToken verifies a Casdoor token and maps it onto the
// result shape the API's middleware uses.
//
// The mapping is the same one the shared-secret path produces, so a
// caller authenticated by Casdoor and one authenticated by the local
// signer are indistinguishable downstream — which is the whole point of
// closing the island.
//
// The workspace is not resolved here: the caller of the middleware
// resolves it from the subject, exactly as it does for a locally issued
// token.
func VerifyCasdoorToken(v *CasdoorVerifier, raw string) (*IdentityVerifierResult, error) {
	claims, err := v.Verify(raw)
	if err != nil {
		return nil, err
	}
	workspaceID := strings.TrimSpace(claims.Tenant)
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(claims.Sub)
	}
	expiresAt := time.Time{}
	if claims.ExpiresAt != nil {
		expiresAt = claims.ExpiresAt.Time
	}
	return &IdentityVerifierResult{
		UserID:      claims.Sub,
		Role:        firstRole(claims.Roles, ""),
		WorkspaceID: workspaceID,
		TenantID:    claims.Tenant,
		Issuer:      v.issuer,
		Audience:    v.audience,
		ExpiresAt:   expiresAt,
	}, nil
}
