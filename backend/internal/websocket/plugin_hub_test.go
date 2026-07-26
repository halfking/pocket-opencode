package websocket

import (
	"testing"
	"time"
)

func waitForInstance(t *testing.T, hub *PluginHub, workspaceID, instanceID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, id := range hub.GetConnectedInstances(workspaceID) {
			if id == instanceID {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("instance %s never registered in workspace %s", instanceID, workspaceID)
}

func TestSendCommandToInstanceRejectsForeignWorkspace(t *testing.T) {
	hub := NewPluginHub()
	go hub.Run()

	conn := &PluginConnection{
		ID:       "inst-1",
		Send:     make(chan []byte, 4),
		Hub:      hub,
		Metadata: PluginMetadata{InstanceID: "inst-1", WorkspaceID: "ws-a"},
	}
	hub.RegisterPlugin(conn)
	waitForInstance(t, hub, "ws-a", "inst-1")

	if err := hub.SendCommandToInstance("ws-b", "inst-1", Message{Type: "session.stop"}); err == nil {
		t.Fatal("expected cross-workspace command to be rejected")
	}
	if len(conn.Send) != 0 {
		t.Fatalf("cross-workspace command leaked to instance: %d queued", len(conn.Send))
	}

	if err := hub.SendCommandToInstance("ws-a", "inst-1", Message{Type: "session.stop"}); err != nil {
		t.Fatalf("same-workspace command should succeed: %v", err)
	}
	if len(conn.Send) != 1 {
		t.Fatalf("expected 1 queued command, got %d", len(conn.Send))
	}
}

func TestSendCommandToInstanceFailsWhenNotConnected(t *testing.T) {
	hub := NewPluginHub()
	go hub.Run()

	if err := hub.SendCommandToInstance("ws-a", "missing", Message{Type: "session.stop"}); err == nil {
		t.Fatal("expected error when instance is not connected")
	}
}

func TestGetConnectedInstancesIsWorkspaceScoped(t *testing.T) {
	hub := NewPluginHub()
	go hub.Run()

	hub.RegisterPlugin(&PluginConnection{
		ID:       "inst-a",
		Send:     make(chan []byte, 4),
		Hub:      hub,
		Metadata: PluginMetadata{InstanceID: "inst-a", WorkspaceID: "ws-a"},
	})
	hub.RegisterPlugin(&PluginConnection{
		ID:       "inst-b",
		Send:     make(chan []byte, 4),
		Hub:      hub,
		Metadata: PluginMetadata{InstanceID: "inst-b", WorkspaceID: "ws-b"},
	})
	waitForInstance(t, hub, "ws-a", "inst-a")
	waitForInstance(t, hub, "ws-b", "inst-b")

	got := hub.GetConnectedInstances("ws-a")
	if len(got) != 1 || got[0] != "inst-a" {
		t.Fatalf("workspace ws-a should only see inst-a, got %#v", got)
	}
}
