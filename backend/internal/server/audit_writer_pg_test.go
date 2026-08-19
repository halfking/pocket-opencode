package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
	"github.com/halfking/pocket-opencode/backend/internal/tasksync"
)

// These cases intentionally mirror the in-memory audit writer tests while
// reading through PGAuditStore.Query. They verify that server writers persist
// correctly against PostgreSQL, without making PostgreSQL mandatory for unit
// test runs.
func TestPGAuditWriterFlows(t *testing.T) {
	store, cleanup := newTestPGAuditStore(t)
	t.Cleanup(cleanup)

	t.Run("write derives claims and redacts detail", func(t *testing.T) {
		srv := &Server{auditStore: store}
		r := httptest.NewRequest(http.MethodPost, "/api/x", nil)
		r.Header.Set("X-Forwarded-For", "10.1.2.3, 10.0.0.1")
		r = withTestClaims(r, "u1", "admin", "ws-write")
		srv.Write(r, "pg.write", "res:1", AuditFields{
			Detail:   `{"password":"hunter2"}`,
			Success:  true,
			Duration: 250 * time.Millisecond,
		})

		e := requireSinglePGAuditEntry(t, store, redclaw.AuditQuery{Action: "pg.write"})
		if e.UserID != "u1" || e.TenantID != "ws-write" || e.IP != "10.1.2.3" {
			t.Fatalf("identity or IP was not persisted: %+v", e)
		}
		if e.Resource != "res:1" || !e.Success || e.DurationMs != 250 {
			t.Fatalf("write fields were not persisted: %+v", e)
		}
		if strings.Contains(e.Detail, "hunter2") || !strings.Contains(e.Detail, auditRedactedValue) {
			t.Fatalf("redacted detail was not persisted: %q", e.Detail)
		}
	})

	t.Run("email bridge persists propagated identity", func(t *testing.T) {
		srv := &Server{auditStore: store}
		NewEmailAuditWriter(srv).Write("email-user", "ws-email", "email.oauth.refreshed", "email_account:acc-1",
			email.AuditFields{Success: true, Detail: "provider=google"})

		e := requireSinglePGAuditEntry(t, store, redclaw.AuditQuery{Action: "email.oauth.refreshed"})
		if e.UserID != "email-user" || e.TenantID != "ws-email" || e.Resource != "email_account:acc-1" || !e.Success {
			t.Fatalf("email audit bridge fields were not persisted: %+v", e)
		}
	})

	t.Run("tasksync bridge enforces system tenant", func(t *testing.T) {
		srv := &Server{auditStore: store}
		NewTasksyncAuditWriter(srv).Write("", "ws-incorrect", "tasksync.sync", "acc_task:batch",
			tasksync.AuditFields{Success: true, Detail: "parsed=3 saved=3"})

		e := requireSinglePGAuditEntry(t, store, redclaw.AuditQuery{Action: "tasksync.sync"})
		if e.TenantID != AuditSystemTenant() || !e.Success || !strings.Contains(e.Detail, "parsed=3") {
			t.Fatalf("tasksync system audit was not persisted: %+v", e)
		}
		if entries := requirePGAuditEntries(t, store, redclaw.AuditQuery{TenantID: "ws-incorrect", Action: "tasksync.sync"}); len(entries) != 0 {
			t.Fatalf("tasksync audit leaked into caller tenant: %+v", entries)
		}
	})

	t.Run("vault upload does not leak blob", func(t *testing.T) {
		srv, _, token := newVaultAuditServer(t)
		srv.auditStore = store
		const secretBlob = "BEGIN_VAULT_BLOB access_token=xyzzy-blob-payload END_VAULT_BLOB"
		req := mobileRequest(http.MethodPost, "/api/vault/sync/", token, `{"blob":"`+secretBlob+`","version":1}`)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("upload failed: %d %s", rr.Code, rr.Body.String())
		}

		e := requireSinglePGAuditEntry(t, store, redclaw.AuditQuery{Action: "vault.sync.upload"})
		if e.TenantID != "ws-a" || e.Resource != "vault:vault-user" || !strings.Contains(e.Detail, "version=1") {
			t.Fatalf("vault audit fields were not persisted: %+v", e)
		}
		if strings.Contains(e.Detail, "BEGIN_VAULT_BLOB") || strings.Contains(e.Detail, "xyzzy-blob-payload") {
			t.Fatalf("vault audit detail leaked blob: %q", e.Detail)
		}
	})

	t.Run("model call does not persist prompt content", func(t *testing.T) {
		srv, _, signer, _ := newMobileRouteServer(t)
		srv.auditStore = store
		srv.llm = &fakeLLMClient{}
		token, err := signer.SignWithWorkspace("llm-user", "member", "ws-model")
		if err != nil {
			t.Fatal(err)
		}
		req := mobileRequest(http.MethodPost, "/api/llm/chat", token,
			`{"messages":[{"role":"user","content":"SECRET-PROMPT-DO-NOT-LOG"}],"model":"gpt-4o-mini"}`)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("chat failed: %d %s", rr.Code, rr.Body.String())
		}

		e := requireSinglePGAuditEntry(t, store, redclaw.AuditQuery{Action: "llm.chat"})
		if e.TenantID != "ws-model" || !e.Success || !strings.Contains(e.Detail, "messages=1") {
			t.Fatalf("model audit fields were not persisted: %+v", e)
		}
		if strings.Contains(e.Detail, "SECRET-PROMPT-DO-NOT-LOG") {
			t.Fatalf("model audit detail leaked prompt: %q", e.Detail)
		}
	})

	t.Run("ACC task rejection is persisted", func(t *testing.T) {
		srv, _, signer, _ := newMobileRouteServer(t)
		srv.auditStore = store
		token, err := signer.SignWithWorkspace("operator", "member", "ws-acc")
		if err != nil {
			t.Fatal(err)
		}
		req := mobileRequest(http.MethodPost, "/api/tasks", token, `{"title":"acc-test","source":"acc","status":"active"}`)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d body=%s", rr.Code, rr.Body.String())
		}

		e := requireSinglePGAuditEntry(t, store, redclaw.AuditQuery{Action: "task.post.rejected"})
		if e.TenantID != "ws-acc" || e.Success || e.Detail == "" {
			t.Fatalf("ACC rejection audit was not persisted: %+v", e)
		}
	})
}

