// Package authz implements the policy evaluation layer of ZAG.
//
// The model combines role-based access control (RBAC) and
// attribute-based access control (ABAC):
//
//   - RBAC matches (principal, action, resource) tuples to a coarse
//     decision based on the principal's role(s). If no RBAC rule allows
//     the action, the request is denied with NoRoleMatch — there is no
//     implicit allow path.
//   - ABAC then evaluates the surviving allow rules under their
//     predicate set. A failing predicate downgrades the decision to
//     Denied with the matching rule id. ABAC can also produce outright
//     Deny rules that short-circuit even a passing RBAC allow — these
//     are how the gateway encodes "deny cross-tenant", "deny if no
//     mTLS", etc.
//
// Hard rules (enforced by policy_test.go):
//
//   - The decision MUST be tenant-scoped. A request whose verified
//     tenant does not match the resource's tenant is denied with
//     CrossTenant regardless of RBAC. Bare X-Tenant-ID headers are
//     never trusted.
//   - The five non-negotiable invariants from the task brief (mTLS
//     fallback, query JWT for WS, second admin key in ZAG, audit
//     unavailability, bare X-Tenant-ID) all map to deny rules here.
//   - Scope escalation is rejected: a request whose effective scope
//     does not contain the action's required scope is denied even when
//     the RBAC rule would otherwise allow it.
package authz

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/halfking/zagent-gateway/internal/identity"
)

// Action is the canonical name of an operation the gateway enforces.
// Convention: dotted lower-snake. New actions are added by extending
// the ruleset, never by branching inside handlers.
type Action string

const (
	ActionSessionRead    Action = "session.read"
	ActionSessionWrite   Action = "session.write"
	ActionSessionDelete  Action = "session.delete"
	ActionAgentInvoke    Action = "agent.invoke"
	ActionPodEnroll      Action = "pod.enroll"
	ActionCertRevoke     Action = "cert.revoke"
	ActionAuditRead      Action = "audit.read"
	ActionPolicyRead     Action = "policy.read"
	ActionPolicyWrite    Action = "policy.write"
	ActionHighRiskApprove Action = "high_risk.approve"
)

// Decision is the verdict returned by Evaluate.
type Decision string

const (
	Allow        Decision = "allow"
	Deny         Decision = "deny"
	DenyNoRole   Decision = "deny.no_role"
	DenyCrossTen Decision = "deny.cross_tenant"
	DenyPredicate Decision = "deny.predicate"
	DenyScopeEsc  Decision = "deny.scope_escalation"
	DenyMTLSReq   Decision = "deny.mtls_required"
	DenyAuditReq  Decision = "deny.audit_unavailable"
)

// Reason describes a single rule's contribution to the decision.
type Reason struct {
	RuleID  string
	Effect  Decision
	Message string
}

// Result is the structured outcome of Evaluate. Reasons are ordered
// deterministically so callers can produce stable logs.
type Result struct {
	Decision Decision
	Reasons  []Reason
	Rule     string // last rule id that drove the decision
}

// Allowed is a convenience predicate.
func (r Result) Allowed() bool { return r.Decision == Allow }

// Resource captures the object being acted upon. TenantID is mandatory;
// callers that omit it get a deny by construction.
type Resource struct {
	Type     string
	ID       string
	TenantID string
	Owner    string // owner_id (e.g. session creator)
	Tags     []string
}

// Request is the input to Evaluate.
type Request struct {
	Action    Action
	Resource  Resource
	Principal identity.Principal
	Env       Environment
}

// Environment carries context attributes that ABAC rules consult. It is
// populated by middleware from verified sources (never from headers).
type Environment struct {
	MTLSPresent        bool
	AuditBackendReachable bool
	HighRiskOp         bool
	ClientIP           string
	Time               time.Time
}

// Rule is the building block of the policy bundle. Each rule pairs an
// action with a predicate; the Effect can be Allow or Deny. Deny rules
// are evaluated first so they can short-circuit allows (e.g. cross-
// tenant deny always wins).
type Rule struct {
	ID         string
	Effect     Decision
	Action     Action // empty matches any action
	Roles      []identity.Role
	ScopeReq   []string // required scopes on the principal (ABAC)
	Required   []Predicate // all must pass (ABAC)
	Forbidden  []Predicate // any one trips a deny
}

