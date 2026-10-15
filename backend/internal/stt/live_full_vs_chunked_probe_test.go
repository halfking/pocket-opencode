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
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/livetest"
)

// fullVsChunkedWindows 是本探针对照的定长分片窗口（秒）。
//
// ⚠ 提到包级是因为**预算要按它算**（见下面 `fullVsChunkedCallCount`）：
//
//	窗口集合改了而预算没跟着变，就会回到 §478.4 那个「跑不完」的状态。
var fullVsChunkedWindows = []float64{1.0, 1.5, 2.0, 3.0}

// fullVsChunkedCallCount 给出「按这些窗口切」一共要发几次请求。
//
// ⚠ 抽成纯函数不是为了整洁，是为了让「预算够不够」这条不变量**可被离线门验证**：
// 它只依赖 (样本时长, 窗口集合)，不需要网关、不需要音频。
// 计数必须与下方循环**同形**：基准 1 次 + 每个窗口 ceil(时长/窗口) 次。
func fullVsChunkedCallCount(durSec float64, windows []float64) int {
	n := 1 // 基准：整段一次
	for _, w := range windows {
		if w <= 0 || w >= durSec {
			continue
		}
		n += int(math.Ceil(durSec / w))
	}
	return n
}

// fullVsChunkedPerCall 是单次调用的预算余量。
//
// ⚠ 12s：§478 实测 4–6s/次（20 次调用 / 47s），留 2 倍余量给网关抖动
// （§477 实测该网关确有 http2 超时）。
//
// ⚠⚠ 它是**包级常量**而不是函数内的字面量 —— 因为上一版的门
//
//	**自己**算预算、从不读生产那个变量，于是把生产改回写死 300s 时门照样全绿。
//	（§474.4 与本节是同一条教训的两次实例：门守的是副本，不是被测的那段。）
const fullVsChunkedPerCall = 12 * time.Second

