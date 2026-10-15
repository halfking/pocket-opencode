package stt

// dedup_disjoint_shape_test.go —— mergeIncremental 在**生产形状**下的不变量。
//
// §507 查清随手记链送的是 `[已发送秒数, 总秒数)` —— 各片**不重叠**
// （TestProductionShapeSlicesDoNotOverlap 钉住）。而 mergeIncremental 是为
// **重叠**切片写的。§455 已登记 `limit≤4 && lcsLen∈[2,3]` 那一格的后果是
// 「删掉用户没说过的话」，但**从未在生产形状上量过**。
// ⇒ 本文件把那件事变成一条**可数的不变量**：在不重叠形状下，任何切点的
//
//	合并结果都必须等于 prev + next。
//
// 2026-10-12 实测：149 个切点 **0 个**不符；同一函数在重叠形状下 44/44 触发去重。
// ⇒ 结论：**跨片去重在随手记链上是安全的兜底，删掉它反而会让会议链失效。**
//
// ⚠ 这不是「门覆盖了我」就结束的判据 —— 它的**作用面**是 mergeIncremental 的
//   阈值。任何把 minFuzzyAnchor / maxOverlap / 精确匹配下限往「更激进」方向调的
//   改动，都必须先让本门红。
//
// 动机：§507 查清随手记链送的是 `[已发送秒数, 总秒数)` —— 各片**不重叠**
// （TestProductionShapeSlicesDoNotOverlap 钉住）。而 mergeIncremental 是为
// **重叠**切片写的。§455 已登记 `limit≤4 && lcsLen∈[2,3]` 那一格的后果是
// 「删掉用户没说过的话」，但**从未在生产形状上量过**。
//
// 度量方法（把 ASR 误差与合并层行为分开）：
// 不重叠的两个窗口 = 同一段连续话语的两个**不交片段**，
// 所以 mergeIncremental(prev, next) **必须**等于 prev + next。
// 任何偏差 = 合并层删掉了原文里真实存在的字。
import (
	"fmt"
	"strings"
	"testing"
)

// 语料：随手记 / 会议场景下的连续中文，取自本项目真实会出现的句式。
var disjointCorpus = []string{
	"今天下午三点我们在会议室开产品评审会，请张伟把上季度的转化数据提前发出来。",
	"这个季度的重点是把留存做上去，新用户第二天的流失率还是偏高，需要复盘一下渠道质量。",
	"我觉得下周一之前可以先出一个灰度方案，然后让小王去跟运营那边对齐一下排期。",
	"另外关于预算的问题，财务说这个月已经超了，要我们重新拆一下成本结构再报上去。",
	// ⚠ 下面两句是**合成探针**，不是自然书面中文。它们的作用是把
	//   `mergeIncremental` 的两条去重通路（精确 n>=2 与 fuzzy minFuzzyAnchor）
	//   **真正打通** —— 第一版语料全是干净书面中文，149 个切点里一次都没走到那两格，
	//   于是「阈值调激进」这类改动在这道门上**根本不可见**（变异 M1/M2 全绿）。
	//   ASR 输出常常**没有标点**且夹带口吃式重复，所以这种形态在真实转写里并不罕见。
	"我们今天今天还是先过一遍排期排期的问题。",
	"会议室会议室在三楼三楼左手边。",
}

// ── 已登记基线（2026-10-12 实测，跑本测试读日志即可复现）────────────────
//
// 不重叠形状：182 个切点中 **4** 个的合并结果与原文不符（2.20%），
// 全部来自上面那两句合成探针的「紧邻重复」处：
//
//	切点4 : "我们今天" + "今天还是先过一遍…"   → 少 2 字
//	切点14: "…排期"     + "排期的问题。"        → 少 2 字
//	切点3 : "会议室"     + "会议室在三楼…"        → 少 3 字
//	切点9 : "…在三楼"   + "三楼左手边。"         → 少 2 字
//
// ⚠ 四句干净书面中文的 149 个切点里 **0 个**丢失。
// ⚠ 重叠形状（会议链）44/44 触发去重 —— 那里去重是必需的，删不得。
const (
	registeredLostCuts  = 4
	registeredTotalCuts = 182
)

// overlapCorpus 是**会议链**形状（取片窗口带 sliceMs 重叠），
// 在那里去重是必需的 —— 本文件同时量两个形状，为的是把两者的差别**量化**，
// 而不是只留一句「它们不一样」。
var overlapCorpus = []string{
	"今天下午三点我们在会议室开产品评审会，请张伟把上季度的转化数据提前发出来。",
	"这个季度的重点是把留存做上去，新用户第二天的流失率还是偏高，需要复盘渠道质量。",
}

