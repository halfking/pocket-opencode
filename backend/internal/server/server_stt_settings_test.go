package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/stt"
	"github.com/halfking/pocket-opencode/backend/internal/usersetting"
)

// errFakeASRNoNetwork 是 noNetworkClient 统一返回的错误。
//
// 为什么必须是包级变量而不是内联 errors.New：单测里有多处要断言
// 「出网被拒」这个行为，共享同一哨兵值才能用 errors.Is 判定，
// 内联新建的 error 值无法比较。
var errFakeASRNoNetwork = errors.New("fake asr: network disabled in tests")

// noNetworkClient 让任何出网请求立刻失败：单测绝不能真打 llm.kxpms.cn。
func noNetworkClient() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errFakeASRNoNetwork
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestSttSettingsWorkWithoutPGStore 锁住「无 PG 也能保存 STT 设置」。
//
// 2026-10-01 黑盒验证实测的真实 bug：pocketd 未配 POCKET_POSTGRES_DSN 时
// 正常启动（remote-only 模式），但 s.userSettings 为 nil，
// saveSTTSettings 直接返回 "user settings store unavailable"
// → PUT /api/stt/config 恒 400。
//
// 后果不是「设置存不下来」这么轻：**整个 STT 功能对用户不可用**——
// 连试转、连手工填 key 的通道都进不去，因为根本没有地方存。
// 上一轮 handoff 曾声称已修（加了 sttMemSettings），但那份实现并未落在代码里。
//
// 这条测试是「设置页能不能用」的底线：它绿，STT 至少是可配置的。
func TestSttSettingsWorkWithoutPGStore(t *testing.T) {
	srv, _ := newWorkspaceIsolationServer(t)
	// 刻意不设置 srv.userSettings —— 模拟无 PG 部署
	if srv.userSettings != nil {
		t.Fatal("前置条件不成立：测试服务器本应没有 userSettings")
	}
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "1")

	h := srv.Handler()
	token := wsAToken(t)

	body := strings.NewReader(`{"channel":"external","externalBaseURL":"http://127.0.0.1:9/v1",` +
		`"externalModel":"gpt-4o-mini-transcribe","externalTransport":"transcriptions",` +
		`"externalApiKey":"k-abc"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/stt/config", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("无 PG 部署下保存 STT 设置应成功，实际 %d: %s", rr.Code, rr.Body.String())
	}

	// 读回来：设置必须真的存住了，且 key 只能以 hasExternalKey 形式存在
	get := httptest.NewRequest(http.MethodGet, "/api/stt/config", nil)
	get.Header.Set("Authorization", "Bearer "+token)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, get)
	var cfg sttConfigResponse
	if err := json.Unmarshal(rr2.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Settings.ExternalModel != "gpt-4o-mini-transcribe" {
		t.Errorf("设置未回读到，externalModel=%q", cfg.Settings.ExternalModel)
	}
	if !cfg.Settings.HasExternalKey {
		t.Error("hasExternalKey 应为 true（key 已保存）")
	}
	if strings.Contains(rr2.Body.String(), "k-abc") {
		t.Errorf("GET 响应泄露明文 key: %s", rr2.Body.String())
	}
}

// installFakeASR 把 STT 出网指向一个假的 OpenAI 兼容上游，让 /api/stt/transcribe
// 与 /api/stt/probe 能真的走完「配置解析 → 目标解析 → 发出请求 → 拿回文本」这条链路。
//
// 为什么不直接换掉 s.transcriber：换掉会让 handler 绕过设置解析（目标从哪来、
// 通道怎么选、SSRF 校验走没走），测不到真正该测的东西。保持 resolver 真实、
// 只把最后一跳的 HTTP 换成 httptest，才是端到端。
func installFakeASR(t *testing.T, srv *Server, text string) {
	t.Helper()
	// httptest 监听 127.0.0.1，而 validateGatewayURL 默认拒绝私网/loopback
	// （防 SSRF）。这里显式 opt-in 放行——否则测试永远在保存设置那一步就被
	// 拒掉，测不到后面真正要测的转写链路。
	// 用 t.Setenv 而非 os.Setenv：它会在测试结束后自动还原，不污染同包其他用例。
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "1")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"` + text + `"}`))
	}))
	t.Cleanup(upstream.Close)

	// 存一条指向假上游的 external 设置，让目标解析走真实路径。
	// key 是独立参数（不进 payload）——外部 ASR 的 key 与对话模型配置刻意分开。
	//
	// 作用域必须与 wsAToken() 签出来的身份一致（shared-user / ws-a）：
	// 设置按 (userID, workspaceID) 存取，作用域写错的话 handler 读到的是空设置，
	// 症状是「配了 key 却报未配置」——与真实的 key 丢失故障无法区分。
	if err := srv.saveSTTSettings("shared-user", "ws-a", sttSettingsPayload{
		Channel:         stt.ChannelExternal,
		ExternalBaseURL: upstream.URL + "/v1",
		ExternalModel:   "gpt-4o-mini-transcribe",
	}, "test-key"); err != nil {
		t.Fatalf("保存假 ASR 设置失败: %v", err)
	}
	srv.SetSTTHTTPClient(upstream.Client())
}

