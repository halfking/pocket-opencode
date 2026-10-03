package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// 用户指定的默认设置页配置：自家网关地址 + 9 个常用模型。
//
// 2026-09-30 首次落地时实测 GET https://llm.kxpms.cn/v1/models → 200 / 603 个
// 模型，9 个 id 全部命中。2026-10-02 用户把首选从 glm-5.2 改为 glm-5.3，
// 复测：GET → 200 / **606** 个模型，glm-5.3 在目录里，且
// POST /v1/chat/completions 非流式返回 content="OK"。
var wantDefaultPreferredModels = []string{
	"glm-5.3",
	"minimax-m3",
	"kimi-k3",
	"claude-sonnet-5",
	"gpt-5.6-terra",
	"claude-opus-5",
	"claude-fable-5",
	"gpt-5.6-sol",
	"gemini-3.5-flash",
}

// 不配任何 env 时，defaultLLMGatewayState 必须预置地址与模型，
// 但 **APIKey 必须为空**——不得回落到任何写在仓库里的租户密钥。
//
// 2026-10-01 契约反转。此前这里断言「必须携带内置 key」，
// 护栏的是「全新装完开箱即用」；那个护栏的实现方式是把租户密钥
// 硬编码进 `opencode.DefaultLLMGatewayAPIKey` 并删掉了仓库原有的
// 「禁止把租户密钥写进仓库」注释。代价是：
//   - 一把租户密钥进入 git 历史与所有克隆；
//   - 所有用户默认共用同一把 key，无法按租户吊销。
//
// 现在改为 env-only：**没配置就如实报未配置**，
// 由设置页引导用户填自己的 key。「开箱即用」的合理形态是
// 地址与模型预置好、key 留空并提示，而不是替用户预置一把不属于他的 key。
func TestDefaultLLMGatewayStateHasNoBuiltinKey(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_URL", "")
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "")

	st := defaultLLMGatewayState()
	if st.BaseURL != "https://llm.kxpms.cn/v1" {
		t.Fatalf("default baseURL must be the self-hosted gateway, got %q", st.BaseURL)
	}
	if st.APIKey != "" {
		t.Fatalf("default state must NOT carry a built-in API key, got %q", st.APIKey)
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

// env 仍然是 key 的唯一来源：换租户/换网关只改 env，不改代码。
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

// 设置页 GET 出口：首次访问（无任何已存配置）返回预置地址与 9 个默认常用模型，
// 但 `apiKeySet` 必须为 **false** —— 前端据此提示用户去填 key，
// 而不是显示一个实际调不通的「已配置」。
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
	if body.APIKeySet {
		t.Fatal("apiKeySet must be false on a fresh instance with no env key — " +
			"reporting a built-in key as configured is exactly the defect this guards")
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
