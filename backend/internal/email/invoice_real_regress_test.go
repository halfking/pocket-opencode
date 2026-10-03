package email

// invoice_real_regress_test.go — 用真实库里的实际样本钉住两个曾经出过问题的
// 判定，防止将来重新引入。
//
// 背景（2026-10-01 巡检真实库 7 条 email_invoices 时发现两类异常）：
//
//  1. seller = "name:"（2 条，QQ Wallet 英文发票）
//     邮件写的是 "Seller name: Tencent Cloud Computing Co Ltd"，旧正则
//     `Seller)[:：\s]*(...)` 匹配 "Seller" 后吃掉了空格，把标签词 "name:"
//     当成销售方。invoice.go:96-102 已修（正则改为允许 Seller(?:\s*Name)?），
//     本文件确认修复在真实原文上确实生效——历史上那两条是修复前的产物。
//
//  2. amount=0 却 status='downloaded'（2 条，主题「9 月度对账单」）
//     对账单根本不是发票，却被识别为发票并落盘成
//     「其他-财务部-0.00-2026-09-30.pdf」。这是**假阳性**，比漏判更糟：
//     它会进账本、进飞书汇总。用当前规则重跑真实库 7 条的结果是
//     hit=5（全是真发票）/ miss=2（两条对账单都被正确排除），
//     说明现存规则已不再产生假阳性，库里那两条同样是历史产物。
//
// 两个修复都在规则层，不在采集层：ExtractInvoice 的命中判定
// （invoiceKeywordHit）已不含「对账单」这类非发票词。这里把结论钉住，
// 免得有人放宽关键词后又把对账单放进来。

import "testing"

// 真实原文（inv_1790789580385036500_1 的 snippet 前段）
const realQQWalletBody = `Dear user, your electronic invoice has been issued.

Invoice number: 24317200000907012698
Total tax-inclusive amount: CNY 126.00
Seller name: Tencent Cloud Computing Co Ltd
Invoice details please see attachment (PDF).`

// 回归 1：「Seller name:」这种复合标签必须取到真正的公司名，
// 而不是把 "name:" 当值、更不能跨行吞掉下一句。
func TestRealQqWalletSellerName(t *testing.T) {
	e := Email{Subject: "[QQ Wallet] Electronic Invoice Issuance Notice", Snippet: realQQWalletBody}
	inv, ok := ExtractInvoice(e, realQQWalletBody)
	if !ok {
		t.Fatal("真实 QQ Wallet 发票未被识别")
	}
	if inv.Seller != "Tencent Cloud Computing Co Ltd" {
		t.Fatalf("Seller=%q —— 必须取到完整公司名，不能是 \"name:\"，也不能跨行吞下一句", inv.Seller)
	}
	if inv.Amount != 126.00 {
		t.Errorf("Amount=%v, want 126.00", inv.Amount)
	}
	if inv.Currency != "CNY" {
		t.Errorf("Currency=%q, want CNY", inv.Currency)
	}
	if inv.InvoiceNo != "24317200000907012698" {
		t.Errorf("InvoiceNo=%q", inv.InvoiceNo)
	}
}

// 回归 2：对账单 / 账单类邮件**不得**被识别为发票。
//
// 这两条曾在真实库里以 amount=0 + status=downloaded 的形态存在，
// 会往账本和飞书汇总里塞零金额垃圾行。
func TestRealStatementIsNotAnInvoice(t *testing.T) {
	cases := []struct{ name, subject, snippet string }{
		{
			"月度对账单",
			"9 月度对账单（附件 + 内嵌图表）",
			"9 月度对账单见附件，图表见正文。",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := Email{Subject: c.subject, Snippet: c.snippet}
			if inv, ok := ExtractInvoice(e, c.snippet); ok {
				t.Fatalf("对账单被误判为发票: amount=%v seller=%q —— "+
					"它会以「其他-…-0.00-….pdf」落盘并进入账本汇总", inv.Amount, inv.Seller)
			}
		})
	}
}