// sttTestServer 起一个自足的 server：内存用户设置 + 拒绝出网的 STT 客户端。
//
// gatewayKey 通过 POCKET_LLM_GATEWAY_API_KEY 注入。2026-10-01 起内置默认网关
// key 已被移除（opencode/config_writer.go：那是一把会计费的真实密钥被写进了
// 源码），所以**测试不能假设网关自带 key**，必须显式给。传 "" 即「网关未配置」，
// 这本身也是一个要锁的行为。
func sttTestServer(t *testing.T, gatewayKey string) (*Server, *memUserSettings) {
	t.Helper()
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", gatewayKey)
	srv, _ := newWorkspaceIsolationServer(t)
	store := newMemUserSettings()
	srv.userSettings = store
	srv.SetSTTHTTPClient(noNetworkClient())
	srv.sttDiscovery = stt.NewDiscoveryCache(0)
	return srv, store
}

// wsAToken 用与 newWorkspaceIsolationServer 相同的签名密钥重新签一个 ws-a 的 token。
func wsAToken(t *testing.T) string {
	t.Helper()
	signer, err := auth.NewSigner("workspace-isolation-test-secret-012345", time.Hour)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	token, err := signer.SignWithWorkspace("shared-user", "member", "ws-a")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

// memUserSettings 是 usersetting.Repository 的内存实现。
type memUserSettings struct {
	recs map[string]usersetting.Record
}

func newMemUserSettings() *memUserSettings {
	return &memUserSettings{recs: map[string]usersetting.Record{}}
}

func memKey(userID, wsID, ns, id string) string {
	return userID + "|" + wsID + "|" + ns + "|" + id
}

func (m *memUserSettings) List(userID, wsID string) ([]usersetting.Record, error) {
	out := []usersetting.Record{}
	for k, v := range m.recs {
		if strings.HasPrefix(k, userID+"|"+wsID+"|") {
			out = append(out, v)
		}
	}
	return out, nil
}

func (m *memUserSettings) Get(userID, wsID, ns, id string) (*usersetting.Record, error) {
	rec, ok := m.recs[memKey(userID, wsID, ns, id)]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

func (m *memUserSettings) Put(rec usersetting.Record) (*usersetting.PutResult, error) {
	k := memKey(rec.UserID, rec.WorkspaceID, rec.Namespace, rec.ID)
	if old, ok := m.recs[k]; ok && rec.Secret == "" {
		rec.Secret = old.Secret
	}
	m.recs[k] = rec
	return &usersetting.PutResult{Applied: true, Record: rec}, nil
}

func seedSTTSetting(userID, wsID, payload, secret string) usersetting.Record {
	return usersetting.Record{
		UserID: userID, WorkspaceID: wsID, Namespace: "stt", ID: "default",
		Payload: json.RawMessage(payload), Secret: secret, UpdatedAt: 1,
	}
}

// TestSttConfigListsBothRecommendedGroups 设置页必须同时拿到网关组与外部组预置，
// 且外部组要带地址与成本依据（否则用户填了 key 也不知道打哪、花多少）。
func TestSttConfigListsBothRecommendedGroups(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	h := srv.Handler()
	rr := serveWorkspaceJSON(t, h, http.MethodGet, "/api/stt/config", wsAToken(t), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/stt/config status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp sttConfigResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	var gw, ext int
	for _, m := range resp.Recommended {
		switch m.Group {
		case "gateway":
			gw++
		case "external":
			ext++
		}
		if m.USDPerHour < 0 {
			t.Errorf("负报价：%+v", m)
		}
	}
	// 只守下限不写死上限：用户要求「尽可能费用少」，外部候选会随调研增补
	// （2026-10-01 从 3 个扩到 7 个）。写死上限会诱导后来者不去补候选。
	if gw != 3 || ext < 7 {
		t.Fatalf("推荐模型应为网关 3 + 外部 ≥7，实际 gateway=%d external=%d", gw, ext)
	}
	// 每个外部候选都必须带地址：用户只填一把 key，地址得由推荐给出。
	for _, m := range resp.Recommended {
		if m.Group == "external" && m.BaseURL == "" {
			t.Errorf("外部候选 %s 缺 BaseURL", m.Model)
		}
	}
	if resp.Settings.Channel != "" && resp.Settings.Channel != stt.ChannelAuto {
		t.Errorf("未配置时通道应是 auto，实际 %q", resp.Settings.Channel)
	}
	// 通道说明必须存在，否则用户看不懂三个选项的区别。
	for _, ch := range []string{stt.ChannelAuto, stt.ChannelGateway, stt.ChannelExternal} {
		if resp.ChannelHints[ch] == "" {
			t.Errorf("通道 %s 缺说明", ch)
		}
	}
}

func TestSttConfigNeverLeaksExternalKey(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	h := srv.Handler()
	token := wsAToken(t)
	body := `{"channel":"external","externalBaseURL":"https://api.openai.com/v1",` +
		`"externalModel":"gpt-4o-mini-transcribe","externalApiKey":"sk-super-secret"}`
	put := serveWorkspaceJSON(t, h, http.MethodPut, "/api/stt/config", token, body)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", put.Code, put.Body.String())
	}
	if strings.Contains(put.Body.String(), "sk-super-secret") {
		t.Fatalf("PUT 响应回显了 key：%s", put.Body.String())
	}
	get := serveWorkspaceJSON(t, h, http.MethodGet, "/api/stt/config", token, "")
	if strings.Contains(get.Body.String(), "sk-super-secret") {
		t.Fatalf("GET 响应泄露了 key：%s", get.Body.String())
	}
	var view sttConfigResponse
	if err := json.Unmarshal(get.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !view.Settings.HasExternalKey {
		t.Error("HasExternalKey 应为 true（key 存了但不该回显）")
	}
	if view.Settings.ExternalModel != "gpt-4o-mini-transcribe" || view.Settings.Channel != stt.ChannelExternal {
		t.Errorf("设置没存上：%+v", view.Settings)
	}
}

func TestSttConfigRejectsDangerousExternalURL(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	h := srv.Handler()
	token := wsAToken(t)
	for _, bad := range []string{
		`{"externalBaseURL":"http://127.0.0.1:8080/v1"}`,
		`{"externalBaseURL":"http://169.254.169.254/v1"}`,
		`{"externalBaseURL":"ftp://example.com/v1"}`,
	} {
		rr := serveWorkspaceJSON(t, h, http.MethodPut, "/api/stt/config", token, bad)
		if rr.Code == http.StatusOK {
			t.Errorf("应拒绝危险地址 %s，实际 %s", bad, rr.Body.String())
		}
	}
}

// TestResolveTargetAutoFallsBackToExternal 网关没有可用 ASR（2026-10-01 实测事实）时，
// auto 通道必须落到外部服务，而不是直接失败。
func TestResolveTargetAutoFallsBackToExternal(t *testing.T) {
	srv, store := sttTestServer(t, "")
	store.Put(seedSTTSetting("shared-user", "ws-a",
		`{"channel":"auto","externalBaseURL":"https://api.openai.com/v1","externalModel":"gpt-4o-mini-transcribe"}`,
		"sk-test"))
	target, err := srv.resolveSTTTarget(context.Background(), stt.Scope{UserID: "shared-user", WorkspaceID: "ws-a"})
	if err != nil {
		t.Fatalf("auto 通道应回退到外部服务：%v", err)
	}
	if target.Channel != stt.ChannelExternal || target.Model != "gpt-4o-mini-transcribe" {
		t.Fatalf("目标不对：%+v", target)
	}
	if target.APIKey != "sk-test" {
		t.Errorf("key 没接上：%q", target.APIKey)
	}
	if target.CostUSDPerHour != 0.18 {
		t.Errorf("成本没带上：%v", target.CostUSDPerHour)
	}
}

// TestResolveTargetAutoReportsBothReasons 两条通道都不通时，错误必须同时说清
// 「网关为什么不行」和「外部为什么不行」，否则用户没法行动。
func TestResolveTargetAutoReportsBothReasons(t *testing.T) {
	// 网关通道必须真的有一把 key 才会走到候选筛选，否则在「网关未配置 API Key」
	// 就短路了，根本不会去比对各候选的探测结论——那正是本测试要验的东西。
	// 2026-10-01 起租户 key 只从 env 注入（不再有仓库内置默认 key），
	// 所以 key 通过 sttTestServer 的参数传，不要在外面再 t.Setenv 一次
	// （那会被 helper 里的第二个 t.Setenv 覆盖掉）。
	srv, store := sttTestServer(t, "sk-gateway-test")
	store.Put(seedSTTSetting("shared-user", "ws-a", `{"channel":"auto"}`, ""))
	gw := srv.ResolveGatewayForUser("shared-user", "ws-a")
	srv.sttDiscovery.Seed(gw.BaseURL, gw.APIKey, stt.DiscoveryResult{
		BaseURL: gw.BaseURL, TotalModels: 604,
		Candidates: []stt.Candidate{
			{Model: "gpt-audio", Status: stt.ProbeNoProvider, Detail: "No available provider"},
			{Model: "mimo-v2.5-asr", Status: stt.ProbeNoProvider, Detail: "No available provider"},
		},
	})
	_, err := srv.resolveSTTTarget(context.Background(), stt.Scope{UserID: "shared-user", WorkspaceID: "ws-a"})
	if err == nil {
		t.Fatal("两条通道都不通时应报错")
	}
	msg := err.Error()
	// 「无上游 provider」是这条测试真正要守的东西：网关侧失败原因必须点名
	// 探测结论，不能只说「不可用」。断言按 summarizeGateway 的实际措辞匹配
	// （用户界面是中文，不该在错误里塞英文 detail）。
	for _, want := range []string{"网关", "外部", "gpt-audio", "无上游 provider"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息缺 %q：%s", want, msg)
		}
	}
}

func TestResolveTargetGatewayChannelDoesNotSilentlySwitch(t *testing.T) {
	// 同上：网关要有 key，才谈得上「选定的模型不可用时不能静默换模型」。
	srv, store := sttTestServer(t, "sk-gateway-test")
	store.Put(seedSTTSetting("shared-user", "ws-a",
		`{"channel":"gateway","gatewayModel":"gpt-audio"}`, "sk-test"))
	gw := srv.ResolveGatewayForUser("shared-user", "ws-a")
	srv.sttDiscovery.Seed(gw.BaseURL, gw.APIKey, stt.DiscoveryResult{
		BaseURL: gw.BaseURL, TotalModels: 604,
		Candidates: []stt.Candidate{{Model: "gpt-audio", Status: stt.ProbeNoProvider}},
	})
	_, err := srv.resolveSTTTarget(context.Background(), stt.Scope{UserID: "shared-user", WorkspaceID: "ws-a"})
	if err == nil {
		t.Fatal("手工指定的模型不可用时必须报错，不能静默换模型")
	}
	if !strings.Contains(err.Error(), "gpt-audio") {
		t.Errorf("错误信息应点名用户选的模型：%s", err.Error())
	}
}

// TestTranscribeWithoutConfigIsActionable 没配任何 ASR 时，转写必须给出可行动的错误，
// 而不是把占位文字当转写结果写进会议记录。
func TestTranscribeWithoutConfigIsActionable(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	h := srv.Handler()
	// 必须发合法 multipart：直接 POST 裸字符串会在解析阶段就 400
	// 「invalid body」，那样根本走不到「未配置 ASR」这条业务分支，
	// 测的就不是本测试想测的东西了。
	body := &strings.Builder{}
	body.WriteString("--X\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.wav\"\r\n")
	body.WriteString("Content-Type: audio/wav\r\n\r\nRIFFfake\r\n--X--\r\n")
	req := httptest.NewRequest(http.MethodPost, "/api/stt/transcribe", strings.NewReader(body.String()))
	req.Header.Set("Authorization", "Bearer "+wsAToken(t))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=X")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502 body=%s", rr.Code, rr.Body.String())
	}
	respBody := rr.Body.String()
	if !strings.Contains(respBody, "stt_unavailable") {
		t.Errorf("错误应带 stt_unavailable 码供前端映射：%s", respBody)
	}
	if !strings.Contains(respBody, "语音转写") {
		t.Errorf("错误应指向设置入口：%s", respBody)
	}
	if strings.Contains(respBody, "（STT") {
		t.Errorf("不该再返回占位转写文本：%s", respBody)
	}
}

