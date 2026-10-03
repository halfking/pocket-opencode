package email

// 「债务通知被当成发票建档」的护栏。
//
// 2026-10-02 真实库实测（diag_real_invoice_extract_test.go 记录了金额侧，
// 本文件管准入侧）：
//
//	inv_1790903383222583800_1  amount=58000.00  invoice_date=2026-10-25
//	kind=bill  主题=中国工商银行客户对账单(ICBC Peony Card Bank Statement)
//
// 58000 是原文里的**信用额度**（由「兜底取全文最大值」选中），10-25 是
// **贷记卡到期还款日**。一笔根本没发生的 5.8 万元支出进了台账。
//
// 根因在准入门：invoiceKeywordHit 是**关键词存在性**检查，而关键词表本来
// 就含「账单」「对账单」「扣款」「支付成功」——信用卡对账单必然放行。
//
// 负控见文件末尾，实测转红。

import (
	"os"
	"strings"
	"testing"
)

// realICBCStatementSnippet 是生产库里那封邮件的**真实** snippet 片段
//（字段名与顺序照原样：还款日在前、账单周期与生成日在后）。
const realICBCStatementSnippet = "信 用 卡 对 账 单 尊敬的客户,您好! 感谢您使用工商银行信用卡，我行24小时服务专线95588竭诚为您服务。 " +
	"重要提示： 贷记卡到期还款日 2026年10月25日 账单周期 2026年09月01日—2026年09月30日 " +
	"对账单生成日 2026年09月30日 需 还 款 明 细 卡号后四位币种 应还款额 最低还款额信用额度 " +
	"9097(牡丹贷记卡)人民币(本位币)12,838.93/RMB1,605.56/RMB58,000.00/RMB"

func realICBCStatementEmail() Email {
	return Email{
		ID:           "em-1298896144-acct-x-5",
		AccountID:    "acct-x",
		Subject:      "中国工商银行客户对账单(ICBC Peony Card Bank Statement)",
		FromAddress:  "bill@icbc.com.cn",
		FromName:     "中国工商银行",
		Snippet:      realICBCStatementSnippet,
	}
}

func TestDebtNoticeStatement_IsNotAnInvoice(t *testing.T) {
	// 核心用例：这封邮件此前会建档，并从全文最大值里挑出 58,000 当金额。
	if _, hit := ExtractInvoice(realICBCStatementEmail(), ""); hit {
		t.Fatal("信用卡对账单不是发票，却仍被建档 —— 台账里会多出一笔 5.8 万元的假支出")
	}
	// 前置检查：确认它确实**本来**能过关键词门，否则本用例可能在守一个
	// 永远不会被触发的分支。
	if !invoiceKeywordHit(realICBCStatementEmail().Subject + "\n" + realICBCStatementSnippet) {
		t.Fatal("前置检查失败：这封邮件连关键词门都不过，说明样本已不复现原缺陷")
	}
	if !reDebtNoticeShape.MatchString(realICBCStatementEmail().Subject + "\n" + realICBCStatementSnippet) {
		t.Fatal("前置检查失败：债务通知形态判据没命中这封样本")
	}
}

func TestDebtNoticeStatement_KeptWhenItReallyCarriesAnInvoice(t *testing.T) {
	// 「账单周期」+ 真实发票号 —— 对账单里附了正式发票的情形，必须建档。
	withNo := realICBCStatementSnippet + " 发票号码 26332000008261110741"
	if !admitDebtNotice(withNo, false) {
		t.Fatal("带真实发票号的对账单被拒了 —— 收紧过头")
	}
	// 同一封带发票类附件（PDF）时也必须建档，交给采集器从附件补金额。
	if !admitDebtNotice(realICBCStatementSnippet, true) {
		t.Fatal("带发票类附件的对账单被拒了 —— 附件采集路径会因此失效")
	}
	// 发票号 + 税号也放行。
	if !admitDebtNotice(realICBCStatementSnippet+" 纳税人识别号 91310000MA1FL0P51K", false) {
		t.Fatal("带税号的对账单被拒了")
	}
}

