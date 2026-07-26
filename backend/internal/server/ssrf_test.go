package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

func TestValidateOutboundURLRejectsPrivateAndMalformedURLs(t *testing.T) {
	for _, rawURL := range []string{
		"http://127.0.0.1:8080",
		"http://10.0.0.1:8080",
		"http://169.254.169.254/latest/meta-data",
		"ftp://example.com",
		"http://user:pass@example.com",
		"https://example.com/path?token=secret",
		"https://example.com/path#fragment",
	} {
		t.Run(rawURL, func(t *testing.T) {
			if err := validateOutboundURL(rawURL); err == nil {
				t.Fatalf("expected URL to be rejected: %s", rawURL)
			}
		})
	}
}

func TestHandleSessionsRejectsRawInstanceURL(t *testing.T) {
	srv, token := newTestServerWithAuth(t)
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/?instance="+"http%3A%2F%2F127.0.0.1%3A8080", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for raw instance URL, got %d: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

func TestHandleSessionsUsesRegistryInstanceID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session" {
			t.Fatalf("expected /session, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "session-1"}})
	}))
	defer upstream.Close()

	srv, token := newTestServerWithAuth(t)
	if err := srv.registry.LoadFromConfig([]registry.InstanceConfig{{ID: "instance-1", APIBaseURL: upstream.URL}}); err != nil {
		t.Fatalf("load registry: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/?instance_id=instance-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
