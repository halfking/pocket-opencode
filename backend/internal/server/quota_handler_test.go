package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/quota"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// denyStrategy 永远返 Deny，用于 EnforceMode=true 的硬拒绝路径测试。
type denyStrategy struct{}

func (denyStrategy) Decide(_ context.Context, _ []quota.Budget, _ quota.DecisionInput) (quota.Decision, error) {
	return quota.Decision{
		Allow:  false,
		Reason: "cost_usd budget exceeded",
	}, nil
}

// unreachableProvider 让 handleLLMBFFStream 中 quota check 之后的 Stream 调用
// 走到即报错——前提是 quota 拦截没起作用。在 e2e 拦截测试里我们期望 Stream
// 永远不被调用，因此该错误必须**不**出现。
type unreachableProvider struct{}

func (unreachableProvider) Chat(context.Context, llmbff.ChatRequest) (*llmbff.ChatResponse, error) {
	return nil, errors.New("unreachableProvider: chat must not be called in quota denial tests")
}
func (unreachableProvider) Stream(context.Context, llmbff.ChatRequest, func(llmbff.Delta) bool) (*llmbff.Usage, error) {
	return nil, errors.New("unreachableProvider: stream must not be called when quota intercepts first")
}
func (unreachableProvider) Embed(context.Context, llmbff.EmbedRequest) (*llmbff.EmbedResponse, error) {
	return nil, errors.New("unreachableProvider: embed must not be called")
}

// storeErrStrategy 触发 strategy 错误路径（fail-open 验证）。
type storeErrStrategy struct{}

func (storeErrStrategy) Decide(_ context.Context, _ []quota.Budget, _ quota.DecisionInput) (quota.Decision, error) {
	return quota.Decision{}, errFakeStore
}

// errFakeStore 让 Decide 报错的策略错误；用导出的 sentinel 避免类型
// 私有字段被外部 test 复用。
var errFakeStore = quotaStrategyErr("fake store error")

type quotaStrategyErr string

func (e quotaStrategyErr) Error() string { return string(e) }

