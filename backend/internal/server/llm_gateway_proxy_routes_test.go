package server

// llm_gateway_proxy_routes_test.go — 白名单路由解析的单测（hermetic，无网络）。
//
// 重点覆盖新增的占位符：{wkey}（任务类型字符串 key）与 {did}（任务默认路由
// 数字 id），以及路径注入防护（".."、非法字符、stats 保留字）。

import (
	"net/http"
	"testing"
)

func TestResolveGatewayRouteWorkTypes(t *testing.T) {
	cases := []struct {
		name          string
		method, action string
		wantOK        bool
		wantUpstream  string
		wantParams    map[string]string
	}{
		{
			name:         "work-types list",
			method:       http.MethodGet,
			action:       "work-types",
			wantOK:       true,
			wantUpstream: "/api/admin/work-types",
		},
		{
			name:         "work-types stats keeps literal segment",
			method:       http.MethodGet,
			action:       "work-types/stats",
			wantOK:       true,
			wantUpstream: "/api/admin/work-types/stats",
		},
		{
			name:         "replace routes for a work type",
			method:       http.MethodPut,
			action:       "work-types/long_context/routes",
			wantOK:       true,
			wantUpstream: "/api/admin/work-types/long_context/routes",
			wantParams:   map[string]string{"wkey": "long_context"},
		},
		{
			name:         "patch a work type",
			method:       http.MethodPatch,
			action:       "work-types/function_call",
			wantOK:       true,
			wantUpstream: "/api/admin/work-types/function_call",
			wantParams:   map[string]string{"wkey": "function_call"},
		},
		{
			name:   "path traversal via wkey rejected",
			method: http.MethodPut,
			action: "work-types/../routes",
			wantOK: false,
		},
		{
			name:   "weird charset in wkey rejected",
			method: http.MethodPut,
			action: "work-types/a%20b/routes",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			route, params, ok := resolveGatewayRoute(tc.method, tc.action)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if route.upstreamPath != tc.wantUpstream && substituteParams(route.upstreamPath, params) != tc.wantUpstream {
				t.Fatalf("upstreamPath = %q (substituted %q), want %q", route.upstreamPath, substituteParams(route.upstreamPath, params), tc.wantUpstream)
			}
			for k, v := range tc.wantParams {
				if params[k] != v {
					t.Fatalf("params[%s] = %q, want %q", k, params[k], v)
				}
			}
		})
	}
}

func TestResolveGatewayRouteTaskDefaults(t *testing.T) {
	// 数字 id 归一化成 {did}（前一个 segment 是 defaults）。
	route, params, ok := resolveGatewayRoute(http.MethodDelete, "auto-route/defaults/42")
	if !ok {
		t.Fatal("expected route to resolve")
	}
	if route.upstreamMethod != http.MethodDelete {
		t.Fatalf("method = %s", route.upstreamMethod)
	}
	if got := substituteParams(route.upstreamPath, params); got != "/api/admin/auto-route/defaults/42" {
		t.Fatalf("substituted path = %q", got)
	}
	if !route.write {
		t.Fatal("delete must be a write route")
	}

	// audit 是字面量子路径，不应被当成 {did}。
	route, _, ok = resolveGatewayRoute(http.MethodGet, "auto-route/defaults/audit")
	if !ok || route.upstreamPath != "/api/admin/auto-route/defaults/audit" {
		t.Fatalf("audit route mismatch: ok=%v path=%q", ok, route.upstreamPath)
	}

	// 未登记的 action 必须拒绝。
	if _, _, ok := resolveGatewayRoute(http.MethodGet, "auto-route/defaults/999999"); ok {
		t.Fatal("GET on numeric defaults id should not resolve (not whitelisted)")
	}
}

func TestResolveGatewayRouteCatalogAndPolicy(t *testing.T) {
	for _, action := range []string{
		"catalog/available-models",
		"routing/policy",
		"routing/featured",
		"routing/scoring-weights",
		"config/default-limits",
	} {
		route, _, ok := resolveGatewayRoute(http.MethodGet, action)
		if !ok {
			t.Fatalf("GET %s should resolve", action)
		}
		if route.write {
			t.Fatalf("GET %s must not be a write route", action)
		}
	}

	// 写路由要求 pocket admin（write 标志由 proxy 检查）。
	if route, _, ok := resolveGatewayRoute(http.MethodPost, "routing/featured"); !ok || !route.write {
		t.Fatalf("POST routing/featured should be a whitelisted write route, ok=%v write=%v", ok, route.write)
	}
}

// 既有占位符不回归：credentials/providers 仍按原样解析。
func TestResolveGatewayRouteLegacyPlaceholders(t *testing.T) {
	route, params, ok := resolveGatewayRoute(http.MethodGet, "credentials/12/history")
	if !ok {
		t.Fatal("legacy credentials route should resolve")
	}
	if params["cid"] != "12" {
		t.Fatalf("cid = %q", params["cid"])
	}
	if route.upstreamPath != "/api/credentials/model-history" {
		t.Fatalf("path = %q", route.upstreamPath)
	}

	if _, _, ok := resolveGatewayRoute(http.MethodPost, "providers/7/toggle"); !ok {
		t.Fatal("legacy provider toggle should resolve")
	}
}
