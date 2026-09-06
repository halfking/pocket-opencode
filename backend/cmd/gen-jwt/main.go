// Command gen-jwt mints a HS256 JWT compatible with backend/internal/auth.
//
// Usage:
//
//	go run ./cmd/gen-jwt --user u1 --role user --workspace ws1 [--ttl 24h]
//
// Reads POCKET_JWT_SECRET from the environment (same contract as pocketd).
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
)

func main() {
	user := flag.String("user", "", "user_id claim (required)")
	role := flag.String("role", "user", "role claim")
	workspace := flag.String("workspace", "", "workspace_id claim (optional)")
	ttl := flag.Duration("ttl", 24*time.Hour, "token TTL")
	flag.Parse()

	if *user == "" {
		fmt.Fprintln(os.Stderr, "--user is required")
		os.Exit(2)
	}
	secret := os.Getenv("POCKET_JWT_SECRET")
	if secret == "" {
		fmt.Fprintln(os.Stderr, "POCKET_JWT_SECRET env var is required")
		os.Exit(2)
	}
	signer, err := auth.NewSigner(secret, *ttl)
	if err != nil {
		fmt.Fprintln(os.Stderr, "NewSigner:", err)
		os.Exit(2)
	}
	tok, err := signer.SignWithWorkspace(*user, *role, *workspace)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Sign:", err)
		os.Exit(2)
	}
	fmt.Println(tok)
}