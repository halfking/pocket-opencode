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
	nonce, err := st.Issue()
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
	n2, _ := expired.Issue()
	if expired.Consume(n2) {
		t.Fatal("expired nonce must fail")
	}
}

func TestSSOTxnStore_Cap(t *testing.T) {
	st := newSSOTxnStore(time.Minute, 2)
	for i := 0; i < 2; i++ {
		if _, err := st.Issue(); err != nil {
			t.Fatalf("Issue #%d: %v", i, err)
		}
	}
	if _, err := st.Issue(); err == nil {
		t.Fatal("issue beyond cap must fail")
	}
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
	srv := New(cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, "", nil)
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
	callbackCalled      bool
	failWith            int
}

func fakeAuthAgent(t *testing.T, rec *fakeAgentRecorder) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":              "fake-platform-jwt",
			"mustChangePassword": false,
			"employee": map[string]any{
				"id": "emp-42", "name": "Alice", "role": "user", "email": "alice@example.com",
			},
		})
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
	if got := u.Query().Get("state"); got != ck.Value {
		t.Errorf("agent url state should carry the binding nonce")
	}
	if got := u.Query().Get("redirect_url"); got != "http://app.example.com/api/auth/sso/callback" {
		t.Errorf("agent url redirect_url = %q", got)
	}

	// 2. callback：带 cookie 回来 → 消费 cookie，透传 code+state 给 auth-agent，
	//    302 到 SPA 只带一次性 sso_code（token 不进 URL，P1-2）
	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/callback?code=authz-code&state=agent-owned-state", nil)
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
	if !rec.callbackCalled || rec.code != "authz-code" || rec.state != "agent-owned-state" {
		t.Errorf("auth-agent relay mismatch: rec=%+v", rec)
	}

	// 2b. 绑定 cookie 已被消费：重放同一回调 → 拒绝
	req2 := httptest.NewRequest(http.MethodGet, "/api/auth/sso/callback?code=authz-code&state=agent-owned-state", nil)
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

func TestSSO_Disabled_Returns404(t *testing.T) {
	fake := fakeAuthAgent(t, &fakeAgentRecorder{})
	defer fake.Close()
	srv := newSSOTestServer(t, fake, false) // SSO 关闭

	paths := []struct{ method, path string }{
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

