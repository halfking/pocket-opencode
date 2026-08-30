package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// 白名单路由解析
// ─────────────────────────────────────────────────────────────────────────────

func TestResolveGatewayRoute(t *testing.T) {
	cases := []struct {
		name         string
		method       string
		action       string
		wantOK       bool
		wantUpstream string
		wantWrite    bool
		wantParams   map[string]string
	}{
		{
			name: "读：供应商列表", method: http.MethodGet, action: "providers",
			wantOK: true, wantUpstream: "/api/providers",
		},
		{
			name: "读：凭据列表", method: http.MethodGet, action: "credentials",
			wantOK: true, wantUpstream: "/api/credentials/monitor-summary",
		},
		{
			name: "读：凭据详情（数字 id 归一化成 {cid}）", method: http.MethodGet, action: "credentials/42",
			wantOK: true, wantUpstream: "/api/credentials/monitor-summary",
			wantParams: map[string]string{"cid": "42"},
		},
		{
			name: "读：凭据历史", method: http.MethodGet, action: "credentials/42/history",
			wantOK: true, wantUpstream: "/api/credentials/model-history",
			wantParams: map[string]string{"cid": "42"},
		},
		{
			name: "读：模型树", method: http.MethodGet, action: "models",
			wantOK: true, wantUpstream: "/api/routing/model-tree",
		},
		{
			name: "写：供应商翻转", method: http.MethodPost, action: "providers/5/toggle",
			wantOK: true, wantUpstream: "/api/providers/{pid}/toggle", wantWrite: true,
			wantParams: map[string]string{"pid": "5"},
		},
		{
			name: "写：凭据上线", method: http.MethodPost, action: "credentials/promote",
			wantOK: true, wantUpstream: "/api/credentials/promote", wantWrite: true,
		},
		{
			name: "写：模型探测", method: http.MethodPost, action: "routing/probe",
			wantOK: true, wantUpstream: "/api/routing/probe", wantWrite: true,
		},
		// ── 以下必须全部落空，否则代理就成了任意 HTTP 跳板 ──
		{name: "拒绝：未登记的上游路径", method: http.MethodGet, action: "admin/tenants", wantOK: false},
		{name: "拒绝：方法不匹配", method: http.MethodDelete, action: "providers", wantOK: false},
		{name: "拒绝：路径穿越", method: http.MethodGet, action: "../../api/admin/tenants", wantOK: false},
		{name: "拒绝：空 action", method: http.MethodGet, action: "", wantOK: false},
		{name: "拒绝：未知位置的数字 segment", method: http.MethodGet, action: "models/7", wantOK: false},
		{name: "拒绝：读端点用写方法", method: http.MethodPost, action: "providers", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			route, params, ok := resolveGatewayRoute(tc.method, tc.action)
			if ok != tc.wantOK {
				t.Fatalf("resolveGatewayRoute(%q, %q) ok = %v, want %v", tc.method, tc.action, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if route.upstreamPath != tc.wantUpstream {
				t.Errorf("upstreamPath = %q, want %q", route.upstreamPath, tc.wantUpstream)
			}
			if route.write != tc.wantWrite {
				t.Errorf("write = %v, want %v", route.write, tc.wantWrite)
			}
			for k, want := range tc.wantParams {
				if params[k] != want {
					t.Errorf("params[%q] = %q, want %q", k, params[k], want)
				}
			}
		})
	}
}

// TestBuildUpstreamQueryDropsUnlistedParams 确认 query 是白名单而非黑名单：
// 调用方注入 tenant_id 这类越权参数时必须被丢弃。
func TestBuildUpstreamQueryDropsUnlistedParams(t *testing.T) {
	route := gatewayProxyRoutes["GET credentials"]
	incoming := url.Values{
		"provider_id":   {"3"},
		"tenant_id":     {"victim-tenant"}, // 不在 allowedQuery 里
		"credential_id": {"999"},           // 同样不在，防止绕过 detail 模式的 scope
	}

	got := buildUpstreamQuery(route, incoming, nil)

	if got.Get("provider_id") != "3" {
		t.Errorf("provider_id 应被透传，得到 %q", got.Get("provider_id"))
	}
	if got.Has("tenant_id") {
		t.Error("tenant_id 必须被丢弃，否则可跨租户读取")
	}
	if got.Has("credential_id") {
		t.Error("credential_id 未在白名单中，必须被丢弃")
	}
}

// TestBuildUpstreamQueryForcedParamWins 确认 forcedQuery 覆盖调用方输入：
// credentials/{cid} 的 credential_id 只能来自 URL path，不能被 query 改写。
func TestBuildUpstreamQueryForcedParamWins(t *testing.T) {
	route := gatewayProxyRoutes["GET credentials/{cid}"]
	incoming := url.Values{"credential_id": {"999"}}

	got := buildUpstreamQuery(route, incoming, map[string]string{"cid": "42"})

	if got.Get("credential_id") != "42" {
		t.Errorf("credential_id = %q, want 42（path 参数必须胜出）", got.Get("credential_id"))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SSRF 校验与 ALLOW_PRIVATE 开关
// ─────────────────────────────────────────────────────────────────────────────

func TestValidateGatewayURLDefaultBlocksPrivate(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "")

	blocked := []string{
		"http://127.0.0.1:8781",
		"http://localhost:8781",
		"http://10.0.0.5",
		"http://192.168.1.10:8781",
		"http://169.254.169.254/latest/meta-data/",
		"http://metadata.google.internal/",
		"ftp://example.com",
		"https://user:pass@example.com",
		"https://example.com?x=1",
	}
	for _, raw := range blocked {
		if err := validateGatewayURL(raw); err == nil {
			t.Errorf("validateGatewayURL(%q) = nil, 期望被拒绝", raw)
		}
	}

	if err := validateGatewayURL("https://llmgo.kxpms.cn"); err != nil {
		t.Errorf("公网 HTTPS 地址应被放行，得到 %v", err)
	}
}

// TestValidateGatewayURLAllowPrivateOptIn 确认开关只放宽私网，
// 不放宽云元数据地址 —— 后者纯粹是凭据泄露面。
func TestValidateGatewayURLAllowPrivateOptIn(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")

	allowed := []string{
		"http://127.0.0.1:8781",
		"http://10.0.0.5",
		"http://llm-gateway.internal",
	}
	for _, raw := range allowed {
		if err := validateGatewayURL(raw); err != nil {
			t.Errorf("开关打开后 %q 应被放行，得到 %v", raw, err)
		}
	}

	stillBlocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://metadata.google.internal/",
	}
	for _, raw := range stillBlocked {
		if err := validateGatewayURL(raw); err == nil {
			t.Errorf("即使开关打开，%q 也必须被拒绝", raw)
		}
	}
}

// TestValidateOutboundURLUnaffectedByGatewaySwitch 回归：新开关不得放宽
// 既有的 /api/llm-gateway/config 出站校验。
func TestValidateOutboundURLUnaffectedByGatewaySwitch(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")

	if err := validateOutboundURL("http://127.0.0.1:1"); err == nil {
		t.Error("validateOutboundURL 必须继续拒绝 loopback，不受网关开关影响")
	}
}

// TestAdminBaseURLStripsDataPlaneSuffix 确认历史配置（带 /v1 的数据面端点）
// 被归一化到 admin API 的根路径。
func TestAdminBaseURLStripsDataPlaneSuffix(t *testing.T) {
	cases := map[string]string{
		"https://llmgo.kxpms.cn/v1":  "https://llmgo.kxpms.cn",
		"https://llmgo.kxpms.cn/v1/": "https://llmgo.kxpms.cn",
		"https://llmgo.kxpms.cn":     "https://llmgo.kxpms.cn",
		"https://llmgo.kxpms.cn/":    "https://llmgo.kxpms.cn",
	}
	for in, want := range cases {
		got, err := adminBaseURL(in)
		if err != nil {
			t.Fatalf("adminBaseURL(%q) 报错 %v", in, err)
		}
		if got != want {
			t.Errorf("adminBaseURL(%q) = %q, want %q", in, got, want)
		}
	}

	if _, err := adminBaseURL("http://169.254.169.254/v1"); err == nil {
		t.Error("adminBaseURL 必须拒绝元数据地址")
	}
}

func TestBuildURLRejectsRelativeUpstreamPath(t *testing.T) {
	if _, err := buildURL("https://gw.example.com", "api/providers", nil); err == nil {
		t.Error("upstreamPath 未以 / 开头时必须报错")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// admin client：token 缓存 / 401 重登
// ─────────────────────────────────────────────────────────────────────────────

// fakeGateway 是一个最小的 llm-gateway-go admin API 替身。
type fakeGateway struct {
	server      *httptest.Server
	loginCount  int32
	tokenSerial int32
	// rejectToken 非空时，携带该 token 的业务请求返回 401（模拟网关重启后
	// JWT secret 轮换，旧 token 失效）。
	rejectToken atomic.Value
	expiresIn   time.Duration
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	fg := &fakeGateway{expiresIn: time.Hour}
	fg.rejectToken.Store("")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/token", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		// 接受两组凭据：轮换密码的测试需要新密码同样能登录成功，
		// 否则"重新登录"与"登录失败"两种结果无法区分。
		validPassword, known := map[string]string{
			"ops": "secret",
		}[body.Username]
		if !known || (body.Password != validPassword && body.Password != "rotated-secret") {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid credentials"})
			return
		}
		atomic.AddInt32(&fg.loginCount, 1)
		serial := atomic.AddInt32(&fg.tokenSerial, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-" + string(rune('0'+serial)),
			"token_type":   "Bearer",
			"expires_at":   time.Now().Add(fg.expiresIn).Format(time.RFC3339),
			"user":         map[string]any{"role": "super_admin", "username": "ops"},
		})
	})
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"role": "super_admin", "username": "ops"})
	})
	mux.HandleFunc("/api/providers", func(w http.ResponseWriter, r *http.Request) {
		presented := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if bad, _ := fg.rejectToken.Load().(string); bad != "" && presented == bad {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authentication required"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": []any{}, "presented_token": presented})
	})
	mux.HandleFunc("/api/routing/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "super_admin required"})
	})

	fg.server = httptest.NewServer(mux)
	t.Cleanup(fg.server.Close)
	return fg
}

