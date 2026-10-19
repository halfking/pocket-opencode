// 门禁：精校稿被**缩写**时必须回落原文（§907/D76）。
//
// 为什么要有这道门（2026-10-19 追链追出来的）
// -------------------------------------------
// 这条链的职责是**纠错**：`buildRefinePrompt` 要求模型原样回显整篇转写、
// 只改识别错误。而 §901 补充用真实数据校准出「271 字/分钟」⇒
// 一场 4 小时会议 ≈ 65,040 字 ≈ **65k token 的输出**，远超常见 8k/16k 上限
// ⇒ 长会议上模型**必然**做不到原样回显，它会**缩写**。
//
// 缩写为什么严重：前端 `ingestMeetingArtifacts` 用
// `refine.refinedTranscript || meeting.transcript` 建笔记
// ⇒ **缩写稿成了笔记正文** ⇒ §898 起那套段级向量索引到的
//
//	也就只有被缩写后的那一部分 ⇒ **长会议尾部从未被索引过**，且零诊断。
//
// 修之前 `guardRefineResult` 只有 `participantLeak` 一条检查 ⇒ 缩写是全漏的。
// 已有的 `isTruncatedOutput` 只拦得住 `finish_reason=length` 那一种，
// **缩写式回落不在其中** —— 它不是截断，是模型「乖乖地」写了份摘要。
//
// ⚠ 本文件是 `go test` 跑的，**不在** `npm run gates` 里；
//
//	CI 侧由 `.github/workflows/backend.yml` 执行。
package server

import (
	"strings"
	"testing"
)

func parsedRefine(s string) map[string]any {
	return map[string]any{"refined_transcript": s}
}

// 真实形状的转写：会议链是 `[说话人] 文本` 每段一行（§900 实测空行率 0%）。
func meetingTranscript(lines int, perLine int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("[发言人" + string(rune('一'+i%5)) + "] ")
		b.WriteString(strings.Repeat("测", perLine))
	}
	return b.String()
}

func TestRefineShrinkageRejectsAbbreviation(t *testing.T) {
	transcript := meetingTranscript(51, 60) // 真实量级：51 行
	cases := []struct {
		name  string
		ratio float64
		want  bool // 是否应当被拦下（回落原文）
	}{
		{"原样回显（理想）", 1.00, false},
		{"纠错后略长（加了注释）", 1.10, false},
		{"只去掉说话人前缀", 0.90, false}, // §907 实测最坏误伤形态 ≈0.908
		// ⚠ 阈值两侧取样，**不取正好 0.70**：`int(len*ratio)` 的整除截断会让
		//   「正好等于阈值」这个刀刃值随原文长度漂在阈值上下（3467 × 0.70 = 2426.9，
		//   而 int(3467×0.70) = 2426 ⇒ 落在下方被拒）。测边界就该测边界两侧。
		{"刀刃上方 0.71", 0.71, false},
		{"刀刃下方 0.69", 0.69, true},
		{"明显缩写：只剩 50%", 0.50, true},
		{"灾难性缩写：只剩 10%", 0.10, true},
		{"只剩一句话", 0.02, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refined := strings.Repeat("测", int(float64(len([]rune(transcript)))*c.ratio))
			_, rejected := guardRefineResult(parsedRefine(refined), transcript, meetingMetaIn{})
			if rejected != c.want {
				t.Fatalf("ratio=%.2f 时 rejected=%v，期望 %v", c.ratio, rejected, c.want)
			}
		})
	}
}

// 拦下时必须**回落原文**，且带 refine_rejected 标记 —— 否则前端会说「已精翻」，
// 点进去却是被缩写过的（与 §108 的「降级不能落 refined」是同一条纪律）。
func TestRefineShrinkageFallsBackToFullTranscript(t *testing.T) {
	transcript := meetingTranscript(51, 60)
	refined := strings.Repeat("测", len([]rune(transcript))/10) // 只回显 10%
	got, rejected := guardRefineResult(parsedRefine(refined), transcript, meetingMetaIn{})
	if !rejected {
		t.Fatal("缩写稿没有被拦下 ⇒ 长会议尾部会静默变成笔记正文")
	}
	if got["refine_rejected"] != true {
		t.Fatalf("回落时没有 refine_rejected 标记：%v", got["refine_rejected"])
	}
	fb, _ := got["refined_transcript"].(string)
	if fb != transcript {
		t.Fatalf("回落内容不是原文：回落 %d 字，原文 %d 字", len([]rune(fb)), len([]rune(transcript)))
	}
}

