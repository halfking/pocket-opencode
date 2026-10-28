package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

func redClawRefreshTestClient(t *testing.T, calls *atomic.Int32, status int) *redclaw.AdminAuthClient {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/auth/me" {
			t.Errorf("unexpected upstream route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "employee-a", "name": "Alice"})
	}))
	t.Cleanup(upstream.Close)
	client, err := redclaw.NewAdminAuthClient(redclaw.AdminAuthClientConfig{
		AdminURL: upstream.URL, Secret: "test-secret-at-least-32-bytes-long", TenantID: "tenant-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestAuthRefreshRequiresSignInForUpstreamCredential(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	var upstreamCalls atomic.Int32
	srv.SetRedClawAdmin(redClawRefreshTestClient(t, &upstreamCalls, http.StatusOK))
	req := mobileRequest(http.MethodPost, "/api/auth/refresh", "upstream-credential", "")
	// The handler receives authenticated identity from middleware. A successful
	// upstream Me response must not be treated as permission to mint a pocket JWT.
	req = req.WithContext(context.WithValue(req.Context(), authClaimsContextKey{}, &authClaims{
		UserID: "employee-a", WorkspaceID: "ws-a", TenantID: "tenant-a",
	}))
	rr := httptest.NewRecorder()
	srv.handleAuthRefresh(rr, req)
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusUnauthorized || body["code"] != "reauth_required" {
		t.Fatalf("want 401 reauth_required, got %d", rr.Code)
	}
	if _, present := body["token"]; present {
		t.Fatal("upstream refresh must not return a replacement credential")
	}
	if upstreamCalls.Load() != 1 {
		t.Fatal("upstream revocation must still be checked once without logout")
	}
}

func TestAuthRefreshKeepsLocalDevAndLegacyIssuer(t *testing.T) {
	srv, _, signer, tokens := newMobileRouteServer(t)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, mobileRequest(http.MethodPost, "/api/auth/refresh", tokens["ws-a"], ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("local refresh failed: %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	claims, err := signer.Parse(body["token"])
	if err != nil || claims.UserID != "user-wsa" {
		t.Fatalf("local refresh changed identity or issuer: claims=%+v err=%v", claims, err)
	}
	if srv.redclawAdminClient != nil {
		t.Fatal("precondition: legacy/dev mode has no upstream")
	}
}

func TestAuthRefreshDoesNotExemptPreviouslyConvertedPocketToken(t *testing.T) {
	for _, upstreamStatus := range []int{http.StatusOK, http.StatusUnauthorized} {
		t.Run(http.StatusText(upstreamStatus), func(t *testing.T) {
			srv, _, _, tokens := newMobileRouteServer(t)
			var upstreamCalls atomic.Int32
			srv.SetRedClawAdmin(redClawRefreshTestClient(t, &upstreamCalls, upstreamStatus))
			// A historical buggy refresh produced the same pocket token shape as
			// local login. Its issuer cannot exempt it from upstream revocation.
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, mobileRequest(http.MethodPost, "/api/auth/refresh", tokens["ws-a"], ""))
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("historical token renewed: status=%d", rr.Code)
			}
			var body map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if _, present := body["token"]; present {
				t.Fatal("historical credential must not be renewed")
			}
			if upstreamCalls.Load() != 1 {
				t.Fatal("historical credential must be checked with upstream")
			}
		})
	}
}
