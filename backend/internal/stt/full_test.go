package stt

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// pcmWAV 造一段可解析的 16-bit PCM 单声道 WAV。
//
// speechSec 秒"语音"（振幅 0.3）+ silenceSec 秒静音（振幅 0），交替拼接，
// 用来验证「按静音切」而不是「按固定长度切」。
func pcmWAV(sampleRate int, speechSec, silenceSec, cycles int) []byte {
	var pcm bytes.Buffer
	full := int(math.Round(float64(sampleRate) * float64(speechSec+silenceSec)))
	per := int(math.Round(float64(sampleRate) * float64(speechSec)))
	sil := full - per
	for c := 0; c < cycles; c++ {
		for i := 0; i < per; i++ {
			writeSample(&pcm, 0.3)
		}
		for i := 0; i < sil; i++ {
			writeSample(&pcm, 0.0)
		}
	}
	return wrapWAV(sampleRate, pcm.Bytes())
}

func writeSample(buf *bytes.Buffer, amp float64) {
	v := int16(amp * 32767 * math.Sin(float64(buf.Len())*0.05))
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], uint16(v))
	buf.Write(b[:])
}

func wrapWAV(sampleRate int, pcm []byte) []byte {
	var buf bytes.Buffer
	byteRate := uint32(sampleRate * 2)
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+len(pcm)))
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
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)
	return buf.Bytes()
}

// continuousWAV 造一段全程有声的 WAV（连续讲话的反例：没有任何静音点）。
func continuousWAV(sampleRate, seconds int) []byte {
	var pcm bytes.Buffer
	for i := 0; i < sampleRate*seconds; i++ {
		writeSample(&pcm, 0.3)
	}
	return wrapWAV(sampleRate, pcm.Bytes())
}

func TestSplitWAVCutsOnSilenceNotFixedLength(t *testing.T) {
	wav := pcmWAV(16000, 2, 1, 3) // 9 秒
	segs, ok := SplitWAV(wav, 4)
	if !ok {
		t.Fatal("SplitWAV 对 16-bit PCM WAV 应返回 ok")
	}
	if len(segs) < 2 {
		t.Fatalf("9 秒音频在 4 秒上限下应至少切 2 段，实际 %d", len(segs))
	}
	for i, s := range segs {
		if s.Index != i || s.Total != len(segs) {
			t.Errorf("段 %d 的序号/总数不对: index=%d total=%d", i, s.Index, s.Total)
		}
		if s.EndSec <= s.StartSec {
			t.Errorf("段 %d 时间范围非法: %v - %v", i, s.StartSec, s.EndSec)
		}
		if len(s.Audio) == 0 {
			t.Errorf("段 %d 音频为空", i)
		}
	}
	silentCuts := 0
	for _, s := range segs {
		if s.SilenceCut {
			silentCuts++
		}
	}
	if silentCuts == 0 {
		t.Errorf("有静音间隔的音频应至少有一段标为 SilenceCut（实际全为硬切）")
	}
}

func TestSplitWAVNeverExceedsMaxSegment(t *testing.T) {
	// 全程有声（无静音点）→ 只能硬切，但绝不能超过上限，
	// 否则会撞上游 30 秒限制（智谱）导致整次转写失败。
	wav := continuousWAV(16000, 10)
	segs, ok := SplitWAV(wav, 3)
	if !ok {
		t.Fatal("SplitWAV 应接受自造 16-bit PCM WAV")
	}
	if len(segs) < 4 {
		t.Fatalf("10 秒在 3 秒上限下应切出至少 4 段，实际 %d", len(segs))
	}
	for i, s := range segs {
		if d := s.EndSec - s.StartSec; d > 3.05 {
			t.Errorf("段 %d 时长 %.2fs 超过上限 3s（会撞上游 30 秒限制）", i, d)
		}
		if s.SilenceCut {
			t.Errorf("段 %d 标记了 SilenceCut，但输入没有任何静音", i)
		}
	}
}

