// 真网关探针（默认 skip）：**全量转写 vs 定长分片拼接**的逐字对照。
//
// 背景：§25.4 我断言「兜底硬切把词劈开了」，理由是实测到
//
//	A(13字) = 今天下午三点，会议室开产。
//	B(12字) = 体评审会，请提前十分钟。
//
// 说「『产』被劈成两半」。⚠ **那是误判** —— A+B 拼起来是
// 「今天下午三点，会议室开产品评审会，请提前十分钟。」，词是完整的。
// 用户看到的是累积文本（A 直接接 B），**看不到断裂**。
//
// 「断词」这个说法混淆了两件事：
//
//	① 文本被分成两次追加 —— 界面上是连贯的，用户不会觉得断；
//	② 字符真的丢了/多了 —— 这才是缺陷，而它来自 **ASR 在切分边界上
//	   的识别差异**（同一段音频，切在不同位置，输出略有不同），
//	   不是切片把词切碎了。
//
// ⇒ 要回答「分片到底损不损质量」，唯一的办法是拿**全量转写当基准**
//
//	逐字对比。§25 之所以得不出结论，就是因为它没有基准 —— 它只看了
//	片段，没看拼起来跟一次转写差多少。
//
// 运行：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=https://llmgo.kxpms.cn/v1 \
//	POCKET_LLM_GATEWAY_API_KEY=... \
//	POCKET_LIVE_ASR_AUDIO=/tmp/gt-voice-16k.wav \
//	POCKET_LIVE_ASR_MODEL=mimo-v2.5-asr \
//	go test ./internal/stt -run TestLiveGatewayFullVsChunked -v
package stt

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveGatewayFullVsChunked 拿「整段一次转写」当基准，对比多种分片方式。
func TestLiveGatewayFullVsChunked(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关全量 vs 分片探针：需 POCKET_LIVE_GATEWAY=1")
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
		t.Fatalf("读音频：%v", err)
	}
	dur := wavDurationSec(t, audio)
	tr := NewTranscriber(apiKey, model, baseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	tx := func(b []byte) string {
		r, err := tr.TranscribeFor(ctx, Scope{UserID: "p", WorkspaceID: "w"}, b, "x.wav")
		if err != nil {
			return ""
		}
		return strings.TrimSpace(r.Text)
	}

	// ── 基准：整段一次 ──
	full := tx(audio)
	t.Logf("基准（全量 %d 字）：%q", len([]rune(full)), full)
	if full == "" {
		t.Fatal("全量转写为空 —— 没有基准可比")
	}

	// ── 对照组：多种定长分片（生产的兜底形状）──
	//
	// 每种都模拟「前端按固定窗口送、后端逐片转写并直接追加」的形态。
	// 最后一窗不足长度时用剩余音频。
	windows := []float64{1.0, 1.5, 2.0, 3.0}
	for _, w := range windows {
		if w >= dur {
			continue
		}
		var parts []string
		for st := 0.0; st < dur-1e-6; st += w {
			en := st + w
			if en > dur {
				en = dur
			}
			p := tx(sliceWav(t, audio, st, en))
			if p != "" {
				parts = append(parts, p)
			}
		}
		joined := strings.Join(parts, "")
		t.Logf("")
		t.Logf("── 定长 %.1fs 分片（%d 片）──", w, len(parts))
		for i, p := range parts {
			t.Logf("   片%d %q", i, p)
		}
		t.Logf("   拼接 = %q", joined)
		t.Logf("   与基准一致? %v", joined == full)
		logDiff(t, full, joined)
	}
}

// logDiff 报告两段文本的编辑距离与逐字差异，**不判定对错**。
//
// 距离用最朴素的 LCS（与 lcsAlign 同源）。这里调用它只是为了量化
// 「分片比全量差多少」，不是为了去重 —— 阈值判据不在本探针的职责内。
func logDiff(t *testing.T, want, got string) {
	t.Helper()
	a, b := []rune(want), []rune(got)
	dp, _ := lcsAlign(a, b)
	d := dp[len(a)][len(b)]
	t.Logf("   字符：基准 %d / 分片 %d / 公共 %d / 净差 %+d",
		len(a), len(b), d, len(b)-len(a))
	// 找出分片版本相对基准多出来的字（多半是 ASR 在边界处的幻觉）
	_, matched := lcsAlign(a, b)
	var extra []rune
	for j, m := range matched {
		if j >= 1 && !m {
			extra = append(extra, b[j-1])
		}
	}
	if len(extra) > 0 {
		t.Logf("   分片多出的字：%q", string(extra))
	}
}