func TestPGAuditWriterTruncatesLongDetail(t *testing.T) {
	store, cleanup := newTestPGAuditStore(t)
	t.Cleanup(cleanup)
	srv := &Server{auditStore: store}
	r := withTestClaims(httptest.NewRequest(http.MethodGet, "/", nil), "u1", "admin", "ws-truncate")
	srv.Write(r, "pg.truncate", "res:1", AuditFields{Detail: strings.Repeat("a", maxAuditDetailBytes+200)})

	e := requireSinglePGAuditEntry(t, store, redclaw.AuditQuery{Action: "pg.truncate"})
	if len(e.Detail) > maxAuditDetailBytes || !strings.HasSuffix(e.Detail, auditDetailTruncatedTail) {
		t.Fatalf("detail truncation was not persisted: length=%d detail=%q", len(e.Detail), e.Detail)
	}
}

func TestPGAuditWriterWritesSystemTenantWithoutClaims(t *testing.T) {
	store, cleanup := newTestPGAuditStore(t)
	t.Cleanup(cleanup)
	srv := &Server{auditStore: store}
	srv.WriteWithClaims(nil, "pg.system", "res:1", AuditFields{TenantID: AuditSystemTenant(), Success: true})

	e := requireSinglePGAuditEntry(t, store, redclaw.AuditQuery{TenantID: AuditSystemTenant(), Action: "pg.system"})
	if e.UserID != "" || e.TenantID != AuditSystemTenant() || !e.Success {
		t.Fatalf("system audit was not persisted: %+v", e)
	}
}