// waveTrain 造一段「语音—静音—语音」交替的 WAV，音量由 amp 控制。
//
// 与 pcmWAV 的区别是**可以控制音量**：静音判定卡在 0.012 这个门槛上，
// 必须用不同音量的样本才能验出「安静但不是静音」有没有被误判。
func waveTrain(sampleRate, speechSec, silenceSec, cycles int, amp float64) []byte {
	var pcm bytes.Buffer
	per := sampleRate * speechSec
	sil := sampleRate * silenceSec
	for c := 0; c < cycles; c++ {
		for i := 0; i < per; i++ {
			writeSample(&pcm, amp)
		}
		for i := 0; i < sil; i++ {
			writeSample(&pcm, 0)
		}
	}
	return wrapWAV(sampleRate, pcm.Bytes())
}

// TestSplitWAVDoesNotTreatQuietSpeechAsSilence 钉住「均方 vs 均方根」这个坑。
//
// frameRMS 省掉 sqrt 返回均方，主循环却拿它直接和按均方根定义的 silenceFloor
// 比，等效门槛被放大到 sqrt(0.012)≈0.11。后果是**所有安静的说话声都被当成
// 静音**：79 秒会议语料被切成 16 段（平均 4.9 秒），句子照样被劈开。
//
// 这里的 amp=0.05 正弦，RMS≈0.035：高于设计门槛 0.012，远低于被放大后的 0.11。
// 正确实现应当认定它全程有声 → 12 秒在 25 秒上限下切成 1 段。
func TestSplitWAVDoesNotTreatQuietSpeechAsSilence(t *testing.T) {
	wav := waveTrain(16000, 12, 0, 1, 0.05) // 12 秒轻声「说话」，中途无任何静音
	segs, ok := SplitWAV(wav, defaultSegmentSec)
	if !ok {
		t.Fatal("SplitWAV 应接受该 WAV")
	}
	if len(segs) != 1 {
		var cuts []int
		for _, s := range segs {
			cuts = append(cuts, int(s.StartSec*100))
		}
		t.Errorf("全程有声且时长未超上限，应当只切出 1 段，实际 %d 段（起点 %v）——"+
			"安静的说话声被误判成静音了", len(segs), cuts)
	}
}

// TestSplitWAVPrefersRealPauseOverIntraSentenceBreath 钉住切点门槛。
//
// 真人/TTS 语音里，句内换气是 100–600ms，句间停顿是 800ms 以上（对 79 秒
// 会议语料实测的分布：句内 100–600ms，句间 1575–3475ms）。门槛若定在 300ms，
// 句内换气会全部变成切点 —— 实测同一份语料从 10 段（句界对齐）碎成 14 段。
//
// 场景：12s 说话 → 0.6s 换气 → 7.7s 说话 → 1.2s 句间停顿 → 12s 说话。
// 期望：只在 1.2s 那个停顿处切一刀，共 2 段；0.6s 换气处不许切。
func TestSplitWAVPrefersRealPauseOverIntraSentenceBreath(t *testing.T) {
	const rate = 16000
	var pcm bytes.Buffer
	// ms 版静音：句内换气 600ms、句间停顿 1200ms。
	//
	// 换气取 600ms 而不是更短的 300ms：25ms 滑动窗口要排空上一段语音的能量，
	// 300ms 的间隙在窗口 RMS 上**根本看不出来**（实测把门槛调回 300ms 时
	// 这个用例照样绿）。600ms 是 79 秒会议语料里实测到的**句内停顿上界**，
	// 用它才能真正区分「句内换气」与「句间停顿」。
	tone := func(sec float64) {
		for i := 0; i < int(float64(rate)*sec); i++ {
			writeSample(&pcm, 0.3)
		}
	}
	quiet := func(sec float64) {
		for i := 0; i < int(float64(rate)*sec); i++ {
			writeSample(&pcm, 0)
		}
	}
	tone(12)   // 0 – 12
	quiet(0.6) // 句内换气，12 – 12.6
	tone(7.7)  // 12.6 – 20.3
	quiet(1.2) // 句间停顿，20.3 – 21.5
	tone(12)   // 21.5 – 33.5
	wav := wrapWAV(rate, pcm.Bytes())

	segs, ok := SplitWAV(wav, defaultSegmentSec)
	if !ok {
		t.Fatal("SplitWAV 应接受该 WAV")
	}
	if len(segs) != 2 {
		var desc []string
		for _, s := range segs {
			desc = append(desc, fmt.Sprintf("[%.2f-%.2f silenceCut=%v]", s.StartSec, s.EndSec, s.SilenceCut))
		}
		t.Fatalf("应当只在 20.3s 的句间停顿处切一刀（共 2 段），实际 %d 段：%v",
			len(segs), desc)
	}
	// 切点必须落在 20.3–21.5 之间，而不是 12.0–12.3 的换气处。
	if s := segs[0].EndSec; s < 20.0 || s > 21.6 {
		t.Errorf("第一段应在句间停顿处结束（20.3–21.5s），实际结束于 %.2fs", s)
	}
	if !segs[0].SilenceCut {
		t.Error("在静音处切的段应标记 SilenceCut")
	}
}

