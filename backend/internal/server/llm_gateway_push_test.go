package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

// capturedPush 记录上游实例收到的请求，供断言。
type capturedPush struct {
	mu      sync.Mutex
	method  string
	path    string
	auth    string
	body    map[string]interface{}
	visible bool
}

func (c *capturedPush) observe(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.method = r.Method
	c.path = r.URL.Path
	c.auth = r.Header.Get("Authorization")
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &c.body)
	c.visible = true
}

func newPushTestServer(t *testing.T, reg *registry.Registry) *Server {
	t.Helper()
	srv, _ := newTestServerWithAuth(t)
	srv.registry = reg
	// pushConfigToOpenCode 在 s.opencode == nil 时直接短路，给它一个真实
	// 适配器实例以覆盖完整推送路径。
	srv.opencode = adapter.NewOpenCodeHTTPAdapter(1000)
	return srv
}

// 推送必须落在 stock OpenCode 的官方契约端点 PATCH /global/config 上，
// 且只合并 provider 子文档、携带实例 Bearer token。
func TestPushConfigUsesGlobalConfigContract(t *testing.T) {
	t.Setenv("POCKET_OPENCODE_CONFIG_TOKEN", "inst-token")
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")
	captured := &capturedPush{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.observe(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"$schema":"https://opencode.ai/config.json"}`)
	}))
	defer upstream.Close()

	reg := registry.NewRegistry()
	if err := reg.RegisterRegisteredInstance(model.RegisteredInstanceInfo{
		ID:          "inst-a",
		WorkspaceID: "ws-a",
		APIBaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	srv := newPushTestServer(t, reg)

	req := httptest.NewRequest(http.MethodPost, "/api/llm-gateway/config", nil)
	err := srv.pushConfigToOpenCode(req, "ws-a", llmGatewayState{
		BaseURL: "https://gateway.example.com/v1",
		APIKey:  "sk-tenant-a",
		Models:  []string{"glm-5.2", "kimi-k3"},
	})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}
	captured.mu.Lock()
	defer captured.mu.Unlock()
	if !captured.visible {
		t.Fatal("upstream received no request")
	}
	if captured.method != http.MethodPatch || captured.path != "/global/config" {
		t.Fatalf("expected PATCH /global/config, got %s %s", captured.method, captured.path)
	}
	if captured.auth != "Bearer inst-token" {
		t.Fatalf("expected instance bearer token, got %q", captured.auth)
	}
	provider, ok := captured.body["provider"].(map[string]interface{})
	if !ok {
		t.Fatalf("patch body missing provider doc: %v", captured.body)
	}
	entry, ok := provider["openai-compatible-pocket"].(map[string]interface{})
	if !ok {
		t.Fatalf("provider doc missing openai-compatible-pocket: %v", provider)
	}
	opts, _ := entry["options"].(map[string]interface{})
	if opts["baseURL"] != "https://gateway.example.com/v1" || opts["apiKey"] != "sk-tenant-a" {
		t.Fatalf("provider options not merged: %v", opts)
	}
	models, _ := entry["models"].(map[string]interface{})
	if len(models) != 2 || models["glm-5.2"] == nil || models["kimi-k3"] == nil {
		t.Fatalf("models not merged: %v", models)
	}
}

// stock opencode 的 SPA 兜底会对未知路径返回 200 text/html。旧实现只看状态码
// 会把这种响应当成功（假成功）；加固后必须显式报错（HANDOFF §4.1.1 结论 2）。
func TestPushConfigRejectsSPAFallbackFakeSuccess(t *testing.T) {
	t.Setenv("POCKET_OPENCODE_CONFIG_TOKEN", "inst-token")
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html><body>opencode</body></html>")
	}))
	defer upstream.Close()

	reg := registry.NewRegistry()
	if err := reg.RegisterRegisteredInstance(model.RegisteredInstanceInfo{
		ID:          "inst-a",
		WorkspaceID: "ws-a",
		APIBaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	srv := newPushTestServer(t, reg)

	req := httptest.NewRequest(http.MethodPost, "/api/llm-gateway/config", nil)
	err := srv.pushConfigToOpenCode(req, "ws-a", llmGatewayState{
		BaseURL: "https://gateway.example.com/v1",
		APIKey:  "sk-tenant-a",
	})
	if err == nil {
		t.Fatal("200 text/html (SPA fallback) must not count as push success")
	}
	if !strings.Contains(err.Error(), "application/json") {
		t.Fatalf("error should mention the JSON contract requirement, got: %v", err)
	}
}

// 非 2xx（如实例开启鉴权后 token 不对）必须把状态码带回错误信息。
func TestPushConfigSurfacesUpstreamStatus(t *testing.T) {
	t.Setenv("POCKET_OPENCODE_CONFIG_TOKEN", "wrong-token")
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer upstream.Close()

	reg := registry.NewRegistry()
	if err := reg.RegisterRegisteredInstance(model.RegisteredInstanceInfo{
		ID:          "inst-a",
		WorkspaceID: "ws-a",
		APIBaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	srv := newPushTestServer(t, reg)

	req := httptest.NewRequest(http.MethodPost, "/api/llm-gateway/config", nil)
	err := srv.pushConfigToOpenCode(req, "ws-a", llmGatewayState{
		BaseURL: "https://gateway.example.com/v1",
		APIKey:  "sk-tenant-a",
	})
	if err == nil {
		t.Fatal("401 must surface as push error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error should carry upstream status, got: %v", err)
	}
}
