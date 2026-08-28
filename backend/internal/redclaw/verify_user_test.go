package redclaw

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyUser(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		serverResponse VerifyUserResponse
		serverStatus   int
		wantErr        bool
		wantValid      bool
	}{
		{
			name:   "valid user",
			userID: "user-123",
			serverResponse: VerifyUserResponse{
				Valid: true,
				UserInfo: &UserInfo{
					UserID:      "user-123",
					Username:    "testuser",
					Email:       "test@example.com",
					DisplayName: "Test User",
					Roles:       []string{"user", "admin"},
					TenantID:    "tenant-1",
					Status:      "active",
				},
			},
			serverStatus: http.StatusOK,
			wantErr:      false,
			wantValid:    true,
		},
		{
			name:   "invalid user",
			userID: "user-disabled",
			serverResponse: VerifyUserResponse{
				Valid:    false,
				UserInfo: nil,
			},
			serverStatus: http.StatusOK,
			wantErr:      false,
			wantValid:    false,
		},
		{
			name:         "empty userID",
			userID:       "",
			wantErr:      true,
			serverStatus: http.StatusOK,
		},
		{
			name:         "server error",
			userID:       "user-123",
			serverStatus: http.StatusInternalServerError,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock RedClaw server
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/users/verify" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if r.Method != http.MethodPost {
					t.Errorf("unexpected method: %s", r.Method)
				}

				// Check authorization header
				if auth := r.Header.Get("Authorization"); auth != "Bearer test-secret" {
					t.Errorf("unexpected auth header: %s", auth)
				}

				// Return mock response
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.serverStatus)
				if tt.serverStatus == http.StatusOK {
					json.NewEncoder(w).Encode(tt.serverResponse)
				}
			}))
			defer srv.Close()

			// Create client
			client, err := NewClient(ClientConfig{
				BaseURL:    srv.URL,
				Secret:     "test-secret",
				TenantID:   "tenant-1",
				TimeoutSec: 5,
			})
			if err != nil {
				t.Fatalf("NewClient failed: %v", err)
			}

			// Call VerifyUser
			resp, err := client.VerifyUser(tt.userID)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if resp.Valid != tt.wantValid {
				t.Errorf("expected Valid=%v, got %v", tt.wantValid, resp.Valid)
			}

			if tt.wantValid && resp.UserInfo == nil {
				t.Errorf("expected UserInfo, got nil")
			}

			if tt.wantValid && resp.UserInfo != nil {
				if resp.UserInfo.UserID != tt.userID {
					t.Errorf("expected UserID=%s, got %s", tt.userID, resp.UserInfo.UserID)
				}
			}
		})
	}
}

func TestBridgeVerifyUser(t *testing.T) {
	// Mock RedClaw server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(VerifyUserResponse{
			Valid: true,
			UserInfo: &UserInfo{
				UserID:   "user-123",
				Username: "testuser",
				Status:   "active",
			},
		})
	}))
	defer srv.Close()

	client, _ := NewClient(ClientConfig{
		BaseURL:    srv.URL,
		Secret:     "test-secret",
		TenantID:   "tenant-1",
		TimeoutSec: 5,
	})

	bridge := NewBridge(client, nil)
	bridge.Start()
	defer bridge.Stop()

	resp, err := bridge.VerifyUser("user-123")
	if err != nil {
		t.Fatalf("VerifyUser failed: %v", err)
	}

	if !resp.Valid {
		t.Errorf("expected Valid=true, got false")
	}
}

func TestBridgeVerifyUser_NotConnected(t *testing.T) {
	client, _ := NewClient(ClientConfig{
		BaseURL:    "http://localhost:9999",
		Secret:     "test-secret",
		TenantID:   "tenant-1",
		TimeoutSec: 5,
	})

	bridge := NewBridge(client, nil)
	// Don't call Start() - bridge not connected

	_, err := bridge.VerifyUser("user-123")
	if err == nil {
		t.Errorf("expected error, got nil")
	}

	if err != ErrBridgeNotConnected {
		t.Errorf("expected ErrBridgeNotConnected, got %v", err)
	}
}
