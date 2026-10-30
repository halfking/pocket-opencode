package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/kaixuan/identity-go/token"
)

const resourceExchangeAudience = "pocket-resource-authority"
const resourceSessionPath = "/internal/pocket-resource/session-check"

type resourceDelegation struct {
	TenantID        string   `json:"tenant_id"`
	WorkspaceID     string   `json:"workspace_id"`
	SessionID       string   `json:"parent_session_id"`
	ParentExpiresAt int64    `json:"parent_expires_at"`
	Scopes          []string `json:"scopes"`
	Method          string   `json:"method"`
	Path            string   `json:"path"`
	jwt.RegisteredClaims
}

func verifyResourceExchange(secret []byte, raw, tenant, path string, now time.Time) (*resourceDelegation, error) {
	if len(secret) < 32 || tenant == "" {
		return nil, errors.New("resource exchange unavailable")
	}
	c := &resourceDelegation{}
	_, err := jwt.ParseWithClaims(raw, c, func(*jwt.Token) (any, error) { return auth.ResourceKey(secret, "openpocket-resource-exchange:v1"), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("redclaw"), jwt.WithAudience(resourceExchangeAudience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil || len(c.Audience) != 1 || c.IssuedAt == nil || c.NotBefore == nil || c.ExpiresAt == nil || c.ExpiresAt.Time.Sub(c.IssuedAt.Time) > 30*time.Second || !c.ExpiresAt.Time.After(c.IssuedAt.Time) || c.Subject == "" || c.Subject != strings.TrimSpace(c.Subject) || c.TenantID != tenant || c.WorkspaceID == "" || c.WorkspaceID != strings.TrimSpace(c.WorkspaceID) || c.SessionID == "" || c.SessionID != strings.TrimSpace(c.SessionID) || c.ParentExpiresAt < c.ExpiresAt.Unix() || c.Method != "POST" || c.Path != path || len(c.Scopes) != 1 {
		return nil, errors.New("invalid resource delegation")
	}
	if _, err = uuid.Parse(c.ID); err != nil {
		return nil, err
	}
	if _, err = auth.CanonicalResourceScopes(c.Scopes); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Server) handleResourceAuthority(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	secret, err := token.LoadSharedSecret("IDENTITY_SHARED_SECRET")
	if err != nil || s.identityStore == nil || s.jwtSigner == nil || strings.TrimSpace(s.cfg.RedClawTenantID) == "" {
		writeError(w, 503, "resource authority is not configured")
		return
	}
	const prefix = "/internal/resource-authority/workspaces/"
	path := r.URL.Path
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/token") {
		writeError(w, 400, "invalid resource authority request")
		return
	}
	workspace := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/token")
	if workspace == "" || strings.ContainsAny(workspace, "/\\%?#") || workspace == "." || workspace == ".." {
		writeError(w, 400, "invalid workspace")
		return
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(authorization, "Bearer ") {
		writeError(w, 401, "a bearer service delegation is required")
		return
	}
	raw := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	c, err := verifyResourceExchange(secret, raw, s.cfg.RedClawTenantID, path, time.Now())
	if err != nil || c.WorkspaceID != workspace {
		writeError(w, 401, "invalid or expired resource delegation")
		return
	}
	if err = s.identityStore.ConsumeDelegation(r.Context(), &identity.DelegationClaims{RegisteredClaims: c.RegisteredClaims}, time.Now()); err != nil {
		if errors.Is(err, identity.ErrDelegationReplay) {
			writeError(w, 401, "resource delegation already used")
		} else {
			writeError(w, 503, "resource replay protection unavailable")
		}
		return
	}
	status, member, err := s.identityStore.CheckMembership(r.Context(), workspace, c.Subject)
	if err != nil {
		writeError(w, 503, "membership lookup unavailable")
		return
	}
	if status != identity.MembershipActive || member == nil {
		writeError(w, 403, "workspace membership denied")
		return
	}
	claims := auth.ResourceClaims{TenantID: c.TenantID, WorkspaceID: workspace, ParentSessionID: c.SessionID, ParentExpiresAt: c.ParentExpiresAt, Scopes: c.Scopes, RegisteredClaims: jwt.RegisteredClaims{Subject: c.Subject}}
	if code := s.checkResourceParent(r.Context(), &claims, c.Scopes[0]); code != 200 {
		writeError(w, code, "parent session or resource policy denied")
		return
	}
	expiry := time.Now().Add(auth.ResourceMaxTTL)
	if member.ExpiresAt > 0 && expiry.Unix() > member.ExpiresAt {
		expiry = time.Unix(member.ExpiresAt, 0)
	}
	issued, metadata, err := s.jwtSigner.SignResource(claims, expiry, time.Now())
	if err != nil {
		writeError(w, 401, "parent membership expires too soon")
		return
	}
	writeJSON(w, 200, map[string]any{"token": issued, "user_id": metadata.Subject, "tenant_id": metadata.TenantID, "workspace_id": metadata.WorkspaceID, "scopes": metadata.Scopes, "issued_at": metadata.IssuedAt.Unix(), "expires_at": metadata.ExpiresAt.Unix()})
}

var resourceSessionHTTP = func() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return &http.Client{Transport: t, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}()

func resourceSessionURL() (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(os.Getenv("POCKET_RESOURCE_SESSION_AUTHORITY_URL")))
	if err != nil || u == nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("invalid session authority")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return nil, errors.New("session authority requires HTTPS")
	}
	u.Path = resourceSessionPath
	return u, nil
}

// Every resource access rechecks the persisted parent session and current
// module policy. No positive result is cached, even within the token's TTL.
func (s *Server) checkResourceParent(ctx context.Context, c *auth.ResourceClaims, scope string) int {
	u, err := resourceSessionURL()
	secret, secretErr := token.LoadSharedSecret("IDENTITY_SHARED_SECRET")
	if err != nil || secretErr != nil {
		return 503
	}
	now := time.Now()
	proof := resourceDelegation{TenantID: c.TenantID, WorkspaceID: c.WorkspaceID, SessionID: c.ParentSessionID, ParentExpiresAt: c.ParentExpiresAt, Scopes: []string{scope}, Method: "POST", Path: resourceSessionPath, RegisteredClaims: jwt.RegisteredClaims{Issuer: "pocket.resource", Subject: c.Subject, Audience: jwt.ClaimStrings{"redclaw-resource-session"}, ID: uuid.NewString(), IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Second))}}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, proof).SignedString(auth.ResourceKey(secret, "openpocket-resource-session:v1"))
	if err != nil {
		return 503
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return 503
	}
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err := resourceSessionHTTP.Do(req)
	if err != nil {
		return 503
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return 401
	}
	if resp.StatusCode == 403 {
		return 403
	}
	if resp.StatusCode != 200 {
		return 503
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	if err != nil || len(body) > 16384 {
		return 503
	}
	var out struct {
		Active    bool   `json:"active"`
		User      string `json:"user_id"`
		Tenant    string `json:"tenant_id"`
		Workspace string `json:"workspace_id"`
		Session   string `json:"parent_session_id"`
		Nonce     string `json:"nonce"`
	}
	if json.Unmarshal(body, &out) != nil || !out.Active || out.User != c.Subject || out.Tenant != c.TenantID || out.Workspace != c.WorkspaceID || out.Session != c.ParentSessionID || out.Nonce != proof.ID {
		return 503
	}
	return 200
}

func (s *Server) resourceCurrent(ctx context.Context, c *auth.ResourceClaims, scope string) (int, string) {
	if s.identityStore == nil || c.TenantID != strings.TrimSpace(s.cfg.RedClawTenantID) {
		return 503, "resource authority is not configured"
	}
	status, member, err := s.identityStore.CheckMembership(ctx, c.WorkspaceID, c.Subject)
	if err != nil {
		return 503, "membership lookup unavailable"
	}
	if status != identity.MembershipActive || member == nil {
		return 403, "workspace membership denied"
	}
	if code := s.checkResourceParent(ctx, c, scope); code != 200 {
		return code, "parent session or resource policy denied"
	}
	return 200, string(member.Role)
}
