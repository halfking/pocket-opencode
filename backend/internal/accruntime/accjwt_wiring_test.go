package accruntime

// accjwt_wiring_test.go — the ACC Runtime client must actually be able to
// talk to ACC.
//
// The regression behind this (2026-10-22): ACC's /api/v2/* surface
// accepts ONLY an HS256-signed service JWT. A static bearer token is
// answered with 401 "invalid bearer token". openpocket's acchttp could
// only send a static token, and nothing in the production wiring ever
// constructed an accruntime.Client at all — so the Pocket↔ACC approval
// interlock and the ACC-bound task cancel path were unreachable.
//
// This test crosses the package boundaries that matter: accjwt signs,
// acchttp carries the header, accruntime builds the URL. It asserts the
// bytes that actually reach the wire.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/acchttp"
	"github.com/halfking/pocket-opencode/backend/internal/accjwt"
)

func TestClientSendsSignedServiceToken(t *testing.T) {
	var capturedAuth, capturedPath, capturedMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedPath = r.URL.Path
		capturedMethod = r.Method
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"approvals":[],"count":0,"ok":true}`)
	}))
	defer ts.Close()

	c, err := New(acchttp.Config{
		BaseURL: ts.URL,
		Signer: accjwt.NewSigner(accjwt.Options{
			Secret:           "control-secret",
			Subject:          "pocketd:test",
			TenantID:         "tenant-a",
			ServiceIdentity:  true,
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	approvals, err := c.ListApprovals(context.Background())
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if approvals == nil {
		t.Fatal("ListApprovals returned nil")
	}
	if capturedMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", capturedMethod)
	}
	if capturedPath != "/api/v2/approvals" {
		t.Errorf("path = %s, want /api/v2/approvals", capturedPath)
	}
	if !strings.HasPrefix(capturedAuth, "Bearer ") {
		t.Fatalf("Authorization = %q, want a Bearer token", capturedAuth)
	}
	parts := strings.Split(strings.TrimPrefix(capturedAuth, "Bearer "), ".")
	if len(parts) != 3 {
		t.Fatalf("Authorization is not a JWT: %q", capturedAuth)
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		t.Fatalf("parse claims: %v", err)
	}
	if claims["type"] != "agent" {
		t.Errorf("claim type = %v, want agent (a service identity)", claims["type"])
	}
	if claims["tenant_id"] != "tenant-a" {
		t.Errorf("claim tenant_id = %v, want tenant-a", claims["tenant_id"])
	}
}

// A client with NO signer must not silently send an empty Authorization
// header: that would be an unauthenticated request to a fail-closed
// permission gate.
func TestClientWithoutSignerSendsNoAuthorizationHeader(t *testing.T) {
	var hadAuth bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"approvals":[],"count":0,"ok":true}`)
	}))
	defer ts.Close()

	c, err := New(acchttp.Config{BaseURL: ts.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.ListApprovals(context.Background()); err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if hadAuth {
		t.Error("an Authorization header was sent without a configured credential")
	}
}
