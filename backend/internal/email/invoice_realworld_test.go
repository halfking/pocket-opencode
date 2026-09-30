package email

// 真实邮件形态回归（2026-09-30 实测，2026-10-01 合成化）。
//
// 夹具来自 QQ 邮箱一封真实发票邮件的**形态**，按 consolidation §5.2 要求，
// 公司名 / 发票号 / OSS 链接已换成合成值，结构与实测邮件逐行一致：
//
//	主题：您收到来自示例投资有限公司的发票，发票号码：12345678901234567890，
//	      金额：3500.00元，请注意查收！
//
// 实测缺陷两个（合成夹具保持同样的触发路径）：
//  1. `金额：3500.00元` 抽不出金额（reAmountTotal 的关键词表里没有「金额」），
//     结果 amount=0 —— 汇总金额直接少算，需求里的「汇总金额」就废了；
//  2. 销售方抽成 "dzfp"（发票平台域名片段），不是「销售方名称」行里的公司名。
//
// 这两个都不是「规则没覆盖到」的小事：金额为 0 会让共享台账的合计行失真。

import "testing"

const realInvoiceSubject = "您收到来自示例投资有限公司的发票，发票号码：12345678901234567890，金额：3500.00元，请注意查收！"

// 电子发票平台邮件的典型正文片段（用于复现 seller=域名片段）。
const realInvoiceSnippet = `------=_Part_397111_1624436759.1790214518883
Content-Type: multipart/alternative; boundary="----=_Part_397110_1060649035.1790214518883"

------=_Part_397110
Content-Type: text/plain; charset=GBK

尊敬的用户：
您已成功开具电子发票，发票信息如下：
发票号码：12345678901234567890
开票日期：2026-05-24
销售方名称：示例投资有限公司
价税合计：￥3500.00
发票链接：https://dzfp-oss.example.com/invoice/12345678901234567890.pdf`

// 真实 IMAP 路径下 envelope 只带主题 + BODY[TEXT] 的**头部**摘要（正文没被拉下来），
// 线上实测就是这一种：amount=0、seller 抽成域名片段。
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
		FromAddress: "noreply@service.dzfp.example.com",
	}
	inv, hit := ExtractInvoice(e, "")
	if !hit {
		t.Fatal("real invoice mail must be recognized")
	}
	if inv.InvoiceNo != "12345678901234567890" {
		t.Fatalf("invoice no = %q", inv.InvoiceNo)
	}
	if inv.Amount != 3500.00 {
		t.Fatalf("amount = %v, want 3500.00（主题里写的是「金额：3500.00元」）", inv.Amount)
	}
	if inv.Seller != "示例投资有限公司" {
		t.Fatalf("seller = %q, want 示例投资有限公司（线上抽成了域名片段）", inv.Seller)
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