// fullVsChunkedBudget 给出本次实验的 (调用数, ctx 预算)。
//
// ⚠ 生产与离线门**必须共用这一个函数**：门若自己复算一份，
//
//	「预算够不够」这条不变量就没人守了 —— 那是本节真实的翻车形态。
func fullVsChunkedBudget(durSec float64, windows []float64) (int, time.Duration) {
	calls := fullVsChunkedCallCount(durSec, windows)
	return calls, time.Duration(calls) * fullVsChunkedPerCall
}

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

	// ⚠⚠ 2026-10-11（§478.4）：ctx 预算**必须由调用数算出来**，不能写死。
	//
	// 原先是 `context.WithTimeout(..., 300*time.Second)` 写死，
	// 而调用数 ≈ 音频时长 ÷ 最小窗口 ⇒ 换一段长录音就必然中途被砍：
	//   6.83s 语料 ⇒ 20 次调用 ⇒ 47s（实测，能跑完）
	//   59.9s 语料 ⇒ 151 次调用 ⇒ ≈700s（实测，直接被砍，一行读数都没留下）
	// 而**这条探针恰好是「长录音到底损多少」的唯一量具** ——
	// 量具在小样本上好使、在目标样本上跑不完，等于没有量具。
	//
	total, budget := fullVsChunkedBudget(dur, fullVsChunkedWindows)
	t.Logf("预算：%d 次调用 × %v = %v（样本 %.1fs）", total, fullVsChunkedPerCall, budget, dur)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	// ⭐ §489.6：本轮跑完了几档必须对账（total = 档数，跳过的不算完成）。
	// total 直接用 len(窗口集合) —— 跳过档会在末尾被显式点名，而不是悄悄消失。
	ledger := livetest.New("整段 vs 分片探针", len(fullVsChunkedWindows))

	// ⚠⚠ §489.6：tx 刻意**不吞掉错误**。
	//
	// 第一版是 `if err != nil { return "" }`，而调用点写的是
	// `if p != "" { parts = append(parts, p) }` ——
	// ⇒ 转写失败的片被**静默滤掉**，`parts` 少几片，末尾却照旧打印
	// 「与基准一致? false」⇒ **读者会把「片丢了」读成「分片更差」**。
	//
	// 现在错误单独计数，且在末尾与对账一起报出来。
	var txErrs []error
	tx := func(b []byte) string {
		r, err := tr.TranscribeFor(ctx, Scope{UserID: "p", WorkspaceID: "w"}, b, "x.wav")
		if err != nil {
			txErrs = append(txErrs, err)
			return ""
		}
		return strings.TrimSpace(r.Text)
	}

	// ── 基准：整段一次 ──
	full := tx(audio)
	t.Logf("基准（全量 %d 字）：%q", len([]rune(full)), full)
	if full == "" {
		t.Fatal("整段转写为空 —— 没有对照可比")
	}
	if len(txErrs) > 0 {
		t.Fatalf("基准转写报错：%v —— 没有对照可比", txErrs[0])
	}

	// ── 对照组：多种定长分片（生产的兜底形状）──
	//
	// 每种都模拟「前端按固定窗口送、后端逐片转写并直接追加」的形态。
	// 最后一窗不足长度时用剩余音频。
	//
	// ⚠⚠ §489.6：本轮做完了几档必须对账，且**跳过的档必须显式打印**。
	//
	// 第一版是 `if w >= dur { continue }` —— 窗口比样本长就整档跳过，
	// 而读数里**完全没有这一档存在的痕迹** ⇒ 「少跑了一档」与「就这几档」完全同形。
	// 这与 §476/§489 是同一个家族：被砍掉的读数长得和完整结果一模一样。
	//
	// 跳过**不是错误**（1.0s 窗口配 0.5s 样本确实没意义），
	// 但它必须**出声**，否则读者会以为该档跑过且结果很差。
	skipped := []float64{}
	// ⚠⚠ §489.6：各档读数**先攒起来，不立刻打印**。
	//
	// 为什么必须攒：`与基准一致? false` 这一行**在循环里**打印，
	// 而账本只有循环结束后才知道跑没跑完 ——
	// ⇒ 若在循环里直接打，任何循环后的 `Report()` 都**必然太晚**
	//   （本轮实测：接线门的判据④当场报「Report 在汇总之后」）。
	// ⇒ 正确形态是：**先对账，再打印读数**。
	var tierLines []string
	var diffs []string // 各档拼接结果，供对账之后再逐档 logDiff
	for _, w := range fullVsChunkedWindows {
		if w >= dur {
			skipped = append(skipped, w)
			tierLines = append(tierLines,
				fmt.Sprintf("⚠ 跳过 %.1fs 档：窗口不小于样本时长 %.2fs（**本档没跑，不能当「分片更差」读**）", w, dur))
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
		// ⭐ 期望片数 vs 实得片数：**这一条才是「片有没有丢」的判据**。
		// 少了就说明有片转写失败（见 txErrs），而不是「分片本身更差」。
		wantParts := int(math.Ceil(dur / w))
		joined := strings.Join(parts, "")
		tierLines = append(tierLines,
			fmt.Sprintf("── 定长 %.1fs 分片（实得 %d 片 / 期望 %d 片）──", w, len(parts), wantParts))
		for i, p := range parts {
			tierLines = append(tierLines, fmt.Sprintf("   片%d %q", i, p))
		}
		tierLines = append(tierLines,
			fmt.Sprintf("   拼接 = %q", joined),
			fmt.Sprintf("   与基准一致? %v", joined == full))
		diffs = append(diffs, joined)
		if len(parts) < wantParts {
			t.Errorf("%.1fs 档：期望 %d 片、实得 %d 片 —— **有 %d 片没转写成功**。\n"+
				"  ⇒ 本档的「与基准一致? false」**不能**读成「分片更差」，"+
				"它混着「片丢了」。请看下面的失败清单。", w, wantParts, len(parts), wantParts-len(parts))
		}
		ledger.Complete(fmt.Sprintf("%.1fs 档", w))
	}

	// ── 对账：必须**在**任何一行读数被当成结论之前出声 ──────────────
	//
	// ⚠⚠ §489.6：正因为读数被攒在 `tierLines` 里，这里才有资格先说话。
	//　 修法不是「把 Report 往前挪」（那在原写法下不可能），
	//　 而是**把读数的打印推迟到对账之后**。
	if msg := ledger.Report(); msg != "" {
		t.Error(msg)
	}

	// 到这里才逐档打印读数。
	for _, line := range tierLines {
		t.Log(line)
	}
	// ⭐ logDiff 也要等对账之后 —— 它与上面那些行是同一个结论的两半
	//（「分片多出/少了哪些字」），拆开打印同样会让人在知道完整性之前就下结论。
	for _, d := range diffs {
		logDiff(t, full, d)
	}

	t.Logf("")
	// ⚠⚠ §492：分母来自**账本**而不是 `len(fullVsChunkedWindows)` 另算一遍。
	t.Logf("【分母自证】账本 %d/%d 档跑完（跳过 %d 档：窗口 ≥ 样本 %.2fs）",
		ledger.Done(), ledger.Total(), len(skipped), dur)

	if len(skipped) > 0 {
		t.Logf("⚠ 本轮**跳过 %d 档**（窗口 ≥ 样本 %.2fs）：%v", len(skipped), dur, skipped)
		t.Log("  ⇒ 这几档**没有读数**，不能被读成「这几档分片更差」。")
	}
	if len(txErrs) > 0 {
		t.Logf("⛔ 本轮有 %d 次转写失败（每片一次）：", len(txErrs))
		for i, e := range txErrs {
			if i >= 8 {
				t.Logf("   …还有 %d 条", len(txErrs)-i)
				break
			}
			t.Logf("   %v", e)
		}
		t.Log("  ⇒ **上面带「与基准一致?」的那些行不成立**：它们混着「片丢了」，不是纯分片损耗。")
	} else {
		t.Log("✓ 零转写失败 ⇒ 上面每档的「与基准一致?」都是纯分片损耗，可直接读。")
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
