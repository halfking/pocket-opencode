package registration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// ── 假注册中心 ──────────────────────────────────────────────────────────────
//
// 真注册中心在本轮够不着（不连 252/211），所以这里用一个只记录、不判断的
// 假服务器：它回答「收到了什么」，判断留给断言。
//
// 假服务器回答任何路径都带 code=0 的信封 —— SDK 的 do() 只在信封里 code≠0 时
// 才报错（见 client.go 的 do()），所以假服务器答得「过于成功」也没关系：
// 断言看的是**请求本身**，不是服务器的答复。

type fakeRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   string
}

type fakeRegistry struct {
	requests []fakeRequest
	server   *httptest.Server
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeRegistry) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.requests = append(f.requests, fakeRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.Query(),
		Body:   string(body),
	})

	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/nacos/v2/ns/instance/beat" {
		// 心跳响应里带 clientBeatInterval —— SDK 会采纳服务端给的节奏。
		io.WriteString(w, `{"code":0,"message":"ok","data":{"clientBeatInterval":5000,"code":0}}`)
		return
	}
	io.WriteString(w, `{"code":0,"message":"ok"}`)
}

func (f *fakeRegistry) all() []fakeRequest {
	out := make([]fakeRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

// registrations 是「注册」请求：POST /nacos/v2/ns/instance。
func (f *fakeRegistry) registrations() []fakeRequest {
	var out []fakeRequest
	for _, req := range f.all() {
		if req.Method == http.MethodPost && req.Path == "/nacos/v2/ns/instance" {
			out = append(out, req)
		}
	}
	return out
}

// deregistrations 是「注销」请求：DELETE /nacos/v2/ns/instance。
func (f *fakeRegistry) deregistrations() []fakeRequest {
	var out []fakeRequest
	for _, req := range f.all() {
		if req.Method == http.MethodDelete && req.Path == "/nacos/v2/ns/instance" {
			out = append(out, req)
		}
	}
	return out
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// envOf 造一个假的 getenv：只认 map 里有的键，其余一律空。
func envOf(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

// ── 解析层（纯函数，不碰网络）─────────────────────────────────────────────

func TestResolveKeepsRegistrationOffByDefault(t *testing.T) {
	// 开关的默认值是**关**。SDK 自身也是默认关（service.go 的 envBool 默认值），
	// 本包不改它 —— 「默认改开」是 D5，未获 owner 确认。
	r, err := resolve(envOf(nil), Options{ListenAddr: ":8088"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.cfg.Enabled {
		t.Fatalf("默认必须是关的，实际 Enabled=%v", r.cfg.Enabled)
	}
}

func TestResolveUsesTheApplicationModelServiceName(t *testing.T) {
	// 名字跟应用模型（openpocket/api 组件）与声明面（pocketd 容器）一致。
	r, err := resolve(envOf(map[string]string{"GOACOS_ENABLED": "true"}),
		Options{ListenAddr: ":8088"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.cfg.ServiceName != "openpocket-api" {
		t.Fatalf("serviceName = %q want openpocket-api", r.cfg.ServiceName)
	}
}

func TestResolveDefaultsTheNamespaceToPublic(t *testing.T) {
	// 与 reconcile 侧（GOACOS_NS 默认 public）同口径：goacos 1.1.1 以前
	// 缺省 namespaceId 会落成空串，同一个服务会被拆成两个 namespace。
	r, err := resolve(envOf(map[string]string{"GOACOS_ENABLED": "true"}),
		Options{ListenAddr: ":8088"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.cfg.NamespaceID != "public" {
		t.Fatalf("namespaceId = %q want public", r.cfg.NamespaceID)
	}
}

func TestResolveAdvertisesTheExplicitPort(t *testing.T) {
	// 对外端口只认 GOACOS_PORT（部署面用 `GOACOS_PORT: ${POCKET_HTTP_PORT:-8090}`
	// 把它注进来）。
	r, err := resolve(envOf(map[string]string{
		"GOACOS_ENABLED": "true",
		"GOACOS_PORT":    "8090",
	}), Options{ListenAddr: ":8088"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.cfg.Port != 8090 {
		t.Fatalf("port = %d want 8090", r.cfg.Port)
	}
	if r.portSource != portSourceEnv {
		t.Fatalf("port_source = %q want %q", r.portSource, portSourceEnv)
	}
}

func TestResolveFallsBackToTheListenPort(t *testing.T) {
	// 没给 GOACOS_PORT 时退回监听端口（兜底，调用方会告警）。
	r, err := resolve(envOf(map[string]string{"GOACOS_ENABLED": "true"}),
		Options{ListenAddr: ":8088"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.cfg.Port != 8088 {
		t.Fatalf("port = %d want 8088（监听端口兜底）", r.cfg.Port)
	}
	if r.portSource != portSourceListen {
		t.Fatalf("port_source = %q want %q", r.portSource, portSourceListen)
	}
}

func TestResolveNeverUsesPocketHttpPortAsTheAdvertisedPort(t *testing.T) {
	// ⚠ **本服务特有的坑**：compose 把容器内的 POCKET_HTTP_PORT 钉成 8088
	//   （内部监听端口），而宿主发布出去的是 8090 / 8091 ⇒ 拿它发布出来的端口
	//   跨机拨不通。代码**不许**读 POCKET_HTTP_PORT 当对外端口。
	//
	// 这条用一个**故意不同**的值来证伪：监听端口 8088，环境变量 9999
	// ⇒ 发布出去的端口必须是 8088（监听端口兜底），绝不能是 9999。
	r, err := resolve(envOf(map[string]string{
		"GOACOS_ENABLED":   "true",
		"POCKET_HTTP_PORT": "9999",
	}), Options{ListenAddr: ":8088"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.cfg.Port == 9999 {
		t.Fatalf("对外端口用了 POCKET_HTTP_PORT（9999）—— 那是容器内端口，" +
			"不是宿主发布端口")
	}
	if r.cfg.Port != 8088 {
		t.Fatalf("port = %d want 8088（监听端口兜底）", r.cfg.Port)
	}
}

func TestResolveRefusesAnAutoDetectedAddress(t *testing.T) {
	// 开了注册但没给 GOACOS_IP ⇒ 地址为空。
	// SDK 自己会探测一个（容器里是容器网段地址），本包必须把它清掉：
	// 发布一个跨机拨不通的实例比不发布更糟。
	r, err := resolve(envOf(map[string]string{"GOACOS_ENABLED": "true"}),
		Options{ListenAddr: ":8088"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.cfg.IP != "" {
		t.Fatalf("IP = %q want 空（不许用自动探测出来的地址）", r.cfg.IP)
	}
	if r.ipExplicit {
		t.Fatalf("ip_explicit = true want false")
	}
}

func TestResolveKeepsAnExplicitAddress(t *testing.T) {
	r, err := resolve(envOf(map[string]string{
		"GOACOS_ENABLED": "true",
		"GOACOS_IP":      "100.106.175.168",
	}), Options{ListenAddr: ":8088"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.cfg.IP != "100.106.175.168" {
		t.Fatalf("IP = %q want 100.106.175.168", r.cfg.IP)
	}
	if !r.ipExplicit {
		t.Fatalf("ip_explicit = false want true")
	}
}

func TestResolveRejectsAnUnparseablePort(t *testing.T) {
	// 操作员打错 ⇒ 报出来（但注册失败不挡服务，那由 Announce 处理）。
	_, err := resolve(envOf(map[string]string{
		"GOACOS_ENABLED": "true",
		"GOACOS_PORT":    "not-a-port",
	}), Options{ListenAddr: ":8088"})
	if err == nil {
		t.Fatalf("GOACOS_PORT 写坏了必须报错")
	}
}

// ── 接线层（对着假注册中心跑）─────────────────────────────────────────────

func TestAnnounceRegistersTheAdvertisedEndpoint(t *testing.T) {
	reg := newFakeRegistry(t)
	t.Setenv("GOACOS_ENABLED", "true")
	t.Setenv("GOACOS_SERVER_ADDR", reg.server.URL)
	t.Setenv("GOACOS_IP", "100.106.175.168")
	t.Setenv("GOACOS_PORT", "8090")

	stop := Announce(context.Background(), Options{
		ListenAddr: ":8088",
		Logger:     quietLogger(),
	})
	defer stop()

	got := reg.registrations()
	if len(got) != 1 {
		t.Fatalf("注册请求数 = %d want 1（%v）", len(got), reg.all())
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got[0].Body), &body); err != nil {
		t.Fatalf("解码注册请求体: %v", err)
	}
	if body["serviceName"] != "openpocket-api" {
		t.Errorf("serviceName = %v want openpocket-api", body["serviceName"])
	}
	if body["ip"] != "100.106.175.168" {
		t.Errorf("ip = %v want 100.106.175.168", body["ip"])
	}
	if port, ok := body["port"].(float64); !ok || int(port) != 8090 {
		t.Errorf("port = %v want 8090（宿主发布端口，不是容器内 8088）", body["port"])
	}
	if body["namespaceId"] != "public" {
		t.Errorf("namespaceId = %v want public", body["namespaceId"])
	}
}

func TestAnnounceSendsNothingWhenRegistrationIsOff(t *testing.T) {
	// 关着的时候一个请求都不该有 —— 连「探一下注册中心在不在」都不该有。
	reg := newFakeRegistry(t)
	t.Setenv("GOACOS_SERVER_ADDR", reg.server.URL)

	stop := Announce(context.Background(), Options{
		ListenAddr: ":8088",
		Logger:     quietLogger(),
	})
	defer stop()

	if reqs := reg.all(); len(reqs) != 0 {
		t.Fatalf("注册关闭时不该有任何请求，实际收到 %d 个：%v", len(reqs), reqs)
	}
}

func TestAnnounceSendsNothingWithoutAnExplicitAddress(t *testing.T) {
	// 开了开关但没给地址 ⇒ 不注册。一个拨不通的实例比没有实例更糟：
	// 网关会挑中它，然后 502。
	reg := newFakeRegistry(t)
	t.Setenv("GOACOS_ENABLED", "true")
	t.Setenv("GOACOS_SERVER_ADDR", reg.server.URL)

	stop := Announce(context.Background(), Options{
		ListenAddr: ":8088",
		Logger:     quietLogger(),
	})
	defer stop()

	if reqs := reg.all(); len(reqs) != 0 {
		t.Fatalf("没有显式地址时不该有任何请求，实际收到 %d 个：%v", len(reqs), reqs)
	}
}

func TestStopDeregistersTheInstance(t *testing.T) {
	// 干净停掉的服务不能在注册面里留着 —— 网关会继续往它上面打。
	reg := newFakeRegistry(t)
	t.Setenv("GOACOS_ENABLED", "true")
	t.Setenv("GOACOS_SERVER_ADDR", reg.server.URL)
	t.Setenv("GOACOS_IP", "100.106.175.168")
	t.Setenv("GOACOS_PORT", "8090")

	stop := Announce(context.Background(), Options{
		ListenAddr: ":8088",
		Logger:     quietLogger(),
	})
	stop()

	gone := reg.deregistrations()
	if len(gone) != 1 {
		t.Fatalf("注销请求数 = %d want 1（%v）", len(gone), reg.all())
	}
	q := gone[0].Query
	if q.Get("serviceName") != "openpocket-api" {
		t.Errorf("注销的 serviceName = %q want openpocket-api", q.Get("serviceName"))
	}
	if q.Get("ip") != "100.106.175.168" {
		t.Errorf("注销的 ip = %q want 100.106.175.168", q.Get("ip"))
	}
	if q.Get("port") != "8090" {
		t.Errorf("注销的 port = %q want 8090", q.Get("port"))
	}
}

func TestStopIsSafeToCallTwice(t *testing.T) {
	// 注销函数会被 defer 调用，也可能在错误路径上再调一次 ⇒ 必须幂等。
	reg := newFakeRegistry(t)
	t.Setenv("GOACOS_ENABLED", "true")
	t.Setenv("GOACOS_SERVER_ADDR", reg.server.URL)
	t.Setenv("GOACOS_IP", "100.106.175.168")

	stop := Announce(context.Background(), Options{
		ListenAddr: ":8088",
		Logger:     quietLogger(),
	})
	stop()
	stop()

	if gone := reg.deregistrations(); len(gone) != 1 {
		t.Fatalf("注销请求数 = %d want 1（重复调用不该重复注销）", len(gone))
	}
}

func TestAnnounceSurvivesAnUnreachableRegistry(t *testing.T) {
	// 注册中心不可达 ⇒ 服务照常起来。这是 SDK 的契约：
	// Announce 永不返回错误，注册失败不挡服务。
	t.Setenv("GOACOS_ENABLED", "true")
	t.Setenv("GOACOS_SERVER_ADDR", "http://127.0.0.1:1")
	t.Setenv("GOACOS_IP", "100.106.175.168")

	stop := Announce(context.Background(), Options{
		ListenAddr: ":8088",
		Logger:     quietLogger(),
	})
	if stop == nil {
		t.Fatalf("注销函数不许是 nil")
	}
	stop()
}
