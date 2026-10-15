package stt

import "testing"

// 本文件把 §612.2 登记的「后端基准」从**一次历史快照**变成**Go 侧常驻断言**。
//
// ## 它补的是哪个洞
//
// §612.2 的跨语言基准（TS 侧那条「renderTranscript 的输出与后端基准逐字相等」）
// 绑的是 **§455 那轮用真 mergeIncremental 量出来的字符串**，钉在**前端测试里**。
// §612.2.3 已如实登记它的限制：
//
//	「§612.2 的后端基准是一次历史快照，不是每次都重跑真函数比一次
//	  ⇒ Go 侧算法若变而那两条基准没变，TS 这层不会报警」
//
// 也就是说：**Go 侧漂移时，唯一会说话的是 TS 那个写死的字符串，
// 而它只能证明「TS 没变」，不能证明「Go 没变」** —— 后者当时**没有任何东西在管**。
// 本文件就是那个「管 Go 有没有变」的东西。
//
// ## 为什么必须两条断言一起写（防判据恒真）
//
// 这两条基准的期望值恰好等于 committed+next 的原样拼接，
// 因此**只写 `got == baseline` 是有风险的**：若有人把算法改成别的形状，
// 只要碰巧仍产出同一串就恒绿。故沿用 §612.2 的做法 ——
// **既断「等于后端基准」，又断「不等于修复前的 TS 输出」**。
//
// ## 语料出处
//
// §455 那轮的分叉样本，两条各带一个 `lcsLen < minFuzzyAnchor` 的对齐窗口
// （§455 补 minFuzzyAnchor 修的就是这一格，与 §608 的 `committed === next` 格不相交）。
const (
	// tsBeforeFix = 修复前前端 dedupeSegmentText 的实测输出
	tsBeforeFixCorpus1 = "甲云山云后续内容"
	tsBeforeFixCorpus2 = "甲乙丙云云后续内容"
)

func TestMergeIncrementalPinsCrossLangBaseline(t *testing.T) {
	cases := []struct {
		name      string
		committed string
		next      string
		baseline  string // §455 记录的 Go 真函数输出（§612.2 的「后端基准」）
		tsBefore  string // 修复前 TS 输出，用于反证判据非恒真
	}{
		{
			name:      "语料1 · 甲云山 + 云甲山后续内容",
			committed: "甲云山",
			next:      "云甲山后续内容",
			baseline:  "甲云山云甲山后续内容",
			tsBefore:  tsBeforeFixCorpus1,
		},
		{
			name:      "语料2 · 甲乙丙云 + 云甲乙丙后续内容",
			committed: "甲乙丙云",
			next:      "云甲乙丙后续内容",
			baseline:  "甲乙丙云云甲乙丙后续内容",
			tsBefore:  tsBeforeFixCorpus2,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergeIncremental(c.committed, c.next)

			// ① 主断言：Go 侧行为仍与 TS 侧绑定的那份基准逐字相等。
			//    Go 侧算法一旦改动，这里立刻红 —— 这正是原先缺失的报警。
			if got != c.baseline {
				t.Errorf("Go 侧行为已漂移，TS 侧 §612.2 绑定的基准随之过期：\n"+
					"  committed=%q next=%q\n  got      =%q\n  baseline =%q",
					c.committed, c.next, got, c.baseline)
			}

			// ② 反恒真：若某天算法退化成前端修复前那套（把重叠吃掉），
			//    ① 有可能因为巧合仍绿；这一条保证那时必红。
			if got == c.tsBefore {
				t.Errorf("判据恒真风险：Go 输出等于修复前 TS 输出 %q，说明两侧又分叉了", c.tsBefore)
			}
		})
	}
}
