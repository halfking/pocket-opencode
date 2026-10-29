package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/jackc/pgx/v5/pgxpool"
)

const authorityDelegationTestSecret = "delegation-contract-test-secret-32-bytes"
const authorityTestPath = "/internal/workspace-authority/workspaces"

func authorityDelegationToken(t *testing.T, user, tenant, path string) string {
	t.Helper()
	now := time.Now()
	c := jwt.MapClaims{"iss": "redclaw", "aud": "pocket-workspace-authority", "sub": user, "tenant_id": tenant, "scope": "workspace-membership:read", "act": "redclaw.admin", "method": "GET", "path": path, "jti": uuid.NewString(), "iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(30 * time.Second).Unix()}
	mac := hmac.New(sha256.New, []byte(authorityDelegationTestSecret))
	_, _ = mac.Write([]byte("openpocket-workspace-delegation:v1"))
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(mac.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func authorityDelegationPG(t *testing.T) *Server {
	t.Helper()
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "workspace_authority_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		root.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(); _, _ = root.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); root.Close() })
	store, err := identity.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{identityStore: store}
	s.cfg.RedClawTenantID = "tenant-a"
	t.Setenv("IDENTITY_SHARED_SECRET", authorityDelegationTestSecret)
	return s
}

func authorityDelegationDo(s *Server, path, raw string) *httptest.ResponseRecorder {
	u, _ := url.Parse(path)
	r := httptest.NewRequest(http.MethodGet, u.String(), nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	s.handleWorkspaceAuthority(w, r)
	return w
}

func TestWorkspaceAuthorityDelegationChecksMembersAndReplay(t *testing.T) {
	s := authorityDelegationPG(t)
	ctx := context.Background()
	ws := "ws_用户_研发 空间🚀"
	if err := s.identityStore.CreateDefaultWorkspace(ctx, &identity.Workspace{ID: ws, Name: "研发", OwnerID: "user-a"}); err != nil {
		t.Fatal(err)
	}
	listToken := authorityDelegationToken(t, "user-a", "tenant-a", authorityTestPath)
	w := authorityDelegationDo(s, authorityTestPath, listToken)
	if w.Code != 200 || !strings.Contains(w.Body.String(), ws) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if again := authorityDelegationDo(s, authorityTestPath, listToken); again.Code != 401 {
		t.Fatalf("replay: %d %s", again.Code, again.Body)
	}
	path := authorityTestPath + "/" + ws + "/binding"
	w = authorityDelegationDo(s, path, authorityDelegationToken(t, "user-a", "tenant-a", path))
	if w.Code != 200 {
		t.Fatalf("binding: %d %s", w.Code, w.Body)
	}
	var out struct {
		Binding string `json:"binding"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	signer, err := identity.NewBindingSigner()
	if err != nil {
		t.Fatal(err)
	}
	assertion, err := signer.Verify(out.Binding)
	if err != nil || assertion.UserID != "user-a" || assertion.WorkspaceID != ws || assertion.TenantID != "tenant-a" {
		t.Fatalf("binding assertion: %+v %v", assertion, err)
	}
	for _, tc := range []struct {
		user, tenant string
		status       int
	}{{"user-b", "tenant-a", 403}, {"user-a", "tenant-b", 401}} {
		w = authorityDelegationDo(s, path, authorityDelegationToken(t, tc.user, tc.tenant, path))
		if w.Code != tc.status {
			t.Fatalf("isolation %s/%s: %d %s", tc.user, tc.tenant, w.Code, w.Body)
		}
	}
	if err := s.identityStore.AddMember(ctx, &identity.Member{WorkspaceID: ws, UserID: "user-b", Role: identity.RoleInvitee, InvitedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	w = authorityDelegationDo(s, path, authorityDelegationToken(t, "user-b", "tenant-a", path))
	if w.Code != 200 {
		t.Fatalf("invited member: %d %s", w.Code, w.Body)
	}
	if err := s.identityStore.RemoveMember(ctx, ws, "user-b"); err != nil {
		t.Fatal(err)
	}
	w = authorityDelegationDo(s, path, authorityDelegationToken(t, "user-b", "tenant-a", path))
	if w.Code != 403 {
		t.Fatalf("revoked member: %d %s", w.Code, w.Body)
	}
	if err := s.identityStore.AddMember(ctx, &identity.Member{WorkspaceID: ws, UserID: "expired", Role: identity.RoleInvitee, ExpiresAt: time.Now().Add(-time.Second).Unix()}); err != nil {
		t.Fatal(err)
	}
	w = authorityDelegationDo(s, authorityTestPath, authorityDelegationToken(t, "expired", "tenant-a", authorityTestPath))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"workspaces":[]`) {
		t.Fatalf("expired discovery: %d %s", w.Code, w.Body)
	}
	w = authorityDelegationDo(s, path, authorityDelegationToken(t, "expired", "tenant-a", path))
	if w.Code != 403 {
		t.Fatalf("expired binding: %d %s", w.Code, w.Body)
	}
}

func TestWorkspaceAuthorityDelegationFailsClosedAndBindsPath(t *testing.T) {
	s := authorityDelegationPG(t)
	for _, tc := range []struct {
		name, path, tokenPath, tenant string
		code                          int
	}{
		{"changed path", authorityTestPath + "/ws-a/binding", authorityTestPath, "tenant-a", 401},
		{"query", authorityTestPath + "?workspace_id=ws-a", authorityTestPath, "tenant-a", 400},
		{"wrong tenant", authorityTestPath, authorityTestPath, "other", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := authorityDelegationDo(s, tc.path, authorityDelegationToken(t, "user-a", tc.tenant, tc.tokenPath))
			if w.Code != tc.code {
				t.Fatalf("code=%d %s", w.Code, w.Body)
			}
		})
	}
	s.cfg.RedClawTenantID = ""
	w := authorityDelegationDo(s, authorityTestPath, authorityDelegationToken(t, "user-a", "tenant-a", authorityTestPath))
	if w.Code != 503 {
		t.Fatalf("missing tenant: %d %s", w.Code, w.Body)
	}
	s.cfg.RedClawTenantID = "tenant-a"
	t.Setenv("IDENTITY_SHARED_SECRET", "")
	w = authorityDelegationDo(s, authorityTestPath, "not-a-token")
	if w.Code != 503 {
		t.Fatalf("missing trust: %d %s", w.Code, w.Body)
	}
}

func TestWorkspaceAuthorityDelegationIsNotAnOrdinaryBearer(t *testing.T) {
	t.Setenv("IDENTITY_SHARED_SECRET", authorityDelegationTestSecret)
	s, _, _, _ := newMobileRouteServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	r.Header.Set("Authorization", "Bearer "+authorityDelegationToken(t, "user-a", "tenant-a", authorityTestPath))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(fmt.Sprintf("delegation accepted by ordinary API: %d %s", w.Code, w.Body))
	}
}
