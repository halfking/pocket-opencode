package server

import (
	"strings"
	"testing"
)

// §222：随手记总结的**回落信号**。
//
// 缺陷形态：解析失败 / summary 为空时，parseNoteSummaryPayload 回落成
// **模型原文**（content）。而那是一段非空、读起来像一段话的文本，
// 与真摘要在形态上**分不出来** ⇒ 调用方把它当真摘要 `updateNote({summary})`
// **写进库并持久化**，于是「抱歉，无法生成摘要」这类散文会长期占着
// 笔记的 AI 总结字段。
//
// ⚠ 与会议侧（§218/§220）的差异，决定了处置**不能**照抄：
//   会议侧回落是**原始转写**，内容对用户有意义但形态是错的 ⇒ 标记出来；
//   本路径回落是**模型原文**，其中「模型没听 JSON 指令、直接给了一段通顺的
//   纯文本总结」是**正当且有用**的形态（那正是 2026-10-06 改格式**之前**的行为）。
//   ⇒ 所以这里只**标记**，不换成空串、不改成报错。
//
// ⚠ 判据形态：纯函数，直接调（与 server_note_action_items_test.go 同款）。
// 调用点 11 个里多数是既有判据，它们钉的是「两值签名的老语义」——
// 那条**不能动**，所以老函数保留、只旁开一个带标志的出口。

func TestNoteSummaryFallbackFlagMatchesWhatItFallsBackTo(t *testing.T) {
	const prose = "好的，我来帮你整理这段内容。"
	const realSummary = "本季度要解决切段导致的专名丢失问题。"

	cases := []struct {
		name        string
		content     string
		wantFellBck bool
		wantSummary string
	}{
		// 阳性对照：形态完全正常时必须放行。
		// 没有这一条，「永远回落」这种把功能改瘫的变异也会判绿。
		{"正常摘要", `{"summary":"` + realSummary + `","action_items":[{"text":"把术语表接进摘要链"}]}`, false, realSummary},
		{"自由文本不是 JSON", prose, true, prose},
		{"summary 空串", `{"summary":"","action_items":[]}`, true, `{"summary":"","action_items":[]}`},
		{"summary 全空白", `{"summary":"  \n\t ","action_items":[]}`, true, `{"summary":"  \n\t ","action_items":[]}`},
		{"空响应", "", true, ""},
		{"截断的 JSON", `{"summary":"本季度要解决切段`, true, `{"summary":"本季度要解决切段`},
		{"拒绝式回应", "抱歉，我无法生成这段内容的总结。", true, "抱歉，我无法生成这段内容的总结。"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			summary, items, fellBack := parseNoteSummaryPayloadWithFallback(tc.content)
			if fellBack != tc.wantFellBck {
				t.Fatalf("fellBack=%v，期望 %v（原文=%q）", fellBack, tc.wantFellBck, tc.content)
			}
			if summary != tc.wantSummary {
				t.Fatalf("summary=%q，期望 %q", summary, tc.wantSummary)
			}
			// 回落形态**必须**仍带内容：这条路径存在的理由是
			//「模型给了通顺的纯文本」也是正当产出，换成空串是浪费。
			if fellBack && tc.content != "" && strings.TrimSpace(summary) == "" {
				t.Fatal("回落形态给了空串：模型原文可能被丢掉了")
			}
			// 任何形态下 action_items 都不许是 nil（调用方按切片迭代）。
			if items == nil {
				t.Fatal("action_items 是 nil 而非空切片")
			}
		})
	}
}

// TestNoteSummaryFallbackKeepsActionItemsOnlyOnRealSummary 钉住「一个字段的失败
// 不许让另一个字段一起丢」这条既定语义：真摘要即使 summary 之外的字段有瑕疵，
// 行动项仍要保留；而回落形态**没有**结构化字段可谈。
func TestNoteSummaryFallbackKeepsActionItemsOnlyOnRealSummary(t *testing.T) {
	// 正摘要 + 行动项 ⇒ 行动项必须在（它带 due，是「时间点进日程」的唯一来源）。
	_, items, fellBack := parseNoteSummaryPayloadWithFallback(
		`{"summary":"定了续期方案","action_items":[{"text":"发报价单","due":"下周三"},{"text":"  "}]}`)
	if fellBack {
		t.Fatal("正常响应被判成回落：标志会开始撒谎，而撒谎的标志比没有更坏")
	}
	if len(items) != 1 {
		t.Fatalf("行动项应剩 1 条（空 text 的那条被过滤），实得 %d 条", len(items))
	}
	if items[0].Due != "下周三" {
		t.Fatalf("due 丢了：%+v", items[0])
	}
}

// TestNoteSummaryTwoValueWrapperKeepsItsPinnedContract 钉住老签名没被顺手改掉。
//
// 老函数有 11 个调用点（多数是既有判据），它们钉的是「回落时给内容而不是报错」。
// 本次只旁开出口；若有人日后把老函数改成「回落时返回空串」，
// 那些判据可能仍然绿（它们多数只断内容，不断空串）——所以这里显式钉住。
func TestNoteSummaryTwoValueWrapperKeepsItsPinnedContract(t *testing.T) {
	for _, content := range []string{
		"这不是 JSON",
		`{"summary":""}`,
		`{"summary":"   "}`,
		"抱歉，我无法生成这段内容的总结。",
	} {
		summary, items := parseNoteSummaryPayload(content)
		if summary != content {
			t.Errorf("content=%q：老签名的回落语义变了（应原样回传 content），实得 %q", content, summary)
		}
		if len(items) != 0 {
			t.Errorf("content=%q：回落形态不该有行动项，实得 %d 条", content, len(items))
		}
	}
}
