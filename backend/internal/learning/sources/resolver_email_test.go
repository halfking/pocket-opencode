package sources

// resolver_email_test.go — tenant isolation on the "add email to learning" path.
//
// The email branch used to call the *unscoped* email.Store.GetEmailByID and
// then compare `msg.WorkspaceID` against the caller's workspace in Go. That
// check was dead code: the detail projection GetEmailByID scans never selects
// workspace_id, so the field was always "" and the `!= ""` guard short-circuited
// to "allowed" for every caller. Any logged-in user could file another user's
// email as their own study material — a cross-tenant read, not a nit.
//
// The fake below reproduces that state exactly: it answers with a real-looking
// email whose WorkspaceID is empty, which is precisely what the old store
// method returned. A resolver that trusts the field will hand back the
// ResolvedSource; only a resolver that scopes the *lookup* rejects it.

import (
	"context"
	"errors"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/learning"
)

// scopedEmailStore answers the way email.Store.GetEmailByIDScoped is required
// to: a mail that is not owned by (user, workspace) is reported as missing.
// The real enforcement is the JOIN against email_accounts; `owners` stands in
// for that account table.
type scopedEmailStore struct {
	byID     map[string]*email.Email
	owners   map[string]string // email id -> "<user>/<workspace>"
	askedFor []string          // "<id>|<user>|<ws>" per call
	err      error
}

func (f *scopedEmailStore) GetEmailByIDScoped(_ context.Context, id, userID, workspaceID string) (*email.Email, error) {
	f.askedFor = append(f.askedFor, id+"|"+userID+"|"+workspaceID)
	if f.err != nil {
		return nil, f.err
	}
	msg, ok := f.byID[id]
	if !ok || f.owners[id] != userID+"/"+workspaceID {
		return nil, nil // (nil, nil) is this store's "does not exist"
	}
	return msg, nil
}

func foreignEmail() *email.Email {
	return &email.Email{
		ID:          "mail-victim",
		AccountID:   "acct-victim",
		Subject:     "Q4 severance plan",
		Snippet:     "the numbers only you should see",
		AISummary:   "sensitive summary",
		Category:    "finance",
		Importance:  "high",
		WorkspaceID: "", // what the unscoped reader produced
	}
}

// The security case: a mail the scoped store refuses to hand out must resolve
// as "not found" — and must not be dereferenced on the way there. The old
// branch called GetEmailByID, which answers (nil, nil) for an unknown id, and
// then read msg.WorkspaceID: an unknown id panicked the request.
func TestResolveEmailRejectsForeignMail(t *testing.T) {
	store := &scopedEmailStore{
		byID:   map[string]*email.Email{"mail-victim": foreignEmail()},
		owners: map[string]string{"mail-victim": "victim/ws-b"},
	}
	r := &Resolver{emails: store}

	got, err := r.Resolve(context.Background(), string(learning.SourceEmail), "mail-victim", "mallory", "ws-a")
	if !errors.Is(err, learning.ErrSourceNotFound) {
		t.Fatalf("resolving another user's email: err = %v, want ErrSourceNotFound", err)
	}
	if got != nil {
		t.Fatalf("a foreign email must not produce a learning source, got %+v", got)
	}
	if want := "mail-victim|mallory|ws-a"; store.askedFor[0] != want {
		t.Errorf("lookup scope = %q, want %q", store.askedFor[0], want)
	}
}

// The scope has to reach the store, not just a post-hoc comparison: if the
// lookup is called without the caller's identity the store has nothing to
// enforce. This is the assertion that pins the original defect — the old code
// called the unscoped GetEmailByID and compared a column it never selected.
func TestResolveEmailPassesCallerScopeToStore(t *testing.T) {
	store := &scopedEmailStore{byID: map[string]*email.Email{}}
	r := &Resolver{emails: store}

	if _, err := r.Resolve(context.Background(), string(learning.SourceEmail), "mail-1", "alice", "ws-a"); !errors.Is(err, learning.ErrSourceNotFound) {
		t.Fatalf("an unknown email must be reported as not found, got %v", err)
	}
	if len(store.askedFor) != 1 {
		t.Fatalf("expected exactly one lookup, got %v", store.askedFor)
	}
	if want := "mail-1|alice|ws-a"; store.askedFor[0] != want {
		t.Errorf("lookup scope = %q, want %q — the user id and workspace must be part of the query", store.askedFor[0], want)
	}
}

// An empty workspace falls back to the default tenant rather than querying the
// empty string, which would match rows written before workspace isolation.
func TestResolveEmailNormalizesEmptyWorkspace(t *testing.T) {
	store := &scopedEmailStore{byID: map[string]*email.Email{}}
	r := &Resolver{emails: store}

	if _, err := r.Resolve(context.Background(), string(learning.SourceEmail), "mail-1", "alice", ""); err == nil {
		t.Fatal("an unknown email must be reported as not found")
	}
	if want := "mail-1|alice|" + learning.DefaultWorkspaceID; store.askedFor[0] != want {
		t.Errorf("lookup scope = %q, want %q", store.askedFor[0], want)
	}
}

// The owner still gets their source, with the classification lifted into tags.
func TestResolveEmailReturnsOwnedMail(t *testing.T) {
	store := &scopedEmailStore{
		byID:   map[string]*email.Email{"mail-1": foreignEmail()},
		owners: map[string]string{"mail-1": "alice/ws-a"},
	}
	r := &Resolver{emails: store}

	got, err := r.Resolve(context.Background(), string(learning.SourceEmail), "mail-1", "alice", "ws-a")
	if err != nil {
		t.Fatalf("own email: %v", err)
	}
	if got.Title != "Q4 severance plan" || got.Summary != "sensitive summary" {
		t.Errorf("resolved source = %+v", got)
	}
	if len(got.Tags) != 2 {
		t.Errorf("expected the classifier labels to survive, got %v", got.Tags)
	}
}

// A backend outage must not be laundered into "not found": that would make the
// client silently drop the user's action.
func TestResolveEmailPropagatesStoreFailure(t *testing.T) {
	r := &Resolver{emails: &scopedEmailStore{err: errors.New("connection refused")}}
	if _, err := r.Resolve(context.Background(), string(learning.SourceEmail), "mail-1", "alice", "ws-a"); err == nil {
		t.Fatal("a store failure must not be reported as a missing source")
	} else if errors.Is(err, learning.ErrSourceNotFound) {
		t.Fatalf("a connection failure was laundered into not-found: %v", err)
	}
}

// The nil-store path (remote-only mode) must stay "not found", not a panic.
func TestResolveEmailWithoutStoreIsNotFound(t *testing.T) {
	r := &Resolver{}
	if _, err := r.Resolve(context.Background(), string(learning.SourceEmail), "mail-1", "alice", "ws-a"); !errors.Is(err, learning.ErrSourceNotFound) {
		t.Fatalf("err = %v, want ErrSourceNotFound", err)
	}
}
