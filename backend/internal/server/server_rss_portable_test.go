package server

import (
	"context"
	"encoding/json"
	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/halfking/pocket-opencode/backend/internal/rss"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRSSPortableRouteUsesVerifiedScopeAndPreservesOriginalEvents(t *testing.T) {
	server, pool, emptyWorkspaceToken, cleanup := newRSSRoutePG(t)
	defer cleanup()
	identityStore, err := identity.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	server.identityStore = identityStore
	if err = identityStore.CreateDefaultWorkspace(context.Background(), &identity.Workspace{ID: "native-workspace", OwnerID: "workspace-owner", Name: "Native"}); err != nil {
		t.Fatal(err)
	}
	if err = identityStore.AddMember(context.Background(), &identity.Member{WorkspaceID: "native-workspace", UserID: "native-user", Role: identity.RoleInvitee}); err != nil {
		t.Fatal(err)
	}
	token, err := server.jwtSigner.SignWithWorkspace("native-user", "member", "native-workspace")
	if err != nil {
		t.Fatal(err)
	}
	code, body := doJSON(t, server, http.MethodPost, "/api/rss/sources", token, `{"url":"https://example.com/feed","title":"new","portalSync":true,"portalEpoch":0,"user_id":"attacker","workspace_id":"other"}`)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %+v", code, body)
	}
	id := body["id"].(string)
	code, body = doJSON(t, server, http.MethodGet, "/api/rss/portable/changes?limit=1", token, "")
	if code != http.StatusOK || len(body["changes"].([]any)) != 1 || body["user_id"] == "attacker" || body["workspace_id"] == "other" {
		t.Fatalf("scope: %d %+v", code, body)
	}
	scope := rss.Scope{UserID: body["user_id"].(string), WorkspaceID: body["workspace_id"].(string)}
	native, err := rss.NewStore(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.UpsertItemScoped(context.Background(), rss.Item{ID: "private", Hash: "private", SourceID: id, Title: "item", Summary: "summary", Content: "PRIVATE FULL BODY"}, scope); err != nil {
		t.Fatal(err)
	}
	code, body = doJSON(t, server, http.MethodGet, "/api/rss/portable/changes", token, "")
	bytes, _ := json.Marshal(body)
	if code != http.StatusOK || strings.Contains(string(bytes), "PRIVATE FULL BODY") {
		t.Fatalf("privacy: %d %s", code, bytes)
	}
	item := body["changes"].([]any)[1].(map[string]any)
	if item["client_updated_at"] != nil || item["sync_error"] != "source_time_unknown" {
		t.Fatalf("source time: %+v", item)
	}
	statusBody := `{"items":[{"ext_id":"pocketd-item:private","status":"starred","client_updated_at":"2026-01-01T00:00:00.123456Z","op_id":"route-state","epoch":0,"deleted":false}]}`
	for _, payload := range []string{`{"items":[]}`, `{"items":[{"ext_id":"pocketd-item:private","status":"other","client_updated_at":"2026-01-01T00:00:00Z","op_id":"invalid-state","epoch":0}]}`, strings.Replace(statusBody, `"epoch":0,`, "", 1), strings.Replace(statusBody, `"epoch":0`, `"epoch":null`, 1), statusBody + ` {}`} {
		if code, _ = doJSON(t, server, http.MethodPost, "/api/rss/portable/item-states", token, payload); code != 400 {
			t.Fatalf("bad status accepted: %d", code)
		}
	}
	if code, body = doJSON(t, server, http.MethodPost, "/api/rss/portable/item-states", token, statusBody); code != 200 {
		t.Fatalf("state route: %d %+v", code, body)
	}
	// An independent device may receive this exact version from a timestamptz
	// service using a different zone or fractional spelling. It remains one op.
	secondDeviceBody := strings.Replace(statusBody, "2026-01-01T00:00:00.123456Z", "2026-01-01T08:00:00.123456000+08:00", 1)
	if code, body = doJSON(t, server, http.MethodPost, "/api/rss/portable/item-states", token, secondDeviceBody); code != 200 || body["acks"].([]any)[0].(map[string]any)["outcome"] != "duplicate" {
		t.Fatalf("second device replay: %d %+v", code, body)
	}
	for _, changed := range []string{strings.Replace(secondDeviceBody, ".123456000+08:00", ".123456001+08:00", 1), strings.Replace(secondDeviceBody, `"starred"`, `"read"`, 1)} {
		if code, _ = doJSON(t, server, http.MethodPost, "/api/rss/portable/item-states", token, changed); code != 409 {
			t.Fatalf("second device changed version accepted: %d", code)
		}
	}
	for _, offset := range []string{"+24:00", "+00:60"} {
		invalid := strings.Replace(strings.Replace(secondDeviceBody, "+08:00", offset, 1), `"route-state"`, `"invalid-zone"`, 1)
		if code, _ = doJSON(t, server, http.MethodPost, "/api/rss/portable/item-states", token, invalid); code != 400 {
			t.Fatalf("invalid timestamp offset %s accepted: %d", offset, code)
		}
	}
	if code, body = doJSON(t, server, http.MethodGet, "/api/rss/items/private", token, ""); code != 200 || body["status"] != "starred" || body["portalManaged"] != true || body["content"] != "PRIVATE FULL BODY" {
		t.Fatalf("state result: %d %+v", code, body)
	}
	if code, _ = doJSON(t, server, http.MethodPost, "/api/rss/items/private/read", token, ""); code != 409 {
		t.Fatalf("managed legacy bypass: %d", code)
	}
	if code, _ = doJSON(t, server, http.MethodPost, "/api/rss/portable/item-states", emptyWorkspaceToken, statusBody); code != 401 {
		t.Fatalf("state fallback accepted: %d", code)
	}
	for _, query := range []string{"?cursor=-1", "?cursor=9007199254740992", "?limit=101"} {
		if code, _ = doJSON(t, server, http.MethodGet, "/api/rss/portable/changes"+query, token, ""); code != 400 {
			t.Fatalf("invalid query accepted: %s %d", query, code)
		}
	}
	if code, _ = doJSON(t, server, http.MethodPost, "/api/rss/sources", token, `{"url":"https://example.com/bad","portalSync":true,"portalEpoch":-1}`); code != 400 {
		t.Fatalf("invalid epoch=%d", code)
	}
	if code, _ = doJSON(t, server, http.MethodGet, "/api/rss/portable/changes", "", ""); code != 401 {
		t.Fatalf("unauthenticated changes=%d", code)
	}
	for _, chunked := range []bool{false, true} {
		url := "https://example.com/delete-empty"
		if chunked {
			url = "https://example.com/delete-chunked"
		}
		code, created := doJSON(t, server, http.MethodPost, "/api/rss/sources", token, `{"url":"`+url+`","portalSync":true,"portalEpoch":0}`)
		if code != 201 {
			t.Fatalf("delete fixture: %d %+v", code, created)
		}
		payload := ""
		if chunked {
			payload = `{"portableMutation":true,"portalEpoch":2}`
		}
		req := mobileRequest(http.MethodDelete, "/api/rss/sources/"+created["id"].(string), token, payload)
		if chunked {
			req.ContentLength = -1
			req.TransferEncoding = []string{"chunked"}
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("delete chunked=%v: %d %s", chunked, response.Code, response.Body.String())
		}
	}
	if code, _ = doJSON(t, server, http.MethodGet, "/api/rss/portable/changes", emptyWorkspaceToken, ""); code != 401 {
		t.Fatalf("workspace fallback accepted: %d", code)
	}
	server.identityStore = nil
	if code, _ = doJSON(t, server, http.MethodGet, "/api/rss/portable/changes", token, ""); code != 503 {
		t.Fatalf("missing membership verifier: %d", code)
	}
	if code, _ = doJSON(t, server, http.MethodPost, "/api/rss/portable/item-states", token, statusBody); code != 503 {
		t.Fatalf("state missing verifier: %d", code)
	}
	server.identityStore = identityStore
	if err = identityStore.RemoveMember(context.Background(), "native-workspace", "native-user"); err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/rss/portable/changes", ""},
		{http.MethodPost, "/api/rss/portable/item-states", statusBody},
		{http.MethodPost, "/api/rss/sources", `{"url":"https://example.com/revoked","portalSync":true,"portalEpoch":0}`},
		{http.MethodPatch, "/api/rss/sources/" + id, `{"title":"revoked edit"}`},
		{http.MethodDelete, "/api/rss/sources/" + id, ""},
		{http.MethodPost, "/api/rss/sources/" + id + "/refresh", ""},
	} {
		if code, _ = doJSON(t, server, request.method, request.path, token, request.body); code != 403 {
			t.Fatalf("revoked member %s %s=%d", request.method, request.path, code)
		}
	}
	if err = identityStore.AddMember(context.Background(), &identity.Member{WorkspaceID: "native-workspace", UserID: "native-user", Role: identity.RoleInvitee, ExpiresAt: time.Now().Add(-time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	if code, _ = doJSON(t, server, http.MethodGet, "/api/rss/portable/changes", token, ""); code != 403 {
		t.Fatalf("expired membership accepted: %d", code)
	}
}