func (fg *fakeGateway) secret(t *testing.T) *GatewayNodeSecret {
	t.Helper()
	return &GatewayNodeSecret{
		Node: GatewayNode{
			ID: 1, WorkspaceID: "ws-1", Name: "test",
			BaseURL: fg.server.URL, AdminUsername: "ops", Enabled: true,
		},
		AdminPassword: "secret",
	}
}

// newTestGatewayClient 构造一个不带 store 的 client。store 只在
// handler 层用于加载凭据，client 自身不依赖它，所以单测可以传 nil。
func newTestGatewayClient() *gatewayAdminClient {
	return newGatewayAdminClient(nil)
}

func TestGatewayClientReusesCachedToken(t *testing.T) {
	// httptest 监听 127.0.0.1，需打开私网开关才能连通。
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")
	fg := newFakeGateway(t)
	client := newTestGatewayClient()
	secret := fg.secret(t)

	for i := 0; i < 3; i++ {
		if _, err := client.do(context.Background(), secret, http.MethodGet, "/api/providers", nil, nil); err != nil {
			t.Fatalf("第 %d 次调用失败: %v", i+1, err)
		}
	}

	if got := atomic.LoadInt32(&fg.loginCount); got != 1 {
		t.Errorf("登录次数 = %d, want 1（后续调用应复用缓存 token）", got)
	}
}

