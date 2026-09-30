package server

import (
	"encoding/json"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/opencode"
	"github.com/halfking/pocket-opencode/backend/internal/usersetting"
)

func TestCanonicalGatewayURLRewritesObsoleteLocal(t *testing.T) {
	t.Parallel()
	got := canonicalGatewayURL("http://llm-gateway-local-8782:8782/v1")
	if got != opencode.DefaultLLMGatewayBaseURL {
		t.Fatalf("obsolete local URL must become kaixuan default, got %q", got)
	}
	// 2026-09-30: llm.kxpms.cn 是当前正式默认网关，必须原样保留。
	// 这条断言是回归护栏——2026-09-21 曾把 llm.kxpms.cn 列为 obsolete 强制改写到
	// llmgo，导致设置页填 llm.kxpms.cn 会被悄悄打回，配置形同虚设。
	if got := canonicalGatewayURL("https://llm.kxpms.cn/v1"); got != "https://llm.kxpms.cn/v1" {
		t.Fatalf("llm.kxpms.cn is the current default and must stay unchanged, got %q", got)
	}
	if canonicalGatewayURL("https://llmgo.kxpms.cn/v1") != "https://llmgo.kxpms.cn/v1" {
		t.Fatal("llmgo URL must stay unchanged")
	}
	// 空值回落默认
	if canonicalGatewayURL("   ") != opencode.DefaultLLMGatewayBaseURL {
		t.Fatal("blank URL must fall back to default")
	}
}

func TestDefaultLLMGatewayStateIgnoresObsoleteEnv(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_URL", "http://llm-gateway-local-8782:8782")
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "sk-from-env")
	st := defaultLLMGatewayState()
	if st.BaseURL != opencode.DefaultLLMGatewayBaseURL {
		t.Fatalf("env 8782 must not be served, got %q", st.BaseURL)
	}
	if st.APIKey != "sk-from-env" {
		t.Fatalf("api key should come from env, got %q", st.APIKey)
	}
}

func TestResolveGatewayRewritesObsoleteCache(t *testing.T) {
	srv, _ := newTestServerWithAuth(t)
	srv.llmGWCache = newLLMGatewayCache()
	srv.llmGWCache.replace("ws_user-admin", llmGatewayState{
		BaseURL: "http://llm-gateway-local-8782:8782/v1",
		APIKey:  "sk-cached",
		Format:  defaultGatewayFormat,
	})
	cfg := srv.ResolveGateway("ws_user-admin")
	if cfg.BaseURL != opencode.DefaultLLMGatewayBaseURL {
		t.Fatalf("conversation must not use 8782, got %q", cfg.BaseURL)
	}
	if cfg.APIKey != "sk-cached" {
		t.Fatalf("rewritten URL must keep cached key, got %q", cfg.APIKey)
	}
}

func TestResolveGatewayForUserPrefersSettings(t *testing.T) {
	srv, _ := newTestServerWithAuth(t)
	srv.llmGWCache = newLLMGatewayCache()
	srv.llmGWCache.replace("ws_user-admin", llmGatewayState{
		BaseURL: "http://llm-gateway-local-8782:8782",
		APIKey:  "sk-stale",
		Format:  defaultGatewayFormat,
	})
	store := usersetting.NewMemStore()
	payload, _ := json.Marshal(map[string]any{
		"baseURL": "https://llm.kxpms.cn/v1",
		"format":  "openai-chat",
		"preferredModels": []string{"glm-5.2"},
	})
	if _, err := store.Put(usersetting.Record{
		UserID: "user-admin", WorkspaceID: "ws_user-admin",
		Namespace: "llm_gateway", ID: "default",
		Payload: payload, Secret: "sk-from-settings", UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("put setting: %v", err)
	}
	srv.userSettings = store

	cfg := srv.ResolveGatewayForUser("user-admin", "ws_user-admin")
	// 2026-09-30: llm.kxpms.cn 不再被改写，设置页填什么就用什么。
	if cfg.BaseURL != "https://llm.kxpms.cn/v1" {
		t.Fatalf("must use settings URL verbatim, got %q", cfg.BaseURL)
	}
	if cfg.APIKey != "sk-from-settings" {
		t.Fatalf("must use settings key, got %q", cfg.APIKey)
	}
	if len(cfg.PreferredModels) != 1 || cfg.PreferredModels[0] != "glm-5.2" {
		t.Fatalf("must use settings preferred models, got %v", cfg.PreferredModels)
	}
}
