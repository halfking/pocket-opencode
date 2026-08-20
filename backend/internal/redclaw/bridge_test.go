package redclaw

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewBridge(t *testing.T) {
	cfg := ClientConfig{
		BaseURL:    "http://localhost:8092",
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 10,
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	bridge := NewBridge(client, nil)

	if bridge == nil {
		t.Fatal("expected non-nil bridge")
	}
	if bridge.client == nil {
		t.Error("expected client to be set")
	}
}

func TestBridgeChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatResponse{
			Message:   Message{Role: "assistant", Content: "Hello from RedClaw"},
			ModelUsed: "test",
			LatencyMs: 10,
		})
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	bridge := NewBridge(client, nil)
	bridge.Start()
	defer bridge.Stop()

	resp, err := bridge.Chat(ChatRequest{
		TenantID: "pocket-test",
		UserID:   "user-1",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "Hello from RedClaw" {
		t.Errorf("expected 'Hello from RedClaw', got %s", resp.Message.Content)
	}
}

func TestBridgeHealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{
			Status:  "ok",
			Version: "1.0.0",
		})
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	bridge := NewBridge(client, nil)
	bridge.Start()
	defer bridge.Stop()

	healthy := bridge.HealthCheck()
	if !healthy {
		t.Error("expected healthy")
	}
}

func TestBridgeHealthCheck_Failure(t *testing.T) {
	client, err := NewClient(ClientConfig{
		BaseURL:    "http://localhost:19999",
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 1,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	bridge := NewBridge(client, nil)

	healthy := bridge.HealthCheck()
	if healthy {
		t.Error("expected unhealthy for unreachable server")
	}
}

func TestBridgeIsConnected(t *testing.T) {
	client, err := NewClient(ClientConfig{
		BaseURL:    "http://localhost:8092",
		Secret:     "test",
		TenantID:   "test",
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	bridge := NewBridge(client, nil)
	bridge.Start()
	defer bridge.Stop()

	if !bridge.IsConnected() {
		t.Error("expected connected after Start()")
	}

	bridge.Stop()

	if bridge.IsConnected() {
		t.Error("expected disconnected after Stop()")
	}
}

func TestBridgeDoubleStop(t *testing.T) {
	client, err := NewClient(ClientConfig{
		BaseURL:    "http://localhost:8092",
		Secret:     "test",
		TenantID:   "test",
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	bridge := NewBridge(client, nil)
	bridge.Start()
	bridge.Stop()
	
	// Second stop should not panic
	bridge.Stop()
}

func TestBridgeNilClient(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil client")
		}
	}()
	
	NewBridge(nil, nil)
}

func TestBridgeChatNotConnected(t *testing.T) {
	client, err := NewClient(ClientConfig{
		BaseURL:    "http://localhost:8092",
		Secret:     "test",
		TenantID:   "test",
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	bridge := NewBridge(client, nil)
	// Don't call Start()

	_, err = bridge.Chat(ChatRequest{
		TenantID: "test",
		UserID:   "user-1",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Error("expected error when bridge not connected")
	}
}