// diarization_test.go —— MAI-Transcribe-2 说话人分离接线的行为门。
//
// §9.1 记着「三项能力需显式开参才生效，**本仓尚未接线该开参** ⇒ 当前选它
// 只拿到更好的文字，拿不到说话人标签」。本文件是把那句话变成事实的门。
//
// 本轮接线时新查到一条此前没人记的约束，它决定了整个设计：
// **MAI 开了 diarization 就只支持约 15 分钟以内的录音**（微软官方文档：
// 约 15 分钟及以上返回 408/500/503 diarization_unavailable，而同一段录音
// 关掉分离就能转成功）。本项目的主场景恰恰是会议，长会议是常态。
//
// ⇒ 所以本文件的核心不是「有没有发那三个开参」，而是三件事：
//  1. 该开的时候**真的**开了（断言上游收到的 multipart 字节，不是源码文本）；
//  2. 不该开的时候**不开**（长音频），否则长会议从「拿到更好的文字」
//     退化成「整段转写失败」——这是接线引入的新故障，比不接更糟；
//  3. 万一上游仍然拒绝，**降级而不是失败**：拿不到说话人，但文字必须还在。
//
// ★ 判据形态说明：本文件的断言全部落在「假上游真正收到什么」与
// 「Result 真正返回什么」上，不做源码文本匹配。理由见设计文档 §12.2
// 记录的五次「判据失明与通过同形」——判据要锚在被测对象自己的行为上。

package stt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// diarizationUpstream 是一个可编排的 /audio/transcriptions 假上游：
// 逐次调用按脚本返回，并记录每一次请求真正收到的 multipart 字段。
type diarizationUpstream struct {
	// replies 按调用次序返回；用完后重复最后一个。
	replies []diarizationReply
	calls   []capturedTranscription
}

type diarizationReply struct {
	status int
	body   string
}

type capturedTranscription struct {
	responseFormat  string
	wordGranularity string
	provider        string
	model           string
	language        string
	prompt          string
}

func (u *diarizationUpstream) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		body := string(raw)
		u.calls = append(u.calls, capturedTranscription{
			responseFormat:  multipartField(body, "response_format"),
			wordGranularity: multipartField(body, "timestamp_granularities[]"),
			provider:        multipartField(body, "provider"),
			model:           multipartModel(body),
			language:        multipartField(body, "language"),
			prompt:          multipartField(body, "prompt"),
		})
		idx := len(u.calls) - 1
		rep := u.replies[len(u.replies)-1]
		if idx < len(u.replies) {
			rep = u.replies[idx]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	})
	return mux
}

func maiTarget(srv *httptest.Server) *Target {
	return &Target{
		BaseURL: srv.URL + "/v1", APIKey: "k", Model: "microsoft/mai-transcribe-2",
		Transport: TransportTranscriptions, Channel: ChannelExternal,
		Language: "zh",
	}
}