// TestRealInvoicesSurviveTheGate 是**正控**：真实发票必须活着。
// 没有这条，一个「把准入门整个关掉」的实现也能让上面那些用例全绿。
func TestRealInvoicesSurviveTheGate(t *testing.T) {
	cases := []struct {
		name   string
		email  Email
		attach bool
	}{
		{
			name: "真实电子发票（生产库那封 3500）",
			email: Email{
				ID:          "em-1",
				Subject:     "您收到来自杭州创客家投资管理有限公司的发票，发票号码：26332000008261110741，金额：3500.00元，请注意查收！",
				FromAddress: "noreply@example.com",
				Snippet:     "您收到来自杭州创客家投资管理有限公司的发票，发票号码：26332000008261110741，金额：3500.00元",
			},
		},
		{
			name: "增值税普通发票（正文形态）",
			email: Email{
				ID:          "em-2",
				Subject:     "增值税电子普通发票",
				FromAddress: "fapiao@example.com",
				Snippet:     "发票号码 25312000000123456789 纳税人识别号 91310000MA1FL0P51K 价税合计：1280.00",
			},
		},
		{
			name: "账单邮件但金额只印在 PDF 附件里（既有放宽路径）",
			email: Email{
				ID:          "em-3",
				Subject:     "9 月度对账单",
				FromAddress: "billing@example.com",
				Snippet:     "本月账单明细见附件，请查收。",
			},
			attach: true,
		},
	}
	for _, c := range cases {
		if _, hit := ExtractInvoiceLoose(c.email, "", c.attach); !hit {
			t.Errorf("%s：真实发票被准入门拦下了 —— 这是收紧过头", c.name)
		}
	}
}

// TestNonStatementEmailsAreUnaffected 钉住「只对债务通知收紧」。
// 这些是准入门放行、但在金额门槛处被丢弃的 5 封真实邮件
// （diag_real_invoice_gate_test.go 逐个复核过，0 封是真发票）。
func TestNonStatementEmailsAreUnaffected(t *testing.T) {
	notDebt := []string{
		"您的账户已扣款 100.00 元，感谢使用",
		"订单确认：您的订单已发货",
		"本月服务账单已生成，请在 App 内查看",
		"Payment received. Thank you for your business.",
	}
	for _, s := range notDebt {
		if reDebtNoticeShape.MatchString(s) {
			t.Errorf("「%s」被误判成债务通知形态 —— 判据过宽会连带影响非对账单邮件", s)
		}
		if !admitDebtNotice(s, false) {
			t.Errorf("「%s」不是债务通知，准入行为不该改变", s)
		}
	}
}

func TestAdmitDebtNotice_DirectTable(t *testing.T) {
	cases := []struct {
		text   string
		attach bool
		want   bool
	}{
		{realICBCStatementSnippet, false, false},
		{realICBCStatementSnippet, true, true},
		{"信用卡对账单 账单周期 2026年09月", false, false},
		{"对账单 发票号码 12345678", false, true},
		{"Amount due 500.00 USD", false, false},
		{"Account statement 发票号码 87654321", false, true},
		{"Account statement", false, false}, // 匹配债务形态且无发票语义 → 拒
		{"本月服务账单已生成，请在 App 内查看", false, true}, // 只是提到「账单」，不是对账单形态
		{"增值税电子普通发票 价税合计 100.00", false, true},
	}
	for _, c := range cases {
		if got := admitDebtNotice(c.text, c.attach); got != c.want {
			t.Errorf("admitDebtNotice(%.60q, %v) = %v，期望 %v", c.text, c.attach, got, c.want)
		}
	}
}

// TestDebtNoticeGateIsActuallyWired 接线护栏：判据存在 ≠ 判据被用上。
func TestDebtNoticeGateIsActuallyWired(t *testing.T) {
	src, err := os.ReadFile("invoice.go")
	if err != nil {
		t.Fatalf("read invoice.go: %v", err)
	}
	if !strings.Contains(string(src), "if !admitDebtNotice(joined, hasInvoiceAttachment) {") {
		t.Fatal("ExtractInvoiceLoose 里没有调用 admitDebtNotice —— 判据写了但没接上")
	}
}

// ---------------------------------------------------------------------------
// 负控
// ---------------------------------------------------------------------------
// 1) 把 ExtractInvoiceLoose 里的 `if !admitDebtNotice(...)` 去掉 → 前两条转红
// 2) 把 admitDebtNotice 改成恒 true → 第一条转红
// 3) 把 admitDebtNotice 改成恒 false → TestRealInvoicesSurviveTheGate 转红
// 4) 把接线调用去掉 → TestDebtNoticeGateIsActuallyWired 转红
