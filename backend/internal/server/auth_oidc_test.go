package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/config"
)

// ============================================================================
// Casdoor 直连 OIDC 全链路测试（2026-11-14 认证收敛轮）
//
// 复用 /api/auth/sso/* 管道的 OIDC 分支：fake casdoor（token + userinfo）+
// 真实 Server 路由。断言点：
//   1. 纯 OIDC 模式本地凭据面退役（login/register → 410）
//   2. dev 旁路在 OIDC 模式失效
//   3. status → {"enabled":true,"mode":"oidc"}
//   4. login → authorize URL（client_id/PKCE/state）+ 绑定 cookie
//   5. callback → PKCE 换 token → userinfo → 302 到 SPA（sso_code + #route）
//   6. exchange → 一次性 code 换会话
//   7. state 失配 fail-closed（login-CSRF 根治）
// ============================================================================

type fakeCasdoorRecorder struct {
	secret, clientID, redirectURI, codeVerifier string
	tokenCalled, userinfoCalled                 bool
	failTokenWith                               int
	emptySub                                    bool
	noNameClaims                                bool
}

func fakeCasdoor(t *testing.T, rec *fakeCasdoorRecorder) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/login/oauth/access_token"):
			rec.tokenCalled = true
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			if got := r.PostFormValue("client_secret"); got != rec.secret {
				t.Errorf("client_secret mismatch: %q", got)
			}
			rec.redirectURI = r.PostFormValue("redirect_uri")
			rec.codeVerifier = r.PostFormValue("code_verifier")
			if rec.codeVerifier == "" {
				t.Error("PKCE code_verifier missing in token exchange")
			}
			if rec.failTokenWith != 0 {
				w.WriteHeader(rec.failTokenWith)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "fake-at", "token_type": "bearer", "expires_in": 3600,
			})
		case strings.HasSuffix(r.URL.Path, "/api/userinfo"):
			rec.userinfoCalled = true
			if rec.emptySub {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"sub": ""})
				return
			}
			if rec.noNameClaims {
				// 2026-11-14 真机验收缺陷场景：casdoor 用户 name 列为空时
				// userinfo 只有 sub/email（SQL 播种用户实测）。
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"sub": "casdoor-sub-42", "email": "alice@itestu.cn",
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub": "casdoor-sub-42", "name": "Alice", "preferred_username": "alice",
				"email": "alice@itestu.cn",
			})
		default:
			http.Error(w, "no route", http.StatusNotFound)
		}
	}))
}

// newOidcTestServer 构造纯 OIDC 模式的 Server（无 RedClaw admin，真签名器）。
func newOidcTestServer(t *testing.T, casdoorURL string, extra func(*config.Config)) *Server {
	t.Helper()
	cfg := config.Config{
		OIDCEndpoint:      casdoorURL,
		OIDCClientID:      "test-client-id",
		OIDCClientSecret:  "test-client-secret",
		OIDCBrowserReturn: "https://openpocket.example",
	}
	if extra != nil {
		extra(&cfg)
	}
	signer, err := auth.NewSigner("test-signing-key-at-least-32-bytes!", time.Hour)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return newServer(cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, signer, nil, nil, nil, nil, "", false, nil, nil)
}

// newOidcClient 不跟随重定向（302 要手工核 Location），携带 cookie（绑定 nonce）。
func newOidcClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func TestOIDC_LocalCredentialPlaneRetired(t *testing.T) {
	casdoor := fakeCasdoor(t, &fakeCasdoorRecorder{secret: "test-client-secret"})
	defer casdoor.Close()
	srv := newOidcTestServer(t, casdoor.URL, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, path := range []string{"/api/auth/login", "/api/auth/register", "/api/auth/code-login",
		"/api/auth/send-code", "/api/auth/forgot-password"} {
		resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(`{"username":"a","password":"b"}`))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusGone {
			t.Errorf("%s: status = %d, want 410 Gone", path, resp.StatusCode)
		}
	}

	// dev 旁路在 OIDC 模式下不再可用（即使误配 POCKET_DEV_AUTH/USER/PASS）。
	srvDev := newOidcTestServer(t, casdoor.URL, func(c *config.Config) {
		c.DevAuth = true
		c.DevAuthUser = "admin"
		c.DevAuthPass = "dev-pass"
	})
	tsDev := httptest.NewServer(srvDev.Handler())
	defer tsDev.Close()
	resp, err := http.Post(tsDev.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"username":"admin","password":"dev-pass"}`))
	if err != nil {
		t.Fatalf("dev bypass login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Errorf("dev bypass in oidc mode: status = %d, want 410 Gone", resp.StatusCode)
	}
}

func TestOIDC_StatusLoginCallbackExchange(t *testing.T) {
	rec := &fakeCasdoorRecorder{secret: "test-client-secret"}
	casdoor := fakeCasdoor(t, rec)
	defer casdoor.Close()
	srv := newOidcTestServer(t, casdoor.URL, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	client := newOidcClient(t)

	// 1. status → enabled + mode=oidc
	resp, err := client.Get(ts.URL + "/api/auth/sso/status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var st map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if st["enabled"] != true || st["mode"] != "oidc" {
		t.Fatalf("status = %v, want enabled+oidc", st)
	}

	// 2. login → authorize URL（client_id/PKCE/state）+ 绑定 cookie
	resp, err = client.Get(ts.URL + "/api/auth/sso/login")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	var body struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body.URL == "" {
		t.Fatal("login returned empty url")
	}
	u, err := url.Parse(body.URL)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	if u.Path != "/login/oauth/authorize" {
		t.Errorf("authorize path = %q", u.Path)
	}
	q := u.Query()
	nonce := q.Get("state")
	if q.Get("client_id") != "test-client-id" || nonce == "" || q.Get("code_challenge") == "" ||
		q.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorize url params incomplete: %v", q)
	}
	if q.Get("redirect_uri") != "https://openpocket.example/api/auth/sso/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}

	// 3. callback：state 原样回显 + code → 302 到 SPA（sso_code + hash route）
	resp, err = client.Get(ts.URL + "/api/auth/sso/callback?code=fake-code&state=" + url.QueryEscape(nonce))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "https://openpocket.example/auth/sso/callback?") ||
		!strings.Contains(loc, "#/auth/sso/callback") {
		t.Fatalf("callback location = %q, want browser-return + sso_code + hash route", loc)
	}
	// 解析 Location query 的 sso_code（在 # 之前）
	ssoCode := ""
	if lu, err2 := url.Parse(loc); err2 == nil {
		ssoCode = lu.Query().Get("sso_code")
	}
	if ssoCode == "" {
		t.Fatalf("sso_code missing in location: %q", loc)
	}
	if !rec.tokenCalled || !rec.userinfoCalled {
		t.Fatalf("casdoor token/userinfo not called: token=%v userinfo=%v", rec.tokenCalled, rec.userinfoCalled)
	}
	if rec.redirectURI != "https://openpocket.example/api/auth/sso/callback" {
		t.Errorf("token exchange redirect_uri = %q", rec.redirectURI)
	}

	// 4. exchange：一次性 code 换会话
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/sso/exchange",
		strings.NewReader(fmt.Sprintf(`{"code":%q}`, ssoCode)))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	var ex struct {
		Token  string `json:"token"`
		User   string `json:"user"`
		UserID string `json:"user_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&ex)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || ex.Token == "" || ex.UserID != "casdoor-sub-42" || ex.User != "Alice" {
		t.Fatalf("exchange = %d %v", resp.StatusCode, ex)
	}

	// 5. replay 同一 code → 401（一次性）
	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/sso/exchange",
		strings.NewReader(fmt.Sprintf(`{"code":%q}`, ssoCode)))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("replay exchange: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("replay exchange status = %d, want 401", resp2.StatusCode)
	}
}

