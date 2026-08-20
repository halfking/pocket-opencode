package websocket

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestHubBroadcastToWorkspace_OnlyMatchingClients verifies that a
// workspace-targeted broadcast (the delivery path used by approval push
// events) reaches only clients bound to that workspace and never leaks to
// other workspaces or unbound clients.
func TestHubBroadcastToWorkspace_OnlyMatchingClients(t *testing.T) {
	hub := NewHub()
	done := make(chan struct{})
	go func() {
		hub.Run()
		close(done)
	}()
	t.Cleanup(func() {
		// Hub.Run has no stop hook in this test; the goroutine idles on
		// channels and exits with the process. Drain is not required.
		_ = done
	})

	clientA := NewClientWithIdentity(hub, &websocket.Conn{}, "client-a", "", "ws-a")
	clientB := NewClientWithIdentity(hub, &websocket.Conn{}, "client-b", "", "ws-b")
	clientGlobal := NewClient(hub, &websocket.Conn{}, "client-global")

	hub.Register(clientA)
	hub.Register(clientB)
	hub.Register(clientGlobal)

	envelope := map[string]any{
		"v":        1,
		"id":       "approval_1",
		"ts":       1723710000000,
		"channel":  "approvals",
		"topic":    "inst-a",
		"type":     "approval.permission.pending",
		"data":     map[string]any{"instance_id": "inst-a"},
		"cause":    map[string]any{"approval_id": "per-1"},
	}
	hub.BroadcastToWorkspace("ws-a", "approval.permission.pending", envelope)

	// The matching client receives the message.
	select {
	case msg := <-clientA.send:
		if msg.Type != "approval.permission.pending" {
			t.Errorf("client-a got type %q, want approval.permission.pending", msg.Type)
		}
		if msg.Payload.(map[string]any)["id"] != "approval_1" {
			t.Errorf("client-a payload mismatch: %+v", msg.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("client-a never received the workspace broadcast")
	}

	// Non-matching clients must not receive anything.
	for name, ch := range map[string]chan Message{"client-b": clientB.send, "client-global": clientGlobal.send} {
		select {
		case msg := <-ch:
			t.Errorf("%s received a broadcast meant for ws-a: %+v", name, msg)
		default:
		}
	}

	// Give the hub loop a moment to prove nothing arrives late.
	time.Sleep(50 * time.Millisecond)
	select {
	case msg := <-clientB.send:
		t.Errorf("client-b received a late broadcast: %+v", msg)
	default:
	}
}
