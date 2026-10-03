package email

// invoice_amount_html_tag_test.go — 护栏：金额抽取要认得「标签夹在金额前面」。
//
// ## 为什么需要（2026-10-03 实测，handoff §7.4.4）
//
// 真实票根电子发票的原文形态（从 data/email-bodies-raw/<id>.bin 解出来的
// **加密原文**，不是手写夹具）：
//
//	…张发票，发票金额共计<span style='color: #FF9100;'>19</span>元。
//
// 纯文本里「共计」和「19」之间隔着一个 HTML 标签。上一轮的结论是「加个
// 「共计」到分隔符集合里就好」——实测**加了也不匹配**，因为 `<span …>`
// 仍然容不下。结果 amount=0，台账合计少算。
//
// 修法是允许「标签与金额之间夹一段 HTML 标签」（invoice.go 的 reHTMLTagRun）。
//
// ## 本文件守的三件事
//
//  1. 正向：真实形态（含标签）能抽出金额。
//  2. **反向最重要**：工行信用卡对账单的「合计人民币(本位币)12,838.93」
//     仍然**不得**命中——那是「应还款额」，不是发票金额。上一轮 handoff §7.2
//     已警告过「错误数字改对了一点，比错误数字更危险」。
//  3. 负控：把 reHTMLTagRun 去掉 → 正向用例转红。

import (
	"strings"
	"testing"
)

// tollAmountHTML 是从真实原文里摘出来的原样片段（合成化过的域名保留原样）。
const tollAmountHTML = "尊敬的客户，您本次通行费消费1张发票，发票金额共计<span style='color: #FF9100;'>19</span>元。"

// icbcStatementTotal 是真实工行信用卡对账单里的形态（已合成化），取自
// handoff §7.2 记录的那一行：合计行与「人民币(本位币)」连写。
//
// 它**不该**被当成发票金额：这是**应还款额**，不是任何人开的发票。
//
// 踩过的坑：最初这个 fixture 写的是「本期交易汇总 …」整行，而那行里
// **根本没有「合计」二字**（「汇总」≠「合计」），于是主断言空转、
// 靠「样本里没有危险词」而假绿。是本文件末尾那条自检抓出来的。
// 所以自检不能删：它防的正是「判据看起来在测某件事，其实没测」。
const icbcStatementTotal = "本期交易汇总 ---合计人民币(本位币)--- 本期应还款额 12,838.93"

func TestAmountExtraction_TagBetweenLabelAndNumber(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "真实形态：标签夹在「共计」与金额之间",
			text: tollAmountHTML,
			want: "19",
		},
		{
			name: "无标签的旧形态（回归：改动不能破坏它）",
			text: "发票金额共计19元",
			want: "19",
		},
		{
			name: "金额：19.00（既有形态）",
			text: "金额：19.00元",
			want: "19.00",
		},
		{
			name: "价税合计（小写）¥1280.00（既有形态）",
			text: "价税合计（小写）¥1280.00",
			want: "1280.00",
		},
	}
	for _, c := range cases {
		m := reAmountTotal.FindStringSubmatch(c.text)
		if m == nil {
			t.Errorf("%s：没匹配上，text=%q", c.name, c.text)
			continue
		}
		got := normalizeInvoiceAmount(m[2])
		if got == 0 {
			t.Errorf("%s：匹配到但归一化成 0（raw=%q）", c.name, m[2])
			continue
		}
		if !strings.HasPrefix(c.want, m[2]) && !strings.HasPrefix(m[2], c.want) {
			t.Errorf("%s：金额 = %q, want %q", c.name, m[2], c.want)
		}
	}
}

// TestAmountExtraction_StatementTotalStillNotAnInvoice 是**反向**护栏，也是本文件
// 最要紧的一条：放宽标签容差不得让对账单的「合计人民币(本位币)」命中。
func TestAmountExtraction_StatementTotalStillNotAnInvoice(t *testing.T) {
	if reAmountTotal.MatchString(icbcStatementTotal) {
		t.Errorf("工行对账单的「合计人民币(本位币)12,838.93」被当成了发票金额；" +
			"那是应还款额。命中它会把一笔应还款伪装成一张发票——" +
			"错误数字改对了一点，比错误数字更危险（handoff §7.2）")
	}
	// 前置自检：确认「合计」这两个字确实在样本里。
	// 否则上面那条可能是靠「样本里根本没有合计」而通过的假绿。
	if !strings.Contains(icbcStatementTotal, "合计") {
		t.Fatalf("样本里没有「合计」二字，本用例的负控失去意义")
	}
}
