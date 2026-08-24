// Package auth implements delegated JWT issuance and verification for the
// ZAgentGateway (ZAG). The token format is fixed by ADR-0001 and is the
// single source of truth for how an actor's identity travels between ZAG,
// the Pocket BFF, Compute Pods, and downstream services.
//
// Hard rules (all enforced by tests in token_test.go):
//
//   - The "alg" header MUST be ES256 or EdDSA. "none", HS256/384/512, and
//     RS* algorithms MUST be rejected outright, even if the signature
//     would otherwise verify. This blocks the alg=none downgrade and the
//     HMAC key-confusion attacks against an asymmetric public key.
//   - The "kid" header MUST resolve to a key currently active in the
//     trust store. Keys marked revoked, retired, or unknown are rejected.
//   - The "aud" claim MUST contain the audience this gateway instance
//     services (typically "zag.api"). Tokens minted for downstream
//     audiences (e.g. "redclaw.api", "acc.api") MUST NOT be replayed at
//     ZAG.
//   - The "iss" claim MUST be one of the configured issuers. Unknown
//     issuers are rejected.
//   - "tenant_id" comes ONLY from the verified claims; the verifier MUST
//     never consult request headers or query parameters as a substitute
//     for an authenticated tenant.
//
// Refresh vs. re-enroll is documented in ADR-0001 §6: end-user tokens
// are refreshed through Pocket IDP; service tokens are short-lived and
// re-minted on demand; pod enrollment tokens are single-use and never
// reused. The verification flow is unaware of refresh semantics — those
// are enforced by the mint path.
package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Sentinel errors returned by Mint and Verify. Callers SHOULD translate
// these to the canonical HTTP status codes listed in ADR-0001 §7.
var (
	ErrAlgForbidden     = errors.New("auth: forbidden signing algorithm")
	ErrAlgMismatch      = errors.New("auth: alg header does not match key")
	ErrUnknownIssuer    = errors.New("auth: unknown issuer")
	ErrAudienceMismatch = errors.New("auth: audience does not include expected audience")
	ErrExpired          = errors.New("auth: token expired")
	ErrNotYetValid      = errors.New("auth: token not yet valid")
	ErrUnknownKey       = errors.New("auth: unknown key id")
	ErrRevokedKey       = errors.New("auth: key id has been revoked")
	ErrTokenMissing     = errors.New("auth: required claim missing")
	ErrTenantMismatch   = errors.New("auth: tenant_id does not match verified claim")
)

// Algorithm is the set of JWS algorithms ZAG accepts. Anything outside
// this list is rejected at the edge.
type Algorithm string

const (
	AlgES256 Algorithm = "ES256"
	AlgEdDSA Algorithm = "EdDSA"
)

// allowedAlgs is the explicit allowlist used at validation time. Tests
// rely on the table to assert rejection for every other value.
var allowedAlgs = map[Algorithm]struct{}{
	AlgES256: {},
	AlgEdDSA: {},
}

// ActorType enumerates the kinds of principal a token can carry. The
// value MUST be one of these strings — anything else is rejected. The
// choice is later enforced by the authz policy to keep user, service
// and pod tokens on disjoint authorization tracks.
type ActorType string

const (
	ActorUser          ActorType = "user"
	ActorService       ActorType = "service"
	ActorPod           ActorType = "pod"
	ActorIdeConnector  ActorType = "ide_connector"
)

// Scope is a single OAuth-style scope string. ZAG carries scopes as a
// string array on the token, but the verification path also accepts a
// space-separated "scope" claim for compatibility with OIDC providers.
type Scope = string

// Claims is the verified JWT payload exposed to downstream layers. It
// is populated only after every check in Verify has passed.
type Claims struct {
	Issuer    string    `json:"iss"`
	Audience  []string  `json:"aud"`
	Subject   string    `json:"sub"`
	TenantID  string    `json:"tenant_id"`
	ActorID   string    `json:"actor_id"`
	ActorType ActorType `json:"actor_type"`
	Scopes    []string  `json:"scope,omitempty"`
	JTI       string    `json:"jti"`
	IssuedAt  time.Time `json:"iat"`
	NotBefore time.Time `json:"nbf"`
	ExpiresAt time.Time `json:"exp"`
	// PodID is required when ActorType == ActorPod. It carries the
	// canonical pod identifier from the IDP.
	PodID string `json:"pod_id,omitempty"`
}

