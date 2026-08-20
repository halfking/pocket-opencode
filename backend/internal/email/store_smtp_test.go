package email

// store_smtp_test.go — integration tests for the SMTP settings columns.
//
// These need a live PostgreSQL instance; see store_workspace_test.go for the
// POCKET_TEST_POSTGRES_DSN convention and the shared schema harness.
//
// Motivation: ListAccountsScoped used to omit smtp_host/smtp_port from its
// SELECT, so GET /api/email/accounts never returned SMTP config and the UI had
// nothing to prefill. A SELECT/scan drift like that is invisible without a real
// database — the same class of defect that hid the InsertEmail column mismatch.

import (
	"context"
	"testing"
)

// SMTP settings written through UpsertSMTPSettingsScoped must come back from
// both ListAccountsScoped and GetAccountByIDScoped.
func TestListAccountsScopedReturnsSMTPSettings(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		user = "user-1"
		ws   = "ws-a"
		acct = "acct-smtp"
	)
	seedAccount(t, store, acct, user, ws)

	// A freshly created account has no SMTP config.
	list, err := store.ListAccountsScoped(ctx, user, ws)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 account, got %d", len(list))
	}
	if list[0].SMTPHost != "" || list[0].SMTPPort != 0 {
		t.Fatalf("fresh account should have no SMTP config, got %q:%d", list[0].SMTPHost, list[0].SMTPPort)
	}

	if err := store.UpsertSMTPSettingsScoped(ctx, acct, user, ws, "smtp.example.com", 587, "enc-smtp-cred", true); err != nil {
		t.Fatalf("upsert smtp: %v", err)
	}

	list, err = store.ListAccountsScoped(ctx, user, ws)
	if err != nil {
		t.Fatalf("list after upsert: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 account, got %d", len(list))
	}
	if list[0].SMTPHost != "smtp.example.com" || list[0].SMTPPort != 587 {
		t.Fatalf("list dropped SMTP settings: got %q:%d", list[0].SMTPHost, list[0].SMTPPort)
	}

	// GetAccountByIDScoped must agree with the list view.
	got, _, err := store.GetAccountByIDScoped(ctx, acct, user, ws)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.SMTPHost != "smtp.example.com" || got.SMTPPort != 587 {
		t.Fatalf("get disagrees with list: got %q:%d", got.SMTPHost, got.SMTPPort)
	}
}

// The encrypted SMTP credential must never ride along on the account list or
// the single-account read; it is only reachable via GetSMTPCredentialScoped.
func TestSMTPCredentialOnlyViaDedicatedGetter(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		user = "user-1"
		ws   = "ws-a"
		acct = "acct-cred"
	)
	seedAccount(t, store, acct, user, ws)
	if err := store.UpsertSMTPSettingsScoped(ctx, acct, user, ws, "smtp.example.com", 465, "enc-smtp-cred", true); err != nil {
		t.Fatalf("upsert smtp: %v", err)
	}

	host, emailAddr, port, cred, err := store.GetSMTPCredentialScoped(ctx, acct, user, ws)
	if err != nil {
		t.Fatalf("get smtp credential: %v", err)
	}
	if host != "smtp.example.com" || port != 465 {
		t.Fatalf("unexpected smtp target %q:%d", host, port)
	}
	if cred != "enc-smtp-cred" {
		t.Fatalf("credential round-trip failed: %q", cred)
	}
	if emailAddr != acct+"@example.com" {
		t.Fatalf("unexpected username fallback %q", emailAddr)
	}
}

// Omitting updateCredential preserves the stored credential (host/port-only
// edit); passing an empty credential with updateCredential clears it. This is
// the contract the frontend "留空表示不变更 / 清空凭证" semantics rely on.
func TestUpsertSMTPSettingsCredentialSemantics(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		user = "user-1"
		ws   = "ws-a"
		acct = "acct-sem"
	)
	seedAccount(t, store, acct, user, ws)
	if err := store.UpsertSMTPSettingsScoped(ctx, acct, user, ws, "smtp.example.com", 587, "enc-v1", true); err != nil {
		t.Fatalf("seed smtp: %v", err)
	}

	// host/port-only edit: credential must survive.
	if err := store.UpsertSMTPSettingsScoped(ctx, acct, user, ws, "smtp2.example.com", 465, "", false); err != nil {
		t.Fatalf("host-only edit: %v", err)
	}
	host, _, port, cred, err := store.GetSMTPCredentialScoped(ctx, acct, user, ws)
	if err != nil {
		t.Fatalf("read after host-only edit: %v", err)
	}
	if host != "smtp2.example.com" || port != 465 {
		t.Fatalf("host/port not updated: %q:%d", host, port)
	}
	if cred != "enc-v1" {
		t.Fatalf("credential should be preserved when updateCredential=false, got %q", cred)
	}

	// explicit clear.
	if err := store.UpsertSMTPSettingsScoped(ctx, acct, user, ws, "smtp2.example.com", 465, "", true); err != nil {
		t.Fatalf("clear credential: %v", err)
	}
	_, _, _, cred, err = store.GetSMTPCredentialScoped(ctx, acct, user, ws)
	if err != nil {
		t.Fatalf("read after clear: %v", err)
	}
	if cred != "" {
		t.Fatalf("credential should be cleared, got %q", cred)
	}

	// clearing host resets the whole SMTP config (port 0 is only legal here).
	if err := store.UpsertSMTPSettingsScoped(ctx, acct, user, ws, "", 0, "", true); err != nil {
		t.Fatalf("clear host: %v", err)
	}
	list, err := store.ListAccountsScoped(ctx, user, ws)
	if err != nil {
		t.Fatalf("list after clear: %v", err)
	}
	if list[0].SMTPHost != "" || list[0].SMTPPort != 0 {
		t.Fatalf("SMTP config should be empty, got %q:%d", list[0].SMTPHost, list[0].SMTPPort)
	}
}

// SMTP writes must not cross workspace or user boundaries.
func TestUpsertSMTPSettingsScopedRejectsForeignAccount(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-a", "user-1", "ws-a")

	cases := []struct{ name, user, ws string }{
		{"foreign workspace", "user-1", "ws-b"},
		{"foreign user", "user-2", "ws-a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := store.UpsertSMTPSettingsScoped(ctx, "acct-a", c.user, c.ws, "evil.example.com", 587, "enc", true)
			if err != ErrNotFound {
				t.Fatalf("want ErrNotFound, got %v", err)
			}
		})
	}

	// The owner's config must be untouched by the rejected writes.
	host, _, _, _, err := store.GetSMTPCredentialScoped(ctx, "acct-a", "user-1", "ws-a")
	if err != nil {
		t.Fatalf("owner read: %v", err)
	}
	if host != "" {
		t.Fatalf("foreign write leaked into owner row: %q", host)
	}

	// Reads are scoped too.
	if _, _, _, _, err := store.GetSMTPCredentialScoped(ctx, "acct-a", "user-1", "ws-b"); err != ErrNotFound {
		t.Fatalf("cross-workspace read: want ErrNotFound, got %v", err)
	}
}