// TestMAIShortAudioRequestsDiarization —— 短音频 + MAI ⇒ 三个开参全部真的发出去。
//
// 这是 §9.1 那句「尚未接线」的直接反面：接线前 response_format 恒为 json、
// 永不出现 timestamp_granularities 与 provider，所以三条断言在修复前**同时红**。
func TestMAIShortAudioRequestsDiarization(t *testing.T) {
	up := &diarizationUpstream{replies: []diarizationReply{{
		status: 200,
		body:   `{"text":"张三负责结算，李四跟进排期。","segments":[{"speaker":"0","text":"张三负责结算，","start":0.5,"end":2.0},{"speaker":"1","text":"李四跟进排期。","start":2.2,"end":4.0}]}`,
	}}}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	eng := engineFor(t, srv, maiTarget(srv))
	// 8 秒语音：远短于 900 秒的分离开关上限。
	res, err := eng.Transcribe(context.Background(), continuousWAV(16000, 8), "m.wav")
	if err != nil {
		t.Fatalf("转写失败: %v", err)
	}
	if len(up.calls) != 1 {
		t.Fatalf("应只发一次请求，实际 %d 次", len(up.calls))
	}
	got := up.calls[0]
	if got.responseFormat != "verbose_json" {
		t.Errorf("response_format = %q，期望 verbose_json（修复前恒为 json）", got.responseFormat)
	}
	if got.wordGranularity != "word" {
		t.Errorf("timestamp_granularities[] = %q，期望 word", got.wordGranularity)
	}
	if !strings.Contains(got.provider, `"diarization"`) || !strings.Contains(got.provider, `"enabled":true`) {
		t.Errorf("provider = %q，期望含 diarization.enabled=true", got.provider)
	}
	// 分离开关必须挂在 OpenRouter 透传给 Azure 的那层（provider.options.azure）。
	// 写成顶层字段上游会**静默忽略**且不报错——那正是「开了参但没生效」。
	var prov map[string]any
	if err := json.Unmarshal([]byte(got.provider), &prov); err != nil {
		t.Fatalf("provider 不是合法 JSON: %v", err)
	}
	azure, ok := prov["options"].(map[string]any)["azure"].(map[string]any)
	if !ok {
		t.Fatalf("provider 缺少 options.azure 层（实际 %q）——上游会静默忽略", got.provider)
	}
	dia, ok := azure["diarization"].(map[string]any)
	if !ok || dia["enabled"] != true {
		t.Errorf("options.azure.diarization.enabled 未置真（实际 %q）", got.provider)
	}
	// 中文简体偏置不能因为换了 response_format 就丢掉。
	if got.prompt != SimplifiedChineseBiasPrompt {
		t.Errorf("prompt = %q，期望仍带简体偏置", got.prompt)
	}
	if got.language != "zh" {
		t.Errorf("language = %q，期望 zh", got.language)
	}
	if res.Text == "" {
		t.Fatal("文本为空")
	}
}

// wavDeclaringDuration 造一个「头里声明了 seconds 秒」的 WAV。
//
// ★ 为什么不用 continuousWAV(16000, 1800)：那是 57 MB 的真实音频，
// 每条用例都要建一遍、传一遍，纯属浪费——而本条要验的判定
// （ShouldRequestDiarization）**只读 RIFF 头里的时长**。
// wavDurationSeconds 的算法是 `data 块声明的字节数 / byteRate`，
// 它不校验文件里真的有那么多字节，所以一个声明了 30 分钟的短 WAV
// 对被测代码而言与真实 30 分钟录音**同形**。
//
// 这是「照抄真实源的键形状」的同一个原则：夹具要和被测代码实际读的那部分同形，
// 而不是随手搓一个「看起来像音频」的字节串。
func wavDeclaringDuration(sampleRate, seconds int) []byte {
	// 16-bit mono ⇒ byteRate = sampleRate*2；data 声明字节数 = byteRate * seconds。
	var buf bytes.Buffer
	byteRate := uint32(sampleRate * 2)
	declared := byteRate * uint32(seconds)
	pcm := make([]byte, 64) // 极短的真实载荷，头部照常声明完整时长

	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+declared))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // mono
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, byteRate)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(2))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, declared)
	buf.Write(pcm)
	return buf.Bytes()
}

// TestMAILongAudioSkipsDiarizationKeepsText —— 本轮最要紧的一条。
//
// 30 分钟会议（> 900 秒）必须**不发**分离开关，但**照发** verbose_json 与词级时间戳。
// 断言的是「整段仍然转写成功」，不是「有没有发某个字段」。
func TestMAILongAudioSkipsDiarizationKeepsText(t *testing.T) {
	up := &diarizationUpstream{replies: []diarizationReply{{
		status: 200,
		body:   `{"text":"这是一场三十分钟的会议。"}`,
	}}}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	eng := engineFor(t, srv, maiTarget(srv))
	res, err := eng.Transcribe(context.Background(), wavDeclaringDuration(16000, 1800), "long.wav")
	if err != nil {
		t.Fatalf("长音频转写失败: %v", err)
	}
	if res.Text == "" {
		t.Fatal("长音频文本为空")
	}
	got := up.calls[0]
	if got.provider != "" {
		t.Errorf("长音频仍发了 provider=%q —— 上游会 503，整段转写直接失败", got.provider)
	}
	// 词级时间戳不受分离限制影响，仍应保留（更好的文字照拿）。
	if got.responseFormat != "verbose_json" {
		t.Errorf("长音频 response_format = %q，期望仍是 verbose_json（文字质量不该因关分离而退化）", got.responseFormat)
	}
	if res.Diarized {
		t.Error("Diarized = true，但本次根本没请求分离")
	}
}

