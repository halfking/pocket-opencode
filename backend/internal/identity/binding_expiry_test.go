package identity

import (
	"context"
	"testing"
	"time"
)

func TestIssueBindingCannotOutliveMembership(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	if err := store.CreateDefaultWorkspace(ctx, &Workspace{ID: "ws-expiry", OwnerID: "owner", Name: "expiry"}); err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(10 * time.Second).Unix()
	if err := store.AddMember(ctx, &Member{WorkspaceID: "ws-expiry", UserID: "guest", Role: RoleInvitee, ExpiresAt: expiry}); err != nil {
		t.Fatal(err)
	}
	signer := testSigner(t)
	raw, issued, err := store.IssueBinding(ctx, signer, "ws-expiry", "guest", "tenant-a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if issued.ExpiresAt > expiry {
		t.Fatalf("binding expires %d after membership %d", issued.ExpiresAt, expiry)
	}
	verified, err := signer.Verify(raw)
	if err != nil || verified.ExpiresAt > expiry {
		t.Fatalf("signed expiry: %+v %v", verified, err)
	}
}