// TestGatewayClientRelogsInOn401 覆盖网关重启导致旧 token 失效的场景。
func TestGatewayClientRelogsInOn401(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")
	fg := newFakeGateway(t)
	client := newTestGatewayClient()
	secret := fg.secret(t)

	// 先拿到一个 token 并缓存。
	if _, err := client.do(context.Background(), secret, http.MethodGet, "/api/providers", nil, nil); err != nil {
		t.Fatalf("首次调用失败: %v", err)
	}
	// 让网关开始拒绝这个 token。
	fg.rejectToken.Store("tok-1")

	body, err := client.do(context.Background(), secret, http.MethodGet, "/api/providers", nil, nil)
	if err != nil {
		t.Fatalf("401 后应自动重登并成功，得到 %v", err)
	}
	if got := atomic.LoadInt32(&fg.loginCount); got != 2 {
		t.Errorf("登录次数 = %d, want 2（一次初始 + 一次 401 重登）", got)
	}
	if !strings.Contains(string(body), "tok-2") {
		t.Errorf("重试应使用新 token，响应为 %s", body)
	}
}

// TestGatewayClientInvalidatesTokenOnCredentialChange 确认改密码后旧 token
// 不会被继续使用（缓存条目带凭据指纹）。
func TestGatewayClientInvalidatesTokenOnCredentialChange(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")
	fg := newFakeGateway(t)
	client := newTestGatewayClient()

	secret := fg.secret(t)
	if _, err := client.do(context.Background(), secret, http.MethodGet, "/api/providers", nil, nil); err != nil {
		t.Fatalf("首次调用失败: %v", err)
	}
	if got := atomic.LoadInt32(&fg.loginCount); got != 1 {
		t.Fatalf("前置条件不成立：登录次数 = %d, want 1", got)
	}

	// 同一节点、同一 id，但密码换了。指纹随之改变，缓存条目必须失效。
	rotated := fg.secret(t)
	rotated.AdminPassword = "rotated-secret"

	body, err := client.do(context.Background(), rotated, http.MethodGet, "/api/providers", nil, nil)
	if err != nil {
		t.Fatalf("新密码应能登录成功: %v", err)
	}
	if got := atomic.LoadInt32(&fg.loginCount); got != 2 {
		t.Errorf("登录次数 = %d, want 2（凭据变更必须触发重登，而非复用旧 token）", got)
	}
	if !strings.Contains(string(body), "tok-2") {
		t.Errorf("应使用重登后的新 token，响应为 %s", body)
	}
}