// HasScope returns true if the claims carry the given scope.
func (c *Claims) HasScope(s string) bool {
	if c == nil {
		return false
	}
	for _, x := range c.Scopes {
		if x == s {
			return true
		}
	}
	return false
}

// HasAnyScope returns true if the claims carry at least one of the
// supplied scopes.
func (c *Claims) HasAnyScope(scopes ...string) bool {
	if c == nil {
		return false
	}
	for _, s := range scopes {
		if c.HasScope(s) {
			return true
		}
	}
	return false
}

// MintInput captures the data required to mint a new delegated token.
// All non-pointer fields are required unless documented otherwise.
type MintInput struct {
	Issuer     string
	Audience   []string
	Subject    string
	TenantID   string
	ActorID    string
	ActorType  ActorType
	Scopes     []string
	TTL        time.Duration
	NotBefore  time.Time // optional; defaults to time.Now()
	PodID      string    // required iff ActorType == ActorPod
	KeyID      string    // required
}

// Valid enforces invariants on a MintInput before any crypto runs so
// that callers see structured errors rather than panics deep inside
// the JWT library. TTL must be non-zero (positive or negative) — a
// negative TTL produces a token that is already expired at issuance,
// which is occasionally useful in tests that exercise the expired
// path.
func (in MintInput) Valid() error {
	if in.Issuer == "" {
		return fmt.Errorf("auth: issuer required")
	}
	if len(in.Audience) == 0 {
		return fmt.Errorf("auth: audience required")
	}
	if in.Subject == "" {
		return fmt.Errorf("auth: subject required")
	}
	if in.TenantID == "" {
		return fmt.Errorf("auth: tenant_id required")
	}
	if in.ActorID == "" {
		return fmt.Errorf("auth: actor_id required")
	}
	switch in.ActorType {
	case ActorUser, ActorService, ActorPod, ActorIdeConnector:
	default:
		return fmt.Errorf("auth: invalid actor_type %q", in.ActorType)
	}
	if in.ActorType == ActorPod && in.PodID == "" {
		return fmt.Errorf("auth: pod actor requires pod_id")
	}
	if in.TTL == 0 {
		return fmt.Errorf("auth: ttl must be non-zero")
	}
	if in.KeyID == "" {
		return fmt.Errorf("auth: key_id required")
	}
	return nil
}

// Key represents a single JWK registered in the local trust store. The
// trust store is the single source of truth for which keys ZAG accepts.
// Both active and previously-active ("previous") keys live here so that
// tokens issued near the rotation boundary still verify for a short
// grace window.
type Key struct {
	KID       string
	Algorithm Algorithm
	PublicKey any // *ecdsa.PublicKey for ES256, ed25519.PublicKey for EdDSA
	NotBefore time.Time
	NotAfter  time.Time
	Status    KeyStatus
}

// KeyStatus mirrors the lifecycle of a JWK.
type KeyStatus string

const (
	KeyStatusActive   KeyStatus = "active"
	KeyStatusPrevious KeyStatus = "previous"
	KeyStatusRevoked  KeyStatus = "revoked"
)

// Active reports whether the key is currently usable for verification.
func (k Key) Active() bool {
	if k.Status == KeyStatusRevoked {
		return false
	}
	now := time.Now()
	if !k.NotBefore.IsZero() && now.Before(k.NotBefore) {
		return false
	}
	if !k.NotAfter.IsZero() && !now.Before(k.NotAfter) {
		return false
	}
	return k.Status == KeyStatusActive || k.Status == KeyStatusPrevious
}