func TestSplitWAVSegmentsAreValidWAVAndLossless(t *testing.T) {
	wav := pcmWAV(16000, 2, 1, 4) // 12 秒
	segs, ok := SplitWAV(wav, 3)
	if !ok {
		t.Fatal("SplitWAV 应成功")
	}
	var totalPayload int
	for i, s := range segs {
		if string(s.Audio[0:4]) != "RIFF" || string(s.Audio[8:12]) != "WAVE" {
			t.Fatalf("段 %d 不是合法 WAV 头", i)
		}
		// RIFF 块长度（偏移 4）必须等于「文件总长 − 8」。
		//
		// 这一条以前**从来没被校验过**，所以 buildWAV 里 +8 的错误一直假绿：
		// parsePCMWAV 对长度是宽容的（自己写的解析器当然读得动），
		// 宽容的上游解码器（PyAV/ffmpeg）也只是默默截掉末尾 8 字节。
		// 2026-10-01 用真 ASR 引擎跑会议长录音时，逐段比对文件头才发现。
		if got, want := int(binary.LittleEndian.Uint32(s.Audio[4:8])), len(s.Audio)-8; got != want {
			t.Errorf("段 %d 的 RIFF 块长度 = %d，应为 文件总长-8 = %d（差 %d 字节）",
				i, got, want, got-want)
		}
		// data 段长度字段必须与实际载荷一致（切分代码最常见的 bug）
		payload, rate, bits, ch, _, ok := parsePCMWAV(s.Audio)
		if !ok {
			t.Fatalf("段 %d 无法被自己的解析器解析", i)
		}
		if rate != 16000 || bits != 16 || ch != 1 {
			t.Errorf("段 %d 格式与原音频不一致: rate=%d bits=%d ch=%d", i, rate, bits, ch)
		}
		totalPayload += len(payload)
	}
	// 拼接后应等于原始载荷（无字节丢失/重复）——这是切分正确性的硬证据。
	origPayload, _, _, _, _, ok := parsePCMWAV(wav)
	if !ok {
		t.Fatal("原始 WAV 无法解析")
	}
	if totalPayload != len(origPayload) {
		t.Errorf("切分后总载荷 %d != 原始 %d（切分有丢字节或重复）", totalPayload, len(origPayload))
	}
}

func TestSplitWAVRejectsNonPCM(t *testing.T) {
	wav := continuousWAV(16000, 1)
	cases := map[string][]byte{
		"空输入":     {},
		"非 RIFF":  []byte("ID3\x04not a wav file at all........"),
		"截断的 WAV": wav[:20],
		"webm 容器": {0x1A, 0x45, 0xDF, 0xA3, 0x01, 0x02, 0x03, 0x04},
	}
	for name, data := range cases {
		if _, ok := SplitWAV(data, 25); ok {
			t.Errorf("%s 不应被判为可切分（会得到解码失败的碎片）", name)
		}
	}
}

// stubASR 是假上游：按收到的音频顺序返回「第N段」，指定序号的段返回 500。
type stubASR struct {
	failIndexes map[int]bool
	seen        []float64 // 每个请求的音频秒数
	langs       []string  // 每个请求收到的 language 字段
	mu          sync.Mutex
}

func (s *stubASR) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	body := readMultipartAudio(r)
	secs, _ := wavDurationSeconds(body)
	s.seen = append(s.seen, secs)
	// readMultipartAudio 已经调过 ParseMultipartForm，这里能直接读普通字段。
	s.langs = append(s.langs, r.FormValue("language"))

	idx := len(s.seen) - 1
	if s.failIndexes[idx] {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream boom"}}`))
		return
	}
	_, _ = w.Write([]byte(`{"text":"seg` + string(rune('0'+idx%10)) + `"}`))
}