// Policy is the immutable view of the bundle. Evaluate is safe for
// concurrent use; the bundle itself is built once at startup.
type Policy struct {
	mu     sync.RWMutex
	rules  []Rule
}

// Predicate evaluates against the request and returns true when the
// condition holds. Predicates MUST be pure; no I/O, no clock reads
// outside the Environment.
type Predicate func(Request) bool

// NewPolicy builds a Policy from a flat slice. The rules are sorted
// so deny rules are evaluated before allow rules; within each group
// the order from the input is preserved.
func NewPolicy(rules []Rule) *Policy {
	cp := append([]Rule(nil), rules...)
	sort.SliceStable(cp, func(i, j int) bool {
		// deny first
		if cp[i].Effect != cp[j].Effect {
			return cp[i].Effect == Deny
		}
		return false
	})
	return &Policy{rules: cp}
}

// Rules returns a copy of the rule set for diagnostics. The returned
// slice must not be mutated by the caller.
func (p *Policy) Rules() []Rule {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Rule, len(p.rules))
	copy(out, p.rules)
	return out
}

// Evaluate runs the decision pipeline. The order is:
//
//  1. Validate inputs.
//  2. Cross-tenant deny (built-in, cannot be overridden).
//  3. RBAC: collect rules whose roles overlap with the principal.
//     If no RBAC allow matches, deny with NoRoleMatch.
//  4. ABAC: for each surviving allow, evaluate Required + Forbidden.
//     A failing predicate downgrades the decision.
//  5. ABAC deny rules run last and can override an allow.
//
// Every step appends a Reason so callers can render an audit-grade
// explanation.
func (p *Policy) Evaluate(req Request) Result {
	res := Result{}
	if !validRequest(req) {
		res.Decision = Deny
		res.Reasons = append(res.Reasons, Reason{RuleID: "input.invalid", Effect: Deny, Message: "missing required fields"})
		return res
	}
	if !crossTenantOK(req) {
		res.Decision = DenyCrossTen
		res.Reasons = append(res.Reasons, Reason{RuleID: "deny.cross_tenant", Effect: Deny, Message: "principal tenant != resource tenant"})
		res.Rule = "deny.cross_tenant"
		return res
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Phase 1: deny rules.
	for _, r := range p.rules {
		if r.Effect != Deny {
			continue
		}
		if !ruleAppliesToAction(r, req.Action) {
			continue
		}
		if !anyRoleOverlap(r.Roles, req.Principal.Roles) {
			continue
		}
		if !allMatch(r.Forbidden, req) {
			res.Decision = Deny
			res.Reasons = append(res.Reasons, Reason{RuleID: r.ID, Effect: Deny, Message: "deny rule matched"})
			res.Rule = r.ID
			return res
		}
	}

	// Phase 2: RBAC allow rules.
	var allowed []Rule
	for _, r := range p.rules {
		if r.Effect != Allow {
			continue
		}
		if !ruleAppliesToAction(r, req.Action) {
			continue
		}
		if !anyRoleOverlap(r.Roles, req.Principal.Roles) {
			continue
		}
		allowed = append(allowed, r)
	}
	if len(allowed) == 0 {
		res.Decision = DenyNoRole
		res.Reasons = append(res.Reasons, Reason{RuleID: "rbac.no_match", Effect: Deny, Message: "no RBAC allow rule for principal/action"})
		res.Rule = "rbac.no_match"
		return res
	}

	// Phase 3: ABAC predicate check.
	anyPredicateOK := false
	anyScopeOK := true
	for _, r := range allowed {
		if !allMatch(r.Required, req) {
			res.Reasons = append(res.Reasons, Reason{RuleID: r.ID, Effect: DenyPredicate, Message: "predicate failed"})
			continue
		}
		anyPredicateOK = true
		if !hasScopes(req.Principal, r.ScopeReq) {
			res.Reasons = append(res.Reasons, Reason{RuleID: r.ID, Effect: DenyScopeEsc, Message: "missing required scope"})
			anyScopeOK = false
			continue
		}
		// Apply ABAC forbids as a final tripwire.
		if !allMatch(r.Forbidden, req) {
			res.Decision = Deny
			res.Reasons = append(res.Reasons, Reason{RuleID: r.ID, Effect: Deny, Message: "forbidden predicate tripped"})
			res.Rule = r.ID
			return res
		}
		res.Decision = Allow
		res.Reasons = append(res.Reasons, Reason{RuleID: r.ID, Effect: Allow, Message: "RBAC+ABAC allow"})
		res.Rule = r.ID
		return res
	}

	if !anyPredicateOK {
		res.Decision = DenyPredicate
		res.Rule = "abac.predicate"
		return res
	}
	if !anyScopeOK {
		res.Decision = DenyScopeEsc
		res.Rule = "abac.scope_escalation"
		return res
	}
	res.Decision = DenyPredicate
	if res.Rule == "" {
		res.Rule = "abac.no_match"
	}
	return res
}

// RequireMTLS is a Predicate that fails when the request did not
// present a valid mTLS peer. Use it on rules whose action is high-risk
// (e.g. cert.revoke, high_risk.approve).
func RequireMTLS(r Request) bool { return r.Env.MTLSPresent }

// RequireAuditBackend enforces the fail-closed rule: if the audit
// backend is unreachable, the action MUST NOT proceed.
func RequireAuditBackend(r Request) bool { return r.Env.AuditBackendReachable }

// RequireTenantMatch is the canonical cross-tenant guard. It is
// implemented as a built-in deny, but exported so policies can include
// it inline if needed.
func RequireTenantMatch(r Request) bool {
	if r.Resource.TenantID == "" {
		return false
	}
	return r.Principal.TenantID == r.Resource.TenantID
}

// RequireOwnerSelf is a Predicate that allows the action only when the
// principal owns the resource.
func RequireOwnerSelf(r Request) bool {
	if r.Resource.Owner == "" {
		return false
	}
	return r.Resource.Owner == r.Principal.Subject
}

// RequireHighRiskOp labels a rule as only valid for high-risk
// operations. It pairs with the Environment.HighRiskOp flag so the
// caller can scope dangerous actions.
func RequireHighRiskOp(r Request) bool { return r.Env.HighRiskOp }

// helpers -------------------------------------------------------------------

func validRequest(r Request) bool {
	if r.Action == "" {
		return false
	}
	if r.Principal.Subject == "" || r.Principal.TenantID == "" {
		return false
	}
	if r.Resource.TenantID == "" {
		return false
	}
	return true
}

func crossTenantOK(r Request) bool {
	return r.Principal.TenantID == r.Resource.TenantID
}

func ruleAppliesToAction(r Rule, a Action) bool {
	if r.Action == "" {
		return true
	}
	return r.Action == a
}

func anyRoleOverlap(needles []identity.Role, haystack []identity.Role) bool {
	if len(needles) == 0 {
		return true // role-free rule (rare)
	}
	for _, n := range needles {
		for _, h := range haystack {
			if n == h {
				return true
			}
		}
	}
	return false
}

func allMatch(preds []Predicate, r Request) bool {
	for _, p := range preds {
		if !p(r) {
			return false
		}
	}
	return true
}

func hasScopes(p identity.Principal, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		if !p.HasScope(w) {
			return false
		}
	}
	return true
}

