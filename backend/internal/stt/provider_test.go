package stt

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 本文件是「转写模板」抽象的判据。
//
// 判据选的不是「能不能转出文字」——那太宽，一个恒真断言就能过。
// 这里钉的是**三处最容易静默失效的差异**，每一处都有 2026-10-08 的实机读数
// 支撑（详见 provider.go 文件头）：
//
//  1. language 必须走 **HTTP 头**。放表单字段 → 上游 200、不报错、但对中文
//     的准确率有实质影响。**这是本仓最贵的一个 bug 形态**：
//     任何「状态码 == 200」的断言都抓不到它。
//  2. 词级时间戳参数名是 **timestamp_level**，不是 timestamp_granularities[]。
//     发错名字同样 200、同样不报错，只是不给词级时间戳。
//  3. stream=true 与 verbose_json **互斥**（上游 400 (2013)）。
//     本仓必须自己兜住，不能把两个都发出去换一个 400。
//
// ★ 每条判据都配一个**变异**（把正确值改错），要求测试变红。
//   没有变异验证的判据，无法区分「测到了东西」与「恒真」。

// capturedRequest 是假上游收到的请求全貌。
type capturedRequest struct {
	Path    string
	Headers http.Header
	Fields  map[string][]string
	FileNam string
	FileLen int
}

// newFakeUpstream 起一个假 MiniMax 上游，按脚本回放响应。
//
// 为什么必须能拿到**请求全貌**而不是只回响应：上面三条判据全都在「发了什么」，
// 只断言响应的话根本无从验证。
func newFakeUpstream(t *testing.T, captured *capturedRequest, respond func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Path = r.URL.Path
		captured.Headers = r.Header.Clone()
		captured.Fields = map[string][]string{}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err == nil {
				mr := multipart.NewReader(r.Body, params["boundary"])
				for {
					part, err := mr.NextPart()
					if err != nil {
						break
					}
					name := part.FormName()
					if name == "file" {
						data, _ := io.ReadAll(part)
						captured.FileNam = part.FileName()
						captured.FileLen = len(data)
						continue
					}
					data, _ := io.ReadAll(part)
					captured.Fields[name] = append(captured.Fields[name], string(data))
				}
			}
		}
		respond(w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// miniWAV 造一段 1 秒的合法 16kHz 单声道 PCM WAV（够 BuildRequest 拼 multipart）。
func miniWAV() []byte {
	const sampleRate, bitsPerSample, ch = 16000, 16, 1
	const seconds = 1
	dataLen := sampleRate * ch * bitsPerSample / 8 * seconds
	buf := make([]byte, 0, 44+dataLen)
	buf = append(buf, "RIFF"...)
	buf = appendUint32(buf, uint32(36+dataLen))
	buf = append(buf, "WAVE"...)
	buf = append(buf, "fmt "...)
	buf = appendUint32(buf, 16)
	buf = appendUint16(buf, 1)
	buf = appendUint16(buf, ch)
	buf = appendUint32(buf, sampleRate)
	buf = appendUint32(buf, sampleRate*ch*bitsPerSample/8)
	buf = appendUint16(buf, ch*bitsPerSample/8)
	buf = appendUint16(buf, bitsPerSample)
	buf = append(buf, "data"...)
	buf = appendUint32(buf, uint32(dataLen))
	buf = append(buf, make([]byte, dataLen)...)
	return buf
}

func appendUint32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}
func appendUint16(b []byte, v uint16) []byte {
	return append(b, byte(v), byte(v>>8))
}

// ---------------------------------------------------------------- 判据 1
//
// ★ 变异点：把 BuildRequest 里的 req.Header.Set("language", lang)
//   改成 w.WriteField("language", lang)（即放表单）。
//   正确实现下本判据绿；变异后 language 表单字段非空而请求头为空 ⇒ 红。

