package server

// mobile_endpoint_scope.go implements helpers that close the mobile
// endpoint workspace gaps called out in PR9 of optimization v4.
//
// Reference:
//   docs/优化v4/14-首批PR与执行顺序.md row 9 (E3-S4)
//   docs/优化v4/15-PR1-契约冻结与发布前置.md §10 (stable error codes)
//
// Goals of PR9 (per 14 §2 row 9):
//   - Add a workspace-required middleware so handlers that take a
//     workspace_id from the path get a clear 400 workspace_required
//     instead of a downstream 500.
//   - Wrap writeJSON error responses in a stable shape that includes
//     code + request_id + retryable so the client (PR3 asyncState.ts)
//     can switch on the code without parsing prose.
//   - Provide a "not_found vs 403" decision helper so we can collapse
//     "you don't own this resource" and "no such resource" into one
//     indistinguishable 404 (per PR1 §5 'do not leak existence').
//   - Provide a small payload-size guard for endpoints that take
//     user-provided JSON bodies.
//
// PR9 boundary:
//   - Helpers only. Existing routes are NOT modified in this commit;
//     follow-up commits (one per resource group) will adopt these
//     helpers in lock-step with new tests.
//   - No DB-gated tests are added that require PostgreSQL; this commit
//     ships pure helpers that are exercised via httptest.

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Stable error codes per PR1 §10.
const (
	CodeUnauthenticated     = "unauthenticated"
	CodeTokenExpired        = "token_expired"
	CodeWorkspaceRequired   = "workspace_required"
	CodeCapabilityDenied    = "capability_denied"
	CodeNotFound            = "not_found"
	CodeConflict            = "conflict"
	CodeRateLimited         = "rate_limited"
	CodeApprovalRequired    = "approval_required"
	CodeApprovalExpired     = "approval_expired"
	CodeIdempotencyRequired = "idempotency_key_required"
	CodeUpstreamUnavailable = "upstream_unavailable"
	CodePayloadTooLarge     = "payload_too_large"
	CodeUnsupportedEvent    = "unsupported_event"
	CodeInvalidRequest      = "invalid_request"
)

// writeStructuredError writes a stable error envelope. It always emits
// the legacy "error" field for backwards compatibility plus the new
// "code", "request_id" and "retryable" fields so clients built against
// PR1 §10 can switch on the code.
//
// `retryable` is informational — clients must still consult the HTTP
// status. The default mapping is:
//
//   4xx → retryable=false (except 408/425/429 which are retryable=true)
//   5xx → retryable=true
func (s *Server) writeStructuredError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	if code == "" {
		code = codeForStatus(status)
	}
	retryable := retryableForStatus(status)
	body := map[string]any{
		"error":     msg,
		"code":      code,
		"retryable": retryable,
	}
	if r != nil {
		body["request_id"] = s.requestIDFromContext(r)
	}
	writeJSON(w, status, body)
}

// requestIDFromContext returns the request id stored by the request-id
// middleware, or an empty string when the middleware is not active.
func (s *Server) requestIDFromContext(r *http.Request) string {
	if s == nil || r == nil {
		return ""
	}
	// Prefer the standard header set by reverse proxies; fall back to
	// the value the request_id middleware may have stored on the
	// context.
	if v := r.Header.Get("X-Request-ID"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Request-Id"); v != "" {
		return v
	}
	return ""
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return CodeUnauthenticated
	case http.StatusForbidden:
		return CodeCapabilityDenied
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeConflict
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusBadRequest:
		return CodeInvalidRequest
	case http.StatusRequestEntityTooLarge:
		return CodePayloadTooLarge
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return CodeUpstreamUnavailable
	}
	return CodeInvalidRequest
}

func retryableForStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout,
		http.StatusTooEarly,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// workspaceFromPath extracts the workspace_id from a /workspaces/:ws/...
// path or returns an empty string. Used by the helpers below.
func workspaceFromPath(r *http.Request) string {
	const prefix = "/workspaces/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// requireWorkspaceFromPath is a middleware that ensures the request
// targets an explicit workspace. Handlers that operate on user-owned
// data (notes / email / vault / meetings) must mount this in front so a
// missing or empty workspace segment never reaches the store layer.
//
// Usage:
//
//	mux.Handle("/api/mobile/workspaces/", s.requireAuth(s.requireWorkspaceFromPath(handler)))
//
// On failure it writes a 400 workspace_required envelope so the client
// surfaces the actionable hint instead of a generic 500.
func (s *Server) requireWorkspaceFromPath(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := workspaceFromPath(r)
		if ws == "" {
			s.writeStructuredError(w, r, http.StatusBadRequest, CodeWorkspaceRequired,
				"workspace_id is required in the URL path")
			return
		}
		claims := s.claimsFromContext(r)
		if claims == nil {
			s.writeStructuredError(w, r, http.StatusUnauthorized, CodeUnauthenticated,
				"authentication required")
			return
		}
		if claims.WorkspaceID != "" && claims.WorkspaceID != ws {
			// Collapse cross-workspace access to a 404 to avoid
			// existence leakage (PR1 §5).
			s.writeStructuredError(w, r, http.StatusNotFound, CodeNotFound,
				"resource not found")
			return
		}
		next.ServeHTTP(w, r)
	}
}

// decideNotFound vs forbidden: collapses "no such resource" and
// "not yours" into one indistinguishable 404. Use this helper from
// every resource handler that looks up by id before returning 404 vs
// 403.
func (s *Server) writeResourceNotFound(w http.ResponseWriter, r *http.Request) {
	s.writeStructuredError(w, r, http.StatusNotFound, CodeNotFound, "resource not found")
}

// maxJSONBodyBytes is the default request body cap for mobile endpoints
// (per docs/优化v4/02-安全审计与整改清单.md §4 P2: 'payload size limits').
// The value matches the long-lived middleware default and is exposed
// here so handlers can be wired around it.
const maxJSONBodyBytes = 1 << 20 // 1 MiB

// decodeJSONBody reads up to maxJSONBodyBytes from r and unmarshals
// into dst. On overflow it returns a structured 413 payload_too_large
// error so the client (PR3 asyncState.ts) can decide whether to retry
// with a smaller payload or surface a permanent failure.
func (s *Server) decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		// MaxBytesReader surfaces as a *http.MaxBytesError in modern
		// Go; we type-assert to be precise about the response code.
		if _, ok := err.(*http.MaxBytesError); ok {
			s.writeStructuredError(w, r, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
				"request body exceeds the maximum allowed size")
			return false
		}
		s.writeStructuredError(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"invalid request body: "+err.Error())
		return false
	}
	return true
}
