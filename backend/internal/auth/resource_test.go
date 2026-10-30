package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestResourceTokenTrustDomainAndRoutes(t *testing.T) {
	s, _ := NewSigner("resource-tests-local-signing-secret-32bytes", time.Hour)
	now := time.Now()
	c := ResourceClaims{TenantID: "t", WorkspaceID: "w", ParentSessionID: "s", ParentExpiresAt: now.Add(time.Minute).Unix(), Scopes: []string{"rss"}, RegisteredClaims: jwt.RegisteredClaims{Subject: "u"}}
	raw, issued, err := s.SignResource(c, now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if issued.ExpiresAt.Time.Sub(issued.IssuedAt.Time) > 30*time.Second {
		t.Fatal("unbounded credential")
	}
	if _, err = s.ParseResource(raw, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Parse(raw); err == nil {
		t.Fatal("resource credential accepted as ordinary login")
	}
	ordinary, _ := s.SignWithWorkspace("u", "admin", "w")
	if _, err = s.ParseResource(ordinary, now); err == nil {
		t.Fatal("ordinary token accepted in resource domain")
	}
	if _, err = s.ParseResource(raw, now.Add(time.Minute)); err == nil {
		t.Fatal("expired token accepted")
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/auth/me"}, {"POST", "/api/auth/refresh"}, {"POST", "/api/email/accounts"}, {"GET", "/api/email/accounts/a"}, {"POST", "/api/email/send"}, {"GET", "/api/email/oauth/start"}, {"GET", "/api/emails/a/body"}, {"GET", "/api/emails/a/attachments"}, {"GET", "/api/vault/sync/latest"}, {"POST", "/api/llm/stream"}, {"GET", "/api/rss//sources"}, {"GET", "/api/rss/../sources"},
	} {
		if got := ResourceScope(tc.method, tc.path, ""); got != "" {
			t.Fatalf("overbroad route %s %s: %s", tc.method, tc.path, got)
		}
	}
	for _, tc := range []struct{ method, path, want string }{{"GET", "/api/email/accounts", "email-metadata"}, {"GET", "/api/emails", "email-metadata"}, {"POST", "/api/embed", "embedding"}, {"GET", "/api/rss/sources", "rss"}, {"DELETE", "/api/rss/sources/abc", "rss"}, {"POST", "/api/rss/portable/item-states", "rss"}} {
		if got := ResourceScope(tc.method, tc.path, ""); got != tc.want {
			t.Fatalf("route %s %s=%s", tc.method, tc.path, got)
		}
	}
}
