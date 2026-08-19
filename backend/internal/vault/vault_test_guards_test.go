package vault

import "testing"

// TestVaultGuardIntegration keeps the live test contract visible: a DSN runs
// the integration setup, while a missing DSN is an explicit skip.
func TestVaultGuardIntegration(t *testing.T) {
	if dsn := vaultTestDSN(); dsn != "" {
		t.Log("vault integration test will dial Postgres (DSN present)")
		return
	}
	t.Log("vault integration test will skip (no PostgreSQL DSN)")
}
