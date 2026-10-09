// live_asr_one_model_probe_test.go — **定向**探针：只探一个模型，别的都不碰。
//
// ── 为什么需要它 ──
//
// 2026-10-07 13:2x 的供给重扫把 6 个候选分成三类：
//
//	✓ mimo-v2.5-asr                    可用 844ms
//	✓ minimax-asr-1.0                  可用 1.155s（转写逐字相同）
//	✗ nemotron-3-nano-omni-… / gpt-audio / gpt-audio-mini   503 no_provider
//	? glm-asr                           **429 upstream_rate_limited**
//
// `429` 与 `503 no_provider` 是**两件事**：前者是「上游限流、现在问不出来」，
// 后者是「没有供给」。把限流记成「没有」，就会在文档里留下一条
// 「glm-asr 不可用」的错误结论 —— 而它可能只是那一刻在限流。
// 仓里 `gateway_compat_test.go` 早就实测过 `gpt-4o-mini` 先 503、
// 十几分钟后又能正常返回（§35 配对实验），同族。
//
// 而全量 supply 探针**不适合**用来消除这个不确定性：它一次打 6 个请求，
// 恰好会制造/加重限流。⇒ 必须有一个**只发一个请求**的探针。
//
// 运行：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=... \
//	POCKET_LLM_GATEWAY_API_KEY=... \
//	POCKET_LIVE_ASR_AUDIO=/tmp/gt-voice-16k.wav \
//	POCKET_LIVE_ASR_MODEL=glm-asr \
//	go test ./internal/stt -run TestLiveGatewayOneASRModel -v -count=1
package stt

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveGatewayOneASRModel(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("单模型供给探针：需 POCKET_LIVE_GATEWAY=1")
	}
	id := strings.TrimSpace(os.Getenv("POCKET_LIVE_ASR_MODEL"))
	if id == "" {
		t.Skip("未指定 POCKET_LIVE_ASR_MODEL")
	}
	baseURL := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_URL"))
	apiKey := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_API_KEY"))
	audioPath := strings.TrimSpace(os.Getenv("POCKET_LIVE_ASR_AUDIO"))
	if audioPath == "" {
		audioPath = "/tmp/gt-voice-16k.wav"
	}
	raw, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatalf("读音频 %s：%v", audioPath, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	res, terr := NewTranscriber(apiKey, id, baseURL).
		TranscribeFor(ctx, Scope{UserID: "probe", WorkspaceID: "ws_probe"}, raw, "one.wav")
	cost := time.Since(start).Round(time.Millisecond)

	switch {
	case terr != nil:
		msg := terr.Error()
		kind := "其他错误"
		switch {
		case strings.Contains(msg, "upstream_rate_limited"), strings.Contains(msg, "429"):
			kind = "★ 上游限流 —— 供给**未知**，不许记成「没有供给」"
		case strings.Contains(msg, "no_provider"), strings.Contains(msg, "503"):
			kind = "无上游供给"
		case strings.Contains(msg, "deadline"), strings.Contains(msg, "timeout"):
			kind = "超时 —— 供给未知"
		}
		t.Logf("✗ %s  %s  %v  %s", id, kind, cost, truncateASR(msg, 160))
		t.Log("  ⇒ 换个时刻再问一次再下结论；限流不是否定证据。")
	case strings.TrimSpace(res.Text) == "":
		t.Logf("✗ %s  返回空文本  %v", id, cost)
	default:
		t.Logf("✓ %s  可用  %v  %q", id, cost, truncateASR(res.Text, 80))
	}
}