// TestSttProbeUsesRecordedAudio 设置页的「试转」走真实录音路径，
// 返回文本并带上模型与通道，便于用户判断是不是选对了模型。
func TestSttProbeUsesRecordedAudio(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	installFakeASR(t, srv, "试转结果：这段录音的中文内容。")
	h := srv.Handler()
	token := wsAToken(t)

	body := &strings.Builder{}
	body.WriteString("--X\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.wav\"\r\n")
	body.WriteString("Content-Type: audio/wav\r\n\r\nRIFFfake\r\n--X--\r\n")
	req := httptest.NewRequest(http.MethodPost, "/api/stt/probe", strings.NewReader(body.String()))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=X")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("probe status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		OK    bool   `json:"ok"`
		Text  string `json:"text"`
		Model string `json:"model"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if !out.OK || !strings.Contains(out.Text, "试转结果") {
		t.Fatalf("试转没返回转写文本：%+v", out)
	}
	if out.Model == "" {
		t.Error("试转响应应带上模型名")
	}
}

// TestSttProbeRejectsAudioDroppedByGateway 复刻最危险的上游行为：
// 200 + 「您似乎没有附上录音文件」。这种响应必须判失败。
func TestSttProbeRejectsAudioDroppedByGateway(t *testing.T) {
	srv, _ := sttTestServer(t, "")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w,
			`{"choices":[{"message":{"content":"您似乎没有附上录音文件，请重新上传。"}}],"usage":{"total_characters":0}}`)
	}))
	defer upstream.Close()
	srv.SetSTTHTTPClient(upstream.Client())
	engine := stt.NewResolver(func(context.Context, stt.Scope) (*stt.Target, error) {
		return &stt.Target{BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "auto",
			Transport: stt.TransportChatAudio, Channel: stt.ChannelGateway}, nil
	})
	engine.SetHTTPClient(upstream.Client())
	_, err := engine.TranscribeFor(context.Background(), stt.Scope{}, stt.ToneWAV(8000, 200), "a.wav")
	if err == nil {
		t.Fatal("丢音频的响应必须判失败")
	}
	if !strings.Contains(err.Error(), "dropped the audio") &&
		!strings.Contains(err.Error(), "no audio") &&
		!strings.Contains(err.Error(), "没有附上") {
		t.Errorf("错误应说明「上游把音频丢了」：%v", err)
	}
}

// TestSttDiscoverReportsGatewayTruth 「重新扫描」必须回报网关的真实状态，
// 而不是把 no_candidate 说成可用。
func TestSttDiscoverReportsGatewayTruth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = io.WriteString(w,
				`{"data":[{"id":"gpt-audio","modality":"audio"},{"id":"deepseek-v4-pro","modality":"text"}]}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w,
				`{"error":{"code":"no_candidate","message":"No available provider"}}`)
		}
	}))
	defer upstream.Close()
	cache := stt.NewDiscoveryCache(0)
	res, err := stt.Discover(context.Background(), upstream.Client(), cache, upstream.URL+"/v1", "key", true)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.UsableCandidates()) != 0 {
		t.Fatalf("no_candidate 的模型不能算可用：%+v", res.UsableCandidates())
	}
	if res.TotalModels != 2 {
		t.Errorf("totalModels=%d want 2", res.TotalModels)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Model != "gpt-audio" {
		t.Fatalf("纯文本模型不该进候选：%+v", res.Candidates)
	}
	if res.Candidates[0].Status != stt.ProbeNoProvider {
		t.Errorf("status=%s want %s", res.Candidates[0].Status, stt.ProbeNoProvider)
	}
}
