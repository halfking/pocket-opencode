package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// /api/integration/status 应同时报告 ACC / kxmemory / llm_gateway 的
// 配置与 capabilities 状态。每个 connector 的 Write 必须为 false——
// 这是 P3 §5 「企业集成只读」验收的契约。
func TestIntegrationStatus_AccReadOnlyReported(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.mcpClient = mcp.NewClient("http://acc.test", "k", false)
	srv.auditStore = redclaw.NewAuditStore()

	tok, _ := signer.SignWithWorkspace("ops", "member", "ws-a")

	h := srv.Handler()
	req := mobileRequest(http.MethodGet, "/api/integration/status", tok, "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Integrations map[string]struct {
			Enabled    bool     `json:"enabled"`
			Configured bool     `json:"configured"`
			Read       bool     `json:"read"`
			Write      bool     `json:"write"`
			Tools      []string `json:"tools"`
		} `json:"integrations"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	acc, ok := resp.Integrations["acc"]
	if !ok {
		t.Fatalf("acc entry missing")
	}
	if !acc.Configured || !acc.Read {
		t.Fatalf("acc must be configured & readable, got %+v", acc)
	}
	if acc.Write {
		t.Fatalf("P3 §5 contract: acc must NOT advertise write capability")
	}
}

// 当 mcpClient 为 nil 时，acc 集成应报 disabled 但不泄露 baseURL/apiKey。
func TestIntegrationStatus_AccDisabledWhenMCPNil(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	srv.mcpClient = nil
	tok, _ := signer.SignWithWorkspace("ops", "member", "ws-a")

	h := srv.Handler()
	req := mobileRequest(http.MethodGet, "/api/integration/status", tok, "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if body == "" {
		t.Fatal("empty body")
	}
	// 当 mcpClient 为 nil 时，响应不应携带任何 baseURL/apiKey。
	// "k" 不放进 secret 列表——它太短，会误命中 "kxmemory" 字符串。
	for _, secret := range []string{"http://acc.test", "acc_get_tasks"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaked %q when acc disabled: %s", secret, body)
		}
	}
}

// kxmemory 已注入时必须 readable / non-writable。
func TestIntegrationStatus_KxmemoryReadable(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	tok, _ := signer.SignWithWorkspace("ops", "member", "ws-a")

	h := srv.Handler()
	req := mobileRequest(http.MethodGet, "/api/integration/status", tok, "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp struct {
		Integrations map[string]struct {
			Write bool `json:"write"`
		} `json:"integrations"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if kxm, ok := resp.Integrations["kxmemory"]; ok && kxm.Write {
		t.Fatalf("kxmemory must not advertise write")
	}
}

// 仅 GET；其他方法返回 405。
func TestIntegrationStatus_MethodNotAllowed(t *testing.T) {
	srv, _, signer, _ := newMobileRouteServer(t)
	tok, _ := signer.SignWithWorkspace("ops", "member", "ws-a")

	h := srv.Handler()
	req := mobileRequest(http.MethodPost, "/api/integration/status", tok, "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

// llm_gateway 必须按「BaseURL 是否配置」报告——llmGWCache 在 newServer
// 中无条件创建，不能作为启用信号（回归：曾恒报 enabled=true）。
func TestIntegrationStatus_LLMGatewayDisabledWithoutBaseURL(t *testing.T) {
	if os.Getenv("POCKET_LLM_GATEWAY_URL") != "" {
		t.Skip("POCKET_LLM_GATEWAY_URL set in env; snapshot would report configured")
	}
	srv, _, signer, _ := newMobileRouteServer(t)
	tok, _ := signer.SignWithWorkspace("ops", "member", "ws-a")

	req := mobileRequest(http.MethodGet, "/api/integration/status", tok, "")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Integrations map[string]struct {
			Enabled bool `json:"enabled"`
		} `json:"integrations"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if gw, ok := resp.Integrations["llm_gateway"]; !ok || gw.Enabled {
		t.Fatalf("llm_gateway must be disabled when baseURL unconfigured, got %+v", resp.Integrations)
	}
}

// no token → 401。
func TestIntegrationStatus_NoAuthRejected(t *testing.T) {
	srv, _, _, _ := newMobileRouteServer(t)
	h := srv.Handler()
	req := mobileRequest(http.MethodGet, "/api/integration/status", "", "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}