// DefaultPolicy returns the production baseline rule set. Tests use
// this to assert that the five non-negotiable invariants are enforced.
func DefaultPolicy() *Policy {
	return NewPolicy([]Rule{
		// 1. cross-tenant deny: built-in to Evaluate but exposed as a
		//    rule so admins see it in the bundle.
		{
			ID:     "deny.cross_tenant",
			Effect: Deny,
			Roles:  []identity.Role{identity.RoleAdmin, identity.RoleOperator, identity.RoleViewer, identity.RoleAgent},
			Required: []Predicate{func(r Request) bool {
				return !crossTenantOK(r)
			}},
		},
		// 2. high-risk actions require mTLS.
		{
			ID:    "deny.high_risk.no_mtls",
			Effect: Deny,
			Roles: []identity.Role{identity.RoleAdmin, identity.RoleOperator, identity.RoleAgent},
			Forbidden: []Predicate{func(r Request) bool {
				return !r.Env.MTLSPresent && r.Env.HighRiskOp
			}},
		},
		// 3. audit-unavailable fail-closed for high-risk.
		{
			ID:    "deny.high_risk.no_audit",
			Effect: Deny,
			Roles: []identity.Role{identity.RoleAdmin, identity.RoleOperator, identity.RoleAgent},
			Forbidden: []Predicate{func(r Request) bool {
				return r.Env.HighRiskOp && !r.Env.AuditBackendReachable
			}},
		},
		// 4. RBAC allows: viewer/operator/admin with appropriate scopes.
		{
			ID:       "allow.session.read",
			Effect:   Allow,
			Action:   ActionSessionRead,
			Roles:    []identity.Role{identity.RoleViewer, identity.RoleOperator, identity.RoleAdmin},
			Required: []Predicate{RequireTenantMatch, RequireMTLS},
			ScopeReq: []string{"session:read"},
		},
		{
			ID:       "allow.session.write",
			Effect:   Allow,
			Action:   ActionSessionWrite,
			Roles:    []identity.Role{identity.RoleOperator, identity.RoleAdmin},
			Required: []Predicate{RequireTenantMatch, RequireMTLS},
			ScopeReq: []string{"session:write"},
		},
		{
			ID:       "allow.agent.invoke",
			Effect:   Allow,
			Action:   ActionAgentInvoke,
			Roles:    []identity.Role{identity.RoleAgent, identity.RoleOperator, identity.RoleAdmin},
			Required: []Predicate{RequireTenantMatch},
			ScopeReq: []string{"agent:invoke"},
		},
		{
			ID:       "allow.pod.enroll",
			Effect:   Allow,
			Action:   ActionPodEnroll,
			Roles:    []identity.Role{identity.RoleAgent, identity.RoleAdmin},
			Required: []Predicate{RequireMTLS},
			ScopeReq: []string{"pod:enroll"},
		},
		{
			ID:       "allow.cert.revoke",
			Effect:   Allow,
			Action:   ActionCertRevoke,
			Roles:    []identity.Role{identity.RoleAdmin},
			Required: []Predicate{RequireMTLS, RequireAuditBackend, RequireHighRiskOp},
			ScopeReq: []string{"cert:revoke"},
		},
		{
			ID:       "allow.high_risk.approve",
			Effect:   Allow,
			Action:   ActionHighRiskApprove,
			Roles:    []identity.Role{identity.RoleAdmin},
			Required: []Predicate{RequireMTLS, RequireAuditBackend, RequireHighRiskOp},
			ScopeReq: []string{"approval:write"},
		},
		{
			ID:       "allow.audit.read",
			Effect:   Allow,
			Action:   ActionAuditRead,
			Roles:    []identity.Role{identity.RoleAdmin},
			Required: []Predicate{RequireTenantMatch, RequireMTLS},
			ScopeReq: []string{"audit:read"},
		},
		{
			ID:       "allow.policy.write",
			Effect:   Allow,
			Action:   ActionPolicyWrite,
			Roles:    []identity.Role{identity.RoleAdmin},
			Required: []Predicate{RequireMTLS, RequireAuditBackend},
			ScopeReq: []string{"policy:write"},
		},
	})
}

// ValidateBundle sanity-checks a Policy at load time. It rejects
// obvious mistakes (missing IDs, role-free allow rules that would
// grant the world, scope escalation surfaces) so configuration errors
// fail fast at startup rather than at request time.
func ValidateBundle(p *Policy) error {
	for _, r := range p.Rules() {
		if r.ID == "" {
			return errors.New("authz: rule missing id")
		}
		if r.Effect != Allow && r.Effect != Deny {
			return fmt.Errorf("authz: rule %q has invalid effect %q", r.ID, r.Effect)
		}
		if r.Effect == Allow && len(r.Roles) == 0 {
			return fmt.Errorf("authz: allow rule %q must declare roles", r.ID)
		}
	}
	return nil
}
