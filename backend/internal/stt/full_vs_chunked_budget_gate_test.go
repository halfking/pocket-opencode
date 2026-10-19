// full_vs_chunked_budget_gate_test.go — §478.4 的**离线**门：
// 分片探针的 ctx 预算必须由**调用数**算出来，而不是写死。
//
// ── 为什么这道门必须离线 ────────────────────────────────────────────
//
// 缺陷的完整形态：探针内部写死 `WithTimeout(..., 300*time.Second)`，
// 而调用数 ≈ 音频时长 ÷ 最小窗口。实测：
//
//	 6.83s 语料 ⇒  20 次调用 ⇒ 47s   ✅ 跑完
//	59.9s 语料 ⇒ 151 次调用 ⇒ ≈700s ⛔ 直接被砍，**一行读数都没留下**
//
// ⚠ 而这条探针**恰好是「长录音到底损多少」的唯一量具** ——
//
//	量具在小样本上好使、在目标样本上跑不完，等于没有量具。
//	而「拿真人会议录音测分片影响」正是 §478.4 登记的待办。
//
// ⚠ 真探针要网关 + 租户 key ⇒ 在 CI 与任何人的克隆里，
//
//	这个缺陷既不会发生也不会被发现 ⇒ 能守住它的判据不能依赖网关。
package stt

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestFullVsChunkedBudgetScalesWithDuration(t *testing.T) {
	cases := []struct {
		name string
		dur  float64
		min  int // 至少要发这么多次
	}{
		// §478 实测跑完的那一条：20 次
		{"6.83s（实测可跑完）", 6.83, 20},
		// §478.4 实测被砍的那一条：151 次
		{"59.9s（实测被砍）", 59.9, 151},
		// 一段真实会议的量级（10 分钟）
		{"600s 会议", 600, 1},
	}
	for _, c := range cases {
		n, budget := fullVsChunkedBudget(c.dur, fullVsChunkedWindows)
		if n < c.min {
			t.Errorf("%s：调用数算成 %d，少于实测的 %d ⇒ 预算会不够用",
				c.name, n, c.min)
		}
		// ⚠ 预算必须**至少**容得下所有调用各花一次的时间。
		if budget < time.Duration(n)*time.Second {
			t.Errorf("%s：预算 %v 容不下 %d 次调用", c.name, budget, n)
		}
		t.Logf("%s：%d 次 × %v = %v", c.name, n, fullVsChunkedPerCall, budget)
	}
}

// TestFullVsChunkedBudgetBeatsHardcoded300s 钉住**这条缺陷本身**：
// 59.9s 样本上的预算必须**远大于**曾经写死的 300s。
func TestFullVsChunkedBudgetBeatsHardcoded300s(t *testing.T) {
	const dur = 59.9
	_, budget := fullVsChunkedBudget(dur, fullVsChunkedWindows)
	if budget <= 300*time.Second {
		t.Fatalf("59.9s 样本的预算只有 %v，仍在写死值 300s 的射程内 ⇒ §478.4 的缺陷没修掉",
			budget)
	}
	t.Logf("59.9s 样本预算 %v（写死值是 300s，超出 %.1f 倍）",
		budget, float64(budget)/float64(300*time.Second))
}

// TestFullVsChunkedCallCountMatchesLoop 守「计数函数与实际循环**同形**」。
//
// ⚠ 这是最容易漂的一处：循环加了一个 `continue` 条件、或计数漏了「基准那一次」，
//
//	预算就会算错 —— 而**算错的预算不会立刻报错**，只会在某天表现为「又跑不完」。
func TestFullVsChunkedCallCountMatchesLoop(t *testing.T) {
	for _, dur := range []float64{6.83, 59.9, 600} {
		got := fullVsChunkedCallCount(dur, fullVsChunkedWindows)
		// 按被测循环的写法逐窗复算一遍（`st < dur-1e-6; st += w`）。
		want := 1
		for _, w := range fullVsChunkedWindows {
			if w >= dur {
				continue
			}
			for st := 0.0; st < dur-1e-6; st += w {
				want++
			}
		}
		if got != want {
			t.Errorf("时长 %.2fs：计数函数给出 %d，按循环复算是 %d ⇒ 两者不同形，预算会算错",
				dur, got, want)
		}
	}
}

