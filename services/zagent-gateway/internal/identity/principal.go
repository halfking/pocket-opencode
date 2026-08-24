// Package identity contains shared identifier and role definitions used
// across the ZAgentGateway (ZAG) service.
package identity

import "time"

// Role represents an RBAC role bound to a principal.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
	RoleAgent    Role = "agent"
)

// Principal is the authenticated identity for a request.
// It is populated by the auth middleware and consumed by the policy
// engine. Both user and service principals share this struct.
type Principal struct {
	Subject   string    `json:"sub"`
	TenantID  string    `json:"tenant_id"`
	Roles     []Role    `json:"roles"`
	TokenID   string    `json:"jti,omitempty"`
	IssuedAt  time.Time `json:"iat"`
	ExpiresAt time.Time `json:"exp"`
	Scopes    []string  `json:"scopes,omitempty"`
}

// HasRole returns true if the principal carries the given role.
func (p *Principal) HasRole(r Role) bool {
	for _, x := range p.Roles {
		if x == r {
			return true
		}
	}
	return false
}

// HasScope returns true if the principal carries the given scope.
func (p *Principal) HasScope(s string) bool {
	for _, x := range p.Scopes {
		if x == s {
			return true
		}
	}
	return false
}

// HasAnyScope returns true if the principal carries at least one of the
// supplied scopes.
func (p *Principal) HasAnyScope(scopes ...string) bool {
	for _, s := range scopes {
		if p.HasScope(s) {
			return true
		}
	}
	return false
}
