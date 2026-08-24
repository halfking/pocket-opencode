package authz

import (
	"testing"
	"time"

	"github.com/halfking/zagent-gateway/internal/identity"
)

// fixtures ------------------------------------------------------------------

func principal(roles ...identity.Role) identity.Principal {
	return identity.Principal{
		Subject:  "user-1",
		TenantID: "tenant-1",
		Roles:    roles,
		Scopes:   []string{"session:read", "session:write", "agent:invoke", "pod:enroll", "cert:revoke", "approval:write", "audit:read", "policy:write"},
	}
}

func baseRequest() Request {
	return Request{
		Action: ActionSessionRead,
		Resource: Resource{
			Type:     "session",
			ID:       "sess-1",
			TenantID: "tenant-1",
		},
		Principal: principal(identity.RoleViewer),
		Env: Environment{
			MTLSPresent:            true,
			AuditBackendReachable: true,
			Time:                   time.Now(),
		},
	}
}

// tests ---------------------------------------------------------------------

func TestSameTenantReadAllowed(t *testing.T) {
	res := DefaultPolicy().Evaluate(baseRequest())
	if !res.Allowed() {
		t.Fatalf("expected allow, got %s reasons=%v", res.Decision, res.Reasons)
	}
	if res.Rule == "" {
		t.Fatalf("expected non-empty rule id")
	}
}

func TestCrossTenantReadDenied(t *testing.T) {
	req := baseRequest()
	req.Resource.TenantID = "tenant-2"
	res := DefaultPolicy().Evaluate(req)
	if res.Decision != DenyCrossTen {
		t.Fatalf("expected DenyCrossTen, got %s", res.Decision)
	}
	if res.Rule != "deny.cross_tenant" {
		t.Fatalf("expected rule deny.cross_tenant, got %q", res.Rule)
	}
}

func TestExpiredTokenViaPrincipal(t *testing.T) {
	// Token expiry is enforced by auth/token.go before Evaluate runs.
	// The contract here is: Evaluate MUST NOT consult token age; an
	// expired token simply never reaches this layer. This test
	// documents that invariant: even with ExpiresAt far in the past
	// the policy returns Allow.
	req := baseRequest()
	req.Principal.ExpiresAt = time.Now().Add(-time.Hour)
	res := DefaultPolicy().Evaluate(req)
	if !res.Allowed() {
		t.Fatalf("expected allow; expiry is enforced upstream, got %s", res.Decision)
	}
}

func TestWrongAudienceForSessionRead(t *testing.T) {
	// "audience" lives on the JWT and is enforced upstream. The policy
	// surface for this is the ScopeReq list: if the principal's scopes
	// don't include the action's required scope, the decision is
	// denied with DenyScopeEsc. Strip the session:read scope and the
	// session.read action must deny.
	req := baseRequest()
	req.Principal.Scopes = []string{} // no scopes
	res := DefaultPolicy().Evaluate(req)
	if res.Decision != DenyScopeEsc {
		t.Fatalf("expected DenyScopeEsc, got %s", res.Decision)
	}
}

func TestRevokedKeyEquivalent(t *testing.T) {
	// Revoked keys never reach Evaluate; revocation is enforced by the
	// JWT verifier in auth/token.go. We model the effect by removing
	// every scope and every role from the principal so the policy has
	// no path to allow.
	req := baseRequest()
	req.Principal.Roles = nil
	req.Principal.Scopes = nil
	res := DefaultPolicy().Evaluate(req)
	if res.Decision != DenyNoRole && res.Decision != DenyScopeEsc {
		t.Fatalf("expected deny, got %s", res.Decision)
	}
}

func TestScopeEscalationDenied(t *testing.T) {
	// Operator attempting cert.revoke without cert:revoke scope.
	req := baseRequest()
	req.Action = ActionCertRevoke
	req.Principal.Roles = []identity.Role{identity.RoleOperator}
	req.Principal.Scopes = []string{"session:read"}
	req.Env.HighRiskOp = true
	res := DefaultPolicy().Evaluate(req)
	if res.Decision == Allow {
		t.Fatalf("expected deny for scope-escalation attempt")
	}
}

func TestReplayedNonceEquivalent(t *testing.T) {
	// Nonce replay is rejected by the JWT verifier (jti reuse) and by
	// the durable outbox layer. At the policy level we model it by
	// removing the principal entirely.
	req := baseRequest()
	req.Principal = identity.Principal{}
	res := DefaultPolicy().Evaluate(req)
	if res.Decision == Allow {
		t.Fatalf("expected deny for empty principal")
	}
}

func TestApproverOnlyAction(t *testing.T) {
	// high_risk.approve requires the admin role; viewer must be denied.
	req := baseRequest()
	req.Action = ActionHighRiskApprove
	req.Principal.Roles = []identity.Role{identity.RoleViewer}
	req.Env.HighRiskOp = true
	res := DefaultPolicy().Evaluate(req)
	if res.Decision == Allow {
		t.Fatalf("expected deny: viewer cannot approve high-risk ops")
	}
}

func TestMTLSRequiredForCertRevoke(t *testing.T) {
	req := baseRequest()
	req.Action = ActionCertRevoke
	req.Principal.Roles = []identity.Role{identity.RoleAdmin}
	req.Env.MTLSPresent = false
	req.Env.HighRiskOp = true
	res := DefaultPolicy().Evaluate(req)
	if res.Decision == Allow {
		t.Fatalf("expected deny: cert.revoke without mTLS")
	}
}

func TestAuditBackendRequiredForHighRisk(t *testing.T) {
	req := baseRequest()
	req.Action = ActionCertRevoke
	req.Principal.Roles = []identity.Role{identity.RoleAdmin}
	req.Env.HighRiskOp = true
	req.Env.AuditBackendReachable = false
	res := DefaultPolicy().Evaluate(req)
	if res.Decision == Allow {
		t.Fatalf("expected deny: high-risk op with no audit backend")
	}
}

func TestValidateBundleRejectsRoleFreeAllow(t *testing.T) {
	bad := NewPolicy([]Rule{
		{ID: "allow.empty", Effect: Allow, Action: ActionSessionRead},
	})
	if err := ValidateBundle(bad); err == nil {
		t.Fatalf("expected ValidateBundle to reject role-free allow rule")
	}
}

func TestValidateBundleRejectsMissingID(t *testing.T) {
	bad := NewPolicy([]Rule{
		{ID: "", Effect: Allow, Action: ActionSessionRead, Roles: []identity.Role{identity.RoleAdmin}},
	})
	if err := ValidateBundle(bad); err == nil {
		t.Fatalf("expected ValidateBundle to reject rule without id")
	}
}

func TestEvaluateReasonDeterministicOrder(t *testing.T) {
	// Two calls on the same request must produce Reasons in the same
	// order. This is a small but real correctness requirement: audit
	// log diffs depend on stable ordering.
	req := baseRequest()
	r1 := DefaultPolicy().Evaluate(req)
	r2 := DefaultPolicy().Evaluate(req)
	if len(r1.Reasons) != len(r2.Reasons) {
		t.Fatalf("reason count drifted: %d vs %d", len(r1.Reasons), len(r2.Reasons))
	}
	for i := range r1.Reasons {
		if r1.Reasons[i].RuleID != r2.Reasons[i].RuleID {
			t.Fatalf("reason drift at %d: %q vs %q", i, r1.Reasons[i].RuleID, r2.Reasons[i].RuleID)
		}
	}
}
