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
	if canonicalGatewayURL("https://llm.kxpms.cn/v1") != "https://llm.kxpms.cn/v1" {
		t.Fatal("kaixuan URL must stay unchanged")
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
	if cfg.BaseURL != "https://llm.kxpms.cn/v1" {
		t.Fatalf("must use settings URL, got %q", cfg.BaseURL)
	}
	if cfg.APIKey != "sk-from-settings" {
		t.Fatalf("must use settings key, got %q", cfg.APIKey)
	}
	if len(cfg.PreferredModels) != 1 || cfg.PreferredModels[0] != "glm-5.2" {
		t.Fatalf("must use settings preferred models, got %v", cfg.PreferredModels)
	}
}