// TestNonDiarizationModelUnaffected —— 不支持分离的模型必须完全不受影响。
//
// 判据的负控：防止「为了给 MAI 加能力，把所有模型都改成 verbose_json」这种
// 波及式改动。whisper 不认 timestamp_granularities，乱发会被上游拒。
func TestNonDiarizationModelUnaffected(t *testing.T) {
	up := &diarizationUpstream{replies: []diarizationReply{{
		status: 200, body: `{"text":"今天下午三点开项目评审会。"}`,
	}}}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	tgt := maiTarget(srv)
	tgt.Model = "openai/whisper-large-v3-turbo"
	eng := engineFor(t, srv, tgt)
	res, err := eng.Transcribe(context.Background(), continuousWAV(16000, 8), "m.wav")
	if err != nil {
		t.Fatalf("转写失败: %v", err)
	}
	got := up.calls[0]
	if got.responseFormat != "json" {
		t.Errorf("whisper 的 response_format = %q，期望保持 json", got.responseFormat)
	}
	if got.wordGranularity != "" || got.provider != "" {
		t.Errorf("whisper 不该收到增强开参（word=%q provider=%q）", got.wordGranularity, got.provider)
	}
	if res.Diarized || len(res.Segments) != 0 {
		t.Errorf("whisper 不该有说话人信息（diarized=%v segments=%d）", res.Diarized, len(res.Segments))
	}
}

// TestSpeakerSegmentsParsedWithMillis —— 解析上游分段，且秒→毫秒必须乘 1000。
//
// 漏乘 1000 的后果不是「报错」，而是时间戳变成 1970 年的头一秒——
// UI 上看起来只是「时间不太对」，极难发现。所以这里用一个
// 「乘 1000 与不乘必然不同」的取值。
func TestSpeakerSegmentsParsedWithMillis(t *testing.T) {
	up := &diarizationUpstream{replies: []diarizationReply{{
		status: 200,
		body: `{"text":"全段","segments":[
			{"speaker":"0","text":"第一句","start":0.5,"end":2.25},
			{"speaker":"1","text":"第二句","start":2.25,"end":4.0}]}`,
	}}}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	eng := engineFor(t, srv, maiTarget(srv))
	res, err := eng.Transcribe(context.Background(), continuousWAV(16000, 8), "m.wav")
	if err != nil {
		t.Fatalf("转写失败: %v", err)
	}
	if !res.Diarized {
		t.Error("Diarized = false，但上游确实给了 speaker")
	}
	if len(res.Segments) != 2 {
		t.Fatalf("segments = %d 条，期望 2", len(res.Segments))
	}
	first := res.Segments[0]
	if first.Speaker != "0" || first.Text != "第一句" {
		t.Errorf("首段 = %+v，期望 speaker=0 text=第一句", first)
	}
	if first.StartMS != 500 || first.EndMS != 2250 {
		t.Errorf("首段时间 = [%d,%d] ms，期望 [500,2250]（上游给的是秒，必须 ×1000）", first.StartMS, first.EndMS)
	}
	if res.Segments[1].Speaker != "1" {
		t.Errorf("第二段 speaker = %q，期望 1", res.Segments[1].Speaker)
	}
}

// TestSegmentsWithoutSpeakerKeepsTextButNotDiarized —— 有 segments ≠ 有说话人。
//
// 开了 verbose_json 后上游一定会给 segments，但单人录音时 speaker 是空的。
// 上层必须能区分这两种情况，且**不能因为 speaker 空就丢掉那段文字**。
func TestSegmentsWithoutSpeakerKeepsTextButNotDiarized(t *testing.T) {
	up := &diarizationUpstream{replies: []diarizationReply{{
		status: 200,
		body:   `{"text":"只有我一个人在说","segments":[{"speaker":"","text":"只有我一个人在说","start":0,"end":3}]}`,
	}}}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	eng := engineFor(t, srv, maiTarget(srv))
	res, err := eng.Transcribe(context.Background(), continuousWAV(16000, 8), "m.wav")
	if err != nil {
		t.Fatalf("转写失败: %v", err)
	}
	if res.Diarized {
		t.Error("Diarized = true，但上游没给任何 speaker")
	}
	if len(res.Segments) != 1 {
		t.Fatalf("segments = %d 条，期望保留 1 条（speaker 空不等于没有内容）", len(res.Segments))
	}
	if res.Segments[0].Text != "只有我一个人在说" {
		t.Errorf("段文本 = %q，期望原样保留", res.Segments[0].Text)
	}
}

