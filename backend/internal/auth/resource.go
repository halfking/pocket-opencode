package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const ResourceAudience = "pocket-resource-api"
const ResourceIssuer = "pocket.resource"
const ResourceMaxTTL = 30 * time.Second

type ResourceClaims struct {
	TenantID        string   `json:"tenant_id"`
	WorkspaceID     string   `json:"workspace_id"`
	ParentSessionID string   `json:"parent_session_id"`
	ParentExpiresAt int64    `json:"parent_expires_at"`
	Scopes          []string `json:"scopes"`
	jwt.RegisteredClaims
}

func ResourceKey(secret []byte, context string) []byte {
	m := hmac.New(sha256.New, secret)
	_, _ = m.Write([]byte(context))
	return m.Sum(nil)
}

func CanonicalResourceScopes(in []string) ([]string, error) {
	if len(in) == 0 || len(in) > 3 {
		return nil, errors.New("invalid resource scopes")
	}
	out := append([]string(nil), in...)
	seen := map[string]bool{}
	for _, s := range out {
		if (s != "rss" && s != "email-metadata" && s != "embedding") || seen[s] {
			return nil, errors.New("invalid resource scopes")
		}
		seen[s] = true
	}
	sort.Strings(out)
	return out, nil
}

func (s *Signer) SignResource(c ResourceClaims, expiry, now time.Time) (string, *ResourceClaims, error) {
	scopes, err := CanonicalResourceScopes(c.Scopes)
	if s == nil || err != nil || !resourceCanonical(c.Subject) || !resourceCanonical(c.TenantID) || !resourceCanonical(c.WorkspaceID) || !resourceCanonical(c.ParentSessionID) || !expiry.After(now) || c.ParentExpiresAt <= now.Unix() {
		return "", nil, errors.New("invalid resource principal")
	}
	if max := now.Add(ResourceMaxTTL); expiry.After(max) {
		expiry = max
	}
	if expiry.Unix() > c.ParentExpiresAt {
		expiry = time.Unix(c.ParentExpiresAt, 0)
	}
	if expiry.Unix() <= now.Unix() {
		return "", nil, errors.New("resource session expires too soon")
	}
	c.Scopes = scopes
	c.RegisteredClaims = jwt.RegisteredClaims{Issuer: ResourceIssuer, Subject: c.Subject, Audience: jwt.ClaimStrings{ResourceAudience}, ID: uuid.NewString(), IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(expiry)}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(ResourceKey(s.secret, "openpocket-resource-token:v1"))
	return raw, &c, err
}

func (s *Signer) ParseResource(raw string, now time.Time) (*ResourceClaims, error) {
	if s == nil {
		return nil, errors.New("resource signer unavailable")
	}
	c := &ResourceClaims{}
	_, err := jwt.ParseWithClaims(raw, c, func(*jwt.Token) (any, error) { return ResourceKey(s.secret, "openpocket-resource-token:v1"), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(ResourceIssuer), jwt.WithAudience(ResourceAudience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil || c.IssuedAt == nil || c.NotBefore == nil || c.ExpiresAt == nil || len(c.Audience) != 1 || c.ExpiresAt.Time.Sub(c.IssuedAt.Time) > ResourceMaxTTL || !c.ExpiresAt.Time.After(c.IssuedAt.Time) || !resourceCanonical(c.Subject) || !resourceCanonical(c.TenantID) || !resourceCanonical(c.WorkspaceID) || !resourceCanonical(c.ParentSessionID) || c.ParentExpiresAt < c.ExpiresAt.Unix() {
		return nil, errors.New("invalid resource token")
	}
	if _, err = uuid.Parse(c.ID); err != nil {
		return nil, errors.New("invalid resource nonce")
	}
	if _, err = CanonicalResourceScopes(c.Scopes); err != nil {
		return nil, err
	}
	return c, nil
}

func resourceCanonical(s string) bool { return s != "" && s == strings.TrimSpace(s) }

// ResourceScope is a method-and-segment allowlist, never a whole API prefix.
// Credentials, email bodies/attachments, send/OAuth, auth, admin and AI streams
// are intentionally not reachable using a resource credential.
func ResourceScope(method, path, rawPath string) string {
	if rawPath != "" || strings.ContainsAny(path, "\\%") || strings.Contains(path, "//") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return ""
		}
	}
	if method == http.MethodPost && path == "/api/embed" {
		return "embedding"
	}
	if method == http.MethodGet {
		switch path {
		case "/api/email/accounts", "/api/emails", "/api/email/summaries", "/api/email/folders":
			return "email-metadata"
		}
		if len(parts) == 4 && parts[0] == "api" && parts[1] == "email" && parts[2] == "summaries" {
			return "email-metadata"
		}
	}
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "rss" {
		return ""
	}
	switch method {
	case http.MethodGet:
		switch path {
		case "/api/rss/sources", "/api/rss/sources/seeds", "/api/rss/sources/starter", "/api/rss/items", "/api/rss/filters", "/api/rss/digest", "/api/rss/digests", "/api/rss/portable/changes":
			return "rss"
		}
		if len(parts) == 4 && (parts[2] == "sources" || parts[2] == "items") {
			return "rss"
		}
	case http.MethodPost:
		switch path {
		case "/api/rss/sources", "/api/rss/sources/import-starter", "/api/rss/sources/discover", "/api/rss/items/bulk-read", "/api/rss/digest/run":
			return "rss"
		}
		if len(parts) == 5 && ((parts[2] == "sources" && parts[4] == "refresh") || (parts[2] == "items" && (parts[4] == "read" || parts[4] == "star"))) {
			return "rss"
		}
	case http.MethodPatch, http.MethodDelete:
		if len(parts) == 4 && parts[2] == "sources" {
			return "rss"
		}
	case http.MethodPut:
		if path == "/api/rss/filters" {
			return "rss"
		}
	}
	return ""
}
