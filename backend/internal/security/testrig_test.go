// Package security contains end-to-end tests for the ZAG control-plane
// security model. These tests intentionally avoid mocking the production
// HTTP layer: each test boots a real httptest.Server with the middleware
// stack we expect from the gateway, issues real requests, and asserts on
// the HTTP semantics an attacker or operator would observe.
//
// The package depends only on stdlib (net/http, net/url, etc.) plus
// golang.org/x/crypto for ed25519, so it can live next to the rest of the
// backend Go code without dragging in the entire internal/server package.
//
// References:
//   docs/新架构v1/01-architecture/安全模型.md
//   docs/新架构v1/05-security/threat-model.md
//   docs/新架构v1/05-security/test-matrix.md
package security

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// shared types and helpers
// =============================================================================

// principal models the authenticated identity attached to a request.
// It mirrors identity.Principal (services/zagent-gateway/internal/identity)
// but is duplicated here so the security tests have no compile-time
// dependency on the ZAG skeleton package.
type principal struct {
	Subject   string
	TenantID  string
	Roles     []string
	Scopes    []string
	ExpiresAt time.Time
	IssuedAt  time.Time
	TokenID   string // jti
}

// hasScope returns true when p carries scope s.
func (p *principal) hasScope(s string) bool {
	for _, x := range p.Scopes {
		if x == s {
			return true
		}
	}
	return false
}

// hasRole returns true when p carries role r.
func (p *principal) hasRole(r string) bool {
	for _, x := range p.Roles {
		if x == r {
			return true
		}
	}
	return false
}

// =============================================================================
// auth middleware (the production-shaped token verifier)
// =============================================================================

// tokenVerifier holds the configured issuer/JWKS + replay cache.
type tokenVerifier struct {
	issuer     string
	audience   string
	publicKey  ed25519.PublicKey
	clockSkew  time.Duration
	replayMax  time.Duration
	revocations map[string]time.Time // jti -> exp
	replaySeen  map[string]time.Time // jti -> first-seen
	mu          sync.Mutex
}

func newTokenVerifier(pub ed25519.PublicKey) *tokenVerifier {
	return &tokenVerifier{
		issuer:      "pocket",
		audience:    "zagent-gateway",
		publicKey:   pub,
		clockSkew:   60 * time.Second,
		replayMax:   15 * time.Minute,
		revocations: map[string]time.Time{},
		replaySeen:  map[string]time.Time{},
	}
}

func (v *tokenVerifier) revoke(jti string, exp time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.revocations[jti] = exp
}

