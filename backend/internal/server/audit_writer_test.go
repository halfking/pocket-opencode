package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

func TestRedactDetail_StripsSensitiveValues(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		secret string // 必须不出现的子串
	}{
		{"json_password", `{"username":"u","password":"hunter2"}`, "hunter2"},
		{"json_api_key", `{"api_key":"sk-live-abcdef"}`, "sk-live-abcdef"},
		{"json_access_token", `{"access_token":"ya29.xxxxx"}`, "ya29.xxxxx"},
		{"json_authorization", `{"Authorization":"Bearer abc.def.ghi"}`, "abc.def.ghi"},
		{"json_smtp_password", `{"smtp_password":"mailsecret"}`, "mailsecret"},
		{"query_password", `user=alice&password=p4ssw0rd&action=login`, "p4ssw0rd"},
		{"uppercase_password", `Password=hunter2`, "hunter2"},
		{"key_keeps_other_text", `user=alice&token=abc&note=hello world`, "abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := redactDetail(c.input)
			if strings.Contains(got, c.secret) {
				t.Fatalf("redactDetail leaked %q in %q", c.secret, got)
			}
			if !strings.Contains(got, auditRedactedValue) {
				t.Fatalf("redactDetail missing placeholder in %q", got)
			}
		})
	}
}

func TestRedactDetail_PreservesBenignContent(t *testing.T) {
	// 与 server_audit_export_test.go:142 中的 CSV detail 同义字符串。
	input := `he said "ok", then left`
	if got := redactDetail(input); got != input {
		t.Fatalf("benign detail must be preserved, got %q", got)
	}
	if got := redactDetail(""); got != "" {
		t.Fatalf("empty detail must remain empty, got %q", got)
	}
}

// 截断由 writeAuditEntry 在写入前完成；测试这一点必须走 Write。
func TestWrite_TruncatesLongDetail(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	r := withTestClaims(httptest.NewRequest("GET", "/", nil), "u1", "admin", "ws-a")
	big := strings.Repeat("a", maxAuditDetailBytes+200)
	srv.Write(r, "test.event", "res:1", AuditFields{Detail: big})
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if len(entries[0].Detail) > maxAuditDetailBytes {
		t.Fatalf("truncated detail exceeds limit: %d", len(entries[0].Detail))
	}
	if !strings.HasSuffix(entries[0].Detail, auditDetailTruncatedTail) {
		t.Fatalf("truncated detail must end with %q, got tail %q",
			auditDetailTruncatedTail, entries[0].Detail[max(0, len(entries[0].Detail)-32):])
	}
}

func TestWrite_NilStoreSafe(t *testing.T) {
	srv := &Server{} // auditStore == nil
	srv.Write(httptest.NewRequest("GET", "/", nil), "test.event", "res:1",
		AuditFields{Detail: "password=hunter2"})
	srv.WriteWithClaims(nil, "test.event", "res:1", AuditFields{Detail: "secret"})
	// 不 panic 即通过。
}

func TestWrite_DerivesClaimsAndIP(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	r := httptest.NewRequest("POST", "/api/x", nil)
	r.Header.Set("X-Forwarded-For", "10.1.2.3, 10.0.0.1")
	r = withTestClaims(r, "u1", "admin", "ws-a")
	srv.Write(r, "test.event", "res:1",
		AuditFields{Detail: `{"password":"hunter2"}`, Success: true})

	entries, err := srv.auditStore.Query(redclaw.AuditQuery{TenantID: "ws-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Action != "test.event" || e.Resource != "res:1" {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if e.UserID != "u1" || e.TenantID != "ws-a" {
		t.Fatalf("unexpected identity: user=%q tenant=%q", e.UserID, e.TenantID)
	}
	if e.IP != "10.1.2.3" {
		t.Fatalf("expected IP from XFF, got %q", e.IP)
	}
	if !e.Success {
		t.Fatal("Success flag must be true")
	}
	if strings.Contains(e.Detail, "hunter2") {
		t.Fatalf("detail must be redacted, got %q", e.Detail)
	}
	if e.Timestamp.IsZero() {
		t.Fatal("Timestamp must be populated by helper")
	}
}

func TestWriteWithClaims_OverridesTenantForSystem(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	srv.WriteWithClaims(&authClaims{UserID: "u1", WorkspaceID: "ws-a"},
		"tasksync.sync", "acc_task:42",
		AuditFields{Detail: "count=3", TenantID: AuditSystemTenant()})

	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{TenantID: AuditSystemTenant()})
	if len(entries) != 1 || entries[0].TenantID != "system:acc" {
		t.Fatalf("system tenant override failed: %+v", entries)
	}
	// ws-a 不应出现这条事件。
	if got, _ := srv.auditStore.Query(redclaw.AuditQuery{TenantID: "ws-a"}); len(got) != 0 {
		t.Fatalf("system event must not leak into ws-a: %+v", got)
	}
}

func TestWrite_SkipsEmptyAction(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	r := withTestClaims(httptest.NewRequest("GET", "/", nil), "u1", "member", "ws-a")
	srv.Write(r, "", "res:1", AuditFields{Detail: "x"})
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{})
	if len(entries) != 0 {
		t.Fatalf("empty action must be skipped, got %+v", entries)
	}
}

func TestWrite_DurationPropagates(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	r := withTestClaims(httptest.NewRequest("GET", "/", nil), "u1", "admin", "ws-a")
	srv.Write(r, "test.event", "res:1", AuditFields{Duration: 250 * time.Millisecond})
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{})
	if len(entries) != 1 || entries[0].DurationMs != 250 {
		t.Fatalf("DurationMs not propagated: %+v", entries[0])
	}
}

func TestWrite_NoContextClaimsLeavesIdentityEmpty(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	r := httptest.NewRequest("GET", "/", nil) // 未注入 claims
	srv.Write(r, "test.event", "res:1", AuditFields{TenantID: "system:noc"})
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{TenantID: "system:noc"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].UserID != "" || entries[0].TenantID != "system:noc" {
		t.Fatalf("expected empty user/system tenant, got %+v", entries[0])
	}
}

// 防止 Write 在 nil request 上 panic；属于 nil-store 防御之外的第二道
// 护栏（helper 也显式拒绝 nil request）。
func TestWrite_NilRequestNoPanic(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	srv.Write(nil, "test.event", "res:1", AuditFields{Detail: "x"})
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{})
	if len(entries) != 0 {
		t.Fatalf("nil request must not produce an entry, got %d", len(entries))
	}
}

// 验证 Success 字段为 false 时也能正确写入。
func TestWrite_SuccessFalsePreserved(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	r := withTestClaims(httptest.NewRequest("GET", "/", nil), "u1", "admin", "ws-a")
	srv.Write(r, "test.failed", "res:1", AuditFields{Success: false, Detail: "boom"})
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{})
	if len(entries) != 1 || entries[0].Success {
		t.Fatalf("Success=false must be preserved: %+v", entries[0])
	}
}

// 通过 context 间接注入的 claims 同样应被识别（与 requireAuth 行为一致）。
func TestWrite_ClaimsFromContext(t *testing.T) {
	srv := &Server{auditStore: redclaw.NewAuditStore()}
	ctx := context.WithValue(context.Background(), authClaimsContextKey{},
		&authClaims{UserID: "ctx-user", Role: "member", WorkspaceID: "ws-ctx"})
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	srv.Write(r, "test.event", "res:1", AuditFields{})
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{TenantID: "ws-ctx"})
	if len(entries) != 1 || entries[0].UserID != "ctx-user" {
		t.Fatalf("context claims not picked up: %+v", entries)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