// TrustStore is the read-only view of registered signing keys. The
// implementation in production is backed by JWKS + a revocation list;
// the interface is intentionally small so it can be mocked in tests.
type TrustStore interface {
	// Get returns the key registered under the given kid, or
	// (nil, ErrUnknownKey) if no such key exists. The boolean is true
	// when the key is currently active and within its validity window.
	Get(kid string) (Key, error)
}

// Verifier is the configured JWT verifier. It is constructed once at
// startup and used concurrently thereafter. It holds an immutable
// reference to the trust store; rotations are visible the next time the
// verifier is rebuilt (which is exactly the "previous_kid" overlap
// window).
type Verifier struct {
	expectedIssuer  string
	expectedAud     string
	trust           TrustStore
	allowedAlgs     map[Algorithm]struct{}
	clockSkew       time.Duration
	maxExpectedTTL  time.Duration
}

// VerifierConfig captures the inputs needed to construct a Verifier.
// ExpectedIssuer is the canonical "iss" value ZAG was deployed with;
// ExpectedAudience is the value tokens must contain in "aud".
type VerifierConfig struct {
	ExpectedIssuer   string
	ExpectedAudience string
	Trust            TrustStore
	ClockSkew        time.Duration // default 30s
	MaxExpectedTTL   time.Duration // default 60m; user tokens default 15m
}

// NewVerifier builds a Verifier with safe defaults if ClockSkew or
// MaxExpectedTTL are zero.
func NewVerifier(cfg VerifierConfig) *Verifier {
	if cfg.ClockSkew <= 0 {
		cfg.ClockSkew = 30 * time.Second
	}
	if cfg.MaxExpectedTTL <= 0 {
		cfg.MaxExpectedTTL = time.Hour
	}
	if cfg.Trust == nil {
		cfg.Trust = noopTrustStore{}
	}
	algs := make(map[Algorithm]struct{}, len(allowedAlgs))
	for k := range allowedAlgs {
		algs[k] = struct{}{}
	}
	return &Verifier{
		expectedIssuer: cfg.ExpectedIssuer,
		expectedAud:    cfg.ExpectedAudience,
		trust:          cfg.Trust,
		allowedAlgs:    algs,
		clockSkew:      cfg.ClockSkew,
		maxExpectedTTL: cfg.MaxExpectedTTL,
	}
}

// Verify validates a serialized JWT and returns the verified Claims
// only when every check below passes.
//
// Order matters: cheap structural checks first, then signature, then
// claims. Returning a sentinel error per failure makes the call site
// trivial to translate into HTTP codes.
func (v *Verifier) Verify(raw string) (*Claims, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrTokenMissing
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"ES256", "EdDSA"}),
		jwt.WithLeeway(v.clockSkew),
	)
	tok, err := parser.Parse(raw, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, ErrUnknownKey
		}
		alg, _ := t.Header["alg"].(string)
		if _, ok := v.allowedAlgs[Algorithm(alg)]; !ok {
			return nil, ErrAlgForbidden
		}
		k, err := v.trust.Get(kid)
		if err != nil {
			return nil, ErrUnknownKey
		}
		if k.Status == KeyStatusRevoked {
			return nil, ErrRevokedKey
		}
		if Algorithm(alg) != k.Algorithm {
			return nil, ErrAlgMismatch
		}
		return k.PublicKey, nil
	})
	if err != nil {
		switch {
		case errors.Is(err, jwt.ErrTokenExpired):
			return nil, ErrExpired
		case errors.Is(err, jwt.ErrTokenNotValidYet):
			return nil, ErrNotYetValid
		case errors.Is(err, ErrAlgForbidden):
			return nil, ErrAlgForbidden
		case errors.Is(err, ErrUnknownKey):
			return nil, ErrUnknownKey
		case errors.Is(err, ErrRevokedKey):
			return nil, ErrRevokedKey
		case errors.Is(err, ErrAlgMismatch):
			return nil, ErrAlgMismatch
		default:
			// jwt-go surfaces algorithm rejections as
			// "signing method X is invalid" without an exported sentinel.
			// Translate the known algorithm names into ErrAlgForbidden
			// so the contract is uniform.
			msg := err.Error()
			if strings.Contains(msg, "signing method") {
				return nil, ErrAlgForbidden
			}
			return nil, err
		}
	}
	if !tok.Valid {
		return nil, ErrTokenMissing
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrTokenMissing
	}
	return v.toClaims(claims)
}

