// Package accjwt mints the HS256 service JSON Web Tokens that ACC's
// Runtime Control surface (/api/v2/*) accepts.
//
// Why this package exists: ACC's /api/v2/* endpoints do NOT accept a
// static bearer token. Measured against the live local ACC on 2026-10-22:
//
//	GET /api/v2/approvals
//	  no Authorization      → 401 "missing bearer token"
//	  Authorization: Bearer <static>  → 401 "invalid bearer token"
//	  Authorization: Bearer <HS256 service JWT>  → 200 {"approvals":[],"count":0,"ok":true}
//
// The static form is what agent-companion's own client used to fall back
// to, and what openpocket's acchttp can only send. So a Pocket↔ACC
// integration built on the static form is permanently 401 — and 401 from
// a fail-closed permission gate reads as "the gate is unavailable", which
// is indistinguishable from "ACC is down" and cannot be acted on.
//
// The implementation is hand-rolled rather than pulled from a JWT
// library: the dependency list stays tiny, the token shape is fixed by
// exactly one consumer, and every claim is explicit — so the signing
// input is auditable here instead of hidden behind middleware.
//
// The claim names are ACC's wire contract and must not be renamed:
//
//	"sub"         subject (ACC maps it to UserID)
//	"tenant_id"   required for anything that is not a service identity
//	"type"/"role" mark a machine actor; ACC waives the tenant requirement
//	              for service identities
//	"iat"/"nbf"/"exp" standard time claims; nbf is backdated to tolerate
//	              clock skew, which is what ACC expects.
package accjwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultTTL is how long a minted token stays valid when the caller does
// not say otherwise.
const DefaultTTL = time.Hour

// ErrNoSecret means the shared secret is not configured. Callers must
// treat it as "ACC integration unavailable" rather than degrading to an
// unauthenticated request.
var ErrNoSecret = errors.New("accjwt: ACC control secret not configured")

// Claims mirrors ACC's auth.Claims. The JSON tags are the wire contract.
type Claims struct {
	Subject   string   `json:"sub,omitempty"`
	Username  string   `json:"username,omitempty"`
	Roles     []string `json:"roles,omitempty"`
	IsAdmin   bool     `json:"isAdmin,omitempty"`
	TenantID  string   `json:"tenant_id,omitempty"`
	Type      string   `json:"type,omitempty"`
	Role      string   `json:"role,omitempty"`
	IssuedAt  int64    `json:"iat,omitempty"`
	NotBefore int64    `json:"nbf,omitempty"`
	ExpiresAt int64    `json:"exp,omitempty"`
}

// Options describes the identity a minted token asserts.
type Options struct {
	// Secret is the shared HS256 signing secret (ACC's control secret).
	Secret string
	// Subject identifies the machine actor (e.g. "pocketd:ac-30").
	Subject string
	// Username is a human-readable label for the actor.
	Username string
	// TenantID is required unless ServiceIdentity is set.
	TenantID string
	// ServiceIdentity marks the token as a machine actor (type/role=agent),
	// which is what waives ACC's tenant requirement.
	ServiceIdentity bool
	// TTL bounds the token's lifetime; DefaultTTL when unset.
	TTL time.Duration
	// Now overrides the clock, for tests.
	Now time.Time
}

// Mint returns a signed HS256 JWT for the given identity.
func Mint(opts Options) (string, error) {
	secret := strings.TrimSpace(opts.Secret)
	if secret == "" {
		return "", ErrNoSecret
	}
	if strings.TrimSpace(opts.Subject) == "" {
		return "", errors.New("accjwt: subject is required")
	}
	if strings.TrimSpace(opts.TenantID) == "" && !opts.ServiceIdentity {
		return "", errors.New("accjwt: tenant_id is required for a non-service identity")
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}

	c := Claims{
		Subject:   opts.Subject,
		Username:  opts.Username,
		TenantID:  opts.TenantID,
		IssuedAt:  now.Unix(),
		NotBefore: now.Add(-30 * time.Second).Unix(),
		ExpiresAt: now.Add(ttl).Unix(),
	}
	if opts.ServiceIdentity {
		c.Type = "agent"
		c.Role = "agent"
	}

	headerJSON, err := json.Marshal(map[string]any{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", fmt.Errorf("accjwt: marshal header: %w", err)
	}
	claimsJSON, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("accjwt: marshal claims: %w", err)
	}
	signingInput := b64(headerJSON) + "." + b64(claimsJSON)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	return signingInput + "." + b64(mac.Sum(nil)), nil
}

func b64(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// Signer mints a fresh token per request. It satisfies the Signer
// interface of the acchttp package.
type Signer struct {
	opts Options
}

// NewSigner returns a Signer bound to the given options.
func NewSigner(opts Options) *Signer { return &Signer{opts: opts} }

// Token mints a signed service token.
func (s *Signer) Token() (string, error) {
	if s == nil {
		return "", ErrNoSecret
	}
	return Mint(s.opts)
}
