package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/identity"
)

func resourceExchangeTestToken(t *testing.T, workspace string, now time.Time) string {
	t.Helper()
	path := "/internal/resource-authority/workspaces/" + workspace + "/token"
	c := resourceDelegation{TenantID: "tenant-a", WorkspaceID: workspace, SessionID: "persisted-parent", ParentExpiresAt: now.Add(time.Minute).Unix(), Scopes: []string{"rss"}, Method: "POST", Path: path, RegisteredClaims: jwt.RegisteredClaims{Issuer: "redclaw", Subject: "user-a", Audience: jwt.ClaimStrings{resourceExchangeAudience}, ID: uuid.NewString(), IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(30 * time.Second))}}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(auth.ResourceKey([]byte(authorityDelegationTestSecret), "openpocket-resource-exchange:v1"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestResourceExchangeAndPerRequestRevocation(t *testing.T) {
	s := authorityDelegationPG(t)
	s.jwtSigner, _ = auth.NewSigner("resource-local-persistent-signing-key-32bytes", time.Hour)
	if err := s.identityStore.CreateDefaultWorkspace(context.Background(), &identity.Workspace{ID: "ws", Name: "work", OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := s.identityStore.AddMember(context.Background(), &identity.Member{WorkspaceID: "ws", UserID: "user-a", Role: identity.RoleInvitee, InvitedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	var parentActive, policyAllowed atomic.Bool
	parentActive.Store(true)
	policyAllowed.Store(true)
	parent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != resourceSessionPath {
			t.Errorf("wrong introspection path %s", r.URL.Path)
		}
		c := &resourceDelegation{}
		_, err := jwt.ParseWithClaims(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), c, func(*jwt.Token) (any, error) {
			return auth.ResourceKey([]byte(authorityDelegationTestSecret), "openpocket-resource-session:v1"), nil
		}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("pocket.resource"), jwt.WithAudience("redclaw-resource-session"))
		if err != nil || c.SessionID != "persisted-parent" || len(c.Scopes) != 1 || c.Scopes[0] != "rss" {
			t.Error("invalid introspection proof")
			w.WriteHeader(401)
			return
		}
		if !parentActive.Load() {
			w.WriteHeader(401)
			return
		}
		if !policyAllowed.Load() {
			w.WriteHeader(403)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "user_id": c.Subject, "tenant_id": c.TenantID, "workspace_id": c.WorkspaceID, "parent_session_id": c.SessionID, "nonce": c.ID})
	}))
	defer parent.Close()
	t.Setenv("POCKET_RESOURCE_SESSION_AUTHORITY_URL", parent.URL)
	path := "/internal/resource-authority/workspaces/ws/token"
	bare := httptest.NewRequest("POST", path, nil)
	bare.Header.Set("Authorization", resourceExchangeTestToken(t, "ws", time.Now()))
	bareResult := httptest.NewRecorder()
	s.handleResourceAuthority(bareResult, bare)
	if bareResult.Code != 401 {
		t.Fatalf("nonbearer service credential accepted: %d", bareResult.Code)
	}
	request := func(raw string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		s.handleResourceAuthority(w, req)
		return w
	}
	delegation := resourceExchangeTestToken(t, "ws", time.Now())
	w := request(delegation)
	if w.Code != 200 {
		t.Fatalf("exchange %d %s", w.Code, w.Body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Token == "" {
		t.Fatal("missing resource token")
	}
	if replay := request(delegation); replay.Code != 401 {
		t.Fatalf("exchange replay %d", replay.Code)
	}
	called := 0
	wrapped := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called++
		c := s.claimsFromContext(r)
		if c == nil || c.UserID != "user-a" || c.WorkspaceID != "ws" || c.TenantID != "tenant-a" {
			t.Errorf("wrong resource principal %+v", c)
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	access := func(route string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", route, nil)
		req.Header.Set("Authorization", "Bearer "+out.Token)
		w := httptest.NewRecorder()
		wrapped(w, req)
		return w
	}
	if w := access("/api/rss/sources"); w.Code != 200 || called != 1 {
		t.Fatalf("resource access %d %s", w.Code, w.Body)
	}
	for _, route := range []string{"/api/auth/me", "/api/email/accounts", "/api/emails/id/body", "/api/vault/sync/latest"} {
		if w := access(route); w.Code != 403 {
			t.Fatalf("resource escaped %s: %d", route, w.Code)
		}
	}
	parentActive.Store(false)
	if w := access("/api/rss/sources"); w.Code != 401 {
		t.Fatalf("revoked parent accepted %d", w.Code)
	}
	parentActive.Store(true)
	policyAllowed.Store(false)
	if w := access("/api/rss/sources"); w.Code != 403 {
		t.Fatalf("local policy accepted %d", w.Code)
	}
	policyAllowed.Store(true)
	entered := make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 1)
	long := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		_, _ = w.Write([]byte("private-after-revoke"))
	})
	go func() {
		req := httptest.NewRequest("GET", "/api/rss/sources", nil)
		req.Header.Set("Authorization", "Bearer "+out.Token)
		w := httptest.NewRecorder()
		long(w, req)
		done <- w
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("resource handler not admitted")
	}
	parentActive.Store(false)
	select {
	case w := <-done:
		if w.Code != 409 || strings.Contains(w.Body.String(), "private-after-revoke") {
			t.Fatalf("revoked long response: %d %s", w.Code, w.Body)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("revoked long resource was not cancelled")
	}
	parentActive.Store(true)
	if err := s.identityStore.RemoveMember(context.Background(), "ws", "user-a"); err != nil {
		t.Fatal(err)
	}
	if w := access("/api/rss/sources"); w.Code != 403 {
		t.Fatalf("revoked membership accepted %d", w.Code)
	}
}

func TestResourceResponseFenceRejectsExpiredAndRevokedPublication(t *testing.T) {
	for _, explicitHeader := range []bool{false, true} {
		w := httptest.NewRecorder()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		fenced := &resourceResponseWriter{ResponseWriter: w, ctx: ctx}
		if explicitHeader {
			fenced.WriteHeader(200)
		}
		if _, err := fenced.Write([]byte("private-body")); err == nil {
			t.Fatal("expired write accepted")
		}
		fenced.publish()
		if w.Code != 409 || strings.Contains(w.Body.String(), "private-body") {
			t.Fatalf("expired success %d %s", w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	fenced := &resourceResponseWriter{ResponseWriter: w, ctx: context.Background(), validate: func() bool { return false }}
	fenced.WriteHeader(200)
	fenced.publish()
	if w.Code != 409 {
		t.Fatalf("revoked response published %d", w.Code)
	}
	w = httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	fenced = &resourceResponseWriter{ResponseWriter: w, ctx: ctx}
	fenced.WriteHeader(200)
	if _, err := fenced.Write([]byte("private-body")); err != nil {
		t.Fatal(err)
	}
	cancel()
	fenced.publish()
	if w.Code != 409 || strings.Contains(w.Body.String(), "private-body") {
		t.Fatalf("buffered response escaped revoke: %d %s", w.Code, w.Body)
	}
}

func TestResourceAuthorityFailsClosed(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.handleResourceAuthority(w, httptest.NewRequest("POST", "/internal/resource-authority/workspaces/ws/token", nil))
	if w.Code != 503 {
		t.Fatalf("unconfigured authority: %d", w.Code)
	}
	for _, value := range []string{"https://user:secret@example.com", "http://example.com", "https://example.com/path", "https://example.com/?q=secret"} {
		t.Setenv("POCKET_RESOURCE_SESSION_AUTHORITY_URL", value)
		if _, err := resourceSessionURL(); err == nil {
			t.Fatal("unsafe introspection configuration accepted")
		}
	}
}