// TestFullVsChunkedProbeKeepsSkipAndPartLossVisible —— §489.6：
// 分片探针**两处静默丢分片**的判据必须留在源码里。
//
// ⛔ 缺陷形态（§489.4 实测）：
//
//	① `if w >= dur { continue }` —— 整档跳过，而读数里**没有这一档存在的痕迹**
//	② `tx()` 失败返空串 + `if p != ""` —— 失败的片被滤掉，`parts` 少几片
//	   而末尾照旧打印「与基准一致? false」
//
// ⇒ **读者会把「片丢了」读成「分片更差」** —— 而这条探针是
// §478「切得越碎损得越狠」的**唯一量具**。
//
// ⚠ 为什么这道门能离线断：三条判据都是**源码形状**，
//
//	不需要网关、不需要音频 —— 与本文件其余三条同源。
//
// ⚠ 它与 `internal/livetest` 的接线门**分工明确**：
// 那边断「账本接了没、Report 在不在读数之前」，**不管探针内部丢没丢片**；
// 这边断「丢片有没有出声」。⇒ 两边都要，缺一即漏。
func TestFullVsChunkedProbeKeepsSkipAndPartLossVisible(t *testing.T) {
	src, err := os.ReadFile("live_full_vs_chunked_probe_test.go")
	if err != nil {
		t.Fatalf("读探针源码失败: %v", err)
	}
	s := string(src)
	// ⚠ 2026-10-17：这里原先调用的是 §467 台账文件里的 `stripGoComments`。
	//	§775 普查轮把那套手写扫描器换成 go/parser 后该函数随之消失，
	//	本文件编译失败才暴露出来 —— 这是「同一份逻辑散在多处」的典型代价。
	//	同包 `dedup_threshold_sync_test.go` 里已有 `stripGoCommentsKeepLines`，
	//	语义相同且保留行数（这里只用 strings.Contains，行数无影响）⇒ 直接改用它。
	code := stripGoCommentsKeepLines(s) // 注释里的判据不算数（§467 同族）

	// ① 跳过整档必须有出声路径。
	//
	// ⚠⚠ 第一版写成 `if 存在 continue { 且 没有提示 }` —— **复合谓词**，实测 P1 变异抓不到：
	//
	//	变异只把 `skipped = append(...)` 换成裸 `continue`，
	//	而 `continue` 那行仍在 ⇒ 前半句仍为真，`跳过 %.1fs 档` 那行也仍在（它挪到了别处）
	//	⇒ 整条判据保持绿，而「静默跳档」确实已经发生。
	//
	// ⇒ 判别式：**复合谓词里只要有一半不由被保护的那件事决定，它就会失效**。
	//　 改成两条**各自独立**的断言：
	if !strings.Contains(code, "跳过 %.1fs 档") {
		t.Errorf("探针没有「跳过整档」的提示文本 ⇒ " +
			"「少跑了一档」与「就这几档」在读数里完全同形（§489.4）")
	}
	if !strings.Contains(code, "本轮**跳过 %d 档") {
		t.Errorf("探针没有把跳过的档位**汇总**出来 ⇒ 单档提示散落在循环里，读者很容易只看到最后几行（§489.4）")
	}
	if !strings.Contains(code, "skipped = append(skipped, w)") {
		t.Errorf("探针跳档时没有记录 skipped ⇒ 末尾的跳档汇总恒为 0，永远不报警")
	}

	// ② 期望片数 vs 实得片数必须被比对。
	if !strings.Contains(code, "wantParts := int(math.Ceil(dur / w))") {
		t.Errorf("探针没有算「期望片数」⇒ 无法区分「片丢了」与「分片更差」")
	}
	if !strings.Contains(code, "if len(parts) < wantParts {") {
		t.Errorf("探针没有比对实得片数与期望片数 ⇒ 静默丢片不会被发现（§489.4）")
	}

	// ③ 读数必须打印在账本对账**之后**（攒起来统一打印）。
	if !strings.Contains(code, "ledger.Report()") {
		t.Errorf("探针没有接完整性对账 ⇒ 被砍掉的读数与完整读数同形")
	}
	printAt := strings.Index(code, "ledger.Report()")
	readAt := strings.Index(code, "range tierLines {")
	if readAt < 0 {
		t.Errorf("找不到「统一打印读数」的循环 ⇒ §489.6 的读数被改回循环内直接打印了")
	} else if readAt < printAt {
		t.Errorf("读数打印循环在 ledger.Report() **之前**（第 %d vs 第 %d 字节）⇒ "+
			"不完整的分母已经先印出去了", readAt, printAt)
	}
}
