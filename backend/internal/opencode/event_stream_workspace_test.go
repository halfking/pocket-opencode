package opencode

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

// Subscribing with a workspace that does not own the registered instance must
// fail before any upstream connection is attempted. Otherwise a tenant could
// have the backend open an SSE stream against another tenant's instance.
func TestSubscribeRejectsForeignWorkspace(t *testing.T) {
	reg := registry.NewRegistry()
	if err := reg.RegisterRegisteredInstance(model.RegisteredInstanceInfo{
		ID:          "inst-a",
		WorkspaceID: "ws-a",
		APIBaseURL:  "http://fake-a",
	}); err != nil {
		t.Fatalf("register ws-a instance: %v", err)
	}
	fake := newFakeEventAdapter()
	mgr := NewEventStreamManager(reg, fake)
	defer mgr.Close()

	_, _, err := mgr.Subscribe(context.Background(), SubscribeOptions{
		InstanceID:  "inst-a",
		WorkspaceID: "ws-b",
	})
	if err == nil {
		t.Fatal("ws-b must not subscribe to a ws-a instance")
	}
	if got := atomic.LoadInt32(&fake.connectCount); got != 0 {
		t.Fatalf("no upstream connection may be opened on rejection, got %d", got)
	}

	if _, _, err := mgr.Subscribe(context.Background(), SubscribeOptions{
		InstanceID:  "inst-a",
		WorkspaceID: "ws-a",
	}); err != nil {
		t.Fatalf("ws-a must subscribe to its own instance: %v", err)
	}
}

// Two workspaces on the same shared instance must not share one stream, so an
// event fanned out for one workspace never reaches the other's subscriber.
func TestSubscribeIsolatesWorkspacesOnSharedInstance(t *testing.T) {
	reg := registry.NewRegistry()
	if err := reg.LoadFromConfig([]registry.InstanceConfig{
		{ID: "shared-1", APIBaseURL: "http://fake-shared"},
	}); err != nil {
		t.Fatalf("load shared instance: %v", err)
	}
	fake := newFakeEventAdapter()
	mgr := NewEventStreamManager(reg, fake)
	defer mgr.Close()

	chA, cleanupA, err := mgr.Subscribe(context.Background(), SubscribeOptions{
		InstanceID: "shared-1", WorkspaceID: "ws-a", BufferSize: 8,
	})
	if err != nil {
		t.Fatalf("subscribe ws-a: %v", err)
	}
	defer cleanupA()

	chB, cleanupB, err := mgr.Subscribe(context.Background(), SubscribeOptions{
		InstanceID: "shared-1", WorkspaceID: "ws-b", BufferSize: 8,
	})
	if err != nil {
		t.Fatalf("subscribe ws-b: %v", err)
	}
	defer cleanupB()

	mgr.PublishEvent(DomainEvent{
		InstanceID:  "shared-1",
		WorkspaceID: "ws-a",
		Type:        "session.updated",
	})

	select {
	case evt := <-chA:
		if evt.WorkspaceID != "ws-a" {
			t.Fatalf("ws-a subscriber got workspace %q", evt.WorkspaceID)
		}
	case <-time.After(time.Second):
		t.Fatal("ws-a subscriber did not receive its own event")
	}

	select {
	case evt := <-chB:
		t.Fatalf("ws-b subscriber must not receive ws-a event: %#v", evt)
	case <-time.After(100 * time.Millisecond):
	}
}

// streams is keyed by instance+workspace+directory, so StreamStatus must match
// on instanceID instead of using the raw ID as a map key.
func TestStreamStatusFindsCompositeKeyedStreams(t *testing.T) {
	reg := registry.NewRegistry()
	if err := reg.LoadFromConfig([]registry.InstanceConfig{
		{ID: "shared-1", APIBaseURL: "http://fake-shared"},
	}); err != nil {
		t.Fatalf("load shared instance: %v", err)
	}
	mgr := NewEventStreamManager(reg, newFakeEventAdapter())
	defer mgr.Close()

	if _, ok := mgr.StreamStatus("shared-1"); ok {
		t.Fatal("no stream exists yet")
	}

	_, cleanupA, err := mgr.Subscribe(context.Background(), SubscribeOptions{
		InstanceID: "shared-1", WorkspaceID: "ws-a",
	})
	if err != nil {
		t.Fatalf("subscribe ws-a: %v", err)
	}
	defer cleanupA()

	_, cleanupB, err := mgr.Subscribe(context.Background(), SubscribeOptions{
		InstanceID: "shared-1", WorkspaceID: "ws-b",
	})
	if err != nil {
		t.Fatalf("subscribe ws-b: %v", err)
	}
	defer cleanupB()

	status, ok := mgr.StreamStatus("shared-1")
	if !ok {
		t.Fatal("StreamStatus must find streams stored under composite keys")
	}
	if status.InstanceID != "shared-1" {
		t.Fatalf("unexpected instance id %q", status.InstanceID)
	}
	if status.Subscribers != 2 {
		t.Fatalf("expected subscribers from both workspaces, got %d", status.Subscribers)
	}
}
