package server

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
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
	nonce, err := st.issue("1.2.3.4", ssoTxnEntry{})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(nonce) != 64 { // 32 字节 hex
		t.Fatalf("nonce should be 64 hex chars, got %d", len(nonce))
	}
	if _, ok := st.consume(nonce); !ok {
		t.Fatal("first consume should succeed")
	}
	if _, ok := st.consume(nonce); ok {
		t.Fatal("replay consume must fail (single use)")
	}
	if _, ok := st.consume("garbage"); ok {
		t.Fatal("unknown nonce must fail")
	}

	expired := newSSOTxnStore(-time.Second, 16) // 已过期
	n2, _ := expired.issue("1.2.3.4", ssoTxnEntry{})
	if _, ok := expired.consume(n2); ok {
		t.Fatal("expired nonce must fail")
	}
}

func TestSSOTxnStore_Cap(t *testing.T) {
	st := newSSOTxnStore(time.Minute, 2)
	for i := 0; i < 2; i++ {
		if _, err := st.issue("1.2.3.4", ssoTxnEntry{}); err != nil {
			t.Fatalf("Issue #%d: %v", i, err)
		}
	}
	if _, err := st.issue("1.2.3.4", ssoTxnEntry{}); err == nil {
		t.Fatal("issue beyond cap must fail")
	}
}

func TestSSOTxnStore_PerIPCap(t *testing.T) {
	st := newSSOTxnStore(time.Minute, 4096)
	// 同一来源灌到 per-IP 上限后必须拒绝，且不影响其他来源。
	for i := 0; i < ssoTxnPerIPCap; i++ {
		if _, err := st.issue("10.0.0.1", ssoTxnEntry{}); err != nil {
			t.Fatalf("Issue #%d for 10.0.0.1: %v", i, err)
		}
	}
	if _, err := st.issue("10.0.0.1", ssoTxnEntry{}); err == nil {
		t.Fatal("per-IP cap must reject same source")
	}
	if _, err := st.issue("10.0.0.2", ssoTxnEntry{}); err != nil {
		t.Fatalf("other source must be unaffected: %v", err)
	}
	// 消费后额度立即释放。
	nonce, _ := st.issue("10.0.0.3", ssoTxnEntry{})
	st.consume(nonce)
}

func TestSSOExchangeStore_SingleUseAndExpiry(t *testing.T) {
	st := newSSOExchangeStore(time.Minute, 16)
	code, err := st.Put(ssoHandoff{Token: "tok", User: "u", UserID: "id", WorkspaceID: "ws"}, "")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	h, ok := st.Take(code, "")
	if !ok || h.Token != "tok" || h.UserID != "id" {
		t.Fatalf("first take mismatch: ok=%v h=%+v", ok, h)
	}
	if _, ok := st.Take(code, ""); ok {
		t.Fatal("replay take must fail (single use)")
	}

	expired := newSSOExchangeStore(-time.Second, 16)
	c2, _ := expired.Put(ssoHandoff{Token: "t2"}, "")
	if _, ok := expired.Take(c2, ""); ok {
		t.Fatal("expired code must fail")
	}
	if _, ok := st.Take("", ""); ok {
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
		nil, nil, nil, nil, nil, nil, "", false, nil, nil)
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
	profileCalls        int
	profileStatus       int
	profileTenant       string
	legacyProfile       bool
	failWith            int
	// echoOverride 非空时替换回显值（模拟失配）；suppressEcho 为 true 时
	// 不带 external_state 字段（模拟未升级的旧版 auth-agent）。
	echoOverride string
	suppressEcho bool
}

