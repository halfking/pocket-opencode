// full_cross_segment_merge_status_test.go — §481：**只读现状登记**，不是承重门。
//
// ⚠ 先读这段，否则容易把它当成「不许加合并层」的禁令：
//
//	本文件登记的是**一个未决的产品取舍**（决策简报 #15 的 B 格），
//	不是一条不变量。**总有一天要给 `TranscribeFull` 补上跨段合并**——
//	那正是用户需求第一条「将不同的段放在一起进行校对合并」。
//	补上之后本文件会转红，那时的正确动作是：
//	把这里改成登记新现状，并**回头更新 #15 B 格与 §481**，而不是删掉这条判据。
//
// ── 它登记什么 ─────────────────────────────────────────────────────
//
// `TranscribeFull` 逐段转写完之后的聚合是：
//
//	full.go:445  for _, seg := range segments { ... transcribeFor(...) }
//	full.go:486  out.Text = strings.Join(texts, "\n")
//
// ⛔ **跨段没有任何合并/校对**——纯换行拼接。
// 段边界落在词中间时，两侧各自识别错一点，拼接处**各丢一半信息**，
// 而且用户看到的是「上一行结尾 + 下一行开头」这种明显不通顺的形态。
//
// ⚠⚠ 一条必须一起读的注释（`minSilenceCutSec` 处，`full.go:138-141`）：
//
//	「修复后 9 句话**每一句都完整落在单个返回段里**（覆盖率 100%、零丢失、零重复）」
//
// 说的是**切分阶段把句子对齐了**，**不是聚合阶段做了合并**。
// 这两件事在同一段注释里挨得很近，很容易被读成后者 ⇒ 这就是本文件存在的理由。
//
// ★ 与 §475 三层分析的分工：那一层讲的是「精校的**依据**不够」，
//
//	本文件讲的是「段与段之间**根本没被合起来**」。两者是不同的缺口。
package stt

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestFullPathJoinsSegmentsWithoutMerging 登记聚合形态。
//
// ⚠ 为什么是**源码形状**而不是行为断言：
//
//	`transcribeFor` 直接打真网关，仓里没有可注入的假实现，
//	所以「两段拼起来是什么样」在离线环境里**跑不出来**。
//	⇒ 只能读源码。这与 `cer_ascii_split_gate_test.go` 用同一口径，
//
//	并同样要求**正样本自证**（见下面两条）：断形状的判据若没有正样本，
//	就是恒真的空壳。
func TestFullPathJoinsSegmentsWithoutMerging(t *testing.T) {
	src, err := os.ReadFile("full.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	// ① 正样本自证：聚合那一行必须真的在（否则下面两条可能是恒假）。
	//
	// ⚠⚠ 这里的 `\\n` 必须是**反斜杠 + n 两个字符**，不是换行符：
	//
	//	Go 的 regexp 里 `\n` 匹配的是**换行字符**，
	//	而源码里那是 Go 字符串字面量 `"\n"`（反斜杠 n 两个字符）。
	//	⇒ 写成 `\n` 的判据恒假，且失败信息会长得像是「聚合形态已变」
	//	  —— 一个**自己错了**的判据，却报出**产品变了**的结论。
	//	★ 本轮就是这么踩到的（§481 同族：先 assert 锚点存在）。
	if !regexp.MustCompile(`strings\.Join\(texts,\s*"\\n"\)`).MatchString(s) {
		t.Fatalf("在 full.go 里找不到 `strings.Join(texts, \"\\n\")` —— " +
			"聚合形态已变。若这是有意补上跨段合并，请把本文件改成登记新现状，" +
			"并回头更新决策简报 #15 的 B 格与设计文档 §481" +
			"（若判定是本判据自己的锚点漂了，先修锚点，别急着改产品）")
	}

	// ② 聚合路径上**不许**出现任何去重/合并函数。
	//
	// ⚠ 只查这几个具名函数是有意的：列全所有「可能的合并函数」不可能，
	// 而 `mergeIncremental` 是仓里**唯一**的跨段去重实现（incremental.go:222），
	// 另有前端手抄的 `meeting-dedup.ts` 的等价物（跨语言，Go 侧不引）。
	// ⇒ 「Go 侧聚合没调它」就是「跨段零合并」的准确表述。
	for _, fn := range []string{"mergeIncremental", "fuzzyOverlapTail", "lcsAlign"} {
		if regexp.MustCompile(`(?m)^\s*\w*\s*:=?\s*` + fn + `\(`).MatchString(s) {
			t.Errorf("full.go 的聚合路径上出现了 %s —— "+
				"**现状已变**：跨段合并被接上了。请把本文件改成登记新现状，"+
				"并回头更新决策简报 #15 的 B 格与设计文档 §481", fn)
		}
	}

	// ③ 那条容易被读反的注释必须原样保留。
	//
	// ⚠ 为什么要守一句注释：它是被 §481 引用的证据之一，
	// 而它「说的是切分对齐、不是聚合合并」这件事**只存在于这句措辞里**。
	// 有人为了「说得更清楚」把它改写成「跨段已合并」时，这里必须先红。
	if !strings.Contains(s, "零丢失、") || !strings.Contains(s, "零重复") {
		t.Error("full.go 里那对「零丢失、零重复」的说法不见了 —— " +
			"若是被改写成了「跨段已合并」之类，请回头核对 §481：那句话说的是" +
			"**切分把句子对齐了**，不是聚合阶段做了合并")
	}
}

// TestUnregisteredModelUsesGlobalDefault 是 §481.5 那条事实的**自证**。
//
// ⚠ 这条断言本体**已经存在**于 `full_segment_clamp_status_test.go`
//
//	（`TestEffectiveSegmentLengthFallsBackToDefault` 覆盖了
//	 `effectiveSegmentSec("")` 与 `effectiveSegmentSec("nonexistent-model")`）。
//
//	说「发现」之前先查有没有门已守着 —— 所以这里**不再重复断言**，
//	只登记「我们线上真正在用的那个模型」也落在同一档里，
//	并把它写进日志（`mimo-v2.5-asr` 不在 `RecommendedModels()` 里 ⇒ 段长恒 25s）。
func TestLiveASRModelFallsBackToGlobalDefault(t *testing.T) {
	const liveModel = "mimo-v2.5-asr"
	if KnownMaxSeconds(liveModel) != 0 {
		t.Skipf("%s 已登记 MaxSeconds=%d ⇒ 本文件的前提已变，请更新 §481.5 的结论",
			liveModel, KnownMaxSeconds(liveModel))
	}
	got := effectiveSegmentSec(liveModel)
	if got != float64(defaultSegmentSec) {
		t.Errorf("%s 未登记上限，有效段长应为全局默认 %v，实际 %v",
			liveModel, defaultSegmentSec, got)
	}
	t.Logf("%s 未登记在 RecommendedModels() 里 ⇒ 有效段长 = %vs（与它实际能力无关）",
		liveModel, got)
}
