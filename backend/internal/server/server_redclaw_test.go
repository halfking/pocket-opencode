package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

func TestRedClawHealth_NotConfigured(t *testing.T) {
	s := &Server{redclawBridge: nil}

	req := httptest.NewRequest(http.MethodGet, "/api/redclaw/health", nil)
	rec := httptest.NewRecorder()

	s.handleRedClawHealth(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestRedClawHealth_Configured(t *testing.T) {
	mockRedClaw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(redclaw.HealthResponse{
			Status:  "ok",
			Version: "1.0.0",
		})
	}))
	defer mockRedClaw.Close()

	client, err := redclaw.NewClient(redclaw.ClientConfig{
		BaseURL:    mockRedClaw.URL,
		Secret:     "test-secret",
		TenantID:   "test-tenant",
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("Failed to create RedClaw client: %v", err)
	}
	bridge := redclaw.NewBridge(client, nil)
	bridge.Start()
	defer bridge.Stop()

	s := &Server{
		redclawBridge: bridge,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/redclaw/health", nil)
	rec := httptest.NewRecorder()

	s.handleRedClawHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp["connected"] != true {
		t.Errorf("expected connected true, got %v", resp["connected"])
	}
}

func TestRedClawChat(t *testing.T) {
	mockRedClaw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(redclaw.ChatResponse{
			Message: redclaw.Message{
				Role:    "assistant",
				Content: "Hello from mock",
			},
			ModelUsed: "test",
			LatencyMs: 10,
		})
	}))
	defer mockRedClaw.Close()

	client, err := redclaw.NewClient(redclaw.ClientConfig{
		BaseURL:    mockRedClaw.URL,
		Secret:     "test-secret",
		TenantID:   "test-tenant",
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("Failed to create RedClaw client: %v", err)
	}
	bridge := redclaw.NewBridge(client, nil)
	bridge.Start()
	defer bridge.Stop()

	s := &Server{
		redclawBridge: bridge,
	}

	body, _ := json.Marshal(redclaw.ChatRequest{
		Messages: []redclaw.Message{{Role: "user", Content: "Hi"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/redclaw/chat", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	s.handleRedClawChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp redclaw.ChatResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Message.Content != "Hello from mock" {
		t.Errorf("expected 'Hello from mock', got %s", resp.Message.Content)
	}
}

func TestRedClawChat_NotConfigured(t *testing.T) {
	s := &Server{redclawBridge: nil}

	body, _ := json.Marshal(redclaw.ChatRequest{
		Messages: []redclaw.Message{{Role: "user", Content: "Hi"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/redclaw/chat", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	s.handleRedClawChat(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}