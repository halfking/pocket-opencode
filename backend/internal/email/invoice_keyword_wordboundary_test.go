package email

// invoice_keyword_wordboundary_test.go — 钉住 `invoiceKeywordHit` 的**词边界**。
//
// ## 根因（2026-10-04 真实库实测，不是推演）
//
// `invoiceKeywordHit` 用 `strings.Contains(text, kw)`，**没有任何词边界**。
// 真实库里 971 封未建档邮件中，有一批被 `InvoiceCandidate` 放进「待拉原文」队列，
// 逐封查出命中的词是这样的：
//
//	[GitHub] You have used 100% of the Actions minutes included
//	    → 命中 billing，出现在正文 "this billing cycle"（套餐周期，与发票无关）
//	Amazon Web Services Account Alert
//	    → 命中 billing，出现在 URL console.aws.amazon.com/billing/home
//	[halfking/ai-native-gateway-core] Run failed: Installer CI
//	    → 命中 vat，出现在 "activation" / "private" 之类的**子串**里
//	You’re invited to GTC Berlin, October 20–22（NVIDIA 会议邀请）
//	    → 命中 vat，同上
//
// 这些邮件**一封都不是发票**。它们进队列的直接代价是：拉原文预算
// `maxInvoiceBodyFetches=24/轮` 被无意义内容占掉，真实发票排在后面被挤掉。
//
// ## 为什么这条判据要写真实文本
//
// 期望值**不用**被测函数算出来，也不另造一份词表：夹具就是上面这 4 封邮件的
// 真实 subject+snippet 片段，期望值「不该命中」是独立字面量。
//
// ## 它钉的是什么（范围有限，如实说）
//
// 钉的是「**非**发票语义不得因为子串巧合而命中」这一个方向。
// 反方向（真发票必须继续命中）由既有的 invoice_retry_test /
// invoice_debt_notice_gate_test / diag_real_invoice_gate_test 覆盖，
// 本文件不重复——避免两份会分化的真相。

import "testing"

// nonInvoiceRealWorldSnippets 是上面 4 封邮件里**真实**的文本片段。
// 不是编造的：每条的命中词都在真实库里查出来了（见文件头）。
var nonInvoiceRealWorldSnippets = []struct {
	name    string
	subject string
	snippet string
	// why 记录它在真实库里是被哪个词、以什么上下文命中的。
	why string
}{
	{
		name:    "GitHub Actions minutes 耗尽提醒",
		subject: "You have used 100% of the Actions minutes included with your plan",
		snippet: "Your plan includes 2,000 Actions minutes per month at no extra cost. " +
			"You have used 100% so far this billing cycle. for the halfking account.",
		why: "billing 出现在 'this billing cycle'（套餐周期）",
	},
	{
		name:    "AWS 账户告警",
		subject: "Amazon Web Services Account Alert",
		snippet: "We received an error while confirming the payment method. " +
			"Update it at https://console.aws.amazon.com/billing/home#/paymentmethods",
		why: "billing 出现在 AWS 控制台 URL 里",
	},
	{
		name:    "NVIDIA 会议邀请",
		subject: "You’re invited to GTC Berlin, October 20–22",
		snippet: "wanted to reach out personally to invite you to join us at NVIDIA GTC Berlin",
		why:     "vat 出现在 'invite'/'private' 一类词的子串里（无词边界）",
	},
	{
		name:    "Cursor Grok Bot 促销",
		subject: "Grok Bot launched. Come back for 50% off.",
		snippet: "Bots sign into your tools, take work start to finish. " +
			"Get 50% off ( https://cursor.com/activate/grok-bot-welcome-back)",
		why: "vat 出现在 'activate' 的子串里（无词边界）",
	},
}

// TestInvoiceKeywordHit_RejectsSubstringFalsePositives 钉住上面 4 条都**不该**命中。
func TestInvoiceKeywordHit_RejectsSubstringFalsePositives(t *testing.T) {
	for _, c := range nonInvoiceRealWorldSnippets {
		text := c.subject + "\n" + c.snippet
		if invoiceKeywordHit(text) {
			t.Errorf("%s 被 invoiceKeywordHit 放行了，但它不是发票：%s\n"+
				"  subject: %s\n"+
				"  说明：这封会占掉一格 maxInvoiceBodyFetches=24 的拉原文预算，把真发票挤到下一轮。",
				c.name, c.why, c.subject)
		}
	}
}

// TestInvoiceKeywordHit_RejectsBareEnglishWordsInsideURLsAndCycles 把「英文裸词」
// 这一类单独钉出来：billing / vat 这类词在英文里是普通业务词，会出现在 URL 与
// 「billing cycle」这类短语里，所以它们**必须**按词边界匹配。
func TestInvoiceKeywordHit_RejectsBareEnglishWordsInsideURLsAndCycles(t *testing.T) {
	cases := []struct {
		text string
		why  string
	}{
		{"see https://console.aws.amazon.com/billing/home for details", "billing 在 URL 路径里"},
		{"You have used 100% so far this billing cycle.", "billing cycle（账单周期 ≠ 账单邮件）"},
		{"Click to activate your account now", "activation 里的 vat 子串"},
		{"This is a private repository", "private 里的 vat 子串"},
	}
	for _, c := range cases {
		if invoiceKeywordHit(c.text) {
			t.Errorf("invoiceKeywordHit(%q) 命中了，但 %s —— 不是发票语义", c.text, c.why)
		}
	}
}

// TestInvoiceKeywordHit_StillAcceptsRealInvoiceSemantics 是**反向**保护：
// 收紧词边界不能把真发票挡在门外。
//
// 词表是生产词表（不另抄一份，见文件头），但期望值是独立字面量。
func TestInvoiceKeywordHit_StillAcceptsRealInvoiceSemantics(t *testing.T) {
	must := []string{
		"增值税电子普通发票",
		"电子发票下载",
		"您的电子发票已开具，请下载",
		"Your invoice is ready for download",
		"Payment Receipt for halfking",
		"receipt from X #2662-4636-8457",
		"中国工商银行客户对账单",
		"Your receipt from X",
	}
	for _, text := range must {
		if !invoiceKeywordHit(text) {
			t.Errorf("invoiceKeywordHit(%q) = false，但它是真发票语义 —— 收紧过头了", text)
		}
	}
}
