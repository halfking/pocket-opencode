// 真网关 ASR 探针（默认 skip）。验「仓自己的 STT 代码路径」在真实
// 转写端点上能不能把音频转成文字。
//
// 为什么需要它：本轮之前，ASR 这条链**从未在真实端点上跑过** ———
// 全部是单测 + 变异。2026-10-06 真网关复测时发现同一个网关
// （llmgo.kxpms.cn）除 LLM 外还提供 `/v1/audio/transcriptions`，
// 且 `mimo-v2.5-asr` 真的能转写。这个探针验的是「不用新申请 API key、
// 直接复用已配置的 LLM 网关」这条路在仓内是通的。
//
// 运行：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=https://llmgo.kxpms.cn/v1 \
//	POCKET_LLM_GATEWAY_API_KEY=... \
//	POCKET_LIVE_ASR_AUDIO=/tmp/gt-voice-16k.wav \
//	POCKET_LIVE_ASR_MODEL=mimo-v2.5-asr \
//	go test ./internal/stt -run TestLiveGatewayASR -v
package stt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveGatewayASR(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关 ASR 探针：需 POCKET_LIVE_GATEWAY=1")
	}
	baseURL := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_URL"))
	apiKey := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_API_KEY"))
	audioPath := strings.TrimSpace(os.Getenv("POCKET_LIVE_ASR_AUDIO"))
	model := strings.TrimSpace(os.Getenv("POCKET_LIVE_ASR_MODEL"))
	if baseURL == "" || apiKey == "" {
		t.Fatal("缺少 POCKET_LLM_GATEWAY_URL / _API_KEY")
	}
	if audioPath == "" {
		audioPath = "/tmp/gt-voice-16k.wav"
	}
	if model == "" {
		model = "mimo-v2.5-asr"
	}

	audio, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatalf("读音频 %s：%v", audioPath, err)
	}
	t.Logf("音频=%s（%d 字节）模型=%s 网关=%s",
		filepath.Base(audioPath), len(audio), model, baseURL)

	// 走生产入口：NewTranscriber(apiKey, model, baseURL)。
	// 不自己拼 multipart —— 那样验的就不是仓里那段上传/转码逻辑。
	tr := NewTranscriber(apiKey, model, baseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := tr.TranscribeFor(ctx, Scope{UserID: "probe", WorkspaceID: "ws_probe"}, audio, "probe.wav")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	t.Logf("文本（%d 字）: %s", len([]rune(res.Text)), res.Text)
	t.Logf("model=%q transport=%q channel=%q durationMs=%d confidence=%.2f costCents=%.4f",
		res.Model, res.Transport, res.Channel, res.DurationMS, res.Confidence, res.CostCents)

	// 只钉「一定会成立」的事：产出了非空文本。
	if strings.TrimSpace(res.Text) == "" {
		t.Error("转写结果为空")
	}
	if res.DurationMS == 0 {
		t.Log("· DurationMS 为 0（该后端未回传时长字段）")
	}
	if res.Diarized || len(res.Segments) > 0 {
		t.Logf("分片数=%d diarized=%v（首个：%v）", len(res.Segments), res.Diarized, res.Segments[0])
	} else {
		t.Log("· 无分片/说话人信息")
	}
}

// sliceWav 返回 wav 的 [fromSec, toSec) 切片，**重建**成一份干净的
// RIFF/fmt/data。
//
// ⚠ 不要"复制原文件前 44 字节当新头"：本仓用的这段音频在 fmt 之后还跟了
// 一个 LIST 元数据块（offset 36，size 26）。复制 44 字节会留下一段
// **只有块头、没有块内容** 的 LIST ⇒ 结构非法 ⇒ 网关侧报
// 500 Internal Server Error（第一次就是这么踩的，读数还长得像"网关抽风"）。
func sliceWav(t *testing.T, data []byte, fromSec, toSec float64) []byte {
	t.Helper()
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("不是 WAV：%q %q", data[0:4], data[8:12])
	}
	le32 := func(b []byte) int {
		return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 | int(b[3])<<24
	}

	var (
		channels, rate, bits int
		dataStart, dataLen   int
	)
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		sz := le32(data[pos+4 : pos+8])
		if id == "fmt " {
			channels = int(data[pos+10]) | int(data[pos+11])<<8
			rate = int(data[pos+12]) | int(data[pos+13])<<8 | int(data[pos+14])<<16 | int(data[pos+15])<<24
			bits = int(data[pos+22]) | int(data[pos+23])<<8
		}
		if id == "data" {
			dataStart = pos + 8
			dataLen = sz
			if dataStart+dataLen > len(data) {
				dataLen = len(data) - dataStart
			}
			break
		}
		pos += 8 + sz
		if sz%2 == 1 {
			pos++
		}
	}
	if dataStart == 0 || rate == 0 || channels == 0 {
		t.Fatal("解析 WAV 头失败")
	}
	byteRate := rate * channels * bits / 8

	from := int(fromSec * float64(byteRate))
	to := int(toSec * float64(byteRate))
	if from < 0 {
		from = 0
	}
	if to > dataLen {
		to = dataLen
	}
	if to <= from {
		t.Fatalf("切片区间无效：%v-%v（PCM 总长 %d 字节，%d Hz/%dch/%dbit）",
			fromSec, toSec, dataLen, rate, channels, bits)
	}
	pcm := data[dataStart+from : dataStart+to]

	// 干净的三块头：RIFF(12) + fmt(24) + data(8)
	pcmLen := len(pcm)
	out := make([]byte, 44+pcmLen)
	copy(out[0:4], "RIFF")
	copy(out[4:8], []byte{byte(36 + pcmLen), byte((36 + pcmLen) >> 8), byte((36 + pcmLen) >> 16), byte((36 + pcmLen) >> 24)})
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	copy(out[16:20], []byte{16, 0, 0, 0})
	out[20], out[21] = 1, 0 // PCM
	out[22], out[23] = byte(channels), byte(channels>>8)
	copy(out[24:28], []byte{byte(rate), byte(rate >> 8), byte(rate >> 16), byte(rate >> 24)})
	copy(out[28:32], []byte{byte(byteRate), byte(byteRate >> 8), byte(byteRate >> 16), byte(byteRate >> 24)})
	out[32], out[33] = byte(channels*bits/8), 0 // block align
	out[34], out[35] = byte(bits), 0
	copy(out[36:40], "data")
	copy(out[40:44], []byte{byte(pcmLen), byte(pcmLen >> 8), byte(pcmLen >> 16), byte(pcmLen >> 24)})
	copy(out[44:], pcm)
	t.Logf("切片 %v-%v 秒 ⇒ %d 字节 PCM（%d Hz/%dch/%dbit）", fromSec, toSec, pcmLen, rate, channels, bits)
	return out
}

