package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

type failingAuditStore struct{ err error }

func (s failingAuditStore) Record(*redclaw.AuditEntry) error { return s.err }
func (s failingAuditStore) Query(redclaw.AuditQuery) ([]*redclaw.AuditEntry, error) {
	return nil, s.err
}
func (s failingAuditStore) Flush() []*redclaw.AuditEntry { return nil }
func (s failingAuditStore) QueryRange(redclaw.AuditQuery) (*redclaw.AuditPage, error) {
	return nil, s.err
}

func TestAuditQueryErrorDoesNotExposeBackendDetails(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = failingAuditStore{err: errors.New("postgres: relation audit_entries does not exist")}
	token, err := signer.SignWithWorkspace("admin-a", "admin", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/audit/logs", "/api/audit/export"} {
		rr := serveAuthedAuditRequest(srv, path, token)
		if rr.Code != 500 {
			t.Fatalf("%s status=%d body=%s", path, rr.Code, rr.Body.String())
		}
		if got := rr.Body.String(); got != "internal server error\n" {
			t.Fatalf("%s leaked backend error: %q", path, got)
		}
	}
}

func TestAuditQueryContextCancellationMapsToTimeout(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = failingAuditStore{err: context.DeadlineExceeded}
	token, err := signer.SignWithWorkspace("admin-a", "admin", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuthedAuditRequest(srv, "/api/audit/logs", token)
	if rr.Code != 504 || rr.Body.String() != "audit query timed out\n" {
		t.Fatalf("unexpected timeout response: %d %q", rr.Code, rr.Body.String())
	}
}

func serveAuthedAuditRequest(srv *Server, path, token string) *httptest.ResponseRecorder {
	req := mobileRequest(http.MethodGet, path, token, "")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}
