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

	client := NewClient(cfg)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.cfg.BaseURL != "http://localhost:8092" {
		t.Errorf("expected base URL http://localhost:8092, got %s", client.cfg.BaseURL)
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

	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 5,
	})

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

	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 5,
	})

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

	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 5,
	})

	_, err := client.Chat(ChatRequest{
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

	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Secret:     "test-secret",
		TenantID:   "pocket-test",
		TimeoutSec: 1, // 1 秒超时，服务器 2 秒响应，必定超时
	})

	_, err := client.Chat(ChatRequest{
		TenantID: "pocket-test",
		UserID:   "user-1",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}