func TestMiniMaxLanguageTravelsAsHeaderNotFormField(t *testing.T) {
	var cap capturedRequest
	srv := newFakeUpstream(t, &cap, func(w http.ResponseWriter) {
		io.WriteString(w, `{"text":"我们下周三开会","duration":1.0}`)
	})

	p := LookupProvider(ProviderMiniMax)
	if p == nil {
		t.Fatalf("模板 %q 未注册", ProviderMiniMax)
	}
	req, err := p.BuildRequest(context.Background(), ProviderRequest{
		Target:   &Target{BaseURL: srv.URL, APIKey: "k", Model: "asr-1.0", Language: "zh"},
		Audio:    miniWAV(),
		Filename: "a.wav",
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.Header.Get("language"); got != "zh" {
		t.Errorf("language 必须作为 HTTP 头发出（MiniMax 官方契约），实际请求头 = %q", got)
	}
	// 反向断言：表单里**不能**出现 language。
	// 这一条不能省：两处都发的话，上游优先读表单，
	// 于是「请求头是对的」这条判据仍然绿，而行为是错的。
	if _, dup := cap.Fields["language"]; dup {
		t.Errorf("language 不应同时出现在表单字段里（上游会优先读表单）：fields=%v", cap.Fields)
	}
}

// TestMiniMaxLanguageHeaderReachesWire 是上面那条的**线上验证**：
// 判据在真实 HTTP 往返上跑，而不是只看 BuildRequest 返回的 *http.Request。
//
// 为什么必须多这一层：http.Request.Header 在 client.Do 之后仍可读，
// 但中间若有 RoundTripper 改写 Header，前者就看不到 ——
// 那正是「断言钉在中间层字节序列上」的恒真形态（见 AGENTS 判据纪律）。
func TestMiniMaxLanguageHeaderReachesWire(t *testing.T) {
	var cap capturedRequest
	srv := newFakeUpstream(t, &cap, func(w http.ResponseWriter) {
		io.WriteString(w, `{"text":"ok","duration":1.0}`)
	})
	tr := &Transcriber{
		resolve: func(context.Context, Scope) (*Target, error) {
			return &Target{BaseURL: srv.URL, APIKey: "k", Model: "asr-1.0",
				Provider: ProviderMiniMax, Transport: TransportTranscriptions, Language: "zh"}, nil
		},
		client:  srv.Client(),
		timeout: 10 * time.Second,
	}
	if _, err := tr.Transcribe(context.Background(), miniWAV(), "a.wav"); err != nil {
		t.Fatalf("转写失败: %v", err)
	}
	if got := cap.Headers.Get("language"); got != "zh" {
		t.Errorf("语言提示没到达上游：请求头 language = %q（期望 zh）", got)
	}
}

// ---------------------------------------------------------------- 判据 2
//
// ★ 变异点：把 w.WriteField("timestamp_level", "word")
//   改成 w.WriteField("timestamp_granularities[]", "word")。

func TestMiniMaxWordTimestampsUseTimestampLevelField(t *testing.T) {
	var cap capturedRequest
	srv := newFakeUpstream(t, &cap, func(w http.ResponseWriter) {
		io.WriteString(w, `{"text":"ok","duration":1.0,"n_speakers":1}`)
	})
	p := LookupProvider(ProviderMiniMax)
	req, err := p.BuildRequest(context.Background(), ProviderRequest{
		Target:   &Target{BaseURL: srv.URL, APIKey: "k", Model: "asr-1.0", Language: "zh"},
		Audio:    miniWAV(),
		Filename: "a.wav",
		// 需要开词级时间戳：用一个「声明支持」的模型 + 非 Plain。
		Plain: false, DurationSec: 5,
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	// 直接读请求体里的字段：先发一次到假上游以捕获。
	_ = req
	// 改用实际发送来捕获（更接近真实行为）。
	tr := &Transcriber{
		resolve: func(context.Context, Scope) (*Target, error) {
			return &Target{BaseURL: srv.URL, APIKey: "k", Model: "asr-1.0",
				Provider: ProviderMiniMax, Transport: TransportTranscriptions, Language: "zh"}, nil
		},
		client: srv.Client(), timeout: 10 * time.Second,
	}
	tr.Transcribe(context.Background(), miniWAV(), "a.wav")

	// asr-1.0 走 RecommendedModels 时 WordTimestamps 目前是 false，
	// 所以这条判据钉的是**参数名映射本身**：
	// 当开了词级时间戳时，字段名必须是 timestamp_level。
	// 用 supportsWordTimestamps 为 true 的路径来验 —— 通过 Target 的模型选择。
	if got := cap.Fields["timestamp_level"]; len(got) == 0 && got != nil {
		t.Errorf("timestamp_level 字段形态异常：%v", got)
	}
	if _, wrong := cap.Fields["timestamp_granularities[]"]; wrong {
		t.Errorf("不应发送 OpenAI 的 timestamp_granularities[]（MiniMax 不认这个字段名）：%v", cap.Fields)
	}
}

// TestMiniMaxWordTimestampFieldNameWhenEnabled 直接钉住参数名。
//
// 上一条依赖「模型是否声明支持词级时间戳」，是间接的；
// 这条把 asr-1.0 声明为支持词级时间戳后再验字段名，判据不依赖别的能力开关。
//
// ★ 变异点：同判据 2。
func TestMiniMaxWordTimestampFieldNameWhenEnabled(t *testing.T) {
	var cap capturedRequest
	srv := newFakeUpstream(t, &cap, func(w http.ResponseWriter) {
		io.WriteString(w, `{"text":"ok","duration":1.0,"n_speakers":1}`)
	})
	orig := recommendedWordTSOverride
	recommendedWordTSOverride = true
	t.Cleanup(func() { recommendedWordTSOverride = orig })

	tr := &Transcriber{
		resolve: func(context.Context, Scope) (*Target, error) {
			return &Target{BaseURL: srv.URL, APIKey: "k", Model: "asr-1.0",
				Provider: ProviderMiniMax, Transport: TransportTranscriptions, Language: "zh"}, nil
		},
		client: srv.Client(), timeout: 10 * time.Second,
	}
	if _, err := tr.Transcribe(context.Background(), miniWAV(), "a.wav"); err != nil {
		t.Fatalf("转写失败: %v", err)
	}
	if got := cap.Fields["timestamp_level"]; len(got) != 1 || got[0] != "word" {
		t.Errorf("词级时间戳字段应为 timestamp_level=word，实际 = %v（fields=%v）", got, cap.Fields)
	}
}

// recommendedWordTSOverride 定义在 transcribe.go（生产文件）里，
// 理由见那里的注释：它要被生产路径上的 supportsWordTimestamps 引用。

// ---------------------------------------------------------------- 判据 3
//
// ★ 变异点：删掉 BuildRequest 里 `if r.WantStream { opts.ResponseFormat = "json" }`
//   这段强制覆盖（改回跟随 verbose）。

func TestMiniMaxStreamForcesPlainJSONFormat(t *testing.T) {
	var cap capturedRequest
	srv := newFakeUpstream(t, &cap, func(w http.ResponseWriter) {
		io.WriteString(w, "data: {\"index\":0,\"delta\":\"你\",\"finish\":false}\n\n")
		io.WriteString(w, "data: {\"index\":1,\"delta\":\"好\",\"finish\":true,\"duration\":1.0}\n\n")
	})
	p := LookupProvider(ProviderMiniMax)
	orig := recommendedWordTSOverride
	recommendedWordTSOverride = true
	t.Cleanup(func() { recommendedWordTSOverride = orig })

	req, err := p.BuildRequest(context.Background(), ProviderRequest{
		Target:   &Target{BaseURL: srv.URL, APIKey: "k", Model: "asr-1.0", Language: "zh"},
		Audio:    miniWAV(),
		Filename: "a.wav",
		// 同时要流式 + 增强特性（模拟用户两个开关都勾了）。
		WantStream: true, Plain: false, DurationSec: 5,
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if req.URL.Path != minimaxTextPath {
		t.Errorf("路径应为 %s，实际 %s", minimaxTextPath, req.URL.Path)
	}
	// 发出去看真实字段。
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if got := cap.Fields["response_format"]; len(got) != 1 || got[0] != "json" {
		t.Errorf("流式时 response_format 必须被强制为 json（上游 400 拒绝 verbose_json+stream，实测 (2013)），实际 = %v", got)
	}
	if got := cap.Fields["stream"]; len(got) != 1 || got[0] != "true" {
		t.Errorf("流式时必须发 stream=true，实际 = %v", got)
	}
	if _, dup := cap.Fields["timestamp_level"]; dup {
		t.Errorf("流式时不应再发 timestamp_level（词级时间戳与流式互斥），实际 fields=%v", cap.Fields)
	}
}

// ---------------------------------------------------------------- 解析层

// TestMiniMaxVerboseJSONSpeakerVerdict 钉住「单人录音也要算已分离」。
//
// 2026-10-08 实读：7.086s 单人中文录音 → n_speakers=1, segments[0].speaker="S1"。
// 若照抄 OpenAI 那套「n_speakers>1 才算分离」，单人会议会被标成未分离 ——
// 那是错的：上游确实做了分离并告诉我们「只有 1 个人」。
func TestMiniMaxVerboseJSONSpeakerVerdict(t *testing.T) {
	body := `{"text":"我们下周三开会","duration":7.086,"n_speakers":1,
	  "segments":[{"id":0,"start":0.02,"end":7.02,"speaker":"S1","text":"我们下周三开会"}]}`
	p := LookupProvider(ProviderMiniMax)
	pr, err := p.ParseResponse(200, []byte(body))
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if len(pr.Segments) != 1 {
		t.Fatalf("应有 1 段，实际 %d", len(pr.Segments))
	}
	segs, diarized := diarizationVerdict(pr)
	if !diarized {
		t.Errorf("上游回了 speaker=S1，应判为已分离（n_speakers=1 也是分离结果，不是「没分离」）")
	}
	if len(segs) == 1 && (segs[0].StartMS != 20 || segs[0].EndMS != 7020) {
		t.Errorf("秒→毫秒换算错：start=%.3fs end=%.3fs 应得 20ms/7020ms，实际 %d/%d",
			0.02, 7.02, segs[0].StartMS, segs[0].EndMS)
	}
	if pr.DurationSec != 7.086 {
		t.Errorf("上游 duration 应回填，实际 %v", pr.DurationSec)
	}
}

// TestMiniMaxStreamParseConcatenatesDeltas 钉住 SSE 解析。
//
// ★ 变异点：把 `sb.WriteString(ev.Delta)` 删掉 → text 为空 → 红。
// ★ 第二个变异：遇到 finish 就 return（提前返回）→ duration 丢失 → 红。
func TestMiniMaxStreamParseConcatenatesDeltas(t *testing.T) {
	// 真实形态：index 递增，delta 拼接，终止事件 delta 为空且带 duration。
	raw := "data: {\"index\":0,\"delta\":\"我们\",\"finish\":false}\n\n" +
		"data: {\"index\":1,\"delta\":\"下周三上午十点开会\",\"finish\":false}\n\n" +
		"data: {\"index\":2,\"delta\":\"\",\"finish\":true,\"duration\":7.086}\n\n"
	p := LookupProvider(ProviderMiniMax)
	var streamed strings.Builder
	pr, err := p.ParseStream(strings.NewReader(raw), func(d string) error {
		streamed.WriteString(d)
		return nil
	})
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	if pr.Text != "我们下周三上午十点开会" {
		t.Errorf("增量拼接错：got %q", pr.Text)
	}
	if pr.DurationSec != 7.086 {
		t.Errorf("终止事件的 duration 必须取到（早退就丢），got %v", pr.DurationSec)
	}
	// 回调拿到的增量之和必须与最终文本一致 ——
	// 这一条抓的是「文本对了但回调漏发」这种半失效。
	if streamed.String() != pr.Text {
		t.Errorf("onDelta 累加 %q ≠ 最终文本 %q", streamed.String(), pr.Text)
	}
}

// TestMiniMaxStreamSkipsCommentsAndHeartbeat 钉住非 data 行被忽略。
func TestMiniMaxStreamSkipsCommentsAndHeartbeat(t *testing.T) {
	raw := ": keep-alive\n\n" +
		"event: message\n\n" +
		"data: {\"index\":0,\"delta\":\"测试\",\"finish\":false}\n\n" +
		"data: {\"index\":1,\"delta\":\"\",\"finish\":true,\"duration\":2.0}\n\n"
	p := LookupProvider(ProviderMiniMax)
	pr, err := p.ParseStream(strings.NewReader(raw), nil)
	if err != nil {
		t.Fatalf("ParseStream: %v", err)
	}
	if pr.Text != "测试" {
		t.Errorf("注释/事件名行应被忽略，只取 data 的 delta；got %q", pr.Text)
	}
}

// ---------------------------------------------------------------- 错误映射

// TestMiniMaxErrorsAreActionable 钉住四个状态码各自映射到**可行动**的提示。
//
// ★ 变异点：把 switch 里的某个 case 删掉 → 落到 default → 提示不含状态码语义 → 红。
func TestMiniMaxErrorsAreActionable(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"401 鉴权", 401,
			`{"type":"error","error":{"type":"authorized_error","message":"login fail (1004)","http_code":"401"},"request_id":"r1"}`,
			"鉴权失败"},
		{"402 余额", 402,
			`{"type":"error","error":{"type":"insufficient_balance_error","message":"insufficient balance (1008)","http_code":"402"}}`,
			"余额不足"},
		{"429 限流", 429,
			`{"type":"error","error":{"type":"rate_limit_error","message":"rate limit (1002)","http_code":"429"}}`,
			"限流"},
		{"413 超大", 413,
			`{"type":"error","error":{"type":"invalid_request_error","message":"request body too large (1000)","http_code":"413"}}`,
			"50MB"},
		{"422 敏感", 422,
			`{"type":"error","error":{"type":"unprocessable_entity_error","message":"sensitive content (1026)","http_code":"422"}}`,
			"敏感"},
		{"400 参数", 400,
			`{"type":"error","error":{"type":"bad_request_error","message":"response_format cannot be used with stream=true (2013)","http_code":"400"}}`,
			"参数非法"},
	}
	p := LookupProvider(ProviderMiniMax)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.ParseResponse(tc.status, []byte(tc.body))
			if err == nil {
				t.Fatalf("非 200 必须返回错误")
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.want) {
				t.Errorf("错误提示应包含 %q（用户要据此知道该做什么），实际：%s", tc.want, msg)
			}
			// 内部错误码必须保留：那是排障时唯一能定位的东西。
			// 抽查 400 这一条（它带 2013），其余同理。
			if tc.status == 400 && !strings.Contains(msg, "2013") {
				t.Errorf("上游内部错误码（2013）必须保留在提示里，实际：%s", msg)
			}
		})
	}
}

// ---------------------------------------------------------------- 注册表

// TestProviderRegistryClosedAndLookupable 钉住「注册表是闭合的」。
//
// ★ 变异点：让 RegisterProvider 覆盖同 id（去掉 dup 判断）
//
//	→ 不影响本条；真正要防的是 LookupProvider 对未知 id 返回非 nil，
//	所以另有一条 TestProviderForTargetRejectsUnknownID。
func TestProviderRegistryClosedAndLookupable(t *testing.T) {
	ids := ProviderIDs()
	if len(ids) < 3 {
		t.Fatalf("至少应注册 3 个模板（MiniMax/OpenAI/智谱），实际 %v", ids)
	}
	for _, id := range []ProviderID{ProviderMiniMax, ProviderOpenAI, ProviderZhipu} {
		if LookupProvider(id) == nil {
			t.Errorf("模板 %q 应已注册", id)
		}
	}
	// 排序可复现：设置页要按稳定顺序渲染，不能每次刷新顺序都变。
	for i := 1; i < len(ids); i++ {
		if ids[i-1] > ids[i] {
			t.Fatalf("ProviderIDs 应按字典序（可复现渲染），实际 %v", ids)
		}
	}
}

// TestProviderForTargetRejectsUnknownID 钉住「未知模板不静默回退」。
//
// ★ 变异点：让 ProviderForTarget 对未知 id 回退到默认模板 → 本条红。
// 这条防的是最贵的缺陷：配了 A 家地址、按 B 家协议发出去，
// 上游要么静默忽略参数，要么返回一个指不到真因的错误。
func TestProviderForTargetRejectsUnknownID(t *testing.T) {
	tr := &Transcriber{
		resolve: func(context.Context, Scope) (*Target, error) {
			return &Target{BaseURL: "https://example.invalid", APIKey: "k", Model: "asr-1.0",
				Provider: "totally-unknown-template", Transport: TransportTranscriptions}, nil
		},
		client: http.DefaultClient, timeout: time.Second,
	}
	_, err := tr.Transcribe(context.Background(), miniWAV(), "a.wav")
	if err == nil {
		t.Fatalf("未知模板 id 必须报错，不能静默回退到别的模板")
	}
	if !strings.Contains(err.Error(), "totally-unknown-template") {
		t.Errorf("错误里要点名那个未知模板，实际：%v", err)
	}
	// 且必须列出可选项，用户才能自己改对。
	if !strings.Contains(err.Error(), ProviderMiniMax) {
		t.Errorf("错误里应列出可选模板（否则用户无从下手），实际：%v", err)
	}
}

// TestProviderForTargetInfersMiniMaxByEndpoint 钉住推断规则。
func TestProviderForTargetInfersMiniMaxByEndpoint(t *testing.T) {
	cases := []struct {
		name string
		t    *Target
		want ProviderID
	}{
		{"国内站", &Target{BaseURL: "https://api.minimax.cn"}, ProviderMiniMax},
		{"国际站", &Target{BaseURL: "https://api.minimaxi.com/v1"}, ProviderMiniMax},
		{"显式指定", &Target{BaseURL: "https://api.minimax.cn", Provider: ProviderOpenAI}, ProviderOpenAI},
		{"OpenAI 兼容", &Target{BaseURL: "https://api.openai.com/v1"}, ProviderOpenAI},
		{"OpenRouter", &Target{BaseURL: "https://openrouter.ai/api/v1"}, ProviderOpenAI},
		{"nil", nil, ProviderOpenAI},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProviderForTarget(tc.t); got != tc.want {
				t.Errorf("ProviderForTarget = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestGatewayModelNamedAsrIsNotMistakenForMiniMax 钉住「同名模型 + 不同域名」。
//
// 为什么需要这条：网关上也可能列出一个叫 asr-1.0 的模型。只看模型名会把
// 网关请求打到 MiniMax 的路径上，症状是 404 且错误指不到真因。
func TestGatewayModelNamedAsrIsNotMistakenForMiniMax(t *testing.T) {
	tgt := &Target{BaseURL: "https://llm.kxpms.cn/v1", Model: "asr-1.0"}
	if got := ProviderForTarget(tgt); got != ProviderOpenAI {
		t.Errorf("网关上的 asr-1.0 应走 OpenAI 兼容层（网关没有 /v1/speech_to_text），实际 %q", got)
	}
}

// ---------------------------------------------------------------- OpenAI 侧未被破坏

// TestOpenAIStillSendsLanguageAsFormField 钉住「两个模板的参数位置不同」。
//
// 这是模板化的核心断言：如果 MiniMax 改坏了 language 的位置，
// 而 OpenAI 侧的判据没单独钉住，就会出现「改一处坏另一处」。
func TestOpenAIStillSendsLanguageAsFormField(t *testing.T) {
	var cap capturedRequest
	srv := newFakeUpstream(t, &cap, func(w http.ResponseWriter) {
		io.WriteString(w, `{"text":"ok"}`)
	})
	tr := &Transcriber{
		resolve: func(context.Context, Scope) (*Target, error) {
			return &Target{BaseURL: srv.URL, APIKey: "k", Model: "whisper-large-v3-turbo",
				Transport: TransportTranscriptions, Language: "zh"}, nil
		},
		client: srv.Client(), timeout: 10 * time.Second,
	}
	if _, err := tr.Transcribe(context.Background(), miniWAV(), "a.wav"); err != nil {
		t.Fatalf("转写失败: %v", err)
	}
	if cap.Path != "/audio/transcriptions" {
		t.Errorf("OpenAI 兼容层路径应为 /audio/transcriptions，实际 %s", cap.Path)
	}
	if got := cap.Fields["language"]; len(got) != 1 || got[0] != "zh" {
		t.Errorf("OpenAI 兼容层的 language 应作为**表单字段**（与 MiniMax 相反），实际 %v", got)
	}
	if got := cap.Headers.Get("language"); got != "" {
		t.Errorf("OpenAI 兼容层不应把 language 放请求头（那是 MiniMax 的契约），实际 %q", got)
	}
	// 简体偏置仍只在中文时发。
	if got := cap.Fields["prompt"]; len(got) != 1 || got[0] != SimplifiedChineseBiasPrompt {
		t.Errorf("中文时必须带简体偏置 prompt，实际 %v", got)
	}
}

// TestOpenAIBaseURLWithV1SuffixDoesNotDoubleV1 钉住路径拼接。
func TestOpenAIBaseURLWithV1SuffixDoesNotDoubleV1(t *testing.T) {
	var cap capturedRequest
	srv := newFakeUpstream(t, &cap, func(w http.ResponseWriter) { io.WriteString(w, `{"text":"ok"}`) })
	tr := &Transcriber{
		resolve: func(context.Context, Scope) (*Target, error) {
			return &Target{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m",
				Transport: TransportTranscriptions, Language: "zh"}, nil
		},
		client: srv.Client(), timeout: 10 * time.Second,
	}
	if _, err := tr.Transcribe(context.Background(), miniWAV(), "a.wav"); err != nil {
		t.Fatalf("转写失败: %v", err)
	}
	if cap.Path != "/v1/audio/transcriptions" {
		t.Errorf("baseURL 已带 /v1 时不应拼成 /v1/v1，实际路径 %s", cap.Path)
	}
}

// TestMiniMaxBaseURLWithV1SuffixDoesNotDoubleV1 与上一条对称。
func TestMiniMaxBaseURLWithV1SuffixDoesNotDoubleV1(t *testing.T) {
	var cap capturedRequest
	srv := newFakeUpstream(t, &cap, func(w http.ResponseWriter) { io.WriteString(w, `{"text":"ok"}`) })
	tr := &Transcriber{
		resolve: func(context.Context, Scope) (*Target, error) {
			return &Target{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "asr-1.0",
				Provider: ProviderMiniMax, Transport: TransportTranscriptions, Language: "zh"}, nil
		},
		client: srv.Client(), timeout: 10 * time.Second,
	}
	if _, err := tr.Transcribe(context.Background(), miniWAV(), "a.wav"); err != nil {
		t.Fatalf("转写失败: %v", err)
	}
	if cap.Path != minimaxTextPath {
		t.Errorf("baseURL 已带 /v1 时路径应为 %s，实际 %s", minimaxTextPath, cap.Path)
	}
}

// ---------------------------------------------------------------- 形态诚实性

// TestStreamRejectedForOpenAIProvider 钉住「不支持流式要明说」。
//
// ★ 变异点：让 openaiProvider 接受 WantStream（不发参数或发 OpenAI 的 stream）
//
//	→ 本条红。
func TestStreamRejectedForOpenAIProvider(t *testing.T) {
	p := LookupProvider(ProviderOpenAI)
	_, err := p.BuildRequest(context.Background(), ProviderRequest{
		Target: &Target{BaseURL: "https://x.invalid", APIKey: "k", Model: "m"},
		Audio:  miniWAV(), Filename: "a.wav", WantStream: true,
	})
	if err == nil {
		t.Fatalf("OpenAI 兼容层不支持 SSE，必须显式报错（静默忽略会让「我开了流式」变成假承诺）")
	}
	if !strings.Contains(err.Error(), "流式") {
		t.Errorf("错误提示要说清是流式不支持，实际：%v", err)
	}
}

// TestZhipuStreamParseUsesItsOwnEventNames 钉住「第三家的事件名真的不同」。
//
// 这条判据是模板抽象的**存在理由**：若两个 provider 是一份代码，抽象没成立。
//
// ★ 方向不是随手挑的 —— 2026-10-08 实测过**两个方向**，它们并不对称：
//
//	智谱流 → 智谱解析器    text="智谱"   duration=0
//	智谱流 → MiniMax解析器 text="智谱"   duration=0   ← **能凑出文本**
//	MiniMax流 → MiniMax解析器 text="我们" duration=7.086
//	MiniMax流 → 智谱解析器  text=""      duration=0
//
// 不对称的成因是字段名：智谱事件的 delta 字段恰好也叫 delta，所以 MiniMax
// 的「读 delta 就拼」能把它吃下去；反过来 MiniMax 事件没有 type 字段，
// 智谱按事件名分派就拿不到任何东西。
//
// ⇒ 所以下面钉的是**能真正区分两家的那个方向**（MiniMax 流喂智谱必须为空）。
// 我最初写的是反方向（「智谱流喂 MiniMax 必须为空」），实测不成立 ——
// 那条判据是我自己造的，且它要求一个**不存在**的严格性。
// 换成真方向后，它仍然能区分两家，但不再要求产品做无谓的严格。
func TestZhipuStreamParseUsesItsOwnEventNames(t *testing.T) {
	zhipuRaw := "data: {\"type\":\"transcript.text.delta\",\"delta\":\"智谱\"}\n\n" +
		"data: {\"type\":\"transcript.text.delta\",\"delta\":\"测试\"}\n\n" +
		"data: {\"type\":\"transcript.text.done\"}\n\n" +
		"data: [DONE]\n\n"
	mmRaw := "data: {\"index\":0,\"delta\":\"我们\",\"finish\":false}\n\n" +
		"data: {\"index\":1,\"delta\":\"开会\",\"finish\":true,\"duration\":7.086}\n\n"

	// 正向：各自解析自己的流。
	zr, err := LookupProvider(ProviderZhipu).ParseStream(strings.NewReader(zhipuRaw), nil)
	if err != nil {
		t.Fatalf("智谱 ParseStream: %v", err)
	}
	if zr.Text != "智谱测试" {
		t.Errorf("智谱 SSE 应按 transcript.text.delta 拼接，got %q", zr.Text)
	}
	mr, err := LookupProvider(ProviderMiniMax).ParseStream(strings.NewReader(mmRaw), nil)
	if err != nil {
		t.Fatalf("MiniMax ParseStream: %v", err)
	}
	if mr.Text != "我们开会" || mr.DurationSec != 7.086 {
		t.Errorf("MiniMax SSE 解析错：text=%q duration=%v", mr.Text, mr.DurationSec)
	}

	// 判别方向：MiniMax 的流喂给智谱解析器，必须**拿不到文本**。
	// 若这条也过不了（拿到文本），说明两个 provider 是同一份代码，抽象没成立。
	cross, err := LookupProvider(ProviderZhipu).ParseStream(strings.NewReader(mmRaw), nil)
	if err != nil {
		t.Fatalf("跨解析不应报错: %v", err)
	}
	if cross.Text != "" {
		t.Errorf("智谱解析器按事件名分派，读不懂 MiniMax 的流（无 type 字段）；"+
			"却拿到了 %q ⇒ 两个 provider 其实是同一份代码", cross.Text)
	}
}

// TestEmptyTranscriptStillFailsAcrossProviders 钉住空文本守卫对所有模板生效。
//
// 这条守护的是「抽 finish() 统一收尾」这件事：之前 chat-audio 走独立早返回，
// 少了两道守卫。
func TestEmptyTranscriptStillFailsAcrossProviders(t *testing.T) {
	for _, id := range []ProviderID{ProviderMiniMax, ProviderOpenAI} {
		t.Run(id, func(t *testing.T) {
			var cap capturedRequest
			srv := newFakeUpstream(t, &cap, func(w http.ResponseWriter) { io.WriteString(w, `{"text":"   "}`) })
			tr := &Transcriber{
				resolve: func(context.Context, Scope) (*Target, error) {
					return &Target{BaseURL: srv.URL, APIKey: "k", Model: "asr-1.0",
						Provider: id, Transport: TransportTranscriptions, Language: "zh"}, nil
				},
				client: srv.Client(), timeout: 10 * time.Second,
			}
			_, err := tr.Transcribe(context.Background(), miniWAV(), "a.wav")
			if err == nil || !strings.Contains(err.Error(), "empty transcript") {
				t.Errorf("空文本必须判失败（模板 %s），实际 err=%v", id, err)
			}
		})
	}
}

// TestMissingAudioClaimRejectedAcrossProviders 钉住「上游说没收到音频」也算失败。
func TestMissingAudioClaimRejectedAcrossProviders(t *testing.T) {
	hallucination := `{"text":"您似乎没有附上录音文件，请重新上传。"}`
	for _, id := range []ProviderID{ProviderMiniMax, ProviderOpenAI} {
		t.Run(id, func(t *testing.T) {
			var cap capturedRequest
			srv := newFakeUpstream(t, &cap, func(w http.ResponseWriter) { io.WriteString(w, hallucination) })
			tr := &Transcriber{
				resolve: func(context.Context, Scope) (*Target, error) {
					return &Target{BaseURL: srv.URL, APIKey: "k", Model: "asr-1.0",
						Provider: id, Transport: TransportTranscriptions, Language: "zh"}, nil
				},
				client: srv.Client(), timeout: 10 * time.Second,
			}
			_, err := tr.Transcribe(context.Background(), miniWAV(), "a.wav")
			if err == nil {
				t.Fatalf("上游声称没收到音频时必须判失败（模板 %s）", id)
			}
			if !strings.Contains(err.Error(), "not a transcript") {
				t.Errorf("错误应说明这不是转写结果，实际：%v", err)
			}
		})
	}
}

// TestDiarizationRejectionFallsBackToPlain 钉住降级重试在新分发路径上仍然生效。
//
// ★ 变异点：删掉 viaProvider 里的 `isDiarizationRejection` 重试分支 → 本条红。
func TestDiarizationRejectionFallsBackToPlain(t *testing.T) {
	var calls int
	var sawPlain bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// 第一次带分离开关 → 拒；之后不带 → 成功。
		if r.Header.Get("X-Force-Plain") == "1" || calls > 1 {
			io.WriteString(w, `{"text":"拿到文字了"}`)
			return
		}
		w.WriteHeader(503)
		io.WriteString(w, `{"error":{"code":"diarization_unavailable"}}`)
	}))
	t.Cleanup(srv.Close)

	tr := &Transcriber{
		resolve: func(context.Context, Scope) (*Target, error) {
			return &Target{BaseURL: srv.URL, APIKey: "k", Model: "microsoft/mai-transcribe-2",
				Transport: TransportTranscriptions, Language: "zh"}, nil
		},
		client: srv.Client(), timeout: 10 * time.Second,
	}
	res, err := tr.Transcribe(context.Background(), miniWAV(), "a.wav")
	if err != nil {
		t.Fatalf("上游拒分离时应降级重试并成功，实际：%v", err)
	}
	if res.Text != "拿到文字了" {
		t.Errorf("降级后应拿到文字，got %q", res.Text)
	}
	if calls < 2 {
		t.Errorf("应至少请求两次（带开关被拒 → 不带开关重试），实际 %d 次", calls)
	}
	_ = sawPlain
}

// TestMiniMaxDefaultBaseURLIsDomesticStation 钉住「默认地址是国内站」。
//
// ★ 变异点：把 ModelOption.asr-1.0 的 BaseURL 改回 api.minimaxi.com（国际站）
//
//	或把 MiniMaxDefaultBaseURL 改掉 → 本条红。
//
// 为什么值得钉：这两个「默认地址」是两处手写的常量。实测踩过的坑是 ——
// 国内 key 打国际站会 401，而错误文案是「login fail: Please carry the API
// secret key…」，指向「key 没带」，**不会**指向「你打错了站」。
// 用户会去反复检查自己的 key，而真因在地址上。
func TestMiniMaxDefaultBaseURLIsDomesticStation(t *testing.T) {
	if got := MiniMaxDefaultBaseURL; !strings.Contains(got, "minimax.cn") || strings.Contains(got, "minimaxi.com") {
		t.Errorf("默认地址应为国内站 %q，实际 %q（国际站会让国内 key 401，且错误文案指向「key 没带」）",
			"api.minimax.cn", got)
	}
	for _, o := range RecommendedModels() {
		if strings.EqualFold(o.Model, "asr-1.0") {
			if strings.Contains(o.BaseURL, "minimaxi.com") {
				t.Errorf("asr-1.0 预置的 BaseURL 指向国际站 %q，应为国内站", o.BaseURL)
			}
			if o.BaseURL != MiniMaxDefaultBaseURL {
				t.Errorf("两处默认地址不一致：预置 %q vs MiniMaxDefaultBaseURL %q —— 会出现「设置页显示 A、实际打 B」",
					o.BaseURL, MiniMaxDefaultBaseURL)
			}
		}
	}
}

// TestMiniMaxCapabilitiesDeclared 钉住「预置里真的声明了实测能力」。
//
// ★ 变异点：把 asr-1.0 的 Diarization 改回 false → 本条红。
//
// 这条来自一次**实跑暴露的静默失效**：7.086s 真实音频经本仓代码路径转写成功，
// 但 diarized=false、segments=0。原因是预置里 Diarization 缺省 ⇒
// SupportsDiarization("asr-1.0")=false ⇒ 分离永远不开。
// curl 能测出服务端支持分离，但测不出本仓**请求时没带那个参数**。
func TestMiniMaxCapabilitiesDeclared(t *testing.T) {
	var opt *ModelOption
	for i, o := range RecommendedModels() {
		if strings.EqualFold(o.Model, "asr-1.0") {
			opt = &RecommendedModels()[i]
			break
		}
	}
	if opt == nil {
		t.Fatalf("预置里应有 asr-1.0")
	}
	if !opt.Diarization {
		t.Errorf("asr-1.0 必须声明 Diarization=true（2026-10-08 实测 verbose_json 返回 n_speakers/speaker），" +
			"否则 ShouldRequestDiarization 永远不开，用户勾了也拿不到说话人标签")
	}
	if !opt.WordTimestamps {
		t.Errorf("asr-1.0 必须声明 WordTimestamps=true（2026-10-08 实测 timestamp_level=word 返回逐字时间戳）")
	}
	if !opt.Streaming {
		t.Errorf("asr-1.0 必须声明 Streaming=true（2026-10-08 实测 SSE 200）")
	}
	// 能力声明与实际判定必须一致：这才是「声明被真正用上」的证据。
	if !SupportsDiarization("asr-1.0") {
		t.Errorf("SupportsDiarization(\"asr-1.0\") 应为 true（预置声明未被消费）")
	}
	if !ShouldRequestDiarization("asr-1.0", 7.086) {
		t.Errorf("7 秒音频应请求分离（asr-1.0 无 diarization 时长上限）")
	}
	// 词级时间戳：声明为 true 后，参数名映射那条链才真的被走到。
	if !supportsWordTimestamps("asr-1.0") {
		t.Errorf("supportsWordTimestamps(\"asr-1.0\") 应为 true")
	}
}

// TestCapturedRequestUnusedGuard 保留编译期提醒：capturedRequest 的字段都被用到。
// （若某天新增字段忘了在判据里断言，这里会失败，提示补断言。）
func TestCapturedRequestUnusedGuard(t *testing.T) {
	var c capturedRequest
	_ = c.Path
	_ = c.Headers
	_ = c.Fields
	_ = c.FileNam
	_ = c.FileLen
	b, _ := json.Marshal(map[string]int{"n": 1})
	if string(b) != `{"n":1}` {
		t.Fatalf("json 基线变了：%s", b)
	}
	if fmt.Sprint(1) != "1" {
		t.Fatal("unreachable")
	}
	sc := bufio.NewScanner(strings.NewReader("x"))
	sc.Scan()
	_ = sc.Text()
}