func readMultipartAudio(r *http.Request) []byte {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		return nil
	}
	defer f.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(f); err != nil {
		return nil
	}
	return buf.Bytes()
}

func newStubTranscriber(srvURL string) *Transcriber {
	return NewResolver(func(context.Context, Scope) (*Target, error) {
		return &Target{
			BaseURL: srvURL, APIKey: "k", Model: "stub-model",
			Transport: TransportTranscriptions, Channel: ChannelExternal,
		}, nil
	})
}

func TestTranscribeFullAggregatesSegmentsInOrder(t *testing.T) {
	stub := &stubASR{failIndexes: map[int]bool{}}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	tr := newStubTranscriber(srv.URL)
	// 20 个「2 秒语音 + 1 秒静音」周期 = 60 秒 > 25 秒默认上限 → 必须切成多段。
	// （用 18 秒这类短样本会得到 1 段，那是正确行为而不是 bug——测试要覆盖
	//  「超过上游限制」这条路径，样本必须真的超限。）
	wav := pcmWAV(16000, 2, 1, 20) // 60 秒
	res, err := tr.TranscribeFull(context.Background(), Scope{}, wav, "a.wav")
	if err != nil {
		t.Fatalf("全量转写失败: %v", err)
	}
	if len(res.Segments) < 2 {
		t.Fatalf("60 秒音频在 25 秒上限下应切出多段，实际 %d", len(res.Segments))
	}
	for i := 1; i < len(res.Segments); i++ {
		if res.Segments[i].StartSec < res.Segments[i-1].StartSec {
			t.Errorf("段顺序错乱: 段 %d 起点 %.2f 早于段 %d 的 %.2f",
				i, res.Segments[i].StartSec, i-1, res.Segments[i-1].StartSec)
		}
	}
	if !strings.Contains(res.Text, "seg0") {
		t.Errorf("聚合文本应含首段内容: %q", res.Text)
	}
	if res.Failed != 0 {
		t.Errorf("不该有失败段，实际 %d", res.Failed)
	}
	// 每一段发给上游的音频都必须在最紧的上游限制内
	for i, secs := range stub.seen {
		if secs > 30.5 {
			t.Errorf("第 %d 段发给上游 %.2fs，超过最紧的 30 秒限制", i, secs)
		}
	}
}

func TestTranscribeFullKeepsOtherSegmentsWhenOneFails(t *testing.T) {
	// 核心契约：一段失败不能让整场会议的内容消失。
	stub := &stubASR{failIndexes: map[int]bool{1: true}}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	tr := newStubTranscriber(srv.URL)
	wav := pcmWAV(16000, 2, 1, 20) // 60 秒 → 多段
	res, err := tr.TranscribeFull(context.Background(), Scope{}, wav, "a.wav")
	if err != nil {
		t.Fatalf("有段失败时整体仍应成功（保留成功段）: %v", err)
	}
	if res.Failed != 1 {
		t.Errorf("应恰好 1 段失败，实际 %d", res.Failed)
	}
	if !strings.Contains(res.Text, "seg0") {
		t.Errorf("失败段之前的成功内容必须保留: %q", res.Text)
	}
	if !strings.Contains(res.Text, "转写失败") {
		t.Errorf("失败段应在聚合文本里留显式占位，否则用户以为内容完整: %q", res.Text)
	}
	found := false
	for _, s := range res.Segments {
		if s.Error != "" {
			found = true
		}
	}
	if !found {
		t.Error("Segments 明细里应标出失败段")
	}
}

func TestTranscribeFullFallsBackToSingleWhenNotSplittable(t *testing.T) {
	stub := &stubASR{failIndexes: map[int]bool{}}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	tr := newStubTranscriber(srv.URL)
	// webm 字节：不可切分 → 必须整段走单次转写，而不是返回错误
	webm := []byte{0x1A, 0x45, 0xDF, 0xA3, 0x01, 0x02, 0x03, 0x04, 0x05}
	_, _ = tr.TranscribeFull(context.Background(), Scope{}, webm, "a.webm")
	if len(stub.seen) != 1 {
		t.Errorf("不可切分时应只发 1 次请求，实际 %d", len(stub.seen))
	}
}

