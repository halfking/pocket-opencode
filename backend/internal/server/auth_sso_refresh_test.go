package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

const ssoRefreshPath = "/api/auth/sso/refresh"

func TestSSORefreshRequiresBearerHeader(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	srv.cfg.RedClawSsoEnabled = true
	var calls atomic.Int32
	srv.SetRedClawAdmin(redClawRefreshTestClient(t, &calls, http.StatusOK))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, ssoRefreshPath+"?token=not-a-header", strings.NewReader(`{"workspace_id":"ws-a"}`))
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || calls.Load() != 0 {
		t.Fatalf("expected a header-only authenticated route, got HTTP %d and %d upstream calls", w.Code, calls.Load())
	}
}

func TestSSORefreshRechecksProvisionedIdentityAndMembership(t *testing.T) {
	for _, tc := range []struct {
		name          string
		oldStatus     int
		newEmployee   string
		newTenant     string
		workspace     string
		removeMember  bool
		renewalStatus int
		wantStatus    int
		wantCode      string
		wantRotations int32
		wantChecks    int32
	}{
		{name: "success", workspace: "ws-a", wantStatus: 200, wantRotations: 1, wantChecks: 2},
		{name: "other workspace", workspace: "ws-other", wantStatus: 403, wantCode: "workspace_membership_required", wantChecks: 1},
		{name: "old parent rejected", workspace: "ws-a", oldStatus: 401, wantStatus: 401, wantCode: "parent_session_invalid", wantChecks: 1},
		{name: "employee changed", workspace: "ws-a", newEmployee: "employee-b", wantStatus: 403, wantCode: "session_scope_changed", wantRotations: 1, wantChecks: 2},
		{name: "tenant changed", workspace: "ws-a", newTenant: "tenant-b", wantStatus: 403, wantCode: "session_scope_changed", wantRotations: 1, wantChecks: 2},
		{name: "membership lost in flight", workspace: "ws-a", removeMember: true, wantStatus: 403, wantCode: "workspace_membership_required", wantRotations: 1, wantChecks: 2},
		{name: "stale bearer", workspace: "ws-a", renewalStatus: 409, wantStatus: 409, wantCode: "stale_session_token", wantRotations: 1, wantChecks: 1},
		{name: "upstream rejects grant", workspace: "ws-a", renewalStatus: 401, wantStatus: 401, wantCode: "reauth_required", wantRotations: 1, wantChecks: 1},
		{name: "upstream unavailable", workspace: "ws-a", renewalStatus: 502, wantStatus: 503, wantCode: CodeUpstreamUnavailable, wantRotations: 1, wantChecks: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, pool := authorityDelegationPGWithPool(t)
			srv.cfg.RedClawSsoEnabled = true
			err := srv.identityStore.CreateDefaultWorkspace(t.Context(), &identity.Workspace{ID: "ws-a", Name: "Test workspace", OwnerID: "employee-a"})
			if err != nil {
				t.Fatal(err)
			}
			var checks, rotations atomic.Int32
			admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				checks.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/auth/me" || r.URL.Query().Get("require_provisioned") != "1" {
					t.Error("renewal used a route that does not require an employee mapping")
					w.WriteHeader(404)
					return
				}
				employee, tenant := "employee-a", "tenant-a"
				switch r.Header.Get("Authorization") {
				case "Bearer original-platform-bearer":
					if tc.oldStatus != 0 {
						w.WriteHeader(tc.oldStatus)
						return
					}
				case "Bearer renewed-platform-bearer":
					if tc.newEmployee != "" {
						employee = tc.newEmployee
					}
					if tc.newTenant != "" {
						tenant = tc.newTenant
					}
				default:
					w.WriteHeader(401)
					return
				}
				_ = json.NewEncoder(w).Encode(redclaw.EmployeeInfo{ID: employee, TenantID: tenant, Provisioned: true, Name: "Employee A"})
			}))
			defer admin.Close()
			agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rotations.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sso/refresh" ||
					r.Header.Get("Authorization") != "Bearer original-platform-bearer" || r.Header.Get("X-Tenant-ID") != "tenant-a" {
					t.Error("invalid authority relay")
					w.WriteHeader(400)
					return
				}
				if tc.renewalStatus != 0 {
					w.WriteHeader(tc.renewalStatus)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "stale_session_token"}})
					return
				}
				if tc.removeMember {
					if _, err := pool.Exec(r.Context(), "DELETE FROM workspace_members WHERE workspace_id = 'ws-a' AND user_id = 'employee-a'"); err != nil {
						t.Errorf("remove workspace member: %v", err)
						w.WriteHeader(500)
						return
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"jwt": "renewed-platform-bearer"})
			}))
			defer agent.Close()
			client, err := redclaw.NewAdminAuthClient(redclaw.AdminAuthClientConfig{
				AdminURL: admin.URL, AuthAgentURL: agent.URL, TenantID: "tenant-a", Secret: "isolated-test-key",
			})
			if err != nil {
				t.Fatal(err)
			}
			srv.SetRedClawAdmin(client)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, ssoRefreshPath, strings.NewReader(`{"workspace_id":"`+tc.workspace+`","user_id":"untrusted"}`))
			r.Header.Set("Authorization", "Bearer original-platform-bearer")
			srv.Handler().ServeHTTP(w, r)
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("HTTP %d did not return JSON: %v", w.Code, err)
			}
			if w.Code != tc.wantStatus || rotations.Load() != tc.wantRotations || checks.Load() != tc.wantChecks {
				t.Fatalf("HTTP %d, renewals %d, employee checks %d", w.Code, rotations.Load(), checks.Load())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("renewal requires no-store")
			}
			if tc.wantStatus != 200 {
				if body["code"] != tc.wantCode || body["token"] != nil {
					t.Fatalf("unexpected error contract: %#v", body)
				}
				return
			}
			if body["token"] != "renewed-platform-bearer" || body["user_id"] != "employee-a" ||
				body["workspace_id"] != "ws-a" || body["auth_method"] != "redclaw-oidc" {
				t.Fatal("renewal did not return the same authenticated scope")
			}
		})
	}
}