// /api/llm/quota 在 Enforcer 未配置时应返 503。
func TestLLMQuotaHandler_NoEnforcerReturns503(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	tok, _ := signer.SignWithWorkspace("q-u", "member", "ws-a")

	req := mobileRequest(http.MethodGet, "/api/llm/quota", tok, "")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when enforcer is nil, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// budgets 为空时必须返回 [] 而非 null——前端消费方按数组遍历。
func TestLLMQuotaHandler_EmptyBudgetsEncodeAsArray(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.quotaEnforcer = quota.NewEnforcer(quota.NewMemoryStore(), nil)

	tok, _ := signer.SignWithWorkspace("q-u", "member", "ws-a")
	req := mobileRequest(http.MethodGet, "/api/llm/quota", tok, "")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"budgets":[]`) {
		t.Fatalf("budgets must encode as [] when empty, got %s", rr.Body.String())
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

// EnforceMode=true 且策略 Deny：checkQuotaOrAudit 必须返 true 并写
// llm.quota.denied 审计。这是「硬拒绝路径」的预飞契约。
func TestLLMQuotaPreFlight_EnforceModeTrue_DenyReturnsBlocked(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.quotaEnforcer = quota.NewEnforcer(quota.NewMemoryStore(), denyStrategy{})
	srv.quotaEnforcer.SetEnforceMode(true)

	r := httptest.NewRequest(http.MethodPost, "/api/llm/stream", nil)
	r = withTestClaims(r, "quota-user", "member", "ws-a")
	if !srv.checkQuotaOrAudit(r, "llm.stream", "chat", "gpt-4o-mini", 100) {
		t.Fatal("EnforceMode=true + Deny must return blocked=true")
	}
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "llm.quota.denied"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 llm.quota.denied, got %d", len(entries))
	}
	e := entries[0]
	if e.Success {
		t.Fatal("denied audit must have success=false")
	}
	if !strings.Contains(e.Detail, "reason=cost_usd budget exceeded") {
		t.Fatalf("denied detail must carry reason, got %q", e.Detail)
	}
	if !strings.Contains(e.Detail, "enforce_mode=true") {
		t.Fatalf("denied detail must record enforce_mode, got %q", e.Detail)
	}
}

// EnforceMode=false 但策略 Deny：仅审计，不阻断（与历史行为一致）。
func TestLLMQuotaPreFlight_EnforceModeFalse_DenyAuditsButAllows(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.quotaEnforcer = quota.NewEnforcer(quota.NewMemoryStore(), denyStrategy{})
	// EnforceMode 默认 false，无需显式设置。

	r := httptest.NewRequest(http.MethodPost, "/api/llm/stream", nil)
	r = withTestClaims(r, "quota-user", "member", "ws-a")
	if srv.checkQuotaOrAudit(r, "llm.stream", "chat", "gpt-4o-mini", 0) {
		t.Fatal("EnforceMode=false must return blocked=false even when strategy denies")
	}
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "llm.quota.denied"})
	if len(entries) != 1 {
		t.Fatalf("audit must still record the deny in audit-only mode, got %d", len(entries))
	}
}

// EnforceMode=true 但策略 Allow：返回 false、写 checked，不写 denied。
func TestLLMQuotaPreFlight_EnforceModeTrue_AllowPasses(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.quotaEnforcer = quota.NewEnforcer(quota.NewMemoryStore(), nil) // AlwaysAllow
	srv.quotaEnforcer.SetEnforceMode(true)

	r := httptest.NewRequest(http.MethodPost, "/api/llm/stream", nil)
	r = withTestClaims(r, "quota-user", "member", "ws-a")
	if srv.checkQuotaOrAudit(r, "llm.stream", "chat", "gpt-4o-mini", 0) {
		t.Fatal("EnforceMode=true + Allow must return blocked=false")
	}
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "llm.quota.checked"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 llm.quota.checked, got %d", len(entries))
	}
}

// EnforceMode=true 但 strategy/Store 报 err：必须 fail-open 返 false
// 并写 llm.quota.error，避免预算子系统故障连带杀死整个 BFF。
func TestLLMQuotaPreFlight_StoreError_FailsOpen(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.quotaEnforcer = quota.NewEnforcer(quota.NewMemoryStore(), storeErrStrategy{})
	srv.quotaEnforcer.SetEnforceMode(true)

	r := httptest.NewRequest(http.MethodPost, "/api/llm/stream", nil)
	r = withTestClaims(r, "quota-user", "member", "ws-a")
	if srv.checkQuotaOrAudit(r, "llm.stream", "chat", "gpt-4o-mini", 0) {
		t.Fatal("store error must fail-open (return blocked=false)")
	}
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "llm.quota.error"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 llm.quota.error, got %d", len(entries))
	}
	if entries[0].Success {
		t.Fatal("quota error event must have success=false")
	}
}

// /api/llm/stream 端到端：EnforceMode=true + Deny 时必须返 429 + 结构化
// JSON，且 Content-Type 必须是 application/json（不是 text/event-stream）。
// 这是 SSE 拦截「必须在 header 写入前」的回归保护。
func TestLLMBFFStream_EnforceMode_DenyReturns429JSON(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.quotaEnforcer = quota.NewEnforcer(quota.NewMemoryStore(), denyStrategy{})
	srv.quotaEnforcer.SetEnforceMode(true)
	// llmBFF 注入一个 unreachable provider：若 quota 拦截失败，Stream
	// 调用会触发错误；测试期望 429 出现，即证明拦截在 Stream 之前。
	srv.SetLLMBFF(llmbff.NewService(unreachableProvider{}, llmbff.NoopRecorder{}), nil)

	tok, _ := signer.SignWithWorkspace("deny-user", "member", "ws-a")
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	req := mobileRequest(http.MethodPost, "/api/llm/stream", tok, body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("denial must be application/json (not SSE), got Content-Type=%q", ct)
	}
	var resp struct {
		Error     string `json:"error"`
		Code      string `json:"code"`
		Resource  string `json:"resource"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("denial body must be valid JSON: %v body=%s", err, rr.Body.String())
	}
	if resp.Code != "llm.quota.denied" || resp.Retryable {
		t.Fatalf("unexpected denial shape: %+v", resp)
	}
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "llm.quota.denied"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 denied audit, got %d", len(entries))
	}
}

// /api/llm/stream 端到端：EnforceMode=false + Deny 时仍必须放行（仅审计），
// 让请求到达 Stream 调用——unreachable provider 会触发 500，但绝不能是
// 429（audit-only 必须放行）。这条规则证明 EnforceMode=false 不会误伤。
func TestLLMBFFStream_AuditOnly_DenyDoesNotBlock(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.auditStore = redclaw.NewAuditStore()
	srv.quotaEnforcer = quota.NewEnforcer(quota.NewMemoryStore(), denyStrategy{})
	// EnforceMode 默认 false。
	srv.SetLLMBFF(llmbff.NewService(unreachableProvider{}, llmbff.NoopRecorder{}), nil)

	tok, _ := signer.SignWithWorkspace("audit-user", "member", "ws-a")
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	req := mobileRequest(http.MethodPost, "/api/llm/stream", tok, body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	// quota 拦截不应阻断；Stream 被调用后会因 unreachable provider 触发
	// SSE error event（非 429）。关键断言：不是 429。
	if rr.Code == http.StatusTooManyRequests {
		t.Fatalf("EnforceMode=false must not block; got 429 body=%s", rr.Body.String())
	}
	entries, _ := srv.auditStore.Query(redclaw.AuditQuery{Action: "llm.quota.denied"})
	if len(entries) != 1 {
		t.Fatalf("audit must still record deny in audit-only mode, got %d", len(entries))
	}
}
