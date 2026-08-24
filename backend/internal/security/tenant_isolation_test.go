// TM-01 / TM-02 / TM-07 — cross-tenant and vertical RBAC tests.
// These tests run a real httptest.Server with the production-shaped
// middleware stack and issue real HTTP requests.
package security

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// tenantIsolatedTaskID is seeded into the test rig as a "tenant B" task.
// Any request that tries to read it as tenant A must be denied.
const tenantIsolatedTaskID = "task-in-tenant-B"

func TestCrossTenantAccessForbidden(t *testing.T) {
	rig := newRig(t)

	// Seed tenant B's task store.
	rig.tasks.insert(&task{
		ID:        tenantIsolatedTaskID,
		Tenant:    "ws-B",
		Workspace: "ws-B",
		Owner:     "user-B",
		Status:    "running",
	})

	// Tenant A's user holds operator role on ws-A.
	now := time.Now().UTC()
	tokA := rig.mintToken(jwsPayload{
		Issuer:    "pocket",
		Audience:  []string{"zagent-gateway"},
		Subject:   "user-A",
		TenantID:  "ws-A",
		Roles:     []string{"operator"},
		Scopes:    []string{"tasks.read", "tasks.write"},
		JTI:       newJTI(),
		IssuedAt:  now,
		ExpiresAt: now.Add(5 * time.Minute),
	})

	resp := rig.authedRequest(http.MethodGet,
		"/api/v1/tasks/"+tenantIsolatedTaskID+"?workspace=ws-B",
		tokA, nil, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "cross_tenant_denied") {
		t.Fatalf("expected cross_tenant_denied error code, got: %s", body)
	}

	// Audit: deny entry must be recorded for this request.
	var found bool
	for _, e := range rig.gate.snapshot() {
		if e.Principal == "user-A" &&
			e.Tenant == "ws-A" &&
			e.Verb == "read" &&
			e.Decision == "deny" &&
			strings.Contains(e.Reason, "cross_tenant_denied") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected cross_tenant audit entry, got: %+v", rig.gate.snapshot())
	}
}

func TestVerticalRBACBlocksApprove(t *testing.T) {
	rig := newRig(t)

	// Seed a permission request owned by ws-A. Viewer user is in ws-A but
	// must NOT be allowed to approve.
	rig.mu.Lock()
	rig.perms["perm-1"] = &permissionRequest{
		ID:       "perm-1",
		Tenant:   "ws-A",
		Resource: "task-1",
		Tool:     "git.push",
	}
	rig.mu.Unlock()

	now := time.Now().UTC()
	viewerTok := rig.mintToken(jwsPayload{
		Issuer:    "pocket",
		Audience:  []string{"zagent-gateway"},
		Subject:   "user-V",
		TenantID:  "ws-A",
		Roles:     []string{"viewer"},
		Scopes:    []string{"permissions.read"},
		JTI:       newJTI(),
		IssuedAt:  now,
		ExpiresAt: now.Add(5 * time.Minute),
	})

	resp := rig.authedRequest(http.MethodPost,
		"/api/v1/permissions/perm-1/reply",
		viewerTok,
		map[string]string{"Idempotency-Key": "idem-1", "X-Request-Id": "req-1"},
		`{"decision":"allow_once"}`,
	)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 from viewer approve, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "role_forbidden") {
		t.Fatalf("expected role_forbidden code, got: %s", body)
	}

	// Confirm no side effect: the permission request was NOT consumed.
	rig.mu.Lock()
	stillPending := rig.perms["perm-1"]
	rig.mu.Unlock()
	if stillPending == nil {
		t.Fatalf("permission request should still exist")
	}
}