func TestTranscribeFullErrorsWhenEverySegmentFails(t *testing.T) {
	all := map[int]bool{}
	for i := 0; i < 20; i++ {
		all[i] = true
	}
	stub := &stubASR{failIndexes: all}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	tr := newStubTranscriber(srv.URL)
	wav := pcmWAV(16000, 2, 1, 20) // 60 秒 → 多段
	if _, err := tr.TranscribeFull(context.Background(), Scope{}, wav, "a.wav"); err == nil {
		t.Fatal("全部段失败时必须报错（返回空文本会让上层误以为整段无内容而清空记录）")
	}
}

func TestKnownMaxSecondsCoversTightestProvider(t *testing.T) {
	if got := KnownMaxSeconds("glm-asr-2512"); got != 30 {
		t.Errorf("glm-asr-2512 单次上限应为 30 秒，实际 %d", got)
	}
	if got := KnownMaxSeconds("asr-1.0"); got != 500 {
		t.Errorf("MiniMax asr-1.0 单次上限应为 500 秒，实际 %d", got)
	}
	if KnownMaxSeconds("不存在的模型") != 0 {
		t.Error("未知模型应返回 0（不猜上限）")
	}
}

func TestSupportsStreamingMatchesResearch(t *testing.T) {
	// 调研结论：只有 MiniMax 与智谱支持服务端真流式；
	// OpenRouter 转写端点不支持（上游约 60 秒超时）。
	for _, m := range []string{"asr-1.0", "glm-asr-2512"} {
		if !SupportsStreaming(m) {
			t.Errorf("%s 应标记为支持流式", m)
		}
	}
	for _, m := range []string{"qwen/qwen3-asr-0.6b", "openai/whisper-large-v3-turbo", "gpt-4o-transcribe"} {
		if SupportsStreaming(m) {
			t.Errorf("%s 不支持流式，不应标记为支持", m)
		}
	}
}

func TestRecommendedExternalModelsAreSortedByCost(t *testing.T) {
	opts := RecommendedExternalModels()
	for i := 1; i < len(opts); i++ {
		if opts[i].USDPerHour < opts[i-1].USDPerHour {
			t.Errorf("预置未按成本升序：%s($%.3f) 排在 %s($%.3f) 之后",
				opts[i].Model, opts[i].USDPerHour, opts[i-1].Model, opts[i-1].USDPerHour)
		}
	}
	have := map[string]bool{}
	for _, o := range opts {
		have[o.BaseURL] = true
	}
	// ⚠ 2026-10-08：MiniMax 那条从 api.minimaxi.com/v1（国际站）改成
	// api.minimax.cn（国内站）。实测踩到的坑是国内 key 打国际站会 401，
	// 而错误文案是「login fail: Please carry the API secret key…」，
	// 指向「key 没带」而不是「打错了站」—— 用户会去反复检查 key。
	// 与 provider_test.go 的 TestMiniMaxDefaultBaseURLIsDomesticStation 同源。
	for _, want := range []string{
		"https://openrouter.ai/api/v1",
		"https://api.minimax.cn",
		"https://open.bigmodel.cn/api/paas/v4",
	} {
		if !have[want] {
			t.Errorf("预置缺少 %s 的候选", want)
		}
	}
}

// TestTranscribeFullSendsLanguageOnEverySegment 长会议全量转写是**逐段**发上游的，
// 所以「language 有没有被带上」必须在每段上都成立，不能只看第一段或只看单次转写。
//
// 这条会随实现演进而失效：哪天有人给 TranscribeFull 加一条不经过
// TranscribeFor 的快速路径，语种就会在长会议上悄悄丢掉——而单次转写的
// 那条测试仍然是绿的，正是最典型的假绿。
func TestTranscribeFullSendsLanguageOnEverySegment(t *testing.T) {
	stub := &stubASR{}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	tr := NewResolver(func(context.Context, Scope) (*Target, error) {
		return &Target{
			BaseURL: srv.URL + "/v1", APIKey: "k", Model: "stub-model",
			Transport: TransportTranscriptions, Channel: ChannelExternal,
		}, nil
	})
	tr.SetHTTPClient(srv.Client())

	// 36 秒（10 秒有声 + 2 秒静音，重复 3 次）。默认切段上限 25 秒，
	// 所以必然切成 ≥2 段——段数不够就说明测不到逐段行为，必须直接失败。
	res, err := tr.TranscribeFull(context.Background(), Scope{}, pcmWAV(16000, 10, 2, 3), "meeting.wav")
	if err != nil {
		t.Fatalf("TranscribeFull: %v", err)
	}
	if len(stub.langs) < 2 {
		t.Fatalf("这段音频应被切成多段，实际只发了 %d 段，测不到逐段行为", len(stub.langs))
	}
	for i, lang := range stub.langs {
		if lang != DefaultLanguage {
			t.Errorf("第 %d 段 language=%q，want %q（长会议逐段请求缺语种 = 中文被按英语转写）",
				i, lang, DefaultLanguage)
		}
	}
	if res == nil || res.Text == "" {
		t.Errorf("全量转写没有聚合出文本：%+v", res)
	}
}

