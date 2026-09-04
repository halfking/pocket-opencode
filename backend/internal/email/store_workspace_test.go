package email

// store_workspace_test.go — workspace-isolation integration tests for the email
// store's scoped methods.
//
// These need a live PostgreSQL instance. Set POCKET_TEST_POSTGRES_DSN (or
// POCKET_POSTGRES_DSN) to run them; otherwise they skip so `go test ./...`
// stays green on machines without a DB.
//
// Each test runs in its own schema, dropped on cleanup, so parallel runs are
// safe.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testDSN() string {
	for _, k := range []string{"POCKET_TEST_POSTGRES_DSN", "POCKET_POSTGRES_DSN"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// newWorkspaceTestStore provisions an isolated schema + pool + Store.
func newWorkspaceTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping email workspace integration test")
	}
	ctx := context.Background()

	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	schema := "email_ws_test_" + hex.EncodeToString(buf)

	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach postgres: %v", err)
	}
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("scoped pool: %v", err)
	}

	store, err := NewStore(pool)
	if err != nil {
		t.Fatalf("NewStore (migrate): %v", err)
	}

	cleanup := func() {
		pool.Close()
		if _, err := rootPool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
		rootPool.Close()
	}
	return store, cleanup
}

func seedAccount(t *testing.T, store *Store, id, userID, workspaceID string) {
	t.Helper()
	acc := &Account{
		ID:              id,
		UserID:          userID,
		WorkspaceID:     workspaceID,
		DisplayName:     "acct " + id,
		EmailAddress:    id + "@example.com",
		IMAPHost:        "imap.example.com",
		IMAPPort:        993,
		AuthType:        "password",
		SyncIntervalMin: 15,
		Enabled:         true,
		CreatedAt:       time.Now().Unix(),
	}
	if err := store.InsertAccount(context.Background(), acc, "enc-cred"); err != nil {
		t.Fatalf("insert account %s: %v", id, err)
	}
}

// The same user in two workspaces must get two independent daily summaries.
// The original schema carried UNIQUE(user_id, summary_date), so the second
// workspace's write silently overwrote the first.
func TestUpsertSummaryScopedKeepsWorkspacesSeparate(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const user = "user-1"
	const date = "2026-08-08"

	for _, ws := range []string{"ws-a", "ws-b"} {
		sum := &DailySummary{
			UserID:      user,
			WorkspaceID: ws,
			SummaryDate: date,
			TotalCount:  1,
			Content:     "summary for " + ws,
			CreatedAt:   time.Now().Unix(),
		}
		if err := store.UpsertSummaryScoped(ctx, sum); err != nil {
			t.Fatalf("upsert %s: %v", ws, err)
		}
	}

	for _, ws := range []string{"ws-a", "ws-b"} {
		got, err := store.GetSummaryByDateScoped(ctx, user, ws, date)
		if err != nil {
			t.Fatalf("get %s: %v", ws, err)
		}
		if got == nil {
			t.Fatalf("%s: summary missing — the other workspace overwrote it", ws)
		}
		if want := "summary for " + ws; got.Content != want {
			t.Fatalf("%s: content = %q, want %q", ws, got.Content, want)
		}
	}
}

