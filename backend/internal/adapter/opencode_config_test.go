package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 全部请求必须落在官方契约端点 /global/config 上。
func TestGetModelConfigMapsGlobalConfig(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/global/config" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"provider": {
				"mockgw": {
					"npm": "@ai-sdk/openai-compatible",
					"name": "Mock Gateway",
					"options": {"baseURL": "http://127.0.0.1:8089/v1", "apiKey": "mock-key"},
					"models": {"gpt-4o": {"name": "GPT-4o (mock)"}}
				}
			},
			"model": "mockgw/gpt-4o"
		}`)
	}))
	defer upstream.Close()

	a := NewOpenCodeConfigHTTPAdapter(2000)
	cfg, err := a.GetModelConfig(context.Background(), upstream.URL)
	if err != nil {
		t.Fatalf("GetModelConfig: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.Providers))
	}
	p := cfg.Providers[0]
	if p.ID != "mockgw" || p.Name != "Mock Gateway" || p.BaseURL != "http://127.0.0.1:8089/v1" || p.APIKey != "mock-key" {
		t.Fatalf("provider mapping wrong: %+v", p)
	}
	if len(p.Models) != 1 || p.Models[0].ID != "gpt-4o" {
		t.Fatalf("model mapping wrong: %+v", p.Models)
	}
	if cfg.DefaultProvider != "mockgw" {
		t.Fatalf("default provider should derive from model key, got %q", cfg.DefaultProvider)
	}
}

// UpdateModelConfig 必须以 PATCH 合并语义提交 provider 子文档，并携带实例 token。
func TestUpdateModelConfigPatchesGlobalConfig(t *testing.T) {
	t.Setenv("POCKET_OPENCODE_CONFIG_TOKEN", "inst-token")
	var method, path, auth string
	var body map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"provider":{}}`)
	}))
	defer upstream.Close()

	a := NewOpenCodeConfigHTTPAdapter(2000)
	err := a.UpdateModelConfig(context.Background(), upstream.URL, &ModelConfig{
		Providers: []Provider{{
			ID: "openai-compatible-pocket", Name: "Pocket LLM Gateway", Enabled: true,
			APIKey: "sk-x", BaseURL: "https://gateway.example.com/v1",
			Models: []ModelDefinition{{ID: "glm-5.2", DisplayName: "glm-5.2", Enabled: true}},
		}},
	})
	if err != nil {
		t.Fatalf("UpdateModelConfig: %v", err)
	}
	if method != http.MethodPatch || path != "/global/config" {
		t.Fatalf("expected PATCH /global/config, got %s %s", method, path)
	}
	if auth != "Bearer inst-token" {
		t.Fatalf("expected bearer token, got %q", auth)
	}
	provider, _ := body["provider"].(map[string]interface{})
	entry, _ := provider["openai-compatible-pocket"].(map[string]interface{})
	if entry == nil {
		t.Fatalf("provider entry missing: %v", body)
	}
	opts, _ := entry["options"].(map[string]interface{})
	if opts["baseURL"] != "https://gateway.example.com/v1" || opts["apiKey"] != "sk-x" {
		t.Fatalf("options wrong: %v", opts)
	}
	models, _ := entry["models"].(map[string]interface{})
	if _, ok := models["glm-5.2"]; !ok {
		t.Fatalf("models wrong: %v", models)
	}
}

// ReloadConfig 以回读校验代替：上游应答 200 JSON 即视为已生效。
func TestReloadConfigVerifiesViaReadBack(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("reload should only read back, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"provider":{}}`)
	}))
	defer upstream.Close()

	a := NewOpenCodeConfigHTTPAdapter(2000)
	if err := a.ReloadConfig(context.Background(), upstream.URL); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
}

// TestModel 校验 provider/model 确实存在于实例配置中。
func TestTestModelChecksConfigPresence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"provider":{"mockgw":{"models":{"gpt-4o":{}}}}}`)
	}))
	defer upstream.Close()

	a := NewOpenCodeConfigHTTPAdapter(2000)
	ctx := context.Background()
	if err := a.TestModel(ctx, upstream.URL, "mockgw", "gpt-4o"); err != nil {
		t.Fatalf("existing model should pass: %v", err)
	}
	if err := a.TestModel(ctx, upstream.URL, "mockgw", "missing"); err == nil {
		t.Fatal("missing model should fail")
	}
	if err := a.TestModel(ctx, upstream.URL, "nope", "gpt-4o"); err == nil {
		t.Fatal("missing provider should fail")
	}
}

// SPA 兜底（200 text/html）在所有方法上都必须判为失败——这是 2026-09-07
// 复核发现的"假成功"根因（HANDOFF §4.1.1）。
func TestSPAFallbackIsRejected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html><body>opencode</body></html>")
	}))
	defer upstream.Close()

	a := NewOpenCodeConfigHTTPAdapter(2000)
	ctx := context.Background()
	if _, err := a.GetModelConfig(ctx, upstream.URL); err == nil {
		t.Fatal("GetModelConfig must reject HTML response")
	} else if !strings.Contains(err.Error(), "application/json") {
		t.Fatalf("error should mention JSON contract, got: %v", err)
	}
	if err := a.UpdateModelConfig(ctx, upstream.URL, &ModelConfig{Providers: []Provider{{ID: "p"}}}); err == nil {
		t.Fatal("UpdateModelConfig must reject HTML response")
	}
	if err := a.ReloadConfig(ctx, upstream.URL); err == nil {
		t.Fatal("ReloadConfig must reject HTML response")
	}
	if err := a.TestModel(ctx, upstream.URL, "p", "m"); err == nil {
		t.Fatal("TestModel must reject HTML response")
	}
}
