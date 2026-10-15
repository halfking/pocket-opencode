// 真网关探针（默认 skip）：**生产整段路径的真实形态** vs 整段一次单发，
// 以及「加上跨段合并层」能不能把前者救回来。
//
// ── 为什么必须有这个探针（§481）──
//
// §478/§480 量到的是「定长窗口分片 vs 整段单发」。但**生产整段路径根本不是
// 整段单发**，它的真实形态是三步：
//
//  1. SplitWAV(audio, effectiveSegmentSec(model))   full.go:426
//     —— 静音感知切段，上限 25 秒（defaultSegmentSec）
//  2. 逐段转写                                        full.go:445-480
//  3. strings.Join(texts, "\n")                       full.go:486
//     —— ⛔ **跨段零合并**，直接换行拼接
//
// ⇒ 我此前那两条实测（整段 144 字逐字正确 vs 3.0s 切净差 +9）量的是
//
//	一个**比生产更好**的基准：生产也会被切成 25 秒两段。
//	把「整段单发逐字正确」当成「生产整段路也逐字正确」是**跨链推断**，
//	而 §477 的教训恰恰是：实验量不到目标时，「净效应为零」不能读成「目标没用」。
//
// 本探针直接调用生产函数（SplitWAV / effectiveSegmentSec / transcribeFor），
// 而不是自己写一个切分循环 —— 前一版的 `live_full_vs_chunked_probe_test.go`
// 用的是后者，那是它测不到生产的唯一原因。
//
// 三个臂：
//
//	A 整段一次单发（基准，非生产形态）
//	B 生产切分 + 逐段 + "\n" 拼接   ← transcribeFull 的真实形态
//	C 生产切分 + mergeIncremental 聚合 ← 「补上合并层」够不够
//
// C 臂专门关掉 §478 留下的坑：「只量了『逐片转写 + 直接拼接』，
// **没量**跨片去重能否修回来」。而这里用生产里**已有的** mergeIncremental，
// 不是新写的算法 —— 因为要回答的是「把现有那套搬过来够不够」。
//
// 运行：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=https://llmgo.kxpms.cn/v1 \
//	POCKET_LLM_GATEWAY_API_KEY=... \
//	POCKET_LIVE_ASR_AUDIO=/tmp/gt-voice-30s-16k.wav \
//	POCKET_LIVE_ASR_MODEL=mimo-v2.5-asr \
//	go test ./internal/stt -run TestLiveGatewayProdSegment -v -timeout 1800s
package stt

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/livetest"
)

// prodSegPerCall 是单次调用的预算余量。
//
// ⚠ §480 实测该网关 4–6s/次；这里给 45s 而不是 §479 用的 12s，
// 因为生产整段路自己就用 `segmentTimeout = 90s`（full.go:520）——
// 给比生产还紧的预算，会把「网关抖动」误读成「探针跑不完」。
const prodSegPerCall = 45 * time.Second

