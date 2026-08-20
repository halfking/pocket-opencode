package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// /api/audit/logs 在默认 format=json 时输出与旧实现兼容的
// {entries,total} 形状；这里确认结构稳定，避免破坏 UI。

func TestAuditLogsRequiresAdmin(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	h := srv.Handler()

	memberToken, err := signer.SignWithWorkspace("user-1", "member", "ws-a")
	if err != nil {
		t.Fatal(err)
	}

	for name, token := range map[string]string{
		"no_token":     "",
		"member_token": memberToken,
	} {
		req := mobileRequest(http.MethodGet, "/api/audit/logs", token, "")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
			t.Fatalf("%s: expected 401/403, got %d", name, rr.Code)
		}
	}
}

// 同 user 跨 workspace：admin 在 ws-a token 必须看不到 ws-b 的事件。
// 使用 shared-user 主题，与 workspace_isolation_test.go 一致，确保
// store 的 owner 谓词掩盖不了 workspace 谓词。
func TestAuditLogsTenantIsolation_SharedUser(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	h := srv.Handler()

	adminA, err := signer.SignWithWorkspace("shared-user", "admin", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	adminB, err := signer.SignWithWorkspace("shared-user", "admin", "ws-b")
	if err != nil {
		t.Fatal(err)
	}

	base := time.UnixMilli(40_000_000)
	for i := 0; i < 5; i++ {
		_ = srv.auditStore.Record(&redclaw.AuditEntry{
			Action: "chat.send", UserID: "shared-user", TenantID: "ws-a",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}
	for i := 0; i < 4; i++ {
		_ = srv.auditStore.Record(&redclaw.AuditEntry{
			Action: "vault.sync.upload", UserID: "shared-user", TenantID: "ws-b",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, mobileRequest(http.MethodGet, "/api/audit/logs", adminA, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("admin/ws-a request failed: %d %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Entries []*redclaw.AuditEntry `json:"entries"`
		Total   int                   `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if resp.Total != 5 {
		t.Fatalf("expected 5 ws-a entries, got total=%d", resp.Total)
	}
	if len(resp.Entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(resp.Entries))
	}
	for i, e := range resp.Entries {
		if e.TenantID != "ws-a" {
			t.Fatalf("entry %d leaked ws-b: %+v", i, e)
		}
		if e.Action == "vault.sync.upload" {
			t.Fatalf("ws-b action must not appear in ws-a logs: %+v", e)
		}
	}

	// 反向：ws-b admin 不应能看到 ws-a 事件。
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, mobileRequest(http.MethodGet, "/api/audit/logs", adminB, ""))
	if rr2.Code != http.StatusOK {
		t.Fatalf("admin/ws-b request failed: %d %s", rr2.Code, rr2.Body.String())
	}
	var resp2 struct {
		Entries []*redclaw.AuditEntry `json:"entries"`
		Total   int                   `json:"total"`
	}
	_ = json.Unmarshal(rr2.Body.Bytes(), &resp2)
	if resp2.Total != 4 {
		t.Fatalf("expected 4 ws-b entries, got total=%d", resp2.Total)
	}
	for i, e := range resp2.Entries {
		if e.TenantID != "ws-b" {
			t.Fatalf("entry %d leaked ws-a into ws-b logs: %+v", i, e)
		}
	}
}

// format=jsonl 与 export 行为对齐；limit + cursor 在 logs 端点同样可用。
func TestAuditLogsPaginationAndFormatParity(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	h := srv.Handler()

	adminA, err := signer.SignWithWorkspace("admin-a", "admin", "ws-a")
	if err != nil {
		t.Fatal(err)
	}

	base := time.UnixMilli(50_000_000)
	for i := 0; i < 7; i++ {
		_ = srv.auditStore.Record(&redclaw.AuditEntry{
			Action: "chat.send", UserID: "u1", TenantID: "ws-a",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}

	// 第一页：limit=3 → 3 行 + cursor。
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, mobileRequest(http.MethodGet, "/api/audit/logs?format=jsonl&limit=3", adminA, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("page1 failed: %d %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "ndjson") {
		t.Fatalf("jsonl content type expected, got %s", got)
	}
	lines := nonEmptyLines(rr.Body.String())
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	cursor := rr.Header().Get("X-Audit-Next-Cursor")
	if cursor == "" {
		t.Fatal("partial page must expose next cursor")
	}

	// 第二页：续传得 4 行；没有 attachment 头。
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, mobileRequest(http.MethodGet, "/api/audit/logs?format=jsonl&limit=10&cursor="+cursor, adminA, ""))
	if rr2.Code != http.StatusOK {
		t.Fatalf("page2 failed: %d %s", rr2.Code, rr2.Body.String())
	}
	if got := rr2.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("logs endpoint must not set Content-Disposition, got %q", got)
	}
	lines2 := nonEmptyLines(rr2.Body.String())
	if len(lines2) != 4 {
		t.Fatalf("expected 4 lines on page 2, got %d", len(lines2))
	}
	if rr2.Header().Get("X-Audit-Next-Cursor") != "" {
		t.Fatal("fully consumed range must not return a cursor")
	}
}

// format=csv 与 export 共享同一表头。
func TestAuditLogsCSVFormatMatchesExport(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	h := srv.Handler()

	adminA, err := signer.SignWithWorkspace("admin-a", "admin", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.auditStore.Record(&redclaw.AuditEntry{
		Action: "mobile.approval.permission_once", UserID: "u1", TenantID: "ws-a",
		Resource: "instance:i1/session:s1/request:r1", Success: true,
		DurationMs: 42, IP: "10.0.0.1",
		Timestamp: time.UnixMilli(60_000_000),
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, mobileRequest(http.MethodGet, "/api/audit/logs?format=csv", adminA, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("csv request failed: %d %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "text/csv") {
		t.Fatalf("csv content type expected, got %s", got)
	}
	if got := rr.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("logs endpoint must not set Content-Disposition, got %q", got)
	}
	if !strings.HasPrefix(rr.Body.String(), "id,timestamp,action,user_id,tenant_id") {
		t.Fatalf("csv header mismatch: %q", strings.SplitN(rr.Body.String(), "\n", 2)[0])
	}
}

// 参数校验与 export 一致。
func TestAuditLogsValidation(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	h := srv.Handler()

	adminA, err := signer.SignWithWorkspace("admin-a", "admin", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
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
		req := mobileRequest(http.MethodGet, "/api/audit/logs"+q, adminA, "")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d body=%s", q, rr.Code, rr.Body.String())
		}
	}
}

// parseAuditQuery 不会因为缺少 Authorization header 而崩——这是对日志
// 端点要求的“fails closed”冗余测试。requireAuth 已经会 401 拦在前面；
// 这里走 mobileRouteServer 自带的 signer 确保能命中 handler。
func TestAuditLogsNoAuthFailsClosed(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/audit/logs", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no token must be rejected, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAuditLogsRejectsAdminWithoutWorkspace(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	h := srv.Handler()
	if err := srv.auditStore.Record(&redclaw.AuditEntry{Action: "chat.send", TenantID: "ws-a", Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := srv.auditStore.Record(&redclaw.AuditEntry{Action: "chat.send", TenantID: "ws-b", Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign("legacy-admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, mobileRequest(http.MethodGet, "/api/audit/logs", token, ""))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "workspace") {
		t.Fatalf("empty workspace must fail closed, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAuditLogsRejectsMalformedCursor(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	token, err := signer.SignWithWorkspace("admin-a", "admin", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, mobileRequest(http.MethodGet, "/api/audit/logs?cursor=not-a-cursor", token, ""))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("malformed cursor must return 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}