func (v *tokenVerifier) verify(token *signedToken, now time.Time) (*principal, error) {
	if token == nil {
		return nil, errors.New("missing token")
	}
	if token.Header.Alg != "EdDSA" {
		// ADR-0001 §3: alg must be in allowlist; HS256/none are forbidden.
		return nil, fmt.Errorf("ZAG_AUTH_ALG_REJECTED: %q", token.Header.Alg)
	}
	if token.Header.Issuer != v.issuer {
		return nil, errors.New("ZAG_AUTH_BAD_ISSUER")
	}
	audOK := false
	for _, a := range token.Payload.Audience {
		if a == v.audience {
			audOK = true
			break
		}
	}
	if !audOK {
		return nil, errors.New("ZAG_AUTH_BAD_AUDIENCE")
	}
	if !token.Payload.ExpiresAt.After(now) {
		return nil, errors.New("ZAG_AUTH_EXPIRED")
	}
	if token.Payload.IssuedAt.After(now.Add(v.clockSkew)) {
		return nil, errors.New("ZAG_AUTH_IAT_FUTURE")
	}
	if token.Payload.IssuedAt.Add(15*time.Minute + v.clockSkew).Before(token.Payload.ExpiresAt) {
		return nil, errors.New("token lifetime exceeds 15 minutes")
	}
	if !token.verify(v.publicKey) {
		return nil, errors.New("ZAG_AUTH_BAD_SIGNATURE")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if exp, ok := v.revocations[token.Payload.JTI]; ok && exp.After(now) {
		return nil, errors.New("ZAG_AUTH_TOKEN_REVOKED")
	}
	if first, ok := v.replaySeen[token.Payload.JTI]; ok {
		if now.Sub(first) < v.replayMax {
			return nil, errors.New("ZAG_AUTH_TOKEN_REPLAY")
		}
	}
	v.replaySeen[token.Payload.JTI] = now
	return &principal{
		Subject:   token.Payload.Subject,
		TenantID:  token.Payload.TenantID,
		Roles:     append([]string(nil), token.Payload.Roles...),
		Scopes:    append([]string(nil), token.Payload.Scopes...),
		ExpiresAt: token.Payload.ExpiresAt,
		IssuedAt:  token.Payload.IssuedAt,
		TokenID:   token.Payload.JTI,
	}, nil
}

// =============================================================================
// minimal JWS / JWT (EdDSA, compact serialization)
// =============================================================================

type jwsHeader struct {
	Alg     string `json:"alg"`
	Typ     string `json:"typ"`
	Kid     string `json:"kid"`
	Issuer  string `json:"-"`
	Payload any     `json:"-"`
}

type jwsPayload struct {
	Issuer    string    `json:"iss"`
	Audience  []string  `json:"aud"`
	Subject   string    `json:"sub"`
	TenantID  string    `json:"tenant_id"`
	Roles     []string  `json:"roles"`
	Scopes    []string  `json:"scope"`
	JTI       string    `json:"jti"`
	IssuedAt  time.Time `json:"iat"`
	ExpiresAt time.Time `json:"exp"`
}

type signedToken struct {
	Header  jwsHeader
	Payload jwsPayload
	sig     []byte
	raw     string
}

// signToken produces a JWS compact serialization signed with priv.
func signToken(t *testing.T, priv ed25519.PrivateKey, kid string, p jwsPayload) *signedToken {
	t.Helper()
	hdr := jwsHeader{Alg: "EdDSA", Typ: "at+jwt", Kid: kid}
	hdrJSON, err := json.Marshal(hdr)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	plJSON, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(hdrJSON) + "." +
		base64.RawURLEncoding.EncodeToString(plJSON)
	sig := ed25519.Sign(priv, []byte(signing))
	raw := signing + "." + base64.RawURLEncoding.EncodeToString(sig)
	return &signedToken{Header: hdr, Payload: p, sig: sig, raw: raw}
}

// verify checks signature against the canonical signing input.
func (t *signedToken) verify(pub ed25519.PublicKey) bool {
	hdrJSON, _ := json.Marshal(t.Header)
	plJSON, _ := json.Marshal(t.Payload)
	signing := base64.RawURLEncoding.EncodeToString(hdrJSON) + "." +
		base64.RawURLEncoding.EncodeToString(plJSON)
	return ed25519.Verify(pub, []byte(signing), t.sig)
}

// toBearer returns the value to put in the Authorization header.
func (t *signedToken) toBearer() string { return "Bearer " + t.raw }

// =============================================================================
// policy engine (RBAC + ABAC)
// =============================================================================

// actionMatrix mirrors ADR-0003 §3.
var actionMatrix = map[string]map[string]bool{
	"read":         {"viewer": true, "operator": true, "approver": true, "admin": true, "agent": true},
	"list":         {"viewer": true, "operator": true, "approver": true, "admin": true, "agent": true},
	"create":       {"operator": true, "approver": true, "admin": true, "agent": true},
	"update":       {"operator": true, "approver": true, "admin": true, "agent": true},
	"delete":       {"operator": true, "approver": true, "admin": true},
	"approve":      {"approver": true, "admin": true},
	"rotate_key":   {"admin": true},
	"manage_iam":   {"admin": true},
	"audit_export": {"approver": true, "admin": true},
}

// ownedWorkspaces is the workspace scope claim for an operator.
type ownedWorkspaces []string

// policyDecision is the result of an RBAC + ABAC evaluation.
type policyDecision struct {
	Allow  bool
	Reason string
}

// evaluate implements ADR-0003 §5.
func evaluate(p *principal, verb, resourceType, resourceTenant, resourceWorkspace, resourceOwner string, owned ownedWorkspaces) policyDecision {
	if p == nil {
		return policyDecision{Reason: "unauthenticated"}
	}
	// 1) tenant containment (ADR-0003 §4.1)
	if resourceTenant != "" && p.TenantID != resourceTenant {
		return policyDecision{Reason: "cross_tenant_denied"}
	}
	// 2) RBAC matrix
	allowed := false
	for _, r := range p.Roles {
		if actionMatrix[verb][r] {
			allowed = true
			break
		}
	}
	if !allowed {
		return policyDecision{Reason: "role_forbidden"}
	}
	// 3) workspace scope (ADR-0003 §4.2)
	if resourceWorkspace != "" && len(owned) > 0 {
		ok := false
		for _, w := range owned {
			if w == resourceWorkspace {
				ok = true
				break
			}
		}
		if !ok {
			return policyDecision{Reason: "workspace_forbidden"}
		}
	}
	// 4) ownership (ADR-0003 §4.4) — for sessions/tasks we require owner or admin.
	if (resourceType == "session" || resourceType == "task") && resourceOwner != "" {
		isOwner := resourceOwner == p.Subject
		isApproverOrAdmin := p.hasRole("approver") || p.hasRole("admin")
		if !isOwner && !isApproverOrAdmin {
			return policyDecision{Reason: "ownership_required"}
		}
	}
	return policyDecision{Allow: true}
}

// =============================================================================
// gate: the test HTTP server (real httptest.Server)
// =============================================================================

// auditEntry is what the gate records when a request is allowed/denied.
type auditEntry struct {
	At        time.Time
	Principal string
	Tenant    string
	Verb      string
	Resource  string
	Decision  string // "allow" or "deny"
	Reason    string
	RequestID string
}

type gate struct {
	verifier *tokenVerifier

	mu        sync.Mutex
	audit     []auditEntry
	idemSeen  map[string]struct{} // idempotency key -> seen
	seenWrite map[string]string   // (key,bodyHash) -> status+body for replay

	// replay record: every consumed (jti, bodyHash) -> result. Used to
	// simulate ZAG's idempotency store.
	replay map[string]replayRecord
}

type replayRecord struct {
	status int
	body   string
}

func newGate(verifier *tokenVerifier) *gate {
	return &gate{
		verifier:  verifier,
		idemSeen:  map[string]struct{}{},
		seenWrite: map[string]string{},
		replay:    map[string]replayRecord{},
	}
}

func (g *gate) record(a auditEntry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if a.At.IsZero() {
		a.At = time.Now().UTC()
	}
	g.audit = append(g.audit, a)
}

func (g *gate) snapshot() []auditEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]auditEntry, len(g.audit))
	copy(out, g.audit)
	return out
}

