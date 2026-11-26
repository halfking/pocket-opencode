package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/halfking/pocket-opencode/backend/internal/rss"
)

func TestRSSLegacyArticleMigrationRequiresProvenance(t *testing.T) {
	server, pool, fallbackToken, cleanup := newRSSRoutePG(t)
	defer cleanup()
	ctx := context.Background()
	owners, err := identity.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	server.identityStore = owners
	for _, workspace := range []string{"archive-workspace", "other-workspace"} {
		if err := owners.CreateDefaultWorkspace(ctx, &identity.Workspace{ID: workspace, OwnerID: "owner", Name: workspace}); err != nil {
			t.Fatal(err)
		}
		if err := owners.AddMember(ctx, &identity.Member{WorkspaceID: workspace, UserID: "reader", Role: identity.RoleInvitee}); err != nil {
			t.Fatal(err)
		}
	}
	token := func(user, workspace string) string {
		value, err := server.jwtSigner.SignWithWorkspace(user, "member", workspace)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	reader := token("reader", "archive-workspace")
	scope := rss.Scope{UserID: "reader", WorkspaceID: "archive-workspace"}
	store, err := rss.NewStore(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateSource(ctx, rss.CreateSourceRequest{URL: "https://example.com/legacy", Title: "Legacy"}, scope)
	if err != nil {
		t.Fatal(err)
	}
	const revision = "2026-01-02T03:04:05.123456Z"
	updated, err := time.Parse(time.RFC3339Nano, revision)
	if err != nil {
		t.Fatal(err)
	}
	article := rss.Item{ID: "legacy", SourceID: source.ID, URL: "https://example.com/article", Title: "Original title",
		Summary: "Original summary", Content: "native article body", PublishedAt: &updated, UpdatedAt: &updated, Status: rss.ItemUnread}
	write := func(item rss.Item) {
		item.Hash = item.ID
		if _, err := store.UpsertItemScoped(ctx, item, scope); err != nil {
			t.Fatal(err)
		}
	}
	write(article)
	undated := article
	undated.ID, undated.UpdatedAt = "undated", nil
	write(undated)
	private := article
	private.ID, private.URL = "private-link", "https://example.com/private?token=example"
	write(private)
	managed, err := store.CreateSource(ctx, rss.CreateSourceRequest{URL: "https://example.com/managed", PortalSync: true}, scope)
	if err != nil {
		t.Fatal(err)
	}
	shared := article
	shared.ID, shared.SourceID = "shared", managed.ID
	write(shared)

	// Legacy stars change the native status without changing the article's version.
	for _, id := range []string{"legacy", "undated"} {
		if code, body := doJSON(t, server, http.MethodPost, "/api/rss/items/"+id+"/star", reader, `{"starred":true}`); code != http.StatusOK || body["status"] != "starred" {
			t.Fatalf("legacy status write for %s: %d %+v", id, code, body)
		}
	}
	const detailPath = "/api/rss/items/legacy"
	const path = detailPath + "/migration-snapshot"
	if code, body := doJSON(t, server, http.MethodGet, detailPath, reader, ""); code != http.StatusOK || body["legacySnapshotAvailable"] != true || body["content"] != article.Content {
		t.Fatalf("detail advertised migration without preserving original: %d %+v", code, body)
	}
	if code, body := doJSON(t, server, http.MethodGet, path, reader, ""); code != http.StatusOK || len(body) != 8 ||
		body["id"] != article.ID || body["source_id"] != source.ID || body["user_id"] != scope.UserID ||
		body["workspace_id"] != scope.WorkspaceID || body["updated_at"] != revision ||
		body["title"] != article.Title || body["summary"] != article.Summary || body["url"] != article.URL {
		t.Fatalf("snapshot allowlist and original version: %d %+v", code, body)
	}
	for _, tc := range []struct {
		method, path, token string
		code                int
	}{
		{http.MethodPost, path, reader, http.StatusMethodNotAllowed},
		{http.MethodGet, path, fallbackToken, http.StatusUnauthorized},
		{http.MethodGet, path, token("owner", scope.WorkspaceID), http.StatusNotFound},
		{http.MethodGet, path, token("reader", "other-workspace"), http.StatusNotFound},
		{http.MethodGet, "/api/rss/items/undated/migration-snapshot", reader, http.StatusUnprocessableEntity},
		{http.MethodGet, "/api/rss/items/private-link/migration-snapshot", reader, http.StatusUnprocessableEntity},
		{http.MethodGet, "/api/rss/items/shared/migration-snapshot", reader, http.StatusConflict},
	} {
		if code, body := doJSON(t, server, tc.method, tc.path, tc.token, ""); code != tc.code {
			t.Fatalf("%s %s: got %d, want %d: %+v", tc.method, tc.path, code, tc.code, body)
		}
	}
	if code, body := doJSON(t, server, http.MethodGet, "/api/rss/items/undated", reader, ""); code != http.StatusOK || body["legacySnapshotAvailable"] != nil || body["status"] != "starred" {
		t.Fatalf("undated article migrated or lost native state: %d %+v", code, body)
	}
	if err := owners.RemoveMember(ctx, scope.WorkspaceID, scope.UserID); err != nil {
		t.Fatal(err)
	}
	if code, body := doJSON(t, server, http.MethodGet, path, reader, ""); code != http.StatusForbidden {
		t.Fatalf("revoked workspace membership accepted: %d %+v", code, body)
	}
}