// TestDiarizationRejectionDegradesInsteadOfFailing —— ★ 本轮最重要的一条。
//
// 上游对长录音 + 分离返回 503 diarization_unavailable，而同一段音频关掉
// 开关就能转成功。正确行为是**降级重试**：拿不到说话人，但文字必须还在。
//
// 判据锚在「用户最终拿到什么」上：第二次请求不能再带 provider（否则就是
// 拿同样的请求再撞一次墙），且 Result.Text 必须非空。
func TestDiarizationRejectionDegradesInsteadOfFailing(t *testing.T) {
	up := &diarizationUpstream{replies: []diarizationReply{
		{status: 503, body: `{"error":{"code":"diarization_unavailable","message":"diarization not supported for recordings over 15 minutes"}}`},
		{status: 200, body: `{"text":"关掉分离之后转出来的正文。"}`},
	}}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	eng := engineFor(t, srv, maiTarget(srv))
	res, err := eng.Transcribe(context.Background(), continuousWAV(16000, 8), "m.wav")
	if err != nil {
		t.Fatalf("上游拒绝了 diarization 应当降级而不是失败: %v", err)
	}
	if res.Text != "关掉分离之后转出来的正文。" {
		t.Errorf("降级后的文本 = %q", res.Text)
	}
	if len(up.calls) != 2 {
		t.Fatalf("应恰好两次请求（一次带分离、一次不带），实际 %d", len(up.calls))
	}
	if up.calls[0].provider == "" {
		t.Error("第一次请求应当带着分离开关")
	}
	if up.calls[1].provider != "" {
		t.Errorf("重试仍带 provider=%q —— 那是拿同样的请求再撞一次墙", up.calls[1].provider)
	}
	if up.calls[1].responseFormat != "json" {
		t.Errorf("重试的 response_format = %q，期望退回 json", up.calls[1].responseFormat)
	}
	if res.Diarized || len(res.Segments) != 0 {
		t.Error("降级路径不该声称拿到了说话人标签")
	}
}

// TestGenericUpstreamErrorDoesNotRetry —— 负控：真故障不能被当成「分离被拒」。
//
// 若只看状态码，限流/网关抖动（也是 500/503）会触发一次多余重试，
// 把一次故障变成两次，还可能把限流打得更狠。
func TestGenericUpstreamErrorDoesNotRetry(t *testing.T) {
	up := &diarizationUpstream{replies: []diarizationReply{{
		status: 503, body: `{"error":{"code":"rate_limit_exceeded","message":"too many requests"}}`,
	}}}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	eng := engineFor(t, srv, maiTarget(srv))
	if _, err := eng.Transcribe(context.Background(), continuousWAV(16000, 8), "m.wav"); err == nil {
		t.Fatal("限流错误应当如实报错，不能被降级逻辑吞掉")
	}
	if len(up.calls) != 1 {
		t.Errorf("普通 503 不应触发重试，实际发了 %d 次", len(up.calls))
	}
}

// TestUnknownDurationSkipsDiarization —— 时长解析不出来时不开分离。
//
// 非 WAV（webm/mp3/m4a）是本项目真机录到的格式，wavDurationSeconds 解析不出。
// 此时拿一个长度未知的请求去赌上游会不会 503 是危险赌注。
func TestUnknownDurationSkipsDiarization(t *testing.T) {
	up := &diarizationUpstream{replies: []diarizationReply{{status: 200, body: `{"text":"webm 录音"}`}}}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	eng := engineFor(t, srv, maiTarget(srv))
	if _, err := eng.Transcribe(context.Background(), []byte("\x1a\x45\xdf\xa3 not a wav at all"), "r.webm"); err != nil {
		t.Fatalf("转写失败: %v", err)
	}
	if up.calls[0].provider != "" {
		t.Errorf("时长未知却仍发了 provider=%q", up.calls[0].provider)
	}
}

