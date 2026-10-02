package email

// diag_amount_gap_test.go — 纯正则诊断：为什么两封「通行费电子发票」没进台账。
//
// ## 背景
//
// 需求 2 的台账实况（logs/zz-invoice-ledger-20261003.txt，只读）：
//
//	email_invoices 4 行，其中 1 行 downloaded+有文件（3500，真发票）
//	但 emails 表里有两封「通行费电子发票」（19.00 / 5.61，共 24.61）
//	LEFT JOIN email_invoices 匹配不到任何行 ⇒ **完全没建档**
//
// 真实流水线跑过（否则那 3 行 new 的 QQ Wallet 发票也不会在库里），
// 发票候选窗口是 90 天，这两封是 2026-09-14，在窗口内。所以是**被丢弃**，
// 不是没被扫到。
//
// ## 假设
//
// reAmountTotal（invoice.go:101）的结构是「标签 + 可选分隔符 + 币种 + 数字」，
// 标签与数字之间只允许 `[:：\s]*` 和「小写」/右括号。��而通行费的正文摘要写的是
//
//	发票金额共计19元
//	         ^^^^ 「共计」不在允许的分隔符集合里
//
// 若成立，ExtractInvoiceLoose 拿到的 Amount=0、InvoiceNo=""、
// hasInvoiceAttachment=false，命中 invoice.go:617 的丢弃门槛 ⇒ 不建档。
//
// ## 本文件的作用
//
// 把上面的假设变成可执行断言。**不修改任何生产正则**——改不改是产品决定。
// 无需数据库，纯函数。

import "testing"

func TestDiagAmountGapWhyTollsMissed(t *testing.T) {
	cases := []struct {
		name string
		text string
		note string
	}{
		{
			name: "通行费真实写法（疑似漏网）",
			text: "您账户下的浙AB59453于2026年09月14日在收费公路通行费电子发票服务平台——票根成功开具了1张发票，发票金额共计19元。",
			note: "标签「金额」与数字之间隔着「共计」",
		},
		{
			name: "通行费另一种金额写法（同样漏网）",
			text: "票根成功开具了1张发票，发票金额共计5.61元。",
			note: "同上",
		},
		{
			name: "对照组：标准写法（应命中）",
			text: "金额：19.00",
			note: "标签 + 冒号 + 数字",
		},
		{
			name: "对照组：价税合计（应命中）",
			text: "价税合计（小写）¥1280.00",
			note: "真实发票的常见写法",
		},
		{
			name: "对照组：工行对账单里的信用额度（误报来源之一）",
			text: "信用额度 58,000.00",
			note: "标签「额度」不在 reAmountTotal 词表里，所以金额来自别处的兜底",
		},
	}

	t.Logf("reAmountTotal = %s", reAmountTotal.String())
	t.Logf("")
	for _, c := range cases {
		m := reAmountTotal.FindStringSubmatch(c.text)
		got := ""
		if len(m) > 0 {
			// 捕获组：币种 + 数字
			got = m[len(m)-1]
			if len(m) >= 2 && m[len(m)-2] != "" {
				got = m[len(m)-2] + got
			}
		}
		status := "MISS"
		if got != "" {
			status = "HIT "
		}
		t.Logf("%s  %-34s  extracted=%q", status, c.name, got)
		t.Logf("      %s", c.text)
		t.Logf("      (%s)", c.note)
	}

	// 断言只针对"对照组必须命中"——那是正则本该做到的事。
	// 「通行费漏网」是**现象**，在这里只报告不断言：修不修是产品决定。
	for _, c := range cases {
		if c.name == "对照组：标准写法（应命中）" ||
			c.name == "对照组：价税合计（应命中）" {
			if !reAmountTotal.MatchString(c.text) {
				t.Errorf("对照组失配，诊断本身不可信：%q 应当命中 reAmountTotal", c.name)
			}
		}
	}
	if reAmountTotal.MatchString("发票金额共计19元") {
		t.Logf("结论：假设**不成立** —— reAmountTotal 能匹配「共计」写法。")
		t.Logf("那两封通行费没建档是别的原因，需另查（可能是有附件但未下载的路径问题）。")
	} else {
		t.Logf("结论：假设**成立** —— reAmountTotal 匹配不到「金额共计19元」。")
		t.Logf("两封通行费电子发票因此 Amount=0 + 无发票号 + 无附件凭证，")
		t.Logf("命中 invoice.go:617 丢弃门槛，需求 2 的台账漏掉 24.61 元真发票。")
	}
}
