package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// /api/integration/status 应同时报告 ACC / kxmemory / llm_gateway 的
// 配置与 capabilities 状态。
//
// T1.2 双向 MCP 之后 ACC connector 的契约变了：pocketd 会调用 ACC 已注册的写
// tool（acc_create_task / acc_task_claim / acc_task_complete /
// acc_report_session），因此 acc.write=true 且 tools 必须列出这些 tool。
// 这与「/api/tasks POST source=acc 仍被 fail-closed 拒绝」不冲突——后者是
// pocketd HTTP 面的策略（见 integration_acc_post_test.go）。
func TestIntegrationStatus_AccBidirectionalReported(t *testing.T) {
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
	if !acc.Write {
		t.Fatalf("T1.2 contract: acc must advertise write capability, got %+v", acc)
	}
	for _, want := range []string{mcp.ToolGetTasks, mcp.ToolCreateTask, mcp.ToolTaskClaim, mcp.ToolTaskComplete, mcp.ToolReportSession} {
		if !strings.Contains(strings.Join(acc.Tools, ","), want) {
			t.Fatalf("acc tools must include %s, got %v", want, acc.Tools)
		}
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

// llm_gateway 的启用信号仍是「凭据是否齐全」，但 2026-09-30 起内置默认
// 网关（地址 + key）本身就可用，所以无 env 的全新实例必须报 enabled=true
// 且标出 source=builtin-default。回归护栏（老版本曾恒报 enabled=true）在
// 这里换了形式：现在恒报 enabled 反而是 bug——只有真没凭据时才该是 false。
func TestIntegrationStatus_LLMGatewayReportsBuiltInDefault(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_URL", "")
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "")
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
			Enabled      bool   `json:"enabled"`
			Configured   bool   `json:"configured"`
			Capabilities string `json:"capabilities"`
		} `json:"integrations"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	gw, ok := resp.Integrations["llm_gateway"]
	if !ok {
		t.Fatalf("llm_gateway entry missing: %+v", resp.Integrations)
	}
	if !gw.Enabled || !gw.Configured {
		t.Fatalf("built-in default gateway must count as usable, got %+v", gw)
	}
	if !strings.Contains(gw.Capabilities, "source: builtin-default") {
		t.Fatalf("capabilities must name the config source, got %q", gw.Capabilities)
	}
	// 端点绝不回显地址/key 本身（内部地址泄露防护）。
	if strings.Contains(rr.Body.String(), "llm.kxpms.cn") || strings.Contains(rr.Body.String(), "sk-") {
		t.Fatalf("status must not echo gateway URL or key: %s", rr.Body.String())
	}
}

// env 显式配置时来源标 env，仍不得回显地址。
func TestIntegrationStatus_LLMGatewayReportsEnvSource(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_URL", "https://llm.kxpms.cn/v1")
	srv, _, signer, _ := newMobileRouteServer(t)
	tok, _ := signer.SignWithWorkspace("ops", "member", "ws-a")

	req := mobileRequest(http.MethodGet, "/api/integration/status", tok, "")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	var resp struct {
		Integrations map[string]struct {
			Enabled      bool   `json:"enabled"`
			Capabilities string `json:"capabilities"`
		} `json:"integrations"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	gw := resp.Integrations["llm_gateway"]
	if !gw.Enabled || !strings.Contains(gw.Capabilities, "source: env") {
		t.Fatalf("env-configured gateway must report enabled + source=env, got %+v", gw)
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
