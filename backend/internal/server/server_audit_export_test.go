package server

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

func newAuditExportServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv, _, signer, _ := newMobileRouteServer(t)
	adminToken, err := signer.SignWithWorkspace("admin-1", "admin", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	return srv, adminToken
}

func TestAuditExportRequiresAdmin(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	h := srv.Handler()

	memberToken, err := signer.SignWithWorkspace("user-1", "member", "ws-a")
	if err != nil {
		t.Fatal(err)
	}

	for name, token := range map[string]string{
		"no_token":      "",
		"member_token":  memberToken,
	} {
		req := mobileRequest(http.MethodGet, "/api/audit/export?format=jsonl", token, "")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
			t.Fatalf("%s: expected 401/403, got %d", name, rr.Code)
		}
	}
}

func TestAuditExportJSONLWithRangeAndPagination(t *testing.T) {
	srv, adminToken := newAuditExportServer(t)
	h := srv.Handler()

	base := time.UnixMilli(10_000_000)
	// ws-a 7 条（时间递增），ws-b 3 条（必须被租户过滤掉）。
	for i := 0; i < 7; i++ {
		_ = srv.auditStore.Record(&redclaw.AuditEntry{
			Action: "chat.send", UserID: "u1", TenantID: "ws-a",
			Resource: "res", Success: true, Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}
	for i := 0; i < 3; i++ {
		_ = srv.auditStore.Record(&redclaw.AuditEntry{
			Action: "chat.send", UserID: "u2", TenantID: "ws-b",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}

	fetch := func(query string) *httptest.ResponseRecorder {
		req := mobileRequest(http.MethodGet, "/api/audit/export"+query, adminToken, "")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	start := url.QueryEscape(base.Format(time.RFC3339))
	end := url.QueryEscape(base.Add(10 * time.Second).Format(time.RFC3339))

	// 第一页：limit=3 → 3 行 JSONL + 游标。
	rr := fetch("?format=jsonl&limit=3&start=" + start + "&end=" + end)
	if rr.Code != http.StatusOK {
		t.Fatalf("export failed: %d %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "ndjson") {
		t.Fatalf("jsonl content type expected, got %s", got)
	}
	lines := nonEmptyLines(rr.Body.String())
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %s", len(lines), rr.Body.String())
	}
	var first redclaw.AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line is not valid audit entry: %v", err)
	}
	if first.TenantID != "ws-a" {
		t.Fatalf("tenant scope violated: %+v", first)
	}
	cursor := rr.Header().Get("X-Audit-Next-Cursor")
	if cursor == "" {
		t.Fatal("partial page must expose next cursor")
	}

	// 第二页：带上游标续传，应得到剩余 4 条。
	rr2 := fetch("?format=jsonl&limit=100&start=" + start + "&end=" + end + "&cursor=" + cursor)
	if rr2.Code != http.StatusOK {
		t.Fatalf("page2 failed: %d %s", rr2.Code, rr2.Body.String())
	}
	lines2 := nonEmptyLines(rr2.Body.String())
	if len(lines2) != 4 {
		t.Fatalf("expected remaining 4 lines, got %d", len(lines2))
	}
	if rr2.Header().Get("X-Audit-Next-Cursor") != "" {
		t.Fatal("fully consumed range must not return a cursor")
	}
	// 两页合并后时间有序且无重复。
	var all []string
	all = append(all, lines...)
	all = append(all, lines2...)
	seen := map[string]bool{}
	var prev time.Time
	for _, line := range all {
		var e redclaw.AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if seen[e.ID] {
			t.Fatalf("duplicate entry across pages: %s", e.ID)
		}
		seen[e.ID] = true
		if !prev.IsZero() && e.Timestamp.Before(prev) {
			t.Fatal("pages must be time-ordered")
		}
		prev = e.Timestamp
	}
}

func TestAuditExportCSV(t *testing.T) {
	srv, adminToken := newAuditExportServer(t)
	h := srv.Handler()

	base := time.UnixMilli(20_000_000)
	_ = srv.auditStore.Record(&redclaw.AuditEntry{
		Action: "mobile.approval.permission_once", UserID: "u1", TenantID: "ws-a",
		Resource: "instance:i1/session:s1/request:r1", Detail: `he said "ok", then left`,
		Success: true, DurationMs: 42, IP: "10.0.0.1", Timestamp: base,
	})

	req := mobileRequest(http.MethodGet, "/api/audit/export?format=csv", adminToken, "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("csv export failed: %d %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "text/csv") {
		t.Fatalf("csv content type expected, got %s", got)
	}

	records, err := csv.NewReader(rr.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected header + 1 row, got %d", len(records))
	}
	if records[0][0] != "id" || records[0][3] != "user_id" {
		t.Fatalf("unexpected header: %v", records[0])
	}
	row := records[1]
	if row[2] != "mobile.approval.permission_once" || row[4] != "ws-a" {
		t.Fatalf("unexpected row: %v", row)
	}
	if row[9] != `he said "ok", then left` {
		t.Fatalf("csv must roundtrip quoted detail with commas/quotes, got %q", row[9])
	}
}

func TestAuditExportValidation(t *testing.T) {
	srv, adminToken := newAuditExportServer(t)
	h := srv.Handler()

	cases := []string{
		"?format=xml",
		"?start=not-a-date",
		"?end=not-a-date",
		"?start=2030-01-01T00:00:00Z&end=2020-01-01T00:00:00Z",
		"?limit=0",
		"?limit=1001",
		"?limit=abc",
	}
	for _, q := range cases {
		req := mobileRequest(http.MethodGet, "/api/audit/export"+q, adminToken, "")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", q, rr.Code)
		}
	}
}

func TestAuditExportIncrementalCursorAcrossNewEntries(t *testing.T) {
	srv, adminToken := newAuditExportServer(t)
	h := srv.Handler()
	base := time.UnixMilli(30_000_000)

	fetch := func(q string) *httptest.ResponseRecorder {
		req := mobileRequest(http.MethodGet, "/api/audit/export"+q, adminToken, "")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// 第一批：2 条。
	for i := 0; i < 2; i++ {
		_ = srv.auditStore.Record(&redclaw.AuditEntry{
			Action: "chat.send", UserID: "u1", TenantID: "ws-a",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}
	rr1 := fetch("?format=jsonl&limit=1")
	if len(nonEmptyLines(rr1.Body.String())) != 1 {
		t.Fatalf("expected 1 line, body=%s", rr1.Body.String())
	}
	cursor := rr1.Header().Get("X-Audit-Next-Cursor")
	if cursor == "" {
		t.Fatal("expected cursor")
	}

	// 期间产生新条目，再用游标增量拉取：应得到第 2 条 + 新条目，不含第 1 条。
	_ = srv.auditStore.Record(&redclaw.AuditEntry{
		Action: "file.read", UserID: "u1", TenantID: "ws-a",
		Timestamp: base.Add(10 * time.Second),
	})
	rr2 := fetch("?format=jsonl&limit=100&cursor=" + cursor)
	actions := map[string]int{}
	for _, line := range nonEmptyLines(rr2.Body.String()) {
		var e redclaw.AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		actions[e.Action]++
	}
	if actions["chat.send"] != 1 || actions["file.read"] != 1 {
		t.Fatalf("incremental cursor must return only new/unexported entries, got %v", actions)
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

