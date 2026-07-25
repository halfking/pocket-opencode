package redclaw

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	cfg := ClientConfig{
		BaseURL:    "http://localhost:8092",
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 10,
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.cfg.BaseURL != "http://localhost:8092" {
		t.Errorf("expected base URL http://localhost:8092, got %s", client.cfg.BaseURL)
	}
}

func TestNewClient_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ClientConfig
		wantErr bool
	}{
		{
			name:    "empty BaseURL",
			cfg:     ClientConfig{Secret: "test", TenantID: "test"},
			wantErr: true,
		},
		{
			name:    "empty Secret",
			cfg:     ClientConfig{BaseURL: "http://localhost", TenantID: "test"},
			wantErr: true,
		},
		{
			name:    "empty TenantID",
			cfg:     ClientConfig{BaseURL: "http://localhost", Secret: "test"},
			wantErr: true,
		},
		{
			name:    "valid config",
			cfg:     ClientConfig{BaseURL: "http://localhost", Secret: "test", TenantID: "test"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestClientHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("expected /health, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{
			Status:    "ok",
			Version:   "1.0.0",
			UptimeSec: 3600,
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

	resp, err := client.Health()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %s", resp.Status)
	}
	if resp.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", resp.Version)
	}
}

func TestClientChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pocket/llm/chat" {
			t.Errorf("expected /api/v1/pocket/llm/chat, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("expected Bearer test-secret, got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Tenant-ID") != "pocket-test" {
			t.Errorf("expected X-Tenant-ID pocket-test, got %s", r.Header.Get("X-Tenant-ID"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatResponse{
			Message: Message{
				Role:    "assistant",
				Content: "Hello from RedClaw!",
			},
			ModelUsed: "test-model",
			LatencyMs: 150,
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

	resp, err := client.Chat(ChatRequest{
		TenantID: "pocket-test",
		UserID:   "user-1",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "Hello from RedClaw!" {
		t.Errorf("expected 'Hello from RedClaw!', got %s", resp.Message.Content)
	}
}

func TestClientChat_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{
			Code:    500,
			Message: "internal error",
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

	_, err = client.Chat(ChatRequest{
		TenantID: "pocket-test",
		UserID:   "user-1",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestClientChat_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatResponse{})
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 1, // 1 秒超时，服务器 2 秒响应，必定超时
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	_, err = client.Chat(ChatRequest{
		TenantID: "pocket-test",
		UserID:   "user-1",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClientChat_EmptyMessages(t *testing.T) {
	client, err := NewClient(ClientConfig{
		BaseURL:    "http://localhost:8092",
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	_, err = client.Chat(ChatRequest{
		TenantID: "pocket-test",
		UserID:   "user-1",
		Messages: []Message{},
	})
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestClientChat_TenantIsolation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatResponse{
			Message:   Message{Role: "assistant", Content: "response"},
			ModelUsed: "test",
		})
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "tenant-a",
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// Try to override with different tenant ID - should fail
	_, err = client.Chat(ChatRequest{
		TenantID: "tenant-b",
		UserID:   "user-1",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for tenant ID mismatch")
	}
	if !contains(err.Error(), "tenant ID mismatch") {
		t.Errorf("expected tenant ID mismatch error, got: %v", err)
	}
}

func TestClientKnowledgeSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(KnowledgeSearchResponse{
			Results: []KnowledgeResult{
				{Title: "Test Doc", Content: "Test content", Score: 0.95},
			},
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

	resp, err := client.KnowledgeSearch(KnowledgeSearchRequest{
		Query: "test query",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Errorf("expected 1 result, got %d", len(resp.Results))
	}
}

func TestClientKnowledgeSearch_EmptyQuery(t *testing.T) {
	client, err := NewClient(ClientConfig{
		BaseURL:    "http://localhost:8092",
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	_, err = client.KnowledgeSearch(KnowledgeSearchRequest{
		Query: "",
	})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestClientKnowledgeSearch_TenantIsolation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(KnowledgeSearchResponse{})
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "tenant-a",
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	_, err = client.KnowledgeSearch(KnowledgeSearchRequest{
		TenantID: "tenant-b",
		Query:    "test",
	})
	if err == nil {
		t.Fatal("expected error for tenant ID mismatch")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}