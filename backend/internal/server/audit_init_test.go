package server

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

func TestProductionAuditInitializationDoesNotFallbackToMemory(t *testing.T) {
	cfg := config.Load()
	cfg.Environment = "production"
	server := New(cfg, adapter.NewStaticNPSAdapter(), nil, nil, registry.NewRegistry(), nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "", nil)
	if server.AuditStore() != nil {
		t.Fatalf("production server must not silently fall back to memory audit store: %T", server.AuditStore())
	}
}

func TestDevelopmentAuditInitializationFallsBackToMemory(t *testing.T) {
	cfg := config.Load()
	cfg.Environment = "development"
	server := New(cfg, adapter.NewStaticNPSAdapter(), nil, nil, registry.NewRegistry(), nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "", nil)
	if server.AuditStore() == nil {
		t.Fatal("development server should retain memory audit fallback")
	}
}
