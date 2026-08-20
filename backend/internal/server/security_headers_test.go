package server

// security_headers_test.go asserts that production responses carry the
// baseline hardening headers:
//   - X-Frame-Options: DENY
//   - X-Content-Type-Options: nosniff
//   - Referrer-Policy: no-referrer
//   - Strict-Transport-Security (only when DevAuth is off; HSTS on a
//     localhost dev server is harmful)
//
// These headers are set by securityHeadersMiddleware. Dev mode (DevAuth
// true) suppresses HSTS so local iteration does not require HTTPS; the
// other three are always on because they are pure hardening (no dev
// downside).

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/config"
)

// newServerWithDevAuth builds a Server with a minimal config that has the
// given DevAuth flag. The middleware chain reads s.cfg.DevAuth to decide
// whether to emit HSTS.
func newServerWithDevAuth(t *testing.T, devAuth bool) *Server {
	t.Helper()
	return &Server{cfg: config.Config{DevAuth: devAuth}}
}

func TestSecurityHeaders_ProductionSetsBaseline(t *testing.T) {
	s := newServerWithDevAuth(t, false)
	handler := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options: got %q, want DENY", got)
	}
	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options: got %q, want nosniff", got)
	}
	if got := rr.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy: got %q, want no-referrer", got)
	}
}

func TestSecurityHeaders_DevModeOmitsHSTS(t *testing.T) {
	// DevAuth=true means HSTS would lock the dev box into HTTPS-only.
	// Confirm the header is NOT set when devAuth is on.
	s := newServerWithDevAuth(t, true)
	handler := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS must not be set in dev mode, got %q", got)
	}
}

func TestSecurityHeaders_ProductionSetsHSTS(t *testing.T) {
	// DevAuth=false simulates production. HSTS should be present.
	s := newServerWithDevAuth(t, false)
	handler := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Strict-Transport-Security"); got == "" {
		t.Errorf("HSTS must be set in production-like mode, got empty")
	}
}