// TestTranscribeFullSkipsTooShortSegments 是 minUsefulSegmentSec 的判据。
//
// §604 变异实测（锚点命中 1 次、跑的是整个 internal/stt 包）：
//
//	minUsefulSegmentSec: 1.0 → 0    全包 EXIT=0、0 条打红   ⇒ 该阈值**零判据**
//	minUsefulSegmentSec: 1.0 → 5.0  全包 EXIT=1、1 条打红   ⇒ 只有「滤得更狠」那侧有判据
//
// 即**方向不对称**：把碎片段照送上游（白花一次计费调用、并在聚合结果里留空段，
// 正是本函数上方注释自己写下的代价）这一侧，**没有任何测试会红**。
//
// 本条用**行为**而不是常量相等来钉它：断言「短于阈值的段一次都不打上游」，
// 并带一条阳性对照（略长于阈值的段**必须**被打上游），免得这条测试
// 因为「什么都没发」而恒绿。
func TestTranscribeFullSkipsTooShortSegments(t *testing.T) {
	build := func(sec float64) []byte {
		var pcm bytes.Buffer
		n := int(math.Round(float64(16000) * sec))
		for i := 0; i < n; i++ {
			writeSample(&pcm, 0.3)
		}
		return wrapWAV(16000, pcm.Bytes())
	}

	cases := []struct {
		name      string
		sec       float64
		wantCalls bool
	}{
		{"0.5 秒纯语音：不许打上游", 0.5, false},
		{"1.5 秒纯语音：必须打上游", 1.5, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubASR{failIndexes: map[int]bool{}}
			srv := httptest.NewServer(stub)
			defer srv.Close()

			tr := newStubTranscriber(srv.URL)
			res, err := tr.TranscribeFull(context.Background(), Scope{}, build(tc.sec), "a.wav")

			if tc.wantCalls {
				if err != nil {
					t.Fatalf("%.2fs 已在阈值之上，转写却失败: %v", tc.sec, err)
				}
				if len(stub.seen) == 0 {
					// ⚠ 必须显式转 float64：minUsefulSegmentSec 是**无类型常量**，
					// 写成 `%.1f` 时只在它是 `1.0` 这种浮点写法时才能编过；
					// 变异把它改成整数 `0` 之后 go vet 会直接判
					//「format %.1f has arg of wrong type int」而**编译失败** ——
					// 那样看到的是「变异打红了」，其实是我的判据自己先坏了。
					threshold := float64(minUsefulSegmentSec)
					t.Errorf("%.2fs 已高于 minUsefulSegmentSec（%.1f）却一次上游都没打："+
						"过滤过头，长段也被吞了", tc.sec, threshold)
				}
				return
			}

			// 短段：一次都不许打上游。
			// 全被跳过时 TranscribeFull 会返回错误（"全部段被判定为纯静音而跳过"），
			// 这本身就是要断言的行为，不算失败。
			if len(stub.seen) != 0 {
				t.Fatalf("%.2fs 的短段被切成了 %d 段并打到了上游（各 %.3fs）："+
					"minUsefulSegmentSec 的过滤没生效，等于白花计费调用并留空段",
					tc.sec, len(stub.seen), stub.seen[0])
			}
			if res != nil && res.Succeeded != 0 {
				t.Errorf("短段不应计成功，实际 Succeeded=%d", res.Succeeded)
			}
		})
	}
}

