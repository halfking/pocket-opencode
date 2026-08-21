package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

func TestPGAuditHTTPExportPaginationAndTenantIsolation(t *testing.T) {
	store, cleanup := newTestPGAuditStore(t)
	t.Cleanup(cleanup)
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = store

	token, err := signer.SignWithWorkspace("audit-admin", "admin", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	for i := 0; i < 3; i++ {
		if err := store.Record(&redclaw.AuditEntry{
			Action: "pg.http.export", UserID: "user-a", TenantID: "ws-a",
			Resource: "resource-a", Success: true,
			Timestamp: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Record(&redclaw.AuditEntry{
		Action: "pg.http.export", UserID: "user-b", TenantID: "ws-b",
		Resource: "resource-b", Success: true, Timestamp: base,
	}); err != nil {
		t.Fatal(err)
	}

	start := url.QueryEscape(base.Format(time.RFC3339))
	end := url.QueryEscape(base.Add(5 * time.Second).Format(time.RFC3339))
	request := func(cursor string) *httptest.ResponseRecorder {
		target := "/api/audit/export?format=jsonl&limit=2&start=" + start + "&end=" + end
		if cursor != "" {
			target += "&cursor=" + url.QueryEscape(cursor)
		}
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}

	first := request("")
	if first.Code != http.StatusOK {
		t.Fatalf("first export status=%d body=%s", first.Code, first.Body.String())
	}
	if got := first.Header().Get("Content-Type"); !strings.Contains(got, "application/x-ndjson") {
		t.Fatalf("unexpected content type %q", got)
	}
	if got := first.Header().Get("Content-Disposition"); got != "attachment; filename=audit-export.jsonl" {
		t.Fatalf("unexpected content disposition %q", got)
	}
	firstEntries := decodeAuditJSONL(t, first.Body.String())
	if len(firstEntries) != 2 {
		t.Fatalf("first page entries=%d, want 2", len(firstEntries))
	}
	for _, entry := range firstEntries {
		if entry.TenantID != "ws-a" {
			t.Fatalf("first page leaked tenant %q", entry.TenantID)
		}
	}
	cursor := first.Header().Get("X-Audit-Next-Cursor")
	if cursor == "" {
		t.Fatal("first page missing next cursor")
	}

	second := request(cursor)
	if second.Code != http.StatusOK {
		t.Fatalf("second export status=%d body=%s", second.Code, second.Body.String())
	}
	secondEntries := decodeAuditJSONL(t, second.Body.String())
	if len(secondEntries) != 1 {
		t.Fatalf("second page entries=%d, want 1", len(secondEntries))
	}
	if second.Header().Get("X-Audit-Next-Cursor") != "" {
		t.Fatal("final page unexpectedly returned a cursor")
	}
	seen := map[string]bool{}
	for _, entry := range append(firstEntries, secondEntries...) {
		if entry.TenantID != "ws-a" {
			t.Fatalf("export leaked tenant %q", entry.TenantID)
		}
		if seen[entry.ID] {
			t.Fatalf("export repeated entry %q", entry.ID)
		}
		seen[entry.ID] = true
	}

	persisted, err := store.Query(redclaw.AuditQuery{TenantID: "ws-a", Action: "audit.export"})
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 2 {
		t.Fatalf("expected one self-audit per export request, got %d", len(persisted))
	}
}

func TestPGAuditHTTPLogsReadsPGStore(t *testing.T) {
	store, cleanup := newTestPGAuditStore(t)
	t.Cleanup(cleanup)
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = store
	token, err := signer.SignWithWorkspace("audit-admin", "admin", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(&redclaw.AuditEntry{
		Action: "pg.http.logs", UserID: "user-a", TenantID: "ws-a", Success: true,
		Timestamp: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/audit/logs?format=json", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("logs status=%d body=%s", rr.Code, rr.Body.String())
	}
	var response struct {
		Entries []*redclaw.AuditEntry `json:"entries"`
		Total   int                   `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Entries) != 1 || response.Entries[0].TenantID != "ws-a" {
		t.Fatalf("unexpected logs response: %+v", response)
	}
}

func decodeAuditJSONL(t *testing.T, body string) []*redclaw.AuditEntry {
	t.Helper()
	var entries []*redclaw.AuditEntry
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if line == "" {
			continue
		}
		var entry redclaw.AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode JSONL line %q: %v", line, err)
		}
		entries = append(entries, &entry)
	}
	return entries
}
