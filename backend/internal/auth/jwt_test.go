package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// mintExpired 签发一个 exp 早于 30s leeway 窗口的 token，用于验证
// Parse/Middleware 真正拒绝过期凭证（1ms TTL + 短 sleep 的旧写法始终
// 落在 leeway 内，无法覆盖过期路径）。
func mintExpired(t *testing.T, secret, userID, role string) string {
	t.Helper()
	now := time.Now()
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    IssuerName,
			Audience:  jwt.ClaimStrings{AudienceName},
			ExpiresAt: jwt.NewNumericDate(now.Add(-time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Minute)),
			NotBefore: jwt.NewNumericDate(now.Add(-2 * time.Minute)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}
	return signed
}

func TestJWTSignAndParse(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes-long"
	ttl := 24 * time.Hour
	signer, err := NewSigner(secret, ttl)
	if err != nil {
		t.Fatalf("NewSigner failed: %v", err)
	}

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
	signer, err := NewSigner(secret, ttl)
	if err != nil {
		t.Fatalf("NewSigner failed: %v", err)
	}

	// Test with invalid token
	_, err = signer.Parse("invalid.token.here")
	if err == nil {
		t.Fatal("Expected error for invalid token, got nil")
	}
}

func TestJWTExpiredToken(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes-long"
	signer, err := NewSigner(secret, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewSigner failed: %v", err)
	}

	token := mintExpired(t, secret, "testuser", "user")

	_, err = signer.Parse(token)
	if err == nil {
		t.Fatal("Expected error for expired token, got nil")
	}
}

func TestJWTDifferentSecrets(t *testing.T) {
	secret1 := "secret-key-one-at-least-32-bytes-long"
	secret2 := "secret-key-two-at-least-32-bytes-long"
	ttl := 24 * time.Hour

	signer1, err := NewSigner(secret1, ttl)
	if err != nil {
		t.Fatalf("NewSigner failed: %v", err)
	}
	signer2, err := NewSigner(secret2, ttl)
	if err != nil {
		t.Fatalf("NewSigner failed: %v", err)
	}

	token, err := signer1.Sign("testuser", "user")
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// Try to parse with different secret
	_, err = signer2.Parse(token)
	if err == nil {
		t.Fatal("Expected error when parsing with different secret")
	}
}

// TestJWTSignWithWorkspace verifies the S0-A extension: workspace_id round-trips
// through SignWithWorkspace/Parse, and legacy Sign keeps it empty.
func TestJWTSignWithWorkspace(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes-long"
	signer, err := NewSigner(secret, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewSigner failed: %v", err)
	}

	tok, err := signer.SignWithWorkspace("u1", "owner", "ws_u1")
	if err != nil {
		t.Fatalf("SignWithWorkspace failed: %v", err)
	}
	claims, err := signer.Parse(tok)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if claims.WorkspaceID != "ws_u1" {
		t.Errorf("expected workspace_id 'ws_u1', got %q", claims.WorkspaceID)
	}

	// Backwards compatibility: legacy Sign must still work and leave
	// WorkspaceID empty.
	legacyTok, err := signer.Sign("u1", "user")
	if err != nil {
		t.Fatalf("legacy Sign failed: %v", err)
	}
	legacyClaims, err := signer.Parse(legacyTok)
	if err != nil {
		t.Fatalf("legacy Parse failed: %v", err)
	}
	if legacyClaims.WorkspaceID != "" {
		t.Errorf("expected empty workspace_id for legacy Sign, got %q", legacyClaims.WorkspaceID)
	}
}

// TestNewSignerValidation tests that NewSigner validates inputs.
func TestNewSignerValidation(t *testing.T) {
	tests := []struct {
		name      string
		secret    string
		ttl       time.Duration
		wantError bool
	}{
		{"valid", "this-is-a-valid-secret-32bytesXX", 24 * time.Hour, false},
		{"secret too short", "short", 24 * time.Hour, true},
		{"empty secret", "", 24 * time.Hour, true},
		{"zero ttl", "this-is-a-valid-secret-32bytesXX", 0, true},
		{"negative ttl", "this-is-a-valid-secret-32bytesXX", -1 * time.Hour, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewSigner(tt.secret, tt.ttl)
			if tt.wantError && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

// TestSignValidation tests that Sign/SignWithWorkspace validate inputs.
func TestSignValidation(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes-long"
	signer, err := NewSigner(secret, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewSigner failed: %v", err)
	}

	tests := []struct {
		name        string
		userID      string
		role        string
		workspaceID string
		wantError   bool
	}{
		{"valid", "user123", "admin", "ws1", false},
		{"empty userID", "", "admin", "ws1", true},
		{"empty role", "user123", "", "ws1", true},
		{"empty workspace ok", "user123", "admin", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := signer.SignWithWorkspace(tt.userID, tt.role, tt.workspaceID)
			if tt.wantError && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

// TestParseEmptyToken tests that Parse handles empty/invalid tokens.
func TestParseEmptyToken(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes-long"
	signer, err := NewSigner(secret, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewSigner failed: %v", err)
	}

	tests := []string{
		"",
		"   ",
		"not-a-jwt",
		"a.b",
	}

	for _, token := range tests {
		t.Run("token="+token, func(t *testing.T) {
			_, err := signer.Parse(token)
			if err == nil {
				t.Error("Expected error for invalid token, got nil")
			}
		})
	}
}
