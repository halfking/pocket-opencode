package main

// ⚠ §692 判据：`resolveGatewayBootstrap`（Phase C 的 embed/chat 网关选择）
// 与网关代理路径（`internal/server`）对**同一个 env** 必须得出同一个结论。
//
// 缺陷原文：这段判定内联在 main() 里、读 `config.Load()` 的字段，而
// `config.getEnv` 不做 TrimSpace ⇒ `POCKET_LLM_GATEWAY_API_KEY="  "` 时
// 启动期判「已配置」（误进企业网关分支），而设置页/网关代理路径判「未配置」。
// 实测读数见 docs §692；修法是地址按空白归一、凭据复用 server 的唯一实现。

import (
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
	"github.com/halfking/pocket-opencode/backend/internal/server"
)

func TestResolveGatewayBootstrap_NormalizesWhitespaceEnv(t *testing.T) {
	cases := []struct {
		name       string
		url        string
		key        string
		wantBase   string
		wantKey    string
		enterprise bool
	}{
		{
			name: "纯空白按「未配置」处理（修复前的缺陷档）",
			url:  "  ", key: "  ",
			wantBase:   opencode.DefaultLLMGatewayBaseURL,
			wantKey:    "",
			enterprise: false,
		},
		{
			name: "首尾空格被裁掉，落到同一个真实地址/凭据",
			url:  " https://gw.example.com/v1 ", key: " sk-abc ",
			wantBase:   "https://gw.example.com/v1",
			wantKey:    "sk-abc",
			enterprise: true,
		},
		{
			name: "未设置回落默认网关，且不因默认地址就自动启用",
			url:  "", key: "",
			wantBase:   opencode.DefaultLLMGatewayBaseURL,
			wantKey:    "",
			enterprise: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("POCKET_LLM_GATEWAY_URL", c.url)
			t.Setenv("POCKET_LLM_GATEWAY_API_KEY", c.key)
			cfg := config.Load()

			// ★ 前提哨兵：本组用例的全部意义来自「config.getEnv 不 trim」。
			// 一旦 #63 落地让 getEnv 变了，这里必须炸掉提醒更新预期，
			// 而不是让用例悄悄退化成「测另一个东西」且照样绿（§687.2 的教训）。
			if cfg.LLMGatewayURL != c.url || cfg.LLMGatewayAPIKey != c.key {
				t.Fatalf("前提失效：config 已不再原样透传 env（url=%q key=%q，期望 %q/%q）—— "+
					"本用例断言的是「消费侧负责归一」，若 #63 已在 getEnv 侧归一，请改写本用例",
					cfg.LLMGatewayURL, cfg.LLMGatewayAPIKey, c.url, c.key)
			}

			base, key := resolveGatewayBootstrap(cfg.LLMGatewayURL)
			if base != c.wantBase {
				t.Errorf("base = %q，期望 %q", base, c.wantBase)
			}
			if key != c.wantKey {
				t.Errorf("key = %q，期望 %q", key, c.wantKey)
			}
			if got := base != "" && key != ""; got != c.enterprise {
				t.Errorf("企业网关分支 = %v，期望 %v（base=%q key=%q）", got, c.enterprise, base, key)
			}
			if c.enterprise && strings.ContainsAny(base+key, " \t\n") {
				t.Errorf("进入企业网关分支的值不得含空白：base=%q key=%q", base, key)
			}
		})
	}
}

// TestResolveGatewayBootstrap_CredentialSameSourceAsGatewayProxy 钉住
// 「凭据只有一处实现」：返回的 key 必须与 `internal/server` 那一处逐字相同。
// 若有人把 TrimSpace 又抄回 cmd/pocketd，这里转红。
func TestResolveGatewayBootstrap_CredentialSameSourceAsGatewayProxy(t *testing.T) {
	for _, raw := range []string{"", "  ", "\tsk-abc\n", " sk-abc ", "sk-abc"} {
		t.Setenv("POCKET_LLM_GATEWAY_API_KEY", raw)
		_, key := resolveGatewayBootstrap("")
		if want := server.LLMGatewayEnvAPIKey(); key != want {
			t.Errorf("env=%q：启动期 key=%q，网关代理路径 key=%q —— 两侧必须是同一个实现",
				raw, key, want)
		}
	}
}