// authorize is the gateway's RBAC+ABAC entry point.
func (g *gate) authorize(r *http.Request, verb, resourceType, resourceTenant, resourceWorkspace, resourceOwner string, owned ownedWorkspaces) (*principal, policyDecision) {
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	tok, err := parseToken(raw)
	if err != nil {
		g.record(auditEntry{Verb: verb, Resource: r.URL.Path, Decision: "deny", Reason: "unauthenticated", RequestID: r.Header.Get("X-Request-Id")})
		return nil, policyDecision{Reason: "unauthenticated"}
	}
	p, err := g.verifier.verify(tok, time.Now())
	if err != nil {
		g.record(auditEntry{Verb: verb, Resource: r.URL.Path, Decision: "deny", Reason: err.Error(), RequestID: r.Header.Get("X-Request-Id")})
		return nil, policyDecision{Reason: err.Error()}
	}
	dec := evaluate(p, verb, resourceType, resourceTenant, resourceWorkspace, resourceOwner, owned)
	entry := auditEntry{
		Principal: p.Subject,
		Tenant:    p.TenantID,
		Verb:      verb,
		Resource:  r.URL.Path,
		RequestID: r.Header.Get("X-Request-Id"),
	}
	if dec.Allow {
		entry.Decision = "allow"
	} else {
		entry.Decision = "deny"
		entry.Reason = dec.Reason
	}
	g.record(entry)
	if !dec.Allow {
		return p, dec
	}
	return p, dec
}

// parseToken decodes a compact JWS into a *signedToken without verifying
// the signature (verification happens later in tokenVerifier.verify).
func parseToken(raw string) (*signedToken, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed token")
	}
	hdrJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}
	plJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	var hdr jwsHeader
	if err := json.Unmarshal(hdrJSON, &hdr); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	var pl jwsPayload
	if err := json.Unmarshal(plJSON, &pl); err != nil {
		return nil, fmt.Errorf("parse payload: %w", err)
	}
	return &signedToken{Header: hdr, Payload: pl, sig: sig, raw: raw}, nil
}

// =============================================================================
// routing helpers
// =============================================================================

