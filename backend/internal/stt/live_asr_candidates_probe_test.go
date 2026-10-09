// live_asr_candidates_probe_test.go — 真网关 ASR 候选**供给**重扫。
//
// 用户原始需求：「寻找更好的便宜的 asr 类型的大模型，请检查并进行完善」。
// §27 当时的结论是「可用 ASR 仍只有 mimo-v2.5-asr 一个」，
// 理由是 `gpt-audio` / `gpt-audio-mini` / `gpt-4o-audio-preview` 全 503 no_provider。
//
// ⚠⚠ **那个结论带一个没有写下来的前提：网关供给不变。**
//
// 网关供给是会变的 —— 同一轮里 `gpt-4o-mini` 先 503 `no_provider`、
// 十几分钟后又能正常返回（§35 配对实验实测）。所以
// 「可用 ASR 只有 1 个」这句话**没有过期日期就不能当结论用**，
// 每轮涉及 ASR 选型的工作都该重扫一次。
//
// 本探针的定位：**不是**找最好的 ASR（那是 CER 探针的活，见
// live_asr_cer_probe_test.go），而是回答一个更基础的问题：
//
//	**此刻**网关上到底有几个 ASR 模型真的有上游供给？
//
// 枚举走生产同款路径（ListGatewayModels + IsASRCandidate），
// 转写走 NewTranscriber —— 不自己 new 客户端、不手搓 multipart。
//
// 运行：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=https://llmgo.kxpms.cn/v1 \
//	POCKET_LLM_GATEWAY_API_KEY=... \
//	POCKET_LIVE_ASR_AUDIO=/tmp/gt-voice-16k.wav \
//	go test ./internal/stt -run TestLiveGatewayASRCandidateSupply -v -count=1
//
// ⚠ 判据只报读数不判定「哪个更好」：好坏要跨模型比 CER，
//
//	那需要同一批语料逐个跑（29 条语料 × N 个模型 = N 倍开销）。
//	这里只分「有供给 / 没供给」这一档。
package stt

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveGatewayASRCandidateSupply(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关 ASR 供给重扫：需 POCKET_LIVE_GATEWAY=1")
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	models, err := ListGatewayModels(ctx, &http.Client{Timeout: 30 * time.Second}, baseURL, apiKey)
	if err != nil {
		t.Fatalf("列网关模型：%v", err)
	}

	var cands []GatewayModel
	for _, m := range models {
		if IsASRCandidate(m) {
			cands = append(cands, m)
		}
	}
	t.Logf("网关共 %d 个模型，其中 %d 个命中 ASR 候选判定", len(models), len(cands))
	if len(cands) == 0 {
		t.Log("⚠ 一个候选都没有 —— 先查 IsASRCandidate 的正则是否过期，别急着报「网关没有 ASR」")
		return
	}

	scope := Scope{UserID: "probe", WorkspaceID: "ws_probe"}
	usable, nosupply := 0, 0
	for _, m := range cands {
		func() {
			cctx, ccancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer ccancel()
			tr := NewTranscriber(apiKey, m.ID, baseURL)
			start := time.Now()
			res, err := tr.TranscribeFor(cctx, scope, raw, "supply.wav")
			cost := time.Since(start).Round(time.Millisecond)
			switch {
			case err != nil:
				nosupply++
				msg := err.Error()
				// 503 no_provider 是「端点在、没供给」；其他错误是另一回事，
				// 混在一起报会让「可用 ASR 数量」这个数失去含义。
				kind := "其他错误"
				if strings.Contains(msg, "no_provider") || strings.Contains(msg, "503") {
					kind = "无上游供给"
				} else if strings.Contains(msg, "deadline") || strings.Contains(msg, "timeout") {
					kind = "超时（供给未知，不能记成没有）"
				}
				t.Logf("  ✗ %-34s %-14s %v", m.ID, kind, truncateASR(msg, 120))
			case strings.TrimSpace(res.Text) == "":
				t.Logf("  ✗ %-34s %-14s 返回空文本", m.ID, "空结果")
			default:
				usable++
				t.Logf("  ✓ %-34s %-14s %v  %q", m.ID, "可用", cost, truncateASR(res.Text, 60))
			}
		}()
	}

	t.Log("")
	t.Logf("═ 此刻网关 ASR 供给：可用 %d / 无供给 %d（候选共 %d）", usable, nosupply, len(cands))
	t.Log("⚠ 这个数**只对本次扫描时刻成立** —— 网关供给会变。")
	t.Log("  引用它时必须带上扫描时间，不要写成「网关只有 N 个 ASR」。")
}

func truncateASR(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
