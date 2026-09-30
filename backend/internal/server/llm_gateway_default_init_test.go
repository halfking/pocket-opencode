package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/opencode"
)

// 用户 2026-09-30 指定的默认设置页配置：自家网关地址 + 内置 key + 9 个常用模型。
// 实测依据：2026-09-30 GET https://llm.kxpms.cn/v1/models → 200 / 603 个模型，
// 下面 9 个 id 全部命中。
var wantDefaultPreferredModels = []string{
	"glm-5.2",
	"minimax-m3",
	"kimi-k3",
	"claude-sonnet-5",
	"gpt-5.6-terra",
	"claude-opus-5",
	"claude-fable-5",
	"gpt-5.6-sol",
	"gemini-3.5-flash",
}

// 不配任何 env 时，defaultLLMGatewayState 必须已经是可用的完整配置。
// 回归护栏：此前 APIKey 只读 env，未注入的实例设置页显示"未设置"、
// 保存被 400 挡回、对话 503——全新装完开箱即用的目标落空。
func TestDefaultLLMGatewayStateNeedsNoEnv(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_URL", "")
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "")

	st := defaultLLMGatewayState()
	if st.BaseURL != "https://llm.kxpms.cn/v1" {
		t.Fatalf("default baseURL must be the self-hosted gateway, got %q", st.BaseURL)
	}
	if st.APIKey != opencode.DefaultLLMGatewayAPIKey {
		t.Fatalf("default api key must be the built-in one, got %q", st.APIKey)
	}
	if st.APIKey == "" {
		t.Fatal("default state must carry a usable API key")
	}
	if st.Format != defaultGatewayFormat {
		t.Fatalf("default format = %q, want %q", st.Format, defaultGatewayFormat)
	}
	if !slices.Equal(st.PreferredModels, wantDefaultPreferredModels) {
		t.Fatalf("preferred models = %v, want %v", st.PreferredModels, wantDefaultPreferredModels)
	}
	if !slices.Equal(st.Models, wantDefaultPreferredModels) {
		t.Fatalf("seed models = %v, want %v", st.Models, wantDefaultPreferredModels)
	}
}

// env 仍然优先于内置默认：换租户/换网关只改 env，不改代码。
func TestDefaultLLMGatewayStateEnvOverridesBuiltinDefaults(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_URL", "https://other-gw.example.com/v1")
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "sk-from-env")

	st := defaultLLMGatewayState()
	if st.BaseURL != "https://other-gw.example.com/v1" {
		t.Fatalf("env URL must win, got %q", st.BaseURL)
	}
	if st.APIKey != "sk-from-env" {
		t.Fatalf("env key must win, got %q", st.APIKey)
	}
}

// 设置页 GET 出口：首次访问（无任何已存配置）就要返回默认地址 + apiKeySet=true
// + 9 个默认常用模型，前端照此渲染，不需要用户先手填一遍。
func TestGatewayConfigGETReturnsInitializedDefaults(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_URL", "")
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "")

	srv, _ := newTestServerWithAuth(t)
	srv.llmGWCache = newLLMGatewayCache()
	srv.userSettings = nil // 无用户级覆盖层 = 全新实例的读路径

	req := httptest.NewRequest(http.MethodGet, "/api/llm-gateway/config", nil).
		WithContext(context.WithValue(context.Background(), authClaimsContextKey{},
			&authClaims{UserID: "user-admin", Role: "admin", WorkspaceID: "ws_user-admin"}))
	rec := httptest.NewRecorder()
	srv.handleLLMGatewayConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		BaseURL         string   `json:"baseURL"`
		APIKeySet       bool     `json:"apiKeySet"`
		Models          []string `json:"models"`
		PreferredModels []string `json:"preferredModels"`
		Format          string   `json:"format"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.BaseURL != "https://llm.kxpms.cn/v1" {
		t.Fatalf("GET baseURL = %q", body.BaseURL)
	}
	if !body.APIKeySet {
		t.Fatal("apiKeySet must be true on a fresh instance (built-in default key)")
	}
	if body.Format != "openai-chat" {
		t.Fatalf("GET format = %q", body.Format)
	}
	if !slices.Equal(body.PreferredModels, wantDefaultPreferredModels) {
		t.Fatalf("GET preferredModels = %v, want %v", body.PreferredModels, wantDefaultPreferredModels)
	}
	if body.Models == nil {
		t.Fatal("models must serialize as [] not null (settings page reads .length)")
	}
}
