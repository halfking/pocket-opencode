package email

// store_email_scope_test.go — the tenant boundary on the single-email read.
//
// Needs a live PostgreSQL instance; see store_workspace_test.go for the
// POCKET_TEST_POSTGRES_DSN convention and the per-test schema harness.
//
// Why this file exists: the learning "add this email to my study material"
// action resolved an email through the *unscoped* GetEmailByID and then
// compared msg.WorkspaceID in Go. The detail projection never selects that
// column, so the field was always "" and the check let every caller through —
// any logged-in user could file another user's mail as their own. The fix is to
// resolve through the account-joined reader; this test pins that the join
// actually excludes a foreign account, which no amount of Go-level reasoning
// can guarantee.

import (
	"context"
	"testing"
	"time"
)

func seedEmail(t *testing.T, store *Store, id, accountID, workspaceID, subject string) {
	t.Helper()
	if err := store.InsertEmail(context.Background(), Email{
		ID: id, AccountID: accountID, WorkspaceID: workspaceID,
		MessageID:   id + "@example.com",
		FromAddress: "sender@example.com", Subject: subject, Snippet: "snippet",
		Date: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

// Another user's mail must read as "does not exist" — same answer as an unknown
// id, so the id cannot be used to probe another tenant's inbox.
func TestGetEmailByIDScopedRejectsForeignAccount(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-victim", "victim", "ws-a")
	seedAccount(t, store, "acct-attacker", "mallory", "ws-b")
	seedEmail(t, store, "mail-victim", "acct-victim", "ws-a", "Q4 severance plan")

	cases := []struct {
		name, user, ws string
	}{
		{"another user in the same workspace", "mallory", "ws-a"},
		{"the same user in another workspace", "victim", "ws-b"},
		{"an unrelated user and workspace", "mallory", "ws-b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := store.GetEmailByIDScoped(ctx, "mail-victim", c.user, c.ws)
			if err != nil {
				t.Fatalf("scoped read: %v", err)
			}
			if got != nil {
				t.Fatalf("cross-tenant read leaked the email: %+v", got)
			}
		})
	}
}

// The owner still gets the mail, and with the same columns as the unscoped
// reader — a scoped variant that quietly dropped a field would be a new bug.
func TestGetEmailByIDScopedReturnsOwnedEmail(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-a", "user-1", "ws-a")
	seedEmail(t, store, "mail-1", "acct-a", "ws-a", "Q3 review")

	scoped, err := store.GetEmailByIDScoped(ctx, "mail-1", "user-1", "ws-a")
	if err != nil || scoped == nil {
		t.Fatalf("owner scoped read: err=%v got=%v", err, scoped)
	}
	unscoped, err := store.GetEmailByID(ctx, "mail-1")
	if err != nil || unscoped == nil {
		t.Fatalf("unscoped read: err=%v got=%v", err, unscoped)
	}
	if scoped.Subject != "Q3 review" || scoped.AccountID != "acct-a" {
		t.Errorf("scoped read returned %+v", scoped)
	}
	if scoped.Snippet != unscoped.Snippet || scoped.Date != unscoped.Date ||
		scoped.FromAddress != unscoped.FromAddress || scoped.HasAttachments != unscoped.HasAttachments {
		t.Errorf("scoped projection drifted from the unscoped one:\n scoped=%+v\nunscoped=%+v", scoped, unscoped)
	}
}

// An unknown id is (nil, nil) on both readers — the contract every caller
// branches on. A regression here would panic the resolver that dereferences it.
func TestGetEmailByIDScopedMissingIsNilNotError(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()

	got, err := store.GetEmailByIDScoped(context.Background(), "no-such-mail", "user-1", "ws-a")
	if err != nil {
		t.Fatalf("missing email must not be an error, got %v", err)
	}
	if got != nil {
		t.Fatalf("missing email must be (nil, nil), got %+v", got)
	}
}