// task is a fake per-tenant resource used to exercise ownership + path
// lookup. Tasks are owned by tenant + workspace + owner_principal.
type task struct {
	ID         string
	Tenant     string
	Workspace  string
	Owner      string
	BodyHash   string
	Status     string
}

// taskStore is a tiny in-memory per-tenant task table. In production this
// is a Postgres table guarded by RLS.
type taskStore struct {
	mu    sync.RWMutex
	byID  map[string]*task
	byWS  map[string]map[string]*task
	seq   atomic.Uint64
}

func newTaskStore() *taskStore {
	return &taskStore{byID: map[string]*task{}, byWS: map[string]map[string]*task{}}
}

func (s *taskStore) insert(t *task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byWS[t.Workspace]; !ok {
		s.byWS[t.Workspace] = map[string]*task{}
	}
	s.byID[t.ID] = t
	s.byWS[t.Workspace][t.ID] = t
}

func (s *taskStore) get(tenant, workspace, id string) (*task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.byID[id]
	if !ok {
		return nil, false
	}
	if t.Tenant != tenant || t.Workspace != workspace {
		// simulate "row not visible to this tenant" — cross-tenant lookup
		// returns nothing rather than 200.
		return nil, false
	}
	return t, true
}

// permissionRequest models a permission request the user can reply to.
type permissionRequest struct {
	ID       string
	Tenant   string
	Resource string
	Tool     string
}

// =============================================================================
// test bootstrap
// =============================================================================

// testRig owns the running test server and helpers. Every test gets a
// fresh rig so they can mutate the gate without cross-talk.
type testRig struct {
	t          *testing.T
	server     *httptest.Server
	verifier   *tokenVerifier
	gate       *gate
	tasks      *taskStore
	perms      map[string]*permissionRequest
	mu         sync.Mutex
	privKey    ed25519.PrivateKey
	pubKey     ed25519.PublicKey
}

func newRig(t *testing.T) *testRig {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}
	v := newTokenVerifier(pub)
	g := newGate(v)
	tasks := newTaskStore()
	r := &testRig{
		t:        t,
		verifier: v,
		gate:     g,
		tasks:    tasks,
		perms:    map[string]*permissionRequest{},
		privKey:  priv,
		pubKey:   pub,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tasks", r.handleListTasks)
	mux.HandleFunc("/api/v1/tasks/", r.handleTaskSubpath)
	mux.HandleFunc("/api/v1/permissions/", r.handlePermissionReply)
	mux.HandleFunc("/api/v1/ide/", r.handleIDECommand)
	mux.HandleFunc("/api/v1/agent/control", r.handlePodControl)
	r.server = httptest.NewServer(mux)
	t.Cleanup(r.server.Close)
	return r
}

// mintToken signs a token using the rig's key.
func (r *testRig) mintToken(p jwsPayload) *signedToken {
	r.t.Helper()
	return signToken(r.t, r.privKey, "test-key-1", p)
}

// do issues an authenticated request.
func (r *testRig) do(method, path string, tok *signedToken, headers map[string]string, body string) *httptest.ResponseRecorder {
	r.t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	if tok != nil {
		req.Header.Set("Authorization", tok.toBearer())
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.server.Handler().ServeHTTP(rec, req)
	return rec
}

// authedRequest issues an authenticated request against the live HTTP
// server (NOT via the in-memory handler). This is the "real httptest
// server" path required by the task brief.
func (r *testRig) authedRequest(method, path string, tok *signedToken, headers map[string]string, body string) *http.Response {
	r.t.Helper()
	var bodyR *strings.Reader
	if body != "" {
		bodyR = strings.NewReader(body)
	} else {
		bodyR = strings.NewReader("")
	}
	req, err := http.NewRequest(method, r.server.URL+path, bodyR)
	if err != nil {
		r.t.Fatalf("new request: %v", err)
	}
	if tok != nil {
		req.Header.Set("Authorization", tok.toBearer())
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatalf("do: %v", err)
	}
	return resp
}

// jtiCounter helps generate unique jti values.
var jtiCounter atomic.Uint64

func newJTI() string {
	return fmt.Sprintf("jti-%d-%s", jtiCounter.Add(1), randomHex(4))
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// nowFunc returns the test's notion of "now". Tests can override this via
// rig.now when they need deterministic timestamps.
func (r *testRig) now() time.Time { return time.Now().UTC() }

// ctxWithCancel returns a context + cancel for goroutines spawned by tests.
func ctxWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
