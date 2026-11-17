package main

import (
	"fmt"
	"os"
	"time"

	"github.com/kaixuan/identity-go/token"
)

func main() {
	secretEnv := os.Getenv("IDENTITY_SHARED_SECRET")
	if secretEnv == "" {
		fmt.Fprintln(os.Stderr, "IDENTITY_SHARED_SECRET not set")
		os.Exit(1)
	}

	// Load shared secret using identity-go's method
	secret, err := token.LoadSharedSecret("IDENTITY_SHARED_SECRET")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load shared secret: %v\n", err)
		os.Exit(1)
	}

	// Create issuer for "pocket"
	issuer := token.Issuer{
		Name:   "pocket",
		Secret: secret,
	}

	// Create claims
	claims := &token.Claims{
		Subject:  "testuser",
		UserID:   "testuser",
		TenantID: "ws_test",
		Audience: "pocket-api",
		Roles:    []string{"admin"},
		Scope:    "admin",
		Extra: map[string]any{
			"workspace_id": "ws_test",
		},
	}

	// Sign token with 24 hour TTL
	tokenString, err := token.SignHS256(issuer, claims, 24*time.Hour)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to sign token: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(tokenString)
}