func TestWorkspaceScopeBlocksForeignWS(t *testing.T) {
	rig := newRig(t)
	now := time.Now().UTC()

	// Operator scoped to ws-A only.
	tok := rig.mintToken(jwsPayload{
		Issuer:    "pocket",
		Audience:  []string{"zagent-gateway"},
		Subject:   "user-O",
		TenantID:  "ws-A",
		Roles:     []string{"operator"},
		Scopes:    []string{"agents.read"},
		JTI:       newJTI(),
		IssuedAt:  now,
		ExpiresAt: now.Add(5 * time.Minute),
	})

	// We test the policy.evaluate directly for ABAC workspace scope.
	p := &principal{
		Subject:  tok.Payload.Subject,
		TenantID: tok.Payload.TenantID,
		Roles:    tok.Payload.Roles,
		Scopes:   tok.Payload.Scopes,
	}
	dec := evaluate(p, "list", "agent", "ws-A", "ws-B", "", ownedWorkspaces{"ws-A"})
	if dec.Allow {
		t.Fatalf("expected deny, got allow")
	}
	if dec.Reason != "workspace_forbidden" {
		t.Fatalf("expected workspace_forbidden, got %q", dec.Reason)
	}
}

// TestCrossTenantViaPathParam ensures path parameter alone cannot bypass
// the tenant scope check. The handler must always verify the resource's
// tenant matches the caller's tenant.
func TestCrossTenantViaPathParam(t *testing.T) {
	rig := newRig(t)
	now := time.Now().UTC()

	// Tenant A user.
	tok := rig.mintToken(jwsPayload{
		Issuer:    "pocket",
		Audience:  []string{"zagent-gateway"},
		Subject:   "user-A",
		TenantID:  "ws-A",
		Roles:     []string{"operator"},
		Scopes:    []string{"tasks.read"},
		JTI:       newJTI(),
		IssuedAt:  now,
		ExpiresAt: now.Add(5 * time.Minute),
	})

	// Attempt to read a task without specifying workspace — must still be
	// denied because the resource belongs to ws-B.
	rig.tasks.insert(&task{
		ID:        "task-foreign",
		Tenant:    "ws-B",
		Workspace: "ws-B",
		Owner:     "user-B",
	})

	resp := rig.authedRequest(http.MethodGet,
		"/api/v1/tasks/task-foreign",
		tok, nil, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403, got %d: %s", resp.StatusCode, body)
	}
}

// TestAuditDeniesIncludeReason shows the deny audit payload carries the
// machine-readable reason required by ADR-0003 §7.
func TestAuditDeniesIncludeReason(t *testing.T) {
	rig := newRig(t)
	now := time.Now().UTC()

	tok := rig.mintToken(jwsPayload{
		Issuer:    "pocket",
		Audience:  []string{"zagent-gateway"},
		Subject:   "user-X",
		TenantID:  "ws-A",
		Roles:     []string{"viewer"},
		Scopes:    []string{"permissions.read"},
		JTI:       newJTI(),
		IssuedAt:  now,
		ExpiresAt: now.Add(5 * time.Minute),
	})

	resp := rig.authedRequest(http.MethodPost,
		"/api/v1/permissions/perm-2/reply",
		tok,
		map[string]string{"Idempotency-Key": "idem-2", "X-Request-Id": "req-2"},
		`{"decision":"allow_once"}`,
	)
	defer resp.Body.Close()

	var found auditEntry
	for _, e := range rig.gate.snapshot() {
		if e.Principal == "user-X" && e.Decision == "deny" {
			found = e
			break
		}
	}
	if found.RequestID != "req-2" {
		t.Fatalf("expected request_id propagation on deny audit, got %q", found.RequestID)
	}
	if found.Reason == "" {
		t.Fatalf("deny audit must carry a machine-readable reason")
	}
}

// TestSuccessfulReadSameTenant ensures the happy path (own tenant, owner)
// returns 200 and is recorded as allow.
func TestSuccessfulReadSameTenant(t *testing.T) {
	rig := newRig(t)
	now := time.Now().UTC()

	rig.tasks.insert(&task{
		ID:        "task-self",
		Tenant:    "ws-A",
		Workspace: "ws-A",
		Owner:     "user-S",
		Status:    "running",
	})
	tok := rig.mintToken(jwsPayload{
		Issuer:    "pocket",
		Audience:  []string{"zagent-gateway"},
		Subject:   "user-S",
		TenantID:  "ws-A",
		Roles:     []string{"operator"},
		Scopes:    []string{"tasks.read"},
		JTI:       newJTI(),
		IssuedAt:  now,
		ExpiresAt: now.Add(5 * time.Minute),
	})
	resp := rig.authedRequest(http.MethodGet, "/api/v1/tasks/task-self", tok, nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var got task
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.ID != "task-self" || got.Owner != "user-S" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}
