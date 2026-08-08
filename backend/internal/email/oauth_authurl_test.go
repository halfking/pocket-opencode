package email

import (
	"net/url"
	"strings"
	"testing"
)

func testPKCE(t *testing.T) *PKCEPair {
	t.Helper()
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	return pkce
}

// The authorization URL must carry the caller's real client_id. An empty
// client_id is rejected by Google/Outlook, so building such a URL is a bug.
func TestBuildAuthURLIncludesClientID(t *testing.T) {
	for _, providerID := range []string{"google", "outlook"} {
		provider, ok := LookupProviderByID(providerID)
		if !ok || !provider.SupportsOAuth2 {
			t.Skipf("provider %s not available for OAuth2", providerID)
		}

		raw, err := BuildAuthURL(provider, "client-abc", "https://app.example.com/cb", "state-xyz", testPKCE(t))
		if err != nil {
			t.Fatalf("%s: BuildAuthURL: %v", providerID, err)
		}
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s: parse auth url: %v", providerID, err)
		}
		q := u.Query()
		if got := q.Get("client_id"); got != "client-abc" {
			t.Fatalf("%s: client_id = %q, want client-abc", providerID, got)
		}
		if got := q.Get("redirect_uri"); got != "https://app.example.com/cb" {
			t.Fatalf("%s: redirect_uri = %q", providerID, got)
		}
		if got := q.Get("state"); got != "state-xyz" {
			t.Fatalf("%s: state = %q", providerID, got)
		}
		if got := q.Get("code_challenge_method"); got != "S256" {
			t.Fatalf("%s: code_challenge_method = %q", providerID, got)
		}
		if q.Get("code_challenge") == "" {
			t.Fatalf("%s: code_challenge must be set", providerID)
		}
	}
}

func TestBuildAuthURLRejectsEmptyClientID(t *testing.T) {
	provider, ok := LookupProviderByID("google")
	if !ok {
		t.Skip("google provider not available")
	}
	_, err := BuildAuthURL(provider, "", "https://app.example.com/cb", "state-xyz", testPKCE(t))
	if err == nil {
		t.Fatal("expected an error for an empty clientID")
	}
	if !strings.Contains(err.Error(), "clientID") {
		t.Fatalf("error should name the missing clientID, got %v", err)
	}
}
