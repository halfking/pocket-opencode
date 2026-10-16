package server

// cors_capacitor_origin_test.go — Capacitor 应用源必须无条件拿到 ACAO（2026-10-16）。
//
// 背景（真机实证，2411DRN47C / Android 16）：
//   移动端 APK 的 WebView 固定跑在 http://localhost（CAP_ANDROID_SCHEME=http 构建）。
//   对生产后端（devAuth=false，如测试环境 pocket.itestu.cn）的所有 fetch 都被
//   Chromium 以缺 Access-Control-Allow-Origin 拦截 —— curl 一切正常、真机登录
//   永远停在登录页。buildOriginChecker 此前只在 devAuth=true 时放行 localhost。
//
//   安全性：API 走 Authorization Bearer 头，没有自动携带的 cookie 凭据；
//   跨源页面拿不到 token 就读不到任何受保护响应，放行 localhost 源不扩大攻击面。

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCapacitorAppOriginsAllowedWithoutDevAuth(t *testing.T) {
	check := buildOriginChecker("", false)
	for _, origin := range []string{"http://localhost", "https://localhost"} {
		r := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
		r.Header.Set("Origin", origin)
		if !check(r) {
			t.Errorf("Capacitor 源 %s 在生产模式（devAuth=false）下必须放行，否则 APK 无法调用任何生产后端", origin)
		}
	}
}

func TestCapacitorAppOriginsStillRejected(t *testing.T) {
	check := buildOriginChecker("", false)
	for _, origin := range []string{
		"http://localhost:8080",     // 带端口 = 别的本地服务，不是 Capacitor 固定源
		"http://localhost.evil.com", // 后缀伪装
		"http://evil.com",           // 完全无关
		"http://localhost/path",     // 带路径（Origin 头理论上是裸源，防御性）
		"capacitor://localhost",     // 非 http(s) scheme
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
		r.Header.Set("Origin", origin)
		if check(r) {
			t.Errorf("源 %s 在生产模式下不应放行", origin)
		}
	}
}

func TestDevAuthLocalhostAnyPortStillAllowed(t *testing.T) {
	check := buildOriginChecker("", true)
	for _, origin := range []string{"http://localhost:5173", "http://127.0.0.1:8080", "http://localhost"} {
		r := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
		r.Header.Set("Origin", origin)
		if !check(r) {
			t.Errorf("devAuth 模式下本地源 %s 应放行（既有行为不能回退）", origin)
		}
	}
}

func TestCorsMiddlewareEmitsACAOForCapacitorOriginInProduction(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "", false)

	// 预检：必须 200 且带 ACAO（此前生产模式对 localhost 返回 403）
	pre := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	pre.Header.Set("Origin", "http://localhost")
	pre.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preRes := httptest.NewRecorder()
	handler.ServeHTTP(preRes, pre)
	if preRes.Code != http.StatusOK {
		t.Errorf("生产模式下 Capacitor 源的预检应 200，实际 %d", preRes.Code)
	}
	if preRes.Header().Get("Access-Control-Allow-Origin") != "http://localhost" {
		t.Errorf("预检响应缺 ACAO: %v", preRes.Header())
	}

	// 实际请求：ACAO 必须透传，否则浏览器仍会拦掉响应
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.Header.Set("Origin", "http://localhost")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Header().Get("Access-Control-Allow-Origin") != "http://localhost" {
		t.Errorf("实际响应缺 ACAO，WebView 会拦截登录响应: %v", res.Header())
	}
}
