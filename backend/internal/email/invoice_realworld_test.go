package email

// 真实数据回归：QQ 邮箱里的真发票邮件（2026-09-30 实测）。
//
// 原文主题：
//
//	您收到来自杭州创客家投资管理有限公司的发票，发票号码：26332000008261110741，
//	金额：3500.00元，请注意查收！
//
// 实测缺陷两个：
//  1. `金额：3500.00元` 抽不出金额（reAmountTotal 的关键词表里没有「金额」），
//     结果 amount=0 —— 汇总金额直接少算，需求里的「汇总金额」就废了；
//  2. 销售方抽成 "dzfp"（发票平台域名片段），不是「杭州创客家投资管理有限公司」。
//
// 这两个都不是「规则没覆盖到」的小事：金额为 0 会让共享台账的合计行失真。

import "testing"

const realInvoiceSubject = "您收到来自杭州创客家投资管理有限公司的发票，发票号码：26332000008261110741，金额：3500.00元，请注意查收！"

// 支付宝电子发票平台邮件的典型正文片段（用于复现 seller=dzfp）。
const realInvoiceSnippet = `------=_Part_397111_1624436759.1790214518883
Content-Type: multipart/alternative; boundary="----=_Part_397110_1060649035.1790214518883"

------=_Part_397110
Content-Type: text/plain; charset=GBK

尊敬的用户：
您已成功开具电子发票，发票信息如下：
发票号码：26332000008261110741
开票日期：2026-05-24
销售方名称：杭州创客家投资管理有限公司
价税合计：￥3500.00
发票链接：https://dzfp-oss.oss-cn-hangzhou.aliyuncs.com/invoice/26332000008261110741.pdf`

// 真实 IMAP 路径下 envelope 只带主题 + BODY[TEXT] 的**头部**摘要（正文没被拉下来），
// 线上实测就是这一种：amount=0、seller="dzzp"。
const realInvoiceLiveSnippet = `------=_Part_397111_1624436759.1790214518883
Content-Type: multipart/alternative; boundary="----=_Part_397110_1060649035.1790214518883"

------=_Part_397110
Content-Type: text/plain; charset=GBK
Content-Transfer-Encoding: base64

LS0tLS0tLV9QYXJ0XzM5NzExMA==`

func TestRealInvoice_Amount3500(t *testing.T) {
	e := Email{
		ID:          "em-real",
		Subject:     realInvoiceSubject,
		Snippet:     realInvoiceLiveSnippet,
		FromAddress: "noreply@service.dzfp.com",
	}
	inv, hit := ExtractInvoice(e, "")
	if !hit {
		t.Fatal("real invoice mail must be recognized")
	}
	if inv.InvoiceNo != "26332000008261110741" {
		t.Fatalf("invoice no = %q", inv.InvoiceNo)
	}
	if inv.Amount != 3500.00 {
		t.Fatalf("amount = %v, want 3500.00（主题里写的是「金额：3500.00元」）", inv.Amount)
	}
	if inv.Seller != "杭州创客家投资管理有限公司" {
		t.Fatalf("seller = %q, want 杭州创客家投资管理有限公司（线上抽成了 dzfp）", inv.Seller)
	}
}

// 主题里没有金额、只有正文有「价税合计」时也要抽到（两种来源都要覆盖）。
func TestRealInvoice_AmountFromBodyOnly(t *testing.T) {
	e := Email{ID: "em-real-2", Subject: "您的电子发票已开具", Snippet: realInvoiceSnippet}
	inv, hit := ExtractInvoice(e, "")
	if !hit || inv.Amount != 3500.00 {
		t.Fatalf("body amount not extracted: hit=%v inv=%+v", hit, inv)
	}
}

// 「金额」不能引入误伤：不含数值的「金额」提示语不该造出金额。
func TestAmountKeyword_NoFalsePositiveOnProse(t *testing.T) {
	e := Email{ID: "em-prose", Subject: "您本月的金额已超出套餐额度，请及时充值", Snippet: "点击查看详情"}
	if inv, hit := ExtractInvoice(e, ""); hit && inv.Amount > 0 {
		t.Fatalf("prose must not produce an amount: %+v", inv)
	}
}