func TestOIDC_StateMismatchFailClosed(t *testing.T) {
	rec := &fakeCasdoorRecorder{secret: "test-client-secret"}
	casdoor := fakeCasdoor(t, rec)
	defer casdoor.Close()
	srv := newOidcTestServer(t, casdoor.URL, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	client := newOidcClient(t)

	resp, err := client.Get(ts.URL + "/api/auth/sso/login")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()

	// 篡改 state：fail-closed，302 error=sso_state，且绝不触碰 casdoor。
	resp, err = client.Get(ts.URL + "/api/auth/sso/callback?code=fake-code&state=tampered")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "error=sso_state") {
		t.Fatalf("state mismatch: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if rec.tokenCalled {
		t.Fatal("token exchange must not run on state mismatch")
	}
}

func TestOIDC_DisabledWhenConfigIncomplete(t *testing.T) {
	// 只有 endpoint 没有 client_id/secret：SSO 面关闭（404），本地凭据面保持。
	srv := newOidcTestServer(t, "", func(c *config.Config) {
		c.OIDCClientID = ""
		c.OIDCClientSecret = ""
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/auth/sso/status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("incomplete oidc: status = %d, want 404", resp.StatusCode)
	}
	resp, err = http.Post(ts.URL+"/api/auth/login", "application/json", strings.NewReader(`{"username":"a","password":"b"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusGone {
		t.Errorf("incomplete oidc must not retire local credential plane")
	}
}

// TestOIDC_EmptyNameClaimsFallback：userinfo 缺 name/preferred_username claim
// 时（SQL 播种用户 name 列为空），exchange 的 user 必须兜底非空——否则前端
// store.user 空 → isAuthenticated=false → 路由守卫把已登录用户弹回 /login
// （2026-11-14 真机验收缺陷的回归钉）。
func TestOIDC_EmptyNameClaimsFallback(t *testing.T) {
	rec := &fakeCasdoorRecorder{secret: "test-client-secret", noNameClaims: true}
	casdoor := fakeCasdoor(t, rec)
	defer casdoor.Close()
	srv := newOidcTestServer(t, casdoor.URL, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	client := newOidcClient(t)

	resp, err := client.Get(ts.URL + "/api/auth/sso/login")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	var body struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	u, err := url.Parse(body.URL)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	nonce := u.Query().Get("state")

	resp, err = client.Get(ts.URL + "/api/auth/sso/callback?code=fake-code&state=" + url.QueryEscape(nonce))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	lu, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	ssoCode := lu.Query().Get("sso_code")
	if ssoCode == "" {
		t.Fatalf("sso_code missing in location: %q", loc)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/sso/exchange",
		strings.NewReader(fmt.Sprintf(`{"code":%q}`, ssoCode)))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	var ex struct {
		Token  string `json:"token"`
		User   string `json:"user"`
		UserID string `json:"user_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&ex)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || ex.Token == "" || ex.UserID != "casdoor-sub-42" {
		t.Fatalf("exchange = %d %v", resp.StatusCode, ex)
	}
	if ex.User == "" {
		t.Fatal("exchange user empty: guard would bounce a logged-in user back to /login")
	}
	if ex.User != "alice@itestu.cn" {
		t.Errorf("exchange user = %q, want email fallback alice@itestu.cn", ex.User)
	}
}
