package stt

import (
	"bytes"
	"context"
	"encoding/binary"
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
	mu          sync.Mutex
}

func (s *stubASR) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	body := readMultipartAudio(r)
	secs, _ := wavDurationSeconds(body)
	s.seen = append(s.seen, secs)

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
	// 核心契约：一段失败不能��让整场会议的内容消失。
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
	for _, want := range []string{
		"https://openrouter.ai/api/v1",
		"https://api.minimaxi.com/v1",
		"https://open.bigmodel.cn/api/paas/v4",
	} {
		if !have[want] {
			t.Errorf("预置缺少 %s 的候选", want)
		}
	}
}
