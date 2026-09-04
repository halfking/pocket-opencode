package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// ============================================================================
// ssoTxnStore / ssoExchangeStore 单元测试
// ============================================================================

func TestSSOTxnStore_SingleUseAndExpiry(t *testing.T) {
	st := newSSOTxnStore(time.Minute, 16)
	nonce, err := st.Issue("1.2.3.4")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(nonce) != 64 { // 32 字节 hex
		t.Fatalf("nonce should be 64 hex chars, got %d", len(nonce))
	}
	if !st.Consume(nonce) {
		t.Fatal("first consume should succeed")
	}
	if st.Consume(nonce) {
		t.Fatal("replay consume must fail (single use)")
	}
	if st.Consume("garbage") {
		t.Fatal("unknown nonce must fail")
	}

	expired := newSSOTxnStore(-time.Second, 16) // 已过期
	n2, _ := expired.Issue("1.2.3.4")
	if expired.Consume(n2) {
		t.Fatal("expired nonce must fail")
	}
}

func TestSSOTxnStore_Cap(t *testing.T) {
	st := newSSOTxnStore(time.Minute, 2)
	for i := 0; i < 2; i++ {
		if _, err := st.Issue("1.2.3.4"); err != nil {
			t.Fatalf("Issue #%d: %v", i, err)
		}
	}
	if _, err := st.Issue("1.2.3.4"); err == nil {
		t.Fatal("issue beyond cap must fail")
	}
}

func TestSSOTxnStore_PerIPCap(t *testing.T) {
	st := newSSOTxnStore(time.Minute, 4096)
	// 同一来源灌到 per-IP 上限后必须拒绝，且不影响其他来源。
	for i := 0; i < ssoTxnPerIPCap; i++ {
		if _, err := st.Issue("10.0.0.1"); err != nil {
			t.Fatalf("Issue #%d for 10.0.0.1: %v", i, err)
		}
	}
	if _, err := st.Issue("10.0.0.1"); err == nil {
		t.Fatal("per-IP cap must reject same source")
	}
	if _, err := st.Issue("10.0.0.2"); err != nil {
		t.Fatalf("other source must be unaffected: %v", err)
	}
	// 消费后额度立即释放。
	nonce, _ := st.Issue("10.0.0.3")
	st.Consume(nonce)
}

