package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

func TestMeetingMinutesHistoryWithResourceCredential(t *testing.T) {
	s, pool := authorityDelegationPGWithPool(t)
	ctx := context.Background()
	var err error
	s.meetingStore, err = meeting.NewPGStore(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	s.jwtSigner, err = auth.NewSigner("meeting-history-fixture-signing-key", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("POCKET_SYNC_SYSTEM_DEFAULT", "cloud")
	for _, ws := range []string{"ws", "other"} {
		if err := s.identityStore.CreateDefaultWorkspace(ctx, &identity.Workspace{ID: ws, Name: ws, OwnerID: "owner"}); err != nil {
			t.Fatal(err)
		}
		for _, user := range []string{"alice", "bob"} {
			if err := s.identityStore.AddMember(ctx, &identity.Member{WorkspaceID: ws, UserID: user, Role: identity.RoleInvitee}); err != nil {
				t.Fatal(err)
			}
		}
	}
	stamp := time.Date(2025, 6, 15, 15, 6, 40, 123456000, time.UTC)
	makeMeeting := func(user, workspace, title string) string {
		t.Helper()
		m, err := s.meetingStore.CreateScoped(meeting.CreateMeetingRequest{Title: title, Status: "done",
			Participants: []string{"Attendee"}, Summary: "Portable minutes",
			RefinedTranscript: "PRIVATE_TRANSCRIPT", NoteID: "PRIVATE_NOTE"}, user, workspace)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(ctx, `UPDATE meetings SET created_at=$1, updated_at=$1, recording_url='PRIVATE_RECORDING' WHERE id=$2`, stamp, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	expected := make(map[string]bool)
	for i := 0; i < 21; i++ {
		expected[makeMeeting("alice", "ws", fmt.Sprintf("Archived %d", i))] = true
	}
	makeMeeting("bob", "ws", "NOT_ALICE")
	makeMeeting("alice", "other", "OTHER_WORKSPACE")
	var policyLocal atomic.Bool
	parent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := &resourceDelegation{}
		_, err := jwt.ParseWithClaims(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), claims, func(*jwt.Token) (any, error) {
			return auth.ResourceKey([]byte(authorityDelegationTestSecret), "openpocket-resource-session:v1"), nil
		}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("pocket.resource"), jwt.WithAudience("redclaw-resource-session"))
		if r.URL.Path != resourceSessionPath || err != nil || len(claims.Scopes) != 1 || claims.Scopes[0] != auth.MeetingMinutesScope {
			t.Error("incorrect meeting resource introspection")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if policyLocal.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "user_id": claims.Subject, "tenant_id": claims.TenantID,
			"workspace_id": claims.WorkspaceID, "parent_session_id": claims.SessionID, "nonce": claims.ID})
	}))
	defer parent.Close()
	t.Setenv("POCKET_RESOURCE_SESSION_AUTHORITY_URL", parent.URL)
	resourceToken := func(user, ws, scope string) string {
		t.Helper()
		now := time.Now()
		raw, _, err := s.jwtSigner.SignResource(auth.ResourceClaims{
			TenantID: "tenant-a", WorkspaceID: ws, ParentSessionID: "fixture-session", ParentExpiresAt: now.Add(time.Minute).Unix(),
			Scopes: []string{scope}, RegisteredClaims: jwt.RegisteredClaims{Subject: user},
		}, now.Add(30*time.Second), now)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	bob := resourceToken("bob", "ws", auth.MeetingMinutesScope)
	other := resourceToken("alice", "other", auth.MeetingMinutesScope)
	h := s.Handler()
	request := func(method, path, raw string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+raw)
		r.Header.Set("X-Workspace-ID", "other")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	const route = "/api/meeting-minutes/history"
	exchange := request("POST", "/internal/resource-authority/workspaces/ws/token",
		resourceExchangeTestTokenForScope(t, "alice", "ws", auth.MeetingMinutesScope, time.Now()))
	var issued struct {
		Token string `json:"token"`
	}
	if exchange.Code != http.StatusOK || json.Unmarshal(exchange.Body.Bytes(), &issued) != nil || issued.Token == "" {
		t.Fatalf("meeting resource exchange failed: %d", exchange.Code)
	}
	alice := issued.Token
	type minutesPage struct {
		User      string                   `json:"user_id"`
		Workspace string                   `json:"workspace_id"`
		Rows      []meetingMinutesSnapshot `json:"meetings"`
		Next      *int                     `json:"next_offset"`
	}
	read := func(raw, suffix string) minutesPage {
		t.Helper()
		w := request("GET", route+suffix, raw)
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("history %d %s", w.Code, w.Body)
		}
		for _, marker := range []string{"PRIVATE_", "transcript", "recording_url", "note_id"} {
			if strings.Contains(w.Body.String(), marker) {
				t.Fatalf("history exported a private field: %s", marker)
			}
		}
		var page minutesPage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	first := read(alice, "?workspace_id=other")
	second := read(alice, "?offset=20")
	if first.User != "alice" || first.Workspace != "ws" || len(first.Rows) != 20 || first.Next == nil || *first.Next != 20 ||
		len(second.Rows) != 1 || second.Next != nil {
		t.Fatal("invalid authenticated scope or pagination")
	}
	for _, row := range append(first.Rows, second.Rows...) {
		if !expected[row.ID] || row.Minutes != "Portable minutes" || !row.UpdatedAt.Equal(stamp) || !row.CreatedAt.Equal(stamp) {
			t.Fatalf("unexpected scope, version, or title: %+v", row)
		}
		delete(expected, row.ID)
	}
	if row := read(bob, "").Rows; len(row) != 1 || row[0].Title != "NOT_ALICE" {
		t.Fatal("bob did not get an independent result")
	}
	if row := read(other, "").Rows; len(row) != 1 || row[0].Title != "OTHER_WORKSPACE" {
		t.Fatal("other workspace did not get an independent result")
	}
	for _, denied := range [][2]string{{"GET", "/api/meetings"}, {"POST", route}} {
		if got := request(denied[0], denied[1], alice).Code; got != http.StatusForbidden {
			t.Fatalf("resource escaped into %v: %d", denied, got)
		}
	}
	if got := request("GET", route+"/extra", alice).Code; got != http.StatusNotFound {
		t.Fatalf("unknown history route returned %d", got)
	}
	if got := request("GET", route, resourceToken("alice", "ws", "rss")).Code; got != http.StatusForbidden {
		t.Fatalf("wrong resource scope accepted: %d", got)
	}
	policyLocal.Store(true)
	if got := request("GET", route, alice).Code; got != http.StatusForbidden {
		t.Fatalf("local-only accepted: %d", got)
	}
	direct, err := s.jwtSigner.SignWithWorkspace("alice", "member", "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("POCKET_SYNC_SYSTEM_DEFAULT", "local")
	if w := request("GET", route, direct); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "module_policy_local") {
		t.Fatalf("direct login did not observe local policy: %d", w.Code)
	}
	policyLocal.Store(false)
	if err := s.identityStore.RemoveMember(ctx, "ws", "alice"); err != nil {
		t.Fatal(err)
	}
	if got := request("GET", route, alice).Code; got != http.StatusForbidden {
		t.Fatalf("revoked workspace membership accepted: %d", got)
	}
}