func fakeAuthAgent(t *testing.T, rec *fakeAgentRecorder) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/me" {
			rec.profileCalls++
			if r.URL.Query().Get("require_provisioned") != "1" || r.Header.Get("Authorization") != "Bearer fake-platform-jwt" {
				t.Error("SSO must resolve the provisioned identity with the issued upstream bearer")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if rec.profileStatus != 0 {
				w.WriteHeader(rec.profileStatus)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			tenant := rec.profileTenant
			if tenant == "" {
				tenant = "default"
			}
			profile := map[string]any{"id": "emp-mapped-42", "name": "Mapped Alice", "tenantId": tenant}
			if !rec.legacyProfile {
				profile["provisioned"] = true
			}
			_ = json.NewEncoder(w).Encode(profile)
			return
		}
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
	if loc.Path != "/auth/sso/callback" || loc.Fragment != "/auth/sso/callback" {
		t.Fatalf("redirect must mount the callback route: %q", loc)
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
	if handoff.Token != "fake-platform-jwt" || handoff.UserID != "emp-mapped-42" || handoff.User != "Mapped Alice" || handoff.WorkspaceID != "" || !strings.Contains(rr3.Body.String(), `"auth_method":"redclaw-oidc"`) {
		t.Errorf("exchange payload mismatch: %+v", handoff)
	}
	if rec.profileCalls != 1 {
		t.Fatalf("profile lookups = %d, want 1", rec.profileCalls)
	}

	// 3b. 同一 code 重放 → 401
	req4 := httptest.NewRequest(http.MethodPost, "/api/auth/sso/exchange", strings.NewReader(`{"code":"`+ssoCode+`"}`))
	rr4 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusUnauthorized {
		t.Fatalf("replayed exchange must 401, got %d", rr4.Code)
	}
}

func TestSSO_CallbackRequiresProvisionedProfile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup fakeAgentRecorder
	}{
		{"not provisioned", fakeAgentRecorder{profileStatus: http.StatusForbidden}},
		{"legacy claims fallback", fakeAgentRecorder{legacyProfile: true}},
		{"different tenant", fakeAgentRecorder{profileTenant: "other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := fakeAuthAgent(t, &tc.setup)
			defer fake.Close()
			srv := newSSOTestServer(t, fake, true)
			agentURL, cookie := getSsoLoginURL(t, srv)
			visitAgentLogin(t, agentURL)
			req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/callback?code=c&state=s", nil)
			req.AddCookie(cookie)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)
			loc, err := url.Parse(w.Header().Get("Location"))
			if err != nil || w.Code != http.StatusFound || loc.Query().Get("error") != "sso_no_user" ||
				loc.Query().Get("sso_code") != "" || tc.setup.profileCalls != 1 {
				t.Fatalf("callback did not require provisioned profile: %d %v", w.Code, loc)
			}
		})
	}
}