func TestSSOExchangeStore_SingleUseAndExpiry(t *testing.T) {
	st := newSSOExchangeStore(time.Minute, 16)
	code, err := st.Put(ssoHandoff{Token: "tok", User: "u", UserID: "id", WorkspaceID: "ws"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	h, ok := st.Take(code)
	if !ok || h.Token != "tok" || h.UserID != "id" {
		t.Fatalf("first take mismatch: ok=%v h=%+v", ok, h)
	}
	if _, ok := st.Take(code); ok {
		t.Fatal("replay take must fail (single use)")
	}

	expired := newSSOExchangeStore(-time.Second, 16)
	c2, _ := expired.Put(ssoHandoff{Token: "t2"})
	if _, ok := expired.Take(c2); ok {
		t.Fatal("expired code must fail")
	}
	if _, ok := st.Take(""); ok {
		t.Fatal("empty code must fail")
	}
}

// ============================================================================
// Handler 全链路测试：fake auth-agent + httptest
// ============================================================================

// newSSOTestServer 构造开启 SSO 的 Server，auth-agent 指向 fake。
func newSSOTestServer(t *testing.T, fakeAgent *httptest.Server, ssoEnabled bool) *Server {
	t.Helper()
	cfg := config.Config{RedClawSsoEnabled: ssoEnabled}
	// startHubs=false：handler 测试不跑 websocket/plugin hub，避免泄漏 goroutine。
	srv := newServer(cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, "", false, nil)
	if fakeAgent != nil {
		client, err := redclaw.NewAdminAuthClient(redclaw.AdminAuthClientConfig{
			AdminURL:     fakeAgent.URL,
			AuthAgentURL: fakeAgent.URL,
			Secret:       "test-secret-32-bytes-long-aaaaaaaaaa",
			TenantID:     "default",
		})
		if err != nil {
			t.Fatalf("NewAdminAuthClient: %v", err)
		}
		srv.SetRedClawAdmin(client)
	}
	return srv
}

// fakeAuthAgent 模拟 RedClaw auth-agent /api/v1/sso/callback。
// 返回的记录器保存最后一次收到的 code/state/secret，便于断言透传行为。
type fakeAgentRecorder struct {
	code, state, secret string
	externalState       string // /sso/login 捕获的 external_state
	callbackCalled      bool
	failWith            int
	// echoOverride 非空时替换回显值（模拟失配）；suppressEcho 为 true 时
	// 不带 external_state 字段（模拟未升级的旧版 auth-agent）。
	echoOverride string
	suppressEcho bool
}

func fakeAuthAgent(t *testing.T, rec *fakeAgentRecorder) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/sso/login") {
			rec.externalState = r.URL.Query().Get("external_state")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"url": "https://idp.example.com/authorize?state=fake-idp-state"})
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/v1/sso/callback") {
			http.Error(w, `{"error":{"code":"not_found","message":"no route"}}`, http.StatusNotFound)
			return
		}
		rec.callbackCalled = true
		rec.code = r.URL.Query().Get("code")
		rec.state = r.URL.Query().Get("state")
		rec.secret = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if rec.failWith != 0 {
			http.Error(w, `{"error":{"code":"invalid_state","message":"state mismatch"}}`, rec.failWith)
			return
		}
		// 真实 RedClaw auth-agent 的响应形状：{jwt, claims, next, external_state}。
		payload := map[string]any{
			"jwt": "fake-platform-jwt",
			"claims": map[string]any{
				"sub": "emp-42", "name": "Alice", "email": "alice@example.com", "tenant": "default",
			},
			"next": "/",
		}
		if !rec.suppressEcho {
			echo := rec.externalState
			if rec.echoOverride != "" {
				echo = rec.echoOverride
			}
			payload["external_state"] = echo
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

func getSsoLoginURL(t *testing.T, srv *Server) (bodyURL string, txnCookie *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/login?redirect_url=http://app.example.com/api/auth/sso/callback", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("sso/login expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login resp: %v", err)
	}
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == ssoTxnCookie {
			txnCookie = ck
		}
	}
	return resp.URL, txnCookie
}

// visitAgentLogin 模拟浏览器访问 auth-agent /sso/login——真实链路中这一步
// 让 auth-agent 捕获 external_state 并签发自己的 IdP state（方案 A 合约）。
func visitAgentLogin(t *testing.T, agentURL string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, agentURL, nil)
	if err != nil {
		t.Fatalf("build agent login request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("agent login unreachable: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("agent login expected 200, got %d", resp.StatusCode)
	}
}

