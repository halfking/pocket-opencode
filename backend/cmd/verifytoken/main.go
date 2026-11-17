package main

import (
	"fmt"
	"os"

	"github.com/kaixuan/identity-go/token"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: verifytoken <jwt>")
		os.Exit(1)
	}

	raw := os.Args[1]

	// Load shared secret
	secret, err := token.LoadSharedSecret("IDENTITY_SHARED_SECRET")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load secret: %v\n", err)
		os.Exit(1)
	}

	// Build issuer allowlist
	allowlist := "redclaw,memora,llm-gateway,pocket,acc"
	issuers, err := token.Allowlist(allowlist, secret)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to build allowlist: %v\n", err)
		os.Exit(1)
	}

	// Verify token
	claims, err := token.VerifyMultiIssuer(raw, issuers, "pocket-api")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Verification failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Token valid!\n")
	fmt.Printf("  Issuer: %s\n", claims.Issuer)
	fmt.Printf("  Subject: %s\n", claims.Subject)
	fmt.Printf("  UserID: %s\n", claims.UserID)
	fmt.Printf("  TenantID: %s\n", claims.TenantID)
	fmt.Printf("  Audience: %s\n", claims.Audience)
	fmt.Printf("  Roles: %v\n", claims.Roles)
	fmt.Printf("  Scope: %s\n", claims.Scope)
	fmt.Printf("  Extra: %v\n", claims.Extra)
}