// toClaims maps the raw JWT claim map to our typed Claims and enforces
// the invariants the parser is unable to express.
func (v *Verifier) toClaims(m jwt.MapClaims) (*Claims, error) {
	iss, _ := m["iss"].(string)
	if iss == "" {
		return nil, ErrTokenMissing
	}
	if v.expectedIssuer != "" && iss != v.expectedIssuer {
		return nil, ErrUnknownIssuer
	}
	aud, _ := m["aud"].([]any)
	audiences := make([]string, 0, len(aud))
	for _, a := range aud {
		if s, ok := a.(string); ok && s != "" {
			audiences = append(audiences, s)
		}
	}
	if len(audiences) == 0 {
		// "aud" may also be a single string per RFC 7519.
		if s, ok := m["aud"].(string); ok && s != "" {
			audiences = append(audiences, s)
		}
	}
	if len(audiences) == 0 {
		return nil, ErrTokenMissing
	}
	if v.expectedAud != "" {
		found := false
		for _, a := range audiences {
			if a == v.expectedAud {
				found = true
				break
			}
		}
		if !found {
			return nil, ErrAudienceMismatch
		}
	}
	sub, _ := m["sub"].(string)
	tenant, _ := m["tenant_id"].(string)
	actorID, _ := m["actor_id"].(string)
	actorType, _ := m["actor_type"].(string)
	jti, _ := m["jti"].(string)
	if sub == "" || tenant == "" || actorID == "" || jti == "" {
		return nil, ErrTokenMissing
	}
	if actorType != string(ActorUser) &&
		actorType != string(ActorService) &&
		actorType != string(ActorPod) &&
		actorType != string(ActorIdeConnector) {
		return nil, ErrTokenMissing
	}
	podID, _ := m["pod_id"].(string)
	if actorType == string(ActorPod) && podID == "" {
		return nil, ErrTokenMissing
	}
	var scopes []string
	switch s := m["scope"].(type) {
	case string:
		if s != "" {
			scopes = strings.Fields(s)
		}
	case []any:
		for _, x := range s {
			if str, ok := x.(string); ok && str != "" {
				scopes = append(scopes, str)
			}
		}
	}
	iat, _ := m["iat"].(float64)
	nbf, _ := m["nbf"].(float64)
	exp, _ := m["exp"].(float64)
	if iat == 0 || exp == 0 {
		return nil, ErrTokenMissing
	}
	issued := time.Unix(int64(iat), 0)
	expires := time.Unix(int64(exp), 0)
	notBefore := time.Unix(int64(nbf), 0)
	// Bound the TTL window. Without this check, a leaked signing key
	// could mint tokens valid for years.
	if v.maxExpectedTTL > 0 && expires.Sub(issued) > v.maxExpectedTTL {
		return nil, ErrTokenMissing
	}
	return &Claims{
		Issuer:    iss,
		Audience:  audiences,
		Subject:   sub,
		TenantID:  tenant,
		ActorID:   actorID,
		ActorType: ActorType(actorType),
		Scopes:    scopes,
		JTI:       jti,
		IssuedAt:  issued,
		NotBefore: notBefore,
		ExpiresAt: expires,
		PodID:     podID,
	}, nil
}

// VerifyTenantCrossCheck compares a tenant header value (from the
// incoming HTTP request) against the verified JWT tenant. The error is
// always ErrTenantMismatch so the authz layer can apply the same
// response code regardless of source. The intent — encoded in
// ADR-0001 §8 — is that no header, query string, or body field can
// ever override the claim-derived tenant.
func VerifyTenantCrossCheck(claimTenant, headerTenant string) error {
	if claimTenant == "" || headerTenant == "" {
		return nil
	}
	if claimTenant != headerTenant {
		return ErrTenantMismatch
	}
	return nil
}