func TestSSO_FullChain_LoginCallbackExchange(t *testing.T) {
	rec := &fakeAgentRecorder{}
	fake := fakeAuthAgent(t, rec)
	defer fake.Close()
	srv := newSSOTestServer(t, fake, true)

	// 1. login：签发绑定 cookie + 返回 auth-agent URL
	agentURL, ck := getSsoLoginURL(t, srv)
	if ck == nil || ck.Value == "" {
		t.Fatal("sso/login must set binding cookie")
	}
	if !ck.HttpOnly {
		t.Error("binding cookie must be HttpOnly")
	}
	if ck.Path != "/api/auth/sso/" {
		t.Errorf("binding cookie path = %q", ck.Path)
	}
	u, err := url.Parse(agentURL)
	if err != nil {
		t.Fatalf("parse agent url: %v", err)
	}
	if got := u.Query().Get("external_state"); got != ck.Value {
		t.Errorf("agent url external_state should carry the binding nonce")
	}
	if u.Query().Get("state") != "" {
		t.Errorf("legacy state param must no longer be sent")
	}
	if got := u.Query().Get("redirect_url"); got != "" {
		t.Errorf("caller-controlled redirect_url must be omitted, got %q", got)
	}

	// 1b. 浏览器步：访问 auth-agent /sso/login（fake 捕获 external_state，
	//     等价真实链路中 IdP 只认 auth-agent 自发的 state）。
	visitAgentLogin(t, agentURL)
	if rec.externalState != ck.Value {
		t.Fatalf("agent login must capture external_state=%q, got %q", ck.Value, rec.externalState)
	}

	// 2. callback：带 cookie 回来 → 消费 cookie，透传 code+state 给 auth-agent，
	//    302 到 SPA 只带一次性 sso_code（token 不进 URL，P1-2）
	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/callback?code=authz-code&state=fake-idp-state", nil)
	req.AddCookie(&http.Cookie{Name: ssoTxnCookie, Value: ck.Value})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("callback expected 302, got %d body=%s", rr.Code, rr.Body.String())
	}
	loc, _ := url.Parse(rr.Header().Get("Location"))
	if loc.Path != "/auth/sso/callback" {
		t.Fatalf("redirect path = %q", loc.Path)
	}
	ssoCode := loc.Query().Get("sso_code")
	if ssoCode == "" {
		t.Fatalf("redirect must carry sso_code, got %q", loc.RawQuery)
	}
	if loc.Query().Get("token") != "" || loc.Query().Get("user") != "" {
		t.Fatal("redirect must NOT carry token/user (P1-2)")
	}
	if !rec.callbackCalled || rec.code != "authz-code" || rec.state != "fake-idp-state" {
		t.Errorf("auth-agent relay mismatch: rec=%+v", rec)
	}
	if rec.externalState != ck.Value {
		t.Errorf("auth-agent should have received external_state=%q, got %q", ck.Value, rec.externalState)
	}

	// 2b. 绑定 cookie 已被消费：重放同一回调 → 拒绝
	req2 := httptest.NewRequest(http.MethodGet, "/api/auth/sso/callback?code=authz-code&state=fake-idp-state", nil)
	req2.AddCookie(&http.Cookie{Name: ssoTxnCookie, Value: ck.Value})
	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusFound || !strings.Contains(rr2.Header().Get("Location"), "error=sso_session") {
		t.Fatalf("replayed callback must redirect with sso_session, got %d %q", rr2.Code, rr2.Header().Get("Location"))
	}

	// 3. exchange：一次性 code 换登录结果
	req3 := httptest.NewRequest(http.MethodPost, "/api/auth/sso/exchange", strings.NewReader(`{"code":"`+ssoCode+`"}`))
	rr3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("exchange expected 200, got %d body=%s", rr3.Code, rr3.Body.String())
	}
	var handoff ssoHandoff
	if err := json.Unmarshal(rr3.Body.Bytes(), &handoff); err != nil {
		t.Fatalf("decode exchange resp: %v", err)
	}
	if handoff.Token != "fake-platform-jwt" || handoff.UserID != "emp-42" || handoff.WorkspaceID != "default" {
		t.Errorf("exchange payload mismatch: %+v", handoff)
	}

	// 3b. 同一 code 重放 → 401
	req4 := httptest.NewRequest(http.MethodPost, "/api/auth/sso/exchange", strings.NewReader(`{"code":"`+ssoCode+`"}`))
	rr4 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusUnauthorized {
		t.Fatalf("replayed exchange must 401, got %d", rr4.Code)
	}
}

func TestSSO_Callback_WithoutBindingCookie(t *testing.T) {
	rec := &fakeAgentRecorder{}
	fake := fakeAuthAgent(t, rec)
	defer fake.Close()
	srv := newSSOTestServer(t, fake, true)

	// 冷启动回调（没走过 login，无 cookie）→ 拒绝且不打到 auth-agent
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/callback?code=c&state=s", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusFound || !strings.Contains(rr.Header().Get("Location"), "error=sso_session") {
		t.Fatalf("cold callback must redirect with sso_session, got %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if rec.callbackCalled {
		t.Error("auth-agent must not be reached without binding cookie")
	}
}

