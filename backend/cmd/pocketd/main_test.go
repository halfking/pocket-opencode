package main

import (
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
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
