package vault

import (
	"os"
	"testing"
)

// vaultGuardDecision describes what the test helper should do for the
// current environment. This is a pure function so the guard can be unit
// tested without spinning up Postgres.
type vaultGuardDecision int

const (
	// vaultProceed means a DSN is available and the test should dial Postgres.
	vaultProceed vaultGuardDecision = iota
	// vaultSkip means no DSN is set and CI is unset — the test skips.
	vaultSkip
	// vaultFail means no DSN is set and CI is set — the test must fail loudly.
	vaultFail
)

// decideVaultGuard returns the decision the test helper should make for the
// given environment. Centralising this here lets us unit-test the policy
// without spinning up Postgres or trapping runtime.Goexit.
func decideVaultGuard(dsn string, ciSet bool) vaultGuardDecision {
	if dsn != "" {
		return vaultProceed
	}
	if ciSet {
		return vaultFail
	}
	return vaultSkip
}

func TestDecideVaultGuard_NoDSN_NoCI_Skips(t *testing.T) {
	if got := decideVaultGuard("", false); got != vaultSkip {
		t.Fatalf("developer-machine no-DSN path must skip, got %d", got)
	}
}

func TestDecideVaultGuard_NoDSN_CI_Fails(t *testing.T) {
	if got := decideVaultGuard("", true); got != vaultFail {
		t.Fatalf("CI no-DSN path must fail loud, got %d", got)
	}
}

func TestDecideVaultGuard_DSN_Proceeds(t *testing.T) {
	if got := decideVaultGuard("postgres://example", false); got != vaultProceed {
		t.Fatalf("DSN-set path must proceed, got %d", got)
	}
	if got := decideVaultGuard("postgres://example", true); got != vaultProceed {
		t.Fatalf("DSN-set path must proceed regardless of CI, got %d", got)
	}
}

// TestVaultGuardIntegration wires decideVaultGuard into the live environment
// so a future caller cannot accidentally bypass the policy.
func TestVaultGuardIntegration(t *testing.T) {
	dsn := vaultTestDSN()
	ciSet := false
	if _, ok := os.LookupEnv("CI"); ok {
		ciSet = true
	}
	got := decideVaultGuard(dsn, ciSet)
	switch got {
	case vaultProceed:
		t.Logf("vault integration test will dial Postgres (DSN present)")
	case vaultSkip:
		t.Logf("vault integration test will skip (developer path, no DSN)")
	case vaultFail:
		t.Fatalf("vault integration test refused to run: CI=%v DSN=%q", ciSet, dsn)
	}
}
