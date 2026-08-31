package auth

import (
	"testing"
	"time"
)

func TestNewWebAuthnVerifier(t *testing.T) {
	tests := []struct {
		name          string
		rpDisplayName string
		rpID          string
		rpOrigin      string
		wantErr       bool
	}{
		{
			name:          "valid config",
			rpDisplayName: "Redclaw",
			rpID:          "localhost",
			rpOrigin:      "http://localhost:3000",
			wantErr:       false,
		},
		{
			name:          "missing rpDisplayName",
			rpDisplayName: "",
			rpID:          "localhost",
			rpOrigin:      "http://localhost:3000",
			wantErr:       true,
		},
		{
			name:          "missing rpID",
			rpDisplayName: "Redclaw",
			rpID:          "",
			rpOrigin:      "http://localhost:3000",
			wantErr:       true,
		},
		{
			name:          "missing rpOrigin",
			rpDisplayName: "Redclaw",
			rpID:          "localhost",
			rpOrigin:      "",
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier, err := NewWebAuthnVerifier(tt.rpDisplayName, tt.rpID, tt.rpOrigin)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if verifier == nil {
					t.Errorf("expected non-nil verifier")
				}
			}
		})
	}
}

func TestChallengeStore(t *testing.T) {
	store := newChallengeStore()
	defer store.Close()

	challengeB64 := "test-challenge-123"
	userID := "user-123"
	ttl := 5 * time.Minute

	// Put
	if err := store.Put(challengeB64, userID, ttl); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Get
	sess, err := store.Get(challengeB64)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if sess.UserID != userID {
		t.Errorf("expected userID=%s, got %s", userID, sess.UserID)
	}
	if time.Now().After(sess.ExpiresAt) {
		t.Errorf("challenge already expired")
	}

	// Delete
	store.Delete(challengeB64)
	_, err = store.Get(challengeB64)
	if err == nil {
		t.Errorf("expected error after delete, got nil")
	}
}

func TestChallengeStoreExpiry(t *testing.T) {
	store := newChallengeStore()
	defer store.Close()

	challengeB64 := "test-challenge-expired"
	userID := "user-123"
	ttl := 100 * time.Millisecond

	if err := store.Put(challengeB64, userID, ttl); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Wait for expiry
	time.Sleep(150 * time.Millisecond)

	_, err := store.Get(challengeB64)
	if err == nil {
		t.Errorf("expected error for expired challenge, got nil")
	}
}

func TestGenerateChallenge(t *testing.T) {
	challenge1, err := GenerateChallenge()
	if err != nil {
		t.Fatalf("GenerateChallenge failed: %v", err)
	}
	if len(challenge1) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(challenge1))
	}

	challenge2, err := GenerateChallenge()
	if err != nil {
		t.Fatalf("GenerateChallenge failed: %v", err)
	}

	// Verify uniqueness
	if string(challenge1) == string(challenge2) {
		t.Errorf("expected unique challenges, got identical")
	}
}