func TestNativeSSOAppProofBindsCallbackAndExchange(t *testing.T) {
	rec := &fakeAgentRecorder{}
	fake := fakeAuthAgent(t, rec)
	defer fake.Close()
	browserAgent := httptest.NewTLSServer(fake.Config.Handler)
	defer browserAgent.Close()
	srv := newSSOTestServer(t, fake, true)
	client, err := redclaw.NewAdminAuthClient(redclaw.AdminAuthClientConfig{
		AdminURL: fake.URL, AuthAgentURL: fake.URL, AuthAgentPublicURL: browserAgent.URL,
		Secret: "test-secret-32-bytes-long-aaaaaaaaaa", TenantID: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetRedClawAdmin(client)
	srv.cfg.RedClawSsoAppOrigin = "https://app-link.example.org"

	const verifier = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	challenge := nativeSSOChallenge(verifier)
	start := func(address string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, address, nil))
		return rr
	}
	status := start("https://pocket.example.org/api/auth/sso/status")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), srv.cfg.RedClawSsoAppOrigin) {
		t.Fatalf("missing native capability: %d %s", status.Code, status.Body.String())
	}
	for _, address := range []string{
		"http://pocket.example.org/api/auth/sso/native/start?challenge=" + challenge,
		"https://app-link.example.org/api/auth/sso/native/start?challenge=" + challenge,
		"https://pocket.example.org/api/auth/sso/native/start?challenge=malformed",
		"https://pocket.example.org/api/auth/sso/native/start?challenge=" + challenge + "&challenge=" + challenge,
	} {
		rr := start(address)
		if rr.Code != http.StatusBadRequest || rr.Header().Get("Set-Cookie") != "" {
			t.Fatalf("unsafe start %s returned %d, cookies=%v", address, rr.Code, rr.Result().Cookies())
		}
	}

	initial := start("https://pocket.example.org/api/auth/sso/native/start?challenge=" + challenge + "&redirect_url=https://untrusted.example.org/")
	if initial.Code != http.StatusFound || initial.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("native start returned %d: %s", initial.Code, initial.Body.String())
	}
	cookies := initial.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected browser binding cookie: %#v", cookies)
	}
	destination := initial.Header().Get("Location")
	if strings.Contains(destination, challenge) || strings.Contains(destination, "untrusted") {
		t.Fatal("provider URL must contain only the normal login transaction state")
	}
	resp, err := browserAgent.Client().Get(destination)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("agent browser entry returned %d", resp.StatusCode)
	}

	callback := httptest.NewRequest(http.MethodGet, "https://pocket.example.org/api/auth/sso/callback?code=authz-code&state=fake-idp-state", nil)
	callback.AddCookie(cookies[0])
	landing := httptest.NewRecorder()
	srv.Handler().ServeHTTP(landing, callback)
	if landing.Code != http.StatusOK || landing.Header().Get("Cache-Control") != "no-store" ||
		landing.Header().Get("Referrer-Policy") != "no-referrer" || landing.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("unexpected native return: %d %s", landing.Code, landing.Body.String())
	}
	link := regexp.MustCompile(`href="([^"]+)"`).FindStringSubmatch(landing.Body.String())
	if len(link) != 2 || strings.Contains(landing.Body.String(), "fake-platform-jwt") {
		t.Fatal("native return needs an app link and must not contain a bearer")
	}
	u, err := url.Parse(html.UnescapeString(link[1]))
	if err != nil || u.Scheme != "https" || u.Host != "app-link.example.org" || u.Path != nativeSSOReturnPath || u.RawQuery != "" {
		t.Fatalf("unexpected app return link %q: %v", link[1], err)
	}
	fallback := start(u.Scheme + "://" + u.Host + u.Path)
	if fallback.Code != http.StatusOK || !strings.Contains(fallback.Body.String(), "未能打开 OpenPocket") {
		t.Fatalf("missing browser fallback: %d %s", fallback.Code, fallback.Body.String())
	}
	result, err := url.ParseQuery(u.Fragment)
	code := result.Get("code")
	if err != nil || code == "" || result.Get("challenge") != challenge {
		t.Fatalf("unexpected bound app return parameters: %v", err)
	}
	exchange := func(proof string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"code": code, "code_verifier": proof})
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "https://pocket.example.org/api/auth/sso/exchange", strings.NewReader(string(body))))
		return rr
	}
	for _, proof := range []string{"", "BAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if rr := exchange(proof); rr.Code != http.StatusUnauthorized || strings.Contains(rr.Body.String(), "fake-platform-jwt") {
			t.Fatalf("missing or wrong proof released a session: %d", rr.Code)
		}
	}
	if rr := exchange(verifier); rr.Code != http.StatusOK ||
		!strings.Contains(rr.Body.String(), `"auth_method":"redclaw-oidc"`) ||
		!strings.Contains(rr.Body.String(), `"user_id":"emp-mapped-42"`) {
		t.Fatalf("initiating app exchange returned %d: %s", rr.Code, rr.Body.String())
	}
	if rr := exchange(verifier); rr.Code != http.StatusUnauthorized {
		t.Fatalf("native exchange replay returned %d", rr.Code)
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
