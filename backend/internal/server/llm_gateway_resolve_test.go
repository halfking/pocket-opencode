package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		"baseURL":         "https://llm.kxpms.cn/v1",
		"format":          "openai-chat",
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

// 2026-09-30 真机回归：设置页保存网关地址后读回仍是旧地址。
//
// 成因是两套存储：POST /api/llm-gateway/config 只写工作区快照
// （llmGWStore.SaveConfig），而 effectiveGatewayState 读的时候还会用
// user setting（seedAdminGatewaySetting 写下的 llm_gateway 行）覆盖 baseURL。
// 一旦那条 user setting 是旧域名，设置页保存永远不生效，对话也照旧打旧网关。
// 修复：保存时用 syncGatewayUserSetting 把用户级设置一并同步。
// 本测试锁住「保存后 ResolveGatewayForUser 必须返回新地址」。
func TestSyncGatewayUserSettingMakesSaveTakeEffect(t *testing.T) {
	srv, _ := newTestServerWithAuth(t)
	srv.llmGWCache = newLLMGatewayCache()
	srv.userSettings = usersetting.NewMemStore()

	// 模拟历史遗留：user setting 指向旧域名，覆盖层比工作区快照优先
	stale, _ := json.Marshal(map[string]any{"baseURL": "https://llmgo.kxpms.cn/v1"})
	if _, err := srv.userSettings.Put(usersetting.Record{
		UserID: "user-admin", WorkspaceID: "ws_user-admin",
		Namespace: "llm_gateway", ID: "default",
		Payload: stale, Secret: "sk-old", UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seed stale setting: %v", err)
	}
	srv.llmGWCache.replace("ws_user-admin", llmGatewayState{
		BaseURL: "https://llmgo.kxpms.cn/v1", APIKey: "sk-old", Format: defaultGatewayFormat,
	})

	const want = "https://llm.kxpms.cn/v1"
	// 带上 claims 上下文，userIDFromRequest 才能取到 user-admin
	req := httptest.NewRequest(http.MethodPost, "/api/llm-gateway/config", nil).
		WithContext(context.WithValue(context.Background(), authClaimsContextKey{},
			&authClaims{UserID: "user-admin", Role: "admin", WorkspaceID: "ws_user-admin"}))
	srv.syncGatewayUserSetting(
		req, "ws_user-admin",
		llmGatewayState{BaseURL: want, APIKey: "sk-new", Format: defaultGatewayFormat},
	)

	rec, err := srv.userSettings.Get("user-admin", "ws_user-admin", "llm_gateway", "default")
	if err != nil || rec == nil {
		t.Fatalf("user setting missing after sync: %v", err)
	}
	var got struct {
		BaseURL string `json:"baseURL"`
	}
	if err := json.Unmarshal(rec.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.BaseURL != want {
		t.Fatalf("user setting still shadows the saved URL: got %q want %q", got.BaseURL, want)
	}
	if rec.Secret != "sk-new" {
		t.Fatalf("secret must be synced too, got %q", rec.Secret)
	}
	// 覆盖层更新后，整体解析结果也必须跟着变成新地址
	if cfg := srv.ResolveGatewayForUser("user-admin", "ws_user-admin"); cfg.BaseURL != want {
		t.Fatalf("ResolveGatewayForUser must return the saved URL, got %q", cfg.BaseURL)
	}

	// 同秒连写不能被静默丢弃：usersetting 的 DecidePut 按 unix 秒比较，
	// 时间戳相同会判 DecisionKeep。直接用 time.Now().Unix() 时，"保存设置页"
	// 紧接着被覆盖层 PUT 就会写不进去，症状仍是「保存成功但读回旧值」。
	const second = "https://llmgo.kxpms.cn/v1"
	srv.syncGatewayUserSetting(req, "ws_user-admin",
		llmGatewayState{BaseURL: second, APIKey: "sk-x", Format: defaultGatewayFormat})
	if cfg := srv.ResolveGatewayForUser("user-admin", "ws_user-admin"); cfg.BaseURL != second {
		t.Fatalf("same-second consecutive save must still take effect, got %q", cfg.BaseURL)
	}
}