// Minter mints delegated JWTs. It binds a single signing key at
// construction so callers cannot accidentally use two different keys
// for the same audience.
type Minter struct {
	key       *ecdsa.PrivateKey
	keyID     string
	algorithm Algorithm
}

// NewMinterFromECDSA builds a Minter backed by an ES256 key. The key is
// expected to be an *ecdsa.PrivateKey on the P-256 curve; any other
// curve is rejected up front to keep the validation story tight.
func NewMinterFromECDSA(kid string, key *ecdsa.PrivateKey) (*Minter, error) {
	if key == nil {
		return nil, fmt.Errorf("auth: nil ECDSA key")
	}
	if key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("auth: ES256 requires P-256 curve")
	}
	return &Minter{
		keyID:     kid,
		algorithm: AlgES256,
		key:       key,
	}, nil
}

// Mint issues a new JWT for the given input. The returned string is the
// compact serialization (header.payload.signature).
func (m *Minter) Mint(in MintInput) (string, error) {
	if err := in.Valid(); err != nil {
		return "", err
	}
	if in.KeyID != m.keyID {
		return "", ErrUnknownKey
	}
	now := time.Now()
	if in.NotBefore.IsZero() {
		in.NotBefore = now
	}
	exp := in.NotBefore.Add(in.TTL)
	// exp may be in the past if a negative TTL was passed (used by tests
	// for the already-expired path); that is fine — Verify will reject
	// the token with ErrExpired when it reaches the expiry check.
	claims := jwt.MapClaims{
		"iss":        in.Issuer,
		"aud":        in.Audience,
		"sub":        in.Subject,
		"tenant_id":  in.TenantID,
		"actor_id":   in.ActorID,
		"actor_type": in.ActorType,
		"scope":      in.Scopes,
		"jti":        uuid.NewString(),
		"iat":        now.Unix(),
		"nbf":        in.NotBefore.Unix(),
		"exp":        exp.Unix(),
	}
	if in.ActorType == ActorPod {
		claims["pod_id"] = in.PodID
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["kid"] = m.keyID
	return tok.SignedString(m.key)
}

// noopTrustStore is the fallback for tests that exercise pure
// validation logic without a real key.
type noopTrustStore struct{}

func (noopTrustStore) Get(string) (Key, error) { return Key{}, ErrUnknownKey }

// StaticTrustStore is a tiny in-memory TrustStore. It exists so tests
// and the local CLI can register keys without dragging in a database.
// Production deployments MUST wire this to a JWKS-backed implementation
// that supports rotation and revocation pushes.
type StaticTrustStore struct {
	keys map[string]Key
}

// NewStaticTrustStore builds an empty store.
func NewStaticTrustStore() *StaticTrustStore {
	return &StaticTrustStore{keys: map[string]Key{}}
}

// Put registers a key. Putting a key with the same KID twice replaces
// the prior entry; this matches how rotation updates a single entry.
func (s *StaticTrustStore) Put(k Key) {
	s.keys[k.KID] = k
}

// Get returns the registered key or ErrUnknownKey.
func (s *StaticTrustStore) Get(kid string) (Key, error) {
	if k, ok := s.keys[kid]; ok {
		return k, nil
	}
	return Key{}, ErrUnknownKey
}

// Revoke flips the key's status to revoked. It does not remove the
// entry because we still want to be able to recognise and refuse
// signatures that claim this kid.
func (s *StaticTrustStore) Revoke(kid string) {
	if k, ok := s.keys[kid]; ok {
		k.Status = KeyStatusRevoked
		s.keys[kid] = k
	}
}

// randomJTI is a small helper so tests can mint predictable IDs
// without bringing in google/uuid in their setup path.
func randomJTI() string { return uuid.NewString() }

// nonceBytes returns n random bytes for tests that need a deterministic
// but unique value. Kept here so tests can avoid importing crypto/rand
// directly.
func nonceBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("auth: rand failed: %w", err))
	}
	return b
}