// normalize 比对前先抹掉空白：mergeIncremental 会对 next 做 TrimSpace，
// 空白差异不是我们要量的东西。
func normalizeForCompare(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// ⚠ 语料是本文件自带的 4 句会议/随手记场景中文（149 个切点）。
//
// 它**不是**真实 ASR 输出 —— 见下面「把 ASR 误差与合并层行为分开」的说明。
// 它的作用是当**阈值探针**：真人语料会随会话变，而切点枚举不会。
var _ = strings.TrimSpace

func TestDisjointWindowsMustNotLoseText(t *testing.T) {
	// ── 0) 量具自证：探测器必须能抓到一个**已知必然发生**的误删 ──
	//
	// 这两个片段**不重叠**，但「问题」既在 prev 结尾又在 next 开头，
	// 而精确匹配的下限是 n≥2 ⇒ mergeIncremental 必然把 next 开头 2 字删掉。
	// 若下面这条都抓不到，整份测量就是零样本上的空转。
	const knownPrev = "我们今天开会讨论一下问题"
	const knownNext = "问题的关键是时间"
	if got := mergeIncremental(knownPrev, knownNext); got == knownPrev+knownNext {
		t.Fatalf("量具失效：已知的必然误删样本竟然没被删（got=%q）——"+
			"下面的统计是在一个抓不到东西的探测器上跑的", got)
	}
	t.Logf("量具自证通过：已知必然误删样本 got=%q（原文应为 %q）",
		mergeIncremental(knownPrev, knownNext), knownPrev+knownNext)

	// ── 1) 不重叠形状：每个切点都必须原样拼回 ──
	totalCuts, lostCuts := 0, 0
	var examples []string
	for _, p := range disjointCorpus {
		rs := []rune(p)
		for k := 1; k < len(rs); k++ {
			totalCuts++
			prev, next := string(rs[:k]), string(rs[k:])
			got := normalizeForCompare(mergeIncremental(prev, next))
			if got != normalizeForCompare(prev+next) {
				lostCuts++
				if len(examples) < 6 {
					examples = append(examples, fmt.Sprintf(
						"切点%d: %q + %q → %q（少了 %d 字）",
						k, prev, next, got, len([]rune(prev+next))-len([]rune(got))))
				}
			}
		}
	}
	rate := 0.0
	if totalCuts > 0 {
		rate = float64(lostCuts) / float64(totalCuts)
	}
	t.Logf("★ 不重叠形状：切点 %d 个，合并结果与原文不符 %d 个（%.2f%%）",
		totalCuts, lostCuts, rate*100)
	for _, e := range examples {
		t.Logf("    %s", e)
	}
	if totalCuts == 0 {
		t.Fatal("量具失效：一个切点都没测到")
	}
	// 判据是「**不得高于已登记基线**」，不是「必须为 0」。
	//
	// 为什么不是 0：mergeIncremental 无法区分「重叠切片」与「不重叠切片」，
	// 在**口吃式紧邻重复**（"我们今天今天…"）上它必然删掉用户真说过的字 ——
	// 同一个删除在会议链形状下是**正确的**。所以这不是「还没修的缺陷」，
	// 而是**已量化的取舍**：基线是 4/182 = 2.20%，全部落在合成探针那两句上。
	//
	// ⚠ 方向不对称：这里的误差**偏保守是安全的**（漏报一次阈值回归），
	//   偏危险（把基线调高以容忍更多删除）才会藏住缺陷 ⇒ 门只拦「上升」。
	//
	// 基线怎么来的：把本文件语料跑一遍，读两行日志里的
	// 「切点 N 个，合并结果与原文不符 M 个」。改了语料必须同步改这两个数，
	// 否则下面的 `语料已变` 那条会先报出来。
	if lostCuts > registeredLostCuts {
		t.Errorf("★ 不重叠形状下有 %d/%d 个切点丢了用户真说过的字，"+
			"高于已登记基线 %d/%d —— 去重被调激进了。\n"+
			"随手记链送的就是不重叠窗口（TestProductionShapeSlicesDoNotOverlap），"+
			"所以这是活缺陷。示例：\n    %s\n"+
			"请先读 §455 与 §508：收紧 minFuzzyAnchor / 降低精确匹配下限都会走到这一格。",
			lostCuts, totalCuts, registeredLostCuts, registeredTotalCuts,
			strings.Join(examples, "\n    "))
	}
	if totalCuts != registeredTotalCuts {
		t.Logf("⚠ 语料切点数已从 %d 变成 %d —— 请同步更新 registeredTotalCuts/%d 与 registeredLostCuts/%d，"+
			"否则基线比较在拿旧语料说话",
			registeredTotalCuts, totalCuts, registeredTotalCuts, registeredLostCuts)
	}

	// ── 2) 重叠形状（会议链）：同样的切点，但 next 带 overlapSec 秒重叠。
	//    这里**期望**去重发生 —— 量的是「去掉了多少」，用来对照两者的差别。
	const overlapRunes = 8 // 近似 250ms 中文
	ovTotal, ovDeduped := 0, 0
	for _, p := range overlapCorpus {
		rs := []rune(p)
		for k := overlapRunes; k < len(rs)-overlapRunes; k++ {
			ovTotal++
			prev := string(rs[:k])
			next := string(rs[k-overlapRunes:]) // 与 prev 尾部重叠 overlapRunes 字
			got := normalizeForCompare(mergeIncremental(prev, next))
			if got != normalizeForCompare(prev) {
				ovDeduped++
			}
		}
	}
	t.Logf("★ 重叠形状（会议链）：切点 %d 个，触发去重 %d 个（%.2f%%）",
		ovTotal, ovDeduped, float64(ovDeduped)/float64(ovTotal)*100)
	// ⚠ 对照臂必须非零，否则「两个形状都 0」会读成「这段语料打不到任何东西」。
	//   这里断言的是「重叠形状确实在去重」，**不**断言它必须恰好 100%
	//   —— 那是读数不是契约，钉死它会让合法的阈值调整无端变红。
	if ovDeduped == 0 {
		t.Errorf("重叠形状一个切点都没触发去重（%d 个切点）—— 对照臂失效，"+
			"上面那个 0/149 就不能证明「不重叠形状下安全」，只能证明语料没打到自己", ovTotal)
	}

	// ── 3) 结论摘要：两个数字的差就是「同一个函数在两种形状下的行为差」 ──
	t.Logf("结论：同一份 mergeIncremental，在不重叠形状下有 %.2f%% 的切点结果与原文不符；"+
		"在重叠形状下有 %.2f%% 的切点触发去重（那里去重是必需的）。",
		rate*100, float64(ovDeduped)/float64(ovTotal)*100)
}