// TestGatewayClientExpiredTokenTriggersRelogin 确认过期条目不会被复用。
func TestGatewayClientExpiredTokenTriggersRelogin(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")
	fg := newFakeGateway(t)
	client := newTestGatewayClient()
	secret := fg.secret(t)

	if _, err := client.do(context.Background(), secret, http.MethodGet, "/api/providers", nil, nil); err != nil {
		t.Fatalf("首次调用失败: %v", err)
	}

	// 手动把缓存条目改成已过期。
	client.mu.Lock()
	for _, tok := range client.tokens {
		tok.expiresAt = time.Now().Add(-time.Minute)
	}
	client.mu.Unlock()

	if _, err := client.do(context.Background(), secret, http.MethodGet, "/api/providers", nil, nil); err != nil {
		t.Fatalf("过期后调用失败: %v", err)
	}
	if got := atomic.LoadInt32(&fg.loginCount); got != 2 {
		t.Errorf("登录次数 = %d, want 2（过期应触发重登）", got)
	}
}

// TestGatewayClientPropagatesUpstreamStatus 确认上游 403 不被压成 502，
// 这样移动端能提示"该账号权限不足"而不是"网关不可用"。
func TestGatewayClientPropagatesUpstreamStatus(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")
	fg := newFakeGateway(t)
	client := newTestGatewayClient()

	_, err := client.do(context.Background(), fg.secret(t), http.MethodGet, "/api/routing/health", nil, nil)
	if err == nil {
		t.Fatal("期望上游 403 转为错误")
	}
	apiErr, ok := err.(*gatewayAPIError)
	if !ok {
		t.Fatalf("错误类型 = %T, want *gatewayAPIError", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Message, "super_admin") {
		t.Errorf("应保留上游错误信息，得到 %q", apiErr.Message)
	}
}

func TestGatewayClientRejectsMissingCredentials(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")
	fg := newFakeGateway(t)
	client := newTestGatewayClient()

	secret := fg.secret(t)
	secret.Node.AdminUsername = ""
	secret.AdminPassword = ""

	if _, err := client.do(context.Background(), secret, http.MethodGet, "/api/providers", nil, nil); err == nil {
		t.Error("未配置 admin 凭据时必须报错（legacy 导入的节点就是这个状态）")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP 层：鉴权 / 角色门禁 / 未装配降级
// ─────────────────────────────────────────────────────────────────────────────

// TestGatewayNodesRequireAuth 确认整个子树都在 requireAuth 之后。
func TestGatewayNodesRequireAuth(t *testing.T) {
	srv, _ := newTestServerWithAuth(t)

	for _, path := range []string{
		"/api/llm-gateway/nodes",
		"/api/llm-gateway/nodes/1",
		"/api/llm-gateway/nodes/1/providers",
		"/api/llm-gateway/nodes/1/live/event",
	} {
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s 无凭证时应返回 401，得到 %d", path, rr.Code)
		}
	}
}

// TestGatewayNodesUnconfiguredReturns503 覆盖无 PG / 缺 master key 的部署：
// 端点必须明确降级，而不是 panic 或 404。
func TestGatewayNodesUnconfiguredReturns503(t *testing.T) {
	srv, token := newTestServerWithAuth(t)
	// newTestServerWithAuth 不注入 gatewayNodes，正是待验证的状态。

	req := httptest.NewRequest(http.MethodGet, "/api/llm-gateway/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("未装配时应返回 503，得到 %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not configured") {
		t.Errorf("503 响应应说明原因，得到 %s", rr.Body.String())
	}
}

// TestRequireGatewayAdminRejectsNonAdmin 直接验证角色门禁：写操作要求
// pocket 侧 admin 角色，与 server_audit.go 的判定保持一致。
func TestRequireGatewayAdminRejectsNonAdmin(t *testing.T) {
	srv, _ := newTestServerWithAuth(t)

	cases := []struct {
		role     string
		wantPass bool
	}{
		{role: "admin", wantPass: true},
		{role: "member", wantPass: false},
		{role: "owner", wantPass: false}, // 刻意不放行：pocket 的写门禁只认 admin
		{role: "", wantPass: false},
	}

	for _, tc := range cases {
		t.Run("role="+tc.role, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/llm-gateway/nodes", nil)
			req = req.WithContext(context.WithValue(req.Context(), authClaimsContextKey{}, &authClaims{
				UserID: "u1", Role: tc.role, WorkspaceID: "ws-1",
			}))
			rr := httptest.NewRecorder()

			got := srv.requireGatewayAdmin(rr, req)
			if got != tc.wantPass {
				t.Errorf("requireGatewayAdmin(role=%q) = %v, want %v", tc.role, got, tc.wantPass)
			}
			if !tc.wantPass && rr.Code != http.StatusForbidden {
				t.Errorf("非 admin 应得到 403，得到 %d", rr.Code)
			}
		})
	}
}

// TestRequireGatewayAdminRejectsMissingClaims 覆盖 claims 缺失（理论上不可达，
// 因为 requireAuth 在前，但门禁自身不应依赖上游保证）。
func TestRequireGatewayAdminRejectsMissingClaims(t *testing.T) {
	srv, _ := newTestServerWithAuth(t)
	req := httptest.NewRequest(http.MethodPost, "/api/llm-gateway/nodes", nil)
	rr := httptest.NewRecorder()

	if srv.requireGatewayAdmin(rr, req) {
		t.Error("无 claims 时必须拒绝")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("无 claims 应得到 401，得到 %d", rr.Code)
	}
}

func TestClientIPFromRequest(t *testing.T) {
	cases := []struct {
		name string
		set  func(*http.Request)
		want string
	}{
		{
			name: "X-Forwarded-For 取第一跳",
			set:  func(r *http.Request) { r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1") },
			want: "203.0.113.7",
		},
		{
			name: "X-Real-IP 兜底",
			set:  func(r *http.Request) { r.Header.Set("X-Real-IP", "203.0.113.9") },
			want: "203.0.113.9",
		},
		{
			name: "无代理头则用 RemoteAddr",
			set:  func(r *http.Request) { r.RemoteAddr = "203.0.113.11:54321" },
			want: "203.0.113.11",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			tc.set(req)
			if got := clientIPFromRequest(req); got != tc.want {
				t.Errorf("clientIPFromRequest() = %q, want %q", got, tc.want)
			}
		})
	}
}
