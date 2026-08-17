package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/quota"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// /api/llm/quota 在 Enforcer 未配置时应返 503（或 401，看 requireAuth 顺序）。
func TestLLMQuotaHandler_NoEnforcerReturns503(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	h := srv.Handler()
	// 取一个有效 token 但 server 上无 quotaEnforcer。
	_, _, signer, _ := newMobileRouteServer(t)
	tok, _ := signer.SignWithWorkspace("q-u", "member", "ws-a")
	_ = srv

	req := mobileRequest(http.MethodGet, "/api/llm/quota", tok, "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when enforcer is nil, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// /api/llm/quota 应返回 budgets + strategy + enforce_mode。
func TestLLMQuotaHandler_ReturnsBudgetsAndStrategy(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.quotaEnforcer = quota.NewEnforcer(quota.NewMemoryStore(), nil)
	if err := srv.quotaEnforcer.Store().Set(context.Background(), quota.Budget{
		WorkspaceID: "ws-a",
		Kind:        "cost_usd",
		Limit:       50,
		PeriodStart: time.Now().Add(-time.Hour),
		PeriodEnd:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	tok, _ := signer.SignWithWorkspace("quota-user", "member", "ws-a")
	h := srv.Handler()
	req := mobileRequest(http.MethodGet, "/api/llm/quota", tok, "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		WorkspaceID string         `json:"workspace_id"`
		Budgets     []quota.Budget `json:"budgets"`
		Strategy    string         `json:"strategy"`
		EnforceMode bool           `json:"enforce_mode"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.WorkspaceID != "ws-a" {
		t.Fatalf("expected ws-a, got %q", resp.WorkspaceID)
	}
	if len(resp.Budgets) != 1 || resp.Budgets[0].Kind != "cost_usd" {
		t.Fatalf("expected 1 cost_usd budget, got %+v", resp.Budgets)
	}
	if resp.Strategy != "always_allow" {
		t.Fatalf("expected always_allow strategy, got %q", resp.Strategy)
	}
	if resp.EnforceMode {
		t.Fatalf("expected enforce_mode=false in skeleton stage")
	}
}

// pre-flight 必须写一条 llm.quota.checked 事件（无论 EnforceMode）。
func TestLLMQuotaPreFlight_WritesAuditChecked(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.quotaEnforcer = quota.NewEnforcer(quota.NewMemoryStore(), nil)

	// checkQuotaOrAudit 走 s.Write → claimsFromContext；直接注入。
	r := httptest.NewRequest(http.MethodPost, "/api/llm/stream", nil)
	r = withTestClaims(r, "quota-user", "member", "ws-a")
	srv.checkQuotaOrAudit(r, "llm.stream", "chat", "gpt-4o-mini", 100)

	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "llm.quota.checked"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 llm.quota.checked, got %d", len(entries))
	}
	e := entries[0]
	if e.TenantID != "ws-a" {
		t.Fatalf("tenant must be ws-a, got %q", e.TenantID)
	}
	if e.Resource != "llm:llm.stream" {
		t.Fatalf("unexpected resource: %q", e.Resource)
	}
	if !e.Success {
		t.Fatalf("checked event must be success=true")
	}
}

// 在 EnforceMode=false 前提下，Allow=true 的策略不会触发 quota.denied。
func TestLLMQuotaPreFlight_AlwaysAllow_NoDeniedEvent(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.quotaEnforcer = quota.NewEnforcer(quota.NewMemoryStore(), nil)

	r := httptest.NewRequest(http.MethodPost, "/api/llm/stream", nil)
	r = withTestClaims(r, "quota-user", "member", "ws-a")
	srv.checkQuotaOrAudit(r, "llm.stream", "chat", "gpt-4o-mini", 0)

	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "llm.quota.denied"})
	if len(entries) != 0 {
		t.Fatalf("denied event must not fire in always_allow mode, got %+v", entries)
	}
}
