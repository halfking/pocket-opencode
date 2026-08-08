package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

// Shared operator instances (WorkspaceID == "") are visible to every
// workspace. Pushing a tenant's gateway APIKey to them would overwrite the same
// process for all tenants and expose that key, so the push must skip them.
func TestPushConfigSkipsSharedInstances(t *testing.T) {
	srv, _ := newTestServerWithAuth(t)

	reg := registry.NewRegistry()
	if err := reg.LoadFromConfig([]registry.InstanceConfig{
		{ID: "shared-1", APIBaseURL: "http://127.0.0.1:65535"},
	}); err != nil {
		t.Fatalf("load shared instance: %v", err)
	}
	srv.registry = reg

	// No POCKET_OPENCODE_CONFIG_TOKEN is set here. If the shared instance were
	// still a push target, pushConfigToOpenCode would fail on the missing
	// token; returning nil proves it was filtered out before that check.
	req := httptest.NewRequest(http.MethodPost, "/api/llm-gateway/config", nil)
	err := srv.pushConfigToOpenCode(req, "ws-a", llmGatewayState{
		BaseURL: "https://gateway.example.com",
		APIKey:  "tenant-a-secret",
	})
	if err != nil {
		t.Fatalf("shared instances must not be push targets, got %v", err)
	}
}

// An instance owned by another workspace must never be a push target either.
func TestPushConfigSkipsForeignWorkspaceInstances(t *testing.T) {
	srv, _ := newTestServerWithAuth(t)

	reg := registry.NewRegistry()
	if err := reg.RegisterRegisteredInstance(model.RegisteredInstanceInfo{
		ID:          "inst-b",
		WorkspaceID: "ws-b",
		APIBaseURL:  "http://127.0.0.1:65535",
	}); err != nil {
		t.Fatalf("register ws-b instance: %v", err)
	}
	srv.registry = reg

	req := httptest.NewRequest(http.MethodPost, "/api/llm-gateway/config", nil)
	if err := srv.pushConfigToOpenCode(req, "ws-a", llmGatewayState{
		BaseURL: "https://gateway.example.com",
		APIKey:  "tenant-a-secret",
	}); err != nil {
		t.Fatalf("ws-a must not push to a ws-b instance, got %v", err)
	}
}
