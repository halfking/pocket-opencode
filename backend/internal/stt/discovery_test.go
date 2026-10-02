package stt

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
)

// fakeGateway 造一个可控的假网关：模型目录 + 两种转写端点的行为都可编排。
type fakeGateway struct {
	models         []GatewayModel
	transcriptions map[string]int    // model -> status
	transcribeText map[string]string // model -> text
	chatStatus     map[string]int
	chatText       map[string]string
	chatChars      int
	lastAuth       string
}

func (f *fakeGateway) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		f.lastAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": f.models})
	})
	mux.HandleFunc("/v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := f.transcriptions["*"]; ok {
			status, text := f.transcriptions["*"], f.transcribeText["*"]
			writeJSONStatus(w, status, text)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		model := multipartModel(string(body))
		status, ok := f.transcriptions[model]
		if !ok {
			status = http.StatusNotFound
		}
		writeJSONStatus(w, status, f.transcribeText[model])
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&payload)
		status, ok := f.chatStatus[payload.Model]
		if !ok {
			status = http.StatusNotFound
		}
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			// 与真实网关一致：错误体是 {"error":{"code":"no_candidate",...}}，
			// 不能返回 choices —— 否则探测层会误判成「有响应但内容为空」。
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"no_candidate","message":"No available provider for model '` + payload.Model + `'"}}`))
			return
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": f.chatText[payload.Model]},
			}},
			"usage": map[string]any{"total_characters": f.chatChars},
		})
	})
	return mux
}

func writeJSONStatus(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "application/json")
	if status != http.StatusOK {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"code":"no_candidate","message":"No available provider"}}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"text": text})
}

// multipartModel 从 multipart 体里抠出 model 字段（探测用）。
func multipartModel(body string) string { return multipartField(body, "model") }

// multipartField 从 multipart 体里抠出任意一个普通字段的值。
//
// 注意它只认「Content-Disposition 行在值之前一行」的简单布局——真实解析要用
// mime/multipart，这里为了断言足够、且避免把 WAV 二进制误当字段。
func multipartField(body, name string) string {
	for _, part := range strings.Split(body, "\r\n") {
		if strings.HasPrefix(part, "Content-Disposition") && strings.Contains(part, `name="`+name+`"`) {
			idx := strings.Index(body, part)
			rest := body[idx+len(part):]
			rest = strings.TrimPrefix(rest, "\r\n\r\n")
			if nl := strings.Index(rest, "\r\n"); nl >= 0 {
				return rest[:nl]
			}
		}
	}
	return ""
}

func TestIsASRCandidate(t *testing.T) {
	cases := []struct {
		model GatewayModel
		want  bool
		why   string
	}{
		{GatewayModel{ID: "gpt-audio", Modality: "audio"}, true, "modality=audio 直接命中"},
		{GatewayModel{ID: "gpt-audio-mini", Modality: "audio"}, true, "modality=audio 直接命中"},
		// 网关把纯 ASR 模型错标成 text，只看 modality 会漏掉它。
		{GatewayModel{ID: "mimo-v2.5-asr", Modality: "text"}, true, "名字命中 asr"},
		{GatewayModel{ID: "whisper-large-v3"}, true, "名字命中 whisper"},
		{GatewayModel{ID: "gpt-4o-transcribe"}, true, "名字命中 transcri"},
		{GatewayModel{ID: "nemotron-3-nano-omni-30b-a3b-reasoning"}, true, "名字命中 omni"},
		{GatewayModel{ID: "deepseek-v4-pro", Modality: "text"}, false, "纯文本模型不该被探测"},
		{GatewayModel{ID: "text-embedding-3-small", Modality: "embedding"}, false, "嵌入模型不是 ASR"},
		// ── 2026-10-01 真机审计新增 ──
		// asrNameRe 里的 `voice` 会把语音**合成**模型拉进 ASR 候选。
		// 这两个是 llm.kxpms.cn 上真实存在的模型名（真机录音失败文案里出现过），
		// 命中 `voice` 但它们做的是 text-to-speech，转写它们永远失败。
		//
		// Modality 用 **audio** 而不是 text：2026-10-01 16:5x 复核 /v1/models
		// 发现这两个的 modality 已从 text 变成 audio。这不是小事——一旦是 audio，
		// 即使 asrNameRe 也不匹配，IsASRCandidate 也会走 modality==audio 分支
		// 直接放行，**只有 ttsNameRe 拦得住**。测试数据必须对齐真实目录，
		// 否则测试守着的是一个网关已经不再返回的形态。
		//
		// 负控的诚实说明：这两条**不能**证明 ttsNameRe 里的
		// `voice-?clone|voice-?design` 分支有效——名字里的 `tts` 同样会命中，
		// 删掉 voice-* 那几个分支它们照样是 false。它们防的是「ttsNameRe 整体
		// 被摘掉」。真正单独守护 `tts` 这个分支的是下面那条裸 mimo-v2.5-tts。
		{GatewayModel{ID: "mimo-v2.5-tts-voiceclone", Modality: "audio"}, false,
			"语音合成（音色克隆）不是转写，modality=audio 也不能放行"},
		{GatewayModel{ID: "mimo-v2.5-tts-voicedesign", Modality: "audio"}, false,
			"语音合成（音色设计）同上"},
		// 裸的 mimo-v2.5-tts（不带 voiceclone/voicedesign 后缀）同样在目录里。
		// 刻意**不**在这里加它的断言：负控对照实测过，把 ttsNameRe 里的 `tts`
		// 整个去掉，这条断言照样通过——因为 asrNameRe 本来也不匹配它。
		// 也就是说裸 tts 真正被 ttsNameRe 挡住的前提是**它同时命中某个弱 ASR
		// 标记**（如 voice / audio），而这类名字 -voiceclone、-voicedesign 已经覆盖。
		// 加一条恒绿的断言只会制造「有测试保护」的错觉。
		{GatewayModel{ID: "mimo-v2.5-tts", Modality: "audio"}, false,
			"裸 TTS 合成模型即使用 modality=audio 也不该当转写候选"},
		// fish-speech 是真实存在的开源 TTS 家族（CoTTS），且名字里**没有 tts**。
		// 它是当前用例表里唯一能单独守护 ttsNameRe 非 tts 分支的条目：删掉
		// `fish-speech` 分支后，modality=audio 会立刻把它放行并让测试转红。
		// 负控实测过：只删 `voice-?clone|voice-?design|voiceclon|voicedesign`
		// 这几个分支，用例表仍全绿——说明它们当时是**没有保护**的。
		{GatewayModel{ID: "fish-speech", Modality: "audio"}, false,
			"不含 tts 字样的合成模型也必须被排除，否则 modality=audio 会放行它"},
		// TTS 词 + 强 ASR 标记的混合命名不能被误杀。
		{GatewayModel{ID: "whisper-tts-hybrid", Modality: "text"}, true,
			"带 whisper 强标记，即使名字含 tts 也不排除"},
	}
	for _, c := range cases {
		if got := IsASRCandidate(c.model); got != c.want {
			t.Errorf("IsASRCandidate(%s/%s)=%v want %v（%s）",
				c.model.ID, c.model.Modality, got, c.want, c.why)
		}
	}
}

// TestDiscoverClassifiesNoProvider 复刻 2026-10-01 对 llm.kxpms.cn 的实测结论：
// 模型目录里有 gpt-audio / mimo-v2.5-asr，但上游全都没有 provider。
func TestDiscoverClassifiesNoProvider(t *testing.T) {
	f := &fakeGateway{
		models: []GatewayModel{
			{ID: "deepseek-v4-pro", Modality: "text"},
			{ID: "gpt-audio", Modality: "audio", Family: "openai-gpt"},
			{ID: "gpt-audio-mini", Modality: "audio", Family: "openai-gpt"},
			{ID: "mimo-v2.5-asr", Modality: "text", Family: "mimo"},
		},
		// 网关没有 /audio/transcriptions 端点（404），chat 一律 no_candidate。
		transcriptions: map[string]int{},
		chatStatus: map[string]int{
			"gpt-audio":      http.StatusServiceUnavailable,
			"gpt-audio-mini": http.StatusServiceUnavailable,
			"mimo-v2.5-asr":  http.StatusServiceUnavailable,
		},
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	res, err := Discover(context.Background(), srv.Client(), NewDiscoveryCache(0),
		srv.URL+"/v1", "key", true)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if res.TotalModels != 4 {
		t.Fatalf("totalModels=%d want 4", res.TotalModels)
	}
	if len(res.UsableCandidates()) != 0 {
		t.Fatalf("不应有可用候选，实际 %+v", res.UsableCandidates())
	}
	byModel := map[string]Candidate{}
	for _, c := range res.Candidates {
		byModel[c.Model] = c
	}
	for _, want := range []string{"gpt-audio", "gpt-audio-mini", "mimo-v2.5-asr"} {
		c, ok := byModel[want]
		if !ok {
			t.Fatalf("候选里缺少 %s", want)
		}
		if c.Status != ProbeNoProvider {
			t.Errorf("%s status=%s want %s（detail=%s）", want, c.Status, ProbeNoProvider, c.Detail)
		}
		if c.Usable() {
			t.Errorf("%s 被判为可用，但上游其实没有 provider", want)
		}
	}
	if _, ok := byModel["deepseek-v4-pro"]; ok {
		t.Errorf("纯文本模型不该出现在 ASR 候选里")
	}
	if f.lastAuth != "Bearer key" {
		t.Errorf("模型目录请求没带 Authorization：%q", f.lastAuth)
	}
}

// TestProbeDetectsAudioDrop 是最关键的一条：网关收下音频却丢掉它，
// 返回 200 +「您似乎没有附上录音文件」。不识别它，用户就会拿到一段幻觉文本。
func TestProbeDetectsAudioDrop(t *testing.T) {
	f := &fakeGateway{
		models:     []GatewayModel{{ID: "auto", Modality: "text"}},
		chatStatus: map[string]int{"auto": http.StatusOK},
		chatText: map[string]string{
			"auto": "您好，您似乎没有附上录音文件。请重新上传需要转写的音频，我会为您逐字转写成文字。",
		},
		chatChars: 0,
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	c := ProbeModel(context.Background(), srv.Client(), srv.URL+"/v1", "key", "auto")
	if c.Status != ProbeAudioIgnored {
		t.Fatalf("status=%s want %s（detail=%s sample=%q）", c.Status, ProbeAudioIgnored, c.Detail, c.SampleText)
	}
	if c.Usable() {
		t.Fatal("丢音频的模型不能被判为可用")
	}
	if c.Transport != TransportChatAudio {
		t.Errorf("transport=%s want %s", c.Transport, TransportChatAudio)
	}
}

func TestLooksLikeMissingAudio(t *testing.T) {
	positive := []string{
		"您好，您似乎没有附上录音文件。请重新上传需要转写的音频。",
		"I don't see any audio file attached to this message.",
		"Sorry, I cannot access the audio.",
		"抱歉，我没有收到音频文件。",
		"抱歉，尚未收到录音，请重新上传。",
		"Please re-upload the audio file.",
	}
	for _, s := range positive {
		if !LooksLikeMissingAudio(s) {
			t.Errorf("应判为「没收到音频」：%q", s)
		}
	}
	negative := []string{
		"今天下午三点开项目评审会，请准备进度报告和预算表。",
		"Meeting starts at three. Please bring the budget.",
		"",
		"附件里没有提到预算上限的具体数字。", // 出现「没有」但不是「没有音频」
	}
	for _, s := range negative {
		if LooksLikeMissingAudio(s) {
			t.Errorf("正常转写被误判：%q", s)
		}
	}
}

// TestProbeClassifiesBothTransportsMissing 见 discovery_endpoint_missing_test.go。
//
// 2026-10-01 审计：本文件是从 feat/2026-10-01-stt-service 恢复出来的，里面有一份
// 同名同义的 TestProbeClassifiesBothTransportsMissing，与已在 main 的那份重复
// 声明，导致整个 stt 包编译不过（duplicate symbol）。保留 main 那份：它除同样
// 的三条断言外，还多断言 Detail 不含 no_candidate，并配了反向用例
// TestProbeNoProviderIsNotEndpointMissing 一起钉住「探测失败 ≠ 端点缺失」。

// TestProbePrefersTranscriptionsEndpoint 外部服务（OpenAI/Groq 兼容）走
// /audio/transcriptions 就该直接判 ok，不再去试 chat。
func TestProbePrefersTranscriptionsEndpoint(t *testing.T) {
	f := &fakeGateway{
		models:         []GatewayModel{{ID: "gpt-4o-mini-transcribe"}},
		transcriptions: map[string]int{"gpt-4o-mini-transcribe": http.StatusOK},
		transcribeText: map[string]string{"gpt-4o-mini-transcribe": "会议记录：确认下周交付。"},
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	c := ProbeModel(context.Background(), srv.Client(), srv.URL+"/v1", "key", "gpt-4o-mini-transcribe")
	if c.Status != ProbeOK || c.Transport != TransportTranscriptions {
		t.Fatalf("status=%s transport=%s detail=%s", c.Status, c.Transport, c.Detail)
	}
	if c.SampleText != "会议记录：确认下周交付。" {
		t.Errorf("sampleText=%q", c.SampleText)
	}
}

func TestDiscoverCachesAndForceRefreshes(t *testing.T) {
	f := &fakeGateway{
		models:         []GatewayModel{{ID: "whisper-1"}},
		transcriptions: map[string]int{"whisper-1": http.StatusOK},
		transcribeText: map[string]string{"whisper-1": "hello"},
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	cache := NewDiscoveryCache(time.Minute)

	if _, err := Discover(context.Background(), srv.Client(), cache, srv.URL+"/v1", "key", false); err != nil {
		t.Fatalf("first Discover: %v", err)
	}
	// 缓存命中：不改上游行为也该拿到同样的结论。
	res2, err := Discover(context.Background(), srv.Client(), cache, srv.URL+"/v1", "key", false)
	if err != nil || len(res2.UsableCandidates()) != 1 {
		t.Fatalf("cache hit broken: %v %+v", err, res2)
	}
	// 缓存 key 必须含地址与 key 指纹：换地址不能串味。
	if _, ok := cache.Peek(srv.URL+"/v2", "key"); ok {
		t.Error("不同地址不应命中同一条缓存")
	}
	if _, ok := cache.Peek(srv.URL+"/v1", "other-key"); ok {
		t.Error("不同 key 不应命中同一条缓存")
	}
}

func TestToneWAVIsParseable(t *testing.T) {
	wav := ToneWAV(8000, 500)
	if len(wav) < 44 {
		t.Fatalf("wav too short: %d bytes", len(wav))
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		t.Fatalf("bad wav header: %q %q", wav[0:4], wav[8:12])
	}
	secs, ok := wavDurationSeconds(wav)
	if !ok {
		t.Fatal("wavDurationSeconds 无法解析自己生成的 WAV")
	}
	if secs < 0.49 || secs > 0.51 {
		t.Errorf("duration=%.3f want ~0.5", secs)
	}
	if _, ok := wavDurationSeconds([]byte("not a wav at all, really not")); ok {
		t.Error("非 WAV 不应解析出时长")
	}
}

func TestKnownUSDPerHour(t *testing.T) {
	if got := KnownUSDPerHour("gpt-4o-mini-transcribe"); got != 0.18 {
		t.Errorf("gpt-4o-mini-transcribe=%.4f want 0.18", got)
	}
	if got := KnownUSDPerHour("gpt-audio-mini"); got != 0 {
		t.Errorf("网关模型不应有报价（未知就是未知），got %.4f", got)
	}
	if got := KnownUSDPerHour("some-unknown-model"); got != 0 {
		t.Errorf("未知模型报价应为 0，got %.4f", got)
	}
}

func TestRecommendedModelsCoverBothGroups(t *testing.T) {
	all := RecommendedModels()
	var gw, ext int
	for _, o := range all {
		switch o.Group {
		case "gateway":
			gw++
		case "external":
			ext++
		}
	}
	// 数量断言只守下限，不写死具体个数：用户要求「尽可能费用少」，候选表
	// 会随调研持续增补（2026-10-01 从 3 个扩到 7 个），写死上限会让正常的
	// 新增候选变成测试失败，从而诱导后来者不去补候选。
	// 上限仍然要设——防止有人把整张 OpenRouter 模型表灌进来。
	if gw != 3 || ext < 7 || ext > 20 {
		t.Fatalf("预置应为网关 3 + 外部 7..20，实际 gateway=%d external=%d", gw, ext)
	}
	for _, o := range all {
		if o.Model == "" || o.Note == "" {
			t.Errorf("预置项缺字段：%+v", o)
		}
	}
	// 外部项必须带地址，否则用户填 key 也不知道打哪。
	for _, o := range RecommendedExternalModels() {
		if !strings.HasPrefix(o.BaseURL, "https://") {
			t.Errorf("外部模型 %s 缺 baseURL", o.Model)
		}
	}
}

func TestNormalizeChannelAndTransport(t *testing.T) {
	if NormalizeChannel("") != ChannelAuto || NormalizeChannel("乱填") != ChannelAuto {
		t.Error("未知通道应回落 auto")
	}
	if NormalizeChannel("Gateway") != ChannelGateway {
		t.Error("通道名应大小写不敏感")
	}
	if NormalizeTransport("") != TransportAuto {
		t.Error("空传输形态应回落 auto")
	}
	if NormalizeTransport("chat-audio") != TransportChatAudio {
		t.Error("chat-audio 应被识别")
	}
}

// ---------------------------------------------------------------------------
// 2026-10-01 网关改了 503 的响应体形状
//
// 早先的 503 体是 {"error":{"code":"no_candidate",...}}；实测（同一天下午）
// 变成了 {"error":{"alternatives":{"requested_model":"mimo-v2.5-asr",
// "task_type":"code","alternatives":[…]}}}。
//
// 语义没变——「这个模型没有上游 provider，附上它能用的备选」——但只认
// no_candidate 的 isNoProvider 会把它判成 ProbeFailed。后果不是分类难看：
// 设置页显示「探测失败」而不是「网关无上游 provider」，用户看不出该去开通
// provider 还是换模型，正是这个功能最初要解决的可行动性问题。
// ---------------------------------------------------------------------------

func TestIsNoProviderRecognizesGatewayAlternativesShape(t *testing.T) {
	cases := []struct {
		name   string
		status int
		err    string
		want   bool
	}{
		{"旧形状 no_candidate", 503, `{"error":{"code":"no_candidate","message":"No available provider"}}`, true},
		{"旧形状 no available provider", 503, "No available provider for model 'x'", true},
		{
			"2026-10-01 新形状 requested_model+alternatives", 503,
			`http 503: {"error":{"alternatives":{"requested_model":"mimo-v2.5-asr","task_type":"code",` +
				`"alternatives":[{"model":"deepseek-v4-flash","family":"deepseek","context_window":131072,` +
				`"featured":true,"reason":"task_match"}]}}}`,
			true,
		},
		// 只出现 requested_model、没有 alternatives 的 503 不能判成 no_provider：
		// 那是别的错误，误判会让用户去开通 provider，而真实原因是别的事。
		{"只有 requested_model", 503, `{"error":{"requested_model":"gpt-audio"}}`, false},
		// 非 503 一律不算
		{"500 不是 no_provider", 500, `{"error":{"alternatives":{"requested_model":"x","alternatives":[]}}}`, false},
		{"404 不是 no_provider", 404, `{"error":{"code":"no_candidate"}}`, false},
		{"nil err", 503, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var err error
			if c.err != "" {
				err = errors.New(c.err)
			}
			if got := isNoProvider(c.status, err); got != c.want {
				t.Errorf("isNoProvider(%d, %q) = %v，期望 %v", c.status, c.err, got, c.want)
			}
		})
	}
}