// TestShouldRequestDiarization 纯判定表：把「模型 × 时长」这个决策本身钉住。
//
// 这一层单独测，是因为它是本轮设计的核心决策，且**没有网络**就能验。
func TestShouldRequestDiarization(t *testing.T) {
	cases := []struct {
		name  string
		model string
		secs  float64
		want  bool
	}{
		{"MAI 8 秒", "microsoft/mai-transcribe-2", 8, true},
		{"MAI 恰好 900 秒（等于上限）", "microsoft/mai-transcribe-2", 900, true},
		{"MAI 901 秒（超过上限）", "microsoft/mai-transcribe-2", 901, false},
		{"MAI 30 分钟会议", "microsoft/mai-transcribe-2", 1800, false},
		{"MAI 时长未知", "microsoft/mai-transcribe-2", 0, false},
		{"MAI 负时长", "microsoft/mai-transcribe-2", -1, false},
		{"whisper 不支持分离", "openai/whisper-large-v3-turbo", 8, false},
		{"未知模型", "some/unknown-asr", 8, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldRequestDiarization(tc.model, tc.secs); got != tc.want {
				t.Errorf("ShouldRequestDiarization(%q, %v) = %v，期望 %v",
					tc.model, tc.secs, got, tc.want)
			}
		})
	}
}

// TestIsDiarizationRejection 判定表：状态码与错误文本两条腿都要有牙。
func TestIsDiarizationRejection(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"503 + diarization_unavailable", 503, `{"code":"diarization_unavailable"}`, true},
		{"408 + speaker 关键词", 408, `{"message":"speaker tracking failed"}`, true},
		{"500 + diarization", 500, `diarization`, true},
		{"503 + 限流（不是分离问题）", 503, `{"code":"rate_limit_exceeded"}`, false},
		{"503 + 空体", 503, ``, false},
		{"400 + diarization（状态码不对）", 400, `diarization_unavailable`, false},
		{"200 正常（不该判为拒绝）", 200, `diarization`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDiarizationRejection(tc.status, []byte(tc.body)); got != tc.want {
				t.Errorf("isDiarizationRejection(%d, %q) = %v，期望 %v",
					tc.status, tc.body, got, tc.want)
			}
		})
	}
}

// TestMAISettingsPageNoLongerClaimsUnwired —— 设置页文案必须与实现同步。
//
// ★ 这条单看着像「文案断言」，实则是本项目吃过亏的那一类：Accuracy 字段
// 直接显示给用户，它说「本仓尚未接线该开参」，而代码一旦接线就会留下
// 「明明能分离却告诉用户不能」的误导。所以接线与改文案必须同一次提交。
func TestMAISettingsPageNoLongerClaimsUnwired(t *testing.T) {
	for _, o := range RecommendedExternalModels() {
		if o.Model != "microsoft/mai-transcribe-2" {
			continue
		}
		if !o.Diarization || !o.WordTimestamps {
			t.Errorf("MAI 能力未登记（diarization=%v word=%v）", o.Diarization, o.WordTimestamps)
		}
		if o.DiarizationMaxSeconds != 900 {
			t.Errorf("DiarizationMaxSeconds = %d，期望 900（微软文档的约 15 分钟上限）", o.DiarizationMaxSeconds)
		}
		for _, stale := range []string{"尚未接线", "未接线该开参"} {
			if strings.Contains(o.Accuracy, stale) {
				t.Errorf("Accuracy 仍含过期文案 %q —— 代码已接线，文案必须同步", stale)
			}
		}
		// ★ 这里刻意**不**断言「拿不到说话人标签」这类措辞不存在：
		// 它在「长会议自动关掉分离」的限定句里是**准确**的。
		// 第一版把该子串一并拉黑，结果自己造了个假红——判据的窗口开太大
		// 会误判正确表述（与设计文档 §13.5「判据越界」同源）。
		// 真正过期的只有「尚未接线」这种描述**当前接线状态**的句子。
		if strings.Contains(o.Accuracy, "尚未接线") {
			t.Error("Accuracy 仍在描述接线前的状态")
		}
		// 长录音的限制必须**留在文案里**，不能因为实现了降级就当它不存在：
		// 用户需要知道长会议为什么拿不到说话人标签。
		if !strings.Contains(o.Accuracy, "15 分钟") {
			t.Error("Accuracy 未说明分离开关的约 15 分钟限制")
		}
		if !strings.Contains(o.Accuracy, "长录音") && !strings.Contains(o.Accuracy, "长会议") {
			t.Error("Accuracy 未说明长录音会自动关掉分离")
		}
		return
	}
	t.Fatal("预置清单里找不到 microsoft/mai-transcribe-2")
}