// TestSplitWAVKeepsSentencesWholeWithoutSegmentExplosion 是 minSilenceCutSec 的判据。
//
// §610 变异实测（锚点命中 1，每档跑**整个包**）：
//
//	minSilenceCutSec: 5 -> 1   全包 EXIT=0、0 条打红   ⇒ 下界（段数）方向零判据
//	minSilenceCutSec: 5 -> 30  全包 EXIT=0、0 条打红   ⇒ 上界（句子完整）方向零判据
//
// 也就是说 full.go:133-144 那段注释里的承诺
// 「修复后 9 句话**每一句都完整落在单个返回段里**」「取 5 秒是权衡：再小会让段数暴涨」
// **一个字都没有测试在断** —— 而它是本文件里承诺最硬的一段注释。
//
// 本条用**行为**把它钉住，两个方向各一条：
//
//	① 完整闸（钉上界）：8 秒语音句 + 1 秒间隙 ×4。切点必须落在**间隙里**
//	  （SilenceCut=true），且每个句子必须完整落在**某一个**段内。
//	  minSilenceCutSec 调到 30 时 minCutFrames 变成 18.75s，静音切点来不及生效，
//	  退化成 25 秒硬切 ⇒ 第 3 句（18.0–26.0s）被劈成两半 ⇒ 红。
//
//	② 成本闸（钉下界）：3 秒语音句 + 1 秒间隙 ×6（共 24 秒语音）。
//	  每段都要付一次上游调用与计费；阈值调小到 1s 会把每句话各切一段，
//	  段数从 4 涨到 7 ⇒ 红。
func TestSplitWAVKeepsSentencesWholeWithoutSegmentExplosion(t *testing.T) {
	t.Run("完整闸：句子不得被 25 秒硬切劈开", func(t *testing.T) {
		const speech, gap, cycles = 8, 1, 4
		period := float64(speech + gap)
		segs, ok := SplitWAV(pcmWAV(16000, speech, gap, cycles), 0)
		if !ok || len(segs) == 0 {
			t.Fatal("切分失败")
		}
		// 第 k 句占 [k*period, k*period+speech]，必须被某一个段完整包含。
		for k := 0; k < cycles; k++ {
			sStart := float64(k) * period
			sEnd := sStart + speech
			contained := false
			for _, sg := range segs {
				if sg.StartSec <= sStart+1e-6 && sg.EndSec >= sEnd-1e-6 {
					contained = true
					break
				}
			}
			if !contained {
				segDesc := make([]string, 0, len(segs))
				for _, sg := range segs {
					segDesc = append(segDesc, formatRange(sg.StartSec, sg.EndSec, sg.SilenceCut))
				}
				t.Errorf("第 %d 句（%.1f–%.1fs）被切开了 —— 它没有完整落在任何一个段内。"+
					"这正是 minSilenceCutSec 注释里承诺「每一句都完整落在单个返回段里」要防的事。"+
					"实测段形: %v", k+1, sStart, sEnd, segDesc)
			}
		}
		// 正向要求：切点必须真的落在静音里（硬切不算数）。
		silenceCuts := 0
		for _, sg := range segs {
			if sg.SilenceCut {
				silenceCuts++
			}
		}
		if silenceCuts == 0 {
			t.Error("一个静音切点都没有 ⇒ 静音感知分支已退化成纯 25 秒硬切")
		}
	})

	t.Run("成本闸：短句不得被逐句切成段", func(t *testing.T) {
		const speech, gap, cycles = 3, 1, 6
		segs, ok := SplitWAV(pcmWAV(16000, speech, gap, cycles), 0)
		if !ok {
			t.Fatal("切分失败")
		}
		// 24 秒语音在任何合理阈值下都不该超过 5 段（现状 4 段）。
		// 阈值降到 1s 时实测切出 7 段，每段都要付一次上游调用与计费。
		if len(segs) > 5 {
			dur := 0
			for _, sg := range segs {
				if sg.IsUsefulSegment() {
					dur++
				}
			}
			t.Errorf("24 秒语音被切成了 %d 段（其中 %d 段会真的打上游）—— "+
				"minSilenceCutSec 太小会把每句话各切一段，成本与延迟都上升",
				len(segs), dur)
		}
	})
}

func formatRange(a, b float64, silenceCut bool) string {
	return fmt.Sprintf("%.2f–%.2fs(silenceCut=%v)", a, b, silenceCut)
}
