package server

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/orchestrator"
	"github.com/halfking/pocket-opencode/backend/internal/scheduledtask"
)

func TestValidateTaskExecutionAvailability(t *testing.T) {
	withoutOrchestrator := &Server{}
	if err := withoutOrchestrator.validateTaskExecutionAvailability(scheduledtask.KindLocalAgent); err == nil {
		t.Fatal("local_agent must require a configured local dispatcher")
	}
	if err := withoutOrchestrator.validateTaskExecutionAvailability(scheduledtask.KindCloudDispatch); err == nil {
		t.Fatal("cloud_dispatch must require a configured cloud dispatcher")
	}
	if err := withoutOrchestrator.validateTaskExecutionAvailability(scheduledtask.KindWebhook); err != nil {
		t.Fatalf("existing executor kinds must remain unaffected: %v", err)
	}

	cloudOnly := &Server{orchestrator: orchestrator.New(nil, orchestrator.NewMockCloudDispatcher(true), nil)}
	if err := cloudOnly.validateTaskExecutionAvailability(scheduledtask.KindCloudDispatch); err != nil {
		t.Fatalf("available cloud dispatcher must allow cloud_dispatch: %v", err)
	}
	if err := cloudOnly.validateTaskExecutionAvailability(scheduledtask.KindLocalAgent); err == nil {
		t.Fatal("cloud-only orchestrator must still reject local_agent")
	}

	unavailableCloud := &Server{orchestrator: orchestrator.New(nil, orchestrator.NewMockCloudDispatcher(false), nil)}
	if err := unavailableCloud.validateTaskExecutionAvailability(scheduledtask.KindCloudDispatch); err == nil {
		t.Fatal("unavailable cloud dispatcher must reject cloud_dispatch")
	}
}
