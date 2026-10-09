// live_asr_candidate_coverage_probe_test.go — **候选集完整性**探针。
//
// ── 它回答的是另一个问题 ──
//
// `TestLiveGatewayASRCandidateSupply` 回答的是「`IsASRCandidate` 认出的那些里，
// 此刻有几个真的有上游供给」。
//
// ⚠ 它的读数**过期速度是小时级**（见本文件末的实测记录），所以下面这句
//
//	  「只有 N 个可用」带着一句没写下来的前提：
//	  **`IsASRCandidate` 认出的那些就是全部候选。**
//
//		而 `IsASRCandidate` 是**三段启发式**（强 ASR 名 → TTS 排除 → modality==audio →
//		弱 ASR 名）。启发式必然有边界：任何一个「能吃音频但名字不含
//		asr/whisper/transcri/speech/audio/omni/voice、modality 又不是 audio」的模型，
//		都会被静默漏掉 —— 而漏掉的那些**可能恰恰是能用的**。
//		⇒ 「网关只有 N 个可用 ASR」有可能只是**过滤器的读数**，不是网关的事实。
//
// 本探针只回答一件事：**有没有被 `IsASRCandidate` 漏掉的、看起来能吃音频的模型。**
//
// ── 2026-10-07 实测结论（写在这里是为了让下一个人不必重跑）──
//
//	目录 557 个（⚠ 同一小时内另一次列举是 609，两次对不上 ⇒ /models 不是稳定全集）
//	生产判定候选 6 个；宽规则多认出 3 个，**差集全是 TTS / 音色克隆**
//	⇒ ★ **候选判定没有漏 ASR**：「6 个候选」是网关的事实，不是过滤器的读数。
//
//	这一轮供给：可用 **2**（`mimo-v2.5-asr` / `minimax-asr-1.0`）——
//	**不是 1 个**，上一轮的读数已被本轮证伪。详见 docs §138。
//
// ⚠ 与 2026-10-07 那次自我否证的差别（别再犯）：
//
//	那次我按 `modality=audio` 枚举 + 手搓正则，得到 4 个候选，
//	少算 2 个 —— 因为 `IsASRCandidate` 把 modality 当**三信号之一**，
//	而 `asrNameRe` 里含 `omni`。
//	⇒ 本探针**不复刻**分类逻辑：它复用生产的 `ListGatewayModels` 拿全量目录，
//	  只在**输入集**上放宽（一条刻意更宽的名字规则），报告差集。
//	  差集是「值得怀疑的」，不是「结论」—— 真要定性得逐个探供给。
//
// 运行：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=https://llmgo.kxpms.cn/v1 \
//	POCKET_LLM_GATEWAY_API_KEY=... \
//	go test ./internal/stt -run TestLiveASRCandidateCoverage -v -count=1
//
// ⚠ 不需要音频：这一轮只比目录，不发转写请求（省掉限流预算）。
package stt

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// wideAudioNameRe 是**刻意更宽**的名字规则，只用于**发现漏网**，不做判定。
//
// 覆盖面刻意超过生产的 asrNameRe：把中文 ASR 生态里常见的族名
// （SenseVoice / Paraformer / FunASR / Conformer / WeNet / DeepSpeech / Kaldi / Vosk）、
// 以及 STT / realtime / hear / listen / recognition 这些不在生产正则里的词都收进来。
var wideAudioNameRe = regexp.MustCompile(`(?i)(asr|stt|whisper|transcri|speech|audio|omni|voice|listen|hear|recognition|sensevoice|paraformer|funasr|conformer|wenet|deepspeech|kaldi|vosk|realtime|multimodal)`)

func TestLiveASRCandidateCoverage(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("候选集完整性探针：需 POCKET_LIVE_GATEWAY=1")
	}
	baseURL := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_URL"))
	apiKey := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_API_KEY"))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	models, err := ListGatewayModels(ctx, &http.Client{Timeout: 30 * time.Second}, baseURL, apiKey)
	if err != nil {
		t.Fatalf("列网关模型：%v", err)
	}

	// 1) 生产判定认出的候选
	var prod []string
	for _, m := range models {
		if IsASRCandidate(m) {
			prod = append(prod, m.ID)
		}
	}
	// 2) 宽规则认出的
	var wide []string
	for _, m := range models {
		if wideAudioNameRe.MatchString(m.ID) || strings.Contains(strings.ToLower(m.Modality), "audio") {
			wide = append(wide, m.ID)
		}
	}
	sort.Strings(prod)
	sort.Strings(wide)
	inProd := map[string]bool{}
	for _, id := range prod {
		inProd[id] = true
	}

	// 3) 差集 = 被生产判定漏掉、但宽规则认为「可能能吃音频」
	var escapees []string
	for _, id := range wide {
		if !inProd[id] {
			escapees = append(escapees, id)
		}
	}

	t.Logf("目录共 %d 个模型", len(models))
	t.Logf("生产 IsASRCandidate 认出的候选：%d 个", len(prod))
	for _, id := range prod {
		t.Logf("    · %s", id)
	}
	t.Logf("宽规则认出的（含 modality 含 audio）：%d 个", len(wide))
	t.Logf("★ 差集（被生产判定漏掉）：%d 个", len(escapees))
	for _, id := range escapees {
		t.Logf("    ⚠ %s", id)
	}

	if len(escapees) == 0 {
		t.Log("⇒ 差集为空：「只有 N 个候选」是网关的事实，不是过滤器的读数。")
		t.Log("  （仍不证明它们都有供给 —— 那是 supply 探针的活。）")
		return
	}
	t.Log("⇒ 差集非空：**「只有 N 个候选」目前只是过滤器的读数**。")
	t.Log("  下一步必须逐个探供给（/audio/transcriptions 真音频），")
	t.Log("  否则「可用 ASR 只有 1 个」这个结论是**不成立**的 —— 参见本文件头。")
	t.Log("  ⚠ 注意差集里很可能是 TTS / 语音合成模型（它们也能吃文本产出音频，")
	t.Log("    名字同样命中 audio/voice）—— 逐个探之前不要把差集当候选。")
}
