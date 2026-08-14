package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setClaimsForTest mirrors what requireAuth does on the request context.
// Tests use it to seed claims without going through the JWT signer.
func setClaimsForTest(ctx context.Context, c *authClaims) context.Context {
	return context.WithValue(ctx, authClaimsContextKey{}, c)
}

// ---- pure helpers ----

func TestRetryableForStatus(t *testing.T) {
	cases := map[int]bool{
		http.StatusOK:                 false,
		http.StatusBadRequest:         false,
		http.StatusUnauthorized:       false,
		http.StatusForbidden:          false,
		http.StatusNotFound:           false,
		http.StatusConflict:           false,
		http.StatusRequestTimeout:     true,
		http.StatusTooManyRequests:    true,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:         true,
		http.StatusServiceUnavailable: true,
		http.StatusGatewayTimeout:     true,
	}
	for status, want := range cases {
		if got := retryableForStatus(status); got != want {
			t.Errorf("status %d: want %v, got %v", status, want, got)
		}
	}
}

func TestCodeForStatus(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:            CodeUnauthenticated,
		http.StatusForbidden:               CodeCapabilityDenied,
		http.StatusNotFound:                CodeNotFound,
		http.StatusConflict:                CodeConflict,
		http.StatusRequestTimeout:          CodeRateLimited,
		http.StatusTooManyRequests:         CodeRateLimited,
		http.StatusBadRequest:              CodeInvalidRequest,
		http.StatusRequestEntityTooLarge:   CodePayloadTooLarge,
		http.StatusBadGateway:              CodeUpstreamUnavailable,
		http.StatusServiceUnavailable:      CodeUpstreamUnavailable,
		http.StatusGatewayTimeout:          CodeUpstreamUnavailable,
	}
	for status, want := range cases {
		if got := codeForStatus(status); got != want {
			t.Errorf("status %d: want %s, got %s", status, want, got)
		}
	}
}

func TestWorkspaceFromPath(t *testing.T) {
	cases := map[string]string{
		"/workspaces/ws-1/notes":         "ws-1",
		"/workspaces/ws-2/notes/123":     "ws-2",
		"/workspaces//notes":             "",
		"/notes":                         "",
		"/workspaces/ws-1":               "ws-1",
	}
	for path, want := range cases {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if got := workspaceFromPath(r); got != want {
			t.Errorf("%q: want %q, got %q", path, want, got)
		}
	}
}

// ---- structured error envelope ----

func TestWriteStructuredError_StableShape(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest(http.MethodPost, "/workspaces/ws-1/notes", strings.NewReader(""))
	r.Header.Set("X-Request-ID", "req-abc")
	w := httptest.NewRecorder()
	s.writeStructuredError(w, r, http.StatusBadRequest, CodeWorkspaceRequired, "workspace required")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] != "workspace required" {
		t.Errorf("error: %v", body["error"])
	}
	if body["code"] != CodeWorkspaceRequired {
		t.Errorf("code: %v", body["code"])
	}
	if body["retryable"] != false {
		t.Errorf("retryable: %v", body["retryable"])
	}
	if body["request_id"] != "req-abc" {
		t.Errorf("request_id: %v", body["request_id"])
	}
}

func TestWriteStructuredError_InfersCode(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.writeStructuredError(w, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusNotFound, "", "missing")
	if !strings.Contains(w.Body.String(), `"code":"`+CodeNotFound+`"`) {
		t.Errorf("expected code to be inferred as %s; body=%s", CodeNotFound, w.Body.String())
	}
}

func TestWriteStructuredError_RetryableTrueOn5xx(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.writeStructuredError(w, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusServiceUnavailable, CodeUpstreamUnavailable, "down")
	if !strings.Contains(w.Body.String(), `"retryable":true`) {
		t.Errorf("expected retryable=true on 503; body=%s", w.Body.String())
	}
}

// ---- requireWorkspaceFromPath middleware ----

func TestRequireWorkspaceFromPath_Missing(t *testing.T) {
	s := &Server{}
	handler := s.requireWorkspaceFromPath(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/notes", nil)
	handler(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), CodeWorkspaceRequired) {
		t.Errorf("expected code %s in body", CodeWorkspaceRequired)
	}
}

func TestRequireWorkspaceFromPath_OK(t *testing.T) {
	s := &Server{}
	handler := s.requireWorkspaceFromPath(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/workspaces/ws-1/notes", nil)
	// inject claims via the real context mechanism used by requireAuth
	// (we replicate the key shape here)
	type claimsKey struct{}
	ctx := r.Context()
	ctx = setClaimsForTest(ctx, &authClaims{UserID: "u-1", WorkspaceID: "ws-1"})
	r = r.WithContext(ctx)
	handler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRequireWorkspaceFromPath_CrossWorkspace(t *testing.T) {
	s := &Server{}
	handler := s.requireWorkspaceFromPath(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/workspaces/ws-2/notes", nil)
	ctx := setClaimsForTest(r.Context(), &authClaims{UserID: "u-1", WorkspaceID: "ws-1"})
	r = r.WithContext(ctx)
	handler(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), CodeNotFound) {
		t.Errorf("expected not_found code in body")
	}
}

func TestRequireWorkspaceFromPath_NoClaims(t *testing.T) {
	s := &Server{}
	handler := s.requireWorkspaceFromPath(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/workspaces/ws-1/notes", nil)
	handler(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: want 401, got %d", w.Code)
	}
}

// ---- decodeJSONBody ----

func TestDecodeJSONBody_OK(t *testing.T) {
	s := &Server{}
	body := bytes.NewBufferString(`{"hello":"world"}`)
	r := httptest.NewRequest(http.MethodPost, "/x", body)
	var dst map[string]string
	w := httptest.NewRecorder()
	if !s.decodeJSONBody(w, r, &dst) {
		t.Fatalf("expected true")
	}
	if dst["hello"] != "world" {
		t.Errorf("got %v", dst)
	}
}

func TestDecodeJSONBody_TooLarge(t *testing.T) {
	s := &Server{}
	huge := bytes.Repeat([]byte("a"), maxJSONBodyBytes+16)
	// Wrap as JSON: {"x":"..."}
	doc := []byte{'"'}
	doc = append(doc, huge...)
	doc = append(doc, '"')
	r := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(doc))
	var dst string
	w := httptest.NewRecorder()
	if s.decodeJSONBody(w, r, &dst) {
		t.Fatalf("expected false")
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status: want 413, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), CodePayloadTooLarge) {
		t.Errorf("expected payload_too_large code in body")
	}
}

func TestDecodeJSONBody_UnknownField(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"hello":"world","extra":1}`))
	var dst struct {
		Hello string `json:"hello"`
	}
	w := httptest.NewRecorder()
	if s.decodeJSONBody(w, r, &dst) {
		t.Fatalf("expected false (DisallowUnknownFields)")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), CodeInvalidRequest) {
		t.Errorf("expected invalid_request code in body")
	}
}
