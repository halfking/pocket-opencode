package registry

// Health probe fallback tests for the OpenCode 4096 detection layer.
//
// We spin up an httptest server on the canonical OpenCode port (4096) and
// verify that probeOne succeeds when the server speaks /global/health, falls
// back to /api/health, and ultimately accepts /healthz.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newProbeClient() *http.Client {
	return &http.Client{Timeout: 1 * time.Second}
}

// TestProbeOnePrefersGlobalHealth 验证 /global/health 是首选路径。
func TestProbeOnePrefersGlobalHealth(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/global/health" {
			t.Errorf("first hit path = %s, want /global/health", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"healthy":true,"version":"1.2.3","product":"opencode"}`))
	}))
	defer srv.Close()
	port := extractPort(t, srv.URL)
	cfg, ok := probeOne(context.Background(), newProbeClient(), "127.0.0.1", port)
	if !ok {
		t.Fatalf("expected probeOne to succeed")
	}
	if cfg.Version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", cfg.Version)
	}
	if cfg.DisplayName == "" || !strings.Contains(cfg.DisplayName, "1.2.3") {
		t.Fatalf("display name should embed version, got %q", cfg.DisplayName)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected exactly 1 request, got %d", hits.Load())
	}
}

// TestProbeOneFallsBackToAPIHealth 验证当 /global/health 返回 404 时回退 /api/health。
func TestProbeOneFallsBackToAPIHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			w.WriteHeader(http.StatusNotFound)
		case "/api/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"healthy":true,"version":"legacy"}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	port := extractPort(t, srv.URL)
	cfg, ok := probeOne(context.Background(), newProbeClient(), "127.0.0.1", port)
	if !ok {
		t.Fatalf("expected fallback to succeed")
	}
	if cfg.Version != "legacy" {
		t.Fatalf("version = %q, want legacy", cfg.Version)
	}
}

// TestProbeOneFallsBackToHealthz 验证最后一个回退点 /healthz 也能识别。
func TestProbeOneFallsBackToHealthz(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health", "/api/health":
			w.WriteHeader(http.StatusNotFound)
		case "/healthz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"healthy":true,"status":"ok"}`))
		}
	}))
	defer srv.Close()
	port := extractPort(t, srv.URL)
	if _, ok := probeOne(context.Background(), newProbeClient(), "127.0.0.1", port); !ok {
		t.Fatalf("expected /healthz fallback to succeed")
	}
}

// TestProbeOneRejectsUnhealthy 验证非 healthy 响应被拒绝。
func TestProbeOneRejectsUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"healthy":false}`))
	}))
	defer srv.Close()
	port := extractPort(t, srv.URL)
	if _, ok := probeOne(context.Background(), newProbeClient(), "127.0.0.1", port); ok {
		t.Fatalf("expected unhealthy response to be rejected")
	}
}

func TestProbeOneRejectsUnreachable(t *testing.T) {
	if _, ok := probeOne(context.Background(), newProbeClient(), "127.0.0.1", 1); ok {
		t.Fatalf("expected unreachable port to fail probe")
	}
}

func TestDefaultAPIPortIs4096(t *testing.T) {
	if DefaultAPIPort != 4096 {
		t.Fatalf("DefaultAPIPort = %d, want 4096", DefaultAPIPort)
	}
}

func TestDefaultPortsIncludes4096First(t *testing.T) {
	if len(DefaultPorts) == 0 || DefaultPorts[0] != 4096 {
		t.Fatalf("DefaultPorts must start with 4096, got %v", DefaultPorts)
	}
}

// extractPort pulls the listener port from an httptest URL like http://127.0.0.1:54321.
func extractPort(t *testing.T, url string) int {
	t.Helper()
	idx := strings.LastIndex(url, ":")
	if idx < 0 {
		t.Fatalf("cannot parse port from %s", url)
	}
	port := url[idx+1:]
	var p int
	for _, c := range port {
		if c < '0' || c > '9' {
			t.Fatalf("unexpected char in port %q", port)
		}
		p = p*10 + int(c-'0')
	}
	return p
}