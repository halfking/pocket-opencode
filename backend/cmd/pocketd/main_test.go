package main

import (
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

func TestStartAuditFileExporterSkipsUnavailableStore(t *testing.T) {
	cancel, started := startAuditFileExporter(nil, t.TempDir(), time.Millisecond, 7)
	if started {
		t.Fatal("exporter must not start without an audit store")
	}
	if cancel != nil {
		t.Fatal("unstarted exporter must not return a cancel function")
	}
}

func TestStartAuditFileExporterStartsWithStore(t *testing.T) {
	cancel, started := startAuditFileExporter(redclaw.NewAuditStore(), t.TempDir(), time.Hour, 7)
	if !started {
		t.Fatal("exporter must start with an audit store")
	}
	if cancel == nil {
		t.Fatal("started exporter must return a cancel function")
	}
	cancel()
}

func TestProductionConfigRecognized(t *testing.T) {
	for _, environment := range []string{"production", "PROD", " prod "} {
		if !isProductionConfig(config.Config{Environment: environment}) {
			t.Fatalf("environment %q must be production", environment)
		}
	}
	if isProductionConfig(config.Config{Environment: "development"}) {
		t.Fatal("development must not be production")
	}
}

func TestNewCloudOrchestratorRequiresConfiguredTenant(t *testing.T) {
	if got := newCloudOrchestrator(nil); got != nil {
		t.Fatal("nil MCP client must not create an orchestrator")
	}

	clientWithoutTenant := mcp.NewClientWithAuth("https://acc.example.test", "secret", "", nil, false)
	if got := newCloudOrchestrator(clientWithoutTenant); got != nil {
		t.Fatal("MCP client without tenant must not create an orchestrator")
	}
}

func TestRegisterPhase4ExecutorsRegistersCloudOnly(t *testing.T) {
	client := mcp.NewClientWithAuth("https://acc.example.test", "secret", "workspace-1", nil, false)
	orch := newCloudOrchestrator(client)
	if orch == nil || orch.Cloud() == nil {
		t.Fatal("configured ACC client must create a cloud orchestrator")
	}
	if orch.Local() != nil {
		t.Fatal("production Phase 4 wiring must not configure a mock local runtime")
	}

	scheduler := scheduledtask.NewScheduler(nil, false)
	if err := registerPhase4Executors(scheduler, orch); err != nil {
		t.Fatalf("register phase 4 executors: %v", err)
	}
	if !scheduler.HasExecutor(scheduledtask.KindCloudDispatch) {
		t.Fatal("cloud_dispatch executor must be registered for configured ACC")
	}
	if scheduler.HasExecutor(scheduledtask.KindLocalAgent) {
		t.Fatal("local_agent executor must remain disabled without a production runtime")
	}
}

func TestRegisterPhase4ExecutorsSkipsUnavailableDependencies(t *testing.T) {
	scheduler := scheduledtask.NewScheduler(nil, false)
	if err := registerPhase4Executors(scheduler, nil); err != nil {
		t.Fatalf("nil orchestrator must be a no-op: %v", err)
	}
	if scheduler.HasExecutor(scheduledtask.KindCloudDispatch) {
		t.Fatal("cloud_dispatch executor must not register without an orchestrator")
	}
}