// Re-running the summary for one workspace must update in place, not duplicate
// or bleed into the other workspace.
func TestUpsertSummaryScopedIsIdempotentPerWorkspace(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const user = "user-1"
	const date = "2026-08-08"

	first := &DailySummary{
		UserID: user, WorkspaceID: "ws-a", SummaryDate: date,
		TotalCount: 1, Content: "v1", CreatedAt: time.Now().Unix(),
	}
	if err := store.UpsertSummaryScoped(ctx, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second := &DailySummary{
		ID: first.ID, UserID: user, WorkspaceID: "ws-a", SummaryDate: date,
		TotalCount: 7, Content: "v2", CreatedAt: time.Now().Unix(),
	}
	if err := store.UpsertSummaryScoped(ctx, second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := store.GetSummaryByDateScoped(ctx, user, "ws-a", date)
	if err != nil || got == nil {
		t.Fatalf("get ws-a: %v (got %v)", err, got)
	}
	if got.Content != "v2" || got.TotalCount != 7 {
		t.Fatalf("expected the row to be updated in place, got content=%q total=%d", got.Content, got.TotalCount)
	}

	// ws-b must still be empty.
	other, err := store.GetSummaryByDateScoped(ctx, user, "ws-b", date)
	if err != nil {
		t.Fatalf("get ws-b: %v", err)
	}
	if other != nil {
		t.Fatalf("ws-b should have no summary, got %#v", other)
	}
}

// ListEnabledAccountsWithWorkspace must populate WorkspaceID; the legacy
// ListEnabledAccounts omits the column and returns it empty, which silently
// collapsed every account into the "default" workspace.
func TestListEnabledAccountsWithWorkspacePopulatesWorkspace(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-a", "user-1", "ws-a")
	seedAccount(t, store, "acct-b", "user-1", "ws-b")

	accounts, err := store.ListEnabledAccountsWithWorkspace(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byID := make(map[string]string, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a.WorkspaceID
	}
	for id, wantWS := range map[string]string{"acct-a": "ws-a", "acct-b": "ws-b"} {
		if got := byID[id]; got != wantWS {
			t.Fatalf("%s: workspace = %q, want %q", id, got, wantWS)
		}
	}

	// Contrast with the legacy method to document why the new one exists.
	legacy, err := store.ListEnabledAccounts(ctx)
	if err != nil {
		t.Fatalf("legacy list: %v", err)
	}
	for _, a := range legacy {
		if a.WorkspaceID != "" {
			t.Fatalf("ListEnabledAccounts unexpectedly returned a workspace (%q); "+
				"if it was fixed, the scheduler can use it directly", a.WorkspaceID)
		}
	}
}

// Revoking must refuse accounts outside the given (user, workspace).
func TestRevokeOAuthTokenScopedRejectsForeignAccount(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-a", "user-1", "ws-a")

	if err := store.RevokeOAuthTokenScoped(ctx, "acct-a", "user-1", "ws-b"); err == nil {
		t.Fatal("revoking from the wrong workspace must fail")
	}
	if err := store.RevokeOAuthTokenScoped(ctx, "acct-a", "user-2", "ws-a"); err == nil {
		t.Fatal("revoking as the wrong user must fail")
	}

	// The account must still be enabled after the rejected attempts.
	acc, _, err := store.GetAccountByIDScoped(ctx, "acct-a", "user-1", "ws-a")
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	if !acc.Enabled {
		t.Fatal("a rejected revoke must not disable the account")
	}

	if err := store.RevokeOAuthTokenScoped(ctx, "acct-a", "user-1", "ws-a"); err != nil {
		t.Fatalf("owner revoke should succeed: %v", err)
	}
	acc, _, err = store.GetAccountByIDScoped(ctx, "acct-a", "user-1", "ws-a")
	if err != nil {
		t.Fatalf("get account after revoke: %v", err)
	}
	if acc.Enabled {
		t.Fatal("revoke should disable the account")
	}
}

// ListEmailsByDayScoped must not mix another workspace's mail into the day.
func TestListEmailsByDayScopedFiltersWorkspace(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-a", "user-1", "ws-a")
	seedAccount(t, store, "acct-b", "user-1", "ws-b")

	day := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ id, acct, ws string }{
		{"mail-a", "acct-a", "ws-a"},
		{"mail-b", "acct-b", "ws-b"},
	} {
		if err := store.InsertEmail(ctx, Email{
			ID: tc.id, AccountID: tc.acct, WorkspaceID: tc.ws,
			MessageID:   tc.id + "@example.com",
			FromAddress: "sender@example.com", Subject: "s", Snippet: "x",
			Date: day.Unix(),
		}); err != nil {
			t.Fatalf("insert %s: %v", tc.id, err)
		}
	}

	got, err := store.ListEmailsByDayScoped(ctx, "user-1", "ws-a", "2026-08-08", 0)
	if err != nil {
		t.Fatalf("list scoped: %v", err)
	}
	if len(got) != 1 || got[0].ID != "mail-a" {
		t.Fatalf("ws-a should see exactly its own mail, got %d rows %#v", len(got), got)
	}
}

// Guard for the migration: the legacy UNIQUE(user_id, summary_date) must be
// gone, replaced by the workspace-aware unique index.
func TestDailySummaryUniqueIndexIsWorkspaceAware(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	var legacy int
	if err := store.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM pg_constraint
		WHERE conname = 'daily_summaries_user_id_summary_date_key'
	`).Scan(&legacy); err != nil {
		t.Fatalf("query legacy constraint: %v", err)
	}
	if legacy != 0 {
		t.Fatal("legacy UNIQUE(user_id, summary_date) is still present")
	}

	var idx int
	if err := store.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND indexname = 'idx_daily_summaries_user_ws_date'
	`).Scan(&idx); err != nil {
		t.Fatalf("query new index: %v", err)
	}
	if idx != 1 {
		t.Fatalf("expected the workspace-aware unique index, found %d", idx)
	}
}