// TestLiveGatewayProdSegment 量生产的整段切分形态。
func TestLiveGatewayProdSegment(t *testing.T) {
	splitOnly := os.Getenv("POCKET_LIVE_SPLIT_ONLY") == "1"
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" && !splitOnly {
		t.Skip("真网关生产切分探针：需 POCKET_LIVE_GATEWAY=1（只看切段计划用 POCKET_LIVE_SPLIT_ONLY=1）")
	}
	baseURL := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_URL"))
	apiKey := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_API_KEY"))
	audioPath := strings.TrimSpace(os.Getenv("POCKET_LIVE_ASR_AUDIO"))
	model := strings.TrimSpace(os.Getenv("POCKET_LIVE_ASR_MODEL"))
	if !splitOnly && (baseURL == "" || apiKey == "") {
		t.Fatal("缺少 POCKET_LLM_GATEWAY_URL / _API_KEY")
	}
	if audioPath == "" {
		audioPath = "/tmp/gt-voice-30s-16k.wav"
	}
	if model == "" {
		model = "mimo-v2.5-asr"
	}
	audio, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatalf("读音频：%v", err)
	}
	dur := wavDurationSec(t, audio)

	segSec := effectiveSegmentSec(model)
	segments, splittable := SplitWAV(audio, segSec)
	t.Logf("样本 %.2fs · effectiveSegmentSec(%q)=%.0fs · SplitWAV 可切=%v · 切出 %d 段",
		dur, model, segSec, splittable, len(segments))
	for _, s := range segments {
		t.Logf("   段%d %.2f–%.2fs (%.2fs) SilenceCut=%v 有用=%v",
			s.Index, s.StartSec, s.EndSec, s.EndSec-s.StartSec,
			s.SilenceCut, s.IsUsefulSegment())
	}
	if splitOnly {
		// 只看切段计划时到此为止：省掉每段的真网关调用与计费。
		//
		// ⚠ 这一模式**不能**用来下质量结论 —— 它一个字都没转写。
		t.Logf("POCKET_LIVE_SPLIT_ONLY=1：只报了切段计划，未转写、未量质量。")
		return
	}

	// 预算 = 1 次整段基准 + 一次/段（生产切分两臂共用同一批段结果，不再重复付费）。
	calls := 1
	for _, s := range segments {
		if s.IsUsefulSegment() {
			calls++
		}
	}
	budget := time.Duration(calls) * prodSegPerCall
	t.Logf("预算：%d 次调用 × %v = %v", calls, prodSegPerCall, budget)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	tr := NewTranscriber(apiKey, model, baseURL)
	scope := Scope{UserID: "p", WorkspaceID: "w"}
	// ⚠⚠ §494：tx 必须把「转写出文本了」与「这次调用返回了空」分成两个结果。
	//
	// 原版两者都 return ""，而下面那段循环照旧把 "" 追加进 segTexts，于是：
	//   · B 臂的 strings.Join 多出一个空行，而 normText 用 strings.Fields
	//     会把它**整个抹平** ⇒「B 与 A 一致」把没转出来的段当成了转出来了；
	//   · C 臂的 mergeIncremental 有空串守卫（incremental.go:227），
	//     反而把空段原样跳过 ⇒ B/C 两臂的分段数悄悄不同，却没人读得出。
	//
	// ⛔ 这与 §493 在 TestLiveGatewayASRCandidateSupply 上抓到的是同一族：
	// 「200 + 空文本」既不是可用、也不是无供给，是第三种状态（#31 在探针层的落点）。
	tx := func(b []byte) (string, bool) {
		r, err := tr.TranscribeFor(ctx, scope, b, "x.wav")
		if err != nil {
			t.Logf("   ⚠ 转写失败：%v", err)
			return "", false
		}
		return strings.TrimSpace(r.Text), true
	}

	// ── A 臂：整段一次单发（基准，注意它**不是**生产形态）──
	full, fullOK := tx(audio)
	if !fullOK || full == "" {
		t.Fatal("整段基准为空或调用失败 —— 没有对照可比")
	}
	t.Logf("")
	t.Logf("A 整段一次单发（%d 字）：%q", len([]rune(full)), full)

	// ── B/C 臂共用：按生产切分逐段转写 ──
	// ⚠ 分母**来自账本**而不是另算一遍：上面那个预算循环数出来的 calls
	//   是「1 次整段基准 + 每个有用段一次」，所以 calls-1 就是本轮该转的段数。
	//   （§492 的教训：另算一遍的分母会在循环改动后悄悄对不上。）
	ledger := livetest.New("生产切分 B/C 臂", calls-1)
	var segTexts []string
	var gotEmpty, gotFail []string // 按段号记，出问题时能定位到是哪一段
	for _, s := range segments {
		if !s.IsUsefulSegment() {
			t.Logf("   段%d 跳过（纯静音）", s.Index)
			continue
		}
		txt, ok := tx(s.Audio)
		seg := fmt.Sprintf("段%d", s.Index)
		switch {
		case !ok:
			gotFail = append(gotFail, seg)
			t.Logf("   %s ✗ 转写失败（不计入 B/C 臂）", seg)
			continue
		case txt == "":
			gotEmpty = append(gotEmpty, seg)
			t.Logf("   %s ⚠ 返回空文本（#31：200 + 空串）⇒ 不计入 B/C 臂", seg)
			continue
		}
		t.Logf("   %s %q", seg, txt)
		segTexts = append(segTexts, txt)
		ledger.Complete(seg)
	}
	// ⚠⚠ 对账必须打在**比较之前**。§491 已在同一条链上栽过一次：
	//   先把不完整的读数印出去、再补一句「不完整」，等于没补。
	if rep := ledger.Report(); rep != "" {
		t.Log(rep)
		t.Logf("   没转出来的段：空文本 %v · 调用失败 %v", gotEmpty, gotFail)
	}
	t.Logf("段级三桶：转出文本 %d / 空文本 %d / 调用失败 %d（本该转 %d 段，纯静音段不计入）",
		ledger.Done(), len(gotEmpty), len(gotFail), ledger.Total())

	// ⚠⚠ 缺段时「与 A 一致」这句话必须**降级**：否则读者会把
	// 「B 少转了 2 段所以更短」误读成「B 抄得更准」——
	// 而这两个数字就印在同一行上。
	caveat := ""
	if n := len(gotEmpty) + len(gotFail); n > 0 {
		caveat = fmt.Sprintf("　⚠⚠ 本轮 %d/%d 段没转出来，这个「一致」只在剩下的段上成立",
			n, ledger.Total())
	}

	// ── B 臂：生产形态 —— strings.Join(texts, "\n")（full.go:486）──
	prodJoined := strings.Join(segTexts, "\n")
	t.Logf("")
	t.Logf("B 生产切分 + \\n 拼接（%d 字）：%q", len([]rune(prodJoined)), prodJoined)
	t.Logf("   与 A 一致? %v%s", normText(prodJoined) == normText(full), caveat)
	logDiff(t, full, prodJoined)

	// ── C 臂：同一批段结果，改用生产已有的 mergeIncremental 聚合 ──
	merged := ""
	for _, s := range segTexts {
		merged = mergeIncremental(merged, s)
	}
	t.Logf("")
	t.Logf("C 生产切分 + mergeIncremental（%d 字）：%q", len([]rune(merged)), merged)
	t.Logf("   与 A 一致? %v%s", normText(merged) == normText(full), caveat)
	logDiff(t, full, merged)

	// ── 结论三连：不判定优劣，只把三个臂摆在一起 ──
	t.Logf("")
	// ⚠⚠ 小结必须带上三桶与降级标记：只印字数时，
	// 「B 比 A 短」有两种完全不同的成因（抄得更准 / 少转了几段），
	// 而字面上分不出来。
	t.Logf("小结：A=%d / B=%d / C=%d 字（B/C 只由 %d 段成功转写的结果拼成；"+
		"空文本 %d 段、调用失败 %d 段）%s",
		len([]rune(full)), len([]rune(prodJoined)), len([]rune(merged)),
		ledger.Done(), len(gotEmpty), len(gotFail), caveat)
	t.Logf("     B 与 A 一致=%v；C 与 A 一致=%v",
		normText(prodJoined) == normText(full), normText(merged) == normText(full))
	if len(segments) == 1 {
		t.Logf("⚠ 只切出 1 段 ⇒ B/C 两臂与 A 是同一次调用，本轮**没有量到跨段合并**。")
		t.Logf("  换更长的音频再跑（POCKET_LIVE_ASR_AUDIO 指向 >%.0fs 的样本）。", segSec)
	}
}

// normText 去掉空白后比较 —— 生产聚合用 "\n" 连接，而基准没有换行。
//
// ⚠ 只去空白、不动标点：标点差异是这次要量的东西之一（§480 实测
// 「误加标点」是净差的主要来源之一），先把它抹掉就看不见了。
func normText(s string) string {
	return strings.Join(strings.Fields(s), "")
}
