package auth

import (
	"testing"
	"time"
)

func TestJWTSignAndParse(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes-long"
	ttl := 24 * time.Hour
	signer := NewSigner(secret, ttl)

	// Test signing
	token, err := signer.Sign("testuser", "user")
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	if token == "" {
		t.Fatal("Token is empty")
	}

	// Test parsing
	claims, err := signer.Parse(token)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if claims.UserID != "testuser" {
		t.Errorf("Expected UserID 'testuser', got '%s'", claims.UserID)
	}
	if claims.Role != "user" {
		t.Errorf("Expected Role 'user', got '%s'", claims.Role)
	}
}

func TestJWTInvalidToken(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes-long"
	ttl := 24 * time.Hour
	signer := NewSigner(secret, ttl)

	// Test with invalid token
	_, err := signer.Parse("invalid.token.here")
	if err == nil {
		t.Fatal("Expected error for invalid token, got nil")
	}
}

func TestJWTExpiredToken(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes-long"
	ttl := 1 * time.Millisecond // Very short TTL
	signer := NewSigner(secret, ttl)

	token, err := signer.Sign("testuser", "user")
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// Wait for token to expire
	time.Sleep(10 * time.Millisecond)

	_, err = signer.Parse(token)
	if err == nil {
		t.Fatal("Expected error for expired token, got nil")
	}
}

func TestJWTDifferentSecrets(t *testing.T) {
	secret1 := "secret-key-one-at-least-32-bytes-long"
	secret2 := "secret-key-two-at-least-32-bytes-long"
	ttl := 24 * time.Hour

	signer1 := NewSigner(secret1, ttl)
	signer2 := NewSigner(secret2, ttl)

	token, err := signer1.Sign("testuser", "user")
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// Try to parse with different secret
	_, err = signer2.Parse(token)
	if err == nil {
		t.Fatal("Expected error when parsing with different secret, got nil")
	}
}
