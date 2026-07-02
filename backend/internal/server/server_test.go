package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

func TestHealthz(t *testing.T) {
	cfg := config.Load()
	timeoutMS, _ := strconv.Atoi(cfg.OpenCodeTimeoutMS)
	if timeoutMS == 0 {
		timeoutMS = 5000
	}

	reg := registry.NewRegistry()
	configAdapter := adapter.NewOpenCodeConfigHTTPAdapter(timeoutMS)
	srv := New(cfg, adapter.NewStaticNPSAdapter(), adapter.NewOpenCodeHTTPAdapter(timeoutMS), nil, reg, configAdapter, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if string(body) != "ok" {
		t.Fatalf("expected ok, got %q", string(body))
	}
}

func TestInstances(t *testing.T) {
	cfg := config.Load()
	timeoutMS, _ := strconv.Atoi(cfg.OpenCodeTimeoutMS)
	if timeoutMS == 0 {
		timeoutMS = 5000
	}

	reg := registry.NewRegistry()
	configAdapter := adapter.NewOpenCodeConfigHTTPAdapter(timeoutMS)
	srv := New(cfg, adapter.NewStaticNPSAdapter(), adapter.NewOpenCodeHTTPAdapter(timeoutMS), nil, reg, configAdapter, nil, nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if rr.Body.String() == "" {
		t.Fatal("expected instances payload")
	}
}
