package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const DelegationAudience = "pocket-workspace-authority"
const DelegationIssuer = "redclaw"
const delegationKeyContext = "openpocket-workspace-delegation:v1"
const DelegationMaxTTL = 30 * time.Second

var ErrDelegationInvalid = errors.New("identity: invalid workspace delegation")
var ErrDelegationReplay = errors.New("identity: workspace delegation was already used")

type DelegationClaims struct {
	TenantID string `json:"tenant_id"`
	Actor    string `json:"act"`
	Scope    string `json:"scope"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	jwt.RegisteredClaims
}

// VerifyDelegation accepts only this dedicated request credential. It never
// routes through the ordinary pocket-api bearer verifier or trusts workspace
// membership claims; handlers still query the identity store for that decision.
func VerifyDelegation(secret []byte, raw, tenantID, method, path string, now time.Time) (*DelegationClaims, error) {
	if len(secret) < 32 || tenantID == "" || method != http.MethodGet {
		return nil, ErrDelegationInvalid
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(delegationKeyContext))
	claims := &DelegationClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) { return mac.Sum(nil), nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(DelegationIssuer), jwt.WithAudience(DelegationAudience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil || claims.ExpiresAt == nil || claims.IssuedAt == nil || claims.NotBefore == nil || claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time) > DelegationMaxTTL || !claims.ExpiresAt.Time.After(claims.IssuedAt.Time) ||
		claims.TenantID != tenantID || strings.TrimSpace(claims.Subject) == "" || claims.Subject != strings.TrimSpace(claims.Subject) || claims.Actor != "redclaw.admin" || claims.Scope != "workspace-membership:read" || claims.Method != method || claims.Path != path || len(claims.Audience) != 1 {
		return nil, ErrDelegationInvalid
	}
	if _, err := uuid.Parse(claims.ID); err != nil {
		return nil, ErrDelegationInvalid
	}
	return claims, nil
}
