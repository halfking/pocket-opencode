package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/halfking/pocket-opencode/backend/internal/rss"
)

func TestRSSQueuedSourceReceiptAndSchedule(t *testing.T) {
	server, pool, fallbackToken, cleanup := newRSSRoutePG(t)
	defer cleanup()
	ctx := context.Background()
	owners, err := identity.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	server.identityStore = owners
	for _, workspace := range []string{"reader-workspace", "other-workspace"} {
		if err := owners.CreateDefaultWorkspace(ctx, &identity.Workspace{ID: workspace, OwnerID: "reader", Name: workspace}); err != nil {
			t.Fatal(err)
		}
	}
	token := func(workspace string) string {
		value, err := server.jwtSigner.SignWithWorkspace("reader", "member", workspace)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	const path = "/api/rss/sources/queued"
	const initial = `{"operationId":"offline-create","clientUpdatedAt":"2026-01-02T03:04:05.123Z","url":"https://example.com/feed","title":"Offline feed","enabled":true,"fetchInterval":"45m","portalEpoch":3}`
	const workspace = "reader-workspace"
	reader := token(workspace)
	for _, request := range []struct {
		token, body string
		code        int
	}{
		{fallbackToken, initial, http.StatusUnauthorized},
		{reader, strings.Replace(initial, `,"enabled":true`, "", 1), http.StatusUnprocessableEntity},
		{reader, strings.Replace(initial, `"portalEpoch":3`, `"portalEpoch":-1`, 1), http.StatusUnprocessableEntity},
		{reader, strings.Replace(initial, `"45m"`, `"invalid"`, 1), http.StatusUnprocessableEntity},
	} {
		if code, body := doJSON(t, server, http.MethodPost, path, request.token, request.body); code != request.code {
			t.Fatalf("expected %d, got %d %+v", request.code, code, body)
		}
	}
	code, first := doJSON(t, server, http.MethodPost, path, reader, initial)
	if code != http.StatusOK || first["user_id"] != "reader" || first["workspace_id"] != workspace || first["operation_id"] != "offline-create" {
		t.Fatalf("create: %d %+v", code, first)
	}
	id := first["source_id"].(string)
	if code, body := doJSON(t, server, http.MethodPost, path, reader, initial); code != http.StatusOK || body["source_id"] != id {
		t.Fatalf("lost receipt replay: %d %+v", code, body)
	}
	for _, changed := range []string{
		strings.Replace(initial, "Offline feed", "Changed settings", 1),
		strings.Replace(initial, `"portalEpoch":3`, `"portalEpoch":4`, 1),
		strings.Replace(initial, ".123Z", ".124Z", 1),
		strings.Replace(initial, "offline-create", "different-operation", 1),
	} {
		if code, body := doJSON(t, server, http.MethodPost, path, reader, changed); code != http.StatusConflict || body["error"] != "rss_source_conflict" {
			t.Fatalf("changed replay or duplicate URL: %d %+v", code, body)
		}
	}
	if code, body := doJSON(t, server, http.MethodPost, path, token("other-workspace"), initial); code != http.StatusOK || body["source_id"] == id || body["workspace_id"] != "other-workspace" {
		t.Fatalf("op identity was not scoped: %d %+v", code, body)
	}
	store, err := rss.NewStore(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	scope := rss.Scope{UserID: "reader", WorkspaceID: workspace}
	journal, err := store.ListPortableChanges(ctx, scope, 0, 10)
	if err != nil || len(journal.Changes) != 1 {
		t.Fatalf("replay or conflicting writes duplicated source journal: %+v %v", journal, err)
	}
	change := journal.Changes[0]
	if change.ClientUpdatedAt == nil || change.ClientUpdatedAt.UTC().Format(time.RFC3339Nano) != "2026-01-02T03:04:05.123Z" || change.Epoch == nil || *change.Epoch != 3 || change.ExtID != "pocketd-source:"+id {
		t.Fatalf("original offline version was replaced: %+v", change)
	}
	due, err := store.ClaimDueSources(ctx, scope, time.Now().Add(time.Minute), 10)
	if err != nil || len(due) != 1 || due[0].ID != id || due[0].FetchInterval != 45*time.Minute || !due[0].Enabled {
		t.Fatalf("created source is unavailable to the scheduler: %+v %v", due, err)
	}
	// Use the real fetcher, parser and PostgreSQL store with a deterministic feed response.
	fetcher := rss.NewFetcher(store, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://example.com/feed" {
			t.Fatalf("fetch used a different source: %s", r.URL)
		}
		const body = `<rss version="2.0"><channel><title>Test feed</title><link>https://example.com</link><description>Local fixture</description><item><guid>first-item</guid><title>First article</title><link>https://example.com/article</link></item></channel></rss>`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})})
	if fetched, err := fetcher.RefreshSource(ctx, due[0], nil); err != nil || fetched.NewItems != 1 {
		t.Fatalf("scheduled source could not fetch: %+v %v", fetched, err)
	}
	if items, err := store.ListItems(ctx, scope, rss.ListItemsOptions{SourceID: id}); err != nil || len(items) != 1 || items[0].Title != "First article" {
		t.Fatalf("first fetch did not persist the article: %+v %v", items, err)
	}
	if err := store.DeleteSource(ctx, id, scope); err != nil {
		t.Fatal(err)
	}
	if code, body := doJSON(t, server, http.MethodPost, path, reader, initial); code != http.StatusOK || body["source_id"] != id {
		t.Fatalf("receipt after delete: %d %+v", code, body)
	}
	if sources, err := store.ListSources(ctx, scope); err != nil || len(sources) != 0 {
		t.Fatalf("old request recreated a deleted source: %+v %v", sources, err)
	}
	checkpoint, err := store.ListPortableChanges(ctx, scope, 0, 10)
	if err != nil {
		t.Fatal(err)
	}

	// A failed journal insert must leave neither a receipt nor a native source.
	disabled := `{"operationId":"offline-disabled","clientUpdatedAt":"2026-01-02T03:04:11.123Z","url":"https://example.com/disabled","title":"Injected rollback","enabled":false,"portalEpoch":null}`
	if _, err := pool.Exec(ctx, `ALTER TABLE rss_portable_changes ADD CONSTRAINT rss_test_queued_rollback CHECK (title <> 'Injected rollback')`); err != nil {
		t.Fatal(err)
	}
	if code, body := doJSON(t, server, http.MethodPost, path, reader, disabled); code != http.StatusInternalServerError {
		t.Fatalf("journal failure settled the request: %d %+v", code, body)
	}
	var staleReceipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM rss_source_create_receipts WHERE op_id='offline-disabled'`).Scan(&staleReceipts); err != nil || staleReceipts != 0 {
		t.Fatalf("receipt survived rollback: %d %v", staleReceipts, err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE rss_portable_changes DROP CONSTRAINT rss_test_queued_rollback`); err != nil {
		t.Fatal(err)
	}
	code, result := doJSON(t, server, http.MethodPost, path, reader, disabled)
	if code != http.StatusOK {
		t.Fatalf("rolled-back request did not retry: %d %+v", code, result)
	}
	saved, err := store.GetSource(ctx, result["source_id"].(string), scope)
	if err != nil || saved.Enabled || saved.Status != rss.SourceDisabled || saved.FetchInterval != 30*time.Minute {
		t.Fatalf("disabled subscription settings: %+v %v", saved, err)
	}
	later, err := store.ListPortableChanges(ctx, scope, checkpoint.NextCursor, 10)
	if err != nil || len(later.Changes) != 1 || later.Changes[0].Epoch != nil {
		t.Fatalf("unknown original epoch was reassigned: %+v %v", later, err)
	}
}