// TestLiveGatewayASRMergeOverlappingChunks 用**真实 ASR + 真实重叠切片**
// 验 mergeIncremental 的去重对实际后端输出是否生效，并**量出重叠长度分布**。
//
// 用户原始反馈是「片段重复」。此前该逻辑只有合成分片用例；真实 ASR 在
// 重叠区两次解码的上下文不同，输出常不完全一致（标点/用词差异）。
//
// 为什么要扫多个切点而不是测一次：去重策略是「锚点 ≥ 4 rune 且相似度 ≥ 0.75」，
// 是个**阈值**。单次实测可能落在阈值上、也可能落在下，只测一次等于
// 拿一个样本给阈值下结论。这里扫若干切点，报告每次的重叠字符数与是否触发。
func TestLiveGatewayASRMergeOverlappingChunks(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关 ASR 探针：需 POCKET_LIVE_GATEWAY=1")
	}
	baseURL := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_URL"))
	apiKey := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_API_KEY"))
	audioPath := strings.TrimSpace(os.Getenv("POCKET_LIVE_ASR_AUDIO"))
	model := strings.TrimSpace(os.Getenv("POCKET_LIVE_ASR_MODEL"))
	if audioPath == "" {
		audioPath = "/tmp/gt-voice-16k.wav"
	}
	if model == "" {
		model = "mimo-v2.5-asr"
	}
	raw, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatalf("读音频：%v", err)
	}

	tr := NewTranscriber(apiKey, model, baseURL)
	scope := Scope{UserID: "probe", WorkspaceID: "ws_probe"}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	// 扫不同切点：每次都是「前段到 a，后段从 b 开始」，b < a 形成重叠。
	cuts := [][2]float64{{0, 3.2}, {2.6, 4.6}, {0, 2.4}, {1.8, 3.6}, {1.0, 2.8}}
	_ = cuts
	type pair struct{ a, b float64 }
	boundaries := []pair{{3.2, 2.6}, {2.4, 1.8}, {2.8, 1.0}, {3.6, 2.4}, {4.0, 3.2}}

	deduped, notDeduped := 0, 0
	for _, bd := range boundaries {
		chunkA := sliceWav(t, raw, 0, bd.a)
		chunkB := sliceWav(t, raw, bd.b, 4.6)
		ra, err := tr.TranscribeFor(ctx, scope, chunkA, "a.wav")
		if err != nil {
			t.Logf("边界 %.1f/%.1f：段A 转写失败 %v（跳过该切点）", bd.a, bd.b, err)
			continue
		}
		rb, err := tr.TranscribeFor(ctx, scope, chunkB, "b.wav")
		if err != nil {
			t.Logf("边界 %.1f/%.1f：段B 转写失败 %v（跳过该切点）", bd.a, bd.b, err)
			continue
		}
		naive := ra.Text + rb.Text
		merged := mergeIncremental(ra.Text, rb.Text)
		// 真实重叠长度 = 后段开头有多少字已出现在前段结尾（粗略量后缀/前缀公共长度）
		ov := commonSuffixPrefixLen([]rune(ra.Text), []rune(rb.Text))
		changed := merged != naive
		if changed {
			deduped++
		} else {
			notDeduped++
		}
		t.Logf("边界 %.1f/%.1f（重叠 %.1fs）", bd.a, bd.b, bd.a-bd.b)
		t.Logf("   段A=%q", ra.Text)
		t.Logf("   段B=%q", rb.Text)
		t.Logf("   字符级公共重叠=%d  去重%s", ov, map[bool]string{true: "已生效", false: "未生效"}[changed])
		t.Logf("   合并=%q", merged)
	}
	t.Logf("汇总：去重生效 %d 次 / 未生效 %d 次", deduped, notDeduped)
}

// commonSuffixPrefixLen 量「前段结尾」与「后段开头」的最长公共片段长度。
func commonSuffixPrefixLen(a, b []rune) int {
	max := 0
	for n := 1; n <= 8 && n <= len(a) && n <= len(b); n++ {
		match := true
		for k := 0; k < n; k++ {
			if a[len(a)-n+k] != b[k] {
				match = false
				break
			}
		}
		if match {
			max = n
		}
	}
	return max
}
