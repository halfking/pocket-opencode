// full_segment_clamp_status_test.go — §121：**只读现状登记**，不是承重门。
//
// ⚠ 先读这段，否则容易把它当成「不许改」的禁令：
//
//	本文件登记的是**一个未决的产品取舍**，不是一条不变量。
//	要改段长，请连同 §121.3 的决策一起改，并把本文件改成登记新现状。
//
// ── 它登记什么 ─────────────────────────────────────────────────────
//
// 能力表 `target.go` 里登记的 `MaxSeconds` 与**真正生效**的切段长度
// 是**两件事**：后者一律被 `maxSegmentSec = 25` 压回。
//
//	模型登记的 MaxSeconds   实际生效段长
//	  60 (OpenRouter/MAI)        25
//	 500 (MiniMax)               25
//	 600 (MAI-1 系)              25
//	  30 (智谱)                  25
//
// 只看登记表会以为「MiniMax 能一次吃 500 秒」，而实际是 25。
//
// ★★ 更强的结论（变异 R2 逼出来的）：删掉钳制那两行，**行为完全不变**。
//
//	因为登记的 MaxSeconds 最小是 30，全部 > 25，而 defaultSegmentSec 本身就是 25
//	⇒ **「按模型能力决定段长」那段逻辑今天是彻底不起作用的**
//	（`segmentSec = min(25, MaxSeconds)`，而所有 MaxSeconds ≥ 30）。
//	它只有在「有人调低 defaultSegmentSec」或「加入一个 MaxSeconds < 25 的模型」
//	时才可能开始起作用。
//	⇒ 所以 §121 的结论要说得更准：那不是「一个全局上限压平了按能力的设计」
//	（那描述的是**意图**），而是**这套按能力分档的设计从未真正生效过**。
//
// ── 为什么必须逐档列出来 ───────────────────────────────────────────
//
// 写一句「会被钳制」读者不会当真。**逐档列出 30/60/500/600 → 25**
// 才能让「这不是笔误，是设计」这件事一眼可见。
//
// ★ 连带一条耦合（写在 transcribe.go 的 forcePlain 注释里）：
//
//	forcePlain 是**无条件**的，不看 audio 长度
//	⇒ **单独调大段长，对说话人标签没有任何效果**。
package stt

import (
	"testing"
)

func TestEffectiveSegmentLengthIsClampedToGlobalCap(t *testing.T) {
	// 量具自证：若 KnownMaxSeconds 恒返回 0，下面「登记值 > 25」这一档就空了，
	// 而整条断言会退化成「什么都没验」。
	var clamped int
	for _, o := range RecommendedModels() {
		if o.MaxSeconds > int(maxSegmentSec) {
			clamped++
		}
	}
	if clamped == 0 {
		t.Fatalf("量具自证失败：没有任何模型的 MaxSeconds 超过 maxSegmentSec(%v) —— "+
			"要么能力表变了，要么 KnownMaxSeconds 坏了，先查这里再改本文件",
			maxSegmentSec)
	}

	for _, o := range RecommendedModels() {
		got := effectiveSegmentSec(o.Model)
		if got != maxSegmentSec {
			t.Errorf("模型 %s 登记 MaxSeconds=%d，有效段长=%v —— "+
				"**现状已变**：要么能力表更新了，要么钳制逻辑改了。"+
				"若这是有意改动段长，请一并更新 §121 的结论（本文件是它的现状登记）。",
				o.Model, o.MaxSeconds, got)
		}
	}

	// 反向自证：钳制值必须真的等于常量 25，而不是碰巧。
	// 若有人把 maxSegmentSec 调到别的值而没想清楚后果，这条会先红。
	if maxSegmentSec != 25 {
		t.Errorf("maxSegmentSec = %v，已不是文档里写的 25 —— "+
			"请确认 §121.3 的取舍被重新评估过（它决定了单次失败会丢多少文字）",
			maxSegmentSec)
	}
}

// TestEffectiveSegmentLengthFallsBackToDefault —— 钳制的另一侧：**未登记上限**时取全局默认。
//
// ⚠ 第一版这条叫「短上限也该被采纳」，但**现有清单里没有任何 MaxSeconds < 25 的模型**
//
//	⇒ 那条断言永远绿，是一个恒真。本轮改成它真正测到的东西，
//	并把「登记值更小时应采纳它」如实记为**未覆盖的边界**（§121.4 的诚实写法）：
//	要真覆盖它，得先有一个 MaxSeconds < 25 的模型，而加那个模型本身是产品决定。
func TestEffectiveSegmentLengthFallsBackToDefault(t *testing.T) {
	for _, model := range []string{"", "nonexistent-model"} {
		if got := effectiveSegmentSec(model); got != float64(defaultSegmentSec) {
			t.Errorf("模型 %q 未登记上限，应取全局默认 %v，实际 %v", model, defaultSegmentSec, got)
		}
	}
	// 反向自证：这两者必须真的是同一个值，否则「默认」这个词没有意义。
	if defaultSegmentSec != 25 {
		t.Errorf("defaultSegmentSec = %v，已不是文档里写的 25（§121 引用的正是这个数）",
			defaultSegmentSec)
	}
}