// ⚠ 反向：正常的纠错**不能**被误伤。
// 阈值算出来的误伤余量就是这条 —— 模型若把 `[姓名] ` 前缀全去掉（约 9%）仍然放行。
func TestRefineShrinkageDoesNotRejectPrefixStripping(t *testing.T) {
	// ⚠ 第一版这里用 `strings.Split(transcript, "] ")` 来「去掉前缀」——
	//   那个写法只去掉 2 个字符、**把名字 `[张三` 留下了**，量出来的比例是
	//   0.970 而不是真实形态的 0.908 ⇒ 量的是一个不存在的输出。
	//   ⇒ 直接按「有前缀 / 无前缀」两种形状**分别构造**，不去做字符串手术。
	const lines = 51
	const body = 60
	withPrefix := make([]string, 0, lines)
	noPrefix := make([]string, 0, lines)
	for i := 0; i < lines; i++ {
		bodyText := strings.Repeat("测", body)
		withPrefix = append(withPrefix, "[发言人"+string(rune('一'+i%5))+"] "+bodyText)
		noPrefix = append(noPrefix, bodyText)
	}
	transcript := strings.Join(withPrefix, "\n")
	stripped := strings.Join(noPrefix, "\n")
	ratio := float64(len([]rune(stripped))) / float64(len([]rune(transcript)))
	if ratio < 0.85 || ratio > 0.95 {
		t.Fatalf("夹具形状不对：去前缀后比例 %.3f，期望 0.85~0.95", ratio)
	}
	t.Logf("去掉全部说话人前缀后长度比例 = %.3f", ratio)
	if _, rejected := guardRefineResult(parsedRefine(stripped), transcript, meetingMetaIn{}); rejected {
		t.Fatalf("比例 %.3f 被判为缩写 ⇒ 阈值把「去掉说话人前缀」这种正常输出误伤了", ratio)
	}
}

// 空稿不放行也不拦：交给既有的空值分支（原样返回），不在本门职责内。
func TestRefineShrinkageEmptyTranscriptIsNotJudged(t *testing.T) {
	if reason := refineShrinkage("测", ""); reason != "" {
		t.Fatalf("空原文被判成缩写：%s", reason)
	}
}

// ⚠ 绝对下限：原文太短时比例没有意义，**不得判为缩写**。
// 这条是被既有判据撞出来的 —— `meeting_sync_id_upsert_test.go` 的夹具是
// 15 字原文 / 4 字精校稿的桩数据，比例判据在它身上没有判别力。
// 这里把它钉成显式行为，免得后来有人把下限删掉。
func TestRefineShrinkageIgnoresVeryShortTranscripts(t *testing.T) {
	short := "[甲] 开会"
	refined := "开会" // 明显更短，但原文只有 5 个 rune
	if reason := refineShrinkage(refined, short); reason != "" {
		t.Fatalf("只有 %d 个 rune 的原文被拿比例判了：%s", len([]rune(short)), reason)
	}
	if refineMinRunesForRatio <= 0 {
		t.Fatal("绝对下限必须为正")
	}
	// 下限之上就必须生效（这条同时钉住「下限不是无限大」）
	below := strings.Repeat("测", refineMinRunesForRatio-1)
	above := strings.Repeat("测", refineMinRunesForRatio)
	if refineShrinkage(below, below) != "" {
		t.Fatal("满长度的原文被误判")
	}
	if refineShrinkage("测", above) == "" {
		t.Fatalf("%d rune 的原文缩写成 1 个 rune 却没被判", refineMinRunesForRatio)
	}
}