// TestSSO_Callback_ExternalStateStrictCompare 端到端 state 比对 fail-closed：
// auth-agent 回显缺失（旧版本未升级）或值不符（login-CSRF 场景）都不允许
// 完成登录——只回稳定错误码 sso_state，不签发 sso_code。
func TestSSO_Callback_ExternalStateStrictCompare(t *testing.T) {
	cases := []struct {
		name string
		rec  fakeAgentRecorder
	}{
		{"echo mismatch", fakeAgentRecorder{echoOverride: "attacker-nonce"}},
		{"echo missing (legacy agent)", fakeAgentRecorder{suppressEcho: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.rec
			fake := fakeAuthAgent(t, &rec)
			defer fake.Close()
			srv := newSSOTestServer(t, fake, true)

			agentURL, ck := getSsoLoginURL(t, srv)
			if ck == nil {
				t.Fatal("no binding cookie")
			}
			// 浏览器步：让 fake 捕获我方 nonce，回显阶段才能区分
			// 「值不符」与「缺失」两种失配。
			visitAgentLogin(t, agentURL)
			req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/callback?code=c&state=s", nil)
			req.AddCookie(&http.Cookie{Name: ssoTxnCookie, Value: ck.Value})
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusFound || !strings.Contains(rr.Header().Get("Location"), "error=sso_state") {
				t.Fatalf("mismatched echo must redirect with sso_state, got %d %q", rr.Code, rr.Header().Get("Location"))
			}
			if !rec.callbackCalled {
				t.Error("upstream should have been reached (rejection happens at echo comparison)")
			}
			loc, _ := url.Parse(rr.Header().Get("Location"))
			if loc.Query().Get("sso_code") != "" {
				t.Error("no sso_code may be issued on state mismatch")
			}
		})
	}
}

func TestSSO_Callback_ErrorPaths(t *testing.T) {
	fake := fakeAuthAgent(t, &fakeAgentRecorder{})
	defer fake.Close()
	srv := newSSOTestServer(t, fake, true)

	issue := func(t *testing.T, srv *Server) string {
		t.Helper()
		_, ck := getSsoLoginURL(t, srv)
		if ck == nil {
			t.Fatal("no binding cookie")
		}
		return ck.Value
	}

	cases := []struct {
		name      string
		query     string
		wantError string
	}{
		{"idp error param", "/api/auth/sso/callback?error=access_denied", "error=sso_idp"},
		{"missing code/state", "/api/auth/sso/callback?code=&state=", "error=sso_invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.query, nil)
			req.AddCookie(&http.Cookie{Name: ssoTxnCookie, Value: issue(t, srv)})
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusFound || !strings.Contains(rr.Header().Get("Location"), tc.wantError) {
				t.Fatalf("want redirect %s, got %d %q", tc.wantError, rr.Code, rr.Header().Get("Location"))
			}
		})
	}

	// upstream 拒绝（fake agent 返回 401）→ error=sso_upstream
	rec := &fakeAgentRecorder{failWith: http.StatusUnauthorized}
	fake2 := fakeAuthAgent(t, rec)
	defer fake2.Close()
	srv2 := newSSOTestServer(t, fake2, true)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: ssoTxnCookie, Value: issue(t, srv2)})
	rr := httptest.NewRecorder()
	srv2.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusFound || !strings.Contains(rr.Header().Get("Location"), "error=sso_upstream") {
		t.Fatalf("upstream failure must redirect with sso_upstream, got %d %q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestSSO_Status_NoSideEffects(t *testing.T) {
	fake := fakeAuthAgent(t, &fakeAgentRecorder{})
	defer fake.Close()
	srv := newSSOTestServer(t, fake, true)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status expected 200, got %d", rr.Code)
	}
	if cookies := rr.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("status probe must not set cookies, got %v", cookies)
	}
	if got := srv.ssoTxns.countByIPLocked("192.0.2.9"); got != 0 {
		t.Errorf("status probe must not mint nonces, pending=%d", got)
	}
}

func TestSSO_Disabled_Returns404(t *testing.T) {
	fake := fakeAuthAgent(t, &fakeAgentRecorder{})
	defer fake.Close()
	srv := newSSOTestServer(t, fake, false) // SSO 关闭

	paths := []struct{ method, path string }{
		{http.MethodGet, "/api/auth/sso/status"},
		{http.MethodGet, "/api/auth/sso/login"},
		{http.MethodGet, "/api/auth/sso/callback?code=c&state=s"},
		{http.MethodPost, "/api/auth/sso/exchange"},
	}
	for _, p := range paths {
		req := httptest.NewRequest(p.method, p.path, strings.NewReader("{}"))
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s %s: expected 404, got %d", p.method, p.path, rr.Code)
		}
	}
